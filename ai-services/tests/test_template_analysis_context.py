"""裁图说明片段去重必须有已知说明与完整实测框共同佐证。"""

import copy

import numpy as np
import pytest
from grading_agent.template_analysis_context import filter_repair_context_instructions

PAGE = {"width": 1600, "height": 1132}
CROP = {"x": .048125, "y": .0980565371024735, "width": .308125, "height": .14752650176678445}
FRAME = {"x": .115, "y": .2252650176678445, "width": .235625, "height": .09982332155477032}
FRAGMENT = {"x": .122075, "y": .21902826855123675, "width": .160225, "height": .026554770318021198}
KNOWN = {"x": .0625, "y": .235, "width": .29, "height": .09}


def instruction(box, **changes):
    row = {"role": "instruction", "proposed_bbox": box, "semantic_check": "not_evaluated",
           "reason_codes": ["MULTIMODAL_FORBIDDEN_REGION_EXCLUDED", "MULTIMODAL_OBSERVATIONS_CONSISTENT"]}
    row.update(changes)
    return row


def frame(box=None):
    return {"bbox": box or FRAME, "evidence_refs": ["cv-frame:measured"]}


def test_trial16_cropped_instruction_is_removed_before_it_can_conflict_with_barcode():
    # 身份裁图只带入已存在说明区的首行，粗框多带入约七像素上缘；完整印刷框独立延伸到裁图外。
    barcode = {"role": "identity", "proposed_bbox": {
        "x": .219375, "y": .11307420494699646, "width": .1225, "height": .10865724381625441}}
    fresh, kept, frames = [barcode, instruction(FRAGMENT)], [instruction(KNOWN)], [frame()]
    before = copy.deepcopy((fresh, kept, frames, PAGE, CROP))
    filtered, metadata = filter_repair_context_instructions(fresh, kept, frames, PAGE, CROP)
    assert filtered == [barcode]
    assert filtered[0] is barcode
    assert metadata["removed_count"] == 1
    assert metadata["reason_codes"] == ["LOCAL_REPAIR_INSTRUCTION_CONTEXT_DEDUPLICATED"]
    assert (fresh, kept, frames, PAGE, CROP) == before


@pytest.mark.parametrize("turns", [0, 1, 2, 3])
def test_measured_instruction_continuation_can_cross_each_crop_edge(turns):
    def rotate(box):
        for _ in range(turns):
            box = {"x": 1 - box["y"] - box["height"], "y": box["x"],
                   "width": box["height"], "height": box["width"]}
        return box

    page = PAGE if turns % 2 == 0 else {"width": PAGE["height"], "height": PAGE["width"]}
    filtered, metadata = filter_repair_context_instructions(
        [instruction(rotate(FRAGMENT))], [instruction(rotate(KNOWN))], [frame(rotate(FRAME))], page, rotate(CROP))
    assert filtered == []
    assert metadata["removed_count"] == 1


@pytest.mark.parametrize("changes", [
    {"role": "uncertain"}, {"role": "answer"}, {"role": "identity"}, {"role": "other"},
    {"semantic_check": "needs_review"}, {"reason_codes": []},
])
def test_unknown_real_uses_and_unverified_fragments_are_retained(changes):
    fresh = [instruction(FRAGMENT, **changes)]
    assert filter_repair_context_instructions(fresh, [instruction(KNOWN)], [frame()], PAGE, CROP)[0] == fresh


@pytest.mark.parametrize("kept,frames", [
    ([], [frame()]), ([instruction(KNOWN)], []),
    ([instruction(KNOWN, semantic_check="needs_review")], [frame()]),
    ([instruction(KNOWN, reason_codes=[])], [frame()]),
    ([instruction(KNOWN)], [{"bbox": FRAME, "evidence_refs": ["source-page:model"]}]),
    ([instruction(KNOWN), instruction(KNOWN)], [frame()]),
    ([instruction(KNOWN)], [frame(), frame({**FRAME, "x": .116})]),
])
def test_missing_independent_frame_uncertain_owner_or_ambiguity_keeps_fragment(kept, frames):
    fresh = [instruction(FRAGMENT)]
    filtered, metadata = filter_repair_context_instructions(fresh, kept, frames, PAGE, CROP)
    assert filtered == fresh
    assert metadata["removed_count"] == 0
    assert metadata["reason_codes"] == []


@pytest.mark.parametrize("role", ["answer", "identity", "objective_group", "uncertain"])
def test_frame_containing_visible_uses_cannot_prove_instruction_context(role):
    kept = [instruction(KNOWN), {"role": role, "proposed_bbox": FRAME}]
    fresh = [instruction(FRAGMENT)]
    assert filter_repair_context_instructions(fresh, kept, [frame()], PAGE, CROP)[0] == fresh


def test_same_batch_answer_in_frame_is_not_hidden_by_existing_instruction():
    fresh = [instruction(FRAGMENT), {"role": "answer", "proposed_bbox": FRAME}]
    assert filter_repair_context_instructions(fresh, [instruction(KNOWN)], [frame()], PAGE, CROP)[0] == fresh


def test_independent_complete_warning_inside_crop_is_retained():
    fresh = [instruction({"x": .13, "y": .23, "width": .12, "height": .01})]
    assert filter_repair_context_instructions(fresh, [instruction(KNOWN)], [frame()], PAGE, CROP)[0] == fresh


def test_fragment_spanning_unknown_content_outside_frame_is_retained():
    fresh = [instruction({**FRAGMENT, "y": .18, "height": CROP["y"] + CROP["height"] - .18})]
    assert filter_repair_context_instructions(fresh, [instruction(KNOWN)], [frame()], PAGE, CROP)[0] == fresh


@pytest.mark.parametrize("turns", [0, 1, 2, 3])
def test_trial17_fragment_may_include_only_measured_blank_margin_beside_instruction(turns):
    def rotate(box):
        for _ in range(turns):
            box = {"x": 1 - box["y"] - box["height"], "y": box["x"],
                   "width": box["height"], "height": box["width"]}
        return box

    crop = {"x": .048125, "y": .0980565371024735, "width": .308125, "height": .142226148409894}
    fragment = {"x": .122075, "y": .22037102473498232, "width": .234175, "height": .01991166077738516}
    crop = rotate(crop)
    page = PAGE if turns % 2 == 0 else {"width": PAGE["height"], "height": PAGE["width"]}
    fresh, kept, frames = [instruction(rotate(fragment))], [instruction(rotate(KNOWN))], [frame(rotate(FRAME))]
    image = np.full((PAGE["height"], PAGE["width"], 3), 255, np.uint8)
    assert filter_repair_context_instructions(fresh, kept, frames, page, crop)[0] == fresh
    filtered, metadata = filter_repair_context_instructions(
        fresh, kept, frames, page, crop, image=np.rot90(image, -turns))
    assert filtered == []
    assert metadata["removed_count"] == 1
    # 相同几何下只要白边出现内容，就不能按说明片段隐藏它。
    image[260, 566] = 0
    assert filter_repair_context_instructions(
        fresh, kept, frames, page, crop, image=np.rot90(image, -turns))[0] == fresh


@pytest.mark.parametrize("shape", [(10, 10, 3), (1132, 1600), (1132, 1600, 4)])
def test_blank_context_must_use_the_original_page_dimensions(shape):
    crop = {"x": .048125, "y": .0980565371024735, "width": .308125, "height": .142226148409894}
    fragment = {"x": .122075, "y": .22037102473498232, "width": .234175, "height": .01991166077738516}
    fresh = [instruction(fragment)]
    assert filter_repair_context_instructions(
        fresh, [instruction(KNOWN)], [frame()], PAGE, crop, image=np.full(shape, 255, np.uint8))[0] == fresh


@pytest.mark.parametrize("crop,page", [(None, PAGE), (CROP, {"width": 0, "height": 1132}),
                                        (CROP, {"width": float("nan"), "height": 1132})])
def test_invalid_crop_or_dimensions_leaves_rows_untouched(crop, page):
    fresh = [instruction(FRAGMENT)]
    assert filter_repair_context_instructions(fresh, [instruction(KNOWN)], [frame()], page, crop)[0] == fresh
