"""Normalize untrusted paper-import transport input."""

import base64
import binascii
import hashlib
import re

from .errors import AgentError
from .paper_schema import (
    MAX_VISUAL_PAGE_BYTES,
    MAX_VISUAL_PAYLOAD_BYTES,
    VISUAL_MEDIA_TYPES,
)


def normalize_paper_input(payload):
    if not isinstance(payload, dict):
        raise AgentError("invalid_request", "request must be an object", status=400)
    request_id = str(payload.get("request_id", "")).strip()
    subject = str(payload.get("subject", "")).strip().lower()
    documents = payload.get("documents")
    if not isinstance(documents, list):
        documents = []
        for index, (role, key) in enumerate(
            (("question", "paper_text"), ("answer", "answer_text"))
        ):
            content = str(payload.get(key, "")).strip()
            if content:
                documents.append(
                    {
                        "source_id": f"legacy-{role}",
                        "file_asset_id": "",
                        "document_index": index,
                        "role_hint": role,
                        "content": content,
                        "blocks": [],
                    }
                )
    cleaned = []
    seen_source_ids = set()
    seen_document_indexes = set()
    for index, document in enumerate(documents):
        if (
            not isinstance(document, dict)
            or not str(document.get("source_id", "")).strip()
            or not str(document.get("content", "")).strip()
        ):
            continue
        source_id = str(document["source_id"]).strip()
        try:
            document_index = int(document.get("document_index", index))
        except (TypeError, ValueError) as exc:
            raise AgentError(
                "invalid_request",
                "document_index must be a non-negative integer",
                status=400,
                request_id=request_id,
            ) from exc
        if (
            document_index < 0
            or source_id in seen_source_ids
            or document_index in seen_document_indexes
        ):
            raise AgentError(
                "invalid_request",
                "document sources and indexes must be unique",
                status=400,
                request_id=request_id,
            )
        seen_source_ids.add(source_id)
        seen_document_indexes.add(document_index)
        cleaned.append(
            {
                **document,
                "source_id": source_id,
                "document_index": document_index,
                "content": str(document["content"]).strip()[:700_000],
            }
        )
    if not request_id or not subject or not cleaned:
        raise AgentError(
            "invalid_request",
            "at least one non-empty document is required",
            status=400,
            request_id=request_id,
        )
    return request_id, subject, cleaned


def clean_visual_pages(raw_pages, documents, request_id):
    if raw_pages is None:
        return []
    if not isinstance(raw_pages, list):
        raise AgentError(
            "invalid_request",
            "visual_pages must be an array",
            status=400,
            request_id=request_id,
        )
    documents_by_id = {
        document["source_id"]: document for document in documents
    }
    cleaned_pages = []
    seen = set()
    total_bytes = 0
    pages_by_source = {}
    for raw_page in raw_pages:
        if not isinstance(raw_page, dict):
            raise AgentError(
                "invalid_request",
                "visual page metadata is invalid",
                status=400,
                request_id=request_id,
            )
        source_id = str(raw_page.get("source_id", "")).strip()
        document = documents_by_id.get(source_id)
        try:
            document_index = int(raw_page.get("document_index"))
            page_no = int(raw_page.get("page_no"))
        except (TypeError, ValueError) as exc:
            raise AgentError(
                "invalid_request",
                "visual page identity is invalid",
                status=400,
                request_id=request_id,
            ) from exc
        media_type = str(raw_page.get("media_type", "")).strip().lower()
        encoded = raw_page.get("data_base64")
        expected_sha = str(raw_page.get("sha256", "")).strip().lower()
        identity = (source_id, page_no)
        if (
            document is None
            or document_index != document["document_index"]
            or page_no <= 0
            or media_type not in VISUAL_MEDIA_TYPES
            or not isinstance(encoded, str)
            or not encoded
            or identity in seen
        ):
            raise AgentError(
                "invalid_request",
                "visual page identity or media type is invalid",
                status=400,
                request_id=request_id,
            )
        try:
            raw = base64.b64decode(encoded, validate=True)
        except (ValueError, binascii.Error) as exc:
            raise AgentError(
                "invalid_request",
                "visual page data is invalid",
                status=400,
                request_id=request_id,
            ) from exc
        total_bytes += len(raw)
        digest = hashlib.sha256(raw).hexdigest()
        if (
            not raw
            or len(raw) > MAX_VISUAL_PAGE_BYTES
            or total_bytes > MAX_VISUAL_PAYLOAD_BYTES
            or not re.fullmatch(r"[0-9a-f]{64}", expected_sha)
            or digest != expected_sha
        ):
            raise AgentError(
                "invalid_request",
                "visual page size or checksum is invalid",
                status=400,
                request_id=request_id,
            )
        seen.add(identity)
        pages_by_source.setdefault(source_id, []).append(page_no)
        cleaned_pages.append(
            {
                "source_id": source_id,
                "document_index": document_index,
                "page_no": page_no,
                "media_type": media_type,
                "data_base64": encoded,
                "sha256": digest,
                "width": raw_page.get("width"),
                "height": raw_page.get("height"),
            }
        )
    for document in documents:
        document["_visual_page_nos"] = sorted(
            pages_by_source.get(document["source_id"], [])
        )
    return sorted(
        cleaned_pages,
        key=lambda page: (page["document_index"], page["page_no"]),
    )
