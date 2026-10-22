import copy
from types import SimpleNamespace

import cv2
import numpy as np
import pytest
from page_processing.grading_groups import grading_group_crops
from page_processing.registration import RegistrationError, crop_regions, required_template_regions
from page_processing.runner import Runner


def _fixture():
    image = np.full((200, 300, 3), 255, np.uint8)
    cv2.putText(image, "12 ___ 13 ___ 14 ___", (25, 100), cv2.FONT_HERSHEY_SIMPLEX, .5, (0, 0, 0), 1)
    ok, encoded = cv2.imencode(".png", image)
    assert ok
    bbox = {"x": .05, "y": .2, "width": .9, "height": .5}
    regions = [{"question_id": f"q{number}", "x": .1 + index * .28, "y": .3, "width": .2, "height": .25,
                "grading_group_id": "fill-12-14", "grading_bbox": bbox.copy()} for index, number in enumerate((12, 13, 14))]
    return encoded.tobytes(), regions


def test_shared_crop_preserves_three_independent_question_positions():
    image, regions = _fixture()
    before = copy.deepcopy(regions)
    shared = grading_group_crops(image, regions)["fill-12-14"]
    children = crop_regions(image, regions)
    assert regions == before
    assert shared["question_ids"] == ["q12", "q13", "q14"]
    assert cv2.imdecode(np.frombuffer(shared["png"], np.uint8), cv2.IMREAD_COLOR).shape[:2] == (100, 270)
    assert len(children) == 3
    assert [child["normalized_bbox"]["x"] for child in children] == [.1, .38, .66]
    assert all(child["normalized_bbox"]["width"] == .2 for child in children)


@pytest.mark.parametrize("case", ["missing_bbox", "different_bbox", "outside_child", "singleton", "unrelated_inside", "duplicate_question"])
def test_invalid_groups_cannot_change_cutting_or_upload(case):
    image, regions = _fixture()
    if case == "missing_bbox": regions[0].pop("grading_bbox")
    elif case == "different_bbox": regions[0]["grading_bbox"]["x"] = .04
    elif case == "outside_child": regions[0]["grading_bbox"]["width"] = .01
    elif case == "singleton": regions = regions[:1]
    elif case == "duplicate_question": regions[1]["question_id"] = regions[0]["question_id"]
    else: regions.append({"question_id": "q15", "x": .1, "y": .6, "width": .2, "height": .05})
    with pytest.raises(RegistrationError, match="invalid_template_grading_group"):
        grading_group_crops(image, regions)


def test_individual_mode_requires_no_shared_image_and_keeps_existing_crops():
    image, regions = _fixture()
    for region in regions:
        region.pop("grading_group_id")
        region.pop("grading_bbox")
    assert grading_group_crops(image, regions) == {}
    assert len(crop_regions(image, regions)) == 3


def test_runner_uploads_group_once_and_attaches_same_version_to_each_child():
    image, regions = _fixture()
    uploads = []
    def upload(task, owner_type, owner_id, name, png):
        uploads.append((owner_type, owner_id, name, png))
        return {"id": name, "hash_sha256": "a" * 64}
    runner = Runner(SimpleNamespace(upload_asset=upload), None)
    heartbeat = SimpleNamespace(raise_if_failed=lambda: None)
    result = runner._upload_segment_crops({}, image, regions, "answer_segment_crop", "run-1", heartbeat)
    assert len(uploads) == 4
    assert sum(name.startswith("grading-group") for _, _, name, _ in uploads) == 1
    assert [row["question_id"] for row in result] == ["q12", "q13", "q14"]
    groups = [row["normalized_bbox"]["grading_group"] for row in result]
    assert groups[0] == groups[1] == groups[2]
    assert groups[0]["file_asset_id"] == "grading-group-0001.png"
    assert all(row["file_asset_id"].startswith("segment-") for row in result)


@pytest.mark.parametrize("keep_children", [True, False])
def test_independent_group_creates_missing_member_without_fabricating_child_crop(keep_children):
    image, regions = _fixture()
    group = {"group_id": "fill-12-14", "question_ids": ["q12", "q13", "q14"], "bbox": regions[0]["grading_bbox"]}
    regions = [{key: value for key, value in row.items() if key not in ("grading_group_id", "grading_bbox")}
               for row in regions[:2]] if keep_children else []
    frozen = copy.deepcopy((regions, group))
    uploads = []

    def upload(_task, _owner_type, _owner_id, name, _png):
        uploads.append(name)
        return {"id": name, "hash_sha256": "a" * 64}

    runner = Runner(SimpleNamespace(upload_asset=upload), None)
    rows = runner._upload_segment_crops({}, image, regions, "answer_segment_crop", "run-1",
                                       SimpleNamespace(raise_if_failed=lambda: None), [group])
    assert (regions, group) == frozen
    assert len(uploads) == 1 + len(regions)
    assert {row["question_id"] for row in rows} == {"q12", "q13", "q14"}
    for row in rows:
        if row["question_id"] in {region["question_id"] for region in regions}:
            assert row["file_asset_id"].startswith("segment-")
            assert "answer_scope" not in row["normalized_bbox"]
        else:
            assert row["normalized_bbox"]["answer_scope"] == "grading_group"
            assert row["file_asset_id"] == "grading-group-0001.png"
            assert row["normalized_bbox"]["width"] == group["bbox"]["width"]


@pytest.mark.parametrize("scenario", ["duplicate", "shared_member", "overlap", "wrong_child", "legacy_conflict"])
def test_independent_group_rejects_member_and_region_conflicts(scenario):
    image, regions = _fixture()
    bbox = regions[0]["grading_bbox"].copy()
    groups = [{"group_id": "fill-12-14", "question_ids": ["q12", "q13", "q14"], "bbox": bbox}]
    for region in regions:
        region.pop("grading_group_id")
        region.pop("grading_bbox")
    if scenario == "duplicate":
        groups[0]["question_ids"][2] = "q12"
    elif scenario == "shared_member":
        groups.append({"group_id": "other", "question_ids": ["q14", "q15"], "bbox": {"x": .05, "y": .75, "width": .9, "height": .2}})
    elif scenario == "overlap":
        groups.append({"group_id": "other", "question_ids": ["q15", "q16"], "bbox": bbox.copy()})
    elif scenario == "wrong_child":
        regions[2]["y"] = .8
    else:
        regions[0].update({"grading_group_id": "other", "grading_bbox": bbox.copy()})
    with pytest.raises(RegistrationError, match="invalid_template_grading_group"):
        grading_group_crops(image, regions, groups)


def test_shared_only_region_participates_in_registration_guard_without_becoming_question_roi():
    box = {"x": .1, "y": .2, "width": .8, "height": .3}
    required = required_template_regions([], [{"group_id": "fill", "question_ids": ["q12", "q14"], "bbox": box}])
    assert required == [{**box, "id": "grading-group:fill"}]
    assert "question_id" not in required[0]
