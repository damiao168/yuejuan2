"""按职责保留现有断言与固定响应，不改变模型协议或运行输入。"""

import base64
import copy
import hashlib
import io

import cv2
import numpy as np
import pytest
from grading_agent.answer_sheet_vision import (
    VisionInputError,
    decode_display_page,
    detect_printed_frames,
    snap_to_printed_frame,
)
from grading_agent.errors import AgentError
from grading_agent.template_analysis import TemplateAnalyzer
from PIL import Image
from test_template_analysis import (
    StructuredModel,
    discovered_row,
    output_for,
    page_image,
    payload,
    unframed_page,
)


@pytest.mark.parametrize("written", [False, True])
def test_geometry_detects_complete_frames_including_blank_answer_space(written):
    frames = detect_printed_frames(page_image(written), 1)
    assert len(frames) == 2
    assert frames[0]["bbox"]["x"] == pytest.approx(59 / 600, abs=2 / 600)
    assert frames[0]["bbox"]["width"] > 0.79
    assert frames[0]["bbox"]["height"] > 0.29
    assert frames == detect_printed_frames(page_image(written), 1)


def test_geometry_does_not_create_blank_space_or_handwriting_boxes_without_frames():
    image = np.full((800, 600, 3), 255, np.uint8)
    cv2.putText(image, "17 x=2", (60, 100), cv2.FONT_HERSHEY_SIMPLEX, 1, (0, 0, 0), 2)
    assert detect_printed_frames(image, 1) == []
    result = TemplateAnalyzer(prompt_variant="v6").analyze(payload(image, candidates=[]))
    assert result["candidates"] == []
    assert "NO_GEOMETRY_CANDIDATES" in result["reason_codes"]
    assert result["semantic_status"] == "not_configured"
    assert result["model_usage"]["request_count"] == 0


def test_identity_correction_is_not_reset_to_the_original_large_geometry_hint():
    request = payload(unframed_page(), candidates=[{
        "candidate_id": "broad-header", "bbox": {"x": .02, "y": .01, "width": .95, "height": .1},
        "evidence_refs": ["local-frame:header"],
    }])
    box = {"x": .05, "y": .02, "width": .3, "height": .05}
    row = discovered_row(None, box, "identity")
    row.update(candidate_id="broad-header", identity_label="姓名", evidence_refs=["local-frame:header"])
    result = TemplateAnalyzer(StructuredModel({"candidates": [row]}), prompt_variant="v6").analyze(request)
    candidate = result["candidates"][0]
    assert result["semantic_status"] == "completed"
    assert candidate["proposed_bbox"] == box
    assert candidate["source_bbox"] == request["candidates"][0]["bbox"]
    assert "GEOMETRY_NOT_VERIFIED" in candidate["reason_codes"]


def test_small_identity_field_does_not_snap_to_a_larger_nearby_header_frame():
    # 原吸附容差按整页计算；高度只占 5% 的身份栏会被扩展到相邻边界，现按字段自身尺寸收紧。
    image = np.full((800, 600, 3), 255, np.uint8)
    cv2.rectangle(image, (60, 80), (540, 128), (0, 0, 0), 2)
    request = payload(image, candidates=[])
    box = {"x": .1, "y": .1, "width": .8, "height": .05}
    frames = detect_printed_frames(image, 1)
    assert snap_to_printed_frame(box, frames)[2] == "PRINTED_FRAME_VERIFIED"
    row = discovered_row(None, box, "identity")
    row["identity_label"] = "学号"
    result = TemplateAnalyzer(StructuredModel({"candidates": [row]}), prompt_variant="v6").analyze(request)
    assert result["candidates"][0]["proposed_bbox"] == box
    assert "NO_PRINTED_FRAME_REQUIRES_REVIEW" in result["candidates"][0]["reason_codes"]


def test_identity_field_with_matching_printed_frame_keeps_cv_evidence():
    request = payload()
    output = output_for(request)
    output["candidates"] = output["candidates"][:1]
    output["candidates"][0].update(role="identity", identity_label="身份信息")
    result = TemplateAnalyzer(StructuredModel(output), prompt_variant="v6").analyze(request)
    candidate = result["candidates"][0]
    assert result["semantic_status"] == "completed"
    assert candidate["proposed_bbox"] == request["candidates"][0]["bbox"]
    assert "PRINTED_FRAME_VERIFIED" in candidate["reason_codes"]


def test_handwriting_sized_model_bbox_cannot_shrink_the_printed_template_frame():
    request = payload(page_image(written=True))
    output = output_for(request)
    output["candidates"][0]["proposed_bbox"] = {"x": 0.25, "y": 0.2, "width": 0.2, "height": 0.05}
    result = TemplateAnalyzer(StructuredModel(output), prompt_variant="v6").analyze(request)
    row = result["candidates"][0]
    assert row["proposed_bbox"] == row["source_bbox"]
    assert "GEOMETRY_NOT_VERIFIED" in row["reason_codes"]
    assert "MODEL_GEOMETRY_REQUIRES_REVIEW" in row["reason_codes"]


def test_model_cannot_move_a_candidate_to_another_genuine_frame():
    request = payload()
    output = output_for(request)
    output["candidates"][0]["proposed_bbox"] = dict(request["candidates"][1]["bbox"])
    result = TemplateAnalyzer(StructuredModel(output), prompt_variant="v6").analyze(request)
    row = result["candidates"][0]
    assert row["proposed_bbox"] == row["source_bbox"]
    assert "MODEL_GEOMETRY_DISAGREEMENT" in row["reason_codes"]


def test_supplied_geometry_snaps_only_with_nearby_printed_frame_evidence():
    frames = detect_printed_frames(page_image(), 1)
    bbox = dict(frames[0]["bbox"])
    bbox["x"] += 0.005
    refined, refs, reason = snap_to_printed_frame(bbox, frames)
    assert refined == frames[0]["bbox"]
    assert refs == frames[0]["evidence_refs"]
    assert reason == "PRINTED_FRAME_VERIFIED"


@pytest.mark.parametrize("change", ["checksum", "dimensions", "not_image", "duplicate", "nonfinite", "out_of_bounds", "unexpected_field"])
def test_invalid_inputs_fail_before_calling_the_model(change):
    request = payload()
    if change == "checksum":
        request["page"]["sha256"] = "0" * 64
    elif change == "dimensions":
        request["page"]["width"] += 1
    elif change == "not_image":
        raw = b"not an image"
        request["page"].update(data_base64=base64.b64encode(raw).decode(), sha256=hashlib.sha256(raw).hexdigest())
    elif change == "duplicate":
        request["candidates"].append(copy.deepcopy(request["candidates"][0]))
    elif change == "nonfinite":
        request["candidates"][0]["bbox"]["x"] = float("nan")
    elif change == "out_of_bounds":
        request["candidates"][0]["bbox"]["width"] = 1
    else:
        request["publish"] = True
    model = StructuredModel()
    with pytest.raises(AgentError) as caught:
        TemplateAnalyzer(model, prompt_variant="v6").analyze(request)
    assert caught.value.status == 400
    assert model.calls == []


def test_exif_orientation_is_rejected_instead_of_silently_changing_coordinates():
    image = Image.new("RGB", (100, 200), "white")
    exif = Image.Exif()
    exif[274] = 6
    buffer = io.BytesIO()
    image.save(buffer, format="JPEG", exif=exif)
    with pytest.raises(VisionInputError):
        decode_display_page(buffer.getvalue(), {"width": 100, "height": 200, "media_type": "image/jpeg"})
