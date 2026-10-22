"""只验证实测框对粗区单边遗漏的补齐，不以几何规则代替区域分类。"""

import copy

import pytest

from grading_agent.template_analysis_frame_alignment import (
    complete_coarse_objective_frame,
)

PAGE = {"page_no": 1, "width": 1600, "height": 1132}


def region(box=None, **changes):
    row = {"candidate_id": "coarse-choice", "role": "objective_group", "_coarse_layout": True,
           "objective_items": [], "proposed_bbox": box or {"x": .2, "y": .3, "width": .2, "height": .2},
           "source_bbox": {"x": .2, "y": .3, "width": .2, "height": .2},
           "evidence_refs": ["source-page:fixture:1"], "reason_codes": []}
    row.update(changes)
    return row


def frame(box=None, candidate_id="frame-1"):
    return {"candidate_id": candidate_id,
            "bbox": box or {"x": .2, "y": .3, "width": .3, "height": .2},
            "evidence_refs": [f"cv-frame:{candidate_id}"]}


def test_trial13_right_edge_omitted_questions_7_and_8_uses_measured_complete_frame():
    # Trial13 把 1–8 题粗区写成宽 .19，右侧 7/8 题落在框外；三条邻近边仍对应同一印刷框。
    row = region({"x": .0625, "y": .33, "width": .19, "height": .09},
                 observed_question_labels=[str(number) for number in range(1, 9)])
    measured = frame({"x": .066875, "y": .33127208480565373,
                      "width": .281875, "height": .08745583038869258})
    before = copy.deepcopy(row)
    frames_before = copy.deepcopy(measured)
    assert complete_coarse_objective_frame(row, [measured], PAGE, [row]) is True
    assert row["proposed_bbox"] == measured["bbox"]
    assert row["source_bbox"] == before["source_bbox"]
    assert row["objective_items"] == before["objective_items"] == []
    assert row["observed_question_labels"] == before["observed_question_labels"]
    assert row["evidence_refs"] == ["source-page:fixture:1", "cv-frame:frame-1"]
    assert row["reason_codes"] == ["COARSE_OBJECTIVE_FRAME_EDGE_COMPLETED"]
    assert measured == frames_before
    assert complete_coarse_objective_frame(row, [measured], PAGE, [row]) is False


@pytest.mark.parametrize("box", [
    {"x": .1, "y": .3, "width": .3, "height": .2},
    {"x": .2, "y": .2, "width": .2, "height": .3},
    {"x": .2, "y": .3, "width": .3, "height": .2},
    {"x": .2, "y": .3, "width": .2, "height": .3},
])
def test_exactly_one_missing_outer_edge_can_be_completed_in_each_direction(box):
    row = region()
    assert complete_coarse_objective_frame(row, [frame(box)], PAGE, [row]) is True
    assert row["proposed_bbox"] == box


@pytest.mark.parametrize("changes", [
    {"_coarse_layout": False},
    {"_coarse_layout": None},
    {"role": "answer"},
    {"role": "uncertain"},
    {"objective_items": [{"printed_question_label": "1", "options": [{"label": "A"}]}]},
])
def test_fine_or_nonchoice_regions_are_not_changed(changes):
    row = region(**changes)
    before = copy.deepcopy(row)
    assert complete_coarse_objective_frame(row, [frame()], PAGE, [row]) is False
    assert row == before


@pytest.mark.parametrize("boxes", [
    [{"x": .2, "y": .3, "width": .2, "height": .2}],
    [{"x": .2, "y": .3, "width": .1, "height": .2}],
    [{"x": .2, "y": .3, "width": .3, "height": .3}],
    [{"x": .2, "y": .3, "width": .3, "height": .2},
     {"x": .201, "y": .3, "width": .32, "height": .2}],
    # 即使近框无需补齐，也不能跳过它去选择另一个更远的大框。
    [{"x": .2, "y": .3, "width": .2, "height": .2},
     {"x": .2, "y": .3, "width": .3, "height": .2}],
])
def test_complete_inward_two_missing_edges_and_ambiguous_frames_are_unchanged(boxes):
    row = region()
    before = copy.deepcopy(row)
    frames = [frame(box, f"frame-{index}") for index, box in enumerate(boxes)]
    assert complete_coarse_objective_frame(row, frames, PAGE, [row]) is False
    assert row == before


@pytest.mark.parametrize("role", ["identity", "answer", "uncertain", "instruction", "other"])
def test_extension_cannot_occupy_other_region_content(role):
    row = region()
    other = region({"x": .43, "y": .35, "width": .04, "height": .05},
                   candidate_id="other-content", role=role)
    before = copy.deepcopy(row)
    assert complete_coarse_objective_frame(row, [frame()], PAGE, [row, other]) is False
    assert row == before


def test_old_conflict_inside_original_region_does_not_block_missing_edge_completion():
    row = region()
    other = region({"x": .25, "y": .35, "width": .05, "height": .05},
                   candidate_id="old-conflict", role="identity")
    assert complete_coarse_objective_frame(row, [frame()], PAGE, [row, other]) is True


def test_slight_outward_shift_of_other_three_edges_also_checks_new_content():
    row = region()
    measured = frame({"x": .2, "y": .29, "width": .3, "height": .21})
    other = region({"x": .25, "y": .29, "width": .05, "height": .008},
                   candidate_id="answer-above", role="answer")
    before = copy.deepcopy(row)
    assert complete_coarse_objective_frame(row, [measured], PAGE, [row, other]) is False
    assert row == before


def test_neighbor_choice_region_does_not_invent_nonchoice_conflict():
    row = region()
    other = region({"x": .43, "y": .35, "width": .04, "height": .05}, candidate_id="other-choice")
    assert complete_coarse_objective_frame(row, [frame()], PAGE, [row, other]) is True


@pytest.mark.parametrize("overlap_pixels, expected", [(0, True), (.8, True), (2, False)])
def test_shared_printed_edge_allows_at_most_one_display_pixel_overlap(overlap_pixels, expected):
    row = region()
    other = region({"x": .5 - overlap_pixels / PAGE["width"], "y": .3, "width": .1, "height": .2},
                   candidate_id="adjacent-answer", role="answer")
    assert complete_coarse_objective_frame(row, [frame()], PAGE, [row, other]) is expected


def test_trial13_lower_group_with_both_right_and_bottom_edges_missing_stays_unchanged():
    row = region({"x": .0625, "y": .42, "width": .19, "height": .06})
    measured = frame({"x": .066875, "y": .41872791519434627,
                      "width": .281875, "height": .08303886925795052})
    before = copy.deepcopy(row)
    assert complete_coarse_objective_frame(row, [measured], PAGE, [row]) is False
    assert row == before


@pytest.mark.parametrize("box", [None, {}, {"x": .2, "y": .3, "width": float("nan"), "height": .2},
                                  {"x": .2, "y": .3, "width": .9, "height": .2}])
def test_invalid_measured_geometry_does_not_change_row(box):
    row = region()
    before = copy.deepcopy(row)
    measured = frame()
    measured["bbox"] = box
    assert complete_coarse_objective_frame(row, [measured], PAGE, [row]) is False
    assert row == before
