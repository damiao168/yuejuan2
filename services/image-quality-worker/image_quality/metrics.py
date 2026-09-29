"""Image signal measurements used by the quality policy."""

from typing import Any

import cv2
import numpy as np

from image_quality.geometry import _analysis_image, _clamp, _detect_shadow_risk

FOCUS_TARGET_SHORT_EDGE = 1600
FOCUS_GRID_ROWS = 12
FOCUS_GRID_COLUMNS = 8
MIN_EFFECTIVE_SHORT_EDGE = 1200
PREFERRED_EFFECTIVE_SHORT_EDGE = 2000


def _metrics(
    gray: np.ndarray,
    *,
    effective_short_edge: int,
    border_completeness: float,
    perspective_risk: float,
    focus: dict[str, Any],
    illumination: dict[str, float],
    occlusion: dict[str, float | str],
    degradation: dict[str, float | str],
) -> dict[str, float | int]:
    effective_resolution_score = _effective_resolution_score(effective_short_edge)
    brightness_score = _clamp(float(gray.mean()) / 255.0)
    ink_coverage = float(focus["ink_coverage"])
    blank_probability = _clamp(1.0 - ink_coverage / 0.012)
    return {
        "sharpness_score": round(float(focus["sharpness_score"]), 4),
        "focus_laplacian_mean": round(float(focus["laplacian_mean"]), 4),
        "focus_laplacian_median": round(float(focus["laplacian_median"]), 4),
        "focus_laplacian_p20": round(float(focus["laplacian_p20"]), 4),
        "focus_tenengrad_median": round(float(focus["tenengrad_median"]), 4),
        "focus_gradient_energy_median": round(
            float(focus["gradient_energy_median"]), 4
        ),
        "blur_pattern": str(focus["blur_pattern"]),
        "bad_focus_patch_ratio": round(float(focus["bad_patch_ratio"]), 4),
        "effective_short_edge_px": effective_short_edge,
        # 按 A4 短边英寸数估算的 DPI，与文件嵌入的扫描 DPI 元数据分开报告。
        "estimated_a4_dpi": round(effective_short_edge / 8.27, 2),
        "effective_resolution_score": round(effective_resolution_score, 4),
        "brightness_score": round(brightness_score, 4),
        "exposure_quality_score": round(illumination["exposure_quality_score"], 4),
        "contrast_score": round(illumination["contrast_score"], 4),
        "low_contrast_area_ratio": round(illumination["low_contrast_area_ratio"], 4),
        "shadow_risk_score": round(illumination["shadow_risk_score"], 4),
        "glare_risk_score": round(illumination["glare_risk_score"], 4),
        "occlusion_risk_score": round(float(occlusion["risk_score"]), 4),
        "occlusion_confidence": round(float(occlusion["confidence"]), 4),
        "noise_risk_score": round(float(degradation["noise_risk_score"]), 4),
        "compression_risk_score": round(
            float(degradation["compression_risk_score"]), 4
        ),
        "blank_probability": round(blank_probability, 4),
        "perspective_risk_score": round(perspective_risk, 4),
        "border_completeness_score": round(border_completeness, 4),
    }



def _focus_report(gray: np.ndarray) -> dict[str, Any]:
    focus_gray = _focus_image(gray)
    height, width = focus_gray.shape
    rows = FOCUS_GRID_ROWS if height >= width else FOCUS_GRID_COLUMNS
    columns = FOCUS_GRID_COLUMNS if height >= width else FOCUS_GRID_ROWS
    patches: list[dict[str, Any]] = []
    all_ink = 0
    for row in range(rows):
        top, bottom = row * height // rows, (row + 1) * height // rows
        for column in range(columns):
            left, right = column * width // columns, (column + 1) * width // columns
            patch = focus_gray[top:bottom, left:right]
            if patch.size == 0:
                continue
            background = float(np.percentile(patch, 88))
            if background < 160.0:
                continue
            ink_mask = patch < min(215.0, background - 16.0)
            ink_ratio = float(ink_mask.mean())
            all_ink += int(ink_mask.sum())
            if ink_ratio < 0.0025:
                continue
            laplacian = cv2.Laplacian(patch, cv2.CV_64F, ksize=3)
            gx = cv2.Sobel(patch, cv2.CV_64F, 1, 0, ksize=3)
            gy = cv2.Sobel(patch, cv2.CV_64F, 0, 1, ksize=3)
            gradient_squared = gx * gx + gy * gy
            laplacian_variance = float(laplacian.var())
            tenengrad = float(np.sqrt(gradient_squared).mean())
            gradient_energy = float(gradient_squared.mean())
            horizontal_energy = float((gx * gx).mean())
            vertical_energy = float((gy * gy).mean())
            gradient_anisotropy = abs(horizontal_energy - vertical_energy) / max(
                horizontal_energy + vertical_energy, 1.0
            )
            focus_score = _clamp(
                0.5 * laplacian_variance / 260.0
                + 0.3 * tenengrad / 36.0
                + 0.2 * gradient_energy / 3600.0
            )
            # 局部清晰度框使用相对宽高的归一化坐标，数值不是原图像素。
            patches.append(
                {
                    "row": row,
                    "column": column,
                    "x": round(left / width, 5),
                    "y": round(top / height, 5),
                    "width": round((right - left) / width, 5),
                    "height": round((bottom - top) / height, 5),
                    "ink_ratio": round(ink_ratio, 5),
                    "laplacian_variance": round(laplacian_variance, 4),
                    "tenengrad": round(tenengrad, 4),
                    "gradient_energy": round(gradient_energy, 4),
                    "gradient_anisotropy": round(gradient_anisotropy, 4),
                    "focus_score": round(focus_score, 4),
                    "status": "bad"
                    if focus_score < 0.35
                    else "warning"
                    if focus_score < 0.55
                    else "good",
                }
            )
    scores = np.asarray(
        [float(item["focus_score"]) for item in patches], dtype=np.float64
    )
    laplacians = np.asarray(
        [float(item["laplacian_variance"]) for item in patches], dtype=np.float64
    )
    tenengrads = np.asarray(
        [float(item["tenengrad"]) for item in patches], dtype=np.float64
    )
    energies = np.asarray(
        [float(item["gradient_energy"]) for item in patches], dtype=np.float64
    )
    anisotropies = np.asarray(
        [float(item["gradient_anisotropy"]) for item in patches], dtype=np.float64
    )
    if scores.size == 0:
        scores = laplacians = tenengrads = energies = anisotropies = np.asarray(
            [0.0], dtype=np.float64
        )
    bad_ratio = float(np.mean(scores < 0.35))
    sharpness_score = _clamp(
        0.55 * float(np.percentile(scores, 20)) + 0.45 * float(np.median(scores))
    )
    median_anisotropy = float(np.median(anisotropies))
    blur_pattern = (
        "directional_suspected"
        if sharpness_score < 0.45 and median_anisotropy > 0.55
        else "defocus_or_mixed"
        if sharpness_score < 0.45
        else "not_detected"
    )
    return {
        "grid": {"rows": rows, "columns": columns},
        "ink_patch_count": len(patches),
        "ink_coverage": round(all_ink / max(height * width, 1), 6),
        "focus_mean": round(float(scores.mean()), 4),
        "focus_median": round(float(np.median(scores)), 4),
        "focus_p20": round(float(np.percentile(scores, 20)), 4),
        "focus_min": round(float(scores.min()), 4),
        "laplacian_mean": round(float(laplacians.mean()), 4),
        "laplacian_median": round(float(np.median(laplacians)), 4),
        "laplacian_p20": round(float(np.percentile(laplacians, 20)), 4),
        "tenengrad_median": round(float(np.median(tenengrads)), 4),
        "gradient_energy_median": round(float(np.median(energies)), 4),
        "gradient_anisotropy_median": round(median_anisotropy, 4),
        "blur_pattern": blur_pattern,
        "bad_patch_ratio": round(bad_ratio, 4),
        "sharpness_score": round(sharpness_score, 4),
        "map": patches,
    }


def _focus_image(gray: np.ndarray) -> np.ndarray:
    height, width = gray.shape
    short_edge = min(height, width)
    if short_edge <= FOCUS_TARGET_SHORT_EDGE:
        return gray
    scale = FOCUS_TARGET_SHORT_EDGE / float(short_edge)
    return cv2.resize(
        gray,
        (max(1, round(width * scale)), max(1, round(height * scale))),
        interpolation=cv2.INTER_AREA,
    )


def _illumination_report(gray: np.ndarray) -> dict[str, float]:
    height, width = gray.shape
    rows, columns = 12, 8
    backgrounds: list[float] = []
    local_contrasts: list[float] = []
    for row in range(rows):
        top, bottom = row * height // rows, (row + 1) * height // rows
        for column in range(columns):
            left, right = column * width // columns, (column + 1) * width // columns
            patch = gray[top:bottom, left:right]
            if not patch.size:
                continue
            background = float(np.percentile(patch, 88))
            if background < 160.0:
                continue
            backgrounds.append(background)
            ink_values = patch[patch < min(215.0, background - 20.0)]
            if ink_values.size / patch.size >= 0.0025:
                foreground = float(np.percentile(ink_values, 50))
                local_contrasts.append(_clamp((background - foreground) / 140.0))
    background_values = np.asarray(
        backgrounds or [float(gray.mean())], dtype=np.float64
    )
    contrast_values = np.asarray(local_contrasts or [0.0], dtype=np.float64)
    shadow_risk = _detect_shadow_risk(gray)
    bright_blocks = float(np.mean(background_values >= 250.0))
    illumination_spread = float(
        np.percentile(background_values, 90) - np.percentile(background_values, 10)
    )
    glare_risk = (
        0.0
        if illumination_spread > 120.0
        else _clamp(bright_blocks * max(0.0, illumination_spread - 24.0) / 70.0)
    )
    likely_page_pixels = gray[gray > 90]
    exposure_pixels = (
        likely_page_pixels
        if likely_page_pixels.size >= gray.size * 0.3
        else gray.ravel()
    )
    dark_ratio = float(np.mean(exposure_pixels < 115))
    clipped_ratio = float(np.mean(exposure_pixels > 253))
    exposure_quality = _clamp(
        1.0
        - 2.5 * dark_ratio
        - 0.35 * max(0.0, clipped_ratio - 0.88)
        - 0.6 * shadow_risk
    )
    return {
        "background_p10": round(float(np.percentile(background_values, 10)), 4),
        "background_p90": round(float(np.percentile(background_values, 90)), 4),
        "illumination_spread": round(illumination_spread, 4),
        "shadow_risk_score": round(shadow_risk, 4),
        "glare_risk_score": round(glare_risk, 4),
        "contrast_score": round(float(np.median(contrast_values)), 4),
        "low_contrast_area_ratio": round(float(np.mean(contrast_values < 0.32)), 4),
        "exposure_quality_score": round(exposure_quality, 4),
    }


def _occlusion_report(gray: np.ndarray) -> dict[str, float | str]:
    height, width = gray.shape
    mask = (gray < 55).astype(np.uint8)
    mask[: max(2, height // 80), :] = 0
    mask[-max(2, height // 80) :, :] = 0
    mask[:, : max(2, width // 80)] = 0
    mask[:, -max(2, width // 80) :] = 0
    count, _, stats, _ = cv2.connectedComponentsWithStats(mask, 8)
    candidates: list[float] = []
    image_area = float(height * width)
    for index in range(1, count):
        component_width = float(stats[index, cv2.CC_STAT_WIDTH])
        component_height = float(stats[index, cv2.CC_STAT_HEIGHT])
        area_ratio = float(stats[index, cv2.CC_STAT_AREA]) / max(image_area, 1.0)
        aspect = max(component_width, component_height) / max(
            min(component_width, component_height), 1.0
        )
        if 0.003 <= area_ratio <= 0.25 and aspect <= 6.0:
            candidates.append(area_ratio)
    largest = max(candidates, default=0.0)
    risk = _clamp(largest / 0.06)
    confidence = _clamp(len(candidates) / 3.0 + largest / 0.12)
    return {
        "status": "suspected" if risk > 0.35 else "normal",
        "risk_score": round(risk, 4),
        "confidence": round(confidence, 4),
        "candidate_count": len(candidates),
        "largest_candidate_area_ratio": round(largest, 5),
    }


def _noise_compression_report(
    gray: np.ndarray, source_format: str
) -> dict[str, float | str]:
    analysis = _analysis_image(gray)
    smooth = cv2.GaussianBlur(analysis, (3, 3), 0)
    residual = cv2.absdiff(analysis, smooth).astype(np.float32)
    gx = cv2.Sobel(analysis, cv2.CV_32F, 1, 0, ksize=3)
    gy = cv2.Sobel(analysis, cv2.CV_32F, 0, 1, ksize=3)
    flat = np.sqrt(gx * gx + gy * gy) < 18.0
    noise_sigma = (
        float(np.median(residual[flat])) if np.any(flat) else float(np.median(residual))
    )
    noise_risk = _clamp((noise_sigma - 1.5) / 10.0)
    vertical_boundary = (
        float(np.mean(np.abs(np.diff(analysis.astype(np.float32), axis=1))[:, 7::8]))
        if analysis.shape[1] > 16
        else 0.0
    )
    horizontal_boundary = (
        float(np.mean(np.abs(np.diff(analysis.astype(np.float32), axis=0))[7::8, :]))
        if analysis.shape[0] > 16
        else 0.0
    )
    ordinary_vertical = (
        float(np.mean(np.abs(np.diff(analysis.astype(np.float32), axis=1))[:, 3::8]))
        if analysis.shape[1] > 16
        else 0.0
    )
    ordinary_horizontal = (
        float(np.mean(np.abs(np.diff(analysis.astype(np.float32), axis=0))[3::8, :]))
        if analysis.shape[0] > 16
        else 0.0
    )
    boundary = (vertical_boundary + horizontal_boundary) / 2.0
    ordinary = (ordinary_vertical + ordinary_horizontal) / 2.0
    blockiness = _clamp((boundary - ordinary - 0.5) / 8.0)
    return {
        "status": "suspected" if max(noise_risk, blockiness) > 0.55 else "normal",
        "source_format": source_format.lower(),
        "estimated_noise_sigma": round(noise_sigma, 4),
        "noise_risk_score": round(noise_risk, 4),
        "jpeg_blockiness": round(blockiness, 4),
        "compression_risk_score": round(blockiness, 4),
        "moire_status": "not_evaluated",
    }


def _dimensions(
    metrics: dict[str, float | int], geometry: dict[str, Any]
) -> dict[str, dict[str, Any]]:
    illumination_score = _clamp(
        0.55 * float(metrics["exposure_quality_score"])
        + 0.45 * (1.0 - float(metrics["shadow_risk_score"]))
    )
    contrast_score = float(metrics["contrast_score"])
    scores = {
        "completeness": float(metrics["border_completeness_score"]),
        "effective_resolution": float(metrics["effective_resolution_score"]),
        "sharpness": float(metrics["sharpness_score"]),
        "geometry": _clamp(
            1.0
            - 0.65 * float(metrics["perspective_risk_score"])
            - 0.35 * min(abs(float(geometry["detected_skew_angle"])) / 15.0, 1.0)
        ),
        "illumination_contrast": _clamp(
            0.5 * illumination_score + 0.5 * contrast_score
        ),
        "occlusion_reflection": _clamp(
            1.0
            - max(
                float(metrics["occlusion_risk_score"])
                * float(metrics["occlusion_confidence"]),
                float(metrics["glare_risk_score"]),
            )
        ),
        "noise_compression": _clamp(
            1.0
            - max(
                float(metrics["noise_risk_score"]),
                float(metrics["compression_risk_score"]),
            )
        ),
    }
    return {
        key: {"score": round(value * 100.0, 1), "status": _score_status(value)}
        for key, value in scores.items()
    }



def _score_status(value: float) -> str:
    if value >= 0.78:
        return "passed"
    if value >= 0.5:
        return "review"
    return "failed"


def _effective_resolution_score(short_edge: int) -> float:
    if short_edge < MIN_EFFECTIVE_SHORT_EDGE:
        return _clamp(0.35 * short_edge / MIN_EFFECTIVE_SHORT_EDGE)
    if short_edge < 1600:
        return 0.45 + 0.25 * (short_edge - MIN_EFFECTIVE_SHORT_EDGE) / 400.0
    if short_edge < PREFERRED_EFFECTIVE_SHORT_EDGE:
        return 0.75 + 0.25 * (short_edge - 1600) / 400.0
    return 1.0
