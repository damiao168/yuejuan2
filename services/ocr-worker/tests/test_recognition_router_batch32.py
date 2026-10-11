import io
from types import SimpleNamespace
from unittest.mock import Mock

import pytest
from ocr_worker import recognition_router as module
from ocr_worker.engine import OCRBlock
from ocr_worker.math_layout import FormulaRegion
from ocr_worker.recognition_router import FormulaRecognitionResult, PaddleFormulaNetEngine, RecognitionRouter, RegionKind
from PIL import Image


def formula(latex: str = "x=2", *, confidence: float = 0.9, status: str = "recognized", warnings: tuple[str, ...] = ()) -> FormulaRecognitionResult:
    return FormulaRecognitionResult(latex, latex, confidence, "fixture", "fixture-v1", status, warnings)


def region(index: int = 0, *, score: float = 0.9, complete: bool = True) -> FormulaRegion:
    bbox = (40.0 + index * 20, 10.0, 15.0, 20.0)
    return FormulaRegion(bbox, bbox, score, f"crop-{index}".encode(), complete, 0.0, 0)


def png() -> bytes:
    with Image.new("RGB", (100, 50), "white") as image:
        output = io.BytesIO()
        image.save(output, "PNG")
    return output.getvalue()


def text_engine(*, failed: bool = False) -> SimpleNamespace:
    return SimpleNamespace(recognize=Mock(side_effect=RuntimeError("text failed") if failed else None, return_value=[OCRBlock("说明", [0, 0, 20, 10], 0.9)]))


def mixed_router(monkeypatch: pytest.MonkeyPatch, engine: object, regions: list[FormulaRegion], *, text_failed: bool = False) -> RecognitionRouter:
    # 固定上游布局结果，不模拟真实 Paddle 检测；公开入口仍执行资格、候选、警告和文本合并。
    monkeypatch.setattr(module, "extract_formula_regions", lambda *_args, **_kwargs: ((100, 50), regions))
    return RecognitionRouter(text_engine=text_engine(failed=text_failed), formula_engine=engine, formula_detector=SimpleNamespace(model_version="layout-v1"), formula_batch_size=3)


@pytest.mark.parametrize("kind", list(RegionKind))
def test_empty_region_returns_before_text_or_formula_calls(kind: RegionKind) -> None:
    text = text_engine()
    engine = SimpleNamespace(recognize_formula=Mock(), recognize_formulas=Mock())
    result = RecognitionRouter(text_engine=text, formula_engine=engine).recognize("mathematics", kind, b"")
    assert result.kind is kind
    assert result.reason_code == "empty_region"
    assert result.requires_human_review
    text.recognize.assert_not_called()
    engine.recognize_formula.assert_not_called()
    engine.recognize_formulas.assert_not_called()


@pytest.mark.parametrize("error_type", [RuntimeError, ValueError])
def test_direct_formula_runtime_failure_keeps_original_review_reason(error_type: type[Exception]) -> None:
    text = text_engine()
    engine = SimpleNamespace(recognize_formula=Mock(side_effect=error_type("model failed")))
    result = RecognitionRouter(text_engine=text, formula_engine=engine).recognize("mathematics", RegionKind.FORMULA, png())
    assert result.reason_code == "formula_engine_failed"
    assert result.requires_human_review
    assert result.formula is None
    text.recognize.assert_not_called()


def test_none_direct_formula_result_keeps_original_attribute_error() -> None:
    engine = SimpleNamespace(recognize_formula=Mock(return_value=None))
    router = RecognitionRouter(text_engine=text_engine(), formula_engine=engine)
    image_bytes = png()
    with pytest.raises(AttributeError):
        router.recognize('mathematics', RegionKind.FORMULA, image_bytes)


def test_non_enum_kind_keeps_unknown_region_route() -> None:
    engine = SimpleNamespace(recognize_formula=Mock())
    result = RecognitionRouter(text_engine=text_engine(), formula_engine=engine).recognize("mathematics", "formula", png())
    assert result.kind is RegionKind.UNKNOWN
    assert result.reason_code == "unknown_region"
    engine.recognize_formula.assert_not_called()


def test_zero_confidence_remains_different_between_direct_and_mixed(monkeypatch: pytest.MonkeyPatch) -> None:
    prediction = formula(confidence=0)
    engine = SimpleNamespace(recognize_formula=Mock(return_value=prediction), recognize_formulas=Mock(return_value=[prediction]))
    router = mixed_router(monkeypatch, engine, [region()])
    direct = router.recognize("mathematics", RegionKind.FORMULA, png())
    mixed = router.recognize("mathematics", RegionKind.MIXED, png())
    assert direct.requires_human_review
    assert direct.reason_code == "ambiguous_formula"
    assert not mixed.requires_human_review
    assert mixed.reason_code == ""
    assert mixed.formula_regions[0].formula is prediction


def test_mixed_ineligible_regions_keep_order_and_warnings_without_inference(monkeypatch: pytest.MonkeyPatch) -> None:
    engine = SimpleNamespace(recognize_formulas=Mock())
    regions = [region(0, score=0.2, complete=False), region(1, complete=False)]
    result = mixed_router(monkeypatch, engine, regions).recognize("mathematics", RegionKind.MIXED, png())
    engine.recognize_formulas.assert_not_called()
    assert [item.bbox for item in result.formula_regions] == [item.bbox for item in regions]
    assert result.formula_regions[0].warnings == ("low_detector_confidence", "incomplete_formula_crop")
    assert result.formula_regions[1].warnings == ("incomplete_formula_crop",)
    assert all(item.candidates == () and item.selected_candidate is None for item in result.formula_regions)
    assert result.reason_code == "ambiguous_formula"
    assert result.preserve_image_evidence
    assert result.requires_human_review


@pytest.mark.parametrize("predictions", [[], None])
def test_mixed_incomplete_or_invalid_batch_keeps_failure_priority(monkeypatch: pytest.MonkeyPatch, predictions: object) -> None:
    engine = SimpleNamespace(recognize_formulas=Mock(return_value=predictions))
    result = mixed_router(monkeypatch, engine, [region()]).recognize("mathematics", RegionKind.MIXED, png())
    engine.recognize_formulas.assert_called_once_with([b"crop-0"], batch_size=3)
    assert result.reason_code == "formula_engine_failed"
    assert result.formula_regions[0].warnings == ("formula_engine_failed",)
    assert result.formula_regions[0].candidates == ()
    assert result.text_blocks[0].text == "说明"


def test_mixed_fallback_aligns_eligible_crops_and_survives_text_failure(monkeypatch: pytest.MonkeyPatch) -> None:
    predictions = [formula("x=1", warnings=("model-warning", "model-warning")), formula("y=2")]
    engine = SimpleNamespace(recognize_formula=Mock(side_effect=predictions))
    regions = [region(0), region(1, score=0.2), region(2)]
    result = mixed_router(monkeypatch, engine, regions, text_failed=True).recognize("mathematics", RegionKind.MIXED, png())
    assert [call.args[0] for call in engine.recognize_formula.call_args_list] == [b"crop-0", b"crop-2"]
    assert result.text_blocks == ()
    assert [item.formula for item in result.formula_regions] == [predictions[0], None, predictions[1]]
    assert result.formula_regions[0].warnings == ("model-warning",)
    assert result.reason_code == "ambiguous_formula"


def test_mixed_none_batch_item_remains_ambiguous_without_runtime_failure(monkeypatch: pytest.MonkeyPatch) -> None:
    engine = SimpleNamespace(recognize_formulas=Mock(return_value=[None]))
    result = mixed_router(monkeypatch, engine, [region()]).recognize("mathematics", RegionKind.MIXED, png())
    assert result.reason_code == "ambiguous_formula"
    assert result.formula_regions[0].warnings == ()
    assert result.formula_regions[0].candidates == ()
    assert result.requires_human_review


def test_mixed_missing_engine_warning_order_precedes_ambiguity(monkeypatch: pytest.MonkeyPatch) -> None:
    result = mixed_router(monkeypatch, None, [region(score=0.2, complete=False)]).recognize("mathematics", RegionKind.MIXED, png())
    assert result.reason_code == "formula_engine_unavailable"
    assert result.formula_regions[0].warnings == ("low_detector_confidence", "incomplete_formula_crop", "formula_engine_unavailable")


def test_formula_batch_aligns_empty_duplicates_and_hits_to_original_inputs() -> None:
    engine = PaddleFormulaNetEngine(cache_size=2)
    predicted = Mock(side_effect=lambda images, *, batch_size: [formula(image.decode()) for image in images])
    engine._predict_uncached = predicted
    first = engine.recognize_formulas([b"B", b"", b"A", b"B"], batch_size=3)
    assert [item.canonical_latex for item in first] == ["B", "", "A", "B"]
    assert first[1].warnings == ("empty_image",)
    assert first[0] is first[3]
    predicted.assert_called_once_with([b"B", b"A"], batch_size=3)
    second = engine.recognize_formulas([b"A", b"B"])
    assert [item.canonical_latex for item in second] == ["A", "B"]
    assert predicted.call_count == 1


def test_formula_cache_hits_promote_before_lru_eviction() -> None:
    engine = PaddleFormulaNetEngine(cache_size=2)
    predicted = Mock(side_effect=lambda images, *, batch_size: [formula(image.decode()) for image in images])
    engine._predict_uncached = predicted
    engine.recognize_formulas([b"A", b"B"])
    engine.recognize_formulas([b"A"])
    engine.recognize_formulas([b"C"])
    engine.recognize_formulas([b"A"])
    engine.recognize_formulas([b"B"])
    assert [call.args[0] for call in predicted.call_args_list] == [[b"A", b"B"], [b"C"], [b"B"]]


@pytest.mark.parametrize(("cache_size", "status"), [(0, "recognized"), (2, "failed")])
def test_formula_cache_disabled_or_failed_result_runs_again(cache_size: int, status: str) -> None:
    engine = PaddleFormulaNetEngine(cache_size=cache_size)
    engine._predict_uncached = Mock(return_value=[formula(status=status)])
    engine.recognize_formulas([b"A", b"A"])
    engine.recognize_formulas([b"A"])
    assert engine._predict_uncached.call_count == 2


def test_formula_short_batch_does_not_cache_new_misses() -> None:
    engine = PaddleFormulaNetEngine(cache_size=2)
    predicted = Mock(side_effect=[[formula("A")], [], [formula("B")]])
    engine._predict_uncached = predicted
    engine.recognize_formulas([b"A"])
    with pytest.raises(RuntimeError, match="formula batch returned 0 results for 1 inputs"):
        engine.recognize_formulas([b"A", b"B"])
    result = engine.recognize_formulas([b"A", b"B"])
    assert [item.canonical_latex for item in result] == ["A", "B"]
    assert [call.args[0] for call in predicted.call_args_list] == [[b"A"], [b"B"], [b"B"]]


@pytest.mark.parametrize(("images", "batch_size", "error_type"), [([], 0, ValueError), (["not-bytes"], 1, TypeError)])
def test_formula_batch_keeps_invalid_input_errors_before_prediction(images: list, batch_size: int, error_type: type[Exception]) -> None:
    engine = PaddleFormulaNetEngine()
    engine._predict_uncached = Mock()
    with pytest.raises(error_type):
        engine.recognize_formulas(images, batch_size=batch_size)
    engine._predict_uncached.assert_not_called()


def test_formula_batch_none_prediction_keeps_original_type_error() -> None:
    engine = PaddleFormulaNetEngine()
    engine._predict_uncached = Mock(return_value=None)
    with pytest.raises(TypeError):
        engine.recognize_formulas([b"A"])


@pytest.mark.parametrize("cache_size", [0, 2])
def test_formula_none_item_preserves_cache_dependent_original_result(cache_size: int) -> None:
    engine = PaddleFormulaNetEngine(cache_size=cache_size)
    engine._predict_uncached = Mock(return_value=[None])
    if cache_size:
        with pytest.raises(AttributeError):
            engine.recognize_formulas([b"A"])
    else:
        result = engine.recognize_formulas([b"A"])
        assert result[0].warnings == ("missing_batch_result",)
        assert result[0].status == "failed"
