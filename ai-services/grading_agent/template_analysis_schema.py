"""答题卡分析独立于试卷解析和评分；所有坐标均为来源显示页的比例坐标。"""

SCHEMA_VERSION = "answer-sheet-analysis-v1"
PROMPT_VERSION = "answer-sheet-full-page-layout-v6"
ALGORITHM_VERSION = "multimodal-layout-with-content-evidence-v6"
MAX_PAGE_BYTES = 8 * 1024 * 1024
MAX_CANDIDATES = 256
ROLES = ["answer", "objective_group", "identity", "instruction", "other", "uncertain"]
ASSOCIATION_STATUSES = ["associated", "unassociated", "ambiguous"]
IDENTITY_LABELS = ["姓名", "学号", "准考证号", "考号", "班级", "学校", "条码", "二维码", "身份信息"]
OBSERVATION_STATIC_LABELS = [
    "姓名", "学号", "准考证号", "考号", "班级", "学校", "条码", "二维码",
    "条形码粘贴区", "二维码粘贴区", "禁止作答", "禁止答题", "请勿作答", "请勿填写",
    "注意事项", "选择题", "单选题", "多选题", "单项选择题", "多项选择题", "判断题", "填空题", "解答题",
    "正确填涂", "错误填涂", "填涂示例",
    "Name", "Student ID", "Candidate No.", "Class", "School", "Barcode", "QR Code",
    "条形码", "条码粘贴区", "条形码区",
]
OBSERVATION_OPTION_PATTERNS = ["none", "letter_options", "bubble_options", "judgement_options", "unknown"]
OBSERVATION_IDENTITY_PATTERNS = ["none", "name_line", "number_grid", "identity_fields", "unknown"]
OBSERVATION_BARCODE_PATTERNS = ["none", "linear_bars", "qr_modules", "labelled_placeholder", "unknown"]
OBSERVATION_BOUNDARY_BASES = [
    "printed_frame", "column_and_next_question", "printed_answer_line", "label_and_field", "page_edge", "unknown",
]
OBSERVATION_QUESTION_LABEL_PATTERN = r"^[0-9]{1,3}(?:[.．-][0-9]{1,2}|[（(][0-9]{1,2}[）)])?$"
MAX_OBSERVED_QUESTION_LABELS = 64


def _object(properties):
    return {"type": "object", "additionalProperties": False,
            "required": list(properties), "properties": properties}


def bbox_schema():
    return _object({
        "x": {"type": "number", "minimum": 0, "maximum": 1},
        "y": {"type": "number", "minimum": 0, "maximum": 1},
        "width": {"type": "number", "exclusiveMinimum": 0, "maximum": 1},
        "height": {"type": "number", "exclusiveMinimum": 0, "maximum": 1},
    })


def _strings(max_items=32):
    return {"type": "array", "maxItems": max_items, "uniqueItems": True,
            "items": {"type": "string", "minLength": 1, "maxLength": 128}}


def request_schema():
    """凭据先由请求级模型适配器提取，不属于可进入提示词的分析输入。"""
    schema = _object({
        "request_id": {"type": "string", "minLength": 8, "maxLength": 128},
        "schema_version": {"const": SCHEMA_VERSION},
        "page": _object({
            "page_no": {"type": "integer", "minimum": 1, "maximum": 10000},
            "width": {"type": "integer", "minimum": 1, "maximum": 30000},
            "height": {"type": "integer", "minimum": 1, "maximum": 30000},
            "media_type": {"enum": ["image/png", "image/jpeg", "image/webp"]},
            "data_base64": {"type": "string", "minLength": 1,
                            "maxLength": ((MAX_PAGE_BYTES + 2) // 3) * 4},
            "sha256": {"type": "string", "pattern": "^[0-9a-f]{64}$"},
        }),
        "candidates": {"type": "array", "maxItems": MAX_CANDIDATES, "items": _object({
            "candidate_id": {"type": "string", "minLength": 1, "maxLength": 128},
            "bbox": bbox_schema(),
            "evidence_refs": _strings(),
        })},
        "question_labels": _strings(512),
        "objective_question_labels": _strings(512),
        "fill_blank_question_labels": _strings(512),
        "writing_state": {"enum": ["blank", "written", "unknown"]},
        "diagnostic_context": _object({
            "template_id": {"type": "string", "minLength": 1, "maxLength": 128},
            "template_revision": {"type": "integer", "minimum": 1},
            "source_paper_id": {"type": "string", "minLength": 1, "maxLength": 128},
            "source_pdf_sha256": {"type": "string", "pattern": "^[0-9a-f]{64}$"},
            "question_snapshot_sha256": {"type": "string", "pattern": "^[0-9a-f]{64}$"},
        }),
    })
    # 旧内部调用没有题型清单时仍可分析；正式教师入口由服务端从本场题目生成，不能信任浏览器声明。
    schema["required"].remove("objective_question_labels")
    schema["required"].remove("fill_blank_question_labels")
    schema["required"].remove("diagnostic_context")
    schema["properties"]["diagnostic_context"]["required"].remove("source_paper_id")
    return schema


def model_output_schema(candidate_ids, page_no, include_observations=False):
    """新区域用 null 交给服务端编号；已有编号只允许来自本次提示，页号仍固定。"""
    candidate_reference = {"type": "null"}
    if candidate_ids:
        candidate_reference = {"anyOf": [
            {"type": "string", "enum": list(candidate_ids)}, {"type": "null"},
        ]}
    schema = _object({"candidates": {
        "type": "array", "minItems": 0, "maxItems": MAX_CANDIDATES, "items": _object({
            "page_no": {"const": page_no},
            "candidate_id": candidate_reference,
            "printed_question_label": {"anyOf": [
                {"type": "string", "minLength": 1, "maxLength": 128}, {"type": "null"},
            ]},
            "identity_label": {"anyOf": [{"enum": IDENTITY_LABELS}, {"type": "null"}]},
            "role": {"enum": ROLES},
            "proposed_bbox": bbox_schema(),
            "association_status": {"enum": ASSOCIATION_STATUSES},
            "evidence_refs": _strings(),
            "reason_codes": {"type": "array", "maxItems": 32, "uniqueItems": True, "items": {
                "type": "string", "pattern": "^[A-Z][A-Z0-9_]{0,63}$",
            }},
        }),
    }})
    if include_observations:
        candidate = schema["properties"]["candidates"]["items"]
        candidate["properties"].update(_observation_properties())
        candidate["required"].extend(["observed_question_labels", "visual_evidence", "objective_items"])
    return schema


def _observation_properties():
    # 观察列表不限正式选择题白名单，否则 15/16 误入选择区的反证会在生成阶段消失。
    # 仅允许短题号及静态枚举，不能把姓名、长考号或自由 OCR 正文塞进观察说明。
    return {
        "objective_items": {"type": "array", "maxItems": 256, "items": _object({
            "printed_question_label": {"type": "string", "maxLength": 7,
                                       "pattern": OBSERVATION_QUESTION_LABEL_PATTERN},
            "bbox": bbox_schema(),
            "options": {"type": "array", "minItems": 2, "maxItems": 12, "items": _object({
                "label": {"enum": list("ABCDEFGHIJKL")}, "bbox": bbox_schema(),
            })},
        })},
        "observed_question_labels": {
            "type": "array", "maxItems": MAX_OBSERVED_QUESTION_LABELS, "uniqueItems": True,
            "items": {"type": "string", "maxLength": 7, "pattern": OBSERVATION_QUESTION_LABEL_PATTERN},
        },
        "visual_evidence": _object({
            "static_label": {"anyOf": [{"enum": OBSERVATION_STATIC_LABELS}, {"type": "null"}]},
            "option_pattern": {"enum": OBSERVATION_OPTION_PATTERNS},
            "identity_pattern": {"enum": OBSERVATION_IDENTITY_PATTERNS},
            "barcode_pattern": {"enum": OBSERVATION_BARCODE_PATTERNS},
            "boundary_basis": {"enum": OBSERVATION_BOUNDARY_BASES},
            # 锚点越页是本区域的复核问题，不应使一整页格式校验失败；范围由语义层核对。
            "anchor_bbox": {"anyOf": [_object({key: {"type": "number"}
                                                 for key in ("x", "y", "width", "height")}), {"type": "null"}]},
        }),
    }
