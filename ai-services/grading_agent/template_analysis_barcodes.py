"""把独立条码测量线索与模型候选对齐，保留冲突和未核验提示。"""

import copy

from .template_analysis_candidates import _intersection_ratio, _reason
from .template_analysis_schema import MAX_CANDIDATES


def _barcode_repair_hints(hints, existing, page):
    rows = []
    for hint in hints:
        if len(existing) + len(rows) >= MAX_CANDIDATES:
            break
        box = hint["bbox"]
        if any(row.get("role") == "identity" and row.get("identity_label") == hint["identity_label"]
               and row.get("semantic_check") == "supported" and _intersection_ratio(box, row["proposed_bbox"]) > .5
               for row in existing):
            continue
        different_codes = [row for row in existing if row.get("role") == "identity"
                           and row.get("identity_label") in {"条码", "二维码"}
                           and row["identity_label"] != hint["identity_label"]
                           and hint.get("anchor_bbox")
                           and _intersection_ratio(hint["anchor_bbox"], row["proposed_bbox"]) > .5]
        for row in different_codes:
            _reason(row, "LOCAL_CODE_CATEGORY_CONFLICT")
            row.update(role="uncertain", identity_label=None, association_status="unassociated", semantic_check="needs_review")
        extras = []
        if len(different_codes) == 1:
            # 同一个实测码图的旧类别和补漏提示须同批替换，避免两个码框共存；失败仍保留旧问题。
            different_codes[0]["_repair_deferred_to_hint"] = True
            extras = [different_codes[0]["candidate_id"]]
        rows.append({"candidate_id": hint["candidate_id"], "page_no": page["page_no"], "role": "uncertain",
                     "identity_label": None, "printed_question_label": None, "association_status": "unassociated",
                     "origin": "local_detector", "semantic_check": "needs_review", "source_bbox": box,
                     "proposed_bbox": box, "evidence_refs": hint["evidence_refs"], "objective_items": [],
                     "reason_codes": [*hint["reason_codes"], "BARCODE_DISCOVERY_CANDIDATE"],
                     "_barcode_discovery_hint": True,
                     "_barcode_boundary_kind": hint.get("boundary_kind"),
                     "_barcode_anchor_bbox": hint.get("anchor_bbox"),
                     "_barcode_identity_label": hint["identity_label"],
                     "_extra_replace_candidate_ids": extras})
    return rows


def _align_verified_barcode_boundaries(fresh, targets):
    # 先由裁图确认用途，再用独立实测四边定位完整贴码区。无闭合外框时，裁图范围不能变成最终框。
    for row in fresh:
        if row["role"] != "identity" or row.get("identity_label") not in {"条码", "二维码"}:
            continue
        matches = [target for target in targets if target.get("_barcode_boundary_kind") == "printed_frame"
                   and target.get("_barcode_identity_label") == row["identity_label"]
                   and target.get("_barcode_anchor_bbox")
                   and _intersection_ratio(target["_barcode_anchor_bbox"], row["proposed_bbox"]) >= .5]
        if len(matches) == 1:
            target = matches[0]
            row["proposed_bbox"] = copy.deepcopy(target["proposed_bbox"])
            row["evidence_refs"] = list(dict.fromkeys([*row["evidence_refs"], *target["evidence_refs"]]))
            # 同一个外框可能含多个真实码，去重身份必须同时包含独立码图锚点。
            # 此内部凭据只由本地测量产生；模型不能通过自报相同标签或原因码伪造它。
            row["_barcode_frame_key"] = (
                row["identity_label"],
                tuple(target["proposed_bbox"][key] for key in ("x", "y", "width", "height")),
                tuple(target["_barcode_anchor_bbox"][key] for key in ("x", "y", "width", "height")),
            )
            _reason(row, "LOCAL_BARCODE_OUTER_FRAME_LOCALIZED")


def _filter_repair_duplicate_codes(fresh, kept):
    """只删除已保留实测码区的重复新建议；矛盾、缺测量或多义结果仍交给原门禁。"""
    result, removed = [], 0
    for row in fresh:
        key = row.get("_barcode_frame_key")
        owners = [old for old in kept if key and old.get("_barcode_frame_key") == key
                  and old.get("role") == "identity" and old.get("semantic_check") == "supported"
                  and old.get("identity_label") == row.get("identity_label")
                  and old.get("proposed_bbox") == row.get("proposed_bbox")]
        if (row.get("origin") == "model" and row.get("role") == "identity"
                and row.get("identity_label") in {"条码", "二维码"}
                and row.get("semantic_check") == "supported" and len(owners) == 1):
            removed += 1
        else:
            result.append(row)
    return result, removed


def _align_existing_discovered_barcodes(hints, existing):
    aligned = []
    targets = _barcode_frame_targets(hints)
    for row in existing:
        if row.get("semantic_check") != "supported":
            continue
        before = copy.deepcopy(row["proposed_bbox"])
        _align_verified_barcode_boundaries([row], targets)
        if row["proposed_bbox"] != before:
            aligned.append(row)
    # 已分类条码也可能只圈了内部条纹；先改实测边界再去重，否则小框会挡住外贴码框的补漏。
    return aligned


def _barcode_frame_targets(hints):
    return [{"_barcode_boundary_kind": hint.get("boundary_kind"),
             "_barcode_identity_label": hint["identity_label"],
             "_barcode_anchor_bbox": hint.get("anchor_bbox"),
             "proposed_bbox": hint["bbox"], "evidence_refs": hint["evidence_refs"]} for hint in hints]
