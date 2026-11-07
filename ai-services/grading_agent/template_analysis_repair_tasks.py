"""只按现有异常和空间关系选择局部复判任务，不调用模型或修改候选。"""

import math

_IDENTITY_LABELS = {"姓名", "学号", "准考证号", "考号", "班级", "学校", "身份信息"}


_CODE_LABELS = {"条码", "二维码"}


_INDEPENDENT_IDENTITY_REASONS = {"SEMANTIC_IDENTITY_LABEL_VERIFIED", "SEMANTIC_BARCODE_DECODE_VERIFIED",
                                 "SEMANTIC_BLANK_BARCODE_AREA_VERIFIED"}


def _box(row):
    box = row.get("proposed_bbox")
    if not isinstance(box, dict):
        return None
    try:
        values = [box[key] for key in ("x", "y", "width", "height")]
    except KeyError:
        return None
    if any(type(value) not in {int, float} or not math.isfinite(value) for value in values):
        return None
    x, y, width, height = values
    return box if x >= 0 and y >= 0 and width > 0 and height > 0 and x + width <= 1.000001 and y + height <= 1.000001 else None


def _repair_tasks(payload, rows):
    rows = [row for row in rows if _box(row) is not None and not row.get("_repair_deferred_to_hint")]
    covered = {row.get("printed_question_label") for row in rows
               if row.get("role") == "answer" and row.get("semantic_check") == "supported"}
    covered.update(item.get("printed_question_label") for row in rows
                   if row.get("role") == "objective_group" and row.get("semantic_check") == "supported"
                   for item in row.get("objective_items", []))
    missing_fill = [label for label in payload.get("fill_blank_question_labels", []) if label not in covered]
    objective = set(payload.get("objective_question_labels", []))
    objective_missing = bool(objective - covered)
    tasks, claimed = [], set()
    # 整页独立观察发现的问题优先做一次中立裁图；不能再次沿用被质疑的类别或只重试旧位置。
    # 新位置带着明确的旧框替换目标时优先重读；否则旧冲突框会先占用一次有限调用，
    # 且提前 claimed 旧编号后让真正的新位置失去原子替换机会。
    coverage_rows = sorted(rows, key=lambda row: not bool(row.get("_extra_replace_candidate_ids")))
    for row in coverage_rows:
        if any(code == "PAGE_COVERAGE_DISCOVERED_REGION" or code.startswith("PAGE_COVERAGE_")
               and code.endswith("_CONFLICT") for code in row.get("reason_codes", [])):
            if row.get("candidate_id") in claimed:
                continue
            task = _task("coverage_region", [row], missing_fill)
            tasks.append(task)
            claimed.update(task["replace_candidate_ids"])
    identities = [row for row in rows if row.get("candidate_id") not in claimed
                  and (row.get("role") == "identity" or row.get("_boundary_role") == "identity"
                                         or "MODEL_PROPOSED_IDENTITY" in row.get("reason_codes", []))
                  and (row.get("identity_label") in _IDENTITY_LABELS or row.get("_boundary_role") == "identity"
                       or "MODEL_PROPOSED_IDENTITY" in row.get("reason_codes", []))
                  # 题目缺失时允许复核模型自报的身份类别，但如果该框包含独立
                  # 条码定位提示则不把它纳入身份替换目标，避免可靠码框被覆盖。
                  and (row.get("semantic_check") != "supported" or _containing_answer_context(row, rows)
                       or objective_missing
                       and not _INDEPENDENT_IDENTITY_REASONS.intersection(row.get("reason_codes", [])))
                  and not _supported_identity_contains_barcode_hint(row, rows)]
    # 同一码的图像线索与模型旧框只占一次调用；位置匹配必须双向唯一，两个真实码不能互相抢替换权。
    identity_ids = {row["candidate_id"] for row in identities}
    hints = [row for row in rows if row.get("candidate_id") not in claimed | identity_ids
             and "BARCODE_DISCOVERY_CANDIDATE" in row.get("reason_codes", [])]
    code_identities = [row for row in identities if _is_code_region(row)]
    for hint in hints:
        matches = [row for row in code_identities if _same_code_location(row, hint)]
        if len(matches) == 1 and sum(_same_code_location(matches[0], other) for other in hints) == 1:
            identities.append(hint)
    # 同栏相邻或实际交叠的身份假设共享一次中立裁图。只扩展已有几何连通区，
    # 不按类别合并远处字段；两个不同码图即使被宽身份框连接也分别处理。
    while identities:
        cluster = [identities.pop(0)]
        pending = True
        while pending:
            pending = False
            for row in identities[:]:
                if _joins_identity_cluster(row, cluster):
                    cluster.append(row)
                    identities.remove(row)
                    pending = True
        # 身份假框可能落在已实测的整题框内部。只裁假框会反复读出半截同题，
        # 随后因重复题号而拒绝；连同唯一完整题框一起重读并原子替换，且不得丢掉原题。
        context = {answer["candidate_id"]: answer for row in cluster
                   for answer in _containing_answer_context(row, rows)
                   if answer["candidate_id"] not in claimed}
        cluster.extend(context.values())
        task = _task("identity_reclassify", cluster, missing_fill)
        if context:
            task["required_answer_labels"] = sorted({row["printed_question_label"] for row in context.values()})
        tasks.append(task)
        claimed.update(task["replace_candidate_ids"])

    # 规范化会清掉一行里无法唯一关联的题号，但会保留本页观察原因。
    # 这类大框不能依赖“全场缺哪些题”猜测内容；必须按原框重新读图，并让裁图模型
    # 自己拆出选择题、填空题或说明区。
    for row in rows:
        if row.get("candidate_id") in claimed:
            continue
        if ("MODEL_OBS_MULTIPLE_QUESTION_LABELS" not in row.get("reason_codes", [])
                or row.get("role") not in {"answer", "uncertain"}):
            continue
        task = _task("mixed_region", [row], [])
        tasks.append(task)
        claimed.update(task["replace_candidate_ids"])
    # 专用定位器提供的是待核验码图线索；裁图可以否决它，不能按“有线索”直接生成身份框。
    for row in rows:
        if "BARCODE_DISCOVERY_CANDIDATE" in row.get("reason_codes", []) and row.get("candidate_id") not in claimed:
            task = _task("barcode_region", [row], [])
            tasks.append(task)
            claimed.update(task["replace_candidate_ids"])
    # 相邻的异常选择题大区属于一次连续阅读任务。逐框重读会让模型把两个相邻
    # 分栏再次当成两个完整模板；但中间已有可靠的非选择题区域时不能跨过去合并。
    bad_objective = [row for row in rows if row.get("candidate_id") not in claimed
                     and _is_bad_objective(row)]
    for cluster in _cluster_objective_regions(bad_objective, rows):
        boxes = [row["proposed_bbox"] for row in cluster]
        height = max(box["y"] + box["height"] for box in boxes) - min(box["y"] for box in boxes)
        kind = "mixed_region" if missing_fill and height > .25 else "objective_region"
        task = _task(kind, cluster, missing_fill)
        tasks.append(task)
        claimed.update(task["replace_candidate_ids"])
    # 只重读本页已有印刷题号或本地填空候选，不能因全场缺题而假设当前页存在相应区域。
    for row in rows:
        if row.get("candidate_id") in claimed or row.get("semantic_check") == "supported":
            continue
        if row.get("printed_question_label") in missing_fill and (
                row.get("role") in {"answer", "uncertain"} or "LOCAL_FILL_REQUIRES_REVIEW" in row.get("reason_codes", [])):
            task = _task("fill_region", [row], [row["printed_question_label"]])
            tasks.append(task)
            claimed.update(task["replace_candidate_ids"])
    return tasks


def _is_code_region(row):
    return (row.get("identity_label") in _CODE_LABELS
            or row.get("_barcode_identity_label") in _CODE_LABELS
            or "BARCODE_DISCOVERY_CANDIDATE" in row.get("reason_codes", []))


def _containing_answer_context(row, rows):
    if _INDEPENDENT_IDENTITY_REASONS.intersection(row.get("reason_codes", [])):
        return []
    box = row["proposed_bbox"]
    matches = [answer for answer in rows if answer is not row
               and answer.get("role") == "answer" and answer.get("semantic_check") == "supported"
               and answer.get("printed_question_label")
               and "PRINTED_FRAME_VERIFIED" in answer.get("reason_codes", [])
               and any(ref.startswith("cv-frame:") for ref in answer.get("evidence_refs", []))
               and _intersection_area(box, answer["proposed_bbox"]) / (box["width"] * box["height"]) >= .95]
    # 多个题框同时声称包含该区域时仍有布局歧义，不能任意选择一个扩大替换范围。
    return matches if len(matches) == 1 else []


def _intersection_area(first, second):
    width = max(0, min(first["x"] + first["width"], second["x"] + second["width"])
                - max(first["x"], second["x"]))
    height = max(0, min(first["y"] + first["height"], second["y"] + second["height"])
                 - max(first["y"], second["y"]))
    return width * height


def _same_code_location(first, second):
    first_anchor = first.get("_barcode_anchor_bbox") or first.get("visual_evidence", {}).get("anchor_bbox")
    second_anchor = second.get("_barcode_anchor_bbox") or second.get("visual_evidence", {}).get("anchor_bbox")
    # 两个实际码图锚点优先于外框。一个大外框可能覆盖两个码，不能据此把它们当同一处。
    if _box({"proposed_bbox": first_anchor}) and _box({"proposed_bbox": second_anchor}):
        smaller = min(first_anchor["width"] * first_anchor["height"],
                      second_anchor["width"] * second_anchor["height"])
        return _intersection_area(first_anchor, second_anchor) / smaller >= .5
    first_box, second_box = first["proposed_bbox"], second["proposed_bbox"]
    intersection = _intersection_area(first_box, second_box)
    union = (first_box["width"] * first_box["height"]
             + second_box["width"] * second_box["height"] - intersection)
    # 缺少独立锚点时仅合并近乎同一位置的旧框；嵌套或邻近本身不足以证明是同一码。
    return intersection / union >= .7


def _joins_identity_cluster(row, cluster):
    if _is_code_region(row) and any(_is_code_region(other) and not _same_code_location(row, other)
                                    for other in cluster):
        return False
    box = row["proposed_bbox"]
    for other in cluster:
        other_box = other["proposed_bbox"]
        if _same_column_near(box, other_box):
            return True
        # 模型可能把姓名/考号框画宽，压到旁边条码。两次裁图会重复读取同一面板，
        # 只有原始区域实质交叠（占较小框至少一成）才横向合并，不使用额外裁图留白相交。
        smaller = min(box["width"] * box["height"], other_box["width"] * other_box["height"])
        if _intersection_area(box, other_box) / smaller >= .1:
            return True
    return False


def _is_bad_objective(row):
    """判断需要局部复判的选择题大区，不把全局缺题当作本页证据。"""
    codes = row.get("reason_codes", [])
    return ("OBJECTIVE_ITEMS_DEFERRED_TO_LOCAL" in codes
            or row.get("semantic_check") != "supported" and (
        row.get("role") == "objective_group" or row.get("_boundary_role") == "objective_group"
        or any("OBJECTIVE" in code and ("CONFLICT" in code or "MISSING" in code) for code in codes)))


def _supported_identity_contains_barcode_hint(row, rows):
    """发现条码线索落在可靠身份框内时，身份任务不取得该框的替换权。"""
    if row.get("semantic_check") != "supported":
        return False
    box = row.get("proposed_bbox")
    if box is None:
        return False
    for hint in rows:
        if not hint.get("_barcode_discovery_hint") or hint is row:
            continue
        other = hint.get("proposed_bbox")
        if other is None:
            continue
        intersection = max(0, min(box["x"] + box["width"], other["x"] + other["width"])
                           - max(box["x"], other["x"]))
        vertical = max(0, min(box["y"] + box["height"], other["y"] + other["height"])
                       - max(box["y"], other["y"]))
        if intersection * vertical / (box["width"] * box["height"]) >= .05:
            return True
    return False


def _cluster_objective_regions(rows, all_rows):
    """按同栏且相邻的异常选择题框分组，并在可靠区域处截断。"""
    clusters = []
    for row in sorted(rows, key=lambda item: (item["proposed_bbox"]["y"], item["proposed_bbox"]["x"])):
        if not clusters:
            clusters.append([row])
            continue
        previous = clusters[-1][-1]
        if (_same_column_near(previous["proposed_bbox"], row["proposed_bbox"])
                and not _objective_join_blocked(previous, row, all_rows)):
            clusters[-1].append(row)
        else:
            clusters.append([row])
    return clusters


def _objective_join_blocked(previous, current, rows):
    """可靠的非选择题区域位于两框之间时，禁止跨区域合并。"""
    left, right = previous["proposed_bbox"], current["proposed_bbox"]
    top = left["y"] + left["height"]
    bottom = right["y"]
    if bottom < top:
        top, bottom = bottom, top
    for row in rows:
        if row is previous or row is current or _is_bad_objective(row):
            continue
        if row.get("semantic_check") != "supported" or row.get("role") in {"instruction", "other"}:
            continue
        box = row.get("proposed_bbox")
        if box is None or box["y"] + box["height"] <= top or box["y"] >= bottom:
            continue
        intersection = max(0, min(left["x"] + left["width"], box["x"] + box["width"])
                           - max(left["x"], box["x"]))
        if intersection / min(left["width"], box["width"]) >= .5:
            return True
    return False


def _same_column_near(left, right):
    intersection = max(0, min(left["x"] + left["width"], right["x"] + right["width"]) - max(left["x"], right["x"]))
    gap = max(0, max(left["y"], right["y"]) - min(left["y"] + left["height"], right["y"] + right["height"]))
    return intersection / min(left["width"], right["width"]) >= .8 and gap <= .06


def _task(kind, rows, missing_fill):
    boxes = [row["proposed_bbox"] for row in rows]
    x, y = min(box["x"] for box in boxes), min(box["y"] for box in boxes)
    right, bottom = max(box["x"] + box["width"] for box in boxes), max(box["y"] + box["height"] for box in boxes)
    # 横向仅加栏内阅读余量；上下加少量标题/下一题上下文，不把裁图扩成按题号均分的区域。
    margin_x, margin_y = min(.015, (right - x) * .05), min(.035, max(.015, (bottom - y) * .12))
    left, top = max(0, x - margin_x), max(0, y - margin_y)
    # 查漏新位置与本轮已反证的旧框属于同一替换事务；只裁新位置，成功后再一并撤下旧框。
    targets = list(dict.fromkeys(identifier for row in rows
                                 for identifier in [row["candidate_id"], *row.get("_extra_replace_candidate_ids", [])]))
    return {"task": kind, "replace_candidate_ids": targets,
            "fill_question_labels": missing_fill,
            "bbox": {"x": left, "y": top, "width": min(1, right + margin_x) - left,
                     "height": min(1, bottom + margin_y) - top}}
