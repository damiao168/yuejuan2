"""只为模板静态标牌提供有界 OCR，不向主进程导入另一套 Paddle 依赖。

子进程先后台预热；尚未就绪或发生故障时，本次识别保留原候选区域。
IPC 只返回固定四字的真实字框及内容数量，不返回任意识别文本。
"""

import atexit
import json
import math
import os
import signal
import socket
import struct
import subprocess
import sys
import tempfile
import threading
import time
from pathlib import Path

MAX_PNG_BYTES = 512 * 1024
MAX_LONG_EDGE = 1000
MAX_RESPONSE_BYTES = 64 * 1024
REQUEST_SECONDS = 3.0
WARMUP_SECONDS = 35.0
COOLDOWN_SECONDS = 30.0
_STATIC_CHARACTERS = frozenset("禁止作答")
_CHILD_REASON_CODES = frozenset({
    "TEMPLATE_STATIC_OCR_OK", "TEMPLATE_STATIC_OCR_INPUT_INVALID",
    "TEMPLATE_STATIC_OCR_INFERENCE_FAILED", "TEMPLATE_STATIC_OCR_WORD_BOX_MISSING",
    "TEMPLATE_STATIC_OCR_OUTPUT_INVALID",
})


def _result(status, reason_code):
    return {"status": status, "reason_code": reason_code, "characters": [],
            "recognized_character_count": 0, "recognized_block_count": 0}


def _png_dimensions(png):
    if (not isinstance(png, bytes) or not 33 <= len(png) <= MAX_PNG_BYTES
            or png[:8] != b"\x89PNG\r\n\x1a\n" or png[12:16] != b"IHDR"):
        return None
    width, height = struct.unpack("!II", png[16:24])
    return (width, height) if 0 < min(width, height) and max(width, height) <= MAX_LONG_EDGE else None


def _receive_exact(channel, size, deadline):
    parts = bytearray()
    while len(parts) < size:
        remaining = deadline - time.monotonic()
        if remaining <= 0:
            raise TimeoutError()
        channel.settimeout(remaining)
        block = channel.recv(size - len(parts))
        if not block:
            raise EOFError()
        parts.extend(block)
    return bytes(parts)


def _receive_frame(channel, deadline):
    length, = struct.unpack("!I", _receive_exact(channel, 4, deadline))
    if not 0 < length <= MAX_RESPONSE_BYTES:
        raise ValueError("invalid IPC frame")
    return _receive_exact(channel, length, deadline)


def _validated_result(value):
    """子进程也可能返回异常数据；只允许固定协议通过跨进程边界。"""
    if (not isinstance(value, dict) or value.get("status") not in {"ok", "failed"}
            or value.get("reason_code") not in _CHILD_REASON_CODES):
        raise ValueError("invalid OCR response")
    if value["status"] != "ok":
        return _result("failed", value["reason_code"])
    counts = [value.get("recognized_character_count"), value.get("recognized_block_count")]
    if any(type(count) is not int or not 0 <= count <= 4096 for count in counts):
        raise ValueError("invalid OCR count")
    entries = value.get("characters")
    if not isinstance(entries, list) or len(entries) > 256 or counts[0] < len(entries):
        raise ValueError("invalid OCR characters")
    clean = []
    for entry in entries:
        if (not isinstance(entry, dict) or entry.get("character") not in _STATIC_CHARACTERS
                or entry.get("confidence_scope") != "block"):
            raise ValueError("invalid OCR character")
        bbox, confidence = entry.get("bbox"), entry.get("confidence")
        if not isinstance(bbox, dict):
            raise TypeError("invalid OCR box")
        coordinates = [bbox.get(key) for key in ("x", "y", "width", "height")]
        if any(type(item) not in {int, float} or not math.isfinite(item) for item in [*coordinates, confidence]):
            raise ValueError("invalid OCR geometry")
        x, y, width, height = coordinates
        if not (0 <= x < 1 and 0 <= y < 1 and width > 0 and height > 0
                and x + width <= 1.000001 and y + height <= 1.000001 and 0 <= confidence <= 1):
            raise ValueError("invalid OCR geometry")
        clean.append({"character": entry["character"], "bbox": dict(zip(
            ("x", "y", "width", "height"), coordinates)),
            "confidence": confidence, "confidence_scope": "block"})
    return {"status": "ok", "reason_code": "TEMPLATE_STATIC_OCR_OK", "characters": clean,
            "recognized_character_count": counts[0], "recognized_block_count": counts[1]}


class TemplateStaticOCRRuntime:
    """一个进程最多保留一个隔离 OCR 子进程；同一时刻只推理一个裁区。"""

    def __init__(self, *, enabled=None):
        self.enabled = (os.environ.get("EDUGRADE_TEMPLATE_LOCAL_OCR_ENABLED", "false").lower()
                        in {"1", "true", "yes"}) if enabled is None else enabled
        self._state_lock = threading.Lock()
        self._request_lock = threading.Lock()
        self._state = "stopped"
        self._process = None
        self._channel = None
        self._cache_dir = None
        self._retry_at = 0.0
        self._closed = False

    def start(self):
        """只调度后台预热；模板请求不会等待约 20 秒的冷启动。"""
        with self._state_lock:
            if (not self.enabled or self._closed or self._state in {"warming", "ready"}
                    or time.monotonic() < self._retry_at):
                return
            self._state = "warming"
            try:
                threading.Thread(target=self._warmup, name="template-static-ocr", daemon=True).start()
            except (RuntimeError, OSError):
                # 线程资源不足也属于局部 OCR 不可用，不能让服务启动或模板请求一起失败。
                self._state = "cooldown"
                self._retry_at = time.monotonic() + COOLDOWN_SECONDS

    def _warmup(self):
        channel = child_channel = process = cache_dir = None
        try:
            if os.name != "posix" or not hasattr(socket, "AF_UNIX"):
                raise OSError("unsupported OCR process isolation")
            channel, child_channel = socket.socketpair(socket.AF_UNIX, socket.SOCK_STREAM)
            child = Path(__file__).with_name("template_analysis_ocr_child.py")
            # 非 root 镜像通常没有可写 home；缓存只写本次子进程专属临时目录。
            cache_dir = tempfile.TemporaryDirectory(prefix="template-static-ocr-", ignore_cleanup_errors=True)
            cache_root = Path(cache_dir.name)
            for name in ("paddlex", "mpl", "cache"):
                (cache_root / name).mkdir(mode=0o700)
            # -I -S 同时忽略继承的 PYTHONPATH 和主镜像 site-packages；只加载隔离依赖。
            env = {**os.environ, "PYTHONNOUSERSITE": "1", "PYTHONDONTWRITEBYTECODE": "1",
                   "OMP_NUM_THREADS": "1", "OPENBLAS_NUM_THREADS": "1", "MKL_NUM_THREADS": "1",
                   "PADDLE_PDX_DISABLE_MODEL_SOURCE_CHECK": "True", "FLAGS_log_level": "3"}
            env.update(PADDLE_PDX_CACHE_HOME=str(cache_root / "paddlex"),
                       MPLCONFIGDIR=str(cache_root / "mpl"), XDG_CACHE_HOME=str(cache_root / "cache"))
            env["LD_LIBRARY_PATH"] = "/opt/template-ocr/lib" + (
                ":" + env["LD_LIBRARY_PATH"] if env.get("LD_LIBRARY_PATH") else "")
            process = subprocess.Popen(
                [sys.executable, "-I", "-S", "-B", str(child), str(child_channel.fileno()), str(os.getpid())],
                env=env, pass_fds=(child_channel.fileno(),), start_new_session=True,
                stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
            )
            child_channel.close()
            child_channel = None
            with self._state_lock:
                if self._closed:
                    raise RuntimeError("closed")
                self._process, self._channel, self._cache_dir = process, channel, cache_dir
            ready = json.loads(_receive_frame(channel, time.monotonic() + WARMUP_SECONDS))
            if ready != {"status": "ready"}:
                raise ValueError("OCR warmup failed")
            with self._state_lock:
                if self._closed:
                    raise RuntimeError("closed")
                self._state = "ready"
            # Linux PDEATHSIG 跟随创建子进程的线程；预热后若直接返回，会误杀仍在服务的 OCR。
            # 此后台线程必须一直存活到子进程退出，页请求仍通过独立 IPC 调用，不等待这里。
            self._wait_for_child(process)
            return
        except (OSError, ValueError, EOFError, RuntimeError, TypeError):
            # 不回显异常内容，避免三方库把裁图文字或本机路径带入任务日志。
            with self._state_lock:
                self._state = "cooldown"
                self._retry_at = time.monotonic() + COOLDOWN_SECONDS
                self._process = self._channel = None
                self._cache_dir = None
            self._dispose(process, channel, cache_dir)
        finally:
            if child_channel is not None:
                child_channel.close()

    def _wait_for_child(self, process):
        """保持创建线程存活，并把空闲期间的子进程退出转为可重试的局部故障。"""
        try:
            process.wait()
        except OSError:
            pass
        with self._state_lock:
            # close 或请求超时可能已接管回收；旧进程不能改写其后的关闭/重试状态。
            if self._process is not process:
                return
            channel, cache_dir = self._channel, self._cache_dir
            self._process = self._channel = None
            self._cache_dir = None
            self._state = "cooldown"
            self._retry_at = time.monotonic() + COOLDOWN_SECONDS
        self._dispose(process, channel, cache_dir)

    @staticmethod
    def _finish_dispose(process, cache_dir):
        if process is not None:
            try:
                process.wait(timeout=.5)
            except (OSError, subprocess.TimeoutExpired):
                pass
        if cache_dir is not None:
            cache_dir.cleanup()

    @staticmethod
    def _dispose(process, channel, cache_dir=None, *, background=False):
        if channel is not None:
            channel.close()
        if process is not None:
            try:
                # Paddle 可能启动本地线程/辅助进程；超时清理整个独立进程组。
                if process.poll() is None:
                    os.killpg(process.pid, signal.SIGKILL)
            except OSError:
                pass
        if process is None and cache_dir is None:
            return
        if background:
            # 页级预算只等待推理；先断开并终止进程，再后台回收，避免额外占用最多 0.5 秒。
            try:
                reaper = threading.Thread(target=TemplateStaticOCRRuntime._finish_dispose,
                                          args=(process, cache_dir), name="template-static-ocr-reaper", daemon=True)
                reaper.start()
            except (RuntimeError, OSError):
                # 解释器退出或线程资源耗尽时仍不能让请求等回收；保留到退出阶段清理。
                atexit.register(TemplateStaticOCRRuntime._finish_dispose, process, cache_dir)
            return
        TemplateStaticOCRRuntime._finish_dispose(process, cache_dir)

    def analyze_crop(self, png_bytes, *, timeout=REQUEST_SECONDS):
        """只接受有界 PNG；忙、预热和失败均留给上层保留原候选。"""
        if not self.enabled:
            return _result("unavailable", "TEMPLATE_STATIC_OCR_DISABLED")
        if _png_dimensions(png_bytes) is None:
            return _result("failed", "TEMPLATE_STATIC_OCR_INPUT_INVALID")
        if type(timeout) not in {int, float} or not math.isfinite(timeout) or timeout <= 0:
            return _result("failed", "TEMPLATE_STATIC_OCR_INPUT_INVALID")
        self.start()
        with self._state_lock:
            state = self._state
            channel = self._channel
        if state != "ready":
            return _result("unavailable", "TEMPLATE_STATIC_OCR_WARMING" if state == "warming"
                           else "TEMPLATE_STATIC_OCR_UNAVAILABLE")
        if not self._request_lock.acquire(blocking=False):
            return _result("unavailable", "TEMPLATE_STATIC_OCR_BUSY")
        try:
            request_seconds = min(timeout, REQUEST_SECONDS)
            deadline = time.monotonic() + request_seconds
            channel.settimeout(request_seconds)
            channel.sendall(struct.pack("!I", len(png_bytes)) + png_bytes)
            response = _validated_result(json.loads(_receive_frame(channel, deadline)))
            return response
        except (OSError, ValueError, EOFError, RuntimeError, TypeError) as exc:
            with self._state_lock:
                process, active_channel = self._process, self._channel
                cache_dir = self._cache_dir
                self._process = self._channel = None
                self._cache_dir = None
                self._state = "cooldown"
                self._retry_at = time.monotonic() + COOLDOWN_SECONDS
            self._dispose(process, active_channel, cache_dir, background=True)
            return _result("failed", "TEMPLATE_STATIC_OCR_TIMEOUT" if isinstance(exc, TimeoutError)
                           else "TEMPLATE_STATIC_OCR_PROTOCOL_FAILED")
        finally:
            self._request_lock.release()

    def close(self):
        """关闭后不再重启；正常退出和后台预热并发时也要回收子进程。"""
        with self._state_lock:
            self._closed = True
            self._state = "closed"
            process, channel = self._process, self._channel
            cache_dir = self._cache_dir
            self._process = self._channel = None
            self._cache_dir = None
        self._dispose(process, channel, cache_dir)


_runtime = TemplateStaticOCRRuntime()
atexit.register(_runtime.close)


def get_template_static_ocr_runtime():
    """返回当前服务进程唯一运行时；服务启动时可显式调用 start 预热。"""
    return _runtime
