"""补漏只产生局部复判提示；专用定位器没有发现码时不能按页位补框。"""

import cv2
import numpy as np
import pytest
from grading_agent.template_analysis_barcode_discovery import discover_barcode_regions
from grading_agent.template_analysis_semantics import LocalContentEvidence


def page(image):
    return {"page_no": 1, "width": image.shape[1], "height": image.shape[0], "sha256": "a" * 64}


class Detector:
    def __init__(self, points=None, *, error=None):
        self.points, self.error, self.calls = points, error, []

    def detectMulti(self, image):
        self.calls.append(image.shape)
        if self.error:
            raise self.error
        return self.points is not None, self.points


def quad(x=100, y=70, width=160, height=40):
    return np.array([[[x, y], [x + width, y], [x + width, y + height], [x, y + height]]], np.float32)


def test_localized_barcode_is_only_a_bounded_model_hint_with_no_decoded_values():
    image = np.full((300, 400, 3), 255, np.uint8)
    detector = Detector(quad())
    hints, reasons = discover_barcode_regions(image, page(image), detectors={"条码": detector})
    assert reasons == [] and len(hints) == 1
    hint = hints[0]
    assert hint["identity_label"] == "条码"
    assert hint["reason_codes"] == ["LOCAL_BARCODE_DETECTOR_LOCALIZED", "LOCAL_BARCODE_OUTER_FRAME_REQUIRES_MODEL"]
    assert set(hint) == {"candidate_id", "bbox", "anchor_bbox", "boundary_kind", "identity_label", "reason_codes", "evidence_refs"}
    assert not any(key in hint for key in ("semantic_check", "status", "decoded", "text", "value"))
    assert hint["anchor_bbox"]["x"] < .25 and hint["anchor_bbox"]["x"] + hint["anchor_bbox"]["width"] > .65
    assert hint["anchor_bbox"]["width"] < .43
    assert hint["boundary_kind"] == "context_only"
    assert hint["bbox"]["width"] > hint["anchor_bbox"]["width"]
    assert detector.calls == [(300, 400, 3)]


def test_large_page_uses_measured_coordinates_after_bounded_downsampling():
    image = np.full((2400, 3200, 3), 255, np.uint8)
    detector = Detector(quad())
    hints, _ = discover_barcode_regions(image, page(image), detectors={"二维码": detector})
    assert detector.calls == [(1200, 1600, 3)]
    assert .055 < hints[0]["anchor_bbox"]["x"] < .065
    assert hints[0]["identity_label"] == "二维码"


def test_default_barcode_scales_preserve_small_codes_without_second_512_pixel_resize(monkeypatch):
    image = np.full((1132, 1600, 3), 255, np.uint8)
    calls = []
    detector = Detector()
    detector.setDownsamplingThreshold = lambda value: calls.append(("threshold", value))
    detector.setDetectorScales = lambda value: calls.append(("scales", value))
    monkeypatch.setattr(cv2, "barcode_BarcodeDetector", lambda: detector)
    monkeypatch.setattr(cv2, "QRCodeDetector", lambda: Detector())
    assert discover_barcode_regions(image, page(image)) == ([], [])
    assert calls == [("threshold", 1600.), ("scales", [.004, .01, .02, .03])]


@pytest.mark.parametrize("points", [
    np.array([[[0, 0], [20, 0], [float("nan"), 20], [0, 20]]]),
    quad(-3), quad(width=2), quad(width=390, height=250),
    np.array([[[10, 10], [40, 40], [10, 40], [40, 10]]]),
    np.ones((3, 2)),
])
def test_invalid_or_unbounded_detector_geometry_is_not_proposed(points):
    image = np.full((300, 400, 3), 255, np.uint8)
    hints, _ = discover_barcode_regions(image, page(image), detectors={"条码": Detector(points)})
    assert hints == []


def test_no_barcode_is_a_normal_empty_result_with_no_missing_field_error():
    image = np.full((300, 400, 3), 255, np.uint8)
    assert discover_barcode_regions(image, page(image), detectors={"条码": Detector(), "二维码": Detector()}) == ([], [])


def test_warning_strokes_do_not_become_barcode_without_a_specialized_detection():
    image = np.full((300, 400, 3), 255, np.uint8)
    cv2.rectangle(image, (80, 50), (300, 250), (0, 0, 0), 2)
    cv2.putText(image, "DO NOT WRITE", (100, 140), cv2.FONT_HERSHEY_SIMPLEX, .6, (0, 0, 0), 2)
    hints, _ = discover_barcode_regions(image, page(image))
    assert hints == []


def test_static_placeholder_label_must_have_a_real_measured_frame():
    image = np.full((300, 400, 3), 255, np.uint8)
    frame = {"bbox": {"x": .1, "y": .1, "width": .4, "height": .2}}
    label = {"text": "条形码粘贴区", "x": .2, "y": .15, "width": .1, "height": .03}
    hints, reasons = discover_barcode_regions(image, page(image), [frame], ocr=lambda _: ([label], None), detectors={})
    assert reasons == [] and len(hints) == 1
    assert hints[0]["bbox"] == frame["bbox"]
    assert hints[0]["reason_codes"] == ["LOCAL_BARCODE_LABELLED_FRAME_DISCOVERED", "LOCAL_BARCODE_OUTER_FRAME_LOCALIZED"]
    assert hints[0]["boundary_kind"] == "printed_frame" and hints[0]["anchor_bbox"] is None
    assert discover_barcode_regions(image, page(image), ocr=lambda _: ([label], None), detectors={}) == ([], [])


@pytest.mark.parametrize("text", ["禁止作答", "请在条码区作答", "PRIVATE_PERSONAL_VALUE", "1234567890"])
def test_warning_body_and_personal_values_are_not_static_placeholder_labels(text):
    image = np.full((300, 400, 3), 255, np.uint8)
    frame = {"bbox": {"x": .1, "y": .1, "width": .4, "height": .2}}
    token = {"text": text, "x": .2, "y": .15, "width": .1, "height": .03}
    assert discover_barcode_regions(image, page(image), [frame], ocr=lambda _: ([token], None), detectors={}) == ([], [])


def test_label_outside_frame_or_in_a_giant_answer_column_does_not_create_code_hint():
    image = np.full((300, 400, 3), 255, np.uint8)
    frames = [{"bbox": {"x": .1, "y": .1, "width": .4, "height": .2}},
              {"bbox": {"x": .1, "y": .1, "width": .4, "height": .8}}]
    label = {"text": "Barcode", "x": .2, "y": .4, "width": .1, "height": .03}
    assert discover_barcode_regions(image, page(image), frames, ocr=lambda _: ([label], None), detectors={}) == ([], [])


def test_duplicate_localizers_and_placeholder_frame_produce_only_one_hint():
    image = np.full((300, 400, 3), 255, np.uint8)
    detector = Detector(quad())
    frame = {"bbox": {"x": .23, "y": .2, "width": .45, "height": .2}}
    label = {"text": "Barcode", "x": .3, "y": .21, "width": .1, "height": .025}
    hints, _ = discover_barcode_regions(image, page(image), [frame], ocr=lambda _: ([label], None),
                                        detectors={"条码": detector, "二维码": Detector(quad())})
    assert len(hints) == 1


def test_discovery_limit_is_bounded_and_preserves_spatially_separate_codes():
    image = np.full((800, 800, 3), 255, np.uint8)
    points = np.concatenate([quad(x=10 + index * 140, y=50, width=80, height=30) for index in range(5)])
    hints, reasons = discover_barcode_regions(image, page(image), detectors={"条码": Detector(points)})
    assert len(hints) == 4
    assert "LOCAL_BARCODE_DISCOVERY_LIMIT_REACHED" in reasons


def test_missing_api_and_detector_error_return_only_fixed_diagnostics(monkeypatch):
    image = np.full((300, 400, 3), 255, np.uint8)
    monkeypatch.delattr(cv2, "barcode_BarcodeDetector")
    monkeypatch.delattr(cv2, "QRCodeDetector")
    assert discover_barcode_regions(image, page(image)) == ([], ["LOCAL_BARCODE_DETECTOR_UNAVAILABLE"])
    hints, reasons = discover_barcode_regions(image, page(image), detectors={"条码": Detector(error=ValueError("PRIVATE"))})
    assert hints == [] and reasons == ["LOCAL_BARCODE_DETECTOR_FAILED"]


def test_page_budget_does_not_start_ocr_after_detector_time_exhausted():
    image = np.full((300, 400, 3), 255, np.uint8)
    ticks = iter([0., 0., 3., 3.])
    ocr = lambda _: pytest.fail("must not start another OCR after budget")
    hints, reasons = discover_barcode_regions(image, page(image), ocr=ocr, detectors={"条码": Detector(), "二维码": Detector()},
                                             clock=lambda: next(ticks))
    assert hints == [] and reasons == ["LOCAL_BARCODE_DISCOVERY_BUDGET_EXCEEDED"]


def test_shared_static_tokens_do_not_repeat_ocr_or_copy_text_into_hint():
    image = np.full((300, 400, 3), 255, np.uint8)
    calls = []
    tokens = [{"text": "PRIVATE", "x": .2, "y": .15, "width": .1, "height": .03}]
    def ocr(_):
        calls.append(True)
        return tokens, None
    evidence = LocalContentEvidence(image, page(image), ocr=ocr)
    assert evidence.static_tokens() == tokens
    assert discover_barcode_regions(image, page(image), ocr=lambda _: (evidence.static_tokens(), None), detectors={}) == ([], [])
    assert calls == [True]


def test_real_qr_pixels_can_be_localized_without_reading_encoded_content():
    encoder = cv2.QRCodeEncoder_create()
    small = encoder.encode("SYNTHETIC_NONPERSONAL_CONTRACT")
    qr = cv2.resize(small, (180, 180), interpolation=cv2.INTER_NEAREST)
    image = np.full((400, 500, 3), 255, np.uint8)
    image[100:280, 150:330] = np.repeat(qr[:, :, None], 3, axis=2)
    hints, _ = discover_barcode_regions(image, page(image))
    assert any(hint["identity_label"] == "二维码" for hint in hints)
    assert all("semantic_check" not in hint for hint in hints)


def framed_code(image, x, y, *, scale=1, dashed=True, erased_left_middle=False, missing_left=False):
    width, height = round(240 * scale), round(160 * scale)
    code = quad(x + round(40 * scale), y + round(35 * scale), round(160 * scale), round(50 * scale))
    right, bottom = x + width, y + height
    if dashed:
        dash, period = max(4, round(7 * scale)), max(7, round(12 * scale))
        for offset in range(0, width, period):
            for edge in (y, bottom):
                cv2.line(image, (x + offset, edge), (min(right, x + offset + dash), edge), (0, 0, 0), 2)
        for offset in range(0, height, period):
            for edge in (x, right):
                if edge == x and missing_left:
                    continue
                cv2.line(image, (edge, y + offset), (edge, min(bottom, y + offset + dash)), (0, 0, 0), 2)
    else:
        cv2.rectangle(image, (x, y), (right, bottom), (0, 0, 0), 2)
    if erased_left_middle:
        cv2.rectangle(image, (x - 3, y + round(height * .25)), (x + 3, y + round(height * .75)), (255, 255, 255), -1)
    return code, (x, y, right, bottom)


@pytest.mark.parametrize("x,y,scale,dashed", [(70, 60, .7, True), (500, 500, 1.2, True), (110, 300, 1, False)])
def test_code_anchor_expands_to_measured_outer_frame_at_different_locations_and_scales(x, y, scale, dashed):
    image = np.full((900, 1200, 3), 255, np.uint8)
    code, expected = framed_code(image, x, y, scale=scale, dashed=dashed)
    hints, reasons = discover_barcode_regions(image, page(image), detectors={"条码": Detector(code)})
    assert reasons == [] and len(hints) == 1
    hint = hints[0]
    assert hint["boundary_kind"] == "printed_frame"
    box = hint["bbox"]
    actual = (box["x"] * 1200, box["y"] * 900,
              (box["x"] + box["width"]) * 1200, (box["y"] + box["height"]) * 900)
    assert all(abs(left - right) <= 3 for left, right in zip(expected, actual))
    assert box["height"] > hint["anchor_bbox"]["height"] * 2


def test_frame_edge_can_have_a_middle_erased_by_pasted_image_but_requires_both_corner_fragments():
    image = np.full((600, 800, 3), 255, np.uint8)
    code, _ = framed_code(image, 170, 140, erased_left_middle=True)
    hints, _ = discover_barcode_regions(image, page(image), detectors={"条码": Detector(code)})
    assert hints[0]["boundary_kind"] == "printed_frame"
    image = np.full((600, 800, 3), 255, np.uint8)
    code, _ = framed_code(image, 170, 140, missing_left=True)
    hints, _ = discover_barcode_regions(image, page(image), detectors={"条码": Detector(code)})
    assert hints[0]["boundary_kind"] == "context_only"


def test_containing_measured_frame_has_priority_and_context_is_not_used_to_deduplicate_separate_codes():
    image = np.full((600, 1000, 3), 255, np.uint8)
    points = np.concatenate([quad(200, 120, 160, 40), quad(400, 120, 160, 40)])
    frame = {"bbox": {"x": .17, "y": .15, "width": .22, "height": .2}}
    hints, _ = discover_barcode_regions(image, page(image), [frame], detectors={"条码": Detector(points)})
    assert len(hints) == 2
    assert hints[0]["bbox"] == frame["bbox"] and hints[0]["boundary_kind"] == "printed_frame"
    assert hints[1]["boundary_kind"] == "context_only"
