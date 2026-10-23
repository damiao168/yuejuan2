import base64
import copy
import io
import json
import subprocess
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch

import cv2
import numpy as np
import pytest
from grading_agent.answer_sheet_vision import detect_printed_frames
from grading_agent.template_analysis import TemplateAnalyzer, _merge_local_fill_regions
from grading_agent.template_analysis_fill_regions import (
    _measured_sections,
    _read_section,
    discover_fill_regions,
)
from PIL import Image
from test_template_analysis import StructuredModel, payload


def fill_page():
    image = np.full((1000, 800, 3), 255, np.uint8)
    cv2.rectangle(image, (100, 100), (700, 900), (0, 0, 0), 2)
    cv2.line(image, (100, 400), (700, 400), (0, 0, 0), 2)
    for start, end, y in [(190, 350, 222), (470, 650, 222), (190, 350, 332)]:
        cv2.line(image, (start, y), (end, y), (0, 0, 0), 1)
    cv2.rectangle(image, (200, 550), (450, 700), (0, 0, 0), 2)
    return image


def tokens():
    def token(text, x, y, w, h=20, confidence=90, line="marker"):
        return {"text": text, "x": x, "y": y, "width": w, "height": h,
                "confidence": confidence, "line": line}
    return [token("填", 120, 130, 20, line="heading"), token("空", 140, 130, 20, line="heading"),
            token("题", 160, 130, 20, line="heading"), token("(12)", 120, 200, 40),
            token("题", 165, 200, 15), token("(13)", 400, 200, 40, confidence=47.5),
            token("(14)题", 120, 310, 55), token("PRIVATE STUDENT VALUE", 230, 180, 90)]


def run_detection(content=None, image=None, labels=None):
    image = fill_page() if image is None else image
    frames = detect_printed_frames(image, 1)
    sections, _ = _measured_sections(image, frames)
    def ocr(crop, *, timeout):
        assert 0 < timeout <= 2
        matching = next(section for section in sections if abs(
            crop.shape[0] / crop.shape[1] - (section[3] - section[1]) / (section[2] - section[0])) < .001)
        left, top = matching[:2]
        factor = crop.shape[1] / (matching[2] - matching[0])
        return [{**token, "x": (token["x"] - left) * factor, "y": (token["y"] - top) * factor,
                 "width": token["width"] * factor, "height": token["height"] * factor}
                for token in (tokens() if content is None else content)], None
    return discover_fill_regions(image, {"page_no": 1, "sha256": "a" * 64}, frames,
                                 ["12", "13", "14"] if labels is None else labels, ocr=ocr)


def test_measured_section_keeps_fill_rows_outside_forbidden_area():
    found, reasons = run_detection()
    assert reasons == []
    assert [row["printed_question_label"] for row in found] == ["12", "13", "14"]
    assert all(row["semantic_check"] == "needs_review" and row["association_status"] == "unassociated" for row in found)
    assert all(row["origin"] == "local_detector" for row in found)
    assert all(row["proposed_bbox"]["y"] + row["proposed_bbox"]["height"] < .4 for row in found)
    assert found[0]["proposed_bbox"]["x"] + found[0]["proposed_bbox"]["width"] < found[1]["proposed_bbox"]["x"]
    assert "PRIVATE" not in json.dumps(found)
    assert all(row["candidate_id"].startswith("local-fill-") for row in found)


def test_missing_official_fill_type_does_not_run_recovery_or_infer_from_nonobjective_numbers():
    with patch("grading_agent.template_analysis_fill_regions._read_section") as reader:
        assert discover_fill_regions(fill_page(), {}, [], [], observed_fill_labels=["14"])[0] == []
    reader.assert_not_called()


def test_only_formal_fill_labels_are_returned():
    found, _ = run_detection(labels=["12", "14"])
    assert [row["printed_question_label"] for row in found] == ["12", "14"]


def test_answer_margin_keeps_small_descender_and_right_overhang_inside_measured_section():
    image = fill_page()
    # 第 13 题在右列，下伸不会撞到下一行左列；字形比实测横线轻微越右、越下。
    cv2.line(image, (640, 210), (657, 230), (0, 0, 0), 2)
    found, _ = run_detection(image=image)
    boxes = {row["printed_question_label"]: row["proposed_bbox"] for row in found}
    box = boxes["13"]
    assert (box["x"] + box["width"]) * image.shape[1] >= 658
    assert (box["y"] + box["height"]) * image.shape[0] >= 231
    assert (box["y"] + box["height"]) * image.shape[0] < 400
    # 左列上下两题的字号余量相交时，共用中界，不能让相邻题框重叠。
    assert boxes["12"]["y"] + boxes["12"]["height"] <= boxes["14"]["y"] + 1e-9


@pytest.mark.parametrize("replacement", ["12", "12.0", "x=(12)", "(12)+1", "(12分)"])
def test_bare_handwritten_numbers_and_calculation_expressions_are_not_question_markers(replacement):
    content = tokens()
    content[3]["text"] = replacement
    found, _ = run_detection(content)
    assert "12" not in [row["printed_question_label"] for row in found]


def test_no_static_fill_heading_cannot_use_underlines_from_math_process():
    content = [token for token in tokens() if token["line"] != "heading"]
    assert run_detection(content)[0] == []


def test_no_real_underline_cannot_create_a_fill_roi():
    image = fill_page()
    cv2.rectangle(image, (180, 215), (660, 230), (255, 255, 255), -1)
    found, _ = run_detection(image=image)
    assert [row["printed_question_label"] for row in found] == ["14"]


def test_low_confidence_marker_is_not_filled_from_question_count():
    content = tokens()
    content[5]["confidence"] = 44
    found, _ = run_detection(content)
    assert [row["printed_question_label"] for row in found] == ["12", "14"]


def test_unread_suffix_requires_another_static_question_word_at_matching_font_height():
    content = [token for token in tokens() if token["text"] != "题" or token["line"] == "heading"]
    content[5]["text"] = "(14)"
    assert run_detection(content)[0] == []


def test_duplicate_question_markers_are_not_resolved_by_reading_order():
    content = tokens()
    content[6]["text"] = "(12)题"
    found, _ = run_detection(content)
    assert [row["printed_question_label"] for row in found] == ["13"]


def test_ocr_failure_does_not_add_guessed_regions():
    with patch("grading_agent.template_analysis_fill_regions._read_section", return_value=([], "LOCAL_FILL_RECOVERY_OCR_TIMEOUT")):
        found, reasons = discover_fill_regions(fill_page(), {"page_no": 1, "sha256": "a" * 64},
                                              detect_printed_frames(fill_page(), 1), ["12"])
    assert found == []
    assert reasons == ["LOCAL_FILL_RECOVERY_OCR_TIMEOUT"]


def test_section_calls_share_one_page_deadline():
    image = fill_page()
    sections = [(100, 100, 700, 400), (100, 500, 700, 800)]
    reader = SimpleNamespace(calls=[])
    def ocr(crop, *, timeout):
        reader.calls.append(timeout)
        return [], None
    with patch("grading_agent.template_analysis_fill_regions._measured_sections", return_value=(sections, [])):
        found, reasons = discover_fill_regions(image, {}, [], ["12"], ocr=ocr, clock=iter([0, 0, 0, 6.1]).__next__)
    assert found == []
    assert reader.calls == [2]
    assert reasons == ["LOCAL_FILL_RECOVERY_BUDGET_EXCEEDED"]


def test_recheck_reads_visible_low_confidence_marker_without_lowering_confidence_gate():
    image = fill_page()
    frames = detect_printed_frames(image, 1)
    section = _measured_sections(image, frames)[0][0]
    seen = []
    def ocr(crop, *, timeout):
        factor = crop.shape[1] / (section[2] - section[0])
        seen.append(factor)
        content = tokens()
        content[6]["confidence"] = 30 if factor == 1 else 90
        return [{**token, "x": (token["x"] - section[0]) * factor, "y": (token["y"] - section[1]) * factor,
                 "width": token["width"] * factor, "height": token["height"] * factor} for token in content], None
    found, reasons = discover_fill_regions(image, {"page_no": 1, "sha256": "a" * 64}, frames,
                                          ["12", "13", "14"], ocr=ocr)
    assert reasons == []
    assert seen == [1, 2]
    assert [row["printed_question_label"] for row in found] == ["12", "13", "14"]
    assert "LOCAL_FILL_LOCAL_OCR_RECHECKED" in found[-1]["reason_codes"]


def test_recheck_can_reuse_heading_verified_on_the_same_measured_section():
    image = fill_page()
    frames = detect_printed_frames(image, 1)
    section = _measured_sections(image, frames)[0][0]
    seen = []

    def ocr(crop, *, timeout):
        factor = crop.shape[1] / (section[2] - section[0])
        seen.append(factor)
        content = tokens()
        if factor == 1:
            # 首轮标题完整，但括号题号后的“题”漏字；放大后题号恢复，标题却漏掉。
            content = [token for token in content if token["text"] != "题" or token["line"] == "heading"]
            for token in content:
                if token["text"] == "(14)题":
                    token["text"] = "(14)"
        else:
            content = [token for token in content if token["line"] != "heading"]
        return [{**token, "x": (token["x"] - section[0]) * factor, "y": (token["y"] - section[1]) * factor,
                 "width": token["width"] * factor, "height": token["height"] * factor} for token in content], None

    found, reasons = discover_fill_regions(image, {"page_no": 1, "sha256": "a" * 64}, frames,
                                          ["12", "13", "14"], ocr=ocr)
    assert reasons == []
    assert seen == [1, 2]
    assert [row["printed_question_label"] for row in found] == ["12", "13", "14"]
    assert all("LOCAL_FILL_LOCAL_OCR_RECHECKED" in row["reason_codes"] for row in found)


@pytest.mark.parametrize("suffix_confidence,expected", [(90, ["12", "13", "14"]), (44, [])])
def test_short_fill_heading_can_reuse_rechecked_static_suffix_without_replacing_reliable_marker_geometry(
        suffix_confidence, expected):
    image = fill_page()
    frames = detect_printed_frames(image, 1)
    section = _measured_sections(image, frames)[0][0]
    seen = []

    def ocr(crop, *, timeout):
        factor = crop.shape[1] / (section[2] - section[0])
        seen.append(factor)
        if factor == 1:
            # 精确 JPEG 故障：标题只读到“填空”，三个括号题号可靠，但都漏了后缀。
            content = [token for token in tokens() if token["text"] != "题"]
            next(token for token in content if token["text"] == "(14)题")["text"] = "(14)"
        else:
            # 二次只读清真实“题”字；不能要求同时重读成功全部题号，也不能放低置信度。
            content = [{"text": "题", "x": 165, "y": 200, "width": 15, "height": 20,
                        "confidence": suffix_confidence, "line": "marker"}]
        return [{**token, "x": (token["x"] - section[0]) * factor,
                 "y": (token["y"] - section[1]) * factor, "width": token["width"] * factor,
                 "height": token["height"] * factor} for token in content], None

    found, reasons = discover_fill_regions(image, {"page_no": 1, "sha256": "a" * 64}, frames,
                                          ["12", "13", "14"], ocr=ocr)
    assert reasons == []
    assert seen == [1, 2]
    assert [row["printed_question_label"] for row in found] == expected
    for row in found:
        assert "LOCAL_FILL_LOCAL_OCR_RECHECKED" in row["reason_codes"]
        assert row["proposed_bbox"]["y"] + row["proposed_bbox"]["height"] < .4


@pytest.mark.parametrize("strip_label,confidence,expected", [("(14)题", 90, ["12", "13", "14"]),
                                                            ("(04)题", 90, ["12", "13"]),
                                                            ("(14)题", 44, ["12", "13"])])
def test_marker_strip_recovers_merged_number_only_after_independent_reading(strip_label, confidence, expected):
    image = fill_page()
    frames = detect_printed_frames(image, 1)
    section = _measured_sections(image, frames)[0][0]
    seen = []

    def ocr(crop, *, timeout):
        seen.append(crop.shape[:2])
        if len(seen) == 1:
            # 第14题静态字与手写答案粘成一个长词，整区不能再依靠正式题数补一个题号。
            content = [token for token in tokens() if token["text"] not in {"题", "(14)题"}]
            content.append({"text": "42", "x": 120, "y": 300, "width": 250, "height": 35,
                            "confidence": 30, "line": "merged"})
            return [{**token, "x": token["x"] - section[0], "y": token["y"] - section[1]}
                    for token in content], None
        if crop.shape[1] == (section[2] - section[0]) * 2:
            # 二倍整区只补回13题旁的静态后缀，14题仍读成不在本考试的04。
            return [{"text": "题", "x": (445 - section[0]) * 2, "y": (200 - section[1]) * 2,
                     "width": 15 * 2, "height": 20 * 2, "confidence": 90, "line": "marker"}], None
        left = max(section[0], 190 - 20 * 5)
        top = max(section[1], 332 - 20 * 1.75)
        return [{"text": strip_label, "x": 120 - left, "y": 310 - top,
                 "width": 55, "height": 20, "confidence": confidence, "line": "marker"}], None

    found, reasons = discover_fill_regions(image, {"page_no": 1, "sha256": "a" * 64}, frames,
                                          ["12", "13", "14"], ocr=ocr)
    assert reasons == []
    assert [row["printed_question_label"] for row in found] == expected
    assert len(seen) == 3
    assert seen[-1][1] < section[2] - section[0]
    if "14" in expected:
        by_label = {row["printed_question_label"]: row for row in found}
        assert "LOCAL_FILL_MARKER_STRIP_VERIFIED" in by_label["14"]["reason_codes"]
        assert by_label["14"]["proposed_bbox"]["y"] > .22
        assert by_label["12"]["proposed_bbox"]["y"] + by_label["12"]["proposed_bbox"]["height"] <= by_label["14"]["proposed_bbox"]["y"]


@pytest.mark.parametrize("observed,allowed,strip_label,confidence,real_line,expected_calls,expected", [
    (["12", "13", "14"], ["12", "13", "14"], "(14)题", 90, True, 2, ["12", "13", "14"]),
    ([], ["12", "13", "14"], "(14)题", 90, True, 1, ["12", "13"]),
    (["12", "13"], ["12", "13", "14"], "(14)题", 90, True, 1, ["12", "13"]),
    (["14"], ["12", "13"], "(14)题", 90, True, 1, ["12", "13"]),
    (["14"], ["12", "13", "14"], "(04)题", 90, True, 2, ["12", "13"]),
    (["14"], ["12", "13", "14"], "(14)题", 44, True, 2, ["12", "13"]),
    (["14"], ["12", "13", "14"], "14", 90, True, 2, ["12", "13"]),
    (["14"], ["12", "13", "14"], "(14)题", 90, False, 1, ["12", "13"]),
])
def test_observed_missing_label_only_schedules_static_strip_and_never_supplies_ocr_evidence(
        observed, allowed, strip_label, confidence, real_line, expected_calls, expected):
    image = fill_page()
    if not real_line:
        cv2.rectangle(image, (180, 325), (360, 340), (255, 255, 255), -1)
    frames = detect_printed_frames(image, 1)
    section = _measured_sections(image, frames)[0][0]
    seen = []

    def ocr(crop, *, timeout):
        assert 0 < timeout <= 2
        seen.append(crop.shape[:2])
        if len(seen) == 1:
            # 整块14题号漏字，连粘连词也没有；模型观察只能安排补读，不能直接变成题框。
            return [{**token, "x": token["x"] - section[0], "y": token["y"] - section[1]}
                    for token in tokens() if token["text"] != "(14)题"], None
        left, top = max(section[0], 190 - 20 * 5), max(section[1], 332 - 20 * 1.75)
        assert crop.shape[1] == 190 - 1 - left
        return [{"text": strip_label, "x": 120 - left, "y": 310 - top, "width": 55,
                 "height": 20, "confidence": confidence, "line": "strip"}], None

    found, reasons = discover_fill_regions(image, {"page_no": 1, "sha256": "a" * 64}, frames,
                                          allowed, observed_fill_labels=observed, ocr=ocr)
    assert reasons == []
    assert len(seen) == expected_calls
    assert [row["printed_question_label"] for row in found] == expected
    if "14" in expected:
        by_label = {row["printed_question_label"]: row for row in found}
        assert "LOCAL_FILL_MARKER_STRIP_VERIFIED" in by_label["14"]["reason_codes"]
        assert by_label["12"]["proposed_bbox"]["y"] + by_label["12"]["proposed_bbox"]["height"] <= by_label["14"]["proposed_bbox"]["y"]


def test_private_real_browser_marker_strip_replay_without_running_ocr():
    root = Path(__file__).resolve().parents[2]
    source = root / "output/position-template-20261019/isolated/browser-input-page1.json"
    report = root / "output/position-template-20261019/fill-recovery-browser-linux-after-private.json"
    if not source.exists() or not report.exists():
        pytest.skip("private local replay assets are unavailable")
    fixture = json.loads(source.read_text(encoding="utf-8"))
    recorded = json.loads(report.read_text(encoding="utf-8"))
    image = np.array(Image.open(io.BytesIO(base64.b64decode(fixture["data_base64"]))).convert("RGB"))
    events = iter(recorded["events"])

    def reader(crop, *, timeout):
        event = next(events)
        assert list(crop.shape[:2]) == event["shape"]
        return [{**token, "line": tuple(token["line"])} for token in event["tokens"]], event["reason"]

    found, reasons = discover_fill_regions(image, {"page_no": 1, "sha256": fixture["sha256"]},
                                          detect_printed_frames(image, 1), ["12", "13", "14"], ocr=reader)
    assert reasons == []
    assert [row["printed_question_label"] for row in found] == ["12", "13", "14"]
    assert "LOCAL_FILL_MARKER_STRIP_VERIFIED" in found[-1]["reason_codes"]
    assert (found[-1]["proposed_bbox"]["y"] + found[-1]["proposed_bbox"]["height"]) * 1132 == pytest.approx(697)


def test_section_count_is_bounded_even_when_clock_does_not_advance():
    image = fill_page()
    sections = [(100, 100, 700, 400)] * 8
    seen = []
    with patch("grading_agent.template_analysis_fill_regions._measured_sections", return_value=(sections, [])):
        _, reasons = discover_fill_regions(image, {}, [], ["12"], ocr=lambda crop, timeout: (seen.append(timeout) or [], None), clock=lambda: 0)
    assert len(seen) == 4
    assert reasons == ["LOCAL_FILL_RECOVERY_BUDGET_EXCEEDED"]


def test_local_recovery_is_appended_after_frame_refinement_and_still_needs_review(tmp_path, monkeypatch):
    monkeypatch.setenv("EDUGRADE_TEMPLATE_ANALYSIS_DIAGNOSTICS_DIR", str(tmp_path))
    image = fill_page()
    request = payload(image)
    request.update(question_labels=["12", "13", "14"], fill_blank_question_labels=["12", "13", "14"])
    discovered, _ = run_detection()
    with patch("grading_agent.template_analysis.discover_fill_regions", return_value=(discovered, [])):
        result = TemplateAnalyzer(StructuredModel({"candidates": []}), prompt_variant="v6").analyze(request)
    assert result["candidates"] == discovered
    assert result["status"] == "review_required"
    assert result["model_usage"]["request_count"] == 1
    trace = json.loads(next(tmp_path.glob("*.json")).read_text(encoding="utf8"))
    assert [row["origin"] for row in trace["candidate_summary"]] == ["local_detector"] * 3
    assert "PRIVATE" not in json.dumps(trace)


def test_old_unknown_whole_column_is_kept_without_expanding_local_roi():
    discovered, _ = run_detection()
    before = copy.deepcopy(discovered)
    existing = [{"role": "uncertain", "printed_question_label": None, "association_status": "unassociated",
                 "proposed_bbox": {"x": .125, "y": .1, "width": .75, "height": .8},
                 "reason_codes": [], "semantic_check": "needs_review"}]
    _merge_local_fill_regions(existing, discovered, {"width": 800, "height": 1000})
    assert len(existing) == 4
    assert "LOCAL_FILL_SUBREGIONS_DETECTED" in existing[0]["reason_codes"]
    assert [row["proposed_bbox"] for row in existing[1:]] == [row["proposed_bbox"] for row in before]
    assert all("LOCAL_FILL_PARENT_REGION_REQUIRES_REVIEW" in row["reason_codes"] for row in existing[1:])


def test_independent_answer_overlap_stays_a_visible_conflict():
    discovered, _ = run_detection()
    existing = [{"role": "answer", "printed_question_label": "15", "association_status": "associated",
                 "proposed_bbox": discovered[0]["proposed_bbox"], "reason_codes": [], "semantic_check": "supported"}]
    _merge_local_fill_regions(existing, discovered[:1], {"width": 800, "height": 1000})
    assert all("REGION_CONFLICT" in row["reason_codes"] for row in existing)
    assert existing[0]["association_status"] == "ambiguous"


def test_local_reader_timeout_returns_only_a_fixed_reason():
    with patch("grading_agent.template_analysis_fill_regions.shutil.which", return_value="tesseract"), \
            patch("grading_agent.template_analysis_fill_regions.subprocess.run", side_effect=subprocess.TimeoutExpired("private", 2)):
        assert _read_section(fill_page(), timeout=2) == ([], "LOCAL_FILL_RECOVERY_OCR_TIMEOUT")


def test_local_reader_quotes_in_handwriting_do_not_swallow_following_rows():
    header = "level\tpage_num\tblock_num\tpar_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext\n"
    data = header + '5\t1\t1\t1\t1\t1\t10\t10\t10\t10\t90\t"\n' + "5\t1\t1\t1\t2\t1\t10\t30\t20\t10\t90\t(14)\n"
    with patch("grading_agent.template_analysis_fill_regions.shutil.which", return_value="tesseract"), \
            patch("grading_agent.template_analysis_fill_regions.subprocess.run", return_value=SimpleNamespace(returncode=0, stdout=data.encode())):
        actual, reason = _read_section(fill_page(), timeout=2)
    assert reason is None
    assert len(actual) == 2
    assert actual[1]["text"] == "(14)"


@pytest.mark.parametrize("data", [
    b"level\ttext\n5\t(14)\n",
    b"level\tpage_num\tblock_num\tpar_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext\n5\t1\n",
])
def test_truncated_or_missing_tsv_columns_are_a_local_failure(data):
    with patch("grading_agent.template_analysis_fill_regions.shutil.which", return_value="tesseract"), \
            patch("grading_agent.template_analysis_fill_regions.subprocess.run", return_value=SimpleNamespace(returncode=0, stdout=data)):
        assert _read_section(fill_page(), timeout=2) == ([], "LOCAL_FILL_RECOVERY_OCR_FAILED")
