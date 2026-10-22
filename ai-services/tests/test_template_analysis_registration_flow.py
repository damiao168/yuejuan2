"""定位基准先于模型输入；输出逆变换后仍对应教师原始画布。"""

import copy
import hashlib

import numpy as np
import pytest
from grading_agent.template_analysis import _restore_registered_analysis
from grading_agent.template_analysis_fill_groups import discover_fill_grading_groups


def box(x, y, width, height):
    return {"x":x,"y":y,"width":width,"height":height}


def test_inverse_registration_restores_source_question_and_option_positions():
    original = {"width": 1000, "height": 800, "sha256": "a" * 64}
    registration = {"status": "detected", "width": 1000, "height": 800,
                    "stable_quad": [{"x":x,"y":y} for x, y in ((.1,.1),(.9,.1),(.9,.9),(.1,.9))]}
    mapping = {"registration": registration, "rectified_to_source": [[1,0,10],[0,1,20],[0,0,1]]}
    first = {"proposed_bbox": box(.2,.2,.4,.3), "source_bbox": box(.2,.2,.4,.3),
             "objective_items": [{"bbox": box(.25,.25,.2,.05), "options": [{"bbox": box(.3,.26,.03,.02)}]}]}
    outside = {"proposed_bbox": box(.01,.02,.02,.02)}
    result = {"source_sha256": "b" * 64, "candidates": [copy.deepcopy(first), outside],
              "fill_grading_groups": [{"bbox": box(.2,.2,.4,.3)}]}
    restored = _restore_registered_analysis(result, mapping, original)
    assert restored["source_sha256"] == original["sha256"]
    assert len(restored["candidates"]) == 1
    actual = restored["candidates"][0]
    assert actual["proposed_bbox"]["x"] == pytest.approx(.21)
    assert actual["proposed_bbox"]["y"] == pytest.approx(.225)
    assert actual["objective_items"][0]["options"][0]["bbox"]["x"] == pytest.approx(.31)
    assert restored["fill_grading_groups"][0]["bbox"] == actual["proposed_bbox"]


def fill(label, bounds):
    return {"role": "answer", "printed_question_label": label, "semantic_check": "supported",
            "association_status": "associated", "proposed_bbox": bounds}


def test_shared_fill_area_uses_measured_separator_and_keeps_individual_regions(monkeypatch):
    image = np.full((500,800,3),255,np.uint8)
    rows = [fill("12",box(.15,.25,.1,.08)), fill("13",box(.3,.25,.1,.08)), fill("14",box(.15,.36,.1,.08))]
    before = copy.deepcopy(rows)
    monkeypatch.setattr("grading_agent.template_analysis_fill_groups._measured_sections", lambda *_: ([(80,100,400,225)], []))
    payload = {"fill_blank_question_labels": ["12","13","14"], "page": {"page_no":1,"sha256":hashlib.sha256(b"sample").hexdigest()}}
    groups = discover_fill_grading_groups(rows,image,[],payload)
    assert rows == before
    assert len(groups) == 1
    assert groups[0]["question_labels"] == ["12","13","14"]
    assert groups[0]["bbox"] == box(.1,.2,.4,.25)
    assert groups[0]["reason_codes"] == ["LOCAL_FILL_SECTION_VERIFIED"]


def test_shared_fill_area_does_not_swallow_instruction_or_unknown_region(monkeypatch):
    image = np.full((500,800,3),255,np.uint8)
    monkeypatch.setattr("grading_agent.template_analysis_fill_groups._measured_sections", lambda *_: ([(80,100,400,225)], []))
    rows = [fill("12",box(.15,.25,.1,.08)), fill("13",box(.3,.25,.1,.08)),
            {"role":"instruction","proposed_bbox":box(.2,.35,.1,.05)}]
    payload = {"fill_blank_question_labels":["12","13"],"page":{"page_no":1,"sha256":"a"*64}}
    assert discover_fill_grading_groups(rows,image,[],payload) == []
