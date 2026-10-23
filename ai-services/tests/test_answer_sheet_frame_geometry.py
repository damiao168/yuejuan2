import itertools

import cv2
import numpy as np
import pytest
from grading_agent.answer_sheet_vision import (
    detect_printed_frames,
    regions_overlap,
    snap_to_printed_frame,
)
from PIL import Image, ImageDraw


def pixel_edges(frame, width, height):
    box = frame["bbox"]
    return tuple(round(value) for value in (
        box["x"] * width, box["y"] * height,
        (box["x"] + box["width"]) * width, (box["y"] + box["height"]) * height))


def rounded_columns(thickness=2):
    width, height = 1600, 1133
    image = np.full((height, width, 3), 255, np.uint8)
    expected = []
    for left in (114, 592, 1063):
        layer = Image.new("RGB", (width, height), "white")
        ImageDraw.Draw(layer).rounded_rectangle(
            (left, 86, left + 440, 1027), radius=14, outline="black", width=thickness)
        layer = cv2.warpAffine(
            np.asarray(layer), np.array([[1, -.004, 0], [.001, 1, 0]], np.float32),
            (width, height), flags=cv2.INTER_NEAREST, borderValue=(255, 255, 255))
        rows, columns = np.where(layer[:, :, 0] < 128)
        expected.append((int(columns.min()), int(rows.min()), int(columns.max() + 1), int(rows.max() + 1)))
        image = np.minimum(image, layer)
    return image, expected


@pytest.mark.parametrize("thickness", [1, 2, 3])
def test_rounded_slightly_skewed_columns_keep_measured_outer_edges(thickness):
    # 圆角会被横、竖开运算分别切断；仍须找到完整三列，不能退回模型近似框。
    image, expected = rounded_columns(thickness)
    frames = detect_printed_frames(image, 2)
    assert len(frames) == 3
    for actual, outer_edges in zip(frames, expected):
        assert pixel_edges(actual, 1600, 1133) == pytest.approx(outer_edges, abs=1)
    assert frames == detect_printed_frames(image, 2)


def test_outside_annotation_touching_a_frame_cannot_expand_its_boundary():
    image, expected = rounded_columns()
    for left, top, right, _ in expected:
        cv2.putText(image, "17: 15", (right - 140, top + 1), cv2.FONT_HERSHEY_SIMPLEX,
                    .7, (255, 0, 0), 2)
        cv2.line(image, (right - 1, top), (right + 6, top - 8), (255, 0, 0), 2)
        cv2.putText(image, "x = 2", (left + 30, top + 130), cv2.FONT_HERSHEY_SIMPLEX,
                    1.2, (0, 0, 0), 2)
    frames = detect_printed_frames(image, 1)
    assert len(frames) == 3
    for actual, outer_edges in zip(frames, expected):
        assert pixel_edges(actual, 1600, 1133) == pytest.approx(outer_edges, abs=1)


def test_rounded_frame_evidence_snaps_a_small_model_inset_to_real_edges():
    image, _ = rounded_columns()
    frames = detect_printed_frames(image, 2)
    for frame in frames:
        source = frame["bbox"]
        inset = {"x": source["x"] + 4 / 1600, "y": source["y"] + 3 / 1133,
                 "width": source["width"] - 10 / 1600, "height": source["height"] - 10 / 1133}
        refined, references, reason = snap_to_printed_frame(inset, frames)
        assert refined == source
        assert references == frame["evidence_refs"]
        assert reason == "PRINTED_FRAME_VERIFIED"


def test_missing_edge_does_not_create_a_complete_printed_frame():
    image = np.full((800, 600, 3), 255, np.uint8)
    cv2.line(image, (60, 100), (540, 100), (0, 0, 0), 2)
    cv2.line(image, (540, 100), (540, 720), (0, 0, 0), 2)
    cv2.line(image, (60, 720), (540, 720), (0, 0, 0), 2)
    cv2.putText(image, "x = 2", (150, 220), cv2.FONT_HERSHEY_SIMPLEX, 1.4, (0, 0, 0), 3)
    assert detect_printed_frames(image, 1) == []


def test_supported_outer_edges_do_not_add_padding_to_a_square_frame():
    image = np.full((800, 600, 3), 255, np.uint8)
    cv2.rectangle(image, (60, 100), (540, 720), (0, 0, 0), 3)
    frames = detect_printed_frames(image, 1)
    assert len(frames) == 1
    assert pixel_edges(frames[0], 600, 800) == pytest.approx((58, 98, 543, 723), abs=1)


@pytest.mark.parametrize("axis", ["horizontal", "vertical"])
def test_adjacent_cells_share_only_the_measured_stroke_center(axis):
    image = np.full((800, 600, 3), 255, np.uint8)
    for index in range(3):
        start, end = ((60, 100 + index * 180), (540, 280 + index * 180)) if axis == "horizontal" else (
            (60 + index * 160, 100), (220 + index * 160, 720))
        cv2.rectangle(image, start, end, (0, 0, 0), 2)
    frames = detect_printed_frames(image, 1)
    # 表格总外框也可作为未分类候选；这里仅核对三个单元的公共边。
    dimension = "height" if axis == "horizontal" else "width"
    frames = [frame for frame in frames if frame["bbox"][dimension] < .5]
    assert len(frames) == 3
    for first, second in itertools.pairwise(frames):
        assert not regions_overlap(first["bbox"], second["bbox"], 600, 800)
        a, b = first["bbox"], second["bbox"]
        edge, size = ("y", "height") if axis == "horizontal" else ("x", "width")
        assert a[edge] + a[size] == pytest.approx(b[edge])
    assert pixel_edges(frames[0], 600, 800)[:2] == pytest.approx((59, 99), abs=1)
    assert pixel_edges(frames[-1], 600, 800)[2:] == pytest.approx((542, 642 if axis == "horizontal" else 722), abs=1)


def test_independent_overlapping_frames_are_still_a_geometry_conflict():
    image = np.full((800, 600, 3), 255, np.uint8)
    cv2.rectangle(image, (60, 100), (480, 500), (0, 0, 0), 2)
    cv2.rectangle(image, (160, 350), (540, 720), (0, 0, 0), 2)
    frames = detect_printed_frames(image, 1)
    first = next(frame for frame in frames if abs(frame["bbox"]["x"] - 59 / 600) < .005)
    second = next(frame for frame in frames if abs(frame["bbox"]["x"] - 159 / 600) < .005)
    assert regions_overlap(first["bbox"], second["bbox"], 600, 800)
