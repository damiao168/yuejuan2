"""题号身份可由部分锚点消歧，单题外框仍应包含各自实测数字，选项原像素不变。"""

import copy

import cv2
import numpy as np
import pytest
from grading_agent.template_analysis_option_alignment import (
    _bounds,
    _measured_item_bounds,
)


def _box(x, y, width, height):
    return x / 800, y / 600, width / 800, height / 600


def _fixture():
    image = np.full((600, 800, 3), 255, np.uint8)
    groups, markers = [], []
    for label, x, y in (("1", 100, 120), ("2", 100, 160), ("11", 330, 120), ("12", 330, 160)):
        groups.append([_box(x + index * 30, y, 20, 12) for index in range(4)])
        cv2.putText(image, label, (x - 28, y + 10), cv2.FONT_HERSHEY_SIMPLEX, .4, (0, 0, 0), 1)
        markers.append(_box(x - 28, y, 20, 12))
    return image, groups, markers, _box(40, 60, 500, 180)


def test_partial_identity_anchors_still_give_every_horizontal_multicolumn_item_its_real_number_ink():
    image, groups, markers, region = _fixture()
    original = copy.deepcopy(groups)
    result = _measured_item_bounds(image, groups, [("1", 0, markers[0])], region, "horizontal")
    assert groups == original
    assert all(bounds[0] < group[0][0] for bounds, group in zip(result, groups))
    # 双位题号的两个字形都属于该题；不能只框住离 A 格最近的一位。
    assert result[2][0] < (330 - 17) / 800
    assert all(bounds[0] + bounds[2] == pytest.approx(_bounds(group)[0] + _bounds(group)[2])
               for bounds, group in zip(result, groups))


def test_color_checkmark_and_tiny_black_residue_do_not_expand_number_ink_bounds():
    image, groups, _markers, region = _fixture()
    before = _measured_item_bounds(image, groups, [], region, "horizontal")
    cv2.line(image, (82, 155), (88, 171), (255, 0, 0), 2)
    cv2.line(image, (88, 171), (95, 152), (255, 0, 0), 2)
    image[156, 96] = 0
    assert _measured_item_bounds(image, groups, [], region, "horizontal") == before


@pytest.mark.parametrize("fault", ["missing", "multiple_clusters", "oversized", "clipped"])
def test_unreadable_number_geometry_keeps_existing_measured_options_without_inventing_a_marker(fault):
    image, groups, markers, region = _fixture()
    image[110:145, 40:99] = 255
    if fault == "multiple_clusters":
        cv2.putText(image, "1", (45, 130), cv2.FONT_HERSHEY_SIMPLEX, .4, (0, 0, 0), 1)
        cv2.putText(image, "2", (78, 130), cv2.FONT_HERSHEY_SIMPLEX, .4, (0, 0, 0), 1)
    elif fault == "oversized":
        cv2.line(image, (75, 110), (75, 143), (0, 0, 0), 2)
    elif fault == "clipped":
        cv2.putText(image, "2", (94, 130), cv2.FONT_HERSHEY_SIMPLEX, .4, (0, 0, 0), 1)
    result = _measured_item_bounds(image, groups, [], region, "horizontal")
    assert result[0] == _bounds(groups[0])
    assert all(bounds[0] < group[0][0] for bounds, group in zip(result[1:], groups[1:]))
    # 独立 OCR 已提供的题号仍保留；墨迹补测失败不能撤销有效的题号或整个选择区。
    result = _measured_item_bounds(image, groups, [("1", 0, markers[0])], region, "horizontal")
    assert result[0] == _bounds([*groups[0], markers[0]])


def test_neighboring_option_pixels_are_a_hard_boundary_for_the_next_column_number_strip():
    image = np.full((600, 800, 3), 255, np.uint8)
    first = [_box(x, 120, 12, 12) for x in (70, 95, 120, 145)]
    second = [_box(x, 120, 12, 12) for x in (175, 200, 225, 250)]
    for x in (70, 95, 120, 145, 175, 200, 225, 250):
        cv2.rectangle(image, (x, 120), (x + 11, 131), (0, 0, 0), -1)
    result = _measured_item_bounds(image, [first, second], [], _box(30, 80, 300, 100), "horizontal")
    assert result[1] == _bounds(second)


def test_vertical_option_layout_does_not_search_the_horizontal_left_number_strip():
    image, _groups, markers, region = _fixture()
    vertical = [[_box(100, y, 20, 12) for y in (120, 150, 180, 210)]]
    assert _measured_item_bounds(image, vertical, [], region, "vertical") == [_bounds(vertical[0])]
    assert _measured_item_bounds(image, vertical, [("1", 0, markers[0])], region, "vertical") == [
        _bounds([*vertical[0], markers[0]])]


@pytest.mark.parametrize("obstacle", ["neighbor_item", "other_purpose"])
def test_number_ink_expansion_cannot_invalidate_existing_omr_by_crossing_another_item_or_purpose(obstacle):
    image = np.full((600, 800, 3), 255, np.uint8)
    groups = [[_box(x, 120, 20, 12) for x in (100, 130, 160, 190)]]
    cv2.rectangle(image, (75, 125), (78, 138), (0, 0, 0), -1)
    protected = []
    if obstacle == "neighbor_item":
        groups.append([_box(x, 136, 20, 12) for x in (100, 130, 160, 190)])
    else:
        protected = [_box(70, 136, 20, 12)]
    assert _measured_item_bounds(image, groups, [], _box(40, 60, 400, 240), "horizontal", protected=protected) == [
        _bounds(group) for group in groups]
