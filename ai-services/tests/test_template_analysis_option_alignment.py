import copy

import cv2
import numpy as np
import pytest
from grading_agent.choice_grid_detection import detect_option_groups
from grading_agent.template_analysis_objective_items import review_objective_items
from grading_agent.template_analysis_option_alignment import (
    align_objective_options,
    merge_objective_groups,
)


def _box(x, y, width, height):
    return {"x": x / 800, "y": y / 600, "width": width / 800, "height": height / 600}


def _fixture(*, obscured=True, missing=False):
    """合成稀疏三列、两段共边选择题；模型题号正确，但所有坐标为另一种尺度和留白。"""
    image = np.full((600, 800, 3), 255, np.uint8)
    points = [(100, 120), (100, 160), (100, 200), (330, 120), (330, 160), (330, 200),
              (560, 120), (560, 160), (100, 320), (100, 360), (330, 320)]
    for number, (x, y) in enumerate(points):
        for index in range(4):
            if missing and number == 8 and index == 3:
                continue
            left = x + index * 40
            if index == number % 3:
                cv2.rectangle(image, (left, y - 1), (left + 26, y + 11), (0, 0, 0), -1)
                continue
            for edge, direction in ((left, 1), (left + 26, -1)):
                cv2.line(image, (edge, y), (edge, y + 10), (0, 0, 0), 1)
                cv2.line(image, (edge, y), (edge + direction * 4, y), (0, 0, 0), 1)
                cv2.line(image, (edge, y + 10), (edge + direction * 4, y + 10), (0, 0, 0), 1)
            cv2.putText(image, "ABCD"[index], (left + 10, y + 8), cv2.FONT_HERSHEY_SIMPLEX, .25, (0, 0, 0), 1)
    if obscured and not missing:
        # D 右括号的下端被彩色批注覆盖，仍有完整左括号和右竖干/上端；不能复原红线下的黑墨。
        cv2.rectangle(image, (244, 329), (250, 332), (255, 0, 0), -1)
    items = []
    for index, (x, y) in enumerate(points):
        model_x = .10 if x == 100 else .50 if x == 330 else .79
        model_y = {120: .15, 160: .25, 200: .35, 320: .60, 360: .70}[y]
        items.append({"printed_question_label": str(index + 1),
                      "bbox": {"x": model_x, "y": model_y, "width": .03, "height": .03},
                      "options": [{"label": name, "bbox": {"x": model_x + option * .025,
                                  "y": model_y, "width": .015, "height": .025}}
                                  for option, name in enumerate("ABCD")]})
    row = {"role": "objective_group", "proposed_bbox": _box(40, 60, 720, 190),
           "observed_question_labels": [item["printed_question_label"] for item in items], "objective_items": items}
    frames = [{"bbox": _box(40, 60, 720, 190)}, {"bbox": _box(40, 250, 720, 200)}]
    tokens = [{"text": "一、单选题", **_box(50, 75, 80, 15)},
              {"text": "2", **_box(76, 160, 8, 10)}, {"text": "10", **_box(76, 360, 12, 10)}]
    payload = {"objective_question_labels": row["observed_question_labels"]}
    return image, row, payload, tokens, frames


def _align(fixture):
    image, row, payload, tokens, frames = fixture
    return align_objective_options(row, image, payload, tokens=tokens,
                                   measurement_bbox=_box(40, 60, 720, 390), frames=frames)


def test_actual_partial_bracket_pixels_and_unique_topology_fix_all_eleven_items_atomically():
    fixture = _fixture()
    image, row, payload, _, _ = fixture
    before = copy.deepcopy(row)
    assert review_objective_items(row, payload) == ["MODEL_OBS_OBJECTIVE_ITEMS_GEOMETRY_CONFLICT"]
    result = _align(fixture)
    assert result == {"aligned": True, "reason_code": "LOCAL_OBJECTIVE_OPTION_PIXELS_ALIGNED",
                      "matched_items": 11, "verified_anchors": 2}
    assert review_objective_items(row, payload) == []
    assert row["observed_question_labels"] == before["observed_question_labels"]
    assert row["proposed_bbox"] == _box(40, 60, 720, 390)
    assert row["objective_items"][8]["options"][3]["bbox"] == _box(220, 320, 27, 11)
    assert np.any(image[320:331, 220:247] < 255)


def test_legacy_alignment_uses_the_same_printed_number_bounds_without_moving_measured_options():
    original_fixture = _fixture()
    assert _align(original_fixture)["aligned"] is True
    baseline = copy.deepcopy(original_fixture[1]["objective_items"])
    fixture = _fixture()
    image, row, _payload, _tokens, _frames = fixture
    points = [(100, 120), (100, 160), (100, 200), (330, 120), (330, 160), (330, 200),
              (560, 120), (560, 160), (100, 320), (100, 360), (330, 320)]
    for number, (x, y) in enumerate(points, 1):
        cv2.putText(image, str(number), (x - 26, y + 10), cv2.FONT_HERSHEY_SIMPLEX, .4, (0, 0, 0), 1)
    assert _align(fixture)["aligned"] is True
    assert [item["options"] for item in row["objective_items"]] == [item["options"] for item in baseline]
    assert all(item["bbox"]["x"] < item["options"][0]["bbox"]["x"] for item in row["objective_items"])


@pytest.mark.parametrize("fault", ["missing", "blank", "no_anchor", "wrong_anchor", "duplicate_anchor",
                                     "unofficial", "duplicate_label", "missing_model_item", "wrong_topology",
                                     "no_choice_content", "negative_heading", "no_frames", "gapped_frames"])
def test_unresolved_or_contradictory_pixels_never_mutate_model_result(fault):
    fixture = _fixture(missing=fault == "missing")
    image, row, payload, tokens, frames = fixture
    if fault == "blank":
        image[:] = 255
    elif fault == "no_anchor":
        tokens[:] = tokens[:1]
    elif fault == "wrong_anchor":
        tokens[1]["text"] = "3"
    elif fault == "duplicate_anchor":
        tokens[2]["text"] = "2"
    elif fault == "unofficial":
        payload["objective_question_labels"] = payload["objective_question_labels"][:-1]
    elif fault == "duplicate_label":
        row["objective_items"][1]["printed_question_label"] = "1"
    elif fault == "missing_model_item":
        row["objective_items"].pop()
    elif fault == "wrong_topology":
        row["objective_items"][0]["options"] = copy.deepcopy(row["objective_items"][1]["options"])
    elif fault == "no_choice_content":
        tokens.pop(0)
    elif fault == "negative_heading":
        tokens[0]["text"] = "非选择题"
    elif fault == "no_frames":
        frames.clear()
    elif fault == "gapped_frames":
        frames[1]["bbox"]["y"] += .01
    original = copy.deepcopy(row)
    assert _align(fixture)["aligned"] is False
    assert row == original


def test_digit_grid_does_not_become_choices_from_model_labels_and_number_tokens():
    fixture = _fixture(obscured=False)
    fixture[3][0]["text"] = "准考证号"
    original = copy.deepcopy(fixture[1])
    assert _align(fixture)["aligned"] is False
    assert fixture[1] == original


def test_open_shape_without_color_occlusion_does_not_recover_missing_endpoint():
    image, *_ = _fixture()
    # 同样缺口若是白色丢失，没有彩色遮挡证据就不能接收残缺的 D。
    cv2.rectangle(image, (244, 329), (250, 332), (255, 255, 255), -1)
    groups, _ = detect_option_groups(image[60:450, 40:760], 4, "horizontal")
    assert len(groups) == 10


def test_color_only_option_example_cannot_create_cells():
    image = np.full((300, 500, 3), 255, np.uint8)
    cv2.putText(image, "[A] [B] [C] [D]", (30, 100), cv2.FONT_HERSHEY_SIMPLEX, 1, (255, 0, 0), 2)
    assert detect_option_groups(image, 4, "horizontal")[0] == []


def test_ambiguous_duplicate_measured_topology_is_not_arbitrarily_chosen(monkeypatch):
    fixture = _fixture()
    monkeypatch.setattr("grading_agent.template_analysis_option_alignment._signature", lambda *_: (1, 2, 3))
    original = copy.deepcopy(fixture[1])
    assert _align(fixture)["aligned"] is False
    assert fixture[1] == original


@pytest.mark.parametrize("edges, expected", [((113, 47, 713, 547), (113, 47, 713, 547)),
                                             ((10, 10, 109, 116), (10, 10, 109, 116)),
                                             ((113.25, 47.25, 713.25, 547.25), (113, 47, 714, 548))])
def test_measurement_uses_exact_crop_despite_normalized_float_roundoff(monkeypatch, edges, expected):
    _, row, payload, _, _ = _fixture()
    image = np.zeros((1132, 1600, 3), np.uint8)
    image[:, :, 0] = np.arange(1600, dtype=np.uint16) % 251
    image[:, :, 1] = (np.arange(1132, dtype=np.uint16) % 251)[:, None]
    actual = []

    def detect(crop, option_count, orientation):
        actual.append(crop.copy())
        assert option_count == 4 and orientation == "horizontal"
        return [], []

    monkeypatch.setattr("grading_agent.template_analysis_option_alignment.detect_option_groups", detect)
    original = copy.deepcopy(row)
    # 整数边乘回尺寸可能略小或略大，不能因此多读任务外像素；真实小数边仍须向外取整。
    left, top, right, bottom = edges
    region = {"x": left / 1600, "y": top / 1132,
              "width": (right - left) / 1600, "height": (bottom - top) / 1132}
    assert align_objective_options(row, image, payload, measurement_bbox=region)["aligned"] is False
    left, top, right, bottom = expected
    assert len(actual) == 1 and np.array_equal(actual[0], image[top:bottom, left:right])
    assert row == original


@pytest.mark.parametrize("outside", ["question_anchors", "printed_frame", "option_pixels"])
def test_evidence_outside_actual_crop_cannot_complete_alignment(outside):
    image, row, payload, tokens, frames = _fixture()
    region = _box(40, 60, 720, 390)
    if outside == "question_anchors":
        for token in tokens[1:]:
            token["x"] = 20 / 800
    elif outside == "printed_frame":
        # 全部选项仍在裁图中，但第二段外框底边超出裁图，不能借整页 frames 扩框。
        region = _box(40, 60, 720, 330)
    else:
        region = _box(40, 60, 600, 390)
    original = copy.deepcopy(row)
    assert align_objective_options(row, image, payload, tokens=tokens,
                                   measurement_bbox=region, frames=frames)["aligned"] is False
    assert row == original


def _merge_fixture(*, missing=False):
    image, combined, payload, tokens, frames = _fixture(missing=missing)
    rows = []
    for index, (start, stop) in enumerate(((0, 8), (8, 11))):
        row = copy.deepcopy(combined)
        row.update(candidate_id=None, printed_question_label=None, identity_label=None, association_status="unassociated",
                   objective_items=row["objective_items"][start:stop], proposed_bbox=copy.deepcopy(frames[index]["bbox"]),
                   reason_codes=[], evidence_refs=["source-page:" + "a" * 64 + ":1"])
        row["observed_question_labels"] = [item["printed_question_label"] for item in row["objective_items"]]
        row["visual_evidence"] = {"static_label": "单选题" if index == 0 else "多选题", "option_pattern": "letter_options",
                                  "identity_pattern": "none", "barcode_pattern": "none",
                                  "boundary_basis": "printed_frame", "anchor_bbox": None}
        rows.append(row)
        frames[index]["evidence_refs"] = [f"cv-frame:fixture:1:{index}"]
    payload["question_labels"] = [*payload["objective_question_labels"], "12"]
    return image, rows, payload, tokens, frames


def _merge(fixture, *, protected_regions=()):
    image, rows, payload, tokens, frames = fixture
    return merge_objective_groups(rows, image, payload, tokens=tokens, frames=frames,
                                  measurement_bbox=_box(40, 60, 720, 390), protected_regions=protected_regions)


def test_two_model_choice_sections_merge_only_after_all_real_cells_and_topology_match():
    fixture = _merge_fixture()
    _, rows, payload, _, _ = fixture
    rows[1]["_requires_option_alignment"] = True
    original = copy.deepcopy(rows)

    merged, metadata = _merge(fixture)

    assert metadata == {"merged": True, "input_groups": 2, "matched_items": 11, "verified_anchors": 2,
                        "reason_code": "LOCAL_OBJECTIVE_GROUPS_PIXELS_MERGED"}
    assert rows == original
    assert len(merged) == 1 and len(merged[0]["objective_items"]) == 11
    assert sum(len(item["options"]) for item in merged[0]["objective_items"]) == 44
    assert merged[0]["proposed_bbox"] == _box(40, 60, 720, 390)
    assert review_objective_items(merged[0], payload) == []
    assert merged[0]["observed_question_labels"] == [str(index) for index in range(1, 12)]
    assert merged[0]["_requires_option_alignment"] is True


@pytest.mark.parametrize("conflict", ["identity_label", "identity_pattern", "barcode", "warning", "fill_type",
                                        "explicit_review", "unknown_static"])
def test_second_group_counterevidence_cannot_disappear_when_first_group_is_valid(conflict):
    fixture = _merge_fixture()
    _, rows, _, _, _ = fixture
    second = rows[1]
    if conflict == "identity_label":
        second["visual_evidence"]["static_label"] = "姓名"
    elif conflict == "identity_pattern":
        second["visual_evidence"]["identity_pattern"] = "name_line"
    elif conflict == "barcode":
        second["visual_evidence"]["barcode_pattern"] = "linear_bars"
    elif conflict == "warning":
        second["visual_evidence"]["static_label"] = "禁止作答"
    elif conflict == "fill_type":
        second["objective_items"][0]["printed_question_label"] = second["observed_question_labels"][0] = "12"
    elif conflict == "explicit_review":
        second.update(semantic_check="needs_review", reason_codes=["MODEL_OBS_QUESTION_TYPE_CONFLICT"])
    else:
        second["visual_evidence"]["static_label"] = "private student text"
    original = copy.deepcopy(rows)
    output, metadata = _merge(fixture)
    assert metadata["merged"] is False
    assert output == rows == original


@pytest.mark.parametrize("failure", ["duplicate_labels", "observed_item_disagreement", "missing_model_item", "missing_pixel",
                                       "wrong_anchor", "no_anchor", "no_frames", "no_frame_provenance", "gapped_frames",
                                       "gapped_columns", "ambiguous_frame"])
def test_unproven_choice_group_union_keeps_all_original_groups(failure):
    fixture = _merge_fixture(missing=failure == "missing_pixel")
    _, rows, _, tokens, frames = fixture
    if failure == "duplicate_labels":
        rows[1]["objective_items"][0]["printed_question_label"] = rows[1]["observed_question_labels"][0] = "1"
    elif failure == "observed_item_disagreement":
        rows[1]["observed_question_labels"][0] = "1"
    elif failure == "missing_model_item":
        rows[1]["objective_items"].pop()
        rows[1]["observed_question_labels"].pop()
    elif failure == "wrong_anchor":
        tokens[1]["text"] = "3"
    elif failure == "no_anchor":
        tokens[:] = tokens[:1]
    elif failure == "no_frames":
        frames.clear()
    elif failure == "no_frame_provenance":
        frames[1]["evidence_refs"] = []
    elif failure == "gapped_frames":
        frames[1]["bbox"]["y"] += .01
    elif failure == "gapped_columns":
        frames[1]["bbox"]["x"] += .01
    elif failure == "ambiguous_frame":
        frames.append(copy.deepcopy(frames[0]))
    original = copy.deepcopy(rows)
    output, metadata = _merge(fixture)
    assert metadata["merged"] is False
    assert output == rows == original


@pytest.mark.parametrize("role", ["identity", "answer", "instruction", "uncertain", "other"])
@pytest.mark.parametrize("source", ["batch", "kept"])
def test_merging_printed_choice_frames_cannot_cross_any_other_visible_purpose(role, source):
    fixture = _merge_fixture()
    rows = fixture[1]
    barrier = {"role": role, "proposed_bbox": _box(50, 240, 700, 20)}
    if source == "batch":
        rows.append(barrier)
    original = copy.deepcopy(rows)
    output, metadata = _merge(fixture, protected_regions=[barrier] if source == "kept" else [])
    assert metadata["merged"] is False
    assert output == rows == original


def test_nonintersecting_instruction_is_preserved_when_two_choice_sections_merge():
    fixture = _merge_fixture()
    note = {"role": "instruction", "proposed_bbox": _box(40, 470, 720, 40)}
    fixture[1].insert(1, note)
    original = copy.deepcopy(fixture[1])
    output, metadata = _merge(fixture)
    assert metadata["merged"] is True
    assert len(output) == 2 and output[1] == note
    assert fixture[1] == original
