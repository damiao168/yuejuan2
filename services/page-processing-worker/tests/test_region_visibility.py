import numpy as np
import pytest
from page_processing.region_visibility import (
    RegionVisibilityError,
    inspect_required_regions,
)


def test_full_page_area_does_not_hide_a_missing_answer_region():
    # 平移不改变页面面积比，但右下侧必需作答区已经落到来源图之外。
    matrix = [[1, 0, -100], [0, 1, -50], [0, 0, 1]]
    with pytest.raises(RegionVisibilityError, match="registration_required_region_incomplete"):
        inspect_required_regions(matrix, (1000, 1000), (1000, 1000), [
            {"x": .85, "y": .8, "width": .12, "height": .18}])


def test_unchanged_answer_regions_remain_visible_under_translation():
    result = inspect_required_regions([[1, 0, -100], [0, 1, -50], [0, 0, 1]],
                                      (1000, 1000), (1000, 1000), [
        {"x": .1, "y": .1, "width": .5, "height": .5}])
    assert result["status"] == "passed"
    assert result["minimum_visible_ratio"] == pytest.approx(1)


def test_no_region_is_not_reported_as_visibility_verified():
    assert inspect_required_regions(np.eye(3), (100, 100), (100, 100), []) == {
        "status": "not_evaluated", "region_count": 0}


def test_projective_singularity_cannot_create_visibility_evidence():
    with pytest.raises(RegionVisibilityError, match="registration_roi_evidence_invalid"):
        inspect_required_regions([[1, 0, 0], [0, 1, 0], [.02, 0, -1]],
                                 (100, 100), (100, 100), [{"x": .1, "y": .1, "width": .2, "height": .2}])


@pytest.mark.parametrize("invalid", [float("nan"), True, -1])
def test_invalid_region_does_not_pass_by_accident(invalid):
    with pytest.raises(RegionVisibilityError, match="invalid_template_region"):
        inspect_required_regions(np.eye(3), (100, 100), (100, 100), [
            {"x": invalid, "y": .1, "width": .2, "height": .2}])
