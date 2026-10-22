"""模板和学生页共用四角印刷定位点；不读取笔迹或猜测题目类别。

坐标始终相对整页，稳定范围另存为定位点围成的四边形，避免破坏既有模板坐标。
少点、形状不一致或多组同样可信时返回空值，由调用方使用原有无定位点流程。
"""

from dataclasses import dataclass
from itertools import product

import cv2
import numpy as np

ANCHOR_VERSION = "four-corner-printed-anchors-v1"
_CORNERS = ("top_left", "top_right", "bottom_right", "bottom_left")


@dataclass(frozen=True)
class PageAnchors:
    width: int
    height: int
    marks: tuple[dict, ...]
    points: tuple[tuple[float, float], ...]
    stable_quad: tuple[tuple[float, float], ...]
    confidence: float

    def as_dict(self) -> dict:
        return {"version": ANCHOR_VERSION, "coordinate_space": "source_normalized",
                "width": self.width, "height": self.height,
                "marks": list(self.marks),
                "anchor_points": [{"x": x, "y": y} for x, y in self.points],
                "stable_quad": [{"x": x, "y": y} for x, y in self.stable_quad],
                "confidence": self.confidence}


def detect_page_anchors(image: np.ndarray) -> PageAnchors | None:
    """仅接受四角各一个、形状与尺寸相符的实心三角/方形/圆形印刷标记。"""
    if (not isinstance(image, np.ndarray) or image.ndim not in (2, 3)
            or image.dtype != np.uint8 or min(image.shape[:2]) < 120):
        return None
    height, width = image.shape[:2]
    if image.ndim == 3 and image.shape[2] != 3:
        return None
    gray = image if image.ndim == 2 else cv2.cvtColor(image, cv2.COLOR_RGB2GRAY)
    # 固定暗色上限排除浅灰扫描背景；Otsu 适配同页亮度，不把深色笔迹当语义标签。
    threshold, _ = cv2.threshold(gray, 0, 255, cv2.THRESH_BINARY_INV | cv2.THRESH_OTSU)
    binary = np.uint8(gray < min(max(threshold, 60), 145)) * 255
    contours, _ = cv2.findContours(binary, cv2.RETR_EXTERNAL, cv2.CHAIN_APPROX_SIMPLE)
    choices: list[list[dict]] = [[], [], [], []]
    scale = min(width, height)
    for contour in contours:
        x, y, w, h = cv2.boundingRect(contour)
        cx, cy = (x + w / 2) / width, (y + h / 2) / height
        if min(w, h) < max(4, scale * .004) or max(w, h) > scale * .045:
            continue
        if not .55 <= w / h <= 1.8:
            continue
        corner = (0 if cx < .23 and cy < .23 else 1 if cx > .77 and cy < .23
                  else 2 if cx > .77 and cy > .77 else 3 if cx < .23 and cy > .77 else None)
        if corner is None:
            continue
        area = float(cv2.contourArea(contour))
        hull_area = float(cv2.contourArea(cv2.convexHull(contour)))
        if area < 12 or hull_area <= 0 or area / hull_area < .88:
            continue
        fill = area / (w * h)
        polygon = cv2.approxPolyDP(contour, .035 * cv2.arcLength(contour, True), True).reshape(-1, 2)
        if len(polygon) == 3 and .30 <= fill <= .68:
            kind = "triangle"
        elif len(polygon) == 4 and .62 <= fill <= 1:
            kind = "square"
        elif 5 <= len(polygon) <= 9 and .62 <= fill <= .87:
            kind = "circle"
        else:
            continue
        # 三角和方角使用朝纸外的顶点；比轮廓质心更适合透视变换后的亚像素对应。
        signs = ((-1, -1), (1, -1), (1, 1), (-1, 1))[corner]
        extreme = polygon[np.argmax(polygon @ np.asarray(signs))].astype(float)
        point = extreme if kind != "circle" else np.float64([x + (w - 1) / 2, y + (h - 1) / 2])
        choices[corner].append({"bbox": {"x": x / width, "y": y / height,
                                           "width": w / width, "height": h / height},
                                "kind": kind, "area": area, "point": point,
                                "solidity": area / hull_area})
    if any(not items for items in choices):
        return None
    # 每角有大量墨块时，不能靠截断候选碰巧选到一组；放弃自动定位更可追踪。
    if any(len(items) > 12 for items in choices):
        return None
    ranked = []
    for group in product(*choices):
        if len({item["kind"] for item in group}) != 1:
            continue
        areas = [item["area"] for item in group]
        if min(areas) / max(areas) < .52:
            continue
        points = np.float32([item["point"] for item in group])
        normalized = points / np.float32([width, height])
        if not cv2.isContourConvex(normalized) or cv2.contourArea(normalized) < .40:
            continue
        top, right, bottom, left = (np.linalg.norm(normalized[(i + 1) % 4] - normalized[i]) for i in range(4))
        if min(top, bottom) / max(top, bottom) < .70 or min(left, right) / max(left, right) < .70:
            continue
        alignment = (abs(normalized[0, 1] - normalized[1, 1]) + abs(normalized[3, 1] - normalized[2, 1])
                     + abs(normalized[0, 0] - normalized[3, 0]) + abs(normalized[1, 0] - normalized[2, 0]))
        if alignment > .30:
            continue
        score = float(.50 * min(areas) / max(areas) + .35 * max(0, 1 - alignment)
                      + .15 * min(item["solidity"] for item in group))
        ranked.append((score, group))
    ranked.sort(key=lambda row: row[0], reverse=True)
    if not ranked or ranked[0][0] < .72:
        return None
    if len(ranked) > 1 and ranked[0][0] - ranked[1][0] < .035:
        return None
    confidence, group = ranked[0]
    marks = tuple({"corner": corner, "kind": item["kind"], "bbox": item["bbox"]}
                  for corner, item in zip(_CORNERS, group, strict=True))
    points = tuple(tuple(map(float, item["point"] / [width, height])) for item in group)
    # 业务区域相对四个定位参考点稳定；再内缩一个角标宽度会误裁紧邻角标的考号横线。
    stable_quad = points
    return PageAnchors(width, height, marks, points, stable_quad, round(confidence, 6))


def bbox_within_anchors(bbox: dict, anchors: PageAnchors, *, tolerance_pixels: float = .5) -> bool:
    """四个框角均位于定位点内才算稳定；只容许像素取整误差，不按面积掩盖越界。"""
    try:
        values = [bbox[key] for key in ("x", "y", "width", "height")]
        if any(isinstance(value, bool) for value in values):
            return False
        x, y, w, h = map(float, values)
    except (KeyError, TypeError, ValueError):
        return False
    if not np.isfinite([x, y, w, h]).all() or min(x, y) < 0 or min(w, h) <= 0 or x + w > 1 or y + h > 1:
        return False
    polygon = np.float32(anchors.stable_quad) * np.float32([anchors.width, anchors.height])
    if (polygon.shape != (4, 2) or not np.isfinite(polygon).all() or min(anchors.width, anchors.height) <= 0
            or not cv2.isContourConvex(polygon) or cv2.contourArea(polygon) <= 0):
        return False
    points = ((x, y), (x + w, y), (x + w, y + h), (x, y + h))
    return all(cv2.pointPolygonTest(polygon, (px * anchors.width, py * anchors.height), True) >= -tolerance_pixels
               for px, py in points)
