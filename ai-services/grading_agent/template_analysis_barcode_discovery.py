"""用专用码定位器或真实贴码标签补充局部多模态复判输入。

结果只是几何提示，不是已确认身份区。无条码是正常空结果；不按固定页位补框，
不解码个人标识，也不把普通竖纹或警示牌当作条码。
"""

import hashlib
import math
import re
import time
from itertools import islice

import cv2
import numpy as np

BARCODE_DISCOVERY_VERSION = "page-code-outer-boundary-v2"
_MAX_LONG_EDGE = 1600
_MAX_HINTS = 4
_MAX_OUTER_SEARCHES = 8
_MAX_FRAMES = 256
_MAX_TOKENS = 20000
_PAGE_SECONDS = 2.0
_PLACEHOLDER_LABELS = {
    "条码": "条码", "条形码": "条码", "条形码粘贴区": "条码", "条码粘贴区": "条码",
    "条形码区": "条码", "贴码区": "条码", "barcode": "条码", "barcodearea": "条码",
    "二维码": "二维码", "二维码粘贴区": "二维码", "qrcode": "二维码", "qrcodearea": "二维码",
}


def discover_barcode_regions(image, page, frames=(), *, ocr=None, detectors=None, clock=None):
    """返回至多四个来源页比例框及固定诊断码，交给调用方裁图复判。

    ``ocr`` 可复用已完成的静态 OCR，返回 ``(tokens, reason)``；缺省不重复运行 OCR。
    ``bbox`` 是实测外框范围（一边中段被遮挡时仍须有两端证据），否则是裁图上下文。
    ``boundary_kind`` 区分两者，任何结果仍须经过局部模型复判。
    ``anchor_bbox`` 只说明定位器发现的码图，不可直接用作最终贴码区域。
    ``frames`` 只能传同张图经印刷框检测得到的边界，不能用待分析的普通候选替代实测框。
    ``detectors`` 用于注入专用定位器，按 ``条码/二维码`` 分类；只调用 detectMulti，
    不读取解码内容。两秒预算在原生调用之间检查，不能强行中断正在执行的 OpenCV。
    """
    if not isinstance(image, np.ndarray) or image.ndim != 3 or image.shape[2] != 3 or image.size == 0:
        return [], ["LOCAL_BARCODE_DISCOVERY_INPUT_INVALID"]
    if (image.dtype != np.uint8 or page.get("width") != image.shape[1]
            or page.get("height") != image.shape[0] or not isinstance(page.get("page_no"), int)
            or isinstance(page["page_no"], bool) or page["page_no"] < 1
            or not isinstance(page.get("sha256"), str) or not re.fullmatch(r"[a-f0-9]{64}", page["sha256"])):
        return [], ["LOCAL_BARCODE_DISCOVERY_INPUT_INVALID"]
    now = clock or time.monotonic
    deadline = now() + _PAGE_SECONDS
    scale = min(1.0, _MAX_LONG_EDGE / max(image.shape[:2]))
    measured = image if scale == 1 else cv2.resize(image, None, fx=scale, fy=scale, interpolation=cv2.INTER_AREA)
    bgr = cv2.cvtColor(measured, cv2.COLOR_RGB2BGR)
    reasons, found, outer_searches = [], [], 0
    frames = list(islice(frames, _MAX_FRAMES))
    if detectors is None:
        detectors, setup_reasons = _detectors(measured.shape)
        reasons.extend(setup_reasons)
    for label in ("条码", "二维码"):
        detector = detectors.get(label)
        if detector is None:
            continue
        if now() >= deadline:
            reasons.append("LOCAL_BARCODE_DISCOVERY_BUDGET_EXCEEDED")
            break
        try:
            present, points = detector.detectMulti(bgr)
            if not present or points is None:
                continue
            points = np.asarray(points, dtype=np.float64)
            if points.size % 8 or points.size > 256 * 8:
                reasons.append("LOCAL_BARCODE_DETECTOR_OUTPUT_INVALID")
                continue
            for quad in points.reshape(-1, 4, 2):
                box = _quad_bbox(quad, measured.shape)
                if box is not None:
                    outer = _containing_printed_frame(box, frames)
                    if outer is None and outer_searches < _MAX_OUTER_SEARCHES and now() < deadline:
                        outer_searches += 1
                        outer = _local_outer_frame(measured, box)
                    found.append((label, outer or _context_box(box), "LOCAL_BARCODE_DETECTOR_LOCALIZED",
                                  box, "printed_frame" if outer is not None else "context_only"))
        except (cv2.error, ValueError, TypeError, AttributeError):
            # 不输出底层异常、码内容或 OCR 原文；定位失败不能使本页其他区域分析失败。
            reasons.append("LOCAL_BARCODE_DETECTOR_FAILED")
    if ocr is not None and now() < deadline:
        try:
            tokens, reason = ocr(image)
            if reason:
                reasons.append("LOCAL_BARCODE_STATIC_OCR_UNAVAILABLE")
            if isinstance(tokens, list):
                found.extend((label, box, reason, None, "printed_frame")
                             for label, box, reason in _labelled_frames(tokens[:_MAX_TOKENS], frames))
        except (OSError, ValueError, TypeError, cv2.error):
            reasons.append("LOCAL_BARCODE_STATIC_OCR_FAILED")
    elif ocr is not None:
        reasons.append("LOCAL_BARCODE_DISCOVERY_BUDGET_EXCEEDED")
    hints = []
    for label, box, reason, anchor, boundary_kind in found:
        if any(_overlap(anchor or box, row["anchor_bbox"] or row["bbox"]) >= .65 for row in hints):
            continue
        if len(hints) >= _MAX_HINTS:
            reasons.append("LOCAL_BARCODE_DISCOVERY_LIMIT_REACHED")
            break
        fingerprint = f"{BARCODE_DISCOVERY_VERSION}:{page['sha256']}:{page['page_no']}:{label}:{box}"
        identifier = "barcode-discovery-" + hashlib.sha256(fingerprint.encode()).hexdigest()[:24]
        boundary_reason = ("LOCAL_BARCODE_OUTER_FRAME_LOCALIZED" if boundary_kind == "printed_frame"
                           else "LOCAL_BARCODE_OUTER_FRAME_REQUIRES_MODEL")
        hints.append({"candidate_id": identifier, "bbox": box, "anchor_bbox": anchor,
                      "boundary_kind": boundary_kind, "identity_label": label,
                      "reason_codes": [reason, boundary_reason], "evidence_refs": [
                          f"local-code:{BARCODE_DISCOVERY_VERSION}:{page['sha256'][:16]}:{page['page_no']}"]})
    return hints, list(dict.fromkeys(reasons))


def _detectors(shape):
    result, reasons = {}, []
    for label, name in (("条码", "barcode_BarcodeDetector"), ("二维码", "QRCodeDetector")):
        factory = getattr(cv2, name, None)
        if factory is None:
            reasons.append("LOCAL_BARCODE_DETECTOR_UNAVAILABLE")
            continue
        try:
            detector = factory()
            if label == "条码":
                # 默认把短边降到 512，会抹掉整页里的小条码。关闭第二次缩小，并按较细条纹调滤波尺度。
                # 这是专用码定位器的尺度参数，仍不能把其结果直接确认为可用身份框。
                detector.setDownsamplingThreshold(float(max(shape[:2])))
                detector.setDetectorScales([.004, .01, .02, .03])
            result[label] = detector
        except (cv2.error, TypeError, AttributeError):
            reasons.append("LOCAL_BARCODE_DETECTOR_UNAVAILABLE")
    return result, reasons


def _quad_bbox(points, shape):
    height, width = shape[:2]
    if (not np.isfinite(points).all() or np.any(points < 0)
            or np.any(points[:, 0] > width) or np.any(points[:, 1] > height)):
        return None
    contour = points.astype(np.float32)
    if not cv2.isContourConvex(contour) or abs(cv2.contourArea(contour)) < 64:
        return None
    left, top = points.min(axis=0)
    right, bottom = points.max(axis=0)
    box_width, box_height = right - left, bottom - top
    if (min(box_width, box_height) < 8 or box_width * box_height > width * height * .15
            or max(box_width, box_height) / min(box_width, box_height) > 25):
        return None
    # 四角只定位码图并保留少量静区；外贴码边界要在下一步独立找四条边。
    margin = max(2, min(box_width, box_height) * .08)
    return {"x": max(0, left - margin) / width, "y": max(0, top - margin) / height,
            "width": (min(width, right + margin) - max(0, left - margin)) / width,
            "height": (min(height, bottom + margin) - max(0, top - margin)) / height}


def _context_box(anchor):
    # 无四边证据时扩大局部图像供模型重新找边界；不能把这块上下文确认为贴码区域。
    # 按码图宽高决定范围，兼容横码和方码，不依赖页眉位置或学校模板。
    extent = max(anchor["width"], anchor["height"])
    left = max(0, anchor["x"] - extent * .65)
    top = max(0, anchor["y"] - extent)
    right = min(1, anchor["x"] + anchor["width"] + extent * .65)
    bottom = min(1, anchor["y"] + anchor["height"] + extent)
    return {"x": left, "y": top, "width": right - left, "height": bottom - top}


def _eligible_frame(box, anchor):
    if box is None or box["width"] * box["height"] > .12 or box["width"] > .65 or box["height"] > .35:
        return False
    # 要包含码图静区且确实大于码图；近贴条纹的矩形不能冒充完整贴码外框。
    return (box["x"] <= anchor["x"] and box["y"] <= anchor["y"]
            and box["x"] + box["width"] >= anchor["x"] + anchor["width"]
            and box["y"] + box["height"] >= anchor["y"] + anchor["height"]
            and box["width"] * box["height"] >= anchor["width"] * anchor["height"] * 1.35
            and box["width"] * box["height"] <= anchor["width"] * anchor["height"] * 16)


def _containing_printed_frame(anchor, frames):
    eligible = [_box(frame.get("bbox")) for frame in frames if isinstance(frame, dict)]
    eligible = [box for box in eligible if _eligible_frame(box, anchor)]
    return dict(min(eligible, key=lambda box: box["width"] * box["height"])) if eligible else None


def _local_outer_frame(image, anchor):
    """只连接短虚线间隙，并由原始像素的四边覆盖验证外框。"""
    height, width = image.shape[:2]
    context = _context_box(anchor)
    x = max(0, int(context["x"] * width))
    y = max(0, int(context["y"] * height))
    right = min(width, math.ceil((context["x"] + context["width"]) * width))
    bottom = min(height, math.ceil((context["y"] + context["height"]) * height))
    gray = cv2.cvtColor(image[y:bottom, x:right], cv2.COLOR_RGB2GRAY)
    ink = cv2.threshold(gray, 0, 255, cv2.THRESH_BINARY_INV | cv2.THRESH_OTSU)[1]
    anchor_width, anchor_height = anchor["width"] * width, anchor["height"] * height
    gap = max(3, min(18, round(max(anchor_width, anchor_height) * .065)))
    horizontal = cv2.morphologyEx(ink, cv2.MORPH_CLOSE, np.ones((1, gap), np.uint8))
    horizontal = cv2.morphologyEx(horizontal, cv2.MORPH_OPEN,
                                 np.ones((1, max(16, round(anchor_width * .65))), np.uint8))
    vertical = cv2.morphologyEx(ink, cv2.MORPH_CLOSE, np.ones((gap, 1), np.uint8))
    vertical = cv2.morphologyEx(vertical, cv2.MORPH_OPEN,
                               np.ones((max(16, round(anchor_height * .65)), 1), np.uint8))
    outlines = cv2.bitwise_or(horizontal, vertical)
    contours, _ = cv2.findContours(outlines, cv2.RETR_LIST, cv2.CHAIN_APPROX_SIMPLE)
    rectangles = [cv2.boundingRect(contour) for contour in contours]
    # 贴码图像可能抹去一条边的中段，也可能与下方说明线相连。上下边端点一致时，
    # 单独组合原始长线，再核对四边；不能依赖连通轮廓的外包框吞下邻近说明区。
    _, _, stats, _ = cv2.connectedComponentsWithStats(horizontal)
    lines = [tuple(map(int, row[:4])) for row in stats[1:]
             if row[3] <= 5 and row[0] + x <= anchor["x"] * width
             and row[0] + row[2] + x >= (anchor["x"] + anchor["width"]) * width]
    tolerance = max(4, round(anchor_width * .04))
    for first in lines:
        for second in lines:
            left, top, line_width, _line_height = first
            other_left, lower, other_width, other_height = second
            if (lower <= top or abs(left - other_left) > tolerance
                    or abs(left + line_width - other_left - other_width) > tolerance):
                continue
            rectangles.append((min(left, other_left), top,
                               max(left + line_width, other_left + other_width) - min(left, other_left),
                               lower + other_height - top))
    eligible = []
    for left, top, box_width, box_height in rectangles:
        box = {"x": (x + left) / width, "y": (y + top) / height,
               "width": box_width / width, "height": box_height / height}
        if not _eligible_frame(box, anchor):
            continue
        # 闭运算可能把文字或条纹接成形；每边仍须在原始墨迹上连续分布，不能只靠生成轮廓。
        if _four_sides_supported(ink, left, top, box_width, box_height):
            eligible.append(box)
    return min(eligible, key=lambda box: box["width"] * box["height"]) if eligible else None


def _four_sides_supported(ink, x, y, width, height):
    band = 2
    rows, columns = ink.shape
    sides = [np.any(ink[max(0, y - band):min(rows, y + band + 1), x:x + width], axis=0),
             np.any(ink[max(0, y + height - 1 - band):min(rows, y + height + band), x:x + width], axis=0),
             np.any(ink[y:y + height, max(0, x - band):min(columns, x + band + 1)], axis=1),
             np.any(ink[y:y + height, max(0, x + width - 1 - band):min(columns, x + width + band)], axis=1)]
    strong, occluded = 0, 0
    for side in sides:
        if not side.size:
            return False
        parts = np.array_split(side, 5)
        # 虚线的实墨比例低于实线；覆盖率与分段分布同时检查，不能只按总墨量判边。
        if np.mean(side) >= .45 and all(np.any(part) for part in parts):
            strong += 1
        elif np.mean(side) >= .18 and np.any(parts[0]) and np.any(parts[-1]):
            occluded += 1
        else:
            return False
    # 仅容许一边的中段被贴图覆盖，且该边两端均有原始墨迹；三边形或任意文字不能补造第四边。
    return strong == 4 or strong == 3 and occluded == 1


def _box(value):
    if not isinstance(value, dict):
        return None
    values = [value.get(key) for key in ("x", "y", "width", "height")]
    if any(type(number) not in {int, float} or not math.isfinite(number) for number in values):
        return None
    x, y, width, height = values
    return value if x >= 0 and y >= 0 and width > 0 and height > 0 and x + width <= 1 and y + height <= 1 else None


def _labelled_frames(tokens, frames):
    frames = list(frames)
    found = []
    for token in tokens:
        if not isinstance(token, dict) or not isinstance(token.get("text"), str) or _box(token) is None:
            continue
        label = _PLACEHOLDER_LABELS.get(re.sub(r"[\s:：._-]", "", token["text"]).lower())
        if label is None:
            continue
        eligible = []
        for frame in frames:
            box = _box(frame.get("bbox")) if isinstance(frame, dict) else None
            if box is None or box["width"] * box["height"] > .12 or box["width"] > .65 or box["height"] > .35:
                continue
            # 标签必须实际位于独立完整框内；不借页眉标签给邻框、整页或含作答题的大栏背书。
            if (box["x"] <= token["x"] and box["y"] <= token["y"]
                    and token["x"] + token["width"] <= box["x"] + box["width"]
                    and token["y"] + token["height"] <= box["y"] + box["height"]):
                eligible.append(box)
        if eligible:
            box = min(eligible, key=lambda box: box["width"] * box["height"])
            found.append((label, dict(box), "LOCAL_BARCODE_LABELLED_FRAME_DISCOVERED"))
    return found


def _overlap(first, second):
    width = max(0, min(first["x"] + first["width"], second["x"] + second["width"]) - max(first["x"], second["x"]))
    height = max(0, min(first["y"] + first["height"], second["y"] + second["height"]) - max(first["y"], second["y"]))
    return width * height / min(first["width"] * first["height"], second["width"] * second["height"])
