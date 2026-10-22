"""从实测分隔线提供填空共框批阅建议，逐题位置仍是独立的切题事实。"""

import hashlib

from .template_analysis_fill_regions import _measured_sections

FILL_GROUP_VERSION = "printed-fill-grading-section-v2"
_FORBIDDEN_REASONS = {"CONTENT_FORBIDDEN_WRITING", "SEMANTIC_FORBIDDEN_AREA_DETECTED",
                      "LOCAL_FORBIDDEN_LABEL_VERIFIED", "MULTIMODAL_FORBIDDEN_REGION_EXCLUDED"}


def discover_fill_grading_groups(rows, image, frames, payload):
    """仅完整包含至少两道已对应填空题的真实印刷分区可作为共框；不按题数均分。"""
    allowed = set(payload.get("fill_blank_question_labels", []))
    fills = [row for row in rows if row.get("role") == "answer" and row.get("semantic_check") == "supported"
             and row.get("association_status") == "associated" and row.get("printed_question_label") in allowed]
    if len(fills) < 2:
        return []
    height, width = image.shape[:2]
    sections, lines = _measured_sections(image, frames)
    proposals = []
    for left, top, right, bottom in sections:
        interior = {"x": left / width, "y": top / height, "width": (right - left) / width,
                    "height": (bottom - top) / height}
        box = _printed_boundary_box((left, top, right, bottom), lines, width, height)
        members = [row for row in fills if _contains(box, row["proposed_bbox"], 1e-6, 1e-6)]
        labels = [row["printed_question_label"] for row in members]
        if len(members) < 2 or len(set(labels)) != len(labels):
            continue
        # 共用印刷分隔线不代表内容冲突；只拦截进入分区内部的区域，禁区即便标为 other 也保留反证。
        if any(row not in members and (row.get("role") != "other" or
               _FORBIDDEN_REASONS.intersection(row.get("reason_codes", []))) and
               _overlaps(interior, row["proposed_bbox"]) for row in rows):
            continue
        fingerprint = f"{FILL_GROUP_VERSION}:{payload['page']['sha256']}:{payload['page']['page_no']}:{sorted(labels)}:{box}"
        proposals.append({"group_id": "fill-group-" + hashlib.sha256(fingerprint.encode()).hexdigest()[:20],
                          "question_labels": labels, "bbox": box,
                          "reason_codes": ["LOCAL_FILL_SECTION_VERIFIED"]})
    # 一个子题落入多种印刷分区时不任选；不清晰的共框不影响已确定的逐题位置。
    return [group for group in proposals if not any(other is not group and
            set(group["question_labels"]) & set(other["question_labels"]) for other in proposals)]


def _printed_boundary_box(section, lines, width, height):
    left, top, right, bottom = section
    span = right - left
    separators = [(y, y + h) for x, y, w, h in lines
                  if max(0, min(right, x + w) - max(left, x)) / span >= .85]
    # 分区测量返回上下线带之间的内沿；外框只包含实际测到的线宽，不能按子题ROI并集扩到禁区。
    upper = [edge for edge in separators if abs(edge[1] - top) <= 3 and edge[0] <= top]
    lower = [edge for edge in separators if abs(edge[0] - bottom) <= 3 and edge[1] >= bottom]
    outer_top = min(upper, key=lambda edge: abs(edge[1] - top))[0] if upper else top
    outer_bottom = min(lower, key=lambda edge: abs(edge[0] - bottom))[1] if lower else bottom
    return {"x": left / width, "y": outer_top / height, "width": span / width,
            "height": (outer_bottom - outer_top) / height}


def _contains(outer, inner, dx, dy):
    return (inner["x"] >= outer["x"] - dx and inner["y"] >= outer["y"] - dy
            and inner["x"] + inner["width"] <= outer["x"] + outer["width"] + dx
            and inner["y"] + inner["height"] <= outer["y"] + outer["height"] + dy)


def _overlaps(first, second):
    return (min(first["x"] + first["width"], second["x"] + second["width"]) - max(first["x"], second["x"]) > 1e-6
            and min(first["y"] + first["height"], second["y"] + second["height"]) - max(first["y"], second["y"]) > 1e-6)
