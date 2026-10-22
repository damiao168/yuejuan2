"""先建立印刷定位基准，供模板理解和保存阶段复用；此处不做区域类别判断。"""

import cv2
import numpy as np
from edugrade_worker_runtime.page_anchors import (
    ANCHOR_VERSION,
    PageAnchors,
    bbox_within_anchors,
    detect_page_anchors,
)

REGISTRATION_VERSION = ANCHOR_VERSION


def prepare_registration(image, page_no):
    """定位点使用整页归一化坐标；无可靠四点时保留整页分析，不伪造内框。"""
    anchors = detect_page_anchors(image)
    if anchors is None:
        return {"version": REGISTRATION_VERSION, "status": "not_found", "registration_marks": [],
                "coordinate_space": "source_normalized", "stable_quad": []}
    result = anchors.as_dict()
    result["status"] = "detected"
    result["registration_marks"] = [
        {"id": f"registration-p{page_no}-{mark['corner']}", "label": mark["corner"],
         **mark["bbox"], "origin": "local_detector", "review_state": "suggested",
         "semantic_check": "supported", "reason_codes": ["LOCAL_PRINTED_REGISTRATION_MARK"]}
        for mark in anchors.marks
    ]
    return result


def region_within_registration(bbox, registration, image=None):
    """边界检查只在检测到可靠四点时执行；不会裁掉未知页面上本来合法的题区。"""
    if registration.get("status") != "detected":
        return True
    try:
        width = registration.get("width") or image.shape[1]
        height = registration.get("height") or image.shape[0]
        quad = tuple((float(point["x"]), float(point["y"])) for point in registration["stable_quad"])
        if len(quad) != 4:
            return False
        anchors = PageAnchors(width, height, (), (), quad, 1.0)
    except (KeyError, TypeError, ValueError, AttributeError):
        return False
    return bbox_within_anchors(bbox, anchors)


def rectify_registration(image, registration=None):
    """返回模型可用的矫正副本及双向像素矩阵，原图片和来源哈希由调用方保留。

    输出画布保持原像素大小；只将四角参考点拉成矩形，不把纸边或笔迹当作新基准。
    调用方须用 rectified_to_source 将识别结果映回原图，再保存既有整页归一化坐标。
    """
    registration = registration or prepare_registration(image, 1)
    height, width = image.shape[:2]
    identity = np.eye(3, dtype=np.float64)
    result = {"image": image, "source_to_rectified": identity.tolist(), "rectified_to_source": identity.tolist(),
              "registration": registration, "width": width, "height": height, "rectified": False}
    if registration.get("status") != "detected":
        return result
    try:
        points = np.float32([[point["x"] * width, point["y"] * height] for point in registration["anchor_points"]])
    except (KeyError, TypeError, ValueError):
        return result
    if (points.shape != (4, 2) or not np.isfinite(points).all()
            or not cv2.isContourConvex(points) or cv2.contourArea(points) < .4 * width * height):
        return result
    left, right = float(points[[0, 3], 0].mean()), float(points[[1, 2], 0].mean())
    top, bottom = float(points[[0, 1], 1].mean()), float(points[[2, 3], 1].mean())
    target = np.float32([[left, top], [right, top], [right, bottom], [left, bottom]])
    matrix = cv2.getPerspectiveTransform(points, target)
    if not np.isfinite(matrix).all() or abs(float(np.linalg.det(matrix))) < 1e-10:
        return result
    rectified = cv2.warpPerspective(image, matrix, (width, height), flags=cv2.INTER_CUBIC,
                                    borderMode=cv2.BORDER_CONSTANT, borderValue=(255, 255, 255))
    corrected_registration = dict(registration)
    corrected_registration["coordinate_space"] = "rectified_normalized"
    corrected_registration["anchor_points"] = [{"x": float(x / width), "y": float(y / height)} for x, y in target]
    corrected_registration["stable_quad"] = corrected_registration["anchor_points"]
    # 定位标记框同样映射，避免模型看矫正图却仍收到原图的角标位置。
    corrected_registration["registration_marks"] = [
        {**mark, **transform_registration_bbox(mark, matrix, width, height)}
        for mark in registration.get("registration_marks", [])
    ]
    corrected_registration["marks"] = [
        {**mark, "bbox": transform_registration_bbox(mark["bbox"], matrix, width, height)}
        for mark in registration.get("marks", [])
    ]
    result.update({"image": rectified, "source_to_rectified": matrix.tolist(),
                   "rectified_to_source": np.linalg.inv(matrix).tolist(),
                   "registration": corrected_registration, "rectified": True})
    return result


def transform_registration_bbox(bbox, matrix, width, height):
    """归一化矩形通过像素单应矩阵投影，返回四角外接框；不会把结果裁入纸面掩盖越界。"""
    x, y, w, h = (float(bbox[key]) for key in ("x", "y", "width", "height"))
    transform = np.asarray(matrix, dtype=np.float64)
    if (not np.isfinite([x, y, w, h, width, height]).all() or min(width, height, w, h) <= 0
            or transform.shape != (3, 3) or not np.isfinite(transform).all()):
        raise ValueError("registration_bbox_invalid")
    points = np.float32([[[x * width, y * height], [(x + w) * width, y * height],
                          [(x + w) * width, (y + h) * height], [x * width, (y + h) * height]]])
    denominators = np.column_stack((points[0], np.ones(4))) @ transform[2]
    if not (np.all(denominators > 1e-9) or np.all(denominators < -1e-9)):
        raise ValueError("registration_bbox_projection_invalid")
    mapped = cv2.perspectiveTransform(points, transform)[0]
    if not np.isfinite(mapped).all():
        raise ValueError("registration_bbox_projection_invalid")
    minimum, maximum = mapped.min(axis=0), mapped.max(axis=0)
    return {"x": float(minimum[0] / width), "y": float(minimum[1] / height),
            "width": float((maximum[0] - minimum[0]) / width),
            "height": float((maximum[1] - minimum[1]) / height)}
