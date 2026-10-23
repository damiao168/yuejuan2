import base64
import copy
import io
import json
from pathlib import Path

import numpy as np
import pytest
from grading_agent.answer_sheet_vision import detect_printed_frames
from grading_agent.template_analysis_fill_groups import discover_fill_grading_groups
from PIL import Image


def _box(x, y, width, height):
    return {"x": x, "y": y, "width": width, "height": height}


def _fill(label, bbox):
    return {"role": "answer", "printed_question_label": label, "semantic_check": "supported",
            "association_status": "associated", "proposed_bbox": bbox}


def _fixture(monkeypatch):
    monkeypatch.setattr("grading_agent.template_analysis_fill_groups._measured_sections",
                        lambda *_: ([(100, 202, 900, 500)], [(100, 200, 800, 2), (100, 500, 800, 3)]))
    rows = [_fill("12", _box(.12, .21, .3, .1)), _fill("13", _box(.5, .21, .3, .1)),
            _fill("14", _box(.12, .4, .3, .1014))]
    image = np.full((1000, 1000, 3), 255, np.uint8)
    payload = {"fill_blank_question_labels": ["12", "13", "14"], "page": {"page_no": 1, "sha256": "a" * 64}}
    return rows, image, payload


def test_group_boundary_uses_measured_line_thickness_without_roi_union(monkeypatch):
    rows, image, payload = _fixture(monkeypatch)
    before = copy.deepcopy(rows)
    groups = discover_fill_grading_groups(rows, image, [], payload)
    assert rows == before
    assert len(groups) == 1
    assert groups[0]["question_labels"] == ["12", "13", "14"]
    assert groups[0]["bbox"] == _box(.1, .2, .8, .303)
    # 同一实测边线上的子框尾差不改变外框，避免候选抖动牵着共框移动。
    rows[2]["proposed_bbox"]["height"] = .1028
    assert discover_fill_grading_groups(rows, image, [], payload)[0]["bbox"] == groups[0]["bbox"]


def test_group_does_not_expand_past_measured_line_to_contain_child(monkeypatch):
    rows, image, payload = _fixture(monkeypatch)
    rows[2]["proposed_bbox"]["height"] = .104
    groups = discover_fill_grading_groups(rows, image, [], payload)
    assert all("14" not in group["question_labels"] for group in groups)
    assert all(group["bbox"]["y"] + group["bbox"]["height"] <= .503 for group in groups)


@pytest.mark.parametrize("role,reason", [("instruction", None), ("uncertain", None),
                                        ("other", "LOCAL_FORBIDDEN_LABEL_VERIFIED")])
def test_separator_edge_touch_is_not_interior_overlap(monkeypatch, role, reason):
    rows, image, payload = _fixture(monkeypatch)
    rows.append({"role": role, "proposed_bbox": _box(.1, .501, .8, .2),
                 "reason_codes": [reason] if reason else []})
    assert len(discover_fill_grading_groups(rows, image, [], payload)) == 1
    rows[-1]["proposed_bbox"]["y"] = .49
    assert discover_fill_grading_groups(rows, image, [], payload) == []


def test_normal_other_overlay_remains_nonblocking(monkeypatch):
    rows, image, payload = _fixture(monkeypatch)
    rows.append({"role": "other", "proposed_bbox": _box(.1, .2, .8, .3)})
    assert len(discover_fill_grading_groups(rows, image, [], payload)) == 1


def test_real_browser_original_pixels_share_fill_section_without_forbidden_area():
    root = Path(__file__).resolve().parents[2]
    source = root / "output/position-template-20261019/isolated/browser-input-page1.json"
    response = root / "output/template-live-incident-20261018/live-http-v8-final-page1-result-private.json"
    if not source.exists() or not response.exists():
        pytest.skip("private local replay assets are unavailable")
    fixture = json.loads(source.read_text(encoding="utf-8"))
    image = np.array(Image.open(io.BytesIO(base64.b64decode(fixture["data_base64"]))).convert("RGB"))
    rows = json.loads(response.read_text(encoding="utf-8"))["candidates"]
    payload = {"fill_blank_question_labels": ["12", "13", "14"], "page": {"page_no": 1, "sha256": fixture["sha256"]}}
    groups = discover_fill_grading_groups(rows, image, detect_printed_frames(image, 1), payload)
    assert len(groups) == 1
    assert groups[0]["question_labels"] == ["12", "13", "14"]
    bbox = groups[0]["bbox"]
    np.testing.assert_allclose([bbox["x"] * 1600, bbox["y"] * 1132,
                                (bbox["x"] + bbox["width"]) * 1600,
                                (bbox["y"] + bbox["height"]) * 1132], [107, 576, 557, 699])
    assert bbox["y"] + bbox["height"] < .675  # 下方禁止作答说明的上沿。
