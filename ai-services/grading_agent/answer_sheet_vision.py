"""来源显示页上的印刷框候选与保守边界精修。

本模块不旋转、裁切或透视校正图片，因此输入和输出共享同一显示坐标。
线框只能作为几何证据，不能证明它是作答区；用途和题号仍须语义分析及人工核对。
"""

import hashlib
import io
import math
import warnings

import cv2
import numpy as np
from PIL import Image

ALGORITHM_VERSION = "answer-sheet-printed-frames-v2"
MAX_PIXELS = 40_000_000
MAX_CANDIDATES = 256
MATCH_ALGORITHM_VERSION = "answer-sheet-geometric-match-v1"


class VisionInputError(ValueError):
    pass


def decode_display_page(raw, page):
    """调用方须先渲染 EXIF/PDF 方向；拒绝再次旋转以防坐标被应用两次。"""
    try:
        with warnings.catch_warnings():
            warnings.simplefilter("error", Image.DecompressionBombWarning)
            with Image.open(io.BytesIO(raw)) as image:
                formats = {"image/png": "PNG", "image/jpeg": "JPEG", "image/webp": "WEBP"}
                if (image.format != formats.get(page["media_type"])
                        or image.size != (page["width"], page["height"])
                        or image.width * image.height > MAX_PIXELS
                        or image.getexif().get(274, 1) != 1
                        or getattr(image, "n_frames", 1) != 1):
                    raise VisionInputError("page display metadata is invalid")
                image.load()
                rgba = image.convert("RGBA")
                background = Image.new("RGBA", rgba.size, "white")
                return np.asarray(Image.alpha_composite(background, rgba).convert("RGB")).copy()
    except (OSError, ValueError, Image.DecompressionBombError, Image.DecompressionBombWarning) as exc:
        raise VisionInputError("page display data is invalid") from exc


def detect_printed_frames(image, page_no):
    """提取闭合线框，返回包括身份栏在内的未分类候选，不使用墨迹包围框。"""
    height, width = image.shape[:2]
    gray = cv2.cvtColor(image, cv2.COLOR_RGB2GRAY)
    ink = cv2.threshold(gray, 0, 255, cv2.THRESH_BINARY_INV | cv2.THRESH_OTSU)[1]
    horizontal = cv2.morphologyEx(ink, cv2.MORPH_OPEN,
                                cv2.getStructuringElement(cv2.MORPH_RECT, (max(12, width // 30), 1)))
    vertical = cv2.morphologyEx(ink, cv2.MORPH_OPEN,
                              cv2.getStructuringElement(cv2.MORPH_RECT, (1, max(12, height // 40))))
    # 长线开运算会切断圆角，因此从原图闭合轮廓取得候选，
    # 四条边仍须由独立长线验证；不把线图的近邻连接当成原图中真实闭合的总框。
    contours, _ = cv2.findContours(ink, cv2.RETR_LIST, cv2.CHAIN_APPROX_SIMPLE)
    horizontal_lines = _line_components(horizontal)
    vertical_lines = _line_components(vertical)
    rectangles = []
    for contour in contours:
        x, y, box_width, box_height = cv2.boundingRect(contour)
        area_ratio = box_width * box_height / (width * height)
        if (box_width < width * 0.08 or box_height < height * 0.02
                or not 0.0025 <= area_ratio <= 0.90):
            continue
        rectangle = _supported_outer_rectangle(
            (x, y, x + box_width, y + box_height), horizontal_lines, vertical_lines,
            max(3, round(min(width, height) * 0.006)))
        if rectangle is not None:
            rectangles.append(rectangle)
            continue
        band = max(2, round(min(width, height) * 0.003))
        supports = [
            _horizontal_support(horizontal, x, x + box_width, y, band),
            _horizontal_support(horizontal, x, x + box_width, y + box_height - 1, band),
            _vertical_support(vertical, y, y + box_height, x, band),
            _vertical_support(vertical, y, y + box_height, x + box_width - 1, band),
        ]
        if min(supports) < 0.70:
            continue
        rectangles.append((x, y, x + box_width, y + box_height))
    # 同一笔画内外轮廓常重复；优先保留外框，避免把边缘细小笔迹裁掉。
    rectangles.sort(key=lambda rect: -((rect[2] - rect[0]) * (rect[3] - rect[1])))
    unique = []
    tolerance = max(4, round(min(width, height) * 0.005))
    for rectangle in rectangles:
        if any(max(abs(a - b) for a, b in zip(rectangle, old)) <= tolerance for old in unique):
            continue
        unique.append(rectangle)
    unique.sort(key=lambda rect: (rect[1], rect[0], rect[3], rect[2]))
    unique = _share_frame_boundaries(unique[:MAX_CANDIDATES], horizontal_lines, vertical_lines)
    candidates = []
    for rectangle in unique:
        digest = hashlib.sha256(f"{ALGORITHM_VERSION}:{page_no}:{width}:{height}:{rectangle}".encode()).hexdigest()[:20]
        candidate_id = f"frame-{page_no}-{digest}"
        left, top, right, bottom = rectangle
        candidates.append({
            "candidate_id": candidate_id,
            "bbox": {"x": left / width, "y": top / height,
                     "width": (right - left) / width, "height": (bottom - top) / height},
            "evidence_refs": [f"cv-frame:{candidate_id}"],
        })
    return candidates


def _line_components(mask):
    _, _, stats, _ = cv2.connectedComponentsWithStats(mask)
    return [tuple(map(int, row[:4])) for row in stats[1:]]


def _supported_outer_rectangle(rectangle, horizontal_lines, vertical_lines, band):
    left, top, right, bottom = rectangle
    top_line = _nearby_line(horizontal_lines, left, right, top, band)
    bottom_line = _nearby_line(horizontal_lines, left, right, bottom - 1, band)
    left_line = _nearby_line(vertical_lines, top, bottom, left, band, vertical=True)
    right_line = _nearby_line(vertical_lines, top, bottom, right - 1, band, vertical=True)
    if any(line is None for line in (top_line, bottom_line, left_line, right_line)):
        return None
    # 像素右、下边界为开区间。使用实测整条边的外缘，保留扫描轻微倾斜和笔画宽度；
    # band 只限制搜寻距离，不作为固定扩框量，也不拿文字轮廓替代框线。
    return left_line[0], top_line[0], right_line[1], bottom_line[1]


def _nearby_line(lines, start, end, edge, band, *, vertical=False):
    eligible = []
    for x, y, width, height in lines:
        along_start, along_end, across_start, across_end = (
            (y, y + height, x, x + width) if vertical else (x, x + width, y, y + height))
        coverage = max(0, min(end, along_end) - max(start, along_start)) / (end - start)
        distance = max(0, across_start - edge, edge - (across_end - 1))
        if coverage >= 0.70 and distance <= band and across_end - across_start <= 2 * band + 1:
            endpoint_distance = abs(along_start - start) + abs(along_end - end)
            eligible.append((distance, endpoint_distance, -coverage, across_start, across_end))
    if not eligible:
        return None
    # 相邻分区可能只隔几像素。距离相差不超过一个采样像素时，优先两端也贴合
    # 当前候选的长线，避免被下一栏伸出的标题线吸走。
    nearest = min(item[0] for item in eligible)
    nearby = [item for item in eligible if item[0] <= nearest + 1]
    return min(nearby, key=lambda item: (item[1], item[0], item[2]))[3:]


def _share_frame_boundaries(rectangles, horizontal_lines, vertical_lines):
    # 相邻表格单元共用同一根框线时，两个外缘框会重叠整条笔画。只有边界恰好
    # 对应同一条实测长线才共用线中心；独立题框和模型近似框的重叠规则不放宽。
    horizontal_bands = _index_line_bands(horizontal_lines)
    vertical_bands = _index_line_bands(vertical_lines, vertical=True)
    adjusted = [list(rectangle) for rectangle in rectangles]
    for first_index, first in enumerate(rectangles):
        for second_index in range(first_index + 1, len(rectangles)):
            second = rectangles[second_index]
            for before_index, before, after_index, after in (
                    (first_index, first, second_index, second),
                    (second_index, second, first_index, first)):
                if before[1] < after[1] < before[3] < after[3]:
                    spans = horizontal_bands.get((after[1], before[3]), [])
                    if _line_covers_both(spans, (before[0], before[2]), (after[0], after[2])):
                        shared = (after[1] + before[3]) / 2
                        adjusted[before_index][3] = adjusted[after_index][1] = shared
                if before[0] < after[0] < before[2] < after[2]:
                    spans = vertical_bands.get((after[0], before[2]), [])
                    if _line_covers_both(spans, (before[1], before[3]), (after[1], after[3])):
                        shared = (after[0] + before[2]) / 2
                        adjusted[before_index][2] = adjusted[after_index][0] = shared
    return [tuple(rectangle) for rectangle in adjusted]


def _index_line_bands(lines, *, vertical=False):
    result = {}
    for x, y, width, height in lines:
        span, band = ((y, y + height), (x, x + width)) if vertical else ((x, x + width), (y, y + height))
        result.setdefault(band, []).append(span)
    return result


def _line_covers_both(spans, first, second):
    return any(all(max(0, min(end, span[1]) - max(start, span[0])) / (end - start) >= .70
                   for start, end in (first, second)) for span in spans)


def snap_to_printed_frame(bbox, frames):
    """只吸附到邻近且尺寸相符的完整线框；无证据时保持原区域并要求人工核对。"""
    eligible = []
    for frame in frames:
        other = frame["bbox"]
        edge_distance = max(abs(a - b) for a, b in zip(_edges(bbox), _edges(other)))
        overlap = _iou(bbox, other)
        if edge_distance <= 0.02 and overlap >= 0.75:
            eligible.append((edge_distance, -overlap, frame))
    if not eligible:
        return dict(bbox), [], "GEOMETRY_NOT_VERIFIED"
    eligible.sort(key=lambda item: item[:2])
    if len(eligible) > 1 and abs(eligible[0][0] - eligible[1][0]) < 0.003:
        return dict(bbox), [], "GEOMETRY_FRAME_AMBIGUOUS"
    frame = eligible[0][2]
    return dict(frame["bbox"]), list(frame["evidence_refs"]), "PRINTED_FRAME_VERIFIED"


def regions_overlap(first, second, width, height):
    """允许共享框线带来的一个像素交集，较大的交集只标风险，不猜答案归属。"""
    a, b = _edges(first), _edges(second)
    return (min(a[2], b[2]) - max(a[0], b[0]) > 1 / width
            and min(a[3], b[3]) - max(a[1], b[1]) > 1 / height)


def _edges(bbox):
    return bbox["x"], bbox["y"], bbox["x"] + bbox["width"], bbox["y"] + bbox["height"]


def _iou(first, second):
    a, b = _edges(first), _edges(second)
    intersection = max(0, min(a[2], b[2]) - max(a[0], b[0])) * max(0, min(a[3], b[3]) - max(a[1], b[1]))
    union = first["width"] * first["height"] + second["width"] * second["height"] - intersection
    return intersection / union if math.isfinite(union) and union > 0 else 0


def _horizontal_support(mask, left, right, y, band):
    strip = mask[max(0, y - band):min(mask.shape[0], y + band + 1), left:right]
    return float(np.any(strip > 0, axis=0).mean()) if strip.size else 0


def _vertical_support(mask, top, bottom, x, band):
    strip = mask[top:bottom, max(0, x - band):min(mask.shape[1], x + band + 1)]
    return float(np.any(strip > 0, axis=1).mean()) if strip.size else 0


def match_template_page(source, reference):
    """单页候选匹配必须同时具有几何和静态结构证据，分数不是识别正确率。"""
    source_gray, reference_gray = _match_gray(source), _match_gray(reference)
    if (source_gray.shape == reference_gray.shape and np.array_equal(source_gray, reference_gray)
            and cv2.countNonZero(cv2.Canny(reference_gray, 60, 160)) >= 100):
        return {"passed": True, "score": 1.0, "reason_codes": ["PIXEL_IDENTITY"], "method": "pixel_identity"}
    best = {"passed": False, "score": 0.0, "reason_codes": ["GEOMETRIC_MATCH_FAILED"], "method": "none"}
    detectors = [("orb", lambda: cv2.ORB_create(nfeatures=3000, fastThreshold=12))]
    # 部分 headless 运行时没有 AKAZE；ORB 仍使用相同几何门禁，不能因可选回退缺失让可靠结果报错。
    if hasattr(cv2, "AKAZE_create"):
        detectors.append(("akaze", cv2.AKAZE_create))
    for method, create_detector in detectors:
        detector = create_detector()
        result = _feature_match(source_gray, reference_gray, detector, method)
        if result["passed"]:
            return result
        if result["score"] > best["score"]:
            best = result
    return best


def _match_gray(image):
    gray = cv2.cvtColor(image, cv2.COLOR_RGB2GRAY)
    scale = min(1.0, 1400 / max(gray.shape))
    if scale < 1:
        gray = cv2.resize(gray, (max(1, round(gray.shape[1] * scale)), max(1, round(gray.shape[0] * scale))),
                          interpolation=cv2.INTER_AREA)
    return gray


def _feature_match(source, reference, detector, method):
    failed = {"passed": False, "score": 0.0, "reason_codes": ["GEOMETRIC_MATCH_FAILED"], "method": method}
    source_points, source_desc = detector.detectAndCompute(source, None)
    target_points, target_desc = detector.detectAndCompute(reference, None)
    if source_desc is None or target_desc is None or min(len(source_points), len(target_points)) < 24:
        return failed
    pairs = cv2.BFMatcher(cv2.NORM_HAMMING).knnMatch(source_desc, target_desc, k=2)
    good = [pair[0] for pair in pairs if len(pair) == 2 and pair[0].distance < 0.72 * pair[1].distance]
    # 重复题框会反复命中同一角点；目标点去重后再计内点，不能靠重复特征刷高证据。
    good.sort(key=lambda item: item.distance)
    unique = {}
    for match in good:
        unique.setdefault(match.trainIdx, match)
    good = list(unique.values())
    if len(good) < 20:
        return failed
    src = np.float32([source_points[item.queryIdx].pt for item in good])
    dst = np.float32([target_points[item.trainIdx].pt for item in good])
    homography, mask = cv2.findHomography(src, dst, cv2.RANSAC, 3.0)
    if homography is None or mask is None or not np.isfinite(homography).all():
        return failed
    inliers = mask.ravel().astype(bool)
    count = int(inliers.sum())
    ratio = count / len(good)
    if count < 18 or ratio < 0.60:
        return failed
    projected = cv2.perspectiveTransform(src[inliers].reshape(-1, 1, 2), homography).reshape(-1, 2)
    reprojection = float(np.median(np.linalg.norm(projected - dst[inliers], axis=1)))
    source_coverage = cv2.contourArea(cv2.convexHull(src[inliers])) / (source.shape[0] * source.shape[1])
    target_coverage = cv2.contourArea(cv2.convexHull(dst[inliers])) / (reference.shape[0] * reference.shape[1])
    coverage = min(source_coverage, target_coverage)
    corners = np.float32([[0, 0], [source.shape[1], 0], [source.shape[1], source.shape[0]], [0, source.shape[0]]])
    quad = cv2.perspectiveTransform(corners.reshape(-1, 1, 2), homography).reshape(-1, 2)
    area_ratio = abs(cv2.contourArea(quad)) / (reference.shape[0] * reference.shape[1])
    if (not np.isfinite(quad).all() or not cv2.isContourConvex(quad.astype(np.float32))
            or not 0.70 <= area_ratio <= 1.35 or coverage < 0.18 or reprojection > 2.5):
        return {**failed, "reason_codes": ["GEOMETRIC_COVERAGE_OR_PROJECTION_FAILED"]}
    warped = cv2.warpPerspective(source, homography, (reference.shape[1], reference.shape[0]), borderValue=255)
    valid = cv2.warpPerspective(np.full(source.shape, 255, np.uint8), homography,
                               (reference.shape[1], reference.shape[0]), flags=cv2.INTER_NEAREST)
    if np.count_nonzero(valid) / valid.size < 0.90:
        return {**failed, "reason_codes": ["PAGE_COVERAGE_INCOMPLETE"]}
    structure = _aligned_structure_score(warped, reference, valid)
    score = float(np.clip(0.40 * structure + 0.35 * ratio + 0.25 * min(coverage / 0.5, 1), 0, 1))
    passed = structure >= 0.90 and score >= 0.86
    return {"passed": passed, "score": round(score, 6),
            "reason_codes": ["GEOMETRY_AND_STRUCTURE_VERIFIED" if passed else "STATIC_STRUCTURE_MISMATCH"],
            "method": method, "inlier_count": count, "inlier_ratio": round(ratio, 6),
            "coverage": round(coverage, 6), "reprojection_error": round(reprojection, 6),
            "structure_score": round(structure, 6)}


def _aligned_structure_score(source, reference, valid):
    source_edges = cv2.Canny(cv2.GaussianBlur(source, (3, 3), 0), 60, 160)
    target_edges = cv2.Canny(cv2.GaussianBlur(reference, (3, 3), 0), 60, 160)
    # 扫描插值允许一个像素的边缘偏差；有效覆盖门禁在调用处单独检查，不能用遮罩隐藏缺页。
    mask = cv2.erode(valid, np.ones((3, 3), np.uint8)) > 0
    source_edges[~mask] = 0
    target_edges[~mask] = 0
    source_count = np.count_nonzero(source_edges)
    target_count = np.count_nonzero(target_edges)
    if min(source_count, target_count) < 100:
        return 0.0
    source_near = cv2.dilate(source_edges, np.ones((3, 3), np.uint8)) > 0
    target_near = cv2.dilate(target_edges, np.ones((3, 3), np.uint8)) > 0
    precision = np.count_nonzero((source_edges > 0) & target_near) / source_count
    recall = np.count_nonzero((target_edges > 0) & source_near) / target_count
    return float(2 * precision * recall / max(precision + recall, 1e-9))
