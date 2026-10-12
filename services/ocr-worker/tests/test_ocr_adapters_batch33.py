from pathlib import Path
from types import SimpleNamespace

import pytest
from ocr_worker.api import EduGradeClient
from ocr_worker.engine import PaddleOCREngine
from ocr_worker.math_layout import PaddleFormulaLayoutDetector


@pytest.mark.parametrize(
    ("method", "args", "queue", "limit"),
    [
        ("claim_tasks", ("worker-33", 3, 17), "ocr", 3),
        ("claim_math_tasks", ("worker-33", 17), "math-understanding", 1),
        ("claim_paper_formula_tasks", ("worker-33", 17), "paper-formula", 1),
        ("claim_math_verification_tasks", ("worker-33", 17), "math-verification", 1),
    ],
)
def test_claim_entry_keeps_path_payload_and_instance_assignment(monkeypatch, method, args, queue, limit):
    client = EduGradeClient("http://api.invalid", "demo", "worker", "test-only")
    tasks = [{"id": "task-33"}]
    calls = []

    def request(verb, path, payload):
        calls.append((verb, path, payload, client.worker_instance_id))
        return {"tasks": tasks}

    monkeypatch.setattr(client, "_request", request)

    assert getattr(client, method)(*args) is tasks
    assert calls == [("POST", "/api/v1/internal/worker/tasks/claim", {
        "queue_name": queue,
        "worker_service": "ocr-worker",
        "worker_instance_id": "worker-33",
        "limit": limit,
        "lease_seconds": 17,
    }, "worker-33")]


@pytest.mark.parametrize("response", [{}, {"tasks": None}, {"tasks": "not-a-list"}])
def test_claim_nonlist_response_still_returns_empty_list(monkeypatch, response):
    client = EduGradeClient("http://api.invalid", "demo", "worker", "test-only")
    monkeypatch.setattr(client, "_request", lambda *_args: response)

    assert client.claim_tasks("worker-33", 3, 17) == []


def _detect(raw=None, *, error=None):
    paths = []

    def predict(*, input):
        path = Path(input)
        assert path.read_bytes() == b"layout-image"
        paths.append(path)
        if error is not None:
            raise error
        return raw

    detector = PaddleFormulaLayoutDetector("test-layout", "cpu")
    detector._model = SimpleNamespace(predict=predict)
    return detector, paths


@pytest.mark.parametrize("shape", ["dict", "list", "tuple", "iterator"])
def test_detect_entry_normalizes_result_container_and_removes_file(shape):
    # fake 只提供模型返回结构；测试不宣称真实 Paddle 检测能力。
    payload = {"boxes": [{"label": "formula", "score": 0.8, "coordinate": [10, 20, 40, 50]}]}
    raw = {
        "dict": payload,
        "list": [payload],
        "tuple": (payload,),
        "iterator": iter([payload]),
    }[shape]
    detector, paths = _detect(raw)

    assert detector.detect(b"layout-image", 0.45) == [([10.0, 20.0, 30.0, 30.0], 0.8)]
    assert len(paths) == 1
    assert not paths[0].exists()


def test_detect_callable_json_precedes_dict_and_unwraps_res():
    class Result(dict):
        def json(self):
            return {"res": {"layout_det_res": [
                {"category_name": "FORMULA", "confidence": 0.8, "bbox": [10, 20, 40, 50]},
            ]}}

    detector, _ = _detect([Result(boxes=[{"label": "formula", "score": "invalid"}])])

    assert detector.detect(b"layout-image", 0.45) == [([10.0, 20.0, 30.0, 30.0], 0.8)]


def test_detect_filters_labels_and_preserves_score_and_coordinate_boundaries():
    detector, _ = _detect({"boxes": [
        None,
        {"label": "formula_number", "score": "invalid"},
        {"label": "formula", "score": 0.44, "coordinate": [1, 2, 30, 20]},
        {"label": "formula", "score": 0.8, "coordinate": [1, 2, 30]},
        {"label": "formula", "score": 0.8, "coordinate": [1, 2, 2, 10]},
        {"label": "formula", "score": 0.45, "coordinate": [10, 20, 40, 50]},
        {"label": "formula", "score": 0.9, "coordinate": (80, 90, 20, 10)},
    ]})

    assert detector.detect(b"layout-image", 0.45) == [
        ([10.0, 20.0, 30.0, 30.0], 0.45),
        ([80.0, 90.0, 20.0, 10.0], 0.9),
    ]


@pytest.mark.parametrize("raw", [[None, "ignored"], {"res": None}])
def test_detect_nonpayload_output_keeps_empty_result(raw):
    detector, _ = _detect(raw)

    assert detector.detect(b"layout-image", 0.45) == []


@pytest.mark.parametrize(
    ("raw", "error_type"),
    [
        ({"boxes": None}, TypeError),
        ({"boxes": [{"label": "formula", "score": "invalid"}]}, ValueError),
        ({"boxes": [{"label": "formula", "score": 0.8, "coordinate": [1, 2, "invalid", 4]}]}, ValueError),
    ],
)
def test_detect_invalid_payload_still_propagates_original_exception(raw, error_type):
    detector, paths = _detect(raw)

    with pytest.raises(error_type):
        detector.detect(b"layout-image", 0.45)
    assert not paths[0].exists()


@pytest.mark.parametrize("failure", ["predict", "iterator"])
def test_detect_cleans_file_when_prediction_or_materialization_fails(failure):
    def failing_output():
        yield {"boxes": []}
        raise RuntimeError("iterator failure")

    detector, paths = _detect(
        failing_output(),
        error=RuntimeError("predict failure") if failure == "predict" else None,
    )

    with pytest.raises(RuntimeError, match=f"{failure} failure"):
        detector.detect(b"layout-image", 0.45)
    assert not paths[0].exists()


def test_detect_unlinks_prediction_file_before_json_accessor_runs():
    paths = []

    class Result:
        def json(self):
            assert not paths[0].exists()
            raise RuntimeError("json failure")

    detector, captured = _detect([Result()])
    paths = captured

    with pytest.raises(RuntimeError, match="json failure"):
        detector.detect(b"layout-image", 0.45)
    assert not paths[0].exists()


def _recognize(raw):
    paths = []

    def predict(path):
        image_path = Path(path)
        assert image_path.read_bytes() == b"ocr-image"
        paths.append(image_path)
        return raw

    engine = PaddleOCREngine()
    engine._ocr = SimpleNamespace(predict=predict)
    try:
        return engine.recognize(b"ocr-image")
    finally:
        assert len(paths) == 1
        assert not paths[0].exists()


@pytest.mark.parametrize(
    "raw",
    [
        [[[[[10, 20], [40, 20], [40, 50], [10, 50]], ("answer", 0.8)]]],
        [{"rec_texts": ["answer"], "rec_scores": [0.8], "rec_polys": [[[10, 20], [40, 20], [40, 50], [10, 50]]]}],
    ],
)
def test_recognize_entry_preserves_legacy_and_paddle3_output(raw):
    blocks = _recognize(raw)

    assert [(block.text, block.bbox, block.confidence) for block in blocks] == [
        ("answer", [10.0, 20.0, 30.0, 30.0], 0.8),
    ]


def test_recognize_callable_json_precedes_dictionary_payload():
    class Result(dict):
        def json(self):
            return {"rec_texts": ["json-text"], "rec_scores": [0.8]}

    blocks = _recognize([Result(rec_texts=["dict-text"], rec_scores=["invalid"])])

    assert [(block.text, block.confidence) for block in blocks] == [("json-text", 0.8)]


def test_recognize_nested_order_short_arrays_and_missing_geometry_are_unchanged():
    blocks = _recognize([{
        "first": {"rec_texts": ["first"], "rec_scores": [0.6], "rec_polys": [], "dt_polys": [[[1, 2], [4, 6]]]},
        "ignored": [{"rec_texts": ["not-a-dict-child"]}],
        "second": {"rec_texts": ["", None, "last"], "rec_scores": [0.2, 0.4]},
    }])

    assert [(block.text, block.bbox, block.confidence) for block in blocks] == [
        ("first", [1.0, 2.0, 3.0, 4.0], 0.6),
        ("None", [0.0, 0.0, 0.0, 0.0], 0.4),
        ("last", [0.0, 0.0, 0.0, 0.0], 0.0),
    ]


def test_recognize_legacy_items_keep_list_only_and_text_tuple_rules():
    blocks = _recognize([[
        None,
        ([], ("ignored", 0.9)),
        [],
        [[], ("", 0.2)],
        [[], ["last", 0.7]],
    ]])

    assert [(block.text, block.bbox, block.confidence) for block in blocks] == [
        ("last", [0.0, 0.0, 0.0, 0.0], 0.7),
    ]


@pytest.mark.parametrize(
    "raw",
    [
        [[[[], ("answer", "invalid")]]],
        [{"rec_texts": ["answer"], "rec_scores": ["invalid"]}],
    ],
)
def test_recognize_invalid_numeric_confidence_keeps_value_error(raw):
    with pytest.raises(ValueError):
        _recognize(raw)


@pytest.mark.parametrize("raw", [None, {}, ({"rec_texts": ["ignored"]},)])
def test_recognize_nonlist_top_level_keeps_empty_result(raw):
    assert _recognize(raw) == []
