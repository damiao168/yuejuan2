"""JSON schemas and stable paper-import domain constants."""

QUESTION_TYPES = {
    "single_choice",
    "multiple_choice",
    "true_false",
    "fill_blank",
    "numeric",
    "formula",
    "short_answer",
    "calculation",
    "essay",
    "discussion",
    "coding",
}
FORMULA_SUBJECTS = {
    "math",
    "mathematics",
    "physics",
    "chemistry",
    "数学",
    "物理",
    "化学",
}
ROLES = ["question", "answer", "solution", "rubric", "mixed", "unknown"]
VISUAL_MEDIA_TYPES = {"image/png", "image/jpeg", "image/webp"}
MAX_VISUAL_PAGE_BYTES = 8 * 1024 * 1024
MAX_VISUAL_PAYLOAD_BYTES = 32 * 1024 * 1024


def _nullable(kind):
    return {"anyOf": [{"type": kind}, {"type": "null"}]}


def paper_import_schema():
    ref = {
        "type": "object",
        "additionalProperties": False,
        "required": [
            "source_id",
            "file_asset_id",
            "document_index",
            "page_no",
            "block_id",
            "bbox",
            "text_start",
            "text_end",
            "ocr_confidence",
        ],
        "properties": {
            "source_id": {"type": "string"},
            "file_asset_id": {"type": "string"},
            "document_index": {"type": "integer", "minimum": 0},
            "page_no": _nullable("integer"),
            "block_id": _nullable("string"),
            "bbox": {},
            "text_start": _nullable("integer"),
            "text_end": _nullable("integer"),
            "ocr_confidence": _nullable("number"),
        },
    }
    common = {
        "candidate_id": {"type": "string"},
        "question_no_hint": _nullable("string"),
        "question_no_normalized": _nullable("string"),
        "subquestion_no_hint": _nullable("string"),
        "confidence": {"type": "number", "minimum": 0, "maximum": 1},
        "source_refs": {"type": "array", "minItems": 1, "items": ref},
        "issues": {"type": "array", "items": {"type": "string"}},
    }
    question = {
        "type": "object",
        "additionalProperties": False,
        "required": [
            "candidate_id",
            "question_no_raw",
            "question_no_normalized",
            "parent_question_no",
            "subquestion_no",
            "section_hint",
            "stem",
            "options",
            "question_type",
            "score",
            "knowledge_point_hints",
            "confidence",
            "source_refs",
            "issues",
        ],
        "properties": {
            "candidate_id": common["candidate_id"],
            "question_no_raw": _nullable("string"),
            "question_no_normalized": _nullable("string"),
            "parent_question_no": _nullable("string"),
            "subquestion_no": _nullable("string"),
            "section_hint": _nullable("string"),
            "stem": _nullable("string"),
            "options": {"type": "array", "items": {"type": "string"}},
            "question_type": {
                "anyOf": [
                    {"type": "string", "enum": sorted(QUESTION_TYPES)},
                    {"type": "null"},
                ]
            },
            "score": _nullable("number"),
            "knowledge_point_hints": {"type": "array", "items": {"type": "string"}},
            "confidence": common["confidence"],
            "source_refs": common["source_refs"],
            "issues": common["issues"],
        },
    }
    answer = {
        "type": "object",
        "additionalProperties": False,
        "required": list(common)
        + ["standard_answer", "equivalent_answers", "tolerance"],
        "properties": {
            **common,
            "standard_answer": {},
            "equivalent_answers": {"type": "array"},
            "tolerance": {},
        },
    }
    step = {
        "type": "object",
        "additionalProperties": False,
        "required": ["step_no", "content"],
        "properties": {
            "step_no": {"type": "integer", "minimum": 1},
            "content": {"type": "string"},
        },
    }
    solution = {
        "type": "object",
        "additionalProperties": False,
        "required": list(common) + ["raw_text", "steps"],
        "properties": {
            **common,
            "raw_text": {"type": "string"},
            "steps": {"type": "array", "items": step},
        },
    }
    evidence_leaf = {
        "type": "object",
        "additionalProperties": False,
        "required": ["type", "target"],
        "properties": {
            "type": {
                "type": "string",
                "enum": [
                    "valid_transformation",
                    "final_result",
                    "concept",
                    "unit",
                    "domain",
                ],
            },
            "target": _nullable("string"),
        },
    }
    evidence = {
        "type": "object",
        "additionalProperties": False,
        "required": ["type", "target", "minimum", "children"],
        "properties": {
            "type": {
                "type": "string",
                "enum": [
                    "all_of",
                    "any_of",
                    "at_least",
                    "valid_transformation",
                    "final_result",
                    "concept",
                    "unit",
                    "domain",
                ],
            },
            "target": _nullable("string"),
            "minimum": _nullable("number"),
            "children": {"type": "array", "items": evidence_leaf},
        },
    }
    rubric_point = {
        "type": "object",
        "additionalProperties": False,
        "required": ["id", "description", "score", "required", "evidence_requirements"],
        "properties": {
            "id": {"type": "string"},
            "description": {"type": "string"},
            "score": _nullable("number"),
            "required": {"type": ["boolean", "null"]},
            "evidence_requirements": {"type": "array", "items": evidence},
        },
    }
    rubric = {
        "type": "object",
        "additionalProperties": False,
        "required": [
            "candidate_id",
            "question_no_hint",
            "question_no_normalized",
            "max_score",
            "points",
            "deductions",
            "examples",
            "confidence",
            "source_refs",
            "issues",
        ],
        "properties": {
            "candidate_id": common["candidate_id"],
            "question_no_hint": common["question_no_hint"],
            "question_no_normalized": common["question_no_normalized"],
            "max_score": _nullable("number"),
            "points": {"type": "array", "items": rubric_point},
            "deductions": {"type": "array"},
            "examples": {"type": "array"},
            "confidence": common["confidence"],
            "source_refs": common["source_refs"],
            "issues": common["issues"],
        },
    }
    issue = {
        "type": "object",
        "additionalProperties": False,
        "required": [
            "code",
            "severity",
            "certainty",
            "question_no",
            "section",
            "message",
            "confidence",
            "source_refs",
            "resolution_hint",
        ],
        "properties": {
            "code": {"type": "string"},
            "severity": {"type": "string", "enum": ["info", "warning", "error"]},
            "certainty": {
                "type": "string",
                "enum": ["confirmed", "suspected", "unknown"],
            },
            "question_no": _nullable("string"),
            "section": _nullable("string"),
            "message": {"type": "string"},
            "confidence": _nullable("number"),
            "source_refs": {"type": "array", "items": ref},
            "resolution_hint": _nullable("string"),
        },
    }
    document = {
        "type": "object",
        "additionalProperties": False,
        "required": ["source_id", "detected_role", "role_confidence"],
        "properties": {
            "source_id": {"type": "string"},
            "detected_role": {"type": "string", "enum": ROLES},
            "role_confidence": {"type": "number", "minimum": 0, "maximum": 1},
        },
    }
    return {
        "type": "object",
        "additionalProperties": False,
        "required": [
            "documents",
            "question_candidates",
            "answer_candidates",
            "solution_candidates",
            "rubric_candidates",
            "issues",
        ],
        "properties": {
            "documents": {"type": "array", "items": document},
            "question_candidates": {"type": "array", "items": question},
            "answer_candidates": {"type": "array", "items": answer},
            "solution_candidates": {"type": "array", "items": solution},
            "rubric_candidates": {"type": "array", "items": rubric},
            "issues": {"type": "array", "items": issue},
        },
    }

def visual_paper_import_schema():
    """Let the model omit trusted transport metadata that the API owns."""
    schema = paper_import_schema()
    reference = schema["properties"]["question_candidates"]["items"][
        "properties"
    ]["source_refs"]["items"]
    reference["properties"]["file_asset_id"] = _nullable("string")
    reference["properties"]["document_index"] = {
        "anyOf": [
            {"type": "integer", "minimum": 0},
            {"type": "null"},
        ]
    }
    return schema


def visual_model_output_schema():
    """Small provider-facing contract; durable fields are restored locally."""
    source = {
        "type": "object",
        "additionalProperties": False,
        "required": ["id", "page"],
        "properties": {
            "id": {"type": "string"},
            "page": _nullable("integer"),
        },
    }
    common = {
        "confidence": {"type": "number", "minimum": 0, "maximum": 1},
        "source": source,
        "issues": {"type": "array", "items": {"type": "string"}},
    }
    question = {
        "type": "object",
        "additionalProperties": False,
        "required": [
            "no", "parent_no", "sub_no", "section", "stem", "options",
            "type", "score", "confidence", "source", "issues",
        ],
        "properties": {
            "no": _nullable("string"),
            "parent_no": _nullable("string"),
            "sub_no": _nullable("string"),
            "section": _nullable("string"),
            "stem": _nullable("string"),
            "options": {"type": "array", "items": {"type": "string"}},
            "type": {
                "anyOf": [
                    {"type": "string", "enum": sorted(QUESTION_TYPES)},
                    {"type": "null"},
                ]
            },
            "score": _nullable("number"),
            **common,
        },
    }
    answer = {
        "type": "object",
        "additionalProperties": False,
        "required": ["no", "value", "confidence", "source", "issues"],
        "properties": {"no": _nullable("string"), "value": {}, **common},
    }
    solution = {
        "type": "object",
        "additionalProperties": False,
        "required": ["no", "text", "confidence", "source", "issues"],
        "properties": {
            "no": _nullable("string"),
            "text": {"type": "string"},
            **common,
        },
    }
    point = {
        "type": "object",
        "additionalProperties": False,
        "required": ["description", "score", "required"],
        "properties": {
            "description": {"type": "string"},
            "score": _nullable("number"),
            "required": {"type": ["boolean", "null"]},
        },
    }
    rubric = {
        "type": "object",
        "additionalProperties": False,
        "required": [
            "no", "max_score", "points", "deductions", "examples",
            "confidence", "source", "issues",
        ],
        "properties": {
            "no": _nullable("string"),
            "max_score": _nullable("number"),
            "points": {"type": "array", "items": point},
            "deductions": {"type": "array"},
            "examples": {"type": "array"},
            **common,
        },
    }
    issue = {
        "type": "object",
        "additionalProperties": False,
        "required": [
            "code", "severity", "certainty", "no", "section", "message",
            "confidence", "source", "resolution",
        ],
        "properties": {
            "code": {"type": "string"},
            "severity": {"type": "string", "enum": ["info", "warning", "error"]},
            "certainty": {
                "type": "string",
                "enum": ["confirmed", "suspected", "unknown"],
            },
            "no": _nullable("string"),
            "section": _nullable("string"),
            "message": {"type": "string"},
            "confidence": _nullable("number"),
            "source": {"anyOf": [source, {"type": "null"}]},
            "resolution": _nullable("string"),
        },
    }
    document = {
        "type": "object",
        "additionalProperties": False,
        "required": ["id", "role", "confidence"],
        "properties": {
            "id": {"type": "string"},
            "role": {"type": "string", "enum": ROLES},
            "confidence": {"type": "number", "minimum": 0, "maximum": 1},
        },
    }
    return {
        "type": "object",
        "additionalProperties": False,
        "required": ["documents", "questions", "answers", "solutions", "rubrics", "issues"],
        "properties": {
            "documents": {"type": "array", "items": document},
            "questions": {"type": "array", "items": question},
            "answers": {"type": "array", "items": answer},
            "solutions": {"type": "array", "items": solution},
            "rubrics": {"type": "array", "items": rubric},
            "issues": {"type": "array", "items": issue},
        },
    }
