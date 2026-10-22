"""用独立 OCR 的真实字框排除小型“禁止作答”标牌。

只处理没有题号的未知小框，文字不完整、夹有正文或运行时不可用时保留原候选。
块级分数只用于筛除低质量证据；空间关系仍由真实单字框核验，不把分数当单字概率。
"""

import math
import time
from itertools import combinations, pairwise
from statistics import median

import cv2

FORBIDDEN_REGION_VERSION = "static-forbidden-wordbox-v1"
_LABEL = "禁止作答"
_MAX_REGIONS = 4
_PAGE_SECONDS = 6.0
_REGION_SECONDS = 3.0
_MIN_BLOCK_CONFIDENCE = .95
_MAX_CROP_BYTES = 512 * 1024
_FAILURES = {
    "WARMING": "LOCAL_FORBIDDEN_OCR_WARMING",
    "TIMEOUT": "LOCAL_FORBIDDEN_OCR_TIMEOUT",
    "UNAVAILABLE": "LOCAL_FORBIDDEN_OCR_UNAVAILABLE",
}


def review_forbidden_regions(image, page, rows, payload, *, ocr=None, clock=None):
    """原地标记已核实的禁区，返回固定失败码；不删除候选、不推断缺失题号。

    ``ocr`` 可注入运行时对象或接受 PNG 与 timeout 的函数。所有裁图共享六秒预算，
    运行时预热不能等待；默认关闭属于正常未执行，不报识别失败。
    """
    now = clock or time.monotonic
    deadline = now() + _PAGE_SECONDS
    eligible = sorted((row for row in rows if _eligible(row, image.shape)),
                      key=lambda row: row["proposed_bbox"]["width"] * row["proposed_bbox"]["height"])
    if not eligible:
        return []
    if ocr is None:
        from .template_analysis_ocr_runtime import get_template_static_ocr_runtime
        ocr = get_template_static_ocr_runtime()
    reasons = []
    for index, row in enumerate(eligible):
        if index >= _MAX_REGIONS or deadline - now() <= .05:
            reasons.append("LOCAL_FORBIDDEN_CHECK_BUDGET_EXCEEDED")
            break
        try:
            encoded, crop_shape = _encode_crop(image, row["proposed_bbox"])
            remaining = deadline - now()
            if remaining <= .05:
                reasons.append("LOCAL_FORBIDDEN_CHECK_BUDGET_EXCEEDED")
                break
            reader = getattr(ocr, "analyze_crop", ocr)
            result = reader(encoded, timeout=min(_REGION_SECONDS, remaining))
        except (OSError, ValueError, TypeError, cv2.error):
            reasons.append("LOCAL_FORBIDDEN_OCR_FAILED")
            continue
        if now() >= deadline:
            reasons.append("LOCAL_FORBIDDEN_CHECK_BUDGET_EXCEEDED")
            break
        if not isinstance(result, dict):
            reasons.append("LOCAL_FORBIDDEN_OCR_FAILED")
            continue
        if result.get("status") != "ok":
            reason = _runtime_failure(result)
            if reason:
                reasons.append(reason)
            # 预热、关闭或超时后的进程对同页其他框也不可用，不能反复触发启动或等待。
            if result.get("status") == "unavailable" or reason == "LOCAL_FORBIDDEN_OCR_TIMEOUT":
                break
            continue
        if not _label_verified(result, crop_shape):
            continue
        row.update(role="instruction", identity_label=None, printed_question_label=None,
                   association_status="unassociated", semantic_check="supported")
        row["reason_codes"] = list(dict.fromkeys([*row.get("reason_codes", []), "LOCAL_FORBIDDEN_LABEL_VERIFIED"]))
        ref = f"local-forbidden:{FORBIDDEN_REGION_VERSION}:{page['sha256'][:16]}:{page['page_no']}"
        row["evidence_refs"] = list(dict.fromkeys([*row.get("evidence_refs", []), ref]))
    return list(dict.fromkeys(reasons))


def _box(box):
    if not isinstance(box, dict):
        return None
    values = [box.get(key) for key in ("x", "y", "width", "height")]
    if any(isinstance(value, bool) or not isinstance(value, (int, float)) or not math.isfinite(value)
           for value in values):
        return None
    x, y, width, height = values
    if x < 0 or y < 0 or width <= 0 or height <= 0 or x + width > 1.000001 or y + height > 1.000001:
        return None
    return values


def _eligible(row, shape):
    # 正式题号和已关联区域都由教师题目流程处理；不能因旁边含警示语就撤掉作答区。
    if (row.get("role") not in {"uncertain", "other"} or row.get("printed_question_label")
            or row.get("association_status") != "unassociated"):
        return False
    box = _box(row.get("proposed_bbox"))
    if box is None:
        return False
    _, _, width, height = box
    pixel_width, pixel_height = width * shape[1], height * shape[0]
    # 比例按整页计，纵横比按实际像素计。巨大填空栏即使含四字标牌也不进入自动排除。
    return (.0004 <= width * height <= .04 and width <= .4 and height <= .25
            and min(pixel_width, pixel_height) >= 16 and .18 <= pixel_width / pixel_height <= 6)


def _encode_crop(image, box):
    height, width = image.shape[:2]
    left, top = math.floor(box["x"] * width), math.floor(box["y"] * height)
    right = min(width, math.ceil((box["x"] + box["width"]) * width))
    bottom = min(height, math.ceil((box["y"] + box["height"]) * height))
    crop = image[top:bottom, left:right]
    scale = min(1, 1000 / max(crop.shape[:2]))
    if scale < 1:
        crop = cv2.resize(crop, None, fx=scale, fy=scale, interpolation=cv2.INTER_AREA)
    # 只降采样有限次；超过协议字节上限就保留原候选，不发送无界图像。
    for _ in range(3):
        ok, encoded = cv2.imencode(".png", cv2.cvtColor(crop, cv2.COLOR_RGB2BGR),
                                   [cv2.IMWRITE_PNG_COMPRESSION, 6])
        if ok and encoded.size <= _MAX_CROP_BYTES:
            return encoded.tobytes(), crop.shape
        crop = cv2.resize(crop, None, fx=.75, fy=.75, interpolation=cv2.INTER_AREA)
    raise ValueError("crop_size_limit")


def _runtime_failure(result):
    code = result.get("reason_code")
    # 供应商异常及 OCR 正文都不能穿透到页面，只接受约定的错误类别。
    if isinstance(code, str):
        suffix = code.rsplit("_", 1)[-1]
        if suffix == "DISABLED":
            return None
        if suffix in _FAILURES:
            return _FAILURES[suffix]
    return "LOCAL_FORBIDDEN_OCR_UNAVAILABLE" if result.get("status") == "unavailable" else "LOCAL_FORBIDDEN_OCR_FAILED"


def _label_verified(result, shape):
    characters = result.get("characters")
    # 计数包括所有 >=0.5 的正文块；只回传静态四字不能掩盖区域里的其他内容。
    if (not isinstance(result.get("recognized_character_count"), int) or result.get("recognized_character_count") != 4
            or isinstance(result.get("recognized_character_count"), bool)
            or not isinstance(result.get("recognized_block_count"), int)
            or isinstance(result.get("recognized_block_count"), bool)
            or not 1 <= result["recognized_block_count"] <= 4 or not isinstance(characters, list) or len(characters) != 4):
        return False
    boxes = {}
    for item in characters:
        if not isinstance(item, dict):
            return False
        character = item.get("character")
        if not isinstance(character, str) or len(character) != 1 or character not in _LABEL or character in boxes:
            return False
        confidence = item.get("confidence")
        box = _box(item.get("bbox"))
        if (box is None or item.get("confidence_scope") != "block" or isinstance(confidence, bool)
                or not isinstance(confidence, (int, float)) or not math.isfinite(confidence)
                or not _MIN_BLOCK_CONFIDENCE <= confidence <= 1):
            return False
        x, y, width, height = box
        boxes[character] = (x * shape[1], y * shape[0], width * shape[1], height * shape[0])
    if set(boxes) != set(_LABEL):
        return False
    widths, heights = [box[2] for box in boxes.values()], [box[3] for box in boxes.values()]
    if (max(widths) / min(widths) > 1.8 or max(heights) / min(heights) > 1.8
            or any(not .45 <= box[2] / box[3] <= 1.9 for box in boxes.values())):
        return False
    for first, second in combinations(boxes.values(), 2):
        overlap = max(0, min(first[0] + first[2], second[0] + second[2]) - max(first[0], second[0])) * max(
            0, min(first[1] + first[3], second[1] + second[3]) - max(first[1], second[1]))
        if overlap / min(first[2] * first[3], second[2] * second[3]) > .2:
            return False
    span_width = max(box[0] + box[2] for box in boxes.values()) - min(box[0] for box in boxes.values())
    span_height = max(box[1] + box[3] for box in boxes.values()) - min(box[1] for box in boxes.values())
    # 只占角落的小标牌不能证明整个候选都是禁区，避免藏起混合作答区域。
    if (span_width < shape[1] * .45 or span_height < shape[0] * .25
            or span_width * span_height < shape[1] * shape[0] * .28):
        return False
    centers = {key: (box[0] + box[2] / 2, box[1] + box[3] / 2) for key, box in boxes.items()}
    unit_width, unit_height = median(widths), median(heights)
    def aligned(labels, axis):
        other = 1 - axis
        unit, cross = (unit_width, unit_height) if axis == 0 else (unit_height, unit_width)
        return (max(centers[key][other] for key in labels) - min(centers[key][other] for key in labels) <= cross * .4
                and all(.65 * unit <= centers[right][axis] - centers[left][axis] <= unit * 3.5
                        for left, right in pairwise(labels)))
    # 真实样本被 OCR 读成左列“禁作”、右列“止答”；必须按字框还原行列，不能拼块文本。
    return (aligned(_LABEL, 0) or aligned(_LABEL, 1)
            or (aligned("禁止", 0) and aligned("作答", 0) and aligned("禁作", 1) and aligned("止答", 1)))
