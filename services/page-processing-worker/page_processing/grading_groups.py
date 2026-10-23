"""独立整区按正式成员共享图片；已有逐题切片保留，缺少的位置不伪造。"""

import math

import cv2
import numpy as np

from page_processing.registration import RegistrationError


def resolve_grading_groups(regions: list[dict], grading_groups: list[dict] | None = None) -> dict[str, dict]:
    """合并独立分组和历史附属字段；同题不能借两种表示进入不同分组。"""
    groups: dict[str, dict] = {}
    for raw in grading_groups or []:
        if not isinstance(raw, dict):
            raise RegistrationError("invalid_template_grading_group")
        group_id, members = raw.get("group_id"), raw.get("question_ids")
        if not isinstance(group_id, str) or not group_id.strip() or group_id != group_id.strip() or len(group_id) > 128 or group_id in groups or not isinstance(members, list):
            raise RegistrationError("invalid_template_grading_group")
        values = _box(raw.get("bbox") or {})
        groups[group_id] = {"normalized_bbox": dict(zip(("x", "y", "width", "height"), values)), "question_ids": list(members)}
    independent = set(groups)
    for region in regions:
        group_id, bbox = region.get("grading_group_id"), region.get("grading_bbox")
        if group_id is None and bbox is None:
            continue
        if not isinstance(group_id, str) or not group_id.strip() or group_id != group_id.strip() or len(group_id) > 128 or not isinstance(bbox, dict):
            raise RegistrationError("invalid_template_grading_group")
        values = _box(bbox)
        if not _contains(values, _box(region)):
            raise RegistrationError("invalid_template_grading_group")
        group = groups.setdefault(group_id, {"normalized_bbox": dict(zip(("x", "y", "width", "height"), values)), "question_ids": []})
        previous = _box(group["normalized_bbox"])
        if any(abs(a - b) > 1e-6 for a, b in zip(values, previous)):
            raise RegistrationError("invalid_template_grading_group")
        question_id = region.get("question_id")
        if not isinstance(question_id, str) or not question_id.strip():
            raise RegistrationError("invalid_template_grading_group")
        if group_id in independent:
            if question_id not in group["question_ids"]:
                raise RegistrationError("invalid_template_grading_group")
        else:
            if question_id in group["question_ids"]:
                raise RegistrationError("invalid_template_grading_group")
            group["question_ids"].append(question_id)
    seen = set()
    for group_id, group in groups.items():
        members = group["question_ids"]
        if len(members) < 2 or len(members) > 500:
            raise RegistrationError("invalid_template_grading_group")
        for question_id in members:
            if not isinstance(question_id, str) or not question_id.strip() or question_id != question_id.strip() or question_id in seen:
                raise RegistrationError("invalid_template_grading_group")
            seen.add(question_id)
        values = _box(group["normalized_bbox"])
        for region in regions:
            if region.get("question_id") in members:
                if not _contains(values, _box(region)):
                    raise RegistrationError("invalid_template_grading_group")
            elif _overlaps(values, _box(region)):
                raise RegistrationError("invalid_template_grading_group")
        for other_id, other in groups.items():
            if other_id != group_id and _overlaps(values, _box(other["normalized_bbox"])):
                raise RegistrationError("invalid_template_grading_group")
    return groups


def grading_group_crops(registered_png: bytes, regions: list[dict], grading_groups: list[dict] | None = None) -> dict[str, dict]:
    """共享框来自冻结页面坐标；没有子题 ROI 也只生成一次整区图片。"""
    groups = resolve_grading_groups(regions, grading_groups)
    if not groups:
        return groups
    image = cv2.imdecode(np.frombuffer(registered_png, np.uint8), cv2.IMREAD_COLOR)
    if image is None:
        raise RegistrationError("invalid_template_grading_group_image")
    height, width = image.shape[:2]
    for group in groups.values():
        if len(group["question_ids"]) < 2:
            raise RegistrationError("invalid_template_grading_group")
        values = _box(group["normalized_bbox"])
        x, y, w, h = values
        left, top, right, bottom = round(x * width), round(y * height), round((x + w) * width), round((y + h) * height)
        if right <= left or bottom <= top:
            raise RegistrationError("empty_template_grading_group")
        ok, encoded = cv2.imencode(".png", image[top:bottom, left:right])
        if not ok:
            raise RegistrationError("invalid_template_grading_group_image")
        group["png"] = encoded.tobytes()
        group["pixel_bbox"] = {"x": left, "y": top, "width": right - left, "height": bottom - top}
    return groups


def _box(value: dict) -> tuple[float, float, float, float]:
    try:
        x, y, w, h = (float(value[key]) for key in ("x", "y", "width", "height"))
    except (KeyError, TypeError, ValueError) as exc:
        raise RegistrationError("invalid_template_grading_group") from exc
    if not all(math.isfinite(v) for v in (x, y, w, h)) or min(x, y) < 0 or min(w, h) <= 0 or x + w > 1.000001 or y + h > 1.000001:
        raise RegistrationError("invalid_template_grading_group")
    return x, y, w, h


def _contains(outer, inner):
    x, y, w, h = outer
    a, b, c, d = inner
    return x <= a + 1e-6 and y <= b + 1e-6 and x + w >= a + c - 1e-6 and y + h >= b + d - 1e-6


def _overlaps(first, second):
    x, y, w, h = first
    a, b, c, d = second
    return min(x + w, a + c) - max(x, a) > 1e-6 and min(y + h, b + d) - max(y, b) > 1e-6
