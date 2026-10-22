import hashlib
import json
import os
import socket
import struct
import threading
import time
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import MagicMock, patch

import pytest
from grading_agent.template_analysis_ocr_child import (
    _MODEL_FILES,
    _MODEL_NAMES,
    _verify_models,
    extract_static_characters,
)
from grading_agent.template_analysis_ocr_runtime import (
    MAX_PNG_BYTES,
    MAX_RESPONSE_BYTES,
    TemplateStaticOCRRuntime,
    _png_dimensions,
    _receive_exact,
    _receive_frame,
    _validated_result,
)


def png_header(width=100, height=100):
    return b"\x89PNG\r\n\x1a\n" + struct.pack("!I", 13) + b"IHDR" + struct.pack("!II", width, height) + b"\0" * 9


def polygon(x, y, width=20, height=20):
    return [[x, y], [x + width, y], [x + width, y + height], [x, y + height]]


def paddle_result(extra=None):
    # 两列竖排必须保留引擎字框，按文本块均分会丢失用于 2×2 验证的行列关系。
    row = {"rec_texts": ["禁作", "止答"], "rec_scores": [.998, .997],
           "text_word": [["禁", "作"], ["止", "答"]],
           "text_word_region": [[polygon(10, 10), polygon(10, 50)],
                                [polygon(60, 10), polygon(60, 50)]]}
    if extra:
        row["rec_texts"].append(extra)
        row["rec_scores"].append(.99)
        row["text_word"].append([extra])
        row["text_word_region"].append([polygon(1, 80)])
    return row


def response():
    return extract_static_characters([paddle_result()], 100, 100)


def ready_runtime():
    runtime = TemplateStaticOCRRuntime(enabled=True)
    client, server = socket.socketpair()
    runtime._state, runtime._channel = "ready", client
    return runtime, server


def test_vertical_words_keep_actual_character_positions_and_block_confidence():
    result = response()
    assert result["status"] == "ok"
    assert result["recognized_character_count"] == 4
    assert result["recognized_block_count"] == 2
    assert [entry["character"] for entry in result["characters"]] == ["禁", "作", "止", "答"]
    assert result["characters"][1]["bbox"] == {"x": .1, "y": .5, "width": .2, "height": .2}
    assert result["characters"][1]["confidence"] == .998
    assert all(entry["confidence_scope"] == "block" for entry in result["characters"])


def test_extra_content_counts_without_returning_its_text():
    result = extract_static_characters([paddle_result("PRIVATE BODY")], 100, 100)
    assert result["recognized_character_count"] == 15
    assert result["recognized_block_count"] == 3
    assert "PRIVATE" not in json.dumps(result)


def test_low_confidence_body_is_not_counted_as_high_confidence_content():
    row = paddle_result("PRIVATE")
    row["rec_scores"][-1] = .2
    result = extract_static_characters([row], 100, 100)
    assert result["recognized_character_count"] == 4
    assert result["recognized_block_count"] == 2


@pytest.mark.parametrize("change", ["missing", "combined", "inconsistent"])
def test_no_character_box_is_ever_invented_from_block_geometry(change):
    row = paddle_result()
    if change == "missing":
        row.pop("text_word_region")
    elif change == "combined":
        row["text_word"][0] = ["禁作"]
        row["text_word_region"][0] = [polygon(10, 10, 20, 60)]
    else:
        row["text_word"][0] = ["禁", "答"]
    result = extract_static_characters([row], 100, 100)
    assert result["status"] == "failed"
    assert result["reason_code"] == "TEMPLATE_STATIC_OCR_WORD_BOX_MISSING"
    assert result["characters"] == []


@pytest.mark.parametrize("bad_polygon", [[], [[0, 0]], polygon(-1, 2), polygon(90, 2),
                                         [[float("nan"), 0]] * 4])
def test_invalid_character_geometry_fails_closed(bad_polygon):
    row = paddle_result()
    row["text_word_region"][0][0] = bad_polygon
    with pytest.raises(ValueError):
        extract_static_characters([row], 100, 100)


def test_disabled_runtime_neither_spawns_nor_waits():
    runtime = TemplateStaticOCRRuntime(enabled=False)
    with patch("grading_agent.template_analysis_ocr_runtime.subprocess.Popen") as spawn:
        assert runtime.analyze_crop(png_header())["reason_code"] == "TEMPLATE_STATIC_OCR_DISABLED"
        runtime.start()
    spawn.assert_not_called()


def test_warming_request_returns_without_waiting_for_cold_start():
    runtime = TemplateStaticOCRRuntime(enabled=True)
    runtime._state = "warming"
    started = time.monotonic()
    assert runtime.analyze_crop(png_header())["reason_code"] == "TEMPLATE_STATIC_OCR_WARMING"
    assert time.monotonic() - started < .1


def test_busy_runtime_does_not_queue_other_page_requests():
    runtime, server = ready_runtime()
    runtime._request_lock.acquire()
    try:
        assert runtime.analyze_crop(png_header())["reason_code"] == "TEMPLATE_STATIC_OCR_BUSY"
    finally:
        runtime._request_lock.release()
        runtime.close()
        server.close()


def test_successful_ipc_returns_only_whitelisted_result_fields():
    runtime, server = ready_runtime()
    sent = response() | {"private_text": "PRIVATE"}

    def serve():
        deadline = time.monotonic() + 1
        length, = struct.unpack("!I", _receive_exact(server, 4, deadline))
        assert _receive_exact(server, length, deadline) == png_header()
        encoded = json.dumps(sent).encode()
        server.sendall(struct.pack("!I", len(encoded)) + encoded)

    thread = threading.Thread(target=serve)
    thread.start()
    try:
        result = runtime.analyze_crop(png_header(), timeout=1)
        assert result == response()
        assert "PRIVATE" not in json.dumps(result)
    finally:
        thread.join(timeout=2)
        runtime.close()
        server.close()


def test_timeout_drops_process_channel_and_enters_cooldown():
    runtime, server = ready_runtime()
    started = time.monotonic()
    try:
        result = runtime.analyze_crop(png_header(), timeout=.02)
        assert result["reason_code"] == "TEMPLATE_STATIC_OCR_TIMEOUT"
        assert time.monotonic() - started < .2
        assert runtime._channel is None and runtime._state == "cooldown"
        with patch("grading_agent.template_analysis_ocr_runtime.threading.Thread") as spawn:
            runtime.start()
            spawn.assert_not_called()
    finally:
        runtime.close()
        server.close()


def test_oversized_ipc_frame_is_rejected_before_reading_body():
    client, server = socket.socketpair()
    try:
        server.sendall(struct.pack("!I", MAX_RESPONSE_BYTES + 1))
        with pytest.raises(ValueError):
            _receive_frame(client, time.monotonic() + 1)
    finally:
        client.close()
        server.close()


@pytest.mark.parametrize("png", [b"", b"not an image", png_header(1001), png_header(0),
                                 png_header() + b"x" * MAX_PNG_BYTES],
                         ids=["empty", "not-png", "oversized-edge", "zero-size", "oversized-bytes"])
def test_input_limits_apply_before_process_creation(png):
    runtime = TemplateStaticOCRRuntime(enabled=True)
    with patch.object(runtime, "start") as start:
        assert runtime.analyze_crop(png)["reason_code"] == "TEMPLATE_STATIC_OCR_INPUT_INVALID"
        start.assert_not_called()
    assert _png_dimensions(png) is None


@pytest.mark.parametrize("timeout", [0, -1, float("nan"), float("inf"), "3", True])
def test_invalid_timeout_is_rejected(timeout):
    runtime = TemplateStaticOCRRuntime(enabled=True)
    assert runtime.analyze_crop(png_header(), timeout=timeout)["reason_code"] == "TEMPLATE_STATIC_OCR_INPUT_INVALID"


@pytest.mark.parametrize("field,value", [("confidence", float("nan")), ("character", "PRIVATE"),
                                        ("confidence_scope", "character")])
def test_parent_rechecks_child_fixed_character_protocol(field, value):
    sent = response()
    sent["characters"][0][field] = value
    with pytest.raises(ValueError):
        _validated_result(sent)


def test_local_models_require_all_files_and_optional_exact_hash_manifest(tmp_path):
    root = tmp_path
    with pytest.raises(ValueError):
        _verify_models(root)
    hashes = {}
    for model in _MODEL_NAMES:
        for name in _MODEL_FILES:
            relative = f"{model}/{name}"
            file = root / "models" / relative
            file.parent.mkdir(parents=True, exist_ok=True)
            file.write_bytes(b"fixed local model fixture")
            hashes[relative] = hashlib.sha256(file.read_bytes()).hexdigest()
    assert _verify_models(root) == root / "models"
    manifest = root / "models" / "manifest.json"
    manifest.write_text(json.dumps(hashes))
    assert _verify_models(root) == root / "models"
    (root / "models" / _MODEL_NAMES[0] / _MODEL_FILES[0]).write_bytes(b"different")
    with pytest.raises(ValueError):
        _verify_models(root)


def test_close_prevents_future_restart():
    runtime = TemplateStaticOCRRuntime(enabled=True)
    runtime.close()
    with patch("grading_agent.template_analysis_ocr_runtime.threading.Thread") as spawn:
        runtime.start()
        spawn.assert_not_called()


def test_start_is_singleflight_while_background_thread_is_pending():
    runtime = TemplateStaticOCRRuntime(enabled=True)
    with patch("grading_agent.template_analysis_ocr_runtime.threading.Thread") as thread:
        runtime.start()
        runtime.start()
        assert runtime._state == "warming"
        thread.assert_called_once()
        thread.return_value.start.assert_called_once()
    runtime.close()


@pytest.mark.parametrize("stage", ["construct", "start"])
@pytest.mark.parametrize("error_type", [RuntimeError, OSError])
def test_background_thread_resource_failure_keeps_startup_and_requests_available(stage, error_type):
    runtime = TemplateStaticOCRRuntime(enabled=True)
    with patch("grading_agent.template_analysis_ocr_runtime.threading.Thread") as thread:
        if stage == "construct":
            thread.side_effect = error_type("thread resources unavailable")
        else:
            thread.return_value.start.side_effect = error_type("thread resources unavailable")
        # 显式服务预热与请求内自动重试共用 start，资源错误不能穿透任一路径。
        runtime.start()
        assert runtime._state == "cooldown" and runtime._retry_at > time.monotonic()
        assert runtime.analyze_crop(png_header())["reason_code"] == "TEMPLATE_STATIC_OCR_UNAVAILABLE"
        thread.assert_called_once()
    runtime.close()


def test_first_request_thread_resource_failure_returns_conservative_unavailable():
    runtime = TemplateStaticOCRRuntime(enabled=True)
    with patch("grading_agent.template_analysis_ocr_runtime.threading.Thread") as thread:
        thread.return_value.start.side_effect = RuntimeError("thread resources unavailable")
        result = runtime.analyze_crop(png_header())
        assert result["status"] == "unavailable"
        assert result["reason_code"] == "TEMPLATE_STATIC_OCR_UNAVAILABLE"
        assert result["characters"] == []
        assert runtime._state == "cooldown"
    runtime.close()


def test_warmup_uses_only_isolated_dependencies_and_private_writable_cache():
    runtime = TemplateStaticOCRRuntime(enabled=True)
    process = MagicMock()
    process.poll.return_value = 0
    runtime_os = SimpleNamespace(name="posix", environ=dict(os.environ), getpid=os.getpid)
    channels = socket.socketpair()
    with (patch("grading_agent.template_analysis_ocr_runtime.os", runtime_os),
          patch("grading_agent.template_analysis_ocr_runtime.socket.AF_UNIX", 1, create=True),
          patch("grading_agent.template_analysis_ocr_runtime.socket.socketpair", return_value=channels),
          patch("grading_agent.template_analysis_ocr_runtime.subprocess.Popen", return_value=process) as spawn,
          patch.object(runtime, "_wait_for_child"),
          patch("grading_agent.template_analysis_ocr_runtime._receive_frame", return_value=b'{"status":"ready"}')):
        runtime._warmup()
        assert runtime._state == "ready"
        command = spawn.call_args.args[0]
        assert command[1:4] == ["-I", "-S", "-B"]
        env = spawn.call_args.kwargs["env"]
        assert env["LD_LIBRARY_PATH"].startswith("/opt/template-ocr/lib")
        cache = Path(runtime._cache_dir.name)
        assert Path(env["PADDLE_PDX_CACHE_HOME"]) == cache / "paddlex"
        assert Path(env["MPLCONFIGDIR"]) == cache / "mpl"
        assert (cache / "paddlex").is_dir()
        assert spawn.call_args.kwargs["start_new_session"] is True
        runtime.close()
        assert not cache.exists()


@pytest.mark.parametrize("stop", ["close", "unexpected_exit"])
def test_creator_thread_lives_after_ready_until_child_exits_and_preserves_owner_state(stop):
    # Linux 将 PDEATHSIG 绑定到创建线程；先前预热线程返回时，正常子进程就会被误杀。
    runtime = TemplateStaticOCRRuntime(enabled=True)
    process = MagicMock()
    process.pid = 123456
    exited, waiting = threading.Event(), threading.Event()
    creators = []

    def spawn(*_args, **_kwargs):
        creators.append(threading.current_thread())
        return process

    def wait(*_args, **_kwargs):
        waiting.set()
        assert exited.wait(timeout=2)
        return 0

    process.wait.side_effect = wait
    process.poll.side_effect = lambda: 0 if exited.is_set() else None
    runtime_os = SimpleNamespace(name="posix", environ=dict(os.environ), getpid=os.getpid,
                                 killpg=lambda *_args: exited.set())
    channels = socket.socketpair()
    with (patch("grading_agent.template_analysis_ocr_runtime.os", runtime_os),
          patch("grading_agent.template_analysis_ocr_runtime.socket.AF_UNIX", 1, create=True),
          patch("grading_agent.template_analysis_ocr_runtime.signal.SIGKILL", 9, create=True),
          patch("grading_agent.template_analysis_ocr_runtime.socket.socketpair", return_value=channels),
          patch("grading_agent.template_analysis_ocr_runtime.subprocess.Popen", side_effect=spawn),
          patch("grading_agent.template_analysis_ocr_runtime._receive_frame", return_value=b'{"status":"ready"}')):
        runtime.start()
        try:
            assert waiting.wait(timeout=1)
            assert runtime._state == "ready" and creators[0].is_alive()
            cache = Path(runtime._cache_dir.name)
            if stop == "close":
                runtime.close()
            else:
                exited.set()
            creators[0].join(timeout=1)
            assert not creators[0].is_alive()
            assert runtime._process is None and runtime._channel is None and not cache.exists()
            if stop == "close":
                assert runtime._state == "closed"
            else:
                assert runtime._state == "cooldown" and runtime._retry_at > time.monotonic()
        finally:
            exited.set()
            runtime.close()


def test_warmup_protocol_failure_cleans_process_and_private_cache():
    runtime = TemplateStaticOCRRuntime(enabled=True)
    process = MagicMock()
    process.poll.return_value = None
    process.pid = 123456
    kill_group = MagicMock()
    runtime_os = SimpleNamespace(name="posix", environ=dict(os.environ), getpid=os.getpid, killpg=kill_group)
    channels = socket.socketpair()
    cache_path = None

    def bad_handshake(*_args):
        nonlocal cache_path
        cache_path = Path(runtime._cache_dir.name)
        return b'{"status":"failed", "private_text":"PRIVATE"}'

    with (patch("grading_agent.template_analysis_ocr_runtime.os", runtime_os),
          patch("grading_agent.template_analysis_ocr_runtime.socket.AF_UNIX", 1, create=True),
          patch("grading_agent.template_analysis_ocr_runtime.socket.socketpair", return_value=channels),
          patch("grading_agent.template_analysis_ocr_runtime.subprocess.Popen", return_value=process),
          patch("grading_agent.template_analysis_ocr_runtime.signal.SIGKILL", 9, create=True),
          patch("grading_agent.template_analysis_ocr_runtime._receive_frame", side_effect=bad_handshake)):
        runtime._warmup()
    assert runtime._state == "cooldown" and runtime._channel is None
    assert not cache_path.exists()
    kill_group.assert_called_once()
    process.wait.assert_called_once()


def test_failed_request_terminates_child_before_returning_but_reaps_outside_page_budget():
    runtime, server = ready_runtime()
    process, cache = MagicMock(), MagicMock()
    process.poll.return_value = None
    process.pid = 123456
    runtime._process, runtime._cache_dir = process, cache
    wait_started, release_wait, cleaned = threading.Event(), threading.Event(), threading.Event()

    def slow_wait(*_args, **_kwargs):
        wait_started.set()
        release_wait.wait(timeout=2)

    process.wait.side_effect = slow_wait
    cache.cleanup.side_effect = cleaned.set
    server.sendall(struct.pack("!I", 2) + b"{}")
    try:
        with (patch("grading_agent.template_analysis_ocr_runtime.os.killpg", create=True) as kill_group,
              patch("grading_agent.template_analysis_ocr_runtime.signal.SIGKILL", 9, create=True)):
            result = runtime.analyze_crop(png_header(), timeout=.1)
            assert result["reason_code"] == "TEMPLATE_STATIC_OCR_PROTOCOL_FAILED"
            kill_group.assert_called_once_with(123456, 9)
            assert runtime._channel is None and runtime._state == "cooldown"
            assert wait_started.wait(timeout=1)
            # 调用已返回而回收仍被事件阻塞，直接验证不等待子进程；不以主机调度耗时判断。
            assert not release_wait.is_set()
            assert not cleaned.is_set()
        release_wait.set()
        assert cleaned.wait(timeout=1)
    finally:
        release_wait.set()
        runtime.close()
        server.close()


@pytest.mark.parametrize("stage", ["construct", "start"])
@pytest.mark.parametrize("error_type", [RuntimeError, OSError])
def test_reaper_thread_failure_defers_cleanup_to_exit_without_waiting(stage, error_type):
    process, cache = MagicMock(), MagicMock()
    process.poll.return_value = 0
    with (patch("grading_agent.template_analysis_ocr_runtime.threading.Thread") as thread,
          patch("grading_agent.template_analysis_ocr_runtime.atexit.register") as on_exit):
        if stage == "construct":
            thread.side_effect = error_type("thread resources unavailable")
        else:
            thread.return_value.start.side_effect = error_type("thread resources unavailable")
        TemplateStaticOCRRuntime._dispose(process, None, cache, background=True)
        process.wait.assert_not_called()
        cache.cleanup.assert_not_called()
        on_exit.assert_called_once_with(TemplateStaticOCRRuntime._finish_dispose, process, cache)
