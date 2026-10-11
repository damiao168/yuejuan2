from __future__ import annotations

import hashlib
import json
from types import SimpleNamespace
from unittest.mock import Mock, call

import page_processing.runner as runner_module
import pytest
from page_processing.api import APIError, Client
from page_processing.config import Config
from page_processing.decoder import DecodeError
from page_processing.omr import OMRExtractionError
from page_processing.registration import (
    RegistrationError,
    RegistrationEvidence,
    RegistrationOutput,
    TemplateGuardEvidence,
    TemplateMatchOutcome,
    TemplateRoutingError,
)
from page_processing.runner import Runner


class RecordingHeartbeat:
    def __init__(self, events: list[tuple], fail_at: int | None = None) -> None:
        self.events = events
        self.fail_at = fail_at
        self.checks = 0

    def raise_if_failed(self) -> None:
        self.checks += 1
        self.events.append(("lease_check",))
        if self.checks == self.fail_at:
            raise APIError("api_request_failed:409", status_code=409)

    def stop(self, raise_on_error: bool = True) -> None:
        self.events.append(("stop", raise_on_error))


def _runner(events: list[tuple]) -> tuple[Runner, Mock]:
    client = Mock(spec=Client)

    def download(url: str) -> bytes:
        events.append(("download", url))
        return url.encode()

    def upload(task: dict, owner_type: str, owner_id: str, filename: str, data: bytes) -> dict:
        events.append(("upload", owner_type, owner_id, filename, data))
        return {"id": filename, "hash_sha256": "hash-" + filename}

    client.download.side_effect = download
    client.upload_asset.side_effect = upload
    client.complete_registration.side_effect = lambda run_id, payload: events.append(("complete", run_id))
    return Runner(client, Config("http://api", "demo", "worker", "secret", "worker-1")), client


def _registration_task() -> dict:
    return {
        "id": "task-1",
        "lease_token": "lease-1",
        "task_type": "page_registration",
        "payload": {
            "registration_run_id": "registration-1",
            "source_download_url": "/source",
            "template_download_url": "/locked",
            "template_id": "locked",
            "template_content_hash": "locked-hash",
            "question_regions": [{"question_id": "q-1"}],
        },
    }


def _registration_output() -> RegistrationOutput:
    evidence = RegistrationEvidence("test", 0.9, [[1.0]], [[1.0]], 10, 9, 8, 0.8, 0.2, 0.95)
    return RegistrationOutput(b"registered", 320, 200, evidence)


def _failed_guard() -> TemplateGuardEvidence:
    return TemplateGuardEvidence(False, True, 0.0, 0.6, 0.7, 0.65)


def test_guard_fallback_reuses_locked_registration_and_stops_before_completion(monkeypatch: pytest.MonkeyPatch) -> None:
    events: list[tuple] = []
    runner, client = _runner(events)
    task = _registration_task()
    task["payload"].update({
        "routing_mode": "locked_with_guard",
        "fallback_templates": [None, {
            "template_id": "alternative", "template_download_url": "/alternative",
            "template_content_hash": "alternative-hash", "template_name": "Alternative", "version_no": 2,
        }],
    })
    outcome = TemplateMatchOutcome("matched", "locked", "locked-hash", 0.9, 0.2, [{"template_id": "locked"}], _registration_output())
    match = Mock(return_value=outcome)
    register = Mock(side_effect=AssertionError("fallback registration must be reused"))
    monkeypatch.setattr(runner_module, "inspect_template_guard", lambda *args, **kwargs: _failed_guard())
    monkeypatch.setattr(runner_module, "match_template_candidates", match)
    monkeypatch.setattr(runner_module, "register_page", register)
    monkeypatch.setattr(runner_module, "crop_regions", lambda *args: [{"question_id": "q-1", "png": b"segment"}])

    runner._process_registration(task, RecordingHeartbeat(events))

    candidates = match.call_args.args[2]
    assert [item.template_id for item in candidates] == ["locked", "alternative"]
    assert candidates[1].template_name == "Alternative"
    assert candidates[1].version_no == 2
    assert events == [
        ("download", "/source"), ("download", "/locked"), ("lease_check",),
        ("download", "/alternative"), ("lease_check",),
        ("upload", "page_registration_output", "registration-1", "registered.png", b"registered"),
        ("lease_check",),
        ("upload", "answer_segment_crop", "registration-1", "segment-0001.png", b"segment"),
        ("stop", False), ("complete", "registration-1"),
    ]
    result = client.complete_registration.call_args.args[1]
    assert result["guard_report"]["fallback_decision"] == "matched"
    assert result["guard_report"]["fallback_candidates"] == outcome.candidates
    assert result["guard_report"]["fallback_margin"] == outcome.margin
    hash_input = {"registered": "hash-registered.png", "segments": [{
        "question_id": "q-1", "file_asset_id": "segment-0001.png", "sha256": "hash-segment-0001.png",
    }]}
    assert result["result_version"] == "sha256:" + hashlib.sha256(json.dumps(hash_input, sort_keys=True).encode()).hexdigest()
    assert task["payload"]["question_regions"] == [{"question_id": "q-1"}]


@pytest.mark.parametrize(("decision", "template_id", "content_hash", "code"), [
    ("ambiguous", None, None, "ambiguous_template_match"),
    ("unknown", None, None, "no_template_match"),
    ("matched", "other", "locked-hash", "template_conflict_with_exam_lock"),
    ("matched", "locked", "different-hash", "template_conflict_with_exam_lock"),
])
def test_guard_rejects_unproven_or_conflicting_match_before_any_upload(
    monkeypatch: pytest.MonkeyPatch, decision: str, template_id: str | None, content_hash: str | None, code: str,
) -> None:
    events: list[tuple] = []
    runner, client = _runner(events)
    task = _registration_task()
    task["payload"]["routing_mode"] = "bound_auto"
    outcome = TemplateMatchOutcome(decision, template_id, content_hash, 0.9, 0.01, [{"template_id": "candidate"}], _registration_output())
    monkeypatch.setattr(runner_module, "inspect_template_guard", lambda *args, **kwargs: _failed_guard())
    monkeypatch.setattr(runner_module, "match_template_candidates", lambda *args, **kwargs: outcome)
    register = Mock()
    monkeypatch.setattr(runner_module, "register_page", register)

    heartbeat = RecordingHeartbeat(events)
    with pytest.raises(TemplateRoutingError, match=code) as caught:
        runner._process_registration(task, heartbeat)

    client.upload_asset.assert_not_called()
    client.complete_registration.assert_not_called()
    register.assert_not_called()
    assert caught.value.detail["candidates"] == outcome.candidates
    assert events == [("download", "/source"), ("download", "/locked"), ("lease_check",)]


def test_passed_guard_registers_bound_template_without_downloading_fallback(monkeypatch: pytest.MonkeyPatch) -> None:
    events: list[tuple] = []
    runner, client = _runner(events)
    task = _registration_task()
    task["payload"].update({
        "routing_mode": "bound_auto",
        "fallback_templates": [{"template_download_url": "/must-not-download"}],
    })
    guard = TemplateGuardEvidence(True, True, 0.0, 1.0, 1.0, 1.0)
    monkeypatch.setattr(runner_module, "inspect_template_guard", lambda *args, **kwargs: guard)
    register = Mock(return_value=_registration_output())
    monkeypatch.setattr(runner_module, "register_page", register)
    match = Mock()
    monkeypatch.setattr(runner_module, "match_template_candidates", match)
    monkeypatch.setattr(runner_module, "crop_regions", lambda *args: [])

    runner._process_registration(task, RecordingHeartbeat(events))

    assert client.download.call_args_list == [call("/source"), call("/locked")]
    assert register.call_args.args == (b"/source", "image/png", b"/locked", "")
    match.assert_not_called()
    result = client.complete_registration.call_args.args[1]
    assert result["guard_report"] == {
        "passed": True, "orientation_match": True, "aspect_ratio_delta": 0.0,
        "perceptual_hash_score": 1.0, "layout_score": 1.0, "score": 1.0,
        "profile_version": "static-layout-guard-v1",
    }


def test_registration_lost_lease_before_crop_upload_keeps_result_uncompleted(monkeypatch: pytest.MonkeyPatch) -> None:
    events: list[tuple] = []
    runner, client = _runner(events)
    monkeypatch.setattr(runner_module, "register_page", lambda *args, **kwargs: _registration_output())
    monkeypatch.setattr(runner_module, "crop_regions", lambda *args: [{"png": b"segment"}])
    # 整页已上传后才到裁片租约检查；失去租约时不能继续上传裁片或报告完成。
    task = _registration_task()
    heartbeat = RecordingHeartbeat(events, fail_at=2)
    with pytest.raises(APIError, match='409'):
        runner._process_registration(task, heartbeat)

    assert client.upload_asset.call_count == 1
    assert client.upload_asset.call_args.args[3] == "registered.png"
    client.complete_registration.assert_not_called()
    assert ("stop", False) not in events


def test_capture_page_upload_failure_stops_completion_and_retry_keeps_page_order(monkeypatch: pytest.MonkeyPatch) -> None:
    events: list[tuple] = []
    runner, client = _runner(events)
    pages = [
        SimpleNamespace(index=1, png=b"first", width=320, height=200),
        SimpleNamespace(index=2, png=b"second", width=320, height=200),
    ]
    monkeypatch.setattr(runner_module, "decode_document", lambda *args, **kwargs: (pages, "image/png"))
    monkeypatch.setattr(runner_module, "detect_barcodes", lambda data: [])
    fail_second = True

    def upload_page(task: dict, index: int, data: bytes) -> dict:
        events.append(("upload_page", index, data))
        if index == 2 and fail_second:
            raise APIError("decoded_page_upload_failed:503")
        return {"id": f"page-{index}", "hash_sha256": f"hash-{index}"}

    client.upload_page.side_effect = upload_page
    client.complete.side_effect = lambda file_id, payload: events.append(("complete_capture", file_id))
    task = {"id": "task-1", "lease_token": "lease-1", "task_type": "capture_file_decode", "payload": {
        "download_url": "/source", "capture_file_id": "capture-1",
    }}
    # 第二页上传失败时，已上传第一页不能被当成整份资料的完成结果。
    heartbeat = RecordingHeartbeat(events)
    with pytest.raises(APIError, match='503') as caught:
        runner._process_capture(task, heartbeat)
    client.complete.assert_not_called()
    assert ("stop", False) not in events
    runner._fail(task, caught.value)
    assert client.fail.call_args.args[1]["retryable"] is True

    events.clear()
    fail_second = False
    runner._process_capture(task, RecordingHeartbeat(events))

    assert events == [
        ("download", "/source"), ("lease_check",), ("lease_check",),
        ("lease_check",), ("upload_page", 1, b"first"),
        ("lease_check",), ("upload_page", 2, b"second"),
        ("stop", False), ("complete_capture", "capture-1"),
    ]
    result = client.complete.call_args.args[1]
    assert result["original_page_count"] == 2
    assert [page["source_index"] for page in result["pages"]] == [1, 2]
    expected_pages = [
        {"source_index": page.index, "file_asset_id": f"page-{page.index}", "sha256": f"hash-{page.index}",
         "width": page.width, "height": page.height, "barcodes": []}
        for page in pages
    ]
    assert result["pages"] == expected_pages
    assert result["result_version"] == "sha256:" + hashlib.sha256(json.dumps(expected_pages, sort_keys=True).encode("utf-8")).hexdigest()


@pytest.mark.parametrize("invalid", [None, {}, {"template_id": "missing-url"}])
def test_template_candidate_validation_stops_downloads_and_matching(monkeypatch: pytest.MonkeyPatch, invalid: object) -> None:
    events: list[tuple] = []
    runner, client = _runner(events)
    task = {
        "id": "task-1", "lease_token": "lease-1",
        "payload": {"source_download_url": "/source", "candidates": [
            {"template_id": "first", "template_download_url": "/first"}, invalid,
            {"template_id": "later", "template_download_url": "/later"},
        ]},
    }
    match = Mock()
    monkeypatch.setattr(runner_module, "match_template_candidates", match)

    heartbeat = RecordingHeartbeat(events)
    with pytest.raises(RegistrationError, match='template_match_candidate_invalid'):
        runner._process_template_match(task, heartbeat)

    assert events == [("download", "/source"), ("lease_check",), ("download", "/first"), ("lease_check",)]
    match.assert_not_called()
    client.complete_template_match.assert_not_called()


@pytest.mark.parametrize(("reference", "code"), [
    ({"download_url": "/reference", "sha256": "hash"}, "omr_reference_missing"),
    ({"file_asset_id": "reference", "sha256": "hash"}, "omr_reference_missing"),
    ({"file_asset_id": "reference", "download_url": "/reference"}, "omr_reference_missing"),
    ({"file_asset_id": "reference", "download_url": "/reference", "sha256": "wrong"}, "omr_reference_hash_mismatch"),
])
def test_difference_reference_is_verified_before_extracting_or_uploading(
    monkeypatch: pytest.MonkeyPatch, reference: dict, code: str,
) -> None:
    events: list[tuple] = []
    runner, client = _runner(events)
    task = {"payload": {
        "source_download_url": "/source", "profile_hash": "profile-hash",
        "profile": {"mode": "template_difference"}, "reference": reference,
    }}
    extract = Mock()
    monkeypatch.setattr(runner_module, "extract_marks", extract)

    heartbeat = RecordingHeartbeat(events)
    with pytest.raises(OMRExtractionError, match=code):
        runner._process_omr(task, heartbeat)

    extract.assert_not_called()
    client.upload_asset.assert_not_called()
    client.complete_omr.assert_not_called()
    assert events[:2] == [("download", "/source"), ("lease_check",)]
    assert client.download.call_count == (2 if code == "omr_reference_hash_mismatch" else 1)


@pytest.mark.parametrize(("exc", "code", "retryable"), [
    (DecodeError("decode-invalid"), "decode-invalid", False),
    (RegistrationError("registration-invalid"), "registration-invalid", False),
    (OMRExtractionError("omr-invalid"), "omr-invalid", False),
    (APIError("source_download_forbidden"), "source_download_forbidden", False),
    (APIError("source_not_found"), "source_not_found", False),
    (APIError("api_request_failed:503", status_code=503), "api_request_failed:503", True),
    (APIError("api_request_failed:409", status_code=409), "api_request_failed:409", True),
    (ValueError("unexpected"), "page_processing_failed", True),
])
def test_failure_classification_preserves_existing_retry_policy(exc: Exception, code: str, retryable: bool) -> None:
    runner, client = _runner([])
    task = {"id": "task-1", "lease_token": "lease-1", "task_type": "omr_extract", "payload": {"omr_run_id": "run-1"}}

    runner._fail(task, exc)

    assert client.fail_omr.call_args.args == ("run-1", {
        "task_id": "task-1", "lease_token": "lease-1", "retryable": retryable,
        "error_code": code, "error_detail": {"error_type": type(exc).__name__, "message": str(exc)[:200]},
        "duration_ms": 0,
    })


@pytest.mark.parametrize(("task_type", "id_key", "method"), [
    ("capture_file_decode", "capture_file_id", "fail"),
    ("page_registration", "registration_run_id", "fail_registration"),
    ("page_template_match", "template_match_run_id", "fail_template_match"),
    ("page_registration_correction_preview", "correction_id", "fail_correction"),
    ("omr_extract", "omr_run_id", "fail_omr"),
])
def test_failure_routing_keeps_bound_resource_and_diagnostics(task_type: str, id_key: str, method: str) -> None:
    runner, client = _runner([])
    task = {"id": "task-1", "lease_token": "lease-1", "task_type": task_type, "payload": {id_key: "bound-1"}}
    exc = TemplateRoutingError("template_conflict_with_exam_lock", {"locked_template_id": "locked"})

    runner._fail(task, exc)

    assert getattr(client, method).call_args.args[0] == "bound-1"
    assert getattr(client, method).call_args.args[1]["error_detail"]["locked_template_id"] == "locked"
    client.fail_task.assert_not_called()


def test_layout_failure_and_missing_run_id_keep_distinct_gateway_routes() -> None:
    runner, client = _runner([])
    exc = DecodeError("decode-invalid")
    task = {"id": "task-1", "lease_token": "lease-1", "task_type": "layout", "payload": {"paper_import_id": "import-1"}}

    runner._fail(task, exc)

    assert client.fail_paper_import.call_args.args == (
        "import-1", task, "decode-invalid", {"error_type": "DecodeError", "message": "decode-invalid"}, False,
    )
    task["task_type"] = "omr_extract"
    runner._fail(task, exc)
    assert client.fail_task.call_args.args == (task, "decode-invalid", {"error_type": "DecodeError", "message": "decode-invalid"})
