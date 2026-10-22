"""隔离 Paddle 执行入口；标准输出关闭，IPC 仅回传静态标牌证据。"""

import ctypes
import hashlib
import json
import math
import os
import signal
import site
import socket
import struct
import sys
from pathlib import Path

_MAX_PNG_BYTES = 512 * 1024
_MAX_LONG_EDGE = 1000
_MAX_RESPONSE_BYTES = 64 * 1024
_CONTENT_MIN_CONFIDENCE = .5
_STATIC_CHARACTERS = frozenset("禁止作答")
_RUNTIME_ROOT = Path("/opt/template-ocr")
_MODEL_NAMES = ("PP-OCRv5_mobile_det", "PP-OCRv5_mobile_rec")
_MODEL_FILES = ("inference.json", "inference.pdiparams", "inference.yml")


def _failed(reason):
    return {"status": "failed", "reason_code": reason, "characters": [],
            "recognized_character_count": 0, "recognized_block_count": 0}


def _word_bbox(points, width, height):
    # 只使用引擎的逐字多边形。块框按字数切分不能证明竖排文字的实际位置。
    if hasattr(points, "tolist"):
        points = points.tolist()
    if (not isinstance(points, (list, tuple)) or len(points) < 4
            or any(not isinstance(point, (list, tuple)) or len(point) != 2 for point in points)):
        raise ValueError("missing character polygon")
    coordinates = [coordinate for point in points for coordinate in point]
    if any(type(value) not in {int, float} or not math.isfinite(value) for value in coordinates):
        raise ValueError("invalid character polygon")
    xs, ys = [point[0] for point in points], [point[1] for point in points]
    left, top, right, bottom = min(xs), min(ys), max(xs), max(ys)
    if not (0 <= left < right <= width and 0 <= top < bottom <= height):
        raise ValueError("out of bounds character polygon")
    return {"x": left / width, "y": top / height,
            "width": (right - left) / width, "height": (bottom - top) / height}


def extract_static_characters(results, width, height):
    """只留下固定四字；计数仍覆盖其他高分文字，使分类器能拒绝混合正文。"""
    found, total_characters, total_blocks = [], 0, 0
    for result in results:
        if not hasattr(result, "get"):
            raise ValueError("invalid OCR result")
        texts, scores = result.get("rec_texts", []), result.get("rec_scores", [])
        words, polygons = result.get("text_word"), result.get("text_word_region")
        if len(texts) != len(scores) or len(texts) > 256:
            raise ValueError("invalid OCR blocks")
        for block_index, (text, score) in enumerate(zip(texts, scores)):
            score = float(score)
            if not math.isfinite(score) or not 0 <= score <= 1:
                raise ValueError("invalid OCR confidence")
            text = "".join(str(text).split())
            if not text or score < _CONTENT_MIN_CONFIDENCE:
                continue
            total_characters += len(text)
            total_blocks += 1
            if total_characters > 4096:
                raise ValueError("excessive OCR output")
            if not any(character in text for character in _STATIC_CHARACTERS):
                continue
            if (words is None or polygons is None or len(words) != len(texts)
                    or len(polygons) != len(texts)):
                return _failed("TEMPLATE_STATIC_OCR_WORD_BOX_MISSING")
            block_words, block_polygons = words[block_index], polygons[block_index]
            if len(block_words) != len(block_polygons):
                return _failed("TEMPLATE_STATIC_OCR_WORD_BOX_MISSING")
            if "".join("".join(str(word).split()) for word in block_words) != text:
                return _failed("TEMPLATE_STATIC_OCR_WORD_BOX_MISSING")
            for word, polygon in zip(block_words, block_polygons):
                word = "".join(str(word).split())
                if not any(character in word for character in _STATIC_CHARACTERS):
                    continue
                # 引擎若把多个汉字合为一个 word，就没有可用于版式检查的独立字框。
                if len(word) != 1:
                    return _failed("TEMPLATE_STATIC_OCR_WORD_BOX_MISSING")
                found.append({"character": word, "bbox": _word_bbox(polygon, width, height),
                              "confidence": score, "confidence_scope": "block"})
                if len(found) > 256:
                    raise ValueError("excessive character output")
    return {"status": "ok", "reason_code": "TEMPLATE_STATIC_OCR_OK", "characters": found,
            "recognized_character_count": total_characters, "recognized_block_count": total_blocks}


def _verify_models(root):
    """所有权重必须本地存在；有清单时校验其哈希，绝不让引擎临时下载。"""
    model_root = root / "models"
    expected_files = [Path(name) / filename for name in _MODEL_NAMES for filename in _MODEL_FILES]
    for relative in expected_files:
        candidate = model_root / relative
        if not candidate.is_file() or candidate.stat().st_size == 0:
            raise ValueError("local OCR model missing")
    manifest = model_root / "manifest.json"
    if manifest.exists():
        hashes = json.loads(manifest.read_text())
        if not isinstance(hashes, dict) or set(hashes) != {path.as_posix() for path in expected_files}:
            raise ValueError("invalid model manifest")
        for relative in expected_files:
            if hashlib.sha256((model_root / relative).read_bytes()).hexdigest() != hashes[relative.as_posix()]:
                raise ValueError("model hash mismatch")
    return model_root


def _initialize_engine(root=_RUNTIME_ROOT):
    model_root = _verify_models(root)
    # 父进程以 -I -S 启动本入口；只装载独立依赖，避免 NumPy/OpenCV 主版本相互覆盖。
    site.addsitedir(str(root / "site-packages"))
    from paddleocr import PaddleOCR

    return PaddleOCR(
        device="cpu", enable_mkldnn=False, enable_hpi=False, cpu_threads=1,
        text_detection_model_name=_MODEL_NAMES[0], text_detection_model_dir=str(model_root / _MODEL_NAMES[0]),
        text_recognition_model_name=_MODEL_NAMES[1], text_recognition_model_dir=str(model_root / _MODEL_NAMES[1]),
        use_doc_orientation_classify=False, use_doc_unwarping=False, use_textline_orientation=False,
        text_det_limit_type="min", text_det_limit_side_len=64, text_recognition_batch_size=1,
    )


def _analyze(ocr, png):
    import cv2
    import numpy as np

    if (not 33 <= len(png) <= _MAX_PNG_BYTES or png[:8] != b"\x89PNG\r\n\x1a\n"
            or png[12:16] != b"IHDR"):
        return _failed("TEMPLATE_STATIC_OCR_INPUT_INVALID")
    width, height = struct.unpack("!II", png[16:24])
    if min(width, height) <= 0 or max(width, height) > _MAX_LONG_EDGE:
        return _failed("TEMPLATE_STATIC_OCR_INPUT_INVALID")
    image = cv2.imdecode(np.frombuffer(png, dtype=np.uint8), cv2.IMREAD_COLOR)
    if image is None or image.shape[:2] != (height, width):
        return _failed("TEMPLATE_STATIC_OCR_INPUT_INVALID")
    try:
        results = ocr.predict(image, text_rec_score_thresh=0.0, return_word_box=True)
        return extract_static_characters(results, width, height)
    except ValueError:
        return _failed("TEMPLATE_STATIC_OCR_OUTPUT_INVALID")
    except Exception:  # noqa: BLE001 -- 三方推理边界统一失败码，不回显含识别内容的异常。
        return _failed("TEMPLATE_STATIC_OCR_INFERENCE_FAILED")


def _read_exact(channel, size):
    parts = bytearray()
    while len(parts) < size:
        block = channel.recv(size - len(parts))
        if not block:
            raise EOFError()
        parts.extend(block)
    return bytes(parts)


def _send(channel, value):
    data = json.dumps(value, separators=(",", ":"), ensure_ascii=True).encode()
    if len(data) > _MAX_RESPONSE_BYTES:
        data = json.dumps(_failed("TEMPLATE_STATIC_OCR_OUTPUT_INVALID")).encode()
    channel.sendall(struct.pack("!I", len(data)) + data)


def main():
    parent_pid, descriptor = int(sys.argv[2]), int(sys.argv[1])
    # 正常退出靠 IPC EOF，父进程异常消失靠 PDEATHSIG；检查父 PID 关闭设置期间的竞态。
    libc = ctypes.CDLL(None, use_errno=True)
    if libc.prctl(1, signal.SIGKILL, 0, 0, 0) != 0 or os.getppid() != parent_pid:
        return 1
    channel = socket.socket(fileno=descriptor)
    try:
        engine = _initialize_engine()
        _send(channel, {"status": "ready"})
        while True:
            length, = struct.unpack("!I", _read_exact(channel, 4))
            if not 0 < length <= _MAX_PNG_BYTES:
                return 1
            png = _read_exact(channel, length)
            _send(channel, _analyze(engine, png))
    except Exception:  # noqa: BLE001 -- 独立入口失败后退出，由父进程回收并进入冷却。
        return 1
    finally:
        channel.close()


if __name__ == "__main__":
    raise SystemExit(main())
