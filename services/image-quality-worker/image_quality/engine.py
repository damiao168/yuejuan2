from __future__ import annotations

import io

import numpy as np
from PIL import Image, ImageOps, UnidentifiedImageError

from image_quality.errors import ImageQualityError
from image_quality.geometry import (
    _analysis_image,
    _apply_deskew,
    _detect_page_border,
    _detect_perspective_risk,
    _detect_skew,
    _effective_page_short_edge,
    _exif_orientation,
    _exif_rotation_degrees,
    _exif_transform_matrix,
    _should_apply_deskew,
    _source_dpi,
)
from image_quality.metrics import (
    _dimensions,
    _focus_report,
    _illumination_report,
    _metrics,
    _noise_compression_report,
    _occlusion_report,
)
from image_quality.policy import _decision, _hard_gates, _issues, _quality_score
from image_quality.report import (
    build_normalization_transform,
    build_quality_report,
)
from image_quality.schemas import QualityAnalysisResult

MAX_IMAGE_PIXELS = 50_000_000
LANDSCAPE_ROTATION_RATIO = 1.05


def analyze_and_normalize(data: bytes) -> QualityAnalysisResult:
    try:
        with Image.open(io.BytesIO(data)) as source:
            if (
                source.width <= 0
                or source.height <= 0
                or source.width * source.height > MAX_IMAGE_PIXELS
            ):
                raise ImageQualityError("image_dimensions_out_of_range")
            # 在完整像素解码前拦截超大尺寸，压缩文件大小不能代表解码后的内存占用。
            source.load()
            source_width, source_height = source.size
            source_format = source.format or "unknown"
            source_dpi = _source_dpi(source)
            exif_orientation = _exif_orientation(source)
            exif_rotation = _exif_rotation_degrees(exif_orientation)
            exif_matrix = _exif_transform_matrix(
                exif_orientation, source_width, source_height
            )
            normalized_image = ImageOps.exif_transpose(source).convert("RGB")
    except (UnidentifiedImageError, OSError, Image.DecompressionBombError) as exc:
        raise ImageQualityError("unreadable_image") from exc

    try:
        with normalized_image.convert("L") as grayscale_image:
            gray = np.asarray(grayscale_image, dtype=np.uint8)
            analysis_gray = _analysis_image(gray)
            skew_angle, skew_confidence = _detect_skew(analysis_gray)
            border_status, border_confidence, border_completeness, page_quad = (
                _detect_page_border(analysis_gray)
            )
            perspective_status, perspective_confidence, perspective_risk = (
                _detect_perspective_risk(
                    page_quad, border_confidence, analysis_gray.shape
                )
            )
            effective_short_edge = _effective_page_short_edge(
                page_quad, analysis_gray.shape, gray.shape
            )
            focus = _focus_report(gray)
            illumination = _illumination_report(gray)
            occlusion = _occlusion_report(analysis_gray)
            degradation = _noise_compression_report(gray, source_format)
            metrics = _metrics(
                gray,
                effective_short_edge=effective_short_edge,
                border_completeness=border_completeness,
                perspective_risk=perspective_risk,
                focus=focus,
                illumination=illumination,
                occlusion=occlusion,
                degradation=degradation,
            )

        # 测量基于纠偏前的图像；是否允许纠偏由角度与证据强度共同决定。
        deskew_applied = _should_apply_deskew(skew_angle, skew_confidence)
        content_rotation = skew_angle if deskew_applied else 0.0
        deskew_matrix = np.eye(3, dtype=np.float64)
        if deskew_applied:
            deskewed_image, deskew_matrix = _apply_deskew(
                normalized_image, content_rotation
            )
            normalized_image.close()
            normalized_image = deskewed_image

        pixel_width, pixel_height = normalized_image.size
        geometry = {
            "detected_skew_angle": round(skew_angle, 4),
            "skew_confidence": round(skew_confidence, 4),
            "skew_correction_applied": deskew_applied,
            "coarse_orientation": "landscape"
            if pixel_width > pixel_height * LANDSCAPE_ROTATION_RATIO
            else "portrait_or_square",
            "page_border_status": border_status,
            "page_border_confidence": round(border_confidence, 4),
            "perspective_status": perspective_status,
            "perspective_confidence": round(perspective_confidence, 4),
            "anchor_detection_status": "not_evaluated",
            "homography_rmse_px": None,
            "curvature_status": "not_evaluated",
        }
        dimensions = _dimensions(metrics, geometry)
        hard_gates = _hard_gates(metrics, geometry)
        issues = _issues(metrics, geometry, hard_gates)
        enhancements = ["limited_deskew"] if deskew_applied else []
        quality_score = _quality_score(metrics)
        decision, quality_status = _decision(
            quality_score, issues, hard_gates, enhancements
        )

        output = io.BytesIO()
        normalized_image.save(output, format="PNG")
    finally:
        normalized_image.close()

    report = build_quality_report(
        pixel_width=pixel_width,
        pixel_height=pixel_height,
        source_dpi=source_dpi,
        source_format=source_format,
        metrics=metrics,
        geometry=geometry,
        focus=focus,
        illumination=illumination,
        occlusion=occlusion,
        degradation=degradation,
        dimensions=dimensions,
        hard_gates=hard_gates,
        quality_score=quality_score,
        decision=decision,
        enhancements=enhancements,
    )
    transform = build_normalization_transform(
        source_width=source_width,
        source_height=source_height,
        pixel_width=pixel_width,
        pixel_height=pixel_height,
        exif_rotation=exif_rotation,
        content_rotation=content_rotation,
        skew_angle=skew_angle,
        deskew_applied=deskew_applied,
        deskew_matrix=deskew_matrix,
        exif_matrix=exif_matrix,
    )
    return QualityAnalysisResult(
        quality_status=quality_status,
        quality_report=report,
        quality_issues=issues,
        normalization_transform=transform,
        normalized_png=output.getvalue(),
    )
