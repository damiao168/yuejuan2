"""完整模板来源的候选匹配，只在逐页几何验证通过时选择可复用版本。

文件解析失败、超出支持页数或候选未完成检索属于 unsupported；不能借此得出
未知模板结论。页面按原文件页序比较，不猜测学生身份、错页或乱序关系。
"""

import base64
import hashlib
import io
import warnings
from contextlib import ExitStack

import numpy as np
import pypdfium2 as pdfium
from jsonschema import Draft202012Validator
from jsonschema.exceptions import ValidationError
from PIL import Image, ImageOps

from .answer_sheet_vision import (
    MATCH_ALGORITHM_VERSION,
    MAX_PIXELS,
    match_template_page,
)
from .errors import AgentError

SCHEMA_VERSION = "answer-sheet-match-v1"
MAX_FILE_BYTES = 8 * 1024 * 1024
MAX_TOTAL_BYTES = 24 * 1024 * 1024
MAX_PAGES = 100
MINIMUM_MARGIN = 0.08


def _document_schema(candidate=False):
    properties = {
        "media_type": {"enum": ["image/png", "image/jpeg", "application/pdf"]},
        "data_base64": {"type": "string", "minLength": 1, "maxLength": ((MAX_FILE_BYTES + 2) // 3) * 4},
        "sha256": {"type": "string", "pattern": "^[0-9a-f]{64}$"},
    }
    required = list(properties)
    if candidate:
        properties["id"] = {"type": "string", "minLength": 1, "maxLength": 128}
        properties["expected_page_count"] = {"type": "integer", "minimum": 1, "maximum": MAX_PAGES}
        required.append("id")
    return {"type": "object", "additionalProperties": False, "required": required, "properties": properties}


def _request_schema():
    return {"type": "object", "additionalProperties": False,
            "required": ["request_id", "schema_version", "source", "candidates"], "properties": {
                "request_id": {"type": "string", "minLength": 8, "maxLength": 128},
                "schema_version": {"const": SCHEMA_VERSION},
                "source": _document_schema(),
                "candidates": {"type": "array", "minItems": 1, "maxItems": 8, "items": _document_schema(True)},
            }}


class TemplateMatcher:
    def match(self, payload):
        request_id = payload.get("request_id", "") if isinstance(payload, dict) else ""
        try:
            Draft202012Validator(_request_schema()).validate(payload)
        except ValidationError as exc:
            raise AgentError("invalid_request", "template match schema validation failed",
                             status=400, request_id=request_id) from exc
        seen = set()
        documents = [payload["source"], *payload["candidates"]]
        raw_documents = []
        total = 0
        for index, document in enumerate(documents):
            if index:
                if document["id"] in seen:
                    raise AgentError("invalid_request", "template candidate IDs must be unique",
                                     status=400, request_id=request_id)
                seen.add(document["id"])
            try:
                raw = base64.b64decode(document["data_base64"], validate=True)
            except ValueError as exc:
                raise AgentError("invalid_request", "template match data is invalid", status=400, request_id=request_id) from exc
            total += len(raw)
            if (not raw or len(raw) > MAX_FILE_BYTES or total > MAX_TOTAL_BYTES
                    or hashlib.sha256(raw).hexdigest() != document["sha256"]):
                raise AgentError("invalid_request", "template match checksum or size is invalid",
                                 status=400, request_id=request_id)
            raw_documents.append(raw)
        result = {"request_id": request_id, "schema_version": SCHEMA_VERSION, "status": "unknown",
                  "selected_id": None, "candidates": [], "reason_codes": [],
                  "versions": {"schema": SCHEMA_VERSION, "algorithm": MATCH_ALGORITHM_VERSION}}
        with ExitStack() as resources:
            try:
                source = resources.enter_context(_Document(raw_documents[0], documents[0]["media_type"]))
            except _UnsupportedDocument:
                return {**result, "status": "unsupported", "reason_codes": ["SOURCE_DOCUMENT_UNSUPPORTED"]}
            source_pages = {}
            rows = []
            unsupported = False
            for document, raw in zip(documents[1:], raw_documents[1:]):
                row = {"id": document["id"], "score": 0.0, "reason_codes": [], "passed": False}
                try:
                    candidate = resources.enter_context(_Document(raw, document["media_type"]))
                    if document.get("expected_page_count", candidate.page_count) != candidate.page_count:
                        row["reason_codes"] = ["LIBRARY_PAGE_COUNT_MISMATCH"]
                        unsupported = True
                    elif source.page_count != candidate.page_count:
                        row["reason_codes"] = ["PAGE_COUNT_MISMATCH"]
                    else:
                        scores = []
                        for page_no in range(source.page_count):
                            if page_no not in source_pages:
                                source_pages[page_no] = source.page(page_no)
                            evidence = match_template_page(source_pages[page_no], candidate.page(page_no))
                            scores.append(evidence["score"])
                            if not evidence["passed"]:
                                row["reason_codes"] = [f"PAGE_{page_no + 1}_MATCH_FAILED", *evidence["reason_codes"]]
                                break
                        else:
                            row["passed"] = True
                            row["reason_codes"] = ["COMPLETE_DOCUMENT_MATCH"]
                        # 任何一页不可靠都不能用其余好页的平均分掩盖；全套分数取最低页。
                        row["score"] = round(min(scores, default=0.0), 6)
                except _UnsupportedDocument:
                    row["reason_codes"] = ["CANDIDATE_DOCUMENT_UNSUPPORTED"]
                    unsupported = True
                rows.append(row)
        rows.sort(key=lambda row: row["score"], reverse=True)
        result["candidates"] = [{key: value for key, value in row.items() if key != "passed"} for row in rows]
        if unsupported:
            result.update(status="unsupported", reason_codes=["CANDIDATE_SEARCH_INCOMPLETE"])
        elif rows and rows[0]["passed"]:
            if len(rows) > 1 and rows[0]["score"] - rows[1]["score"] < MINIMUM_MARGIN:
                result.update(status="ambiguous", reason_codes=["MATCH_MARGIN_TOO_SMALL"])
            else:
                result.update(status="matched", selected_id=rows[0]["id"])
        return result


class _UnsupportedDocument(ValueError):
    pass


class _Document:
    def __init__(self, raw, media_type):
        self._pdf = None
        self._image = None
        try:
            if media_type == "application/pdf":
                self._pdf = pdfium.PdfDocument(raw)
                self.page_count = len(self._pdf)
            else:
                with warnings.catch_warnings():
                    warnings.simplefilter("error", Image.DecompressionBombWarning)
                    self._image = Image.open(io.BytesIO(raw))
                expected = {"image/png": "PNG", "image/jpeg": "JPEG"}
                if (self._image.format != expected[media_type]
                        or self._image.width * self._image.height > MAX_PIXELS
                        or getattr(self._image, "n_frames", 1) != 1):
                    raise _UnsupportedDocument()
                self.page_count = 1
            if not 1 <= self.page_count <= MAX_PAGES:
                raise _UnsupportedDocument()
        except (OSError, ValueError, pdfium.PdfiumError, Image.DecompressionBombWarning) as exc:
            self.close()
            raise _UnsupportedDocument() from exc

    def __enter__(self):
        return self

    def __exit__(self, *_args):
        self.close()

    def close(self):
        if self._pdf is not None:
            self._pdf.close()
        if self._image is not None:
            self._image.close()

    def page(self, index):
        try:
            if self._pdf is not None:
                page = self._pdf[index]
                try:
                    width, height = page.get_size()
                    if not all(np.isfinite(value) and value > 0 for value in (width, height)):
                        raise _UnsupportedDocument()
                    bitmap = page.render(scale=min(2.0, 1400 / max(width, height)))
                    try:
                        return np.asarray(bitmap.to_pil().convert("RGB")).copy()
                    finally:
                        bitmap.close()
                finally:
                    page.close()
            # 这里输入是原始文件，EXIF 只在解析阶段应用一次；与分析接口的显示页协议不同。
            image = ImageOps.exif_transpose(self._image)
            image.thumbnail((1400, 1400), Image.Resampling.LANCZOS)
            rgba = image.convert("RGBA")
            return np.asarray(Image.alpha_composite(Image.new("RGBA", rgba.size, "white"), rgba).convert("RGB")).copy()
        except (OSError, ValueError, pdfium.PdfiumError) as exc:
            raise _UnsupportedDocument() from exc
