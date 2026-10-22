"""将独立整页观察作为反证或漏区建议，不用第二次回答直接覆盖原分类。

已有框只会因明确矛盾降级；漏区仍经同一内容与全局布局门禁。
这里不推断整场缺题，也不把第二次模型的一致回答升级成独立确认。
"""

import copy

from jsonschema import Draft202012Validator
from jsonschema.exceptions import ValidationError

from .answer_sheet_vision import regions_overlap
from .errors import AgentError
from .template_analysis_objective_items import review_objective_items
from .template_analysis_observations import _answer_observed_labels
from .template_analysis_schema import MAX_CANDIDATES, model_output_schema


def apply_page_coverage(existing, coverage, payload, frames, page, content_evidence):
    """仅应用 proposed 响应；统计矛盾旧框、追加漏区和拒绝漏区的数量。

    observations 的 index 固定引用调用前列表，未观察的旧框保持原状。真实漏区带
    固定原因交给末轮裁图；同题新位置只有在本轮明确反证旧框后才可原子替换。
    """
    result = {"observed_conflict_count": 0, "discoveries_added": 0, "rejected_count": 0}
    if coverage.get("metadata", {}).get("status") != "proposed":
        return result
    original = tuple(existing)
    changed = set()
    official = set(payload["question_labels"])
    for observation in coverage.get("observations", []):
        index = observation.get("index")
        if type(index) is not int or not 0 <= index < len(original):
            continue
        row = original[index]
        observed_role = observation["role"]
        codes = []
        if observed_role != "uncertain" and observed_role != row["role"]:
            codes.append("PAGE_COVERAGE_ROLE_CONFLICT")
        # uncertain 代表本轮没读清，不能拿其中猜测的题号反驳已有可用结果。
        if observed_role != "uncertain":
            codes.extend(_question_observation_conflicts(row, observation, payload, official))
        if (row["role"] == "identity" and observed_role == "identity"
                and observation.get("identity_label") is not None
                and observation["identity_label"] != row.get("identity_label")):
            codes.append("PAGE_COVERAGE_IDENTITY_CONFLICT")
        if not codes:
            continue
        changed.add(index)
        _flag(row, codes)
        # 说明区通常从编辑器隐藏；明确观察到作答或身份内容后不能继续隐藏整个可疑框。
        if row["role"] in {"instruction", "other"} and observed_role in {"answer", "identity", "objective_group"}:
            row["role"] = "uncertain"
    result["observed_conflict_count"] = len(changed)

    # 复用主流水线门禁，但在调用时才导入，避免 main 初始化本模块时相互导入。
    from .template_analysis import (
        _normalize,
        _refine,
        _review_objective_item_conflicts,
        _review_question_association_conflicts,
        _review_region_conflicts,
    )

    validator = Draft202012Validator(model_output_schema([], page["page_no"], include_observations=True))
    claimed_replacements = {identifier for old in existing for identifier in old.get("_extra_replace_candidate_ids", [])}
    for raw in coverage.get("uncovered", []):
        if len(existing) >= MAX_CANDIDATES:
            result["rejected_count"] += 1
            continue
        try:
            schema_row = copy.deepcopy(raw)
            # coverage 已先严格校验供应商粗响应；该标记由展开器补入，不能送进公开契约。
            # 只在校验副本剥离这个可信标记，原件仍交给规范化，其他未知字段继续拒绝。
            if isinstance(schema_row, dict) and schema_row.get("_coarse_layout") is True:
                schema_row.pop("_coarse_layout")
            validator.validate({"candidates": [schema_row]})
            fresh = _normalize([copy.deepcopy(raw)], {}, payload)
            _refine(fresh, frames, page)
            _review_objective_item_conflicts(fresh, payload)
            content_evidence.review(fresh, payload)
        except (AgentError, ValidationError, KeyError, TypeError, ValueError):
            result["rejected_count"] += 1
            continue
        if len(fresh) != 1 or any(old["candidate_id"] == fresh[0]["candidate_id"] for old in existing):
            result["rejected_count"] += 1
            continue
        replacement = []
        candidate = fresh[0]
        if candidate["role"] == "answer" and candidate.get("printed_question_label"):
            same_question = [old for old in existing if _same_answer_question(old, candidate)]
            if same_question:
                # 一题只支持一个作答 ROI。历史待审不等于本轮反证，不能借查漏追加
                # 第二个同题框；只有唯一旧框刚被明确否定时，才将新位置送去原子替换。
                if len(same_question) != 1 or not any(original[index] is same_question[0] for index in changed):
                    result["rejected_count"] += 1
                    continue
                replacement = same_question
        elif candidate.get("identity_label") in {"条码", "二维码"}:
            same_kind = [old for old in existing if old.get("role") == "identity"
                         and old.get("identity_label") == candidate.get("identity_label")]
            if len(same_kind) == 1 and any(original[index] is same_kind[0] for index in changed):
                # 两个真实码可同时存在，不能仅凭类别相同就删除旧框。
                replacement = same_kind
        if any(target["candidate_id"] in claimed_replacements for target in replacement):
            result["rejected_count"] += 1
            continue
        kept = [old for old in existing if all(old is not target for target in replacement)]
        if _duplicates_existing(candidate, kept):
            result["rejected_count"] += 1
            continue
        # 替换目标仍留在原列表，裁图成功后才一起删除；本轮试合并必须先排除它，
        # 否则新旧同题必然制造重复冲突，或被旧错误位置的重叠检查挡住。
        combined = copy.deepcopy([*kept, *fresh])
        _review_objective_item_conflicts(combined, payload)
        _review_question_association_conflicts(combined)
        _review_region_conflicts(combined, page, semantic_completed=True)
        candidate = combined[-1]
        if _degrades_supported(kept, combined[:-1]):
            result["rejected_count"] += 1
            continue
        if (candidate.get("semantic_check") != "supported" and any(
                _visible(old) and regions_overlap(old["proposed_bbox"], candidate["proposed_bbox"],
                                                 page["width"], page["height"]) for old in kept)):
            result["rejected_count"] += 1
            continue
        if _visible(candidate):
            # 新漏区即使通过本地内容证据，也只代表粗边界候选；必须再走一次裁图精定位，
            # 否则查漏阶段会把第二次模型的粗框直接当成最终模板区域。
            _flag(candidate, ["PAGE_COVERAGE_DISCOVERED_REGION"])
        if replacement:
            candidate["_extra_replace_candidate_ids"] = [target["candidate_id"] for target in replacement]
            claimed_replacements.update(candidate["_extra_replace_candidate_ids"])
        existing.append(candidate)
        result["discoveries_added"] += 1
    return result


def _question_observation_conflicts(row, observation, payload, official):
    """只比较本轮明确读到的正式题号，不把空观察或未知编号当作缺题证据。"""
    labels = set(observation["labels"])
    codes = []
    if row["role"] == "answer" and row.get("printed_question_label") in official:
        # 同题连续小问仍属于父题；另有正式独立题号时必须保留多题共框的反证。
        observed = _answer_observed_labels(row, labels, official) & official
        if observed and observed != {row["printed_question_label"]}:
            codes.append("PAGE_COVERAGE_QUESTION_CONFLICT")
    elif row["role"] == "objective_group" and observation["role"] == "objective_group":
        observed = labels & official
        if not observed:
            return codes
        if "objective_question_labels" in payload and observed - set(payload["objective_question_labels"]):
            codes.append("PAGE_COVERAGE_QUESTION_TYPE_CONFLICT")
        mapped = {item["printed_question_label"] for item in row.get("objective_items", [])
                  if item.get("printed_question_label") in official}
        # 末轮漏读部分题号不是反证：已有同来源 OMR 实测映射时保留完整结果。
        # 新增题号、题型冲突及上层明确用途矛盾仍按原规则处理，普通模型映射也不享受此例外。
        omitted_verified_items = observed < mapped and _verified_local_omr_mapping(row, payload)
        if mapped and observed != mapped and not omitted_verified_items:
            codes.append("PAGE_COVERAGE_OBJECTIVE_ITEMS_CONFLICT")
    return codes


def _verified_local_omr_mapping(row, payload):
    page = payload["page"]
    suffix = f":{page['sha256'][:16]}:{page['page_no']}"
    if (row.get("semantic_check") != "supported"
            or "LOCAL_OBJECTIVE_OMR_LAYOUT_VERIFIED" not in row.get("reason_codes", [])
            or not any(isinstance(ref, str) and ref.startswith("local-omr:") and ref.endswith(suffix)
                       for ref in row.get("evidence_refs", []))):
        return False
    # 来源绑定不能替代当前映射检查；被编辑成越界、重叠或正式题型不符后不能继续借历史证据免责。
    return not review_objective_items(row, payload, check_observed=False)


def _flag(row, codes):
    for code in codes:
        _add_reason(row, code)
    row["semantic_check"] = "needs_review"
    row["association_status"] = "ambiguous" if row["role"] == "answer" and row.get("printed_question_label") else "unassociated"
    if "objective_items" in row:
        row["objective_items"] = []


def _add_reason(row, code):
    if code not in row["reason_codes"]:
        row["reason_codes"].append(code)


def _visible(row):
    return row["role"] not in {"instruction", "other"}


def _duplicates_existing(candidate, existing):
    for old in existing:
        if old["candidate_id"] == candidate["candidate_id"] or _same_answer_question(old, candidate):
            return True
        if not _visible(old):
            continue
        first, second = old["proposed_bbox"], candidate["proposed_bbox"]
        width = max(0, min(first["x"] + first["width"], second["x"] + second["width"]) - max(first["x"], second["x"]))
        height = max(0, min(first["y"] + first["height"], second["y"] + second["height"]) - max(first["y"], second["y"]))
        # 包含关系也属于同一个可见区域，不能因为大框与小框 IoU 低就追加一个重叠漏区。
        area = min(first["width"] * first["height"], second["width"] * second["height"])
        if area > 0 and width * height / area >= .5:
            return True
    return False


def _same_answer_question(first, second):
    return (first["role"] == second["role"] == "answer" and bool(first.get("printed_question_label"))
            and first["printed_question_label"] == second.get("printed_question_label"))


def _degrades_supported(before, after):
    return any(old.get("semantic_check") == "supported" and (
        checked.get("semantic_check") != "supported" or checked["role"] != old["role"]
        or checked["association_status"] != old["association_status"])
        for old, checked in zip(before, after))
