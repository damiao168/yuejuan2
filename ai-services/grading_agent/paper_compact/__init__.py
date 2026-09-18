"""Compact, provenance-safe model protocol for OCR paper imports."""

from .anchors import anchored_paper_result, page_furniture_issues
from .chunks import CompactChunk, build_compact_chunks, needs_compact_protocol
from .expand import expand_compact_output
from .schema import (
    MAX_BLOCKS_PER_CHUNK,
    MAX_DIRECT_TEXT_CHARS_PER_CHUNK,
    MAX_REFS_PER_CANDIDATE,
    MAX_TEXT_CHARS_PER_CHUNK,
    compact_paper_import_schema,
)

__all__ = [
    "CompactChunk",
    "MAX_BLOCKS_PER_CHUNK",
    "MAX_DIRECT_TEXT_CHARS_PER_CHUNK",
    "MAX_REFS_PER_CANDIDATE",
    "MAX_TEXT_CHARS_PER_CHUNK",
    "anchored_paper_result",
    "build_compact_chunks",
    "compact_paper_import_schema",
    "expand_compact_output",
    "needs_compact_protocol",
    "page_furniture_issues",
]
