"""验证粗语义区可以直接采用实测OMR；模板题目白名单不参与生成题号或缺格。"""

import copy
import subprocess
import time

import cv2
import numpy as np
import pytest

from grading_agent.template_analysis_omr import populate_objective_items
from grading_agent.template_analysis_registration import transform_registration_bbox


def box(x, y, w, h):
    return {"x": x / 800, "y": y / 600, "width": w / 800, "height": h / 600}


def fixture(*, marked=True, missing=False, full_anchors=False):
    image = np.full((600, 800, 3), 255, np.uint8)
    points = [(100, 120), (100, 160), (100, 200), (330, 120), (330, 160), (330, 200),
              (560, 120), (560, 160), (100, 320), (100, 360), (330, 320)]
    for number, (x, y) in enumerate(points):
        for index in range(4):
            if missing and number == 8 and index == 3:
                continue
            left = x + index * 40
            if marked and index == number % 3:
                cv2.rectangle(image, (left, y - 1), (left + 26, y + 11), (0, 0, 0), -1)
            else:
                for edge, direction in ((left, 1), (left + 26, -1)):
                    cv2.line(image, (edge, y), (edge, y + 10), (0, 0, 0), 1)
                    cv2.line(image, (edge, y), (edge + direction * 4, y), (0, 0, 0), 1)
                    cv2.line(image, (edge, y + 10), (edge + direction * 4, y + 10), (0, 0, 0), 1)
                cv2.putText(image, "ABCD"[index], (left + 10, y + 8), cv2.FONT_HERSHEY_SIMPLEX, .25, (0, 0, 0), 1)
    labels = list(map(str, range(1, 12)))
    row = {"role": "objective_group", "proposed_bbox": box(40, 60, 720, 390), "objective_items": [],
           "observed_question_labels": labels, "_coarse_layout": True, "printed_question_label": None,
           "identity_label": None, "association_status": "unassociated", "reason_codes": [], "evidence_refs": [],
           "visual_evidence": {"static_label": "单选题", "option_pattern": "letter_options", "identity_pattern": "none",
                               "barcode_pattern": "none", "boundary_basis": "printed_frame", "anchor_bbox": None}}
    payload = {"page": {"page_no": 1, "width": 800, "height": 600}, "question_labels": labels,
               "objective_question_labels": labels, "fill_blank_question_labels": []}
    chosen = range(11) if full_anchors else (0, 3, 9, 10)
    tokens = [{"text": str(index + 1), **box(points[index][0] - 24, points[index][1], 12, 10)} for index in chosen]
    return image, row, payload, tokens


@pytest.mark.parametrize("marked", [False, True])
def test_coarse_semantics_and_unique_measured_topology_yield_eleven_items_without_model_coordinates(marked):
    image, row, payload, tokens = fixture(marked=marked)
    result = populate_objective_items([row], image, payload, tokens=tokens,
                                      label_reader=lambda *args, **kwargs: pytest.fail("all required anchors already available"))
    assert result["completed_indices"] == [0]
    assert result["matched_items"] == 11
    assert result["verified_anchors"] == 4
    assert result["label_read_count"] == 0
    assert [item["printed_question_label"] for item in row["objective_items"]] == list(map(str, range(1, 12)))
    assert sum(len(item["options"]) for item in row["objective_items"]) == 44
    assert row["objective_items"][3]["options"][0]["bbox"]["x"] == pytest.approx(330 / 800)


def test_all_individual_number_anchors_work_without_using_a_reading_order():
    image, row, payload, tokens = fixture(full_anchors=True)
    # 打乱题号不能靠排序重排，全部静态锚点直接决定对应关系。
    for token in tokens:
        token["text"] = str(12 - int(token["text"]))
    result = populate_objective_items([row], image, payload, tokens=tokens)
    assert result["matched_items"] == 11
    assert result["verified_anchors"] == 11
    assert row["objective_items"][0]["options"][0]["bbox"]["x"] == pytest.approx(330 / 800)


def test_partial_ocr_identity_anchors_do_not_leave_other_items_without_their_real_printed_number():
    image, row, payload, tokens = fixture()
    baseline = copy.deepcopy(row)
    assert populate_objective_items([baseline], image, payload, tokens=tokens)["matched_items"] == 11
    points = [(100, 120), (100, 160), (100, 200), (330, 120), (330, 160), (330, 200),
              (560, 120), (560, 160), (100, 320), (100, 360), (330, 320)]
    for number, (x, y) in enumerate(points, 1):
        cv2.putText(image, str(number), (x - 26, y + 10), cv2.FONT_HERSHEY_SIMPLEX, .4, (0, 0, 0), 1)
    result = populate_objective_items([row], image, payload, tokens=tokens)
    assert result["matched_items"] == 11 and result["verified_anchors"] == 4
    assert [item["options"] for item in row["objective_items"]] == [item["options"] for item in baseline["objective_items"]]
    assert all(item["bbox"]["x"] < item["options"][0]["bbox"]["x"] for item in row["objective_items"])


@pytest.mark.parametrize("fault", ["missing_cell", "blank", "no_anchor", "wrong_anchor", "duplicate_anchor",
                                   "one_section_unanchored", "insufficient_anchors", "unofficial_label",
                                   "duplicate_label", "identity_evidence", "warning_evidence",
                                   "nonobjective_role"])
def test_incomplete_or_conflicting_observations_leave_original_row_for_existing_repair(fault):
    image, row, payload, tokens = fixture(missing=fault == "missing_cell")
    if fault == "blank":
        image[:] = 255
    elif fault == "no_anchor":
        tokens.clear()
    elif fault == "wrong_anchor":
        tokens[1]["text"] = "8"
    elif fault == "duplicate_anchor":
        tokens[1]["text"] = "1"
    elif fault == "one_section_unanchored":
        tokens[:] = tokens[:2]
    elif fault == "insufficient_anchors":
        tokens[:] = [tokens[0], tokens[-1]]
    elif fault == "unofficial_label":
        payload["objective_question_labels"] = payload["objective_question_labels"][:-1]
    elif fault == "duplicate_label":
        row["observed_question_labels"] = [*row["observed_question_labels"][:-1], "1"]
    elif fault == "identity_evidence":
        row["visual_evidence"]["identity_pattern"] = "number_grid"
    elif fault == "warning_evidence":
        row["visual_evidence"]["static_label"] = "禁止作答"
    elif fault == "nonobjective_role":
        row["role"] = "identity"
    original = copy.deepcopy(row)
    result = populate_objective_items([row], image, payload, tokens=tokens, label_reader=lambda *args, **kwargs: ([], None))
    assert result["matched_items"] == 0
    assert row == original


def test_deadline_expiry_preserves_model_repair_path_without_launching_number_reader():
    image, row, payload, _tokens = fixture()
    original = copy.deepcopy(row)
    result = populate_objective_items([row], image, payload, deadline=time.monotonic() - 1,
                                      label_reader=lambda *args, **kwargs: pytest.fail("deadline expired"))
    assert result["label_read_count"] == 0
    assert row == original


def test_number_reader_timeout_is_local_failure_and_does_not_erase_coarse_observation():
    image, row, payload, _tokens = fixture()
    original = copy.deepcopy(row)

    def timeout(*args, **kwargs):
        raise subprocess.TimeoutExpired("tesseract", .1)

    result = populate_objective_items([row], image, payload, label_reader=timeout)
    assert result["label_read_count"] == 1
    assert result["matched_items"] == 0
    assert row == original


def test_original_pixels_are_measured_and_option_positions_projected_to_current_analysis_image():
    source, row, payload, tokens = fixture()
    matrix = [[1, 0, 8], [0, 1, 6], [0, 0, 1]]
    row["proposed_bbox"] = transform_registration_bbox(row["proposed_bbox"], matrix, 800, 600)
    mapped_tokens = [{**token, **transform_registration_bbox(token, matrix, 800, 600)} for token in tokens]
    # 校正图可以丢掉细格，模型仍在该图理解语义；OMR必须只测来源原像素。
    analysis = np.full_like(source, 255)
    result = populate_objective_items([row], analysis, payload, tokens=mapped_tokens,
                                      source_image=source, source_to_analysis=matrix)
    assert result["matched_items"] == 11
    assert row["objective_items"][3]["options"][0]["bbox"]["x"] == pytest.approx(338 / 800)
    assert "LOCAL_OBJECTIVE_ORIGINAL_PIXELS_MAPPED" in row["reason_codes"]


def test_new_number_ink_cannot_cross_another_purpose_after_source_coordinate_mapping():
    source, row, payload, tokens = fixture()
    cv2.putText(source, "5", (304, 170), cv2.FONT_HERSHEY_SIMPLEX, .4, (0, 0, 0), 1)
    matrix = [[1, 0, 40], [0, 1, 30], [0, 0, 1]]
    row["proposed_bbox"] = transform_registration_bbox(row["proposed_bbox"], matrix, 800, 600)
    mapped_tokens = [{**token, **transform_registration_bbox(token, matrix, 800, 600)} for token in tokens]
    obstacle = {"role": "instruction", "proposed_bbox": transform_registration_bbox(box(306, 160, 2, 12), matrix, 800, 600)}
    # 屏障来自校正图；先映回测量图才能放弃与其冲突的新增数字外框，同时保留原有 44 格。
    result = populate_objective_items([row, obstacle], np.full_like(source, 255), payload, tokens=mapped_tokens,
                                      source_image=source, source_to_analysis=matrix)
    assert result["matched_items"] == 11
    fifth = row["objective_items"][4]
    assert fifth["bbox"]["x"] == fifth["options"][0]["bbox"]["x"]


@pytest.mark.parametrize("matrix", [[[0, 0, 0]] * 3, [[float("nan"), 0, 0], [0, 1, 0], [0, 0, 1]], [[1, 0], [0, 1]]])
def test_invalid_source_mapping_never_mutates_observations(matrix):
    image, row, payload, tokens = fixture()
    original = copy.deepcopy(row)
    assert populate_objective_items([row], image, payload, tokens=tokens, source_image=image,
                                    source_to_analysis=matrix)["matched_items"] == 0
    assert row == original


def split_fixture():
    image, row, payload, tokens = fixture()
    rows = [copy.deepcopy(row), copy.deepcopy(row)]
    frames = [{"bbox": box(40, 60, 720, 190), "evidence_refs": ["cv-frame:first"]},
              {"bbox": box(40, 250, 720, 200), "evidence_refs": ["cv-frame:second"]}]
    for index, (start, stop) in enumerate(((0, 8), (8, 11))):
        rows[index]["observed_question_labels"] = row["observed_question_labels"][start:stop]
        rows[index]["proposed_bbox"] = copy.deepcopy(frames[index]["bbox"])
    rows[1]["visual_evidence"]["static_label"] = "多选题"
    return image, rows, payload, tokens, frames


def test_two_successful_omr_sections_with_continuous_printed_frames_merge_without_remeasuring_options():
    image, rows, payload, tokens, frames = split_fixture()
    result = populate_objective_items(rows, image, payload, tokens=tokens, frames=frames)
    assert result["matched_items"] == 11
    assert result["merged_groups"] == 2
    assert result["completed_indices"] == [0]
    assert len(rows) == 1
    assert len(rows[0]["objective_items"]) == 11
    assert rows[0]["proposed_bbox"] == box(40, 60, 720, 390)


def test_model_missing_one_label_is_completed_only_by_actual_number_anchor():
    image, row, payload, tokens = fixture()
    row["observed_question_labels"] = row["observed_question_labels"][:-1]
    result = populate_objective_items([row], image, payload, tokens=tokens)
    assert result["matched_items"] == 11
    assert row["observed_question_labels"][-1] == "11"


def test_official_allowlist_does_not_complete_unobserved_label_without_its_actual_anchor():
    image, row, payload, tokens = fixture()
    row["observed_question_labels"] = row["observed_question_labels"][:-1]
    tokens = [token for token in tokens if token["text"] != "11"]
    original = copy.deepcopy(row)
    result = populate_objective_items([row], image, payload, tokens=tokens, label_reader=lambda *args, **kwargs: ([], None))
    assert result["matched_items"] == 0
    assert result["label_read_count"] == 1
    assert row == original


def test_segmented_measurement_uses_real_printed_frames_and_does_not_require_model_box_perfectly_aligned(monkeypatch):
    import grading_agent.template_analysis_omr as omr

    image, row, payload, tokens = fixture()
    frames = [{"bbox": box(40, 60, 720, 190)}, {"bbox": box(40, 250, 720, 200)}]
    row["proposed_bbox"] = box(39, 58, 718, 390)
    original_detector = omr.detect_option_groups
    heights = []

    def detector(crop, count, orientation):
        heights.append(crop.shape[0])
        # 浏览器渲染实际故障是整区尺度丢一行，真实分隔框中各自测量则完整；这里模拟同一边界。
        if crop.shape[0] > 250:
            return [], []
        return original_detector(crop, count, orientation)

    monkeypatch.setattr(omr, "detect_option_groups", detector)
    result = populate_objective_items([row], image, payload, tokens=tokens, frames=frames)
    assert result["matched_items"] == 11
    assert set(heights) == {190, 200}
    assert row["proposed_bbox"] == box(40, 60, 720, 390)


@pytest.mark.parametrize("barrier_role", ["identity", "answer", "instruction", "uncertain"])
def test_other_purposes_block_merging_but_do_not_erase_already_verified_omr_items(barrier_role):
    image, rows, payload, tokens, frames = split_fixture()
    rows.append({"role": barrier_role, "proposed_bbox": box(45, 255, 10, 10)})
    result = populate_objective_items(rows, image, payload, tokens=tokens, frames=frames)
    assert result["matched_items"] == 11
    assert result["merged_groups"] == 0
    assert [len(row.get("objective_items", [])) for row in rows] == [8, 3, 0]
    assert sum(len(item["options"]) for row in rows for item in row.get("objective_items", [])) == 44
    assert result["completed_indices"] == [0, 1]


@pytest.mark.parametrize("barrier_role", ["identity", "answer", "instruction", "uncertain"])
@pytest.mark.parametrize("barrier_y,barrier_height", [(252, 5), (255, 10)])
def test_printed_frame_expansion_cannot_add_other_purpose_even_when_already_partly_overlapping(
    barrier_role, barrier_y, barrier_height,
):
    image, rows, payload, tokens, frames = split_fixture()
    rows[1]["proposed_bbox"] = box(40, 260, 720, 185)
    before = copy.deepcopy(rows[1])
    rows.append({"role": barrier_role, "proposed_bbox": box(45, barrier_y, 10, barrier_height)})
    result = populate_objective_items(rows, image, payload, tokens=tokens, frames=frames)
    assert result["matched_items"] == 8
    assert result["merged_groups"] == 0
    assert rows[1] == before


@pytest.mark.parametrize("barrier_role", ["identity", "answer", "instruction", "uncertain"])
def test_real_item_overlap_still_blocks_omr_even_inside_existing_coarse_region(barrier_role):
    image, rows, payload, tokens, frames = split_fixture()
    before = copy.deepcopy(rows[1])
    rows.append({"role": barrier_role, "proposed_bbox": box(100, 320, 26, 12)})
    result = populate_objective_items(rows, image, payload, tokens=tokens, frames=frames)
    assert result["matched_items"] == 8
    assert result["merged_groups"] == 0
    assert rows[1] == before


def test_separate_printed_frames_cannot_be_merged_across_empty_gap():
    image, rows, payload, tokens, frames = split_fixture()
    frames[1]["bbox"]["y"] += .01
    result = populate_objective_items(rows, image, payload, tokens=tokens, frames=frames)
    assert result["matched_items"] == 11
    assert result["merged_groups"] == 0
    assert len(rows) == 2


def test_measured_items_survive_normalization_while_local_refs_remain_outside_model_evidence_contract():
    from grading_agent.errors import AgentError
    from grading_agent.template_analysis import _normalize

    image, row, payload, tokens = fixture()
    payload.update(request_id="normalization-integration", writing_state="written")
    payload["page"]["sha256"] = "a" * 64
    row.update(candidate_id=None, page_no=1, evidence_refs=["source-page:" + "a" * 64 + ":1"])
    assert populate_objective_items([row], image, payload, tokens=tokens)["matched_items"] == 11
    # 服务端独立证据不能伪装成供应商证据；调用方先暂存，再在规范化之后追加。
    with pytest.raises(AgentError, match="model used unknown evidence"):
        _normalize([row], {}, payload)
    trusted = [ref for ref in row["evidence_refs"] if ref.startswith("local-omr:")]
    row["evidence_refs"] = [ref for ref in row["evidence_refs"] if not ref.startswith("local-omr:")]
    normalized = _normalize([row], {}, payload)
    normalized[0]["evidence_refs"].extend(trusted)
    assert normalized[0]["role"] == "objective_group"
    assert normalized[0].get("semantic_check") != "needs_review"
    assert len(normalized[0]["objective_items"]) == 11
    assert "MULTIMODAL_OBSERVATIONS_CONSISTENT" in normalized[0]["reason_codes"]
