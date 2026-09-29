"""Build bounded, provenance-safe model chunks."""

from dataclasses import dataclass

from .anchors import _without_page_furniture
from .layout import _reading_order
from .schema import (
    _QUESTION_START,
    _SECTION_START,
    MAX_BLOCKS_PER_CHUNK,
    MAX_DIRECT_TEXT_CHARS_PER_CHUNK,
    MAX_TEXT_CHARS_PER_CHUNK,
)


@dataclass(frozen=True)
class CompactChunk:
    model_document: dict
    source_document: dict
    references: dict[str, dict]


def needs_compact_protocol(documents):
    return any(document.get("blocks") for document in documents) or any(
        len(str(document.get("content", ""))) > 4_000 for document in documents
    )



def build_compact_chunks(documents):
    chunks = []
    for document in documents:
        blocks = document.get("blocks")
        if isinstance(blocks, list) and blocks:
            chunks.extend(_ocr_chunks(document, blocks))
        else:
            chunks.extend(_direct_text_chunks(document))
    return chunks

def _ocr_chunks(document, blocks):
    by_page = {}
    retained = {id(block) for block in _without_page_furniture(blocks)}
    for sequence, block in enumerate(blocks):
        if not isinstance(block, dict) or not str(block.get("text", "")).strip():
            continue
        try:
            page_no = int(block.get("page_no", 0))
        except (TypeError, ValueError):
            page_no = 0
        by_page.setdefault(page_no, []).append((sequence, block))

    chunks = []
    for page_no in sorted(by_page):
        ordered = [
            entry
            for entry in _reading_order(by_page[page_no])
            if id(entry[1]) in retained
        ]
        for block_group in _split_block_groups(ordered):
            references = {}
            compact_blocks = []
            for ref_number, (_, block) in enumerate(block_group, 1):
                ref_id = f"r{ref_number:03d}"
                compact_blocks.append([ref_id, str(block.get("text", "")).strip()])
                references[ref_id] = {
                    "source_id": str(document.get("source_id", "")),
                    "file_asset_id": str(document.get("file_asset_id", "")),
                    "document_index": int(document.get("document_index", 0)),
                    "page_no": page_no,
                    "block_id": block.get("block_id"),
                    "bbox": block.get("bbox"),
                    "text_start": None,
                    "text_end": None,
                    "ocr_confidence": block.get("confidence"),
                }
            chunks.append(
                CompactChunk(
                    model_document={
                        "source_id": str(document.get("source_id", "")),
                        "document_index": int(document.get("document_index", 0)),
                        "role_hint": str(document.get("role_hint", "auto")),
                        "page_no": page_no,
                        "ordered_blocks": compact_blocks,
                    },
                    source_document=document,
                    references=references,
                )
            )
    return chunks


def _direct_text_chunks(document):
    content = str(document.get("content", "")).strip()
    if not content:
        return []
    chunks = []
    start = 0
    # 文本范围按 Python 字符索引记录为 [start, end)，分片不重置原文偏移。
    while start < len(content):
        end = min(len(content), start + MAX_DIRECT_TEXT_CHARS_PER_CHUNK)
        if end < len(content):
            boundary = max(
                content.rfind("\n", start, end), content.rfind("。", start, end)
            )
            if boundary > start + MAX_DIRECT_TEXT_CHARS_PER_CHUNK // 2:
                end = boundary + 1
        ref = {
            "source_id": str(document.get("source_id", "")),
            "file_asset_id": str(document.get("file_asset_id", "")),
            "document_index": int(document.get("document_index", 0)),
            "page_no": None,
            "block_id": None,
            "bbox": None,
            "text_start": start,
            "text_end": end,
            "ocr_confidence": None,
        }
        chunks.append(
            CompactChunk(
                model_document={
                    "source_id": str(document.get("source_id", "")),
                    "document_index": int(document.get("document_index", 0)),
                    "role_hint": str(document.get("role_hint", "auto")),
                    "page_no": None,
                    "ordered_blocks": [["r001", content[start:end]]],
                },
                source_document=document,
                references={"r001": ref},
            )
        )
        start = end
    return chunks



def _split_block_groups(blocks):
    # 优先以题号和章节形成逻辑组；超预算的单组仍需拆分，不能无限放大模型上下文。
    logical_groups = []
    current = []
    has_question = False
    for item in blocks:
        text = str(item[1].get("text", "")).strip()
        starts_question = bool(_QUESTION_START.match(text))
        starts_section = bool(_SECTION_START.match(text))
        if current and ((starts_question and has_question) or starts_section):
            logical_groups.append(current)
            current = []
            has_question = False
        current.append(item)
        has_question = has_question or starts_question
    if current:
        logical_groups.append(current)

    chunks = []
    current = []
    current_chars = 0
    for group in logical_groups:
        group_chars = sum(len(str(item[1].get("text", ""))) for item in group)
        if current and (
            len(current) + len(group) > MAX_BLOCKS_PER_CHUNK
            or current_chars + group_chars > MAX_TEXT_CHARS_PER_CHUNK
        ):
            chunks.append(current)
            current = []
            current_chars = 0
        while (
            len(group) > MAX_BLOCKS_PER_CHUNK or group_chars > MAX_TEXT_CHARS_PER_CHUNK
        ):
            take = []
            take_chars = 0
            for item in group:
                size = len(str(item[1].get("text", "")))
                if take and (
                    len(take) >= MAX_BLOCKS_PER_CHUNK
                    or take_chars + size > MAX_TEXT_CHARS_PER_CHUNK
                ):
                    break
                take.append(item)
                take_chars += size
            chunks.append(take)
            group = group[len(take) :]
            group_chars = sum(len(str(item[1].get("text", ""))) for item in group)
        current.extend(group)
        current_chars += group_chars
    if current:
        chunks.append(current)
    return chunks
