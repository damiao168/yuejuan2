"""用模型题号、独立题号锚点和唯一行列拓扑关联实测选项，不补题或推算缺失格。"""

import copy
import math
import re
from itertools import pairwise
from statistics import median

import cv2

from .choice_grid_detection import detect_option_groups
from .template_analysis_objective_items import review_objective_items
from .template_analysis_observations import review_model_observations

OPTION_ALIGNMENT_VERSION = "question-anchored-measured-options-v3"
_KEYS = ("x", "y", "width", "height")


def merge_objective_groups(rows, image, payload, *, tokens=(), measurement_bbox=None, frames=(), protected_regions=()):
    """连续实测框和全组选项均唯一对应时合并；失败返回原行，不修改模型观察。

    输入仍是局部模型结果，所有坐标为来源整页比例。每组先独立检查语义反证，
    再合并其实际观察题号并重测全部选项；正式清单只用于核对，不补任何题或格。
    protected_regions 由调用方提供保留区，包含说明与未知用途，不能跨过它们合框。
    """
    groups = [row for row in rows if row.get("role") == "objective_group"]
    metadata = {"merged": False, "input_groups": len(groups), "matched_items": 0, "verified_anchors": 0,
                "reason_code": "LOCAL_OBJECTIVE_GROUP_MERGE_UNRESOLVED"}
    if len(groups) < 2 or image is None or image.ndim != 3 or image.shape[2] != 3:
        return list(rows), metadata
    region = _box(measurement_bbox)
    if region is None:
        return list(rows), metadata
    reviewed = copy.deepcopy(groups)
    labels, items = [], []
    for row in reviewed:
        # 不能只取第一组的证据后重建“正常”大框；第二组的身份、禁区或题型反证同样须保留。
        review_model_observations(row, payload)
        if row.get("semantic_check") == "needs_review" or not row.get("objective_items"):
            return list(rows), metadata
        observed = row.get("observed_question_labels")
        item_labels = [item.get("printed_question_label") for item in row["objective_items"]]
        if (not isinstance(observed, list) or len(set(observed)) != len(observed)
                or set(observed) != set(item_labels) or len(set(item_labels)) != len(item_labels)):
            return list(rows), metadata
        labels.extend(observed)
        items.extend(row["objective_items"])
    if len(set(labels)) != len(labels):
        return list(rows), metadata
    source_boxes = [_box(row.get("proposed_bbox")) for row in reviewed]
    if any(box is None for box in source_boxes):
        return list(rows), metadata
    merged = copy.deepcopy(reviewed[0])
    merged.update(candidate_id=None, printed_question_label=None, identity_label=None, association_status="unassociated",
                  observed_question_labels=labels, objective_items=items)
    merged["proposed_bbox"] = dict(zip(_KEYS, _bounds(source_boxes)))
    merged["evidence_refs"] = list(dict.fromkeys(ref for row in reviewed for ref in row.get("evidence_refs", [])))
    merged["reason_codes"] = list(dict.fromkeys(code for row in reviewed for code in row.get("reason_codes", [])))
    if any(row.get("_requires_option_alignment") for row in reviewed):
        merged["_requires_option_alignment"] = True
    measured = align_objective_options(merged, image, payload, tokens=tokens,
                                       measurement_bbox=measurement_bbox, frames=frames)
    if not measured["aligned"]:
        return list(rows), metadata
    cells = [[_box(option["bbox"]) for option in item["options"]] for item in merged["objective_items"]]
    printed_frames = [frame for frame in frames if any(isinstance(ref, str) and ref.startswith("cv-frame:")
                                                      for ref in frame.get("evidence_refs", []))]
    height, width = image.shape[:2]
    enclosure = _frame_enclosure(cells, printed_frames, region, width, height)
    if enclosure is None:
        return list(rows), metadata
    barriers = [row for row in rows if row.get("role") != "objective_group"] + list(protected_regions)
    # 合并大框只能取真实连续框的并集；邻近身份、填空、禁区或未知区即使只在中间也不能被吞并。
    if any((box := _box(row.get("proposed_bbox"))) is not None and _positive_overlap(enclosure, box) for row in barriers):
        return list(rows), metadata
    merged["proposed_bbox"] = dict(zip(_KEYS, enclosure))
    if review_objective_items(merged, payload):
        return list(rows), metadata
    output, inserted = [], False
    for row in rows:
        if row.get("role") == "objective_group":
            if not inserted:
                output.append(merged)
                inserted = True
        else:
            output.append(row)
    metadata.update(merged=True, matched_items=measured["matched_items"], verified_anchors=measured["verified_anchors"],
                    reason_code="LOCAL_OBJECTIVE_GROUPS_PIXELS_MERGED")
    return output, metadata


def _positive_overlap(first, second):
    return all(min(first[axis] + first[axis + 2], second[axis] + second[axis + 2])
               > max(first[axis], second[axis]) + 1e-6 for axis in (0, 1))


def align_objective_options(row, image, payload, *, tokens=(), measurement_bbox=None, frames=()):
    """完整唯一对应才原子更新整组；失败保留模型原值，调用方继续执行原语义及冲突检查。

    所有 bbox 和 OCR token 坐标均为来源整页归一化坐标。measurement_bbox 必须由
    本次受控裁图任务提供；允许修正模型偏小的大区，但只能采用连续实测印刷框的并集。
    OCR 只在内存中使用，返回值只含固定原因和计数，不返回填写内容。
    """
    failure = {"aligned": False, "reason_code": "LOCAL_OBJECTIVE_OPTION_ALIGNMENT_UNRESOLVED",
               "matched_items": 0, "verified_anchors": 0}
    items = row.get("objective_items")
    if row.get("role") != "objective_group" or not isinstance(items, list) or not 1 <= len(items) <= 256:
        return failure
    official = set(payload.get("objective_question_labels", []))
    labels = [item.get("printed_question_label") for item in items if isinstance(item, dict)]
    if (len(labels) != len(items) or any(not isinstance(label, str) or label not in official for label in labels)
            or len(set(labels)) != len(labels)):
        return failure
    observed = row.get("observed_question_labels")
    if observed is not None and set(observed) != set(labels):
        return failure
    region = _box(measurement_bbox or row.get("proposed_bbox"))
    if region is None or image is None or image.ndim != 3 or image.shape[2] != 3:
        return failure
    height, width = image.shape[:2]
    left, top = _pixel_edge(region[0] * width), _pixel_edge(region[1] * height)
    right = _pixel_edge((region[0] + region[2]) * width, upper=True)
    bottom = _pixel_edge((region[1] + region[3]) * height, upper=True)
    if right <= left or bottom <= top:
        return failure
    source_groups = [_model_cells(item) for item in items]
    if any(group is None for group in source_groups):
        return failure
    counts = {len(group) for group in source_groups}
    orientations = {_orientation(group) for group in source_groups}
    if len(counts) != 1 or len(orientations) != 1 or None in orientations:
        return failure
    orientation = orientations.pop()
    measured, _ = detect_option_groups(image[top:bottom, left:right], counts.pop(), orientation)
    if len(measured) != len(items):
        return failure
    measured = [[((cell[0] + left) / width, (cell[1] + top) / height,
                  cell[2] / width, cell[3] / height) for cell in group] for group in measured]
    # 只有真实选择题标题或至少三种实际选项字母可支持用途，纯数字格和红色示例不能靠模型自证。
    inside = [token for token in tokens if _inside_token(token, region)]
    if not _choice_content(inside, measured):
        return failure
    signatures = [_signature(group, source_groups, orientation) for group in source_groups]
    measured_signatures = [_signature(group, measured, orientation) for group in measured]
    mapping = []
    for signature in signatures:
        matches = [index for index, other in enumerate(measured_signatures) if other == signature]
        if len(matches) != 1:
            return failure
        mapping.append(matches[0])
    if len(set(mapping)) != len(mapping):
        return failure
    anchors = _anchors(inside, measured, official, orientation)
    if not anchors:
        return failure
    assignment = {group: labels[index] for index, group in enumerate(mapping)}
    if any(assignment[index] != label for label, index, _ in anchors):
        return failure
    # 同一标签出现两个不同位置也属矛盾，不能只挑与模型相符的一个题号。
    if any(len({index for label, index, _ in anchors if label == candidate}) != 1
           for candidate in {label for label, _, _ in anchors}):
        return failure
    updated = copy.deepcopy(row)
    item_bounds = _measured_item_bounds(image, measured, anchors, region, orientation)
    for item, group_index in zip(updated["objective_items"], mapping, strict=True):
        cells = measured[group_index]
        for option, box in zip(item["options"], cells, strict=True):
            option["bbox"] = dict(zip(_KEYS, box))
        item["bbox"] = dict(zip(_KEYS, item_bounds[group_index]))
    bounds = [_box(item["bbox"]) for item in updated["objective_items"]]
    outer = _box(updated.get("proposed_bbox"))
    if outer is None or not all(_contains(outer, box) for box in bounds):
        outer = _frame_enclosure(measured, frames, region, width, height)
        if outer is None or not all(_contains(outer, box) for box in bounds):
            return failure
        updated["proposed_bbox"] = dict(zip(_KEYS, outer))
    if review_objective_items(updated, payload):
        return failure
    row["objective_items"] = updated["objective_items"]
    row["proposed_bbox"] = updated["proposed_bbox"]
    return {"aligned": True, "reason_code": "LOCAL_OBJECTIVE_OPTION_PIXELS_ALIGNED",
            "matched_items": len(items), "verified_anchors": len({(label, index) for label, index, _ in anchors})}


def _box(value):
    if not isinstance(value, dict):
        return None
    values = tuple(value.get(key) for key in _KEYS)
    if any(type(number) not in {int, float} or not math.isfinite(number) for number in values):
        return None
    x, y, width, height = values
    return values if x >= 0 and y >= 0 and width > 0 and height > 0 and x + width <= 1.000001 and y + height <= 1.000001 else None


def _pixel_edge(value, *, upper=False):
    # 受控裁图把整数像素边归一化后，乘回原尺寸可能出现 112.99999999999999。
    # 只吸附浮点尾差，避免重复 floor/ceil 多取一行或一列；真实小数边仍按包含范围取整。
    nearest = round(value)
    if abs(value - nearest) <= 1e-8:
        value = nearest
    return math.ceil(value) if upper else math.floor(value)


def _model_cells(item):
    options = item.get("options")
    if not isinstance(options, list) or not 2 <= len(options) <= 12:
        return None
    if any(not isinstance(option, dict) for option in options):
        return None
    if [option.get("label") for option in options] != list("ABCDEFGHIJKL"[:len(options)]):
        return None
    boxes = [_box(option.get("bbox")) for option in options]
    return boxes if all(box is not None for box in boxes) else None


def _orientation(cells):
    horizontal = all(second[0] >= first[0] + first[2] for first, second in pairwise(cells))
    vertical = all(second[1] >= first[1] + first[3] for first, second in pairwise(cells))
    return "horizontal" if horizontal and not vertical else "vertical" if vertical and not horizontal else None


def _feature(cells, orientation):
    along = 0 if orientation == "horizontal" else 1
    cross = 1 - along
    point = [0, 0]
    point[along] = cells[0][along] + cells[0][along + 2] / 2
    point[cross] = median(cell[cross] + cell[cross + 2] / 2 for cell in cells)
    return point, [median(cell[axis + 2] for cell in cells) for axis in (0, 1)]


def _signature(group, groups, orientation):
    point, size = _feature(group, orientation)
    result = []
    for axis in (0, 1):
        relations = []
        for other in groups:
            target, other_size = _feature(other, orientation)
            distance = target[axis] - point[axis]
            # 同行同列容差只随实测格尺寸缩放，不按题数、模板位置或整张页宽外推。
            relations.append(0 if abs(distance) <= min(size[axis], other_size[axis]) * .6
                             else 1 if distance > 0 else -1)
        result.extend(relations.count(sign) for sign in (-1, 0, 1))
    return tuple(result)


def _inside_token(token, region):
    box = _box(token)
    return box is not None and _contains(region, box)


def _choice_content(tokens, groups):
    heading = r"(?:[一二三四五六七八九十\d]+[、.．:：]?)?[、.．:：]?(?:单选题?|多选题?|选择题|multiplechoice)"
    if any(re.fullmatch(heading, re.sub(r"\s+", "", str(token.get("text", ""))), re.IGNORECASE) for token in tokens):
        return True
    letters = set()
    for token in tokens:
        text = str(token.get("text", "")).strip()
        if re.fullmatch(r"[\[（(]?[A-L][\]）)]?", text) and any(
                _contains(_bounds(group), _box(token)) for group in groups):
            letters.add(text.strip("[]（）()"))
    return len(letters) >= 3


def _anchors(tokens, groups, official, orientation):
    result = []
    if orientation != "horizontal":
        return result
    for token in tokens:
        if token.get("_joined_static_words"):
            continue
        match = re.fullmatch(r"(\d{1,3})[.、．]?", str(token.get("text", "")).strip())
        box = _box(token)
        if match is None or match.group(1) not in official or box is None:
            continue
        matching = []
        for index, group in enumerate(groups):
            point, size = _feature(group, orientation)
            gap = group[0][0] - box[0] - box[2]
            if (0 <= gap <= size[1] * 5 and .5 <= box[3] / size[1] <= 1.8
                    and abs(box[1] + box[3] / 2 - point[1]) <= size[1] * .65):
                matching.append(index)
        if len(matching) == 1:
            result.append((match.group(1), matching[0], box))
    return result


def _measured_item_bounds(image, groups, anchors, region, orientation, *, protected=()):
    # 锚点只需足够消歧即可完成题号排布，不能因此让未被 OCR 读中的题只框住选项。
    # 题号外框补测真实墨迹；不读数字身份，不按题号清单或相邻题号生成位置，也不改选项小格。
    bounds, original_bounds = [], []
    for index, group in enumerate(groups):
        markers = [box for _label, group_index, box in anchors if group_index == index]
        original_bounds.append(_bounds([*group, *markers]))
        if orientation == "horizontal":
            marker = _number_ink_bounds(image, groups, index, region)
            if marker is not None:
                markers.append(marker)
        bounds.append(_bounds([*group, *markers]))
    # 补题号外框不能使原本可用的 OMR 跨入邻题或其他用途；此处只放弃新增的墨迹范围。
    conflicts = {index for index, box in enumerate(bounds) if any(
        index != other_index and _positive_overlap(box, other) for other_index, other in enumerate(bounds))
        or any(_positive_overlap(box, other) for other in protected)}
    return [original_bounds[index] if index in conflicts else box for index, box in enumerate(bounds)]


def _number_ink_bounds(image, groups, index, region):
    height, width = image.shape[:2]
    group = groups[index]
    point, size = _feature(group, "horizontal")
    # 以下长度为原图像素；五个格高的左带和上下各 1.4 个格高仅限制搜索，不作为题号外框。
    scale = size[1] * height
    right = _pixel_edge(group[0][0] * width) - 2
    left = max(_pixel_edge(region[0] * width), round(right - scale * 5))
    # 多列可能紧邻；上一组最后一个选项是硬边界，不能把它当作下一题的数字字形。
    for other in groups:
        other_point, other_size = _feature(other, "horizontal")
        other_box = _bounds(other)
        edge = _pixel_edge((other_box[0] + other_box[2]) * width, upper=True)
        if (edge <= right and abs(other_point[1] - point[1]) <= max(size[1], other_size[1]) * .8):
            left = max(left, edge + 2)
    top = max(_pixel_edge(region[1] * height), round(point[1] * height - scale * 1.4))
    bottom = min(_pixel_edge((region[1] + region[3]) * height, upper=True), round(point[1] * height + scale * 1.4))
    if right <= left or bottom <= top:
        return None
    crop = image[top:bottom, left:right]
    gray = cv2.cvtColor(crop, cv2.COLOR_RGB2GRAY)
    # 红勾、红叉常在题号和 A 格之间；先去除明显彩色像素，残余小碎点不能算作数字。
    gray[crop.max(axis=2).astype(int) - crop.min(axis=2) > 75] = 255
    ink = cv2.threshold(gray, 0, 255, cv2.THRESH_BINARY_INV | cv2.THRESH_OTSU)[1]
    count, _labels, stats, _centers = cv2.connectedComponentsWithStats(ink)
    if count > 128:
        return None
    characters = []
    for x, y, w, h, area in stats[1:]:
        if (not .5 * scale <= h <= 1.8 * scale or not 1 <= w <= 1.4 * scale
                or area < max(3, h * .65) or abs(top + y + h / 2 - point[1] * height) > scale * .8):
            continue
        # 窄带切到字形时不能把残留半个数字当作完整题号扩大外框。
        if x == 0 or y == 0 or x + w >= right - left or y + h >= bottom - top:
            return None
        characters.append((left + int(x), top + int(y), int(w), int(h)))
    clusters = []
    for character in sorted(characters):
        if (not clusters or character[0] - (clusters[-1][-1][0] + clusters[-1][-1][2]) > max(2, scale * .7)
                or abs(character[1] + character[3] / 2 - median(box[1] + box[3] / 2 for box in clusters[-1])) > scale * .5):
            clusters.append([])
        clusters[-1].append(character)
    # 一至三位数字可由多个连通字形组成；多簇或过宽墨迹无法唯一归属，保留既有 OCR/OMR 范围。
    if len(clusters) != 1 or len(clusters[0]) > 3:
        return None
    marker = _bounds(clusters[0])
    if marker[2] > scale * 2.8:
        return None
    return marker[0] / width, marker[1] / height, marker[2] / width, marker[3] / height


def _bounds(boxes):
    left, top = min(box[0] for box in boxes), min(box[1] for box in boxes)
    return left, top, max(box[0] + box[2] for box in boxes) - left, max(box[1] + box[3] for box in boxes) - top


def _contains(outer, inner):
    return all(outer[axis] <= inner[axis] + 1e-6 and outer[axis] + outer[axis + 2] >= inner[axis] + inner[axis + 2] - 1e-6 for axis in (0, 1))


def _frame_enclosure(groups, frames, region, width, height):
    selected = []
    for group in groups:
        matches = [box for frame in frames if (box := _box(frame.get("bbox"))) is not None
                   and _contains(region, box) and _contains(box, _bounds(group))]
        if len(matches) != 1:
            return None
        if matches[0] not in selected:
            selected.append(matches[0])
    connected = {0}
    while True:
        addition = {index for index, box in enumerate(selected) if index not in connected and any(
            _adjacent(box, selected[other], width, height) for other in connected)}
        if not addition:
            break
        connected.update(addition)
    return _bounds(selected) if len(connected) == len(selected) else None


def _adjacent(first, second, width, height):
    for axis, tolerance in ((0, 2 / width), (1, 2 / height)):
        cross = 1 - axis
        cross_tolerance = 2 / (height if cross else width)
        if (abs(first[cross] - second[cross]) <= cross_tolerance
                and abs(first[cross] + first[cross + 2] - second[cross] - second[cross + 2]) <= cross_tolerance
                and min(abs(first[axis] + first[axis + 2] - second[axis]),
                        abs(second[axis] + second[axis + 2] - first[axis])) <= tolerance):
            return True
    return False
