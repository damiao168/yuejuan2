from __future__ import annotations

import io
import math
from dataclasses import dataclass
from typing import Any

import cv2
import numpy as np
from PIL import Image, UnidentifiedImageError

from page_processing.decoder import DecodeError, decode_document


class OMRExtractionError(ValueError):
    pass


@dataclass(frozen=True)
class OMRProfile:
    mode: str = "manual_only"
    marked_threshold: float = 0.18
    ambiguous_threshold: float = 0.10
    minimum_margin: float = 0.06
    border_fraction: float = 0.12
    version: str = "opencv-fill-v1"
    reference_mask_dilation_pixels: int = 0


def extract_marks(
    image_bytes: bytes,
    option_regions: list[dict[str, Any]],
    *,
    multiple: bool = False,
    profile: OMRProfile | None = None,
    reference_image_bytes: bytes | None = None,
    reference_content_type: str = "",
    reference_page_no: int = 1,
    reference_page_width: int = 0,
    reference_page_height: int = 0,
    reference_question_region: dict[str, Any] | None = None,
) -> dict[str, Any]:
    profile = profile or OMRProfile()
    _validate_profile(profile)
    if not isinstance(multiple, bool):
        raise OMRExtractionError("omr_multiple_flag_invalid")
    image = _decode_grayscale(image_bytes)
    height, width = image.shape
    if not option_regions:
        raise OMRExtractionError("omr_option_regions_missing")

    foreground = _foreground_mask(image)
    reference_foreground: np.ndarray | None = None
    if profile.mode == "template_difference":
        if not reference_image_bytes or not reference_question_region:
            raise OMRExtractionError("omr_reference_missing")
        reference = _decode_reference_crop(
            reference_image_bytes,
            reference_content_type,
            reference_page_no,
            reference_question_region,
            reference_page_width,
            reference_page_height,
        )
        if reference.shape != image.shape:
            reference = _align_reference_crop(reference, image.shape)
        reference_foreground = _foreground_mask(reference)
        # Registration introduces sub-pixel anti-aliasing around printed text and
        # bubble outlines. Suppress only a narrow halo from the blank page before
        # measuring ink added by the student.
        kernel_size = max(1, int(profile.reference_mask_dilation_pixels) * 2 + 1)
        reference_mask = cv2.dilate(reference_foreground, np.ones((kernel_size, kernel_size), dtype=np.uint8))
        binary = cv2.bitwise_and(foreground, cv2.bitwise_not(reference_mask))
    else:
        binary = foreground

    measurements: list[dict[str, Any]] = []
    for region in option_regions:
        label = str(region.get("label") or "").strip()
        if not label:
            raise OMRExtractionError("omr_option_label_missing")
        x, y, w, h = _pixel_region(region, width, height)
        roi = binary[y : y + h, x : x + w]
        inset_x = max(1, round(w * profile.border_fraction))
        inset_y = max(1, round(h * profile.border_fraction))
        inner = roi[inset_y : h - inset_y, inset_x : w - inset_x]
        if inner.size == 0:
            raise OMRExtractionError("omr_option_region_too_small")
        fill_ratio = float(np.count_nonzero(inner)) / float(inner.size)
        source_inner = foreground[y : y + h, x : x + w][inset_y : h - inset_y, inset_x : w - inset_x]
        source_fill_ratio = float(np.count_nonzero(source_inner)) / float(source_inner.size)
        reference_fill_ratio = 0.0
        if reference_foreground is not None:
            reference_inner = reference_foreground[y : y + h, x : x + w][inset_y : h - inset_y, inset_x : w - inset_x]
            reference_fill_ratio = float(np.count_nonzero(reference_inner)) / float(reference_inner.size)
        measurements.append({
            "label": label,
            "bbox": [x, y, w, h],
            "fill_ratio": round(fill_ratio, 6),
            "source_fill_ratio": round(source_fill_ratio, 6),
            "reference_fill_ratio": round(reference_fill_ratio, 6),
            "foreground_delta": round(fill_ratio, 6),
            "marked": fill_ratio >= profile.marked_threshold,
        })

    ranked = sorted(measurements, key=lambda item: item["fill_ratio"], reverse=True)
    selected = [item["label"] for item in measurements if item["marked"]]
    top = float(ranked[0]["fill_ratio"])
    second = float(ranked[1]["fill_ratio"]) if len(ranked) > 1 else 0.0
    margin = top - second

    if top < profile.ambiguous_threshold:
        decision = "blank"
        selected = []
    elif not multiple and len(selected) > 1:
        decision = "multiple"
    elif not multiple and len(selected) == 1 and margin < profile.minimum_margin or multiple and not selected:
        decision = "ambiguous"
    elif selected:
        decision = "selected"
    else:
        decision = "ambiguous"

    # 该值由填涂强度与间隔计算，是规则强度指标，不是经样本校准的正确概率。
    confidence = _confidence(decision, top, second, profile)
    return {
        "decision": decision,
        "selected": selected,
        "confidence": confidence,
        "needs_human_review": decision != "selected",
        "measurements": measurements,
        "profile_version": profile.version,
        "thresholds": {
            "mode": profile.mode,
            "marked": profile.marked_threshold,
            "ambiguous": profile.ambiguous_threshold,
            "minimum_margin": profile.minimum_margin,
            "reference_mask_dilation_pixels": profile.reference_mask_dilation_pixels,
        },
        "overlay_png": _overlay(image, measurements, selected, binary if reference_foreground is not None else None),
    }


def _validate_profile(profile: OMRProfile) -> None:
    thresholds = (
        profile.marked_threshold,
        profile.ambiguous_threshold,
        profile.minimum_margin,
        profile.border_fraction,
    )
    if not all(isinstance(value, (int, float)) and not isinstance(value, bool) and math.isfinite(value) for value in thresholds):
        raise OMRExtractionError("omr_profile_threshold_invalid")
    if not 0 < profile.marked_threshold <= 1:
        raise OMRExtractionError("omr_profile_threshold_invalid")
    if not 0 <= profile.ambiguous_threshold <= profile.marked_threshold:
        raise OMRExtractionError("omr_profile_threshold_invalid")
    if not 0 <= profile.minimum_margin <= 1:
        raise OMRExtractionError("omr_profile_threshold_invalid")
    if not 0 <= profile.border_fraction < 0.5:
        raise OMRExtractionError("omr_profile_threshold_invalid")
    if (
        not isinstance(profile.reference_mask_dilation_pixels, int)
        or isinstance(profile.reference_mask_dilation_pixels, bool)
        or not 0 <= profile.reference_mask_dilation_pixels <= 4
    ):
        raise OMRExtractionError("omr_reference_mask_dilation_invalid")
    if profile.mode == "manual_only" and profile.version == "opencv-fill-v1":
        return
    if profile.mode == "template_difference" and profile.version == "opencv-template-difference-bubble-v1":
        return
    raise OMRExtractionError("omr_profile_unsupported")


def _decode_grayscale(data: bytes) -> np.ndarray:
    try:
        with Image.open(io.BytesIO(data)) as image:
            if image.width < 8 or image.height < 8:
                raise OMRExtractionError("omr_image_too_small")
            if image.width * image.height > 50_000_000:
                raise OMRExtractionError("omr_image_too_large")
            image.load()
            return np.asarray(image.convert("L"), dtype=np.uint8)
    except (UnidentifiedImageError, OSError, Image.DecompressionBombError) as exc:
        raise OMRExtractionError("omr_image_decode_failed") from exc


def _decode_reference_crop(
    data: bytes,
    content_type: str,
    page_no: int,
    region: dict[str, Any],
    page_width: int = 0,
    page_height: int = 0,
) -> np.ndarray:
    if page_no < 1 or page_no > 100:
        raise OMRExtractionError("omr_reference_page_invalid")
    if (
        not isinstance(page_width, int)
        or isinstance(page_width, bool)
        or not isinstance(page_height, int)
        or isinstance(page_height, bool)
        or page_width < 0
        or page_height < 0
        or (page_width == 0) != (page_height == 0)
        or page_width * page_height > 60_000_000
    ):
        raise OMRExtractionError("omr_reference_dimensions_invalid")
    try:
        pages, _ = decode_document(
            data,
            content_type,
            render_dpi=300,
            max_pages=100,
            max_page_pixels=60_000_000,
            max_total_pixels=600_000_000,
        )
    except DecodeError as exc:
        raise OMRExtractionError("omr_reference_decode_failed") from exc
    if page_no > len(pages):
        raise OMRExtractionError("omr_reference_page_missing")
    reference = _decode_grayscale(pages[page_no - 1].png)
    if page_width > 0 and page_height > 0 and reference.shape != (page_height, page_width):
        reference = cv2.resize(reference, (page_width, page_height), interpolation=cv2.INTER_AREA)
    try:
        raw_values = [region[name] for name in ("x", "y", "width", "height")]
        if any(isinstance(value, bool) for value in raw_values):
            raise TypeError("boolean coordinates are invalid")
        x, y, width, height = (float(value) for value in raw_values)
    except (KeyError, TypeError, ValueError) as exc:
        raise OMRExtractionError("omr_reference_region_invalid") from exc
    if not all(math.isfinite(value) for value in (x, y, width, height)):
        raise OMRExtractionError("omr_reference_region_invalid")
    if x < 0 or y < 0 or width <= 0 or height <= 0 or x + width > 1 or y + height > 1:
        raise OMRExtractionError("omr_reference_region_invalid")
    page_height, page_width = reference.shape
    left, top = round(x * page_width), round(y * page_height)
    right, bottom = round((x + width) * page_width), round((y + height) * page_height)
    left, top = max(0, left), max(0, top)
    right, bottom = min(page_width, right), min(page_height, bottom)
    if right <= left or bottom <= top:
        raise OMRExtractionError("omr_reference_region_empty")
    return reference[top:bottom, left:right]


def _foreground_mask(gray: np.ndarray) -> np.ndarray:
    blurred = cv2.GaussianBlur(gray, (3, 3), 0)
    _, otsu = cv2.threshold(blurred, 0, 255, cv2.THRESH_BINARY_INV + cv2.THRESH_OTSU)
    adaptive = cv2.adaptiveThreshold(
        blurred, 255, cv2.ADAPTIVE_THRESH_GAUSSIAN_C,
        cv2.THRESH_BINARY_INV, 21, 7,
    )
    combined = cv2.bitwise_and(otsu, adaptive)
    kernel = np.ones((2, 2), dtype=np.uint8)
    return cv2.morphologyEx(combined, cv2.MORPH_OPEN, kernel)


def _align_reference_crop(reference: np.ndarray, target_shape: tuple[int, int]) -> np.ndarray:
    target_height, target_width = target_shape
    reference_height, reference_width = reference.shape
    interpolation = cv2.INTER_AREA if reference_width > target_width or reference_height > target_height else cv2.INTER_CUBIC
    return cv2.resize(reference, (target_width, target_height), interpolation=interpolation)


def _pixel_region(region: dict[str, Any], width: int, height: int) -> tuple[int, int, int, int]:
    try:
        raw_values = [region[name] for name in ("x", "y", "width", "height")]
        if any(isinstance(value, bool) for value in raw_values):
            raise TypeError("boolean coordinates are invalid")
        values = [float(value) for value in raw_values]
    except (KeyError, TypeError, ValueError) as exc:
        raise OMRExtractionError("omr_option_region_invalid") from exc
    x, y, w, h = values
    if not all(math.isfinite(value) for value in values):
        raise OMRExtractionError("omr_option_region_invalid")
    # 全部坐标不大于 1 时按当前题目裁剪的比例解释，否则按该裁剪内的像素解释。
    normalized = max(values) <= 1.0
    if normalized:
        x, w = x * width, w * width
        y, h = y * height, h * height
    px = round(x)
    py = round(y)
    pw = round(w)
    ph = round(h)
    if px < 0 or py < 0 or pw < 6 or ph < 6 or px + pw > width or py + ph > height:
        raise OMRExtractionError("omr_option_region_out_of_bounds")
    return px, py, pw, ph


def _confidence(decision: str, top: float, second: float, profile: OMRProfile) -> float:
    if decision != "selected":
        return round(min(0.79, max(0.0, top)), 4)
    fill_strength = min(1.0, top / max(profile.marked_threshold * 2.0, 0.01))
    margin_strength = min(1.0, max(0.0, top - second) / max(profile.minimum_margin * 2.0, 0.01))
    return round(0.5 * fill_strength + 0.5 * margin_strength, 4)


def _overlay(gray: np.ndarray, measurements: list[dict[str, Any]], selected: list[str], foreground_delta: np.ndarray | None = None) -> bytes:
    canvas = cv2.cvtColor(gray, cv2.COLOR_GRAY2BGR)
    if foreground_delta is not None:
        mask = foreground_delta > 0
        canvas[mask] = (30, 30, 210)
    selected_set = set(selected)
    for item in measurements:
        x, y, w, h = item["bbox"]
        color = (40, 150, 40) if item["label"] in selected_set else (30, 120, 220)
        cv2.rectangle(canvas, (x, y), (x + w, y + h), color, 2)
        cv2.putText(canvas, item["label"], (x, max(12, y - 4)), cv2.FONT_HERSHEY_SIMPLEX, 0.45, color, 1, cv2.LINE_AA)
    ok, encoded = cv2.imencode(".png", canvas)
    if not ok:
        raise OMRExtractionError("omr_overlay_encode_failed")
    return encoded.tobytes()
