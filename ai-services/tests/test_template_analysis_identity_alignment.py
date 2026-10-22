"""静态标签和实测结构共同定位字段；合成图仅验证边界，不代表真实 OCR 准确率。"""

import copy
import subprocess
import time
from types import SimpleNamespace

import cv2
import numpy as np
import pytest
from grading_agent import template_analysis_identity_alignment as identity_alignment
from grading_agent.template_analysis_identity_alignment import (
    _complete_lattice,
    _label_windows,
    _read_initial_static_labels,
    _read_static_labels,
    align_identity_fields,
    align_initial_identity_fields,
)


def bbox(x, y, width, height):
    return {"x": x, "y": y, "width": width, "height": height}


def fixture(*, grid=True, line=True, missing_bar=False, cells=9):
    image = np.full((500, 800, 3), 255, np.uint8)
    if line:
        cv2.line(image, (100, 150), (300, 150), (0, 0, 0), 2)
    if grid:
        cv2.rectangle(image, (100, 190), (100 + cells * 24, 214), (0, 0, 0), 1)
        for index in range(1, cells):
            if not (missing_bar and index == 3):
                cv2.line(image, (100 + index * 24, 190), (100 + index * 24, 214), (0, 0, 0), 1)
    page = {"width": 800, "height": 500}
    rows = [{"role": "identity", "identity_label": "姓名", "proposed_bbox": bbox(.12, .2, .26, .045)},
            {"role": "identity", "identity_label": "准考证号", "proposed_bbox": bbox(.12, .28, .27, .045)}]
    tokens = [{"text": "姓", **bbox(105 / 800, 111 / 500, 16 / 800, 16 / 500)},
              {"text": "名", **bbox(160 / 800, 111 / 500, 16 / 800, 16 / 500)},
              {"text": "准考证号", **bbox(105 / 800, 173 / 500, 55 / 800, 13 / 500)}]
    return image, page, rows, tokens


def align(image, page, rows, tokens=(), **changes):
    kwargs = {"tokens": tokens, "measurement_bbox": bbox(.08, .1, .4, .4),
              "label_reader": lambda _image, **_kwargs: ([], None)}
    kwargs.update(changes)
    return align_identity_fields(rows, image, page, **kwargs)


def test_real_case_shifted_boxes_include_complete_name_line_and_number_grid_after_independent_alignment():
    image, page, rows, tokens = fixture()
    before = copy.deepcopy(rows)

    result = align(image, page, rows, tokens)

    assert result["aligned_indices"] == [0, 1]
    assert result["reason_codes"] == ["LOCAL_IDENTITY_FIELD_STRUCTURE_ALIGNED"]
    assert result["label_read_count"] == 0
    assert rows[0]["proposed_bbox"] == pytest.approx(bbox(97 / 800, 109 / 500, 207 / 800, 45 / 500))
    assert rows[1]["proposed_bbox"] == pytest.approx(bbox(98 / 800, 171 / 500, 221 / 800, 46 / 500))
    for row, original in zip(rows, before, strict=True):
        assert {key: value for key, value in row.items() if key != "proposed_bbox"} == {
            key: value for key, value in original.items() if key != "proposed_bbox"}


@pytest.mark.parametrize("cells", [4, 5, 6, 7, 8, 9, 10])
def test_complete_number_grid_does_not_assume_a_fixed_digit_count(cells):
    image, page, rows, tokens = fixture(cells=cells)
    result = align(image, page, rows, tokens)
    assert result["aligned_indices"] == [0, 1]
    assert rows[1]["proposed_bbox"]["width"] * page["width"] == pytest.approx(cells * 24 + 5)


@pytest.mark.parametrize("missing", ["label", "full_label", "name_line", "number_grid", "grid_bar"])
def test_missing_static_label_or_complete_structure_preserves_affected_original_box(missing):
    image, page, rows, tokens = fixture(line=missing != "name_line", grid=missing != "number_grid", missing_bar=missing == "grid_bar")
    if missing == "label":
        tokens = []
    elif missing == "full_label":
        tokens = tokens[1:]
    before = copy.deepcopy(rows)

    result = align(image, page, rows, tokens)

    blocked = [0, 1] if missing == "label" else [0] if missing in {"full_label", "name_line"} else [1]
    assert all(rows[index] == before[index] for index in blocked)
    assert not set(blocked).intersection(result["aligned_indices"])


def test_grid_under_choice_heading_and_digits_cannot_prove_candidate_number_field():
    image, page, rows, _tokens = fixture()
    tokens = [{"text": text, **bbox(.14 + index * .01, .34, .01, .02)}
              for index, text in enumerate(["选择题", "A", "B", "C", "D", "123456789"])]
    before = copy.deepcopy(rows)
    assert align(image, page, rows, tokens)["aligned_indices"] == []
    assert rows == before


def test_two_existing_fields_cannot_claim_the_same_measured_grid():
    image, page, rows, tokens = fixture()
    rows.append(copy.deepcopy(rows[1]))
    before = copy.deepcopy(rows)
    assert align(image, page, rows, tokens)["aligned_indices"] == [0]
    assert rows[1:] == before[1:]


def test_two_compatible_underlines_are_ambiguous_and_do_not_move_name_field():
    image, page, rows, tokens = fixture()
    cv2.line(image, (100, 158), (300, 158), (0, 0, 0), 2)
    before = copy.deepcopy(rows[0])
    assert 0 not in align(image, page, rows, tokens)["aligned_indices"]
    assert rows[0] == before


def test_wrong_static_identity_label_cannot_be_translated_to_expected_model_category():
    image, page, rows, tokens = fixture()
    tokens[-1]["text"] = "学号"
    before = copy.deepcopy(rows[1])
    assert 1 not in align(image, page, rows, tokens)["aligned_indices"]
    assert rows[1] == before


def test_barcode_unknown_and_absent_fields_are_not_invented_or_modified():
    image, page, rows, tokens = fixture()
    for role, label in (("identity", "条码"), ("identity", "身份信息"), ("uncertain", "姓名")):
        rows.append({"role": role, "identity_label": label, "proposed_bbox": bbox(.12, .17, .26, .045)})
    before = copy.deepcopy(rows[2:])
    assert align(image, page, rows, tokens)["aligned_indices"] == [0, 1]
    assert rows[2:] == before
    assert len(rows) == 5


def test_structure_outside_controlled_crop_cannot_move_field_to_neighbor():
    image, page, rows, tokens = fixture()
    before = copy.deepcopy(rows)
    assert align(image, page, rows, tokens, measurement_bbox=bbox(.08, .1, .4, .12))["aligned_indices"] == []
    assert rows == before


@pytest.mark.parametrize("region", [None, bbox(-.1, .1, .4, .4), bbox(0, 0, 1, 1)])
def test_invalid_or_excessive_measurement_region_has_no_effect(region):
    image, page, rows, tokens = fixture()
    if region == bbox(0, 0, 1, 1):
        image = np.full((1000, 1200, 3), 255, np.uint8)
        page = {"width": 1200, "height": 1000}
    before = copy.deepcopy(rows)
    assert align(image, page, rows, tokens, measurement_bbox=region)["aligned_indices"] == []
    assert rows == before


def test_existing_shared_deadline_prevents_extra_label_ocr():
    image, page, rows, _tokens = fixture()
    def reader(*_args, **_kwargs):
        raise AssertionError("expired requests cannot start OCR")
    assert align(image, page, rows, [], label_reader=reader, deadline=time.monotonic() - 1)["aligned_indices"] == []


def test_ocr_failure_retains_original_geometry_and_does_not_return_raw_error():
    image, page, rows, _tokens = fixture()
    # 文字块仅触发静态补读，测试不把绘制的英文字当成身份标签。
    cv2.putText(image, "label", (105, 121), cv2.FONT_HERSHEY_SIMPLEX, .5, (0, 0, 0), 1)
    before = copy.deepcopy(rows)
    def reader(*_args, **_kwargs):
        raise subprocess.TimeoutExpired("private filled text", 1)
    result = align(image, page, rows, [], label_reader=reader)
    assert result["aligned_indices"] == []
    assert result["label_read_count"] <= 2
    assert "private" not in repr(result)
    assert rows == before


@pytest.mark.parametrize("separators", [[0, 24, 48, 96, 120, 144, 168, 192],
                                         [0, 24, 48, 72, 96, 120, 144, 168]])
def test_missing_lattice_bar_or_open_right_edge_cannot_be_replaced_with_a_guessed_grid(separators):
    assert _complete_lattice(separators, 193) is False


def test_static_reader_fuses_only_high_confidence_static_tokens_without_exposing_filled_text(monkeypatch):
    monkeypatch.setattr(identity_alignment.shutil, "which", lambda _name: "tesseract")
    calls = []
    def run(command, **kwargs):
        scale = (2, 3, 4)[len(calls)]
        calls.append((command, kwargs["timeout"]))
        header = "level\tleft\ttop\twidth\theight\tconf\ttext\n"
        values = [(0, 60, "准"), (10, 44, "考"), (20, 90, "private-student-value")]
        body = "".join(f"5\t{x * scale}\t0\t{8 * scale}\t{10 * scale}\t{confidence}\t{text}\n"
                       for x, confidence, text in values)
        return SimpleNamespace(returncode=0, stdout=(header + body).encode(), stderr=b"private error")
    monkeypatch.setattr(identity_alignment.subprocess, "run", run)

    tokens, reason = _read_static_labels(np.full((20, 50, 3), 255, np.uint8), timeout=4)

    assert reason is None
    assert tokens == [{"text": "准", "confidence": 60, "x": 0, "y": 0, "width": 8, "height": 10}]
    assert len(calls) == 3 and all(0 < timeout <= 4 for _command, timeout in calls)
    assert all(command[command.index("--psm") + 1] == "6" for command, _timeout in calls)


def test_static_label_recovery_has_two_window_limit_across_all_existing_fields():
    image, page, rows, _tokens = fixture()
    cv2.putText(image, "label", (105, 121), cv2.FONT_HERSHEY_SIMPLEX, .5, (0, 0, 0), 1)
    cv2.putText(image, "label", (105, 183), cv2.FONT_HERSHEY_SIMPLEX, .4, (0, 0, 0), 1)
    rows *= 4
    calls = []
    def reader(crop, **kwargs):
        calls.append((crop.shape, kwargs["timeout"]))
        return [], None
    before = copy.deepcopy(rows)

    result = align(image, page, rows, [], label_reader=reader)

    assert result["label_read_count"] == len(calls) <= 2
    assert rows == before
    assert all(0 < timeout <= 4 for _shape, timeout in calls)


def test_initial_identity_candidates_provide_bounded_windows_without_explicit_measurement_bbox():
    image, page, rows, tokens = fixture()
    rows[1]["proposed_bbox"] = bbox(.12, .33, .4, .08)
    original = copy.deepcopy(rows)
    result = align_initial_identity_fields(rows, image, page, tokens=tokens,
                                           label_reader=lambda *args, **kwargs: ([], None))
    assert result["aligned_indices"] == [0, 1]
    assert result["window_count"] == 1
    assert result["label_read_count"] == 0
    assert rows[0]["proposed_bbox"]["width"] < original[0]["proposed_bbox"]["width"]
    assert rows[1]["proposed_bbox"]["width"] < original[1]["proposed_bbox"]["width"]
    assert rows[0]["proposed_bbox"]["y"] + rows[0]["proposed_bbox"]["height"] < rows[1]["proposed_bbox"]["y"]


def test_initial_identity_calibration_does_not_invent_or_modify_barcode_or_unknown_fields():
    image, page, rows, tokens = fixture()
    rows[1]["proposed_bbox"] = bbox(.12, .33, .4, .08)
    extra = [{"role": "identity", "identity_label": label, "proposed_bbox": bbox(.55, .2, .2, .15)}
             for label in ("条码", "身份信息")]
    rows.extend(copy.deepcopy(extra))
    result = align_initial_identity_fields(rows, image, page, tokens=tokens,
                                           label_reader=lambda *args, **kwargs: ([], None))
    assert result["aligned_indices"] == [0, 1]
    assert rows[2:] == extra


def test_initial_alignment_without_real_static_labels_keeps_model_geometry():
    image, page, rows, _tokens = fixture()
    original = copy.deepcopy(rows)
    result = align_initial_identity_fields(rows, image, page, label_reader=lambda *args, **kwargs: ([], None))
    assert result["aligned_indices"] == []
    assert rows == original


def test_initial_alignment_expired_deadline_cannot_launch_ocr():
    image, page, rows, _tokens = fixture()
    result = align_initial_identity_fields(rows, image, page, deadline=time.monotonic() - 1,
                                           label_reader=lambda *args, **kwargs: pytest.fail("expired label OCR"))
    assert result["aligned_indices"] == []
    assert result["label_read_count"] == 0


def test_empty_static_label_band_does_not_call_opencv_with_an_empty_image():
    assert _label_windows(np.zeros((20, 20), np.uint8), (2, 5, 18, 5)) == []


def test_initial_static_reader_fuses_exact_partial_label_words_across_same_band_pixel_representations(monkeypatch):
    calls = []

    def read(_image, *, timeout, scales):
        calls.append(scales)
        values = [("考证", 10, 20), ("号", 30, 10)] if len(calls) == 1 else [("准", 0, 10)]
        return [{"text": text, "confidence": 90, "x": x, "y": 0, "width": width, "height": 10}
                for text, x, width in values], None

    monkeypatch.setattr(identity_alignment, "_read_static_labels", read)
    tokens, reason = _read_initial_static_labels(np.full((20, 60, 3), 255, np.uint8), timeout=4)
    assert reason is None
    assert calls == [(2, 4), (4,)]
    assert {token["text"] for token in tokens} == {"准", "考证", "号"}
    assert "淮" not in identity_alignment._STATIC_PARTS


def test_initial_static_reader_avoids_second_pixel_representation_when_complete_label_is_available(monkeypatch):
    calls = []

    def read(_image, *, timeout, scales):
        calls.append(scales)
        return [{"text": text, "x": index * 20, "y": 0, "width": 15, "height": 15}
                for index, text in enumerate("姓名")], None

    monkeypatch.setattr(identity_alignment, "_read_static_labels", read)
    tokens, _reason = _read_initial_static_labels(np.full((20, 60, 3), 255, np.uint8), timeout=4)
    assert len(tokens) == 2
    assert calls == [(2, 4)]


def test_candidate_number_reader_does_not_stop_when_only_shorter_exam_number_category_is_complete(monkeypatch):
    calls = []

    def read(_image, *, timeout, scales):
        calls.append(scales)
        values = [("考证", 20, 22), ("号", 47, 8), ("考", 20, 8), ("证", 34, 8)]
        if len(calls) > 1:
            values = [("准", 3, 12), ("考", 20.25, 7), ("证", 34.25, 8)]
        return [{"text": text, "confidence": 90, "x": x, "y": 0, "width": width, "height": 10}
                for text, x, width in values], None

    monkeypatch.setattr(identity_alignment, "_read_static_labels", read)
    tokens, _reason = _read_initial_static_labels(np.full((20, 60, 3), 255, np.uint8), timeout=4, label="准考证号")
    assert calls == [(2, 4), (4,)]
    assert len(tokens) == 5
    matches = identity_alignment._matching_labels(tokens, "准考证号", (0, 12, 100, 32), "grid")
    assert matches == [(3, 0, 55, 10)]


def test_complete_current_target_label_avoids_extra_ocr_even_if_longer_alias_exists(monkeypatch):
    calls = []

    def read(_image, *, timeout, scales):
        calls.append(scales)
        return [{"text": text, "x": index * 20, "y": 0, "width": 15, "height": 15}
                for index, text in enumerate("考号")], None

    monkeypatch.setattr(identity_alignment, "_read_static_labels", read)
    tokens, _reason = _read_initial_static_labels(np.full((20, 60, 3), 255, np.uint8), timeout=4, label="考号")
    assert len(tokens) == 2
    assert calls == [(2, 4)]
