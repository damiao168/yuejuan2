import cv2
import numpy as np
import pytest
from page_processing.registration import (
    RegistrationError,
    TemplateMatchCandidate,
    crop_regions,
    inspect_template_guard,
    match_template_candidates,
    register_page,
    register_page_manual,
)


def _png(image: np.ndarray) -> bytes:
    ok, encoded = cv2.imencode(".png", image)
    assert ok
    return encoded.tobytes()


def test_registers_perspective_page_and_crops_real_region() -> None:
    template = np.full((900, 700, 3), 255, np.uint8)
    for y in range(80, 850, 80):
        cv2.line(template, (40, y), (660, y), (0, 0, 0), 2)
    cv2.putText(template, "EDUGRADE Q1 2026", (70, 145), cv2.FONT_HERSHEY_SIMPLEX, 1.2, (0, 0, 0), 3)
    cv2.rectangle(template, (100, 250), (600, 700), (0, 0, 0), 4)
    source_corners = np.float32([[0, 0], [700, 0], [700, 900], [0, 900]])
    skewed_corners = np.float32([[35, 20], [670, 45], [690, 860], [20, 885]])
    skew = cv2.getPerspectiveTransform(source_corners, skewed_corners)
    source = cv2.warpPerspective(template, skew, (700, 900), borderValue=(255, 255, 255))

    result = register_page(_png(source), "image/png", _png(template), "image/png")
    crops = crop_regions(result.registered_png, [{"question_id": "q1", "label": "Q1", "x": 0.14, "y": 0.27, "width": 0.72, "height": 0.52}])

    assert result.evidence.method in {"orb", "akaze"}
    assert result.evidence.inlier_count >= 8
    assert result.evidence.confidence >= 0.6
    assert len(result.evidence.source_to_template) == 3
    assert crops[0]["question_id"] == "q1"
    assert len(crops[0]["png"]) > 1000


def test_identical_page_uses_identity_registration() -> None:
    page = np.full((300, 200, 3), 255, np.uint8)
    cv2.putText(page, "Q1", (30, 120), cv2.FONT_HERSHEY_SIMPLEX, 2, (0, 0, 0), 3)
    result = register_page(_png(page), "image/png", _png(page), "image/png")
    assert result.evidence.method == "pixel_identity"
    assert result.evidence.confidence == 1.0


def test_registration_rejects_missing_required_region_even_when_global_area_passes(monkeypatch):
    source = np.full((1000, 1000, 3), 255, np.uint8)
    template = source.copy()
    cv2.rectangle(template, (100, 100), (900, 900), (0, 0, 0), 10)
    matrix = np.array([[1, 0, -100], [0, 1, 0], [0, 0, 1]], dtype=np.float64)
    monkeypatch.setattr("page_processing.registration._feature_homography", lambda *args: (
        matrix, "orb", 100, 100, np.ones(100, dtype=bool), 0.0))
    with pytest.raises(RegistrationError, match="registration_required_region_incomplete"):
        register_page(_png(source), "image/png", _png(template), "image/png", required_regions=[
            {"question_id": "q19", "x": .9, "y": .1, "width": .08, "height": .8}])


def test_registration_records_actual_required_region_visibility():
    page = np.full((300, 200, 3), 255, np.uint8)
    result = register_page(_png(page), "image/png", _png(page), "image/png", required_regions=[
        {"question_id": "q1", "x": .1, "y": .1, "width": .8, "height": .8}])
    assert result.evidence.required_region_visibility == {
        "status": "passed", "region_count": 1, "minimum_visible_ratio": 1.0,
        "coordinate_space": "template_pixels"}


def test_template_guard_ignores_marks_inside_configured_answer_region() -> None:
    template = np.full((500, 350, 3), 255, np.uint8)
    cv2.rectangle(template, (20, 20), (330, 480), (0, 0, 0), 3)
    cv2.putText(template, "EXAM 2026", (55, 80), cv2.FONT_HERSHEY_SIMPLEX, 0.9, (0, 0, 0), 2)
    cv2.rectangle(template, (60, 180), (290, 350), (0, 0, 0), 2)
    source = template.copy()
    cv2.circle(source, (170, 260), 45, (0, 0, 0), -1)

    evidence = inspect_template_guard(
        _png(source),
        "image/png",
        _png(template),
        "image/png",
        question_regions=[{"x": 0.15, "y": 0.34, "width": 0.7, "height": 0.4}],
    )

    assert evidence.passed is True
    assert evidence.score >= 0.68


def test_template_guard_rejects_wrong_orientation_before_registration() -> None:
    portrait = np.full((500, 350, 3), 255, np.uint8)
    landscape = np.full((350, 500, 3), 255, np.uint8)
    cv2.putText(portrait, "PORTRAIT", (30, 100), cv2.FONT_HERSHEY_SIMPLEX, 1, (0, 0, 0), 2)
    cv2.putText(landscape, "LANDSCAPE", (30, 100), cv2.FONT_HERSHEY_SIMPLEX, 1, (0, 0, 0), 2)

    evidence = inspect_template_guard(_png(landscape), "image/png", _png(portrait), "image/png")

    assert evidence.passed is False
    assert evidence.orientation_match is False


def test_full_template_match_uses_geometric_proof_after_coarse_guard() -> None:
    template = np.full((600, 420, 3), 255, np.uint8)
    cv2.rectangle(template, (20, 20), (400, 580), (0, 0, 0), 3)
    for index, y in enumerate(range(90, 550, 70), start=1):
        cv2.putText(template, f"Q{index}", (45, y), cv2.FONT_HERSHEY_SIMPLEX, 0.8, (0, 0, 0), 2)
        cv2.line(template, (110, y - 15), (370, y - 15), (0, 0, 0), 2)
    wrong = np.full((420, 600, 3), 255, np.uint8)
    cv2.putText(wrong, "WRONG LANDSCAPE", (40, 180), cv2.FONT_HERSHEY_SIMPLEX, 1, (0, 0, 0), 3)

    outcome = match_template_candidates(_png(template), "image/png", [
        TemplateMatchCandidate("wrong", "sha256:wrong", _png(wrong), "image/png", 1, []),
        TemplateMatchCandidate("right", "sha256:right", _png(template), "image/png", 1, []),
    ])

    assert outcome.decision == "matched"
    assert outcome.selected_template_id == "right"
    assert outcome.registration is not None


def test_full_template_match_rejects_near_duplicate_versions_without_margin() -> None:
    template = np.full((400, 300, 3), 255, np.uint8)
    cv2.rectangle(template, (20, 20), (280, 380), (0, 0, 0), 3)
    cv2.putText(template, "SAME LAYOUT", (35, 120), cv2.FONT_HERSHEY_SIMPLEX, 0.7, (0, 0, 0), 2)
    data = _png(template)

    outcome = match_template_candidates(data, "image/png", [
        TemplateMatchCandidate("v1", "sha256:v1", data, "image/png", 1, []),
        TemplateMatchCandidate("v2", "sha256:v2", data, "image/png", 1, []),
    ])

    assert outcome.decision == "ambiguous"
    assert outcome.selected_template_id is None
    assert outcome.margin == pytest.approx(0.0)


def test_manual_four_point_registration_produces_stable_output() -> None:
    page = np.full((400, 300, 3), 255, np.uint8)
    cv2.rectangle(page, (20, 20), (280, 380), (0, 0, 0), 3)
    points = [{"x": 0.0, "y": 0.0}, {"x": 1.0, "y": 0.0}, {"x": 1.0, "y": 1.0}, {"x": 0.0, "y": 1.0}]

    result = register_page_manual(_png(page), "image/png", _png(page), "image/png", points, points)

    assert result.evidence.method == "manual_four_point"
    assert result.evidence.coverage == pytest.approx(1.0, abs=0.02)
    assert result.width == 300
    assert result.height == 400


@pytest.mark.parametrize("points,error", [
    ([{"x": 0.0, "y": 0.0}] * 4, "manual_registration_points_duplicate"),
    ([{"x": 0.0, "y": 0.0}, {"x": 1.0, "y": 1.0}, {"x": 1.0, "y": 0.0}, {"x": 0.0, "y": 1.0}], "manual_registration_points_crossed"),
    ([{"x": -0.1, "y": 0.0}, {"x": 1.0, "y": 0.0}, {"x": 1.0, "y": 1.0}, {"x": 0.0, "y": 1.0}], "manual_registration_points_out_of_range"),
])
def test_manual_registration_rejects_invalid_geometry(points: list[dict], error: str) -> None:
    page = np.full((100, 100, 3), 255, np.uint8)
    target = [{"x": 0.0, "y": 0.0}, {"x": 1.0, "y": 0.0}, {"x": 1.0, "y": 1.0}, {"x": 0.0, "y": 1.0}]
    page_bytes = _png(page)
    page_bytes_2 = _png(page)
    with pytest.raises(RegistrationError, match=error):
        register_page_manual(page_bytes, 'image/png', page_bytes_2, 'image/png', points, target)
