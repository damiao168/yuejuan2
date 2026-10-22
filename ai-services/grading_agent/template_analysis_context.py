"""移除局部裁图重复带入的说明片段，不改变真实用途分类。

只有已保留说明区与独立完整印刷框共同定位同一区域时才去重。
没有实测框、存在多义或可见用途冲突时保留原候选，继续正常复核。
"""

import math

from .answer_sheet_vision import regions_overlap

CONTEXT_FILTER_VERSION = "measured-frame-instruction-context-v2"
_INSTRUCTION_EVIDENCE = {"MULTIMODAL_FORBIDDEN_REGION_EXCLUDED", "MULTIMODAL_OBSERVATIONS_CONSISTENT"}


def filter_repair_context_instructions(fresh, kept, frames, page, crop_bbox, *, image=None):
    """返回新列表及固定计数，不修改输入行；仅去掉裁边处的同用途说明片段。

    坐标均为整页比例。完整框须在裁图外延续且已被保留说明区覆盖，
    不能仅凭模型两次自报相同标签或两个矩形相交就隐藏候选。
    """
    metadata = {"version": CONTEXT_FILTER_VERSION, "removed_count": 0, "reason_codes": []}
    dimensions = [page.get(key) for key in ("width", "height")]
    crop = _box(crop_bbox)
    if crop is None or any(type(value) not in {int, float} or not math.isfinite(value)
                           or value <= 0 for value in dimensions):
        return list(fresh), metadata
    if image is not None and (image.ndim != 3 or image.shape[:2] != (dimensions[1], dimensions[0])
                              or image.shape[2] != 3):
        image = None
    tolerance = (2 / dimensions[0], 2 / dimensions[1])
    valid_kept = [row for row in kept if _instruction(row)]
    visible = [row for row in [*kept, *fresh] if row.get("role") not in {"instruction", "other"}]
    measured = []
    for frame in frames:
        box = _box(frame.get("bbox"))
        if box is None or not any(isinstance(ref, str) and ref.startswith("cv-frame:")
                                  for ref in frame.get("evidence_refs", [])):
            continue
        # 同一实测框若仍有真实作答、身份或未知用途占用，不用旧说明分类压掉反证。
        if any(_box(row.get("proposed_bbox")) is not None and regions_overlap(
                frame["bbox"], row["proposed_bbox"], *dimensions) for row in visible):
            continue
        owners = [row for row in valid_kept if _covers_frame(row, box)]
        if len(owners) == 1:
            measured.append(box)
    result = []
    for row in fresh:
        box = _box(row.get("proposed_bbox"))
        matching = [frame for frame in measured if box is not None
                    and _clipped_fragment(box, frame, crop, tolerance, image)] if _instruction(row) else []
        if len(matching) == 1:
            metadata["removed_count"] += 1
        else:
            result.append(row)
    if metadata["removed_count"]:
        metadata["reason_codes"] = ["LOCAL_REPAIR_INSTRUCTION_CONTEXT_DEDUPLICATED"]
    return result, metadata


def _instruction(row):
    return (row.get("role") == "instruction" and row.get("semantic_check") != "needs_review"
            and _INSTRUCTION_EVIDENCE <= set(row.get("reason_codes", [])))


def _box(value):
    if not isinstance(value, dict):
        return None
    coordinates = [value.get(key) for key in ("x", "y", "width", "height")]
    if any(type(number) not in {int, float} or not math.isfinite(number) for number in coordinates):
        return None
    x, y, width, height = coordinates
    if x < 0 or y < 0 or width <= 0 or height <= 0 or x + width > 1.000001 or y + height > 1.000001:
        return None
    return x, y, x + width, y + height


def _area(box):
    return (box[2] - box[0]) * (box[3] - box[1])


def _intersection(first, second):
    return max(0, min(first[2], second[2]) - max(first[0], second[0])) * max(
        0, min(first[3], second[3]) - max(first[1], second[1]))


def _covers_frame(row, frame):
    box = _box(row.get("proposed_bbox"))
    if box is None:
        return False
    area = _intersection(box, frame)
    return area / _area(frame) >= .85 and area / _area(box) >= .5


def _clipped_fragment(box, frame, crop, tolerance, image):
    if any(box[index] < crop[index] - tolerance[index % 2] for index in (0, 1)) or any(
            box[index] > crop[index] + tolerance[index % 2] for index in (2, 3)):
        return False
    center = ((box[0] + box[2]) / 2, (box[1] + box[3]) / 2)
    if not (frame[0] <= center[0] <= frame[2] and frame[1] <= center[1] <= frame[3]):
        return False
    if _intersection(box, frame) / _area(box) < .6:
        return False
    # 粗框可略带相邻框线，但不能把大段未知内容当成说明上下文一起删除。
    margins = tuple(max(tolerance[axis], (frame[axis + 2] - frame[axis]) * .1) for axis in (0, 1))
    if any(box[index] < frame[index] - margins[index % 2] for index in (0, 1)) or any(
            box[index] > frame[index] + margins[index % 2] for index in (2, 3)):
        return False
    clipped = []
    for edge in range(4):
        axis = edge % 2
        direction = -1 if edge < 2 else 1
        extension = direction * (frame[edge] - crop[edge])
        if (abs(box[edge] - crop[edge]) <= tolerance[axis]
                and extension > max(tolerance[axis], (frame[axis + 2] - frame[axis]) * .2)):
            orthogonal = 1 - axis
            if _orthogonal_inside_or_blank(box, frame, orthogonal, tolerance, image):
                clipped.append(edge)
    return len(clipped) == 1


def _orthogonal_inside_or_blank(box, frame, axis, tolerance, image):
    if (box[axis] >= frame[axis] - tolerance[axis]
            and box[axis + 2] <= frame[axis + 2] + tolerance[axis]):
        return True
    # 粗片段可能多带入说明框旁的白边。只在原图证实超出部分没有印刷或笔迹时去重；
    # 仍沿用上方的十分之一框宽上限，不能仅凭模型说是说明就吞掉邻近未知内容。
    if image is None or image.ndim != 3 or image.shape[2] != 3:
        return False
    height, width = image.shape[:2]
    dimensions = (width, height)
    cross = 1 - axis
    cross_start = max(0, math.floor(box[cross] * dimensions[cross]))
    cross_end = min(dimensions[cross], math.ceil(box[cross + 2] * dimensions[cross]))
    for edge in (axis, axis + 2):
        if abs(box[edge] - frame[edge]) <= tolerance[axis]:
            continue
        outside = box[edge] < frame[edge] if edge < 2 else box[edge] > frame[edge]
        if not outside:
            continue
        if edge < 2:
            start, end = math.floor(box[edge] * dimensions[axis]), math.floor(frame[edge] * dimensions[axis]) - 2
        else:
            start, end = math.ceil(frame[edge] * dimensions[axis]) + 2, math.ceil(box[edge] * dimensions[axis])
        if not (0 <= start < end <= dimensions[axis] and cross_start < cross_end):
            return False
        patch = (image[cross_start:cross_end, start:end] if axis == 0
                 else image[start:end, cross_start:cross_end])
        # 两像素只排除已实测边框本身；白边内任何明显深色像素都保留候选交给正常复核。
        if (patch < 200).any():
            return False
    return True
