"""用完整印刷外框补齐首轮选择题粗区漏掉的一条边，不修正逐题或选项坐标。"""

import math

from .answer_sheet_vision import regions_overlap

FRAME_ALIGNMENT_VERSION = "coarse-objective-printed-frame-v1"


def complete_coarse_objective_frame(row, frames, page, regions):
    """唯一完整框的三条边吻合且第四边只需外扩时，补齐粗区并返回 True。

    frames 必须来自本页完整印刷框检测，regions 是本页当前候选。新增范围不得占用
    非选择题用途；相邻共用边线沿用一个显示像素容差。原始来源框和任何选项均不改动。
    """
    if (row.get("_coarse_layout") is not True or row.get("role") != "objective_group"
            or row.get("objective_items") != []):
        return False
    box = row.get("proposed_bbox")
    if not _valid_box(box) or not _valid_dimension(page.get("width")) or not _valid_dimension(page.get("height")):
        return False
    original = _edges(box)
    horizontal = max(2 / page["width"], box["width"] * .08)
    vertical = max(2 / page["height"], box["height"] * .08)
    tolerances = (horizontal, vertical, horizontal, vertical)
    matches = []
    for frame in frames:
        target = frame.get("bbox")
        if not _valid_box(target):
            continue
        edges = _edges(target)
        nearby = [abs(first - second) <= tolerance for first, second, tolerance
                  in zip(original, edges, tolerances)]
        # 四边吻合交给原精修流程。它同样占据匹配名额，不能跳过近框去选更远的大框。
        if sum(nearby) >= 3:
            matches.append((frame, edges, nearby))
    if len(matches) != 1:
        return False
    frame, edges, nearby = matches[0]
    if all(nearby):
        return False
    missing = nearby.index(False)
    if ((missing < 2 and edges[missing] >= original[missing])
            or (missing >= 2 and edges[missing] <= original[missing])):
        return False
    # 只检查新增范围，旧区域的既有矛盾仍由语义层处理。三条近边也可能略向外移动，
    # 因此每条新增带都检查；框线相触仍沿用一个显示像素容差。
    extensions = [_extension_band(original, edges, index) for index in range(4)
                  if (edges[index] < original[index] if index < 2 else edges[index] > original[index])]
    for other in regions:
        if other is row or other.get("candidate_id") is not None and other.get("candidate_id") == row.get("candidate_id"):
            continue
        if other.get("role") == "objective_group" or not _valid_box(other.get("proposed_bbox")):
            continue
        if any(regions_overlap(extension, other["proposed_bbox"], page["width"], page["height"])
               for extension in extensions):
            return False
    row["proposed_bbox"] = dict(frame["bbox"])
    row["evidence_refs"] = list(dict.fromkeys([*row.get("evidence_refs", []), *frame.get("evidence_refs", [])]))
    reasons = row.setdefault("reason_codes", [])
    if "COARSE_OBJECTIVE_FRAME_EDGE_COMPLETED" not in reasons:
        reasons.append("COARSE_OBJECTIVE_FRAME_EDGE_COMPLETED")
    return True


def _valid_dimension(value):
    return type(value) in {int, float} and math.isfinite(value) and value > 0


def _valid_box(box):
    if not isinstance(box, dict):
        return False
    values = [box.get(key) for key in ("x", "y", "width", "height")]
    if any(type(value) not in {int, float} or not math.isfinite(value) for value in values):
        return False
    x, y, width, height = values
    return x >= 0 and y >= 0 and width > 0 and height > 0 and x + width <= 1.000001 and y + height <= 1.000001


def _edges(box):
    return box["x"], box["y"], box["x"] + box["width"], box["y"] + box["height"]


def _extension_band(original, target, missing):
    left, top, right, bottom = target
    if missing == 0:
        right = original[0]
    elif missing == 1:
        bottom = original[1]
    elif missing == 2:
        left = original[2]
    else:
        top = original[3]
    return {"x": left, "y": top, "width": right - left, "height": bottom - top}
