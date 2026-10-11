import io

from ocr_worker.engine import OCRBlock
from ocr_worker.recognition_router import (
    FormulaRecognitionResult,
    PaddleFormulaNetEngine,
    RecognitionRouter,
    RegionKind,
    UniMERNetEngine,
    _parse_formula_prediction,
)
from PIL import Image


class TextEngine:
    model_version = "text-v1"
    device = "cpu"

    def initialize(self):
        return None

    def recognize(self, _image):
        return [OCRBlock("所以", [0, 0, 10, 10], 0.9)]


class FormulaEngine:
    engine_name = "fixture"
    model_version = "formula-v1"

    def initialize(self):
        return None

    def recognize_formula(self, _image):
        return FormulaRecognitionResult(" x = 2 ", "x=2", 0.9, self.engine_name, self.model_version, "recognized")

    def recognize_formulas(self, images, batch_size=4):
        self.batch = (len(images), batch_size)
        return [self.recognize_formula(image) for image in images]


class FormulaDetector:
    model_version = "layout-v1"
    initialized = True

    def initialize(self):
        return None

    def detect(self, _image, min_score):
        assert min_score == 0.45
        return [([60, 10, 40, 20], 0.92)]


def png(width=100, height=40):
    image = Image.new("RGB", (width, height), "white")
    output = io.BytesIO()
    image.save(output, format="PNG")
    image.close()
    return output.getvalue()


def test_routes_regions_and_fails_closed():
    router = RecognitionRouter(text_engine=TextEngine(), formula_engine=FormulaEngine())
    assert router.recognize("mathematics", RegionKind.TEXT, b"image").text_blocks[0].text == "所以"
    assert router.recognize("mathematics", RegionKind.FORMULA, b"image").formula.canonical_latex == "x=2"
    diagram = router.recognize("mathematics", RegionKind.DIAGRAM, b"image")
    assert diagram.preserve_image_evidence
    assert diagram.requires_human_review
    assert router.recognize("mathematics", RegionKind.UNKNOWN, b"image").requires_human_review


def test_missing_formula_engine_never_falls_back_to_text():
    result = RecognitionRouter(text_engine=TextEngine(), formula_engine=None).recognize("physics", RegionKind.FORMULA, b"image")
    assert result.requires_human_review
    assert result.reason_code == "formula_engine_unavailable"
    assert not result.text_blocks


def test_humanities_never_invoke_formula_model():
    class ExplodingFormulaEngine(FormulaEngine):
        def recognize_formula(self, _image):
            raise AssertionError("formula engine must not run for humanities")

    router = RecognitionRouter(text_engine=TextEngine(), formula_engine=ExplodingFormulaEngine())
    for subject in ("chinese", "english", "history", "ethics_politics", "geography", "biology"):
        result = router.recognize(subject, RegionKind.FORMULA, b"image")
        assert result.reason_code == "formula_not_enabled_for_subject"
        assert result.preserve_image_evidence
        assert result.requires_human_review


def test_mixed_route_runs_full_ocr_and_batched_formula_rois_without_duplicate_text():
    class MixedTextEngine(TextEngine):
        def recognize(self, _image):
            return [OCRBlock("解：由题意得x2=2", [0, 10, 100, 20], 0.9)]

    formula_engine = FormulaEngine()
    router = RecognitionRouter(
        text_engine=MixedTextEngine(),
        formula_engine=formula_engine,
        formula_detector=FormulaDetector(),
        formula_padding=0,
        formula_batch_size=3,
    )
    result = router.recognize("mathematics", RegionKind.MIXED, png())

    assert result.image_size == (100, 40)
    assert [block.text for block in result.text_blocks] == ["解：由题意得"]
    assert result.text_blocks[0].bbox == [0.0, 10.0, 60.0, 20.0]
    assert result.formula_regions[0].bbox == (60.0, 10.0, 40.0, 20.0)
    assert result.formula_regions[0].formula.canonical_latex == "x=2"
    assert formula_engine.batch == (1, 3)
    assert not result.requires_human_review


def test_mixed_route_preserves_text_and_fails_closed_without_layout_detector():
    result = RecognitionRouter(text_engine=TextEngine(), formula_engine=FormulaEngine()).recognize(
        "physics", RegionKind.MIXED, png(),
    )
    assert result.text_blocks[0].text == "所以"
    assert result.requires_human_review
    assert result.preserve_image_evidence
    assert result.reason_code == "formula_layout_detector_unavailable"


def test_paddle_formula_result_and_unimernet_adapter():
    latex, confidence = _parse_formula_prediction([{"res": {"rec_formula": "x^2", "rec_score": 0.8}}])
    assert (latex, confidence) == ("x^2", 0.8)
    challenger = UniMERNetEngine(model_version="checkpoint-1", predictor=lambda _: (" y = 3 ", 0.7))
    assert challenger.recognize_formula(b"image").canonical_latex == "y=3"


def test_formula_engine_deduplicates_batch_and_reuses_lru_cache():
    engine = PaddleFormulaNetEngine(model_version="PP-FormulaNet_plus-M", cache_size=4)
    calls = []

    def predict(images, *, batch_size):
        calls.append((len(images), batch_size))
        return [FormulaRecognitionResult("x^2", "x^{2}", 0, "paddle-formula", engine.model_version, "recognized") for _ in images]

    engine._predict_uncached = predict
    first = engine.recognize_formulas([b"same", b"same", b"different"], batch_size=4)
    second = engine.recognize_formulas([b"same"], batch_size=4)
    assert len(first) == 3
    assert len(second) == 1
    assert calls == [(2, 4)]
