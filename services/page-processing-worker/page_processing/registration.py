from __future__ import annotations

from dataclasses import dataclass

import cv2
import numpy as np

from page_processing.decoder import DecodeError, decode_document


class RegistrationError(RuntimeError):
    pass


class TemplateRoutingError(RegistrationError):
    def __init__(self, code: str, detail: dict) -> None:
        super().__init__(code)
        self.detail = detail


@dataclass(frozen=True)
class TemplateGuardEvidence:
    passed: bool
    orientation_match: bool
    aspect_ratio_delta: float
    perceptual_hash_score: float
    layout_score: float
    score: float
    profile_version: str = "static-layout-guard-v1"


@dataclass(frozen=True)
class RegistrationEvidence:
    method: str
    confidence: float
    source_to_template: list[list[float]]
    template_to_source: list[list[float]]
    feature_count: int
    match_count: int
    inlier_count: int
    inlier_ratio: float
    reprojection_error: float
    coverage: float


@dataclass(frozen=True)
class RegistrationOutput:
    registered_png: bytes
    width: int
    height: int
    evidence: RegistrationEvidence


@dataclass(frozen=True)
class TemplateMatchCandidate:
    template_id: str
    template_content_hash: str
    data: bytes
    content_type: str
    page_index: int
    question_regions: list[dict]
    template_name: str = ""
    version_no: int = 0


@dataclass(frozen=True)
class TemplateMatchOutcome:
    decision: str
    selected_template_id: str | None
    selected_template_content_hash: str | None
    score: float
    margin: float
    candidates: list[dict]
    registration: RegistrationOutput | None


def match_template_candidates(
    source: bytes,
    source_content_type: str,
    candidates: list[TemplateMatchCandidate],
    *,
    render_dpi: int = 300,
    minimum_score: float = 0.70,
    minimum_margin: float = 0.06,
) -> TemplateMatchOutcome:
    """Rank a small candidate set with coarse structure and geometric proof."""
    ranked: list[tuple[float, TemplateMatchCandidate, RegistrationOutput, TemplateGuardEvidence]] = []
    evidence_rows: list[dict] = []
    for candidate in candidates:
        guard = inspect_template_guard(
            source,
            source_content_type,
            candidate.data,
            candidate.content_type,
            question_regions=candidate.question_regions,
            render_dpi=render_dpi,
            template_page_index=candidate.page_index,
        )
        try:
            registration = register_page(
                source,
                source_content_type,
                candidate.data,
                candidate.content_type,
                render_dpi=render_dpi,
                template_page_index=candidate.page_index,
            )
        except RegistrationError as exc:
            evidence_rows.append({
                "template_id": candidate.template_id,
                "template_content_hash": candidate.template_content_hash,
                "template_name": candidate.template_name,
                "version_no": candidate.version_no,
                "score": round(0.30 * guard.score, 6),
                "guard_score": round(guard.score, 6),
                "geometric_score": 0.0,
                "geometry_passed": False,
                "error_code": str(exc),
            })
            continue
        score = float(np.clip(0.30 * guard.score + 0.70 * registration.evidence.confidence, 0.0, 1.0))
        ranked.append((score, candidate, registration, guard))
        evidence_rows.append({
            "template_id": candidate.template_id,
            "template_content_hash": candidate.template_content_hash,
            "template_name": candidate.template_name,
            "version_no": candidate.version_no,
            "score": round(score, 6),
            "guard_score": round(guard.score, 6),
            "geometric_score": round(registration.evidence.confidence, 6),
            "geometry_passed": True,
            "inlier_ratio": round(registration.evidence.inlier_ratio, 6),
            "reprojection_error": round(registration.evidence.reprojection_error, 6),
        })
    evidence_rows.sort(key=lambda item: float(item["score"]), reverse=True)
    ranked.sort(key=lambda item: item[0], reverse=True)
    if not ranked:
        return TemplateMatchOutcome("unknown", None, None, 0.0, 0.0, evidence_rows, None)
    top_score, top_candidate, top_registration, _ = ranked[0]
    # 只有通过几何配准的候选参与间隔比较；近似版本得分接近时保留歧义。
    margin = top_score - ranked[1][0] if len(ranked) > 1 else top_score
    if top_score < minimum_score:
        decision = "unknown"
    elif len(ranked) > 1 and margin < minimum_margin:
        decision = "ambiguous"
    else:
        decision = "matched"
    return TemplateMatchOutcome(
        decision=decision,
        selected_template_id=top_candidate.template_id if decision == "matched" else None,
        selected_template_content_hash=top_candidate.template_content_hash if decision == "matched" else None,
        score=top_score,
        margin=margin,
        candidates=evidence_rows,
        registration=top_registration if decision == "matched" else None,
    )


def inspect_template_guard(
    source: bytes,
    source_content_type: str,
    template: bytes,
    template_content_type: str,
    *,
    question_regions: list[dict] | None = None,
    render_dpi: int = 300,
    template_page_index: int = 1,
) -> TemplateGuardEvidence:
    """Cheaply reject a page that clearly does not share the bound layout.

    The guard is intentionally not a replacement for registration. It ignores
    configured answer regions and compares only coarse static structure before
    the existing ORB/AKAZE geometric verification runs.
    """
    source_image = _decode_first(source, source_content_type, render_dpi)
    template_image = _decode_page(template, template_content_type, render_dpi, template_page_index)
    source_height, source_width = source_image.shape[:2]
    template_height, template_width = template_image.shape[:2]
    source_landscape = source_width > source_height
    template_landscape = template_width > template_height
    orientation_match = source_landscape == template_landscape
    source_aspect = source_width / max(source_height, 1)
    template_aspect = template_width / max(template_height, 1)
    aspect_ratio_delta = abs(source_aspect / max(template_aspect, 1e-9) - 1.0)

    size = 96
    source_gray = cv2.resize(cv2.cvtColor(source_image, cv2.COLOR_RGB2GRAY), (size, size), interpolation=cv2.INTER_AREA)
    template_gray = cv2.resize(cv2.cvtColor(template_image, cv2.COLOR_RGB2GRAY), (size, size), interpolation=cv2.INTER_AREA)
    static_mask = np.ones((size, size), dtype=bool)
    for region in question_regions or []:
        try:
            left = max(0, int((float(region["x"]) - 0.01) * size))
            top = max(0, int((float(region["y"]) - 0.01) * size))
            right = min(size, int((float(region["x"]) + float(region["width"]) + 0.01) * size))
            bottom = min(size, int((float(region["y"]) + float(region["height"]) + 0.01) * size))
        except (KeyError, TypeError, ValueError):
            continue
        if right > left and bottom > top:
            static_mask[top:bottom, left:right] = False

    source_static = source_gray.copy()
    template_static = template_gray.copy()
    source_static[~static_mask] = 255
    template_static[~static_mask] = 255
    source_hash = _perceptual_hash(source_static)
    template_hash = _perceptual_hash(template_static)
    perceptual_hash_score = 1.0 - float(np.count_nonzero(source_hash != template_hash)) / float(source_hash.size)

    source_edges = cv2.Canny(cv2.GaussianBlur(source_static, (3, 3), 0), 60, 160) > 0
    template_edges = cv2.Canny(cv2.GaussianBlur(template_static, (3, 3), 0), 60, 160) > 0
    source_edges &= static_mask
    template_edges &= static_mask
    edge_total = int(source_edges.sum() + template_edges.sum())
    layout_score = 1.0 if edge_total == 0 else float(2 * np.logical_and(source_edges, template_edges).sum() / edge_total)
    score = float(np.clip(0.7 * perceptual_hash_score + 0.3 * layout_score, 0.0, 1.0))
    passed = bool(orientation_match and aspect_ratio_delta <= 0.10 and score >= 0.68)
    return TemplateGuardEvidence(
        passed=passed,
        orientation_match=orientation_match,
        aspect_ratio_delta=aspect_ratio_delta,
        perceptual_hash_score=perceptual_hash_score,
        layout_score=layout_score,
        score=score,
    )


def _perceptual_hash(gray: np.ndarray) -> np.ndarray:
    resized = cv2.resize(gray, (32, 32), interpolation=cv2.INTER_AREA).astype(np.float32)
    low_frequency = cv2.dct(resized)[:8, :8]
    values = low_frequency.flatten()[1:]
    return values >= np.median(values)


def register_page(
    source: bytes,
    source_content_type: str,
    template: bytes,
    template_content_type: str,
    *,
    render_dpi: int = 300,
    minimum_matches: int = 12,
    template_page_index: int = 1,
) -> RegistrationOutput:
    source_image = _decode_first(source, source_content_type, render_dpi)
    template_image = _decode_page(template, template_content_type, render_dpi, template_page_index)
    height, width = template_image.shape[:2]

    if source_image.shape == template_image.shape:
        difference = float(np.mean(cv2.absdiff(source_image, template_image)))
        if difference <= 1.0:
            matrix = np.eye(3, dtype=np.float64)
            return RegistrationOutput(
                registered_png=_encode_png(source_image),
                width=width,
                height=height,
                evidence=RegistrationEvidence(
                    method="pixel_identity",
                    confidence=1.0,
                    source_to_template=matrix.tolist(),
                    template_to_source=matrix.tolist(),
                    feature_count=0,
                    match_count=0,
                    inlier_count=0,
                    inlier_ratio=1.0,
                    reprojection_error=0.0,
                    coverage=1.0,
                ),
            )

    source_gray = cv2.cvtColor(source_image, cv2.COLOR_RGB2GRAY)
    template_gray = cv2.cvtColor(template_image, cv2.COLOR_RGB2GRAY)
    result = _feature_homography(source_gray, template_gray, "orb", minimum_matches)
    if result is None:
        result = _feature_homography(source_gray, template_gray, "akaze", minimum_matches)
    if result is None:
        raise RegistrationError("registration_insufficient_matches")

    matrix, method, feature_count, match_count, mask, reprojection_error = result
    inlier_count = int(mask.sum())
    inlier_ratio = inlier_count / max(match_count, 1)
    coverage = _coverage(matrix, source_image.shape[1], source_image.shape[0], width, height)
    confidence = float(np.clip(0.55 * inlier_ratio + 0.30 * min(coverage, 1.0) + 0.15 * max(0.0, 1.0 - reprojection_error / 8.0), 0.0, 1.0))
    if inlier_count < 8 or inlier_ratio < 0.35 or reprojection_error > 8.0 or coverage < 0.55:
        raise RegistrationError("registration_low_confidence")

    registered = cv2.warpPerspective(source_image, matrix, (width, height), flags=cv2.INTER_CUBIC, borderMode=cv2.BORDER_CONSTANT, borderValue=(255, 255, 255))
    inverse = np.linalg.inv(matrix)
    return RegistrationOutput(
        registered_png=_encode_png(registered),
        width=width,
        height=height,
        evidence=RegistrationEvidence(
            method=method,
            confidence=confidence,
            source_to_template=matrix.tolist(),
            template_to_source=inverse.tolist(),
            feature_count=feature_count,
            match_count=match_count,
            inlier_count=inlier_count,
            inlier_ratio=inlier_ratio,
            reprojection_error=reprojection_error,
            coverage=coverage,
        ),
    )


def register_page_manual(
    source: bytes,
    source_content_type: str,
    template: bytes,
    template_content_type: str,
    source_points: list[dict],
    template_points: list[dict],
    *,
    render_dpi: int = 300,
    template_page_index: int = 1,
) -> RegistrationOutput:
    source_image = _decode_first(source, source_content_type, render_dpi)
    template_image = _decode_page(template, template_content_type, render_dpi, template_page_index)
    source_normalized = _validated_quad(source_points)
    template_normalized = _validated_quad(template_points)
    if np.sign(cv2.contourArea(source_normalized, oriented=True)) != np.sign(cv2.contourArea(template_normalized, oriented=True)):
        raise RegistrationError("manual_registration_mirrored")
    source_height, source_width = source_image.shape[:2]
    target_height, target_width = template_image.shape[:2]
    # 手工点输入是 [0,1] 比例，边界 1 映射到最后一个像素；矩阵本身使用像素坐标。
    source_pixels = source_normalized * np.float32([source_width - 1, source_height - 1])
    template_pixels = template_normalized * np.float32([target_width - 1, target_height - 1])
    matrix = cv2.getPerspectiveTransform(source_pixels.astype(np.float32), template_pixels.astype(np.float32))
    if not np.isfinite(matrix).all() or abs(float(np.linalg.det(matrix))) < 1e-10:
        raise RegistrationError("manual_registration_matrix_invalid")
    condition = float(np.linalg.cond(matrix))
    if not np.isfinite(condition) or condition > 1e8:
        raise RegistrationError("manual_registration_matrix_unstable")
    coverage = _coverage(matrix, source_width, source_height, target_width, target_height)
    if coverage < 0.70 or coverage > 1.15:
        raise RegistrationError("manual_registration_coverage_invalid")
    registered = cv2.warpPerspective(source_image, matrix, (target_width, target_height), flags=cv2.INTER_CUBIC, borderMode=cv2.BORDER_CONSTANT, borderValue=(255, 255, 255))
    inverse = np.linalg.inv(matrix)
    return RegistrationOutput(
        registered_png=_encode_png(registered), width=target_width, height=target_height,
        evidence=RegistrationEvidence(
            method="manual_four_point", confidence=1.0,
            source_to_template=matrix.tolist(), template_to_source=inverse.tolist(),
            feature_count=0, match_count=4, inlier_count=4, inlier_ratio=1.0,
            reprojection_error=0.0, coverage=coverage,
        ),
    )


def _validated_quad(points: list[dict]) -> np.ndarray:
    if len(points) != 4:
        raise RegistrationError("manual_registration_points_invalid")
    try:
        quad = np.float32([[float(point["x"]), float(point["y"])] for point in points])
    except (KeyError, TypeError, ValueError) as exc:
        raise RegistrationError("manual_registration_points_invalid") from exc
    if not np.isfinite(quad).all() or np.any(quad < 0) or np.any(quad > 1):
        raise RegistrationError("manual_registration_points_out_of_range")
    if len({(float(point[0]), float(point[1])) for point in quad}) != 4:
        raise RegistrationError("manual_registration_points_duplicate")
    if not cv2.isContourConvex(quad.reshape((-1, 1, 2))):
        raise RegistrationError("manual_registration_points_crossed")
    if abs(float(cv2.contourArea(quad))) < 0.05:
        raise RegistrationError("manual_registration_area_too_small")
    return quad


def crop_regions(registered_png: bytes, regions: list[dict]) -> list[dict]:
    image = _decode_png(registered_png)
    height, width = image.shape[:2]
    outputs: list[dict] = []
    for region in regions:
        try:
            x = float(region["x"])
            y = float(region["y"])
            w = float(region["width"])
            h = float(region["height"])
            raw_question_id = region["question_id"]
        except (KeyError, TypeError, ValueError) as exc:
            raise RegistrationError("invalid_template_region") from exc
        if not isinstance(raw_question_id, str):
            raise RegistrationError("invalid_template_region")
        question_id = raw_question_id
        if not np.isfinite([x, y, w, h]).all():
            raise RegistrationError("invalid_template_region")
        if not question_id.strip() or x < 0 or y < 0 or w <= 0 or h <= 0 or x + w > 1 or y + h > 1:
            raise RegistrationError("invalid_template_region")
        left, top = round(x * width), round(y * height)
        right, bottom = round((x + w) * width), round((y + h) * height)
        left, top = max(0, left), max(0, top)
        right, bottom = min(width, right), min(height, bottom)
        if right <= left or bottom <= top:
            raise RegistrationError("empty_template_region")
        outputs.append(
            {
                "question_id": question_id,
                "label": str(region.get("label") or ""),
                "normalized_bbox": {"x": x, "y": y, "width": w, "height": h},
                "pixel_bbox": {"x": left, "y": top, "width": right - left, "height": bottom - top},
                "png": _encode_png(image[top:bottom, left:right]),
            }
        )
    return outputs


def _feature_homography(source: np.ndarray, template: np.ndarray, method: str, minimum_matches: int):
    detector = cv2.ORB_create(nfeatures=5000, fastThreshold=7) if method == "orb" else cv2.AKAZE_create()
    source_points, source_descriptors = detector.detectAndCompute(source, None)
    template_points, template_descriptors = detector.detectAndCompute(template, None)
    if source_descriptors is None or template_descriptors is None:
        return None
    matcher = cv2.BFMatcher(cv2.NORM_HAMMING)
    good = []
    for pair in matcher.knnMatch(source_descriptors, template_descriptors, k=2):
        if len(pair) == 2 and pair[0].distance < 0.75 * pair[1].distance:
            good.append(pair[0])
    if len(good) < minimum_matches:
        return None
    src = np.float32([source_points[item.queryIdx].pt for item in good]).reshape(-1, 1, 2)
    dst = np.float32([template_points[item.trainIdx].pt for item in good]).reshape(-1, 1, 2)
    matrix, mask = cv2.findHomography(src, dst, cv2.RANSAC, 4.0)
    if matrix is None or mask is None or not np.isfinite(matrix).all():
        return None
    mask = mask.ravel().astype(bool)
    projected = cv2.perspectiveTransform(src, matrix)
    errors = np.linalg.norm(projected.reshape(-1, 2) - dst.reshape(-1, 2), axis=1)
    reprojection = float(errors[mask].mean()) if mask.any() else float("inf")
    return matrix, method, len(source_points), len(good), mask, reprojection


def _coverage(matrix: np.ndarray, source_width: int, source_height: int, target_width: int, target_height: int) -> float:
    corners = np.float32([[[0, 0], [source_width, 0], [source_width, source_height], [0, source_height]]])
    transformed = cv2.perspectiveTransform(corners, matrix)[0]
    # coverage 衡量变换后四角面积与模板面积之比，不是与模板边界相交后的覆盖率。
    area = abs(float(cv2.contourArea(transformed)))
    return float(np.clip(area / max(target_width * target_height, 1), 0.0, 1.25))


def _decode_first(data: bytes, content_type: str, render_dpi: int) -> np.ndarray:
    return _decode_page(data, content_type, render_dpi, 1)


def _decode_page(data: bytes, content_type: str, render_dpi: int, page_index: int) -> np.ndarray:
    if page_index < 1 or page_index > 100:
        raise RegistrationError("invalid_template_page")
    try:
        pages, _ = decode_document(data, content_type, render_dpi=render_dpi, max_pages=100, max_page_pixels=60_000_000, max_total_pixels=600_000_000)
    except DecodeError as exc:
        raise RegistrationError(str(exc)) from exc
    if page_index > len(pages):
        raise RegistrationError("template_page_missing")
    return _decode_png(pages[page_index - 1].png)


def _decode_png(data: bytes) -> np.ndarray:
    encoded = np.frombuffer(data, np.uint8)
    image = cv2.imdecode(encoded, cv2.IMREAD_COLOR)
    if image is None:
        raise RegistrationError("image_decode_failed")
    return cv2.cvtColor(image, cv2.COLOR_BGR2RGB)


def _encode_png(image: np.ndarray) -> bytes:
    bgr = cv2.cvtColor(image, cv2.COLOR_RGB2BGR)
    ok, encoded = cv2.imencode(".png", bgr, [cv2.IMWRITE_PNG_COMPRESSION, 6])
    if not ok:
        raise RegistrationError("image_encode_failed")
    return encoded.tobytes()
