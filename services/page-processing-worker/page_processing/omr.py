from __future__ import annotations

import io
import math
from dataclasses import dataclass
from typing import Any

import cv2
import numpy as np
from PIL import Image

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

    solid_gray, red_annotation = _black_ink_grayscale(image_bytes, image)
    # 考试选择题只认黑色或中性灰铅笔；两条提取路径都先排除彩色，差分路径也不能计入红色批注。
    foreground = _foreground_mask(solid_gray)
    solid_foreground = _solid_ink_mask(solid_gray)
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
        # 配准会在印刷文字和填涂框轮廓周围引入
        # 亚像素抗锯齿。测量学生新增墨迹前，只去除
        # 空白页中很窄的一圈光晕。
        kernel_size = max(1, int(profile.reference_mask_dilation_pixels) * 2 + 1)
        reference_mask = cv2.dilate(reference_foreground, np.ones((kernel_size, kernel_size), dtype=np.uint8))
        binary = cv2.bitwise_and(foreground, cv2.bitwise_not(reference_mask))
    else:
        binary = foreground

    measurements: list[dict[str, Any]] = []
    for region in option_regions:
        measurements.append(_measure_option(region, width, height, binary, foreground, reference_foreground, profile, solid_gray,
                                            solid_gray, solid_foreground, red_annotation))

    if len({item["label"] for item in measurements}) != len(measurements):
        raise OMRExtractionError("omr_option_label_duplicate")

    decision, selected, confidence = _select_marks(measurements, multiple, profile)
    quality = _set_quality(measurements, multiple, profile)
    # 整张裁图近乎全黑属于不可读输入，不能因每个框都超过填涂阈值而输出有效多选。
    if float(np.count_nonzero(solid_gray < 64)) / float(solid_gray.size) > 0.75:
        decision, selected, confidence = "ambiguous", [], 0.0
        quality["classification"] = "unreadable"
        quality["risk_flags"] = ["unreadable"]
    return {
        "decision": decision,
        "selected": selected,
        "confidence": confidence,
        "needs_human_review": decision not in {"selected", "blank"} or quality["classification"] not in {"clear", "blank"},
        "measurements": measurements,
        "profile_version": profile.version,
        "thresholds": {
            "coordinate_space": "question_crop_pixels",
            "crop_width": width,
            "crop_height": height,
            "mode": profile.mode,
            "ink_color_policy": "black-only-v1",
            "marked": profile.marked_threshold,
            "ambiguous": profile.ambiguous_threshold,
            "minimum_margin": profile.minimum_margin,
            "reference_mask_dilation_pixels": profile.reference_mask_dilation_pixels,
            "measurement_method": "solid-core-v1" if reference_foreground is None else "template-difference-v1",
            "solid_core_radius_fraction": 0.12,
            "solid_core_minimum_retention": 0.65,
            "quality": quality,
        },
        "overlay_png": _overlay(image, measurements, selected, binary if reference_foreground is not None else None),
    }


def _measure_option(
    region: dict[str, Any],
    width: int,
    height: int,
    binary: np.ndarray,
    foreground: np.ndarray,
    reference_foreground: np.ndarray | None,
    profile: OMRProfile,
    gray: np.ndarray,
    solid_gray: np.ndarray,
    solid_foreground: np.ndarray,
    red_annotation: np.ndarray,
) -> dict[str, Any]:
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
    gray_inner = gray[y : y + h, x : x + w][inset_y : h - inset_y, inset_x : w - inset_x]
    gray_ink_ratio = float(np.count_nonzero((gray_inner >= 64) & (gray_inner < 224))) / float(gray_inner.size)
    solid_inner = solid_foreground[y + inset_y : y + h - inset_y, x + inset_x : x + w - inset_x]
    # 无空白参考时，字母与方括号也有大量黑像素；按内框短边选核，比较打开后仍保留的实心墨块。
    # 保留不足 65% 表示墨迹主要是细笔画，继续按保留率衰减；小而实心的浅涂仍进入原判定带。
    radius = max(1, round(min(solid_inner.shape) * 0.12))
    solid_fill_ratio, solid_core_ratio, stroke_retention = _solid_core_evidence(solid_inner, radius)
    decision_ratio = fill_ratio
    if reference_foreground is None:
        decision_ratio = solid_fill_ratio if stroke_retention >= 0.65 else solid_core_ratio * stroke_retention
    solid_gray_inner = solid_gray[y + inset_y : y + h - inset_y, x + inset_x : x + w - inset_x]
    gray_mask = ((solid_gray_inner >= 64) & (solid_gray_inner < 224)).astype(np.uint8) * 255
    _, gray_core_ratio, _ = _solid_core_evidence(gray_mask, radius)
    red_inner = red_annotation[y + inset_y : y + h - inset_y, x + inset_x : x + w - inset_x]
    red_annotation_ratio = float(np.count_nonzero(red_inner)) / float(red_inner.size)
    reference_fill_ratio = 0.0
    if reference_foreground is not None:
        reference_inner = reference_foreground[y : y + h, x : x + w][inset_y : h - inset_y, inset_x : w - inset_x]
        reference_fill_ratio = float(np.count_nonzero(reference_inner)) / float(reference_inner.size)
    # fill_ratio 与 foreground_delta 保留旧的总墨量证据；只有 decision_ratio 用于本次判定。
    # marked 使用未取整比例；取整后的判定值用于排序和 margin，两者不能互换。
    return {
        "label": label,
        "bbox": [x, y, w, h],
        "fill_ratio": round(fill_ratio, 6),
        "source_fill_ratio": round(source_fill_ratio, 6),
        "reference_fill_ratio": round(reference_fill_ratio, 6),
        "foreground_delta": round(fill_ratio, 6),
        "decision_ratio": round(decision_ratio, 6),
        "solid_fill_ratio": round(solid_fill_ratio, 6),
        "solid_core_ratio": round(solid_core_ratio, 6),
        "stroke_retention": round(stroke_retention, 6),
        "solid_core_radius_pixels": radius,
        "marked": decision_ratio >= profile.marked_threshold,
        "state": "marked" if decision_ratio >= profile.marked_threshold else "clear" if decision_ratio < profile.ambiguous_threshold else "uncertain",
        "gray_ink_ratio": round(gray_ink_ratio, 6),
        "gray_core_ratio": round(gray_core_ratio, 6),
        "red_annotation_ratio": round(red_annotation_ratio, 6),
    }


def _select_marks(
    measurements: list[dict[str, Any]], multiple: bool, profile: OMRProfile
) -> tuple[str, list[str], float]:
    ranked = sorted(measurements, key=lambda item: item["decision_ratio"], reverse=True)
    # 排序只用于比较强度；已选标签保留原区域顺序，与结果和 overlay 使用的测量顺序一致。
    selected = [item["label"] for item in measurements if item["marked"]]
    top = float(ranked[0]["decision_ratio"])
    second = float(ranked[1]["decision_ratio"]) if len(ranked) > 1 else 0.0
    margin = top - second

    if top < profile.ambiguous_threshold:
        decision = "blank"
        selected = []
    elif not multiple and len(selected) > 1:
        decision = "multiple"
    elif (
        multiple and any(item["state"] == "uncertain" for item in measurements)
        or not multiple and len(selected) == 1 and margin < profile.minimum_margin
        or multiple and not selected
    ):
        decision = "ambiguous"
    elif selected:
        decision = "selected"
    else:
        decision = "ambiguous"

    # 该值由填涂强度与间隔计算，是规则强度指标，不是经样本校准的正确概率。
    if multiple or decision == "blank":
        # 多选比较每个选项与判定带的距离，不比较两个正常涂点的强弱；集合置信度取最弱成员。
        confidence = _set_quality(measurements, multiple, profile)["set_confidence"]
    else:
        confidence = _confidence(decision, top, second, profile)
    return decision, selected, confidence


def _set_quality(measurements: list[dict[str, Any]], multiple: bool, profile: OMRProfile) -> dict[str, Any]:
    uncertain = [item for item in measurements if item["state"] == "uncertain"]
    # 灰色实心残留与印刷笔画的抗锯齿分开测量；这只是可能擦改的复核风险，不能证明发生过擦除。
    possible_erasure = any(item["state"] != "marked" and item["gray_core_ratio"] > 0.12 for item in measurements)
    marked = [item for item in measurements if item["state"] == "marked"]
    classification = "clear" if marked else "blank"
    risks: list[str] = []
    if uncertain:
        classification, risks = "uncertain_mark", ["uncertain_mark"]
    if possible_erasure:
        classification = "possible_erasure"
        risks.append("possible_erasure")
    # 彩色批注比例仅留作查看证据，不单独触发复核；实际黑色填涂不清仍由 uncertain/erasure 判定。
    if not multiple and len(marked) > 1:
        classification = "multiple_marks"
        risks.append("multiple_marks")
    strengths = []
    for item in measurements:
        fill = item["decision_ratio"]
        if item["state"] == "marked":
            strengths.append(min(1.0, fill / max(profile.marked_threshold * 2, 0.01)))
        elif item["state"] == "clear":
            strengths.append(max(0.0, 1 - fill / max(profile.ambiguous_threshold, 0.01)))
        else:
            strengths.append(0.0)
    return {
        "version": "omr-set-quality-v1",
        "question_type": "multiple_choice" if multiple else "single_choice",
        "classification": classification,
        "option_count": len(measurements),
        "selected_count": len(marked),
        "set_confidence": round(min(strengths), 4),
        "risk_flags": risks,
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
    except (OSError, Image.DecompressionBombError) as exc:
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


def _black_ink_grayscale(data: bytes, gray: np.ndarray) -> tuple[np.ndarray, np.ndarray]:
    with Image.open(io.BytesIO(data)) as image:
        rgb = np.asarray(image.convert("RGB"), dtype=np.int16)
    # 黑色铅笔在扫描中可呈中性灰，不能只保留 RGB=0 的像素；允许少量色差以容纳压缩和轻微色偏。
    # 彩色按通道色差识别，排除红、蓝、绿等墨迹；已转成灰度的原色无法由本步骤恢复。
    maximum, minimum = rgb.max(axis=2), rgb.min(axis=2)
    colored = maximum - minimum > np.maximum(24, maximum * 0.20)
    red = (rgb[:, :, 0] > 120) & (rgb[:, :, 0] - rgb[:, :, 1] > 40) & (rgb[:, :, 0] - rgb[:, :, 2] > 40)
    neutral = gray.copy()
    neutral[colored] = 255
    return neutral, red


def _solid_ink_mask(gray: np.ndarray) -> np.ndarray:
    # 实心测量只用 Otsu；自适应阈值会在大块均匀黑色内部挖空，不能作为笔画厚度依据。
    blurred = cv2.GaussianBlur(gray, (3, 3), 0)
    _, binary = cv2.threshold(blurred, 0, 255, cv2.THRESH_BINARY_INV + cv2.THRESH_OTSU)
    return binary


def _solid_core_evidence(inner: np.ndarray, radius: int) -> tuple[float, float, float]:
    kernel = cv2.getStructuringElement(cv2.MORPH_ELLIPSE, (radius * 2 + 1, radius * 2 + 1))
    # 框外当作白色，防止贴边字母或括号通过 OpenCV 默认边界无限延伸，伪装成粗实心墨块。
    opened = cv2.morphologyEx(inner, cv2.MORPH_OPEN, kernel, borderType=cv2.BORDER_CONSTANT, borderValue=0)
    ink_count = int(np.count_nonzero(inner))
    core_count = int(np.count_nonzero(opened))
    return ink_count / inner.size, core_count / inner.size, core_count / ink_count if ink_count else 0.0


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
