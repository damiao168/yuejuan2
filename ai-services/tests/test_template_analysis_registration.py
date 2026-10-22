import cv2
import numpy as np
import pytest
from grading_agent.template_analysis_registration import (
    prepare_registration,
    rectify_registration,
    region_within_registration,
    transform_registration_bbox,
)


def test_printed_marks_can_be_stored_without_changing_page_coordinate_system():
    image = np.full((600, 800, 3), 255, np.uint8)
    for x, y in ((35, 35), (755, 35), (755, 555), (35, 555)):
        cv2.rectangle(image, (x, y), (x + 10, y + 10), (0, 0, 0), -1)
    result = prepare_registration(image, 2)
    assert result["status"] == "detected"
    assert result["coordinate_space"] == "source_normalized"
    assert len(result["registration_marks"]) == 4
    assert result["registration_marks"][0]["id"] == "registration-p2-top_left"
    assert result["registration_marks"][0]["review_state"] == "suggested"
    assert result["registration_marks"][0]["semantic_check"] == "supported"
    assert region_within_registration({"x": .2, "y": .2, "width": .6, "height": .6}, result)
    assert not region_within_registration({"x": .01, "y": .2, "width": .6, "height": .6}, result)


def test_no_marks_preserves_existing_full_page_analysis():
    result = prepare_registration(np.full((600, 800, 3), 255, np.uint8), 1)
    assert result["status"] == "not_found"
    assert result["registration_marks"] == []
    assert region_within_registration({"x": .01, "y": .01, "width": .95, "height": .95}, result)


def test_rectification_retains_original_image_and_provides_inverse_pixel_mapping():
    image = np.full((600, 800, 3), 255, np.uint8)
    for x, y in ((40, 50), (755, 30), (745, 550), (50, 565)):
        cv2.rectangle(image, (x, y), (x + 10, y + 10), (0, 0, 0), -1)
    original = image.copy()
    registration = prepare_registration(image, 2)
    result = rectify_registration(image, registration)
    assert result["rectified"]
    assert np.array_equal(image, original)
    assert result["image"].shape == image.shape
    matrix = np.array(result["source_to_rectified"])
    inverse = np.array(result["rectified_to_source"])
    assert np.allclose(matrix @ inverse, np.eye(3))
    expected = np.float32([[point["x"] * 800, point["y"] * 600]
                           for point in result["registration"]["anchor_points"]])
    source = np.float32([[[point["x"] * 800, point["y"] * 600]
                          for point in registration["anchor_points"]]])
    assert np.allclose(cv2.perspectiveTransform(source, matrix)[0], expected, atol=.01)
    assert len(result["registration"]["registration_marks"]) == 4


def test_no_anchor_rectification_is_identity():
    image = np.full((600, 800, 3), 255, np.uint8)
    result = rectify_registration(image)
    assert not result["rectified"]
    assert result["image"] is image
    assert result["source_to_rectified"] == np.eye(3).tolist()


def test_normalized_bbox_mapping_uses_pixel_units_and_does_not_clip_outside_bounds():
    matrix = [[1, 0, -80], [0, 1, 30], [0, 0, 1]]
    result = transform_registration_bbox({"x": .05, "y": .1, "width": .2, "height": .3}, matrix, 800, 600)
    assert result == pytest.approx({"x": -.05, "y": .15, "width": .2, "height": .3})
    restored = transform_registration_bbox(result, np.linalg.inv(matrix), 800, 600)
    assert restored == pytest.approx({"x": .05, "y": .1, "width": .2, "height": .3})


def test_bbox_crossing_projective_singularity_is_not_reported_as_finite_position():
    with pytest.raises(ValueError, match="registration_bbox_projection_invalid"):
        transform_registration_bbox({"x": .1, "y": .1, "width": .4, "height": .3},
                                    [[1, 0, 0], [0, 1, 0], [.01, 0, -2]], 800, 600)


@pytest.mark.parametrize("quad", [[], [{"x": 0, "y": 0}] * 4,
                                  [{"x": float("nan"), "y": 0}] * 4])
def test_bad_internal_stable_quad_cannot_silently_accept_regions(quad):
    assert not region_within_registration({"x": .1, "y": .1, "width": .1, "height": .1},
                                          {"status": "detected", "stable_quad": quad, "width": 800, "height": 600})
