"""在切题前核对原图映射是否完整覆盖必需作答区，不把页面面积比当局部可见率。"""

import cv2
import numpy as np


class RegionVisibilityError(ValueError):
    pass


def inspect_required_regions(matrix, source_size, template_size, regions):
    """矩阵使用来源像素到模板像素；仅容许半个像素的坐标取整误差。"""
    if not regions:
        return {"status": "not_evaluated", "region_count": 0}
    source_width, source_height = source_size
    width, height = template_size
    transform = np.asarray(matrix, dtype=np.float64)
    if (transform.shape != (3, 3) or not np.isfinite(transform).all()
            or min(source_width, source_height, width, height) <= 0):
        raise RegionVisibilityError("registration_roi_evidence_invalid")
    corners = np.float32([[0, 0], [source_width, 0], [source_width, source_height], [0, source_height]])
    denominators = np.column_stack((corners, np.ones(4))) @ transform[2]
    # 透视分母跨零表示原图内部有投影奇点，四角面积再大也不能证明作答区可见。
    if not (np.all(denominators > 1e-9) or np.all(denominators < -1e-9)):
        raise RegionVisibilityError("registration_roi_evidence_invalid")
    footprint = cv2.perspectiveTransform(corners.reshape(1, 4, 2), transform)[0]
    if not np.isfinite(footprint).all() or not cv2.isContourConvex(footprint):
        raise RegionVisibilityError("registration_roi_evidence_invalid")
    minimum_visible = 1.0
    for region in regions:
        try:
            box = [region[key] for key in ("x", "y", "width", "height")]
            if any(isinstance(value, bool) for value in box):
                raise ValueError()
            x, y, w, h = map(float, box)
        except (KeyError, TypeError, ValueError) as exc:
            raise RegionVisibilityError("invalid_template_region") from exc
        if (not np.isfinite([x, y, w, h]).all() or min(x, y) < 0
                or min(w, h) <= 0 or x + w > 1 or y + h > 1):
            raise RegionVisibilityError("invalid_template_region")
        rectangle = np.float32([[x * width, y * height], [(x + w) * width, y * height],
                                [(x + w) * width, (y + h) * height], [x * width, (y + h) * height]])
        area = float(cv2.contourArea(rectangle))
        if area <= 0:
            raise RegionVisibilityError("invalid_template_region")
        intersection, _ = cv2.intersectConvexConvex(footprint, rectangle)
        minimum_visible = min(minimum_visible, max(0.0, min(1.0, float(intersection) / area)))
        # 即使总体 coverage 很高，只要某个必需题区超出原图有效投影就停止切题。
        if any(cv2.pointPolygonTest(footprint, tuple(map(float, point)), True) < -0.5 for point in rectangle):
            raise RegionVisibilityError("registration_required_region_incomplete")
    return {"status": "passed", "region_count": len(regions),
            "minimum_visible_ratio": round(minimum_visible, 6), "coordinate_space": "template_pixels"}
