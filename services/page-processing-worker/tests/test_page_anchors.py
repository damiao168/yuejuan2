"""合成印刷标记验证配准边界；不把这些样例当成陌生真实答题卡的准确率。"""

import cv2
import numpy as np
import pytest
from edugrade_worker_runtime.page_anchors import (
    bbox_within_anchors,
    detect_page_anchors,
)
from page_processing.registration import RegistrationError, register_page


def marked_page(kind="triangle"):
    image = np.full((1100, 800, 3), 255, np.uint8)
    for corner, (x, y) in enumerate(((40, 45), (740, 45), (740, 1030), (40, 1030))):
        if kind == "triangle":
            dx = 18 if corner in (0, 3) else -18
            dy = 18 if corner in (0, 1) else -18
            cv2.fillConvexPoly(image, np.int32([(x, y), (x + dx, y), (x, y + dy)]), (0, 0, 0))
        elif kind == "square":
            cv2.rectangle(image, (x - 9, y - 9), (x + 9, y + 9), (0, 0, 0), -1)
        else:
            cv2.circle(image, (x, y), 10, (0, 0, 0), -1)
    cv2.putText(image, "EXAM 2026", (180, 130), cv2.FONT_HERSHEY_SIMPLEX, 1, (0, 0, 0), 2)
    for y in range(220, 900, 90):
        cv2.line(image, (100, y), (680, y), (0, 0, 0), 2)
        cv2.putText(image, f"Q{y}", (100, y - 8), cv2.FONT_HERSHEY_SIMPLEX, .65, (0, 0, 0), 2)
    return image


def png(image):
    ok, encoded = cv2.imencode(".png", image)
    assert ok
    return encoded.tobytes()


@pytest.mark.parametrize("kind", ["triangle", "square", "circle"])
def test_detects_four_consistent_printed_corner_marks(kind):
    anchors = detect_page_anchors(marked_page(kind))
    assert anchors is not None
    assert [mark["corner"] for mark in anchors.marks] == ["top_left", "top_right", "bottom_right", "bottom_left"]
    assert {mark["kind"] for mark in anchors.marks} == {kind}
    assert bbox_within_anchors({"x": .1, "y": .1, "width": .7, "height": .7}, anchors)
    assert not bbox_within_anchors({"x": .01, "y": .1, "width": .7, "height": .7}, anchors)
    assert anchors.as_dict()["coordinate_space"] == "source_normalized"


def test_written_content_does_not_move_printed_anchors():
    blank = marked_page()
    written = blank.copy()
    cv2.putText(written, "ANSWERS ABCD + 99", (170, 400), cv2.FONT_HERSHEY_SCRIPT_SIMPLEX, 1.3, (0, 0, 0), 3)
    cv2.circle(written, (450, 650), 45, (0, 0, 0), -1)
    assert detect_page_anchors(blank).as_dict() == detect_page_anchors(written).as_dict()


def test_missing_mark_uses_no_invented_fourth_corner():
    page = marked_page()
    page[1010:1050, 20:65] = 255
    assert detect_page_anchors(page) is None


def test_similar_extra_corner_mark_is_ambiguous():
    page = marked_page()
    cv2.fillConvexPoly(page, np.int32([(70, 45), (88, 45), (70, 63)]), (0, 0, 0))
    assert detect_page_anchors(page) is None


def test_mixed_corner_shapes_do_not_establish_template_baseline():
    page = marked_page()
    page[30:70, 20:70] = 255
    cv2.rectangle(page, (40, 45), (58, 63), (0, 0, 0), -1)
    assert detect_page_anchors(page) is None


@pytest.mark.parametrize("value", [None, np.zeros((40, 50, 3), np.uint8), np.zeros((150, 150, 4), np.uint8),
                                  np.zeros((200, 200), np.float32), np.full((400, 400, 3), 255, np.uint8)])
def test_invalid_or_unmarked_input_returns_no_baseline(value):
    assert detect_page_anchors(value) is None


def test_student_handwriting_and_changed_outer_margins_use_four_point_registration(monkeypatch):
    template = marked_page()
    source = template.copy()
    cv2.putText(source, "student writing", (170, 410), cv2.FONT_HERSHEY_SCRIPT_SIMPLEX, 1.5, (0, 0, 0), 3)
    cv2.putText(source, "MARGIN", (200, 1090), cv2.FONT_HERSHEY_SIMPLEX, 1, (0, 0, 0), 2)
    perspective = cv2.getPerspectiveTransform(np.float32([[0, 0], [800, 0], [800, 1100], [0, 1100]]),
                                              np.float32([[12, 20], [780, 5], [795, 1085], [22, 1095]]))
    source = cv2.warpPerspective(source, perspective, (800, 1100), borderValue=(255, 255, 255))
    # 明确禁止特征回退，才能证明此处真正复用四角标，而非旧 ORB 恰好通过。
    monkeypatch.setattr("page_processing.registration._feature_homography", lambda *args: pytest.fail("unexpected feature fallback"))
    result = register_page(png(source), "image/png", png(template), "image/png", required_regions=[
        {"question_id": "q1", "x": .15, "y": .3, "width": .65, "height": .15}])
    assert result.evidence.method == "printed_four_point"
    point = np.float32([[[300, 600]]])
    warped = cv2.perspectiveTransform(point, perspective)
    recovered = cv2.perspectiveTransform(warped, np.array(result.evidence.source_to_template))
    assert np.linalg.norm(recovered - point) < 1.5
    assert result.evidence.anchor_evidence["aligned_static_score"] >= .68
    assert result.evidence.required_region_visibility["status"] == "passed"


def test_required_region_outside_four_points_is_explicit_failure_even_for_identical_image():
    page = png(marked_page())
    with pytest.raises(RegistrationError, match="registration_required_region_outside_anchor_area"):
        register_page(page, "image/png", page, "image/png", required_regions=[
            {"question_id": "q1", "x": .01, "y": .2, "width": .5, "height": .5}])


def test_shared_grading_image_must_also_be_inside_stable_anchor_area():
    page = png(marked_page())
    with pytest.raises(RegistrationError, match="registration_required_region_outside_anchor_area"):
        register_page(page, "image/png", page, "image/png", required_regions=[
            {"question_id": "q1", "x": .1, "y": .2, "width": .2, "height": .1,
             "grading_group_id": "fill-12-14", "grading_bbox": {"x": .01, "y": .15, "width": .5, "height": .4}}])


def test_shared_grading_image_must_be_visible_after_feature_registration(monkeypatch):
    template = np.full((600, 800, 3), 255, np.uint8)
    cv2.rectangle(template, (100, 100), (700, 500), (0, 0, 0), 2)
    source = np.full_like(template, 255)
    matrix = np.array([[1, 0, -80], [0, 1, 0], [0, 0, 1]], dtype=np.float64)
    monkeypatch.setattr("page_processing.registration._feature_homography", lambda *args: (
        matrix, "orb", 100, 100, np.ones(100, dtype=bool), 0.0))
    with pytest.raises(RegistrationError, match="registration_required_region_incomplete"):
        register_page(png(source), "image/png", png(template), "image/png", required_regions=[
            {"question_id": "q1", "x": .2, "y": .2, "width": .2, "height": .1,
             "grading_group_id": "fill-12-14", "grading_bbox": {"x": .1, "y": .15, "width": .88, "height": .4}}])


def test_same_corner_marks_do_not_suffice_for_a_different_printed_layout(monkeypatch):
    template = marked_page()
    source = marked_page()
    source[100:1000, 80:700] = 255
    for x in range(120, 700, 60):
        cv2.rectangle(source, (x, 160), (x + 20, 950), (0, 0, 0), 3)
    monkeypatch.setattr("page_processing.registration._feature_homography", lambda *args: None)
    with pytest.raises(RegistrationError, match="registration_insufficient_matches"):
        register_page(png(source), "image/png", png(template), "image/png")


@pytest.mark.parametrize("bbox", [{"x": True, "y": .2, "width": .1, "height": .1},
                                  {"x": .2, "y": .2, "width": float("nan"), "height": .1},
                                  {"x": .2, "y": .2, "width": -.1, "height": .1}])
def test_stable_region_check_rejects_invalid_coordinates(bbox):
    assert not bbox_within_anchors(bbox, detect_page_anchors(marked_page()))
