"""合成印刷结构验证真实几何匹配；不代表学校扫描件的实测识别率。"""

import base64
import hashlib
import io

import cv2
import numpy as np
import pytest
from grading_agent.answer_sheet_vision import match_template_page
from grading_agent.errors import AgentError
from grading_agent.template_matching import TemplateMatcher
from PIL import Image, ImageDraw


def _sheet(seed=17):
    rng = np.random.default_rng(seed)
    image = Image.new("RGB", (800, 1100), "white")
    draw = ImageDraw.Draw(image)
    for row in range(3):
        top = 100 + row * 310
        draw.rectangle((60, top, 740, top + 270), outline="black", width=3)
        draw.text((72, top + 12), f"Question {row + 17} - Printed reference {seed}", fill="black")
    for _ in range(160):
        x, y = rng.integers(70, 710), rng.integers(80, 1020)
        draw.text((int(x), int(y)), str(rng.integers(100, 999)), fill="black")
    return np.asarray(image)


def _wire(image, candidate_id=None, media_type="image/png"):
    output = io.BytesIO()
    if media_type == "application/pdf":
        pages = image if isinstance(image, list) else [image]
        converted = [Image.fromarray(page) for page in pages]
        converted[0].save(output, format="PDF", save_all=True, append_images=converted[1:])
        for page in converted:
            page.close()
    else:
        Image.fromarray(image).save(output, format="PNG")
    raw = output.getvalue()
    document = {"media_type": media_type, "data_base64": base64.b64encode(raw).decode(),
                "sha256": hashlib.sha256(raw).hexdigest()}
    if candidate_id is not None:
        document["id"] = candidate_id
    return document


def _request(source, candidates):
    return {"request_id": "synthetic-template-match", "schema_version": "answer-sheet-match-v1",
            "source": source, "candidates": candidates}


def test_rescanned_reference_requires_real_geometry_and_structure():
    reference = _sheet()
    matrix = cv2.getRotationMatrix2D((400, 550), 1.0, .995)
    scanned = cv2.warpAffine(reference, matrix, (800, 1100), borderValue=(255, 255, 255))
    evidence = match_template_page(scanned, reference)
    assert evidence["passed"] is True
    assert evidence["method"] in {"orb", "akaze"}
    result = TemplateMatcher().match(_request(_wire(scanned), [_wire(reference, "correct")]))
    assert result["status"] == "matched"
    assert result["selected_id"] == "correct"


def test_near_identical_versions_remain_ambiguous():
    image = _sheet()
    result = TemplateMatcher().match(_request(_wire(image), [_wire(image, "v1"), _wire(image, "v2")]))
    assert result["status"] == "ambiguous"
    assert result["selected_id"] is None
    assert {row["id"] for row in result["candidates"]} == {"v1", "v2"}


def test_different_printed_structure_does_not_match_by_page_dimensions():
    result = TemplateMatcher().match(_request(_wire(_sheet(100)), [_wire(_sheet(), "wrong")]))
    assert result["status"] != "matched"
    assert result["selected_id"] is None


def test_every_pdf_page_must_match_in_order():
    first, second = _sheet(), _sheet(18)
    correct = _wire([first, second], "complete", "application/pdf")
    correct["expected_page_count"] = 2
    result = TemplateMatcher().match(_request(_wire([first, second], media_type="application/pdf"), [correct]))
    assert result["status"] == "matched"
    swapped = TemplateMatcher().match(_request(_wire([second, first], media_type="application/pdf"), [correct]))
    assert swapped["status"] != "matched"
    missing = TemplateMatcher().match(_request(_wire(first), [correct]))
    assert missing["status"] == "unknown"
    assert "PAGE_COUNT_MISMATCH" in missing["candidates"][0]["reason_codes"]


def test_bad_reference_page_count_is_unsupported_not_unknown():
    document = _wire(_sheet(), "incorrect-declaration")
    document["expected_page_count"] = 2
    result = TemplateMatcher().match(_request(_wire(_sheet()), [document]))
    assert result["status"] == "unsupported"
    assert result["selected_id"] is None


def test_corrupted_documents_and_forged_digests_never_look_unknown():
    image = _wire(_sheet())
    bad = {**image, "sha256": "0" * 64}
    with pytest.raises(AgentError):
        TemplateMatcher().match(_request(bad, [_wire(_sheet(), "reference")]))
    raw = b"not a PDF"
    corrupt = {"media_type": "application/pdf", "data_base64": base64.b64encode(raw).decode(),
               "sha256": hashlib.sha256(raw).hexdigest()}
    result = TemplateMatcher().match(_request(corrupt, [_wire(_sheet(), "reference")]))
    assert result["status"] == "unsupported"


def test_matching_cannot_create_candidates_or_accept_duplicate_ids():
    reference = _wire(_sheet(), "same")
    with pytest.raises(AgentError):
        TemplateMatcher().match(_request(_wire(_sheet()), [reference, reference]))
