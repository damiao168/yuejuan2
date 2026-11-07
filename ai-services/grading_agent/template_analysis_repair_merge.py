"""局部复判的候选替换事务；全部门禁通过后才修改原列表。

本地填空与条码测量提供独立证据，任何失败仍保留旧候选交给教师复核。
"""

import copy

from .answer_sheet_vision import regions_overlap
from .errors import AgentError
from .template_analysis_barcodes import (
    _align_verified_barcode_boundaries,
    _barcode_frame_targets,
    _filter_repair_duplicate_codes,
)
from .template_analysis_candidates import (
    _contains,
    _discovered_id,
    _intersection_ratio,
    _normalize,
    _reason,
    _refine,
    _review_objective_item_conflicts,
    _review_region_conflicts,
)
from .template_analysis_context import filter_repair_context_instructions
from .template_analysis_identity_alignment import (
    IDENTITY_ALIGNMENT_VERSION,
    align_identity_fields,
)
from .template_analysis_option_alignment import (
    OPTION_ALIGNMENT_VERSION,
    align_objective_options,
    merge_objective_groups,
)
from .template_analysis_schema import MAX_CANDIDATES


def _merge_local_fill_regions(existing, local, page):
    _replace_verified_fill_parents(existing, local, page)
    _align_verified_fill_fields(existing, local, page)
    for candidate in local:
        if any(row.get("candidate_id") == candidate.get("candidate_id") for row in existing
               if candidate.get("candidate_id") is not None):
            continue
        if len(existing) >= MAX_CANDIDATES:
            break
        same_label = [row for row in existing if row["role"] == "answer"
                      and row["printed_question_label"] == candidate["printed_question_label"]]
        if any(row.get("semantic_check") == "supported" for row in same_label):
            continue
        for row in existing:
            # 与最终区域冲突检查保持一致：说明区不占作答空间，局部补框不能撤回已核实的禁区排除。
            if row["role"] in {"instruction", "other"}:
                continue
            if not regions_overlap(row["proposed_bbox"], candidate["proposed_bbox"], page["width"], page["height"]):
                continue
            # 模型可能只返回“填空题+禁区”的大外框。保留这条未知建议并说明包含子区，
            # 不让大外框吞掉有独立标题/题号/横线证据的局部建议，也不静默删除旧证据。
            if row["role"] == "uncertain" and _contains(row["proposed_bbox"], candidate["proposed_bbox"]):
                _reason(row, "LOCAL_FILL_SUBREGIONS_DETECTED")
                _reason(candidate, "LOCAL_FILL_PARENT_REGION_REQUIRES_REVIEW")
            else:
                _reason(row, "REGION_CONFLICT")
                _reason(candidate, "REGION_CONFLICT")
                row["semantic_check"] = "needs_review"
                if row["association_status"] == "associated":
                    row["association_status"] = "ambiguous"
        existing.append(candidate)


def _align_verified_fill_fields(existing, local, page):
    required = {"LOCAL_FILL_HEADER_VERIFIED", "LOCAL_FILL_PRINTED_MARKER_VERIFIED", "LOCAL_FILL_UNDERLINE_VERIFIED"}
    for measured in local:
        if (measured.get("origin") != "local_detector" or measured.get("role") != "answer"
                or not required <= set(measured.get("reason_codes", []))):
            continue
        matches = [row for row in existing if row.get("role") == "answer"
                   and row.get("printed_question_label") == measured.get("printed_question_label")]
        if (len(matches) != 1 or matches[0].get("origin") != "model"
                or matches[0].get("semantic_check") != "supported"):
            continue
        row = matches[0]
        old, box = row["proposed_bbox"], measured["proposed_bbox"]
        overlap = max(0, min(old["x"] + old["width"], box["x"] + box["width"]) - max(old["x"], box["x"]))
        gap = abs(old["y"] + old["height"] / 2 - box["y"] - box["height"] / 2)
        if overlap / min(old["width"], box["width"]) < .5 or gap > max(old["height"], box["height"]) * 2:
            continue
        if any(other is not row and other.get("role") not in {"instruction", "other"}
               and regions_overlap(other["proposed_bbox"], box, page["width"], page["height"]) for other in existing):
            continue
        # 模型题号已正确仍可能把单题粗框画在横线下方；同栏唯一的静态题号与实测横线
        # 应校准该框，不能因它被标为 supported 就跳过测量，留下查漏阶段再造同题框。
        row["proposed_bbox"] = copy.deepcopy(box)
        evidence = row.get("visual_evidence", {})
        if evidence.get("anchor_bbox") and not _contains(box, evidence["anchor_bbox"]):
            evidence["anchor_bbox"] = None
        if evidence:
            evidence["boundary_basis"] = "printed_answer_line"
        row["evidence_refs"] = list(dict.fromkeys([*row.get("evidence_refs", []), *measured.get("evidence_refs", [])]))
        _reason(row, "LOCAL_FILL_LAYOUT_VERIFIED")


def _replace_verified_fill_parents(existing, local, page):
    """独立检测完整覆盖模型合题观察后原子替换；失败不删旧框、不影响人工或可用区域。"""
    required = {"LOCAL_FILL_HEADER_VERIFIED", "LOCAL_FILL_PRINTED_MARKER_VERIFIED", "LOCAL_FILL_UNDERLINE_VERIFIED"}
    for parent in tuple(existing):
        labels = set(parent.get("_fill_parent_labels", []))
        if (len(labels) < 2 or parent.get("origin") != "model"
                or parent.get("semantic_check") != "needs_review"):
            continue
        fresh = [row for row in local if row.get("printed_question_label") in labels]
        if (len(fresh) != len(labels) or {row.get("printed_question_label") for row in fresh} != labels
                or any(row.get("origin") != "local_detector" or row.get("role") != "answer"
                       or not required <= set(row.get("reason_codes", [])) for row in fresh)):
            continue
        # 新框须来自同一实际位置；题号巧合不能撤下另一栏的父框。
        union = {"x": min(row["proposed_bbox"]["x"] for row in fresh),
                 "y": min(row["proposed_bbox"]["y"] for row in fresh)}
        union["width"] = max(row["proposed_bbox"]["x"] + row["proposed_bbox"]["width"] for row in fresh) - union["x"]
        union["height"] = max(row["proposed_bbox"]["y"] + row["proposed_bbox"]["height"] for row in fresh) - union["y"]
        if _intersection_ratio(union, parent["proposed_bbox"]) < .5:
            continue
        # 外接并集可能跨空白包住父框；每个子题还须落在父框所在栏。
        # 粗父框可能漏右侧留白，横向重叠按较窄框计，至少覆盖四分之一。
        parent_box = parent["proposed_bbox"]
        if any(max(0, min(row["proposed_bbox"]["x"] + row["proposed_bbox"]["width"],
                          parent_box["x"] + parent_box["width"]) - max(row["proposed_bbox"]["x"], parent_box["x"]))
               / min(row["proposed_bbox"]["width"], parent_box["width"]) < .25 for row in fresh):
            continue
        kept = [row for row in existing if row is not parent]
        if len(kept) + len(fresh) > MAX_CANDIDATES:
            continue
        if any(regions_overlap(first["proposed_bbox"], second["proposed_bbox"], page["width"], page["height"])
               for index, first in enumerate(fresh) for second in fresh[index + 1:]):
            continue
        if any(old.get("role") not in {"instruction", "other"} and (
                old.get("printed_question_label") in labels or any(
                    regions_overlap(old["proposed_bbox"], row["proposed_bbox"], page["width"], page["height"]) for row in fresh))
               for old in kept):
            continue
        accepted = copy.deepcopy(fresh)
        for row in accepted:
            row.update(semantic_check="supported", association_status="associated")
            # 独立证据与模型观察已在此汇合；待核对只是上一阶段状态，
            # 不应在已自动对应的界面继续提示教师重复确认。
            row["reason_codes"] = [code for code in row["reason_codes"] if code != "LOCAL_FILL_REQUIRES_REVIEW"]
            _reason(row, "LOCAL_FILL_LAYOUT_VERIFIED")
        existing[:] = [*kept, *accepted]


def _apply_repair_batches(existing, batches, payload, frames, page, content_evidence, *, barcode_hints=(), deadline=None, identity_aligner=None):
    """局部结果经过同一语义和全局几何门禁后原子替换；空结果或新冲突不能抹去原来的问题。"""
    applications = []
    for batch in batches:
        application = {"task": batch["task"], "applied": False}
        applications.append(application)
        targets = set(batch["replace_candidate_ids"])
        if not targets or not targets <= {row["candidate_id"] for row in existing}:
            application["reason"] = "REPAIR_TARGET_CHANGED"
            continue
        try:
            raw = copy.deepcopy(batch["candidates"])
            image = getattr(content_evidence, "image", None)
            token_reader = getattr(content_evidence, "static_tokens", None)
            # 模型仍可能按单选、多选标题拆框。只在全部实际选项、独立题号及连续印刷
            # 外框共同证明同一大区时合并；跨其他用途或任何语义反证都保留原分组继续校验。
            raw, objective_merge = merge_objective_groups(
                raw, image, payload,
                tokens=token_reader() if callable(token_reader) and sum(
                    row.get("role") == "objective_group" for row in raw) > 1 else (),
                measurement_bbox=batch.get("crop_bbox"), frames=frames,
                protected_regions=[row for row in existing if row["candidate_id"] not in targets])
            if objective_merge["merged"]:
                application["objective_merge"] = {key: objective_merge[key] for key in (
                    "input_groups", "matched_items", "verified_anchors")}
            aligned = {}
            for candidate in raw:
                requires_alignment = candidate.pop("_requires_option_alignment", False) is True
                if candidate.get("role") != "objective_group" or not candidate.get("objective_items"):
                    if requires_alignment:
                        raise AgentError("model_output_invalid", "deferred options require objective alignment", status=502)
                    continue
                # 模型保留题号语义，原图像素负责选项位置。只有完整唯一匹配且独立题号
                # 锚点一致才校准；供应商原响应已留在受控诊断中，失败仍走原几何门禁。
                measured = {"aligned": False}
                if image is not None and callable(token_reader):
                    measured = align_objective_options(candidate, image, payload, tokens=token_reader(),
                                                       measurement_bbox=batch.get("crop_bbox"), frames=frames)
                if measured["aligned"]:
                    aligned[_discovered_id(candidate, payload)] = measured
                elif requires_alignment:
                    # 裁图外的模型选项只是待替换的测量建议；独立像素校准不成功就拒绝整批，
                    # 不能让还原到整页后看似合法的坐标绕过原裁图边界。
                    raise AgentError("model_output_invalid", "deferred option alignment failed", status=502)
            fresh = _normalize(raw, {}, payload)
            for candidate in fresh:
                if objective_merge["merged"] and candidate["role"] == "objective_group":
                    _reason(candidate, "LOCAL_OBJECTIVE_GROUPS_MEASURED_MERGED")
                if candidate["candidate_id"] in aligned:
                    _reason(candidate, "LOCAL_OBJECTIVE_OPTION_PIXELS_ALIGNED")
                    candidate["evidence_refs"].append(
                        f"local-options:{OPTION_ALIGNMENT_VERSION}:{page['sha256'][:16]}:{page['page_no']}")
            if aligned:
                application["option_alignment"] = {
                    "matched_items": sum(item["matched_items"] for item in aligned.values()),
                    "verified_anchors": sum(item["verified_anchors"] for item in aligned.values())}
            _align_verified_barcode_boundaries(fresh, [row for row in existing if row["candidate_id"] in targets])
            _refine(fresh, frames, page)
            # 身份栏可与条码一起复判，替换目标未必是独立条码提示。按本页实测码图
            # 匹配外框，避免正确分类的小码框因普通修复路径而丢掉外虚线框定位。
            _align_verified_barcode_boundaries(fresh, _barcode_frame_targets(barcode_hints))
            _review_objective_item_conflicts(fresh, payload)
            if any(row.get("role") == "identity" and row.get("identity_label") in {"姓名", "学号", "准考证号", "考号"}
                   for row in fresh):
                # 标签类别正确仍可能只框到标题、漏掉下方填写横线或编号格。独立静态标签与
                # 完整实测结构唯一对应时才校准，有限补读共用本次分析时限，不按模板位置猜框。
                token_reader = getattr(content_evidence, "static_tokens", None)
                identity_alignment = (identity_aligner or align_identity_fields)(
                    fresh, getattr(content_evidence, "image", None), page,
                    tokens=token_reader() if callable(token_reader) else (),
                    measurement_bbox=batch.get("crop_bbox"), deadline=deadline)
                for index in identity_alignment["aligned_indices"]:
                    _reason(fresh[index], "LOCAL_IDENTITY_FIELD_STRUCTURE_ALIGNED")
                    fresh[index]["evidence_refs"].append(
                        f"local-identity:{IDENTITY_ALIGNMENT_VERSION}:{page['sha256'][:16]}:{page['page_no']}")
                if identity_alignment["aligned_indices"]:
                    application["identity_alignment"] = {"aligned_fields": len(identity_alignment["aligned_indices"])}
            content_evidence.review(fresh, payload)
            reconciled = _reconcile_fill_repair_children(existing, fresh, targets, payload, batch)
            if reconciled:
                targets.update(reconciled)
                application["fill_reconciliation"] = {"preserved_local_fields": len(reconciled)}
            # 裁图边缘可能重复带入已有说明的一小段。只有旧说明与完整实测框共同
            # 证实它是裁边片段才去重，避免末轮把重复片段当成新的用途冲突。
            kept = [row for row in existing if row["candidate_id"] not in targets]
            fresh, context = filter_repair_context_instructions(
                fresh, kept, frames, page, batch.get("crop_bbox"), image=getattr(content_evidence, "image", None))
            if context["removed_count"]:
                application["context_filter"] = context
            # 身份裁图可能带回已独立核验、未列入替换目标的条码。只有两行都唯一对应
            # 同一次实测外框和码图锚点时才去掉新行；保留行及人工修改始终不被覆盖。
            fresh, duplicate_codes = _filter_repair_duplicate_codes(fresh, kept)
            if duplicate_codes:
                application["duplicate_code_context"] = {"removed_count": duplicate_codes}
        except AgentError:
            application["reason"] = "REPAIR_RESULT_INVALID"
            continue
        # 只返回警示牌不能证明混合栏的填空已经找全；必须包含可用的实际字段或作答区。
        visible = [row for row in fresh if row["role"] not in {"instruction", "other"}]
        if not visible or any(row.get("semantic_check") != "supported" for row in visible):
            application["reason"] = "REPAIR_CONTENT_UNRESOLVED"
            application["content_issues"] = list(dict.fromkeys(
                code for row in visible for code in row["reason_codes"] if code.startswith(("MODEL_OBS_", "SEMANTIC_"))))[:12]
            continue
        if not set(batch.get("required_answer_labels", [])).issubset({
                row.get("printed_question_label") for row in visible if row["role"] == "answer"}):
            # 为纠正题框内的假身份栏而扩大裁图时，可靠整题也属于同一事务；
            # 新响应漏掉该题不能只留下身份字段或说明，须完整保留旧结果等待下一次识别。
            application["reason"] = "REPAIR_REQUIRED_ANSWER_MISSING"
            continue
        if {row["candidate_id"] for row in kept} & {row["candidate_id"] for row in fresh}:
            application["reason"] = "REPAIR_DUPLICATES_EXISTING_REGION"
            continue
        # 原布局自身可能已有冲突（如填空共框与下一题轻微重叠）。只拒绝本批新增的伤害，
        # 不能让另一栏既有问题阻挡无关身份区域的正确修复；旧冲突仍由最终全局校验保留。
        baseline = copy.deepcopy(existing)
        _review_objective_item_conflicts(baseline, payload)
        _review_region_conflicts(baseline, page, semantic_completed=True)
        _review_question_association_conflicts(baseline)
        baseline_by_id = {row["candidate_id"]: row for row in baseline}
        combined = copy.deepcopy([*kept, *fresh])
        _review_objective_item_conflicts(combined, payload)
        _review_region_conflicts(combined, page, semantic_completed=True)
        # 空间重验会重新评估单行内容；同题唯一性必须最后执行，不能被内容一致重新覆盖为可用。
        _review_question_association_conflicts(combined)
        checked = combined[len(kept):]
        if any(row.get("semantic_check") == "needs_review" for row in checked
               if row["role"] not in {"instruction", "other"}):
            application["reason"] = "REPAIR_COMBINED_LAYOUT_CONFLICT"
            continue
        # 新结果也可能让保留区出现歧义；检查整个合并结果，不能只看新增行后丢掉旧行的降级。
        if any(before.get("semantic_check") == "supported" and (
                after.get("semantic_check") != "supported"
                or after.get("association_status") != before.get("association_status"))
               for before, after in ((baseline_by_id[old["candidate_id"]], checked_old)
                                     for old, checked_old in zip(kept, combined[:len(kept)]))):
            application["reason"] = "REPAIR_INVALIDATES_EXISTING_REGION"
            continue
        for row in checked:
            _reason(row, "LOCAL_MODEL_REPAIR_APPLIED")
        existing[:] = [*kept, *checked]
        application.update(applied=True, candidate_count=len(checked))
    return applications


def _reconcile_fill_repair_children(existing, fresh, targets, payload, batch):
    """完整父框复判保留同位置的实测子题，不让旧局部候选挡住缺失子题的恢复。"""
    parents = [row for row in existing if row["candidate_id"] in targets and row.get("origin") == "model"
               and row.get("semantic_check") == "needs_review" and len(row.get("_fill_parent_labels", [])) >= 2]
    if len(parents) != 1 or not isinstance(batch.get("crop_bbox"), dict):
        return set()
    parent = parents[0]
    labels = set(parent["_fill_parent_labels"])
    answers = [row for row in fresh if row["role"] == "answer"]
    if (not labels <= set(payload.get("fill_blank_question_labels", [])) or len(answers) != len(labels)
            or {row.get("printed_question_label") for row in answers} != labels
            or any(row.get("semantic_check") != "supported" or row.get("association_status") != "associated"
                   for row in answers)):
        return set()
    required = {"LOCAL_FILL_HEADER_VERIFIED", "LOCAL_FILL_PRINTED_MARKER_VERIFIED", "LOCAL_FILL_UNDERLINE_VERIFIED"}
    replacements = []
    for answer in answers:
        matching = [row for row in existing if row["candidate_id"] not in targets
                    and row.get("printed_question_label") == answer["printed_question_label"]
                    and row.get("origin") == "local_detector" and row.get("role") == "answer"
                    and required <= set(row.get("reason_codes", []))
                    and _contains(parent["proposed_bbox"], row["proposed_bbox"])
                    and _contains(batch["crop_bbox"], row["proposed_bbox"])
                    and _intersection_ratio(row["proposed_bbox"], answer["proposed_bbox"]) >= .5]
        if len(matching) > 1:
            return set()
        if matching:
            replacements.append((answer, matching[0]))
    # 只收纳本批完整重读所对应的局部子题；人工题框和其他位置的同号框仍交给原冲突门禁。
    # 同题实测位置比模型粗框精确，保留它后再合并，避免把已有12/13和新14判为重复布局。
    for answer, measured in replacements:
        answer["proposed_bbox"] = copy.deepcopy(measured["proposed_bbox"])
        answer["evidence_refs"] = list(dict.fromkeys([*answer["evidence_refs"], *measured.get("evidence_refs", [])]))
        evidence = answer.get("visual_evidence", {})
        if evidence.get("anchor_bbox") and not _contains(answer["proposed_bbox"], evidence["anchor_bbox"]):
            evidence["anchor_bbox"] = None
        if evidence:
            evidence["boundary_basis"] = "printed_answer_line"
        _reason(answer, "LOCAL_FILL_LAYOUT_VERIFIED")
    return {row["candidate_id"] for _, row in replacements}


def _review_question_association_conflicts(rows):
    # 当前模板一题只支持一个 ROI；局部替换后仍须检查跨批次同题框，不能静默选一个。
    labels = {}
    for row in rows:
        if row["role"] in {"answer", "uncertain"} and row.get("printed_question_label") is not None:
            labels.setdefault(row["printed_question_label"], []).append(row)
    for matching in labels.values():
        if len(matching) > 1:
            for row in matching:
                _reason(row, "MULTIPLE_REGIONS_FOR_QUESTION")
                row.update(association_status="ambiguous", semantic_check="needs_review")
