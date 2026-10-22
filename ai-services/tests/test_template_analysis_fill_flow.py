"""固定模型/OCR 响应验证填空拆分在 v8 主流程中的时序，不代表真实模型验收。"""

import base64
import copy
import hashlib
from contextlib import nullcontext

import cv2
import numpy as np
import pytest

from grading_agent.config import Settings
from grading_agent.template_analysis import TemplateAnalyzer
from grading_agent.template_analysis_fill_regions import _measured_sections
from grading_agent.template_analysis_repair import repair_template_candidates
from grading_agent.template_analysis_schema import SCHEMA_VERSION
from grading_agent.template_analysis_semantics import LocalContentEvidence


class FixedFillModel:
    def __init__(self):
        self.settings = Settings()
        self.calls = []

    def session(self, _request_id):
        return nullcontext()

    def request_structured(self, _request_id, _messages, _schema, name):
        self.calls.append(name)
        if name != "answer_sheet_template_analysis":
            return {"candidates": []}
        return {"candidates": [{
            "role": "answer", "bbox": {"x": .125, "y": .1, "width": .75, "height": .3},
            "labels": ["12", "13", "14"], "identity_label": None, "static_label": "填空题",
            "option_pattern": "none", "identity_pattern": "none", "barcode_pattern": "none",
            "boundary_basis": "printed_frame", "anchor_bbox": None,
        }]}

    def last_usage(self):
        return {"input_tokens": 10, "output_tokens": 2, "total_tokens": 12}


def fill_fixture():
    image = np.full((1000, 800, 3), 255, np.uint8)
    cv2.rectangle(image, (100, 100), (700, 900), (0, 0, 0), 2)
    cv2.line(image, (100, 400), (700, 400), (0, 0, 0), 2)
    for left, right, y in [(190, 350, 222), (470, 650, 222), (190, 350, 332)]:
        cv2.line(image, (left, y), (right, y), (0, 0, 0), 1)
    raw = cv2.imencode(".png", image)[1].tobytes()
    payload = {
        "request_id": "fill-main-flow-fixture-0001", "schema_version": SCHEMA_VERSION,
        "page": {"page_no": 1, "width": 800, "height": 1000, "media_type": "image/png",
                 "data_base64": base64.b64encode(raw).decode(), "sha256": hashlib.sha256(raw).hexdigest()},
        "question_labels": ["12", "13", "14"], "objective_question_labels": [],
        "fill_blank_question_labels": ["12", "13", "14"], "writing_state": "blank", "candidates": [],
    }
    return image, payload


@pytest.mark.parametrize("complete_evidence", [True, False])
def test_v8_replaces_observed_fill_parent_before_model_repair_only_with_complete_local_evidence(
        monkeypatch, complete_evidence):
    image, payload = fill_fixture()
    events, review_inputs, repair_inputs = [], [], []
    monkeypatch.delenv("EDUGRADE_TEMPLATE_ANALYSIS_DIAGNOSTICS_DIR", raising=False)
    monkeypatch.delenv("EDUGRADE_TEMPLATE_ANALYSIS_CAPTURE_CONTENT", raising=False)
    monkeypatch.setattr("grading_agent.template_analysis.discover_barcode_regions", lambda *_args, **_kwargs: ([], []))
    monkeypatch.setattr("grading_agent.template_analysis.review_forbidden_regions", lambda *_args: [])
    monkeypatch.setattr("grading_agent.template_analysis_semantics.detect_option_groups", lambda *_args: ([], []))
    monkeypatch.setattr("grading_agent.template_analysis.review_template_page_coverage", lambda *_args, **_kwargs: {
        "observations": [], "uncovered": [], "usage": {},
        "metadata": {"status": "not_run", "request_count": 0, "reason_codes": []},
    })

    def evidence_factory(source, page):
        evidence = LocalContentEvidence(source, page, ocr=lambda _image: ([], "SEMANTIC_OCR_UNAVAILABLE"),
                                        barcode=lambda _image: None)
        review = evidence.review

        def record_review(rows, request):
            events.append("content_review")
            review_inputs.append(copy.deepcopy(rows))
            return review(rows, request)

        evidence.review = record_review
        return evidence

    def read_section(crop, *, timeout):
        events.append("local_fill_ocr")
        assert 0 < timeout <= 2
        # 固定 OCR 只提供印刷标题和括号题号；横线及分区边界仍由真实合成像素测量。
        from grading_agent.answer_sheet_vision import detect_printed_frames

        sections, _ = _measured_sections(image, detect_printed_frames(image, 1))
        section = next((section for section in sections
                        if crop.shape[:2] == (section[3] - section[1], section[2] - section[0])), None)
        if section is None:
            # 模型观察现在可安排短横线左侧补读；本场景刻意缺独立题号证据，补读不能生成14。
            return [], None
        left, top = section[:2]
        values = [("填空题", 120, 130, 60, "heading"), ("(12)题", 120, 200, 55, "first"),
                  ("(13)题", 400, 200, 55, "first"), ("(14)题", 120, 310, 55, "second")]
        if not complete_evidence:
            values = values[:-1]
        return [{"text": text, "x": x - left, "y": y - top, "width": width,
                 "height": 20, "confidence": 90, "line": line}
                for text, x, y, width, line in values], None

    def record_repair(*args, **kwargs):
        events.append("model_repair")
        repair_inputs.append(copy.deepcopy(args[3]))
        return repair_template_candidates(*args, **kwargs)

    monkeypatch.setattr("grading_agent.template_analysis_fill_regions._read_section", read_section)
    monkeypatch.setattr("grading_agent.template_analysis.repair_template_candidates", record_repair)
    model = FixedFillModel()
    result = TemplateAnalyzer(model, evidence_factory, prompt_variant="v8-layout").analyze(payload)

    assert events.index("content_review") < events.index("local_fill_ocr") < events.index("model_repair")
    assert review_inputs[0][0]["_fill_parent_labels"] == ["12", "13", "14"]
    assert not any("_fill_parent_labels" in row for row in result["candidates"])
    assert result["semantic_status"] == "completed"
    if complete_evidence:
        assert model.calls == ["answer_sheet_template_analysis"]
        assert result["model_usage"]["request_count"] == 1
        assert len(result["candidates"]) == 3
        assert [row["printed_question_label"] for row in result["candidates"]] == ["12", "13", "14"]
        assert repair_inputs[0] == result["candidates"]
        for row in result["candidates"]:
            assert row["origin"] == "local_detector"
            assert row["semantic_check"] == "supported" and row["association_status"] == "associated"
            assert "LOCAL_FILL_LAYOUT_VERIFIED" in row["reason_codes"]
            assert "LOCAL_FILL_REQUIRES_REVIEW" not in row["reason_codes"]
            assert row["proposed_bbox"]["y"] + row["proposed_bbox"]["height"] < .4
    else:
        # 少一题时不能把半套局部证据当作成功替换；保留父框，并允许模型继续尝试拆分。
        assert "answer_sheet_template_local_repair" in model.calls
        assert any(row.get("_fill_parent_labels") == ["12", "13", "14"] for row in repair_inputs[0])
        assert any(row["origin"] == "model" and row["semantic_check"] == "needs_review"
                   for row in result["candidates"])
        assert not any("LOCAL_FILL_LAYOUT_VERIFIED" in row["reason_codes"] for row in result["candidates"])
