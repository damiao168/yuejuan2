"""校验多模态直接读取的选择题及选项坐标；不按题目清单补题或均分网格。"""

import math
from itertools import combinations


def _box(value):
    if not isinstance(value, dict) or set(value) != {"x", "y", "width", "height"}:
        return None
    values = [value[key] for key in ("x", "y", "width", "height")]
    if any(type(number) not in {int, float} or not math.isfinite(number) for number in values):
        return None
    x, y, width, height = values
    if x < 0 or y < 0 or width <= 0 or height <= 0 or x + width > 1.000001 or y + height > 1.000001:
        return None
    return values


def _contains(outer, inner):
    return all(outer[axis] <= inner[axis] + .000001
               and outer[axis] + outer[axis + 2] >= inner[axis] + inner[axis + 2] - .000001
               for axis in (0, 1))


def _overlap(first, second):
    width = max(0, min(first[0] + first[2], second[0] + second[2]) - max(first[0], second[0]))
    height = max(0, min(first[1] + first[3], second[1] + second[3]) - max(first[1], second[1]))
    return width > .000001 and height > .000001


def objective_groups_overlap(first, second):
    first_box, second_box = _box(first["proposed_bbox"]), _box(second["proposed_bbox"])
    return first_box is not None and second_box is not None and _overlap(first_box, second_box)


def constrain_objective_item_parents(row):
    """只裁掉单题父框超出大区的留白，不移动选项、不补题，也不修复选项坐标错误。

    多模态分别估算大区与单题父框时，父框的留白可能越界；只有所有真实选项建议
    原本已同时位于两层父框内，才取父框交集。重复、相交或跨题仍交给原校验器拒绝。
    """
    if row.get("role") != "objective_group":
        return False
    group = _box(row.get("proposed_bbox"))
    if group is None or not isinstance(row.get("objective_items"), list):
        return False
    changed = False
    for item in row["objective_items"]:
        if not isinstance(item, dict):
            continue
        parent = _box(item.get("bbox"))
        options = item.get("options")
        if parent is None or _contains(group, parent) or not isinstance(options, list) or not options:
            continue
        boxes = [_box(option.get("bbox")) if isinstance(option, dict) else None for option in options]
        if not all(box is not None and _contains(group, box) and _contains(parent, box) for box in boxes):
            continue
        left, top = max(group[0], parent[0]), max(group[1], parent[1])
        right, bottom = min(group[0] + group[2], parent[0] + parent[2]), min(group[1] + group[3], parent[1] + parent[3])
        if right > left and bottom > top:
            item["bbox"] = {"x": left, "y": top, "width": right - left, "height": bottom - top}
            changed = True
    return changed


def review_objective_items(row, payload, *, check_observed=True):
    """只返回稳定问题码；异常映射不能进入正式题区，原始响应由受控诊断另存。"""
    if "objective_items" not in row:
        return []
    items = row["objective_items"]
    if row["role"] != "objective_group":
        return ["MODEL_OBS_OBJECTIVE_ITEMS_ROLE_CONFLICT"] if items else []
    if not isinstance(items, list) or not items:
        return ["MODEL_OBS_OBJECTIVE_ITEMS_MISSING"]
    if len(items) > 256:
        return ["MODEL_OBS_OBJECTIVE_ITEMS_INVALID"]
    group = _box(row["proposed_bbox"])
    official = set(payload.get("objective_question_labels", []))
    observed = set(row.get("observed_question_labels", []))
    labels, boxes = set(), []
    for item in items:
        if not isinstance(item, dict) or set(item) != {"printed_question_label", "bbox", "options"}:
            return ["MODEL_OBS_OBJECTIVE_ITEMS_INVALID"]
        label, box, options = item["printed_question_label"], _box(item["bbox"]), item["options"]
        if not isinstance(label, str) or label not in official or label in labels:
            return ["MODEL_OBS_OBJECTIVE_ITEMS_QUESTION_CONFLICT"]
        if group is None or box is None or not _contains(group, box):
            return ["MODEL_OBS_OBJECTIVE_ITEMS_GEOMETRY_CONFLICT"]
        if not isinstance(options, list) or not 2 <= len(options) <= 12:
            return ["MODEL_OBS_OBJECTIVE_ITEMS_OPTIONS_CONFLICT"]
        option_labels, option_boxes = set(), []
        for option in options:
            if not isinstance(option, dict) or set(option) != {"label", "bbox"}:
                return ["MODEL_OBS_OBJECTIVE_ITEMS_INVALID"]
            name, option_box = option["label"], _box(option["bbox"])
            if not isinstance(name, str) or name not in "ABCDEFGHIJKL" or len(name) != 1 or name in option_labels:
                return ["MODEL_OBS_OBJECTIVE_ITEMS_OPTIONS_CONFLICT"]
            if option_box is None or not _contains(box, option_box):
                return ["MODEL_OBS_OBJECTIVE_ITEMS_GEOMETRY_CONFLICT"]
            option_labels.add(name)
            option_boxes.append(option_box)
        if option_labels != set("ABCDEFGHIJKL"[:len(options)]):
            return ["MODEL_OBS_OBJECTIVE_ITEMS_OPTIONS_CONFLICT"]
        # 与 Go/编辑器共用整页百万分之一的边界容差；重复选项或题区不能静默选其中一个。
        if any(_overlap(first, second) for first, second in combinations(option_boxes, 2)):
            return ["MODEL_OBS_OBJECTIVE_ITEMS_GEOMETRY_CONFLICT"]
        labels.add(label)
        boxes.append(box)
    if any(_overlap(first, second) for first, second in combinations(boxes, 2)):
        return ["MODEL_OBS_OBJECTIVE_ITEMS_GEOMETRY_CONFLICT"]
    # 只核对模型本页实际读到的选择题号；不能拿整场题目清单要求本页补齐不存在的题。
    if check_observed and labels != observed & official:
        return ["MODEL_OBS_OBJECTIVE_ITEMS_COVERAGE_CONFLICT"]
    return []
