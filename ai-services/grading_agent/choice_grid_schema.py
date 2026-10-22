"""选择题大框解析只接收图像和人工几何约束，不接受模型或答案映射。"""

SCHEMA_VERSION = "answer-sheet-objective-grid-v1"
ALGORITHM_VERSION = "opencv-objective-grid-v2"
MAX_PAGE_BYTES = 8 * 1024 * 1024
MAX_PAGE_PIXELS = 4_000_000
MAX_CELLS = 2000
MAX_GROUPS = 500


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


def request_schema():
    return _object({
        "request_id": {"type": "string", "minLength": 8, "maxLength": 128},
        "schema_version": {"const": SCHEMA_VERSION},
        "page": _object({
            "page_no": {"type": "integer", "minimum": 1, "maximum": 10000},
            "width": {"type": "integer", "minimum": 1, "maximum": 4096},
            "height": {"type": "integer", "minimum": 1, "maximum": 4096},
            "media_type": {"enum": ["image/png", "image/jpeg", "image/webp"]},
            "data_base64": {"type": "string", "minLength": 1,
                            "maxLength": ((MAX_PAGE_BYTES + 2) // 3) * 4},
            "sha256": {"type": "string", "pattern": "^[0-9a-f]{64}$"},
        }),
        "block_bbox": bbox_schema(),
        "option_count": {"type": "integer", "minimum": 2, "maximum": 12},
        "option_orientation": {"enum": ["horizontal", "vertical"]},
    })
