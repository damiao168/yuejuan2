import hashlib
import io
from types import SimpleNamespace
from unittest.mock import Mock

import pytest
from ocr_worker import runner as runner_module
from ocr_worker.api import APIError, AuthenticationError
from ocr_worker.engine import OCRBlock
from ocr_worker.math_runner import _build_artifact
from ocr_worker.recognition_router import (
    FormulaRecognitionResult,
    FormulaRegionRecognition,
    RegionKind,
    RoutedRecognitionResult,
)
from ocr_worker.runner import OCRRunner, WorkerConfig
from PIL import Image
from test_runner import FakeAPI, FakeEngine


def _paper_api():
    api = FakeAPI()
    task = api.claim_tasks("worker-a", 1, 300)[0]
    task.update(source_type="paper_import_job", source_id="import-1")
    task["payload"]["pages"] = [
        {"source_id": "source-1", "document_index": 0, "page_no": 1, "download_url": "page-1"},
        {"source_id": "source-2", "document_index": 1, "page_no": 1, "download_url": "page-2"},
    ]
    api.claim_tasks = Mock(return_value=[task])
    return api, task


def _math_task():
    return {
        "answer_segment_id": "segment-39",
        "exam_question_snapshot_id": "snapshot-39",
        "subject_code": "mathematics",
        "input_hash": "image-39",
    }


def test_download_authentication_is_not_reported_as_retryable_ocr_failure():
    api = FakeAPI()
    expired = AuthenticationError("download credentials expired")
    api.downloads["/files/file-1/download"] = expired
    engine = FakeEngine([])
    engine.recognize = Mock()

    runner = OCRRunner(api=api, engine=engine, config=WorkerConfig(worker_id='worker-a'))
    with pytest.raises(AuthenticationError) as caught:
        runner.process_once()

    assert caught.value is expired
    assert api.failed == api.completed == []
    engine.recognize.assert_not_called()


def test_roi_engine_selection_and_input_hash_preserve_repeated_page_order():
    api = FakeAPI()
    encoded = io.BytesIO()
    with Image.new("RGB", (20, 20), "white") as image:
        image.save(encoded, format="PNG")
    image_bytes = encoded.getvalue()
    api.downloads.update({"whole": b"whole-page", "roi": image_bytes})
    repeated_page = {"id": "repeated", "download_url": "whole", "regions": []}
    api.get_task_input = Mock(return_value={"pages": [
        repeated_page,
        {"id": "roi", "download_url": "roi", "regions": [{"bbox": [2, 3, 4, 5]}]},
        repeated_page,
    ]})
    calls = []
    block = OCRBlock("answer", [1, 2, 3, 4], 0.9)
    engine = FakeEngine([block])
    engine.recognize = Mock(side_effect=lambda image: calls.append("whole") or [block])
    engine.recognize_region = Mock(side_effect=lambda image: calls.append("roi") or [block])

    assert OCRRunner(api=api, engine=engine, config=WorkerConfig(worker_id="worker-a")).process_once() == 1

    payload = api.completed[0][1]
    assert calls == ["whole", "roi", "whole"]
    assert [row["submission_page_id"] for row in payload["results"]] == ["repeated", "roi", "repeated"]
    assert payload["results"][1]["bbox"] == [3.0, 5.0, 3, 4]
    assert payload["input_hash"] == hashlib.sha256(b"whole-page" + image_bytes + b"whole-page").hexdigest()
    assert [call.args for call in engine.recognize.call_args_list] == [(b"whole-page",), (b"whole-page",)]
    assert api.failed == []


@pytest.mark.parametrize("failure", ["authentication", "download", "page_metadata"])
def test_paper_original_entry_keeps_authentication_and_classified_retry_boundaries(failure):
    api, task = _paper_api()
    error = AuthenticationError("expired") if failure == "authentication" else APIError("download failed")
    if failure == "page_metadata":
        task["payload"]["pages"][0]["source_id"] = ""
    else:
        api.downloads["page-1"] = error
    api.download = Mock(wraps=api.download)
    engine = FakeEngine([])
    engine.recognize = Mock()
    runner = OCRRunner(api=api, engine=engine, config=WorkerConfig(worker_id="worker-a"))

    if failure == "authentication":
        with pytest.raises(AuthenticationError) as caught:
            runner.process_once()
        assert caught.value is error
        assert api.paper_failed == []
    else:
        assert runner.process_once() == 1
        code = "invalid_paper_ocr_page" if failure == "page_metadata" else "paper_ocr_download_failed"
        assert api.paper_failed == [("import-1", code, failure == "download")]

    assert api.download.call_count == (0 if failure == "page_metadata" else 1)
    engine.recognize.assert_not_called()
    assert api.paper_completed == []
    assert api.failed == []


def test_paper_stop_lease_failure_precedes_reporting_the_engine_error(monkeypatch):
    api, _ = _paper_api()
    lease_error = APIError("lease lost", status_code=409)
    heartbeat_type = runner_module._LeaseHeartbeat
    heartbeats = []

    def heartbeat_factory(**kwargs):
        heartbeat = heartbeat_type(**kwargs)
        heartbeats.append(heartbeat)
        return heartbeat

    def fail_engine(image):
        # 模拟推理期间后台已记录失租；使用真实 stop 验证它先于失败回调抛出。
        heartbeats[0]._error = lease_error
        raise RuntimeError("engine failed")

    monkeypatch.setattr(runner_module, "_LeaseHeartbeat", heartbeat_factory)
    engine = FakeEngine([])
    engine.recognize = Mock(side_effect=fail_engine)
    runner = OCRRunner(api=api, engine=engine, config=WorkerConfig(worker_id='worker-a'))
    with pytest.raises(APIError) as caught:
        runner.process_once()

    assert caught.value is lease_error
    assert api.paper_failed == api.paper_completed == []
    engine.recognize.assert_called_once()


def test_text_artifact_keeps_geometry_order_and_exact_review_threshold():
    verifier = SimpleNamespace(normalize=Mock())
    routed = RoutedRecognitionResult(
        kind=RegionKind.TEXT,
        text_blocks=(OCRBlock(" later  answer ", [0, 40, 20, 10], 0.75), OCRBlock("first", [0, 10, 20, 10], 0.8)),
        image_size=(100, 100),
    )

    artifact = _build_artifact(_math_task(), routed, verifier)

    assert [block["id"] for block in artifact["blocks"]] == ["block-2", "block-1"]
    assert [block["source_refs"] for block in artifact["blocks"]] == [[], []]
    assert artifact["solution_graph"]["steps"][0]["normalized_text"] == "first later answer"
    assert artifact["solution_graph"]["overall_confidence"] == 0.75
    assert artifact["solution_graph"]["requires_human_review"] is False
    assert artifact["engine_version"] == "math-runtime-v1"
    assert artifact["formulas"] == artifact["verifications"] == []
    task = _math_task()
    recognition = RoutedRecognitionResult(kind=RegionKind.TEXT)
    with pytest.raises(ValueError, match='recognition produced no evidence'):
        _build_artifact(task, recognition, verifier)
    verifier.normalize.assert_not_called()


def test_mixed_geometry_sort_preserves_formula_ids_candidates_and_warning_order():
    rejected = FormulaRecognitionResult("bad", "", 0.2, "fixture", "v0", "uncertain")
    selected = FormulaRecognitionResult("x = 2", "x=2", 0.9, "fixture", "v1", "recognized", ("formula", "duplicate"))
    second = FormulaRecognitionResult("x = 3", "x=3", 0.85, "fixture", "v2", "recognized")
    routed = RoutedRecognitionResult(
        kind=RegionKind.MIXED,
        text_blocks=(OCRBlock("last", [0, 90, 20, 5], 0.9),),
        formula_regions=(
            FormulaRegionRecognition((20, 60, 40, 20), 0.95, True, (rejected, selected), 1, ("duplicate", "region")),
            FormulaRegionRecognition((0, 30, 20, 10), 0.6, True),
            FormulaRegionRecognition((10, 0, 20, 10), 0.9, True, (second,), 0),
        ),
        image_size=(100, 100),
    )
    verifier = SimpleNamespace(normalize=Mock(return_value={"ast": {"kind": "equation"}}))

    artifact = _build_artifact(_math_task(), routed, verifier)

    assert [block["id"] for block in artifact["blocks"]] == ["block-formula-3", "block-formula-2", "block-formula-1", "block-text-1"]
    assert artifact["blocks"][1]["kind"] == "unknown"
    assert artifact["blocks"][1]["status"] == "uncertain"
    assert [(formula["id"], formula["block_id"]) for formula in artifact["formulas"]] == [("formula-1", "block-formula-1"), ("formula-2", "block-formula-3")]
    assert [row["formula_id"] for row in artifact["verifications"]] == ["formula-1", "formula-2"]
    assert artifact["solution_graph"]["steps"][0]["formula_ids"] == ["formula-1", "formula-2"]
    first = artifact["formulas"][0]
    assert first["bbox"] == pytest.approx({"x": 0.2, "y": 0.6, "width": 0.4, "height": 0.2})
    assert first["selected_candidate"] == 1
    assert [candidate["latex"] for candidate in first["candidates"]] == ["bad", "x=2"]
    assert [candidate["syntax_valid"] for candidate in first["candidates"]] == [False, True]
    assert first["warnings"] == ["formula", "duplicate", "region"]
    assert [call.args for call in verifier.normalize.call_args_list] == [("x=2",), ("x=3",)]
