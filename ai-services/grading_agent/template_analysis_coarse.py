"""整页阶段只发现用途和粗边界，密集选项坐标留给局部裁图。

这里是内部模型契约；服务端补齐来源、页号及关联字段后仍走既有语义校验，
不改变公开候选结构，也不把整场题号清单当成本页内容清单。
"""

import copy

from .template_analysis_schema import (
    IDENTITY_LABELS,
    MAX_CANDIDATES,
    MAX_OBSERVED_QUESTION_LABELS,
    OBSERVATION_BARCODE_PATTERNS,
    OBSERVATION_BOUNDARY_BASES,
    OBSERVATION_IDENTITY_PATTERNS,
    OBSERVATION_OPTION_PATTERNS,
    OBSERVATION_QUESTION_LABEL_PATTERN,
    OBSERVATION_STATIC_LABELS,
    ROLES,
    bbox_schema,
)

COARSE_CONTRACT_VERSION = "answer-sheet-coarse-layout-v1"
_MAX_GEOMETRY_HINTS = 64


def _object(properties):
    return {"type": "object", "additionalProperties": False, "required": list(properties),
            "properties": properties}


def coarse_model_output_schema():
    """粗阶段不包含逐题选项坐标，十个字段仅记录本页可见用途、题号与边界观察。"""
    return _object({"candidates": {"type": "array", "maxItems": MAX_CANDIDATES, "items": _object({
        "role": {"enum": ROLES},
        "bbox": bbox_schema(),
        "labels": {"type": "array", "maxItems": MAX_OBSERVED_QUESTION_LABELS, "uniqueItems": True,
                   "items": {"type": "string", "maxLength": 7, "pattern": OBSERVATION_QUESTION_LABEL_PATTERN}},
        "identity_label": {"anyOf": [{"enum": IDENTITY_LABELS}, {"type": "null"}]},
        "static_label": {"anyOf": [{"enum": OBSERVATION_STATIC_LABELS}, {"type": "null"}]},
        "option_pattern": {"enum": OBSERVATION_OPTION_PATTERNS},
        "identity_pattern": {"enum": OBSERVATION_IDENTITY_PATTERNS},
        "barcode_pattern": {"enum": OBSERVATION_BARCODE_PATTERNS},
        "boundary_basis": {"enum": OBSERVATION_BOUNDARY_BASES},
        # 锚点越界仍交给候选级语义校验，不能让一个观察问题使整页其余区域丢失。
        "anchor_bbox": {"anyOf": [_object({key: {"type": "number"}
                                             for key in ("x", "y", "width", "height")}), {"type": "null"}]},
    })}})


def expand_coarse_output(output, payload):
    """将已按粗阶段 schema 校验的响应扩成既有原始候选；不代表内容已通过语义校验。

    只有 answer 的单一实际观察题号属于正式清单时才提出关联；选择题型矛盾、
    多题共框和自报观察矛盾仍由后续 _normalize 及内容层处理，不能在这里擦去反证。
    """
    official = set(payload["question_labels"])
    page = payload["page"]
    source = f"source-page:{page['sha256']}:{page['page_no']}"
    candidates = []
    for supplied in output["candidates"]:
        row = copy.deepcopy(supplied)
        labels = row["labels"]
        question = labels[0] if row["role"] == "answer" and len(labels) == 1 and labels[0] in official else None
        candidates.append({"candidate_id": None, "page_no": page["page_no"], "role": row["role"],
                           "proposed_bbox": row["bbox"], "printed_question_label": question,
                           "identity_label": row["identity_label"],
                           "association_status": "associated" if question else "unassociated",
                           "evidence_refs": [source], "reason_codes": [], "objective_items": [],
                           "observed_question_labels": labels,
                           "visual_evidence": {key: row[key] for key in (
                               "static_label", "option_pattern", "identity_pattern", "barcode_pattern",
                               "boundary_basis", "anchor_bbox")},
                           # 粗阶段刻意不返回逐题选项框；下游不能把空列表误当成模型漏识别。
                           "_coarse_layout": True})
    return {"candidates": candidates}


def coarse_message_payload(payload):
    """只传页面编号、写入状态和少量几何提示，不把考试题号清单喂给模型。

    首轮只负责发现本页看得见的版面；整场题号和题型白名单由服务端在模型返回后
    做一致性校验。把清单放进视觉请求即使附带“不能补题”的文字，也会让模型把
    清单误当作本页待完成的识别任务，形成先验污染。保留几何提示仅用于定位已有
    画框附近的像素，提示框外仍必须扫描整页。
    """
    manifest = {"task": "discover_visible_page_layout", "page_no": payload["page"]["page_no"],
                "question_labels_scope": "server_side_only_not_provided",
                "writing_state": payload["writing_state"], "geometry_hints": []}
    seen = set()
    for candidate in payload["candidates"]:
        box = candidate["bbox"]
        fingerprint = tuple(box[key] for key in ("x", "y", "width", "height"))
        if fingerprint in seen:
            continue
        seen.add(fingerprint)
        manifest["geometry_hints"].append(copy.deepcopy(box))
        if len(manifest["geometry_hints"]) >= _MAX_GEOMETRY_HINTS:
            break
    return manifest
