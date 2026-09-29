import io
from types import SimpleNamespace

from ocr_worker.formula_validation import (
    FormulaAction,
    FormulaValidator,
    normalize_latex,
    validate_latex_structure,
)
from ocr_worker.math_layout import _adaptive_crop_formula, _merge_formula_boxes
from ocr_worker.paper_formula import (
    _bucketed_batches,
    _needs_fallback,
    _select_formula,
    _valid_formula,
)
from PIL import Image, ImageDraw


def result(latex: str, model: str = "PP-FormulaNet_plus-M") -> SimpleNamespace:
    return SimpleNamespace(canonical_latex=latex, raw_latex=latex, confidence=0.9, engine_version=model)


def test_formula_gate_accepts_valid_high_confidence_roi() -> None:
    selected, status, reasons = _select_formula(result(r"x^{2}+1"), None, 0.91, 0.01)
    assert selected.canonical_latex == r"x^{2}+1"
    assert status == "accepted"
    assert reasons == []


def test_missing_uncalibrated_model_confidence_does_not_force_fallback() -> None:
    primary = result(r"x^{2}+1")
    primary.confidence = 0.0
    assert not _needs_fallback(primary, 0.91, 0.01)


def test_detector_and_crop_failures_never_invoke_large_model() -> None:
    invalid = result(r"\frac{1}{2")
    assert not _needs_fallback(invalid, 0.53, 0.01)
    assert not _needs_fallback(invalid, 0.91, 0.20)
    assert _needs_fallback(invalid, 0.91, 0.01)


def test_formula_gate_requires_review_when_models_disagree() -> None:
    selected, status, reasons = _select_formula(
        result(r"x^{2}"), result(r"x^{3}", "PP-FormulaNet_plus-L"), 0.92, 0.01,
    )
    assert selected.canonical_latex == r"x^{2}"
    assert status == "review_required"
    assert "model_disagreement" in reasons


def test_formula_syntax_and_fragment_merge() -> None:
    assert _valid_formula(r"\frac{1}{2}")
    assert not _valid_formula(r"\frac{1}{2")
    assert _merge_formula_boxes([([10, 10, 20, 10], 0.8), ([34, 11, 15, 9], 0.9)]) == [([10, 10, 39, 10], 0.9)]


def test_latex_normalizer_and_parser_cover_printed_math() -> None:
    assert normalize_latex(r"\left(\tfrac {1}{2}+x^2\right)") == r"(\frac{1}{2}+x^{2})"
    assert normalize_latex("x²+2x+1") == r"x^{2}+2x+1"
    assert validate_latex_structure(r"\begin{cases}x+1&x>0\\0&x\leq0\end{cases}")[:2] == (True, True)
    assert not validate_latex_structure(r"\input{secret}")[1]
    assert not validate_latex_structure("得 f(x)在")[1]
    assert not validate_latex_structure(r"\{a_n\}公")[1]
    assert validate_latex_structure(r"x\in\text{实数}")[:2] == (True, True)
    assert normalize_latex(r"A=\left\{x\left|-2<x<2\right.\right\}") == r"A=\{x|-2<x<2\}"
    assert normalize_latex(r"\left.0.25\right|") == "0.25|"


# 同一合成图同时充当源图和渲染结果，仅验证阈值分流，不衡量公式识别准确率。
def test_render_validation_routes_only_visual_mismatch_to_l() -> None:
    image = Image.new("L", (40, 20), "white")
    ImageDraw.Draw(image).line((5, 10, 35, 10), fill="black", width=2)
    output = io.BytesIO()
    image.save(output, format="PNG")
    crop = output.getvalue()
    accepted = FormulaValidator(renderer=lambda _latex: crop).validate(
        "x+1", crop, detector_score=.9, crop_complete=True,
    )
    rejected = FormulaValidator(renderer=lambda _latex: b"").validate(
        "x+1", crop, detector_score=.9, crop_complete=True,
    )
    assert accepted.action is FormulaAction.ACCEPT and accepted.render_similarity is not None and accepted.render_similarity > .99
    assert rejected.action is FormulaAction.RETRY_L and not rejected.render_valid


def test_fraction_gate_rejects_an_entire_equation_misread_as_numerator() -> None:
    wrong = r"\frac{S_{10}=10a_{1}+\frac{10\times9}{2}d=70-90=-20}{2}"
    assert "fraction_contains_relation" in validate_latex_structure(wrong)[2]
    assert not validate_latex_structure(wrong)[1]
    assert validate_latex_structure(r"\frac{\sum_{i=1}^{n}a_i}{n}")[1]
    assert validate_latex_structure(r"\frac{x-3}{x+1}\leq0")[1]


def test_missing_render_validator_never_auto_accepts_formula() -> None:
    missing = FormulaValidator(renderer=lambda _latex: None).validate(
        "x+1", b"unused", detector_score=.9, crop_complete=True,
    )
    assert missing.action is FormulaAction.REVIEW
    assert "render_validator_unavailable" in missing.reason_codes


def test_adaptive_crop_expands_clipped_formula_before_recognition() -> None:
    image = Image.new("RGB", (100, 60), "white")
    ImageDraw.Draw(image).line((30, 20, 70, 20), fill="black", width=2)
    _, bbox, edge_ink, recrops, complete = _adaptive_crop_formula(image, [30, 20, 40, 20], 0, max_padding=32)
    assert recrops > 0
    assert bbox[0] < 30 and bbox[1] < 20
    assert edge_ink <= .08 and complete


def test_adaptive_crop_never_turns_short_inline_formula_into_paragraph_crop() -> None:
    image = Image.new("RGB", (400, 200), "black")
    _, bbox, _, recrops, complete = _adaptive_crop_formula(
        image, [100, 80, 180, 20], 12, max_padding=96, max_padding_height_ratio=.75,
    )
    assert recrops == 1
    assert bbox == [85.0, 65.0, 210.0, 50.0]
    assert not complete


def test_formula_batches_are_bounded_and_aspect_bucketed() -> None:
    crops = [{"aspect_ratio": ratio} for ratio in (0.5, 0.6, 4.0, 5.0, 4.5)]
    batches = _bucketed_batches(crops, list(range(len(crops))), 2)
    assert all(len(batch) <= 2 for batch in batches)
    assert sorted(index for batch in batches for index in batch) == list(range(len(crops)))
