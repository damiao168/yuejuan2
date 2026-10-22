"""人工大框内的选择题网格发现；只输出待核对几何，不调用模型或发布模板。"""

import base64
import hashlib
import json
import math

from jsonschema import Draft202012Validator
from jsonschema.exceptions import ValidationError

from .answer_sheet_vision import VisionInputError, decode_display_page
from .choice_grid_detection import detect_option_groups
from .choice_grid_schema import (
    ALGORITHM_VERSION,
    MAX_PAGE_BYTES,
    MAX_PAGE_PIXELS,
    SCHEMA_VERSION,
    request_schema,
)
from .errors import AgentError


class ObjectiveGridAnalyzer:
    def analyze(self, payload):
        """坐标始终相对于来源显示页；题号及选项标签必须由调用方展示并人工核对。"""
        request_id = payload.get("request_id", "") if isinstance(payload, dict) else ""
        try:
            Draft202012Validator(request_schema()).validate(payload)
            page, block = payload["page"], payload["block_bbox"]
            if (page["width"] * page["height"] > MAX_PAGE_PIXELS
                    or any(not math.isfinite(value) for value in block.values())
                    or block["x"] + block["width"] > 1 + 1e-9 or block["y"] + block["height"] > 1 + 1e-9):
                raise ValueError("invalid page bounds")
            raw = base64.b64decode(page["data_base64"], validate=True)
            if not raw or len(raw) > MAX_PAGE_BYTES or hashlib.sha256(raw).hexdigest() != page["sha256"]:
                raise ValueError("invalid page bytes")
            image = decode_display_page(raw, page)
        except (ValidationError, ValueError, VisionInputError) as exc:
            # 不返回校验器原文，其中可能带入页面字节；不接受模型凭据或任意扩展输入字段。
            raise AgentError("invalid_request", "objective grid image or geometry is invalid", status=400,
                             request_id=request_id) from exc
        height, width = image.shape[:2]
        left, top = math.ceil(block["x"] * width), math.ceil(block["y"] * height)
        right = min(width, math.floor((block["x"] + block["width"]) * width))
        bottom = min(height, math.floor((block["y"] + block["height"]) * height))
        if right <= left or bottom <= top:
            raise AgentError("invalid_request", "objective grid block has no image pixels", status=400, request_id=request_id)
        groups, reasons = detect_option_groups(image[top:bottom, left:right], payload["option_count"], payload["option_orientation"])
        output = []
        for cells in groups:
            options = [{"bbox": _page_bbox(cell, left, top, width, height)} for cell in cells]
            # 题区只包裹实测小格，额外留半个格高以内的边缘供人调整，不能扩出人工指定的大框。
            padding = max(2, round(min(cell[3] for cell in cells) * .35))
            x = max(0, min(cell[0] for cell in cells) - padding)
            y = max(0, min(cell[1] for cell in cells) - padding)
            edge_x = min(right - left, max(cell[0] + cell[2] for cell in cells) + padding)
            edge_y = min(bottom - top, max(cell[1] + cell[3] for cell in cells) + padding)
            bbox = _page_bbox((x, y, edge_x - x, edge_y - y), left, top, width, height)
            fingerprint = json.dumps({"page": page["sha256"], "page_no": page["page_no"], "options": options}, sort_keys=True)
            group_id = "grid-" + hashlib.sha256(fingerprint.encode()).hexdigest()[:24]
            output.append({"group_id": group_id, "bbox": bbox, "options": options,
                           "reason_codes": ["GEOMETRY_ONLY_REQUIRES_REVIEW"]})
        return {
            "request_id": request_id, "schema_version": SCHEMA_VERSION, "status": "review_required",
            "coordinate_space": "source_display_normalized", "page_no": page["page_no"],
            "source_sha256": page["sha256"], "block_bbox": dict(block), "groups": output,
            "reason_codes": reasons, "versions": {"algorithm": ALGORITHM_VERSION},
        }


def _page_bbox(cell, left, top, width, height):
    return {"x": (left + cell[0]) / width, "y": (top + cell[1]) / height,
            "width": cell[2] / width, "height": cell[3] / height}
