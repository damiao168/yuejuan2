"""保守识别页眉页脚；只过滤解析视图，原始 OCR 和来源引用保留在调用方。"""

import re
import unicodedata

from .layout import _box
from .schema import (
    _ANSWER_MARKER,
    _DISTRIBUTION_MARKER,
    _OPTION,
    _PAGE_FOOTER,
    _QUESTION_START,
    _REPEATED_FURNITURE_MARKER,
    _SOLUTION_MARKER,
    MAX_PAGE_FOOTER_TEXT_CHARS,
)


def _block_text(block):
    return str(block.get("text", "")).strip()


def _furniture_signature(text):
    value = unicodedata.normalize("NFKC", str(text or "")).lower()
    value = re.sub(r"\d+", "#", value)
    return re.sub(r"[\s,，、.．·:：_\-—]+", "", value)


def _without_page_furniture(blocks):
    """Exclude strong page furniture while retaining raw OCR in storage.

    Single-page imports need explicit footer/watermark markers; repeated text
    near the top or bottom of multiple pages is also treated as furniture.
    Position alone never removes content, so a last question near the page edge
    remains available to the parser.
    """

    valid = [block for block in blocks if isinstance(block, dict)]
    page_bounds, signatures = _furniture_page_context(valid)
    repeated = {signature for signature, pages in signatures.items() if len(pages) >= 2}
    return [block for block in valid if not _is_page_furniture(block, page_bounds, repeated)]


def _furniture_page_context(blocks):
    page_bounds = {}
    signatures = {}
    for block in blocks:
        box = _box((0, block))
        if box is None:
            continue
        try:
            page_no = int(block.get("page_no", 0))
        except (TypeError, ValueError):
            page_no = 0
        top, bottom = box[1], box[1] + box[3]
        previous = page_bounds.get(page_no)
        page_bounds[page_no] = (
            min(previous[0], top) if previous else top,
            max(previous[1], bottom) if previous else bottom,
        )
        signature = _furniture_signature(_block_text(block))
        if len(signature) >= 6:
            signatures.setdefault(signature, set()).add(page_no)

    return page_bounds, signatures


def _is_page_furniture(block, page_bounds, repeated):
    text = _block_text(block)
    box = _box((0, block))
    try:
        page_no = int(block.get("page_no", 0))
    except (TypeError, ValueError):
        page_no = 0
    bounds = page_bounds.get(page_no)
    if box is None or bounds is None or bounds[1] <= bounds[0]:
        return False
    page_height = bounds[1] - bounds[0]
    center = box[1] + box[3] / 2
    near_top = center <= bounds[0] + page_height * 0.08
    near_bottom = center >= bounds[0] + page_height * 0.90
    protected_anchor = any(
        pattern.match(text)
        for pattern in (_QUESTION_START, _ANSWER_MARKER, _SOLUTION_MARKER, _OPTION)
    )
    # 长度预算必须先于正则调用；后续分块上限不能保护此处的原始 OCR 文本。
    strong_footer = not protected_anchor and near_bottom and len(text) <= MAX_PAGE_FOOTER_TEXT_CHARS and (
        _PAGE_FOOTER.fullmatch(text) is not None
        or _DISTRIBUTION_MARKER.search(text) is not None
    )
    repeated_edge = not protected_anchor and (near_top or near_bottom) and (
        _furniture_signature(text) in repeated
        and _REPEATED_FURNITURE_MARKER.search(text) is not None
    )
    return strong_footer or repeated_edge
