import hashlib
from types import SimpleNamespace

import pytest
from ocr_worker import paper_formula
from ocr_worker.formula_validation import FormulaAction, FormulaValidationResult
from ocr_worker.runner import WorkerConfig


def _roi(number, *, score=0.9, complete=True, recrops=0, ratio=1):
    return {
        "number": number,
        "bbox": [number * 20, 20, ratio * 20, 20],
        "score": score,
        "complete": complete,
        "recrops": recrops,
    }


class _Engine:
    def __init__(self, name, state):
        self.model_version = name
        self.initialized = False
        self.state = state

    def initialize(self):
        self.state.record("initialize", self.model_version)
        self.initialized = True

    def recognize_formulas(self, crops, *, batch_size):
        self.state.record("recognize", self.model_version, tuple(crops), batch_size)
        return [
            SimpleNamespace(
                canonical_latex=f"{self.model_version}:{crop.decode()}",
                raw_latex=f"{self.model_version}:{crop.decode()}",
                confidence=0.9,
                engine_version=self.model_version,
            )
            for crop in crops
        ]


@pytest.fixture
def harness(monkeypatch):
    state = SimpleNamespace(
        events=[], errors={}, actions={}, reasons={}, completed=[], failed=[], progress=[],
        roi_pages={b"page-1": [_roi(1)]}, stop_error=None,
    )

    def record(operation, *values):
        state.events.append((operation, *values))
        key = f"{operation}-{values[0]}" if operation in {"initialize", "recognize", "validate"} else operation
        if key in state.errors:
            raise state.errors[key]

    state.record = record
    state.task = {
        "id": "task-35", "lease_token": "lease-35", "source_id": "import-35", "tenant_id": "tenant-35",
        "payload": {
            "subject_code": "mathematics",
            "recognition_policy": {"formula_enabled": True},
            "pages": [{"source_id": "source-35", "page_no": 1, "document_index": 0, "download_url": "page-1"}],
        },
    }

    class Heartbeat:
        def __init__(self, **kwargs):
            state.heartbeat_kwargs = kwargs

        def __enter__(self):
            record("enter")
            return self

        def __exit__(self, exc_type, _exc, _traceback):
            record("exit", exc_type)
            # 同步 fake 保留退出时的异常优先规则，不创建后台线程或发送网络心跳。
            try:
                self.stop()
            except Exception:
                if exc_type is None:
                    raise

        def stop(self):
            record("stop")
            if state.stop_error is not None:
                raise state.stop_error

        def raise_if_failed(self):
            record("check_lease")

        def set_progress(self, progress, *, flush=False):
            state.progress.append(dict(progress))
            record("progress", progress["phase"], flush)

    def claim(worker_id, lease_seconds):
        record("claim", worker_id, lease_seconds)
        return [state.task]

    def activate(task, worker_id):
        record("activate", task, worker_id)

    def download(url):
        record("download", url)
        return url.encode()

    def fail(import_id, task_id, lease_token, error_code, retryable, *, error_detail=None, duration_ms=0):
        args = (import_id, task_id, lease_token, error_code, retryable, error_detail or {}, duration_ms)
        state.failed.append(args)
        record("fail", *args)

    def complete(import_id, payload):
        args = (import_id, payload)
        state.completed.append(args)
        record("complete", *args)

    def initialize_detector():
        record("initialize", "detector")
        state.detector.initialized = True

    def detect(image_bytes, min_score):
        record("detect", image_bytes, min_score)
        return [(region["bbox"], region["score"]) for region in state.roi_pages[image_bytes]]

    def open_image(image_bytes):
        record("open_image", image_bytes)
        return SimpleNamespace(page=image_bytes, close=lambda: record("close_image", image_bytes))

    def crop(image, bbox, padding, **kwargs):
        region = next(value for value in state.roi_pages[image.page] if value["bbox"] == bbox)
        record("crop", region["number"], padding, kwargs)
        return f"crop-{region['number']}".encode(), list(bbox), 0.01, region["recrops"], region["complete"]

    def validate(latex, crop_bytes, **kwargs):
        model = latex.split(":", 1)[0]
        record("validate", model, crop_bytes, kwargs)
        action = state.actions.get((model, crop_bytes), FormulaAction.ACCEPT)
        reasons = state.reasons.get((model, crop_bytes), ())
        return FormulaValidationResult(True, True, action is FormulaAction.ACCEPT, True, 0.9, action, reasons)

    state.api = SimpleNamespace(
        claim_paper_formula_tasks=claim, activate_task=activate, download=download,
        fail_paper_import=fail, complete_paper_import_formula=complete,
    )
    state.detector = SimpleNamespace(
        initialized=False, model_version="detector-35", initialize=initialize_detector, detect=detect,
    )
    state.primary = _Engine("M", state)
    state.fallback = _Engine("L", state)
    monkeypatch.setattr(paper_formula, "_LeaseHeartbeat", Heartbeat)
    monkeypatch.setattr(paper_formula, "_open_image", open_image)
    monkeypatch.setattr(paper_formula, "_adaptive_crop_formula", crop)
    state.runner = paper_formula.PaperFormulaRunner(
        api=state.api,
        config=WorkerConfig(worker_id="worker-35"),
        detector=state.detector,
        primary=state.primary,
        fallback=state.fallback,
        batch_size=2,
        validator=SimpleNamespace(render_similarity_threshold=0.34, validate=validate),
        runtime_plan={"runtime_mode": "resident", "runtime_batch_size": 2},
    )
    return state


@pytest.mark.parametrize("invalid", ["subject", "disabled", "pages"])
def test_process_once_keeps_invalid_route_failure_before_heartbeat(harness, invalid):
    payload = harness.task["payload"]
    if invalid == "subject":
        payload["subject_code"] = "english"
    elif invalid == "disabled":
        payload["recognition_policy"]["formula_enabled"] = False
    else:
        payload["pages"] = None

    assert harness.runner.process_once() == 1
    assert [event[0] for event in harness.events] == ["claim", "activate", "fail"]
    assert harness.failed == [("import-35", "task-35", "lease-35", "invalid_formula_route", False, {}, 0)]
    assert harness.completed == []


@pytest.mark.parametrize("empty_pages", [False, True])
def test_empty_formula_work_keeps_completion_without_recognition_models(harness, empty_pages):
    harness.roi_pages[b"page-1"] = []
    if empty_pages:
        harness.task["payload"]["pages"] = []

    assert harness.runner.process_once() == 1
    assert not harness.primary.initialized
    assert not harness.fallback.initialized
    assert harness.completed[0][0] == "import-35"
    assert harness.completed[0][1]["regions"] == []
    assert len(harness.completed[0]) == 2
    assert ("activate", harness.task, "worker-35") in harness.events
    assert harness.task["tenant_id"] == "tenant-35"
    assert harness.progress[-1]["phase"] == "completed"
    assert harness.progress[-1]["completed"] == harness.progress[-1]["total"] == 0
    assert harness.progress[-1]["runtime_mode"] == "resident"
    operations = [event[0] for event in harness.events]
    assert operations.index("stop") < operations.index("complete")
    assert harness.failed == []


def test_mixed_rois_keep_primary_then_validation_then_fallback_and_output_order(harness):
    harness.roi_pages[b"page-1"] = [
        _roi(1), _roi(2, ratio=4), _roi(3), _roi(4, score=0.5),
        _roi(5, complete=False), _roi(6, recrops=1, ratio=4),
    ]
    for number in (2, 6):
        harness.actions["M", f"crop-{number}".encode()] = FormulaAction.RETRY_L
        harness.reasons["M", f"crop-{number}".encode()] = ("primary_retry",)
    harness.actions["M", b"crop-3"] = FormulaAction.REVIEW
    harness.reasons["M", b"crop-3"] = ("validator_unavailable",)
    harness.actions["L", b"crop-6"] = FormulaAction.REVIEW
    harness.reasons["L", b"crop-6"] = ("validator_unavailable",)

    assert harness.runner.process_once() == 1
    events = harness.events
    primary_positions = [index for index, event in enumerate(events) if event[:2] == ("recognize", "M")]
    validation_positions = [index for index, event in enumerate(events) if event[:2] == ("validate", "M")]
    fallback_positions = [index for index, event in enumerate(events) if event[:2] == ("recognize", "L")]
    assert max(primary_positions) < min(validation_positions) < max(validation_positions) < min(fallback_positions)
    assert [events[index][2] for index in primary_positions] == [(b"crop-1", b"crop-3"), (b"crop-2", b"crop-6")]
    assert [events[index][2] for index in validation_positions] == [b"crop-1", b"crop-2", b"crop-3", b"crop-6"]
    assert [events[index][2] for index in fallback_positions] == [(b"crop-2", b"crop-6")]
    regions = harness.completed[0][1]["regions"]
    assert [region["region_id"] for region in regions] == [f"formula:source-35:1:{index}" for index in range(1, 7)]
    assert [region["status"] for region in regions] == ["accepted", "accepted", "review_required", "review_required", "review_required", "review_required"]
    assert regions[0] == {
        "source_id": "source-35", "document_index": 0, "page_no": 1,
        "region_id": "formula:source-35:1:1", "bbox": [20, 20, 20, 20],
        "detector_model": "detector-35", "detector_confidence": 0.9,
        "crop_sha256": hashlib.sha256(b"crop-1").hexdigest(),
        "edge_ink_ratio": 0.01, "crop_complete": True, "recrop_count": 0,
        "validation_version": paper_formula.FORMULA_VALIDATION_VERSION,
        "candidates": [{
            "model_version": "M", "raw_latex": "M:crop-1", "canonical_latex": "M:crop-1",
            "confidence": 0.9, "valid": True, "syntax_valid": True, "structure_valid": True,
            "render_valid": True, "render_similarity": 0.9, "validation_action": "accept", "reason_codes": [],
        }],
        "selected_latex": "M:crop-1", "selected_model": "M", "status": "accepted", "reason_codes": [],
    }
    assert [candidate["model_version"] for candidate in regions[1]["candidates"]] == ["M", "L"]
    assert regions[1]["selected_model"] == "L"
    assert regions[1]["reason_codes"] == ["primary_retry", "fallback_used"]
    assert regions[2]["reason_codes"] == ["validator_unavailable", "fallback_unavailable"]
    assert regions[3]["candidates"] == regions[4]["candidates"] == []
    assert regions[3]["reason_codes"] == ["low_detector_confidence"]
    assert regions[4]["reason_codes"] == ["roi_incomplete_after_recrop"]
    assert regions[5]["reason_codes"] == ["primary_retry", "validator_unavailable", "fallback_validation_failed", "roi_recropped"]
    assert "tenant_id" not in harness.heartbeat_kwargs
    assert harness.heartbeat_kwargs["runtime_task_id"] == "task-35"
    assert harness.heartbeat_kwargs["lease_token"] == "lease-35"
    assert harness.heartbeat_kwargs["worker_id"] == "worker-35"
    assert harness.failed == []


def test_multiple_pages_keep_local_region_indices_and_scoped_download_order(harness):
    harness.task["payload"]["pages"].append(
        {"source_id": "source-35", "page_no": 2, "document_index": 0, "download_url": "page-2"}
    )
    harness.roi_pages[b"page-1"] = [_roi(1), _roi(2)]
    harness.roi_pages[b"page-2"] = [_roi(3)]

    assert harness.runner.process_once() == 1
    assert [event[1:] for event in harness.events if event[0] == "download"] == [
        ("page-1",), ("page-2",),
    ]
    assert [region["region_id"] for region in harness.completed[0][1]["regions"]] == [
        "formula:source-35:1:1", "formula:source-35:1:2", "formula:source-35:2:1",
    ]
    assert [event[2] for event in harness.events if event[:2] == ("recognize", "M")] == [
        (b"crop-1", b"crop-2"), (b"crop-3",),
    ]
    assert harness.events.index(("close_image", b"page-1")) < harness.events.index(("download", "page-2"))


@pytest.mark.parametrize(
    "key",
    ["accept_detector_score", "max_edge_ink_ratio", "formula_batch_size", "max_roi_padding_pixels", "render_similarity_threshold"],
)
def test_policy_conversion_failure_precedes_model_initialization(harness, key):
    harness.task["payload"]["recognition_policy"][key] = "invalid-number"

    assert harness.runner.process_once() == 1
    assert not any(event[0] in {"initialize", "download"} for event in harness.events)
    assert harness.failed[0][3] == "paper_formula_failed:ValueError"
    assert harness.failed[0][4] is True
    assert harness.completed == []


@pytest.mark.parametrize(
    "stage",
    ["initialize-detector", "download", "detect", "recognize-M", "validate-M", "recognize-L", "validate-L", "check_lease"],
)
def test_backend_failure_stops_heartbeat_before_task_failure(harness, stage):
    harness.actions["M", b"crop-1"] = FormulaAction.RETRY_L
    harness.errors[stage] = RuntimeError(stage)

    assert harness.runner.process_once() == 1
    assert len(harness.failed) == 1
    assert harness.completed == []
    failure = harness.failed[0]
    assert failure[:5] == ("import-35", "task-35", "lease-35", "paper_formula_failed:RuntimeError", True)
    assert failure[5] == {"error_type": "RuntimeError", "message": stage}
    assert isinstance(failure[6], int)
    assert failure[6] >= 0
    operations = [event[0] for event in harness.events]
    assert operations.index("stop") < operations.index("fail") < operations.index("exit")


@pytest.mark.parametrize("model", ["M", "L"])
def test_short_batch_keeps_strict_zip_failure(harness, monkeypatch, model):
    harness.actions["M", b"crop-1"] = FormulaAction.RETRY_L
    original = paper_formula._recognize_many

    def recognize_many(engine, crops, batch_size):
        # 绕过前置数量校验，单独保护批次索引回填的 strict zip，避免静默丢失 ROI。
        return [] if engine.model_version == model else original(engine, crops, batch_size)

    monkeypatch.setattr(paper_formula, "_recognize_many", recognize_many)
    assert harness.runner.process_once() == 1
    assert harness.failed[0][3] == "paper_formula_failed:ValueError"
    assert harness.completed == []


@pytest.mark.parametrize("backend_failed", [False, True])
def test_heartbeat_stop_error_keeps_original_exception_priority(harness, backend_failed):
    if backend_failed:
        harness.errors["download"] = ValueError("download")
    harness.stop_error = LookupError("stop")

    # 现有 stop 失败会阻止终态回写；本次职责提取不能改变这个异常边界。
    with pytest.raises(LookupError) as caught:
        harness.runner.process_once()

    assert caught.value is harness.stop_error
    assert harness.failed == harness.completed == []


@pytest.mark.parametrize("terminal", ["fail", "complete"])
def test_terminal_api_error_keeps_propagation_without_another_write(harness, terminal):
    if terminal == "fail":
        harness.errors["download"] = ValueError("download")
    error = OSError(terminal)
    harness.errors[terminal] = error

    with pytest.raises(OSError) as caught:
        harness.runner.process_once()

    assert caught.value is error
    assert len(harness.failed) == (1 if terminal == "fail" else 0)
    assert len(harness.completed) == (1 if terminal == "complete" else 0)


def test_first_heartbeat_failure_stays_outside_processing_catch(harness):
    error = RuntimeError("enter")
    harness.errors["enter"] = error

    with pytest.raises(RuntimeError) as caught:
        harness.runner.process_once()

    assert caught.value is error
    assert [event[0] for event in harness.events] == ["claim", "activate", "enter"]
    assert harness.failed == harness.completed == []


@pytest.mark.parametrize(
    "primary_latex,fallback_latex,chosen_name,status,reasons",
    [
        ("x^2", None, "primary", "accepted", []),
        (r"\frac{1}{2", None, None, "review_required", ["primary_invalid_latex"]),
        ("x^2", r"\frac{1}{2", "primary", "accepted", ["fallback_invalid_latex"]),
        (r"\frac{1}{2", "x^2", "fallback", "accepted", ["primary_invalid_latex"]),
        (r"\frac{1}{2", r"\frac{1}{2", None, "review_required", ["primary_invalid_latex", "fallback_invalid_latex"]),
        ("x^2", "x^2", "primary", "accepted", []),
        ("x^2", "x^3", "primary", "review_required", ["model_disagreement"]),
    ],
)
def test_formula_selection_keeps_candidate_identity_and_reason_order(primary_latex, fallback_latex, chosen_name, status, reasons):
    primary = SimpleNamespace(canonical_latex=primary_latex)
    fallback = SimpleNamespace(canonical_latex=fallback_latex) if fallback_latex is not None else None

    selected, actual_status, actual_reasons = paper_formula._select_formula(primary, fallback, 0.99, 0)

    expected = {"primary": primary, "fallback": fallback, None: None}[chosen_name]
    assert selected is expected
    assert actual_status == status
    assert actual_reasons == reasons


@pytest.mark.parametrize("detector_offset,edge_offset", [(0, 0), (-0.01, 0), (0, 0.01)])
def test_formula_selection_keeps_threshold_boundaries(detector_offset, edge_offset):
    primary = SimpleNamespace(canonical_latex="x^2")
    selected, status, reasons = paper_formula._select_formula(
        primary, None,
        paper_formula.FORMULA_DETECTOR_ACCEPT_SCORE + detector_offset,
        paper_formula.FORMULA_EDGE_INK_THRESHOLD + edge_offset,
    )

    assert selected is primary
    assert status == ("accepted" if detector_offset == edge_offset == 0 else "review_required")
    expected = ["low_detector_confidence"] if detector_offset < 0 else []
    if edge_offset > 0:
        expected.append("roi_edge_ink")
    assert reasons == expected
