"""Page geometry, orientation, deskew, and transform helpers."""

import cv2
import numpy as np
from PIL import Image

from image_quality.errors import ImageQualityError

MAX_ANALYSIS_DIMENSION = 1800
MAX_SKEW_ANALYSIS_DEGREES = 45.0
MIN_LINE_LENGTH_RATIO = 0.12
MIN_DESKEW_DEGREES = 0.5
MAX_DESKEW_DEGREES = 7.0
MIN_DESKEW_CONFIDENCE = 0.45
PERSPECTIVE_SUSPECTED_SCORE = 0.15


def _analysis_image(gray: np.ndarray) -> np.ndarray:
    height, width = gray.shape
    longest_side = max(height, width)
    if longest_side <= MAX_ANALYSIS_DIMENSION:
        return gray
    scale = MAX_ANALYSIS_DIMENSION / float(longest_side)
    return cv2.resize(
        gray,
        (max(1, round(width * scale)), max(1, round(height * scale))),
        interpolation=cv2.INTER_AREA,
    )


def _effective_page_short_edge(
    page_quad: np.ndarray | None,
    analysis_shape: tuple[int, int],
    source_shape: tuple[int, int],
) -> int:
    source_height, source_width = source_shape
    if page_quad is None:
        return min(source_width, source_height)
    analysis_height, analysis_width = analysis_shape
    scale_x = source_width / max(float(analysis_width), 1.0)
    scale_y = source_height / max(float(analysis_height), 1.0)
    # 边框检测在缩小分析图上完成，有效短边必须先换回原图像素。
    scaled = page_quad.astype(np.float64) * np.asarray([scale_x, scale_y])
    top_left, top_right, bottom_right, bottom_left = scaled
    page_width = (
        float(np.linalg.norm(top_right - top_left))
        + float(np.linalg.norm(bottom_right - bottom_left))
    ) / 2.0
    page_height = (
        float(np.linalg.norm(bottom_left - top_left))
        + float(np.linalg.norm(bottom_right - top_right))
    ) / 2.0
    return max(1, round(min(page_width, page_height)))



def _detect_skew(gray: np.ndarray) -> tuple[float, float]:
    """Return line tilt in raster coordinates and heuristic evidence strength.

    Positive raster angles slope downward to the right. OpenCV's affine
    rotation with the same signed angle counter-rotates that tilt.
    """
    _, width = gray.shape
    blurred = cv2.GaussianBlur(gray, (5, 5), 0)
    edges = cv2.Canny(blurred, 50, 150)
    minimum_length = max(40, int(width * MIN_LINE_LENGTH_RATIO))
    lines = cv2.HoughLinesP(
        edges,
        1,
        np.pi / 1800.0,
        threshold=max(30, int(width * 0.05)),
        minLineLength=minimum_length,
        maxLineGap=max(8, int(width * 0.02)),
    )
    if lines is None:
        return 0.0, 0.0

    angles: list[float] = []
    weights: list[float] = []
    for x1, y1, x2, y2 in lines.reshape(-1, 4):
        dx, dy = float(x2 - x1), float(y2 - y1)
        length = float(np.hypot(dx, dy))
        if length < minimum_length:
            continue
        angle = float(np.degrees(np.arctan2(dy, dx)))
        # Treat horizontal and vertical document structure as evidence for the
        # same page-axis rotation. This keeps large rotations up to 45 degrees visible
        # instead of discarding them before the quality decision.
        angle = ((angle + 45.0) % 90.0) - 45.0
        if abs(angle) > MAX_SKEW_ANALYSIS_DEGREES:
            continue
        angles.append(angle)
        # Capping prevents one long page border from deciding the result.
        weights.append(min(length, width * 0.45))

    if len(angles) < 2:
        return 0.0, min(0.12, len(angles) * 0.06)
    values = np.asarray(angles, dtype=np.float64)
    line_weights = np.asarray(weights, dtype=np.float64)
    angle = _weighted_median(values, line_weights)
    deviations = np.abs(values - angle)
    mad = _weighted_median(deviations, line_weights)
    consistency = _clamp(1.0 - mad / 3.0)
    count_support = min(1.0, len(values) / 8.0)
    length_support = min(1.0, float(line_weights.sum()) / max(width * 2.0, 1.0))
    # This is heuristic evidence strength, not a calibrated probability.
    confidence = _clamp(consistency * (0.55 * count_support + 0.45 * length_support))
    if confidence < 0.15:
        return 0.0, confidence
    return float(angle), confidence


def _should_apply_deskew(angle: float, confidence: float) -> bool:
    return (
        np.isfinite(angle)
        and np.isfinite(confidence)
        and MIN_DESKEW_DEGREES <= abs(angle) <= MAX_DESKEW_DEGREES
        and confidence >= MIN_DESKEW_CONFIDENCE
    )


def _apply_deskew(
    image: Image.Image, rotation_degrees: float
) -> tuple[Image.Image, np.ndarray]:
    width, height = image.size
    center = ((width - 1) / 2.0, (height - 1) / 2.0)
    affine = cv2.getRotationMatrix2D(center, rotation_degrees, 1.0).astype(np.float64)
    corners = np.array(
        [
            [[0.0, 0.0]],
            [[width - 1.0, 0.0]],
            [[width - 1.0, height - 1.0]],
            [[0.0, height - 1.0]],
        ],
        dtype=np.float64,
    )
    transformed = cv2.transform(corners, affine).reshape(-1, 2)
    minimum = transformed.min(axis=0)
    maximum = transformed.max(axis=0)
    # 将旋转后的最小坐标平移到画布原点，扩展画布以保留原图四角。
    affine[:, 2] -= minimum
    output_width = int(np.ceil(maximum[0] - minimum[0])) + 1
    output_height = int(np.ceil(maximum[1] - minimum[1])) + 1

    rgb = np.asarray(image, dtype=np.uint8)
    rotated = cv2.warpAffine(
        rgb,
        affine,
        (output_width, output_height),
        flags=cv2.INTER_LINEAR,
        borderMode=cv2.BORDER_CONSTANT,
        borderValue=(255, 255, 255),
    )
    matrix = np.eye(3, dtype=np.float64)
    matrix[:2, :] = affine
    return Image.fromarray(rotated), matrix


def _detect_page_border(
    gray: np.ndarray,
) -> tuple[str, float, float, np.ndarray | None]:
    height, width = gray.shape
    image_area = float(height * width)
    blurred = cv2.GaussianBlur(gray, (5, 5), 0)
    edges = cv2.Canny(blurred, 45, 140)
    closed = cv2.morphologyEx(
        edges, cv2.MORPH_CLOSE, np.ones((5, 5), np.uint8), iterations=2
    )
    contours, _ = cv2.findContours(closed, cv2.RETR_EXTERNAL, cv2.CHAIN_APPROX_SIMPLE)

    best_quad: np.ndarray | None = None
    best_score = 0.0
    for contour in contours:
        perimeter = float(cv2.arcLength(contour, True))
        if perimeter < 0.8 * (width + height):
            continue
        approximation = cv2.approxPolyDP(contour, 0.02 * perimeter, True)
        if len(approximation) != 4 or not cv2.isContourConvex(approximation):
            continue
        quad = approximation.reshape(4, 2).astype(np.float32)
        area_ratio = abs(float(cv2.contourArea(quad))) / max(image_area, 1.0)
        if area_ratio < 0.45 or area_ratio > 1.01:
            continue
        center = quad.mean(axis=0)
        center_offset = float(
            np.linalg.norm(center - np.array([width / 2.0, height / 2.0]))
        )
        center_score = _clamp(
            1.0 - center_offset / max(np.hypot(width, height) * 0.3, 1.0)
        )
        candidate_score = 0.85 * area_ratio + 0.15 * center_score
        if candidate_score > best_score:
            best_quad, best_score = quad, candidate_score

    if best_quad is not None:
        area_ratio = abs(float(cv2.contourArea(best_quad))) / max(image_area, 1.0)
        completeness = _clamp(area_ratio / 0.78)
        confidence = _clamp(0.55 + 0.45 * min(area_ratio / 0.8, 1.0))
        status = "complete" if completeness >= 0.8 else "partial"
        return status, confidence, completeness, _order_quad(best_quad)

    side_support = _border_side_support(edges)
    completeness = float(np.mean(side_support))
    detected_sides = sum(value >= 0.35 for value in side_support)
    if detected_sides:
        confidence = _clamp(0.2 + 0.15 * detected_sides + 0.2 * completeness)
        status = "complete" if completeness >= 0.8 else "partial"
        return status, confidence, completeness, None

    # A flatbed scan may contain paper to every canvas edge and therefore no
    # visible outer contour. Report moderate completeness with low confidence
    # instead of inventing a reliable border observation.
    edge_band = max(2, int(min(width, height) * 0.015))
    edge_pixels = np.concatenate(
        (
            gray[:edge_band, :].ravel(),
            gray[-edge_band:, :].ravel(),
            gray[:, :edge_band].ravel(),
            gray[:, -edge_band:].ravel(),
        )
    )
    if float(np.percentile(edge_pixels, 25)) >= 210.0:
        return "unknown", 0.25, 0.75, None
    return "unknown", 0.0, 0.0, None


def _border_side_support(edges: np.ndarray) -> list[float]:
    height, width = edges.shape
    lines = cv2.HoughLinesP(
        edges,
        1,
        np.pi / 720.0,
        threshold=max(25, int(min(width, height) * 0.04)),
        minLineLength=max(30, int(min(width, height) * 0.12)),
        maxLineGap=max(8, int(min(width, height) * 0.025)),
    )
    support = [0.0, 0.0, 0.0, 0.0]  # top, right, bottom, left
    if lines is None:
        return support
    for x1, y1, x2, y2 in lines.reshape(-1, 4):
        dx, dy = abs(float(x2 - x1)), abs(float(y2 - y1))
        if dx >= 2.5 * max(dy, 1.0):
            y = (y1 + y2) / 2.0
            coverage = _clamp(dx / max(width, 1))
            if y <= height * 0.2:
                support[0] = max(support[0], coverage)
            if y >= height * 0.8:
                support[2] = max(support[2], coverage)
        elif dy >= 2.5 * max(dx, 1.0):
            x = (x1 + x2) / 2.0
            coverage = _clamp(dy / max(height, 1))
            if x >= width * 0.8:
                support[1] = max(support[1], coverage)
            if x <= width * 0.2:
                support[3] = max(support[3], coverage)
    return support


def _detect_perspective_risk(
    quad: np.ndarray | None, border_confidence: float, shape: tuple[int, int]
) -> tuple[str, float, float]:
    if quad is None:
        return "unknown", 0.0, 0.0
    top_left, top_right, bottom_right, bottom_left = quad
    top = float(np.linalg.norm(top_right - top_left))
    bottom = float(np.linalg.norm(bottom_right - bottom_left))
    left = float(np.linalg.norm(bottom_left - top_left))
    right = float(np.linalg.norm(bottom_right - top_right))
    if min(top, bottom, left, right) <= 1.0:
        return "unknown", 0.0, 0.0

    horizontal_difference = abs(top - bottom) / max(top, bottom)
    vertical_difference = abs(left - right) / max(left, right)
    angle_deviations = []
    for previous, current, following in (
        (bottom_left, top_left, top_right),
        (top_left, top_right, bottom_right),
        (top_right, bottom_right, bottom_left),
        (bottom_right, bottom_left, top_left),
    ):
        first, second = previous - current, following - current
        cosine = abs(float(np.dot(first, second))) / max(
            float(np.linalg.norm(first) * np.linalg.norm(second)), 1.0
        )
        angle_deviations.append(_clamp(cosine))
    angle_risk = float(np.mean(angle_deviations))
    risk = _clamp(
        0.35 * horizontal_difference + 0.35 * vertical_difference + 0.30 * angle_risk
    )
    height, width = shape
    area_ratio = abs(float(cv2.contourArea(quad))) / max(float(height * width), 1.0)
    confidence = _clamp(border_confidence * min(area_ratio / 0.6, 1.0))
    status = "suspected" if risk > PERSPECTIVE_SUSPECTED_SCORE else "normal"
    return status, confidence, risk


def _detect_shadow_risk(gray: np.ndarray) -> float:
    height, width = gray.shape
    rows, columns = 8, 8
    illumination = np.empty((rows, columns), dtype=np.float32)
    for row in range(rows):
        top, bottom = row * height // rows, (row + 1) * height // rows
        for column in range(columns):
            left, right = column * width // columns, (column + 1) * width // columns
            block = gray[top:bottom, left:right]
            illumination[row, column] = (
                float(np.percentile(block, 80)) if block.size else 255.0
            )
    illumination = cv2.GaussianBlur(illumination, (3, 3), 0)
    high = float(np.percentile(illumination, 90))
    likely_page = illumination[illumination >= max(100.0, high * 0.45)]
    if likely_page.size < 4:
        likely_page = illumination.ravel()
    low = float(np.percentile(likely_page, 10))
    high = float(np.percentile(likely_page, 90))
    spread = float(high - low)
    variation = float(likely_page.std())
    return _clamp(0.65 * spread / 90.0 + 0.35 * variation / 45.0)


def _order_quad(quad: np.ndarray) -> np.ndarray:
    points = quad.astype(np.float32)
    sums = points.sum(axis=1)
    differences = np.diff(points, axis=1).ravel()
    return np.array(
        [
            points[np.argmin(sums)],
            points[np.argmin(differences)],
            points[np.argmax(sums)],
            points[np.argmax(differences)],
        ],
        dtype=np.float32,
    )


def _weighted_median(values: np.ndarray, weights: np.ndarray) -> float:
    order = np.argsort(values)
    sorted_values = values[order]
    sorted_weights = weights[order]
    midpoint = float(sorted_weights.sum()) / 2.0
    index = int(np.searchsorted(np.cumsum(sorted_weights), midpoint, side="left"))
    return float(sorted_values[min(index, len(sorted_values) - 1)])


def _exif_orientation(image: Image.Image) -> int:
    orientation = int(image.getexif().get(274, 1))
    return orientation if 1 <= orientation <= 8 else 1


def _source_dpi(image: Image.Image) -> float | None:
    raw = image.info.get("dpi")
    if not isinstance(raw, (tuple, list)) or len(raw) < 2:
        return None
    try:
        horizontal, vertical = float(raw[0]), float(raw[1])
    except (TypeError, ValueError):
        return None
    if (
        not np.isfinite(horizontal)
        or not np.isfinite(vertical)
        or min(horizontal, vertical) <= 0
    ):
        return None
    return round((horizontal + vertical) / 2.0, 2)


def _exif_rotation_degrees(orientation: int) -> int:
    return {
        3: 180,
        4: 180,
        5: 270,
        6: 90,
        7: 90,
        8: 270,
    }.get(orientation, 0)


def _exif_transform_matrix(orientation: int, width: int, height: int) -> np.ndarray:
    matrices = {
        1: [[1.0, 0.0, 0.0], [0.0, 1.0, 0.0], [0.0, 0.0, 1.0]],
        2: [[-1.0, 0.0, width - 1.0], [0.0, 1.0, 0.0], [0.0, 0.0, 1.0]],
        3: [[-1.0, 0.0, width - 1.0], [0.0, -1.0, height - 1.0], [0.0, 0.0, 1.0]],
        4: [[1.0, 0.0, 0.0], [0.0, -1.0, height - 1.0], [0.0, 0.0, 1.0]],
        5: [[0.0, 1.0, 0.0], [1.0, 0.0, 0.0], [0.0, 0.0, 1.0]],
        6: [[0.0, -1.0, height - 1.0], [1.0, 0.0, 0.0], [0.0, 0.0, 1.0]],
        7: [[0.0, -1.0, height - 1.0], [-1.0, 0.0, width - 1.0], [0.0, 0.0, 1.0]],
        8: [[0.0, 1.0, 0.0], [-1.0, 0.0, width - 1.0], [0.0, 0.0, 1.0]],
    }
    return np.asarray(matrices[orientation], dtype=np.float64)


def _matrix_list(matrix: np.ndarray) -> list[list[float]]:
    if matrix.shape != (3, 3) or not np.isfinite(matrix).all():
        raise ImageQualityError("invalid_normalization_transform")
    return [[round(float(value), 8) for value in row] for row in matrix]


def _clamp(value: float) -> float:
    return max(0.0, min(1.0, value))
