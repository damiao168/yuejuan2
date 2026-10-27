import io
import json

import pytest
from ocr_worker.math_runner import _build_artifact
from ocr_worker.recognition_router import (
    PaddleFormulaNetEngine,
    RecognitionRouter,
    RegionKind,
)
from PIL import Image


class SyntheticPipeline:
    def __init__(self, score):
        self.score = score

    def predict(self, *, input, batch_size):
        return [{"res": {"rec_formula": "x=2", "rec_score": self.score}} for _ in input]


class OfflineVerifier:
    def normalize(self, latex):
        return {"ast": {"kind": "equation", "children": []}}


@pytest.mark.parametrize("score,expected,review", [
    (float("nan"), 0, True), (float("inf"), 0, True),
    (-float("inf"), 0, True), (0.5, 0.5, True), (0.9, 0.9, False),
])
def test_nonfinite_formula_confidence_keeps_review_required(score, expected, review):
    # 只替换模型和符号服务；真实识别路由与证据投影必须保留不确定性。
    engine = PaddleFormulaNetEngine(cache_size=0)
    engine._pipeline = SyntheticPipeline(score)
    with Image.new("RGB", (100, 40), "white") as image:
        stream = io.BytesIO()
        image.save(stream, format="PNG")
    routed = RecognitionRouter(text_engine=None, formula_engine=engine).recognize(
        "mathematics", RegionKind.FORMULA, stream.getvalue(),
    )
    artifact = _build_artifact({
        "answer_segment_id": "synthetic-segment", "exam_question_snapshot_id": "synthetic-question",
        "subject_code": "mathematics", "input_hash": "synthetic-hash",
    }, routed, OfflineVerifier())
    assert artifact["formulas"][0]["confidence"] == expected
    assert artifact["solution_graph"]["overall_confidence"] == expected
    assert artifact["solution_graph"]["requires_human_review"] is review
    json.dumps(artifact, allow_nan=False)
