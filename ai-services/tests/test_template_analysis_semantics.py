import copy
import json
import subprocess
from types import SimpleNamespace
from unittest.mock import patch

import numpy as np
import pytest
from grading_agent.template_analysis import TemplateAnalyzer
from grading_agent.template_analysis_observations import review_model_observations
from grading_agent.template_analysis_semantics import (
    LocalContentEvidence,
    read_static_ocr,
)
from test_template_analysis import (
    StructuredModel,
    discovered_row,
    payload,
    unframed_page,
)


def token(text, box):
    return {"text": text, **box}


def row(role, label=None, box=None, identity=None):
    result = discovered_row(label, box, role)
    result.update(identity_label=identity, association_status="associated" if role == "answer" else "unassociated")
    return result


def evidence_factory(tokens, *, groups=None, barcode=None):
    def build(image, page):
        evidence = LocalContentEvidence(image, page, ocr=lambda _: (tokens, None), barcode=barcode)
        if groups is not None:
            evidence._option_groups = groups
        return evidence
    return build


def incident_identity(label):
    # 来自 2026-10-18 真实响应的两条错误身份记录；仅保留静态类别、坐标和观察，不含填写值。
    y, height = ((.33127208480565373, .08745583038869258) if label == "姓名"
                 else (.41872791519434627, .08303886925795052))
    box = {"x": .066875, "y": y, "width": .28125, "height": height}
    value = row("identity", box=box, identity=label)
    value.update(observed_question_labels=[], objective_items=[],
                 visual_evidence={"static_label": label, "option_pattern": "none",
                                  "identity_pattern": "name_line" if label == "姓名" else "number_grid",
                                  "barcode_pattern": "none", "boundary_basis": "label_and_field",
                                  "anchor_bbox": copy.deepcopy(box)})
    return value


@pytest.mark.parametrize("identity", ["姓名", "准考证号"])
def test_live_identity_response_with_a_full_frame_label_anchor_triggers_local_recheck(identity):
    value = incident_identity(identity)
    before = copy.deepcopy(value["visual_evidence"])
    review_model_observations(value, {"question_labels": [str(number) for number in range(1, 20)]})
    assert value["semantic_check"] == "needs_review"
    assert "MODEL_OBS_IDENTITY_LABEL_ANCHOR_NOT_LOCALIZED" in value["reason_codes"]
    assert value["visual_evidence"] == before


@pytest.mark.parametrize("identity", ["姓名", "准考证号"])
@pytest.mark.parametrize("text", ["A B C D A B C D", "一、选择题", "Multiple Choice"])
def test_live_wrong_identity_claim_is_rejected_by_independent_choice_content(identity, text):
    value = incident_identity(identity)
    # 先验证局部内容防线本身；标签锚点已改为小框，避免被更早的模型观察防线拦下。
    box = value["proposed_bbox"]
    value["visual_evidence"]["anchor_bbox"] = {"x": box["x"], "y": box["y"], "width": .03, "height": .02}
    review_model_observations(value, {"question_labels": ["1", "2"], "objective_question_labels": ["1", "2"]})
    assert value.get("semantic_check") != "needs_review"
    value["reason_codes"].append("MULTIMODAL_OBSERVATIONS_CONSISTENT")
    evidence = LocalContentEvidence(np.full((200, 300, 3), 255, np.uint8),
                                    {"page_no": 1, "sha256": "0" * 64},
                                    ocr=lambda _: ([token(text, box)], None))
    evidence._option_groups = lambda _: [[(1, 1, 10, 10)] * 4]
    evidence.review([value], {"question_labels": ["1", "2"], "objective_question_labels": ["1", "2"]})
    assert value["semantic_check"] == "needs_review"
    assert value["role"] == "uncertain"
    assert value["identity_label"] is None
    assert "SEMANTIC_IDENTITY_OPTION_CONTENT_CONFLICT" in value["reason_codes"]
    assert "MULTIMODAL_CONTENT_CONSISTENT" not in value["reason_codes"]


@pytest.mark.parametrize("text", ["0 1 2 3 4 5 6 7 8 9", "A B C D", "非选择题", "请用 A B C D 作答"])
def test_number_grid_or_incidental_letters_do_not_veto_consistent_identity(text):
    value = incident_identity("准考证号")
    value["reason_codes"] = ["MULTIMODAL_OBSERVATIONS_CONSISTENT"]
    evidence = LocalContentEvidence(np.full((200, 300, 3), 255, np.uint8),
                                    {"page_no": 1, "sha256": "0" * 64},
                                    ocr=lambda _: ([token(text, value["proposed_bbox"])], None))
    evidence._option_groups = lambda _: [[(1, 1, 10, 10)] * 4]
    evidence.review([value], {"question_labels": ["1"]})
    assert value["semantic_check"] == "supported"
    assert value["identity_label"] == "准考证号"


def test_joined_ocr_words_do_not_turn_a_single_option_example_into_repeated_options():
    value = incident_identity("准考证号")
    value["reason_codes"] = ["MULTIMODAL_OBSERVATIONS_CONSISTENT"]
    box = value["proposed_bbox"]
    tokens = [token(letter, {**box, "x": box["x"] + index * .02, "width": .01})
              for index, letter in enumerate("ABCD")]
    tokens += [{**token("A B C D", box), "_joined_static_words": True} for _ in range(4)]
    evidence = LocalContentEvidence(np.full((200, 300, 3), 255, np.uint8),
                                    {"page_no": 1, "sha256": "0" * 64}, ocr=lambda _: (tokens, None))
    evidence._option_groups = lambda _: [[(1, 1, 10, 10)] * 4]
    evidence.review([value], {"question_labels": ["1"]})
    assert value["semantic_check"] == "supported"


def test_repeated_choice_letters_without_measured_options_are_not_sufficient_identity_counterevidence():
    value = incident_identity("准考证号")
    value["reason_codes"] = ["MULTIMODAL_OBSERVATIONS_CONSISTENT"]
    evidence = LocalContentEvidence(np.full((200, 300, 3), 255, np.uint8),
                                    {"page_no": 1, "sha256": "0" * 64},
                                    ocr=lambda _: ([token("A B C D A B C D", value["proposed_bbox"])], None))
    evidence._option_groups = lambda _: []
    evidence.review([value], {"question_labels": ["1"]})
    assert value["semantic_check"] == "supported"


def test_choice_letters_outside_identity_do_not_veto_neighboring_field():
    value = incident_identity("准考证号")
    value["reason_codes"] = ["MULTIMODAL_OBSERVATIONS_CONSISTENT"]
    box = value["proposed_bbox"]
    outside = {"x": box["x"], "y": box["y"] - .012, "width": .1, "height": .004}
    evidence = LocalContentEvidence(np.full((200, 300, 3), 255, np.uint8),
                                    {"page_no": 1, "sha256": "0" * 64},
                                    ocr=lambda _: ([token("A B C D A B C D", outside)], None))
    evidence._option_groups = lambda _: [[(1, 1, 10, 10)] * 4]
    evidence.review([value], {"question_labels": ["1"]})
    assert value["semantic_check"] == "supported"


def test_corrected_identity_with_localized_label_is_automatically_usable_without_ocr():
    value = incident_identity("姓名")
    box = value["proposed_bbox"]
    value["visual_evidence"]["anchor_bbox"] = {"x": box["x"], "y": box["y"], "width": .03, "height": .02}
    request = {"question_labels": ["1"]}
    review_model_observations(value, request)
    assert value.get("semantic_check") != "needs_review"
    value["reason_codes"].append("MULTIMODAL_OBSERVATIONS_CONSISTENT")
    evidence = LocalContentEvidence(np.full((200, 300, 3), 255, np.uint8),
                                    {"page_no": 1, "sha256": "0" * 64},
                                    ocr=lambda _: ([], "SEMANTIC_OCR_UNAVAILABLE"))
    evidence._option_groups = lambda _: []
    evidence.review([value], request)
    assert (value["role"], value["semantic_check"]) == ("identity", "supported")
    assert "SEMANTIC_OCR_UNAVAILABLE" in value["reason_codes"]


@pytest.mark.parametrize("role,label,identity,reason", [
    ("objective_group", "15", None, "SEMANTIC_ROLE_QUESTION_TYPE_CONFLICT"),
    ("objective_group", "16", None, "SEMANTIC_ROLE_QUESTION_TYPE_CONFLICT"),
    ("identity", "1", "姓名", "SEMANTIC_ROLE_QUESTION_TYPE_CONFLICT"),
    ("answer", "15", "姓名", "SEMANTIC_ROLE_IDENTITY_CONFLICT"),
])
def test_raw_role_contradictions_survive_cleanup_as_review_reasons(role, label, identity, reason):
    request = payload()
    request.update(question_labels=["1", "15", "16"], objective_question_labels=["1"])
    candidate = row(role, label, request["candidates"][0]["bbox"], identity)
    result = TemplateAnalyzer(StructuredModel({"candidates": [candidate]}), prompt_variant="v6").analyze(request)
    actual = result["candidates"][0]
    assert actual["role"] == "uncertain"
    assert actual["semantic_check"] == "needs_review"
    assert actual["association_status"] == "unassociated"
    assert reason in actual["reason_codes"]
    assert actual["printed_question_label"] == label


def test_empty_model_result_is_completed_but_does_not_claim_template_coverage():
    result = TemplateAnalyzer(StructuredModel({"candidates": []}), prompt_variant="v6").analyze(payload())
    assert result["semantic_status"] == "completed"
    assert result["status"] == "review_required"
    assert result["candidates"] == []


def test_screenshot_wrong_roles_are_not_supported_even_with_real_printed_frames():
    request = payload()
    request.update(question_labels=["1", "9", "15", "16"], objective_question_labels=["1", "9"])
    boxes = [frame["bbox"] for frame in request["candidates"]]
    supplied = [row("objective_group", box=boxes[0]), row("objective_group", box=boxes[1]),
                row("identity", box={"x": .1, "y": .01, "width": .35, "height": .03}, identity="姓名"),
                row("identity", box={"x": .5, "y": .01, "width": .35, "height": .03}, identity="准考证号"),
                row("identity", box={"x": .1, "y": .94, "width": .3, "height": .04}, identity="条码")]
    content = [token("禁止作答", {"x": .12, "y": .95, "width": .2, "height": .02})]
    result = TemplateAnalyzer(StructuredModel({"candidates": supplied}), evidence_factory(content, groups=lambda _: []), prompt_variant="v6").analyze(request)
    assert len(result["candidates"]) == 5
    assert all(candidate["role"] == "uncertain" for candidate in result["candidates"])
    assert all(candidate["semantic_check"] == "needs_review" for candidate in result["candidates"])
    assert "PRINTED_FRAME_VERIFIED" in result["candidates"][0]["reason_codes"]
    assert "SEMANTIC_FORBIDDEN_AREA_DETECTED" in result["candidates"][-1]["reason_codes"]


def test_option_shapes_do_not_prove_choice_when_identity_number_grid_has_no_letters():
    request = payload()
    box = request["candidates"][0]["bbox"]
    candidate = row("objective_group", box=box)
    factory = evidence_factory([token("准考证号", box)], groups=lambda _: [[(1, 1, 10, 10)] * 4])
    result = TemplateAnalyzer(StructuredModel({"candidates": [candidate]}), factory, prompt_variant="v6").analyze(request)
    assert result["candidates"][0]["role"] == "uncertain"


def test_measured_options_and_ocr_letters_support_choice_but_not_confirm_it():
    request = payload()
    box = request["candidates"][0]["bbox"]
    factory = evidence_factory([token("A B C D", box)], groups=lambda _: [[(1, 1, 10, 10)] * 4])
    result = TemplateAnalyzer(StructuredModel({"candidates": [row("objective_group", box=box)]}), factory, prompt_variant="v6").analyze(request)
    candidate = result["candidates"][0]
    assert candidate["semantic_check"] == "supported"
    assert candidate["role"] == "objective_group"
    assert candidate["association_status"] == "unassociated"
    assert result["status"] == "review_required"
    assert any(ref.startswith("local-semantic:") for ref in candidate["evidence_refs"])


@pytest.mark.parametrize("label,text,decode,reason", [
    ("姓名", "姓名", None, "SEMANTIC_IDENTITY_LABEL_VERIFIED"),
    ("条码", "条形码粘贴区", None, "SEMANTIC_BLANK_BARCODE_AREA_VERIFIED"),
    ("条码", "", "barcode", "SEMANTIC_BARCODE_DECODE_VERIFIED"),
    ("二维码", "", "qr", "SEMANTIC_BARCODE_DECODE_VERIFIED"),
])
def test_static_identity_and_blank_barcode_labels_support_categories_without_recording_values(label, text, decode, reason):
    request = payload()
    box = request["candidates"][0]["bbox"]
    factory = evidence_factory([token(text, box)], groups=lambda _: [], barcode=lambda _: decode)
    result = TemplateAnalyzer(StructuredModel({"candidates": [row("identity", box=box, identity=label)]}), factory, prompt_variant="v6").analyze(request)
    candidate = result["candidates"][0]
    assert candidate["semantic_check"] == "supported"
    assert candidate["identity_label"] == label
    assert reason in candidate["reason_codes"]
    assert text not in json.dumps(candidate["evidence_refs"], ensure_ascii=False) if text else True


def test_barcode_stripes_alone_remain_unconfirmed():
    request = payload()
    box = request["candidates"][0]["bbox"]
    factory = evidence_factory([], groups=lambda _: [], barcode=lambda _: "stripes")
    candidate = TemplateAnalyzer(StructuredModel({"candidates": [row("identity", box=box, identity="条码")]}), factory, prompt_variant="v6").analyze(request)["candidates"][0]
    assert candidate["semantic_check"] == "needs_review"
    assert candidate["role"] == "uncertain"
    assert "SEMANTIC_BARCODE_PATTERN_ONLY" in candidate["reason_codes"]


def test_missing_ocr_is_explicit_review_not_semantic_success():
    request = payload()
    box = request["candidates"][0]["bbox"]
    with patch("grading_agent.template_analysis_semantics.shutil.which", return_value=None):
        candidate = TemplateAnalyzer(StructuredModel({"candidates": [row("identity", box=box, identity="姓名")]}), prompt_variant="v6").analyze(request)["candidates"][0]
    assert candidate["role"] == "uncertain"
    assert "SEMANTIC_OCR_UNAVAILABLE" in candidate["reason_codes"]


def test_ocr_runs_once_and_never_copies_student_text_to_output():
    request = payload()
    seen = []
    def build(image, page):
        def ocr(_):
            seen.append(True)
            return [token("PRIVATE STUDENT 123456", request["candidates"][0]["bbox"])], None
        return LocalContentEvidence(image, page, ocr=ocr)
    output = {"candidates": [row("identity", box=frame["bbox"], identity="姓名") for frame in request["candidates"]]}
    result = TemplateAnalyzer(StructuredModel(output), build, prompt_variant="v6").analyze(request)
    assert len(seen) == 1
    assert "PRIVATE" not in json.dumps(result)


def test_tesseract_parser_joins_static_chinese_tokens():
    image = np.full((100, 100, 3), 255, np.uint8)
    tsv = ("level\tpage_num\tblock_num\tpar_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext\n"
           "5\t1\t1\t1\t1\t1\t10\t10\t10\t10\t90\t姓\n"
           "5\t1\t1\t1\t1\t2\t20\t10\t10\t10\t90\t名\n").encode()
    with patch("grading_agent.template_analysis_semantics.shutil.which", return_value="tesseract"), patch(
        "grading_agent.template_analysis_semantics.subprocess.run", return_value=type("R", (), {"returncode": 0, "stdout": tsv})()):
        tokens, reason = read_static_ocr(image)
    assert reason is None
    assert any(item["text"] == "姓名" for item in tokens)


def test_tesseract_tsv_preserves_math_quotes_without_consuming_following_tokens():
    image = np.full((100, 100, 3), 255, np.uint8)
    # Tesseract 原样输出数学引号；CSV 默认引号规则会把下一行题号一起吞进该字段。
    tsv = (b"level\tpage_num\tblock_num\tpar_num\tline_num\tword_num\tleft\ttop\twidth\theight\tconf\ttext\n"
           b'5\t1\t1\t1\t1\t1\t10\t10\t10\t10\t90\t"\n'
           b"5\t1\t2\t1\t1\t1\t10\t30\t20\t10\t90\t(12)\n"
           b'5\t1\t3\t1\t1\t1\t10\t50\t10\t10\t90\t"x"\n')
    with patch("grading_agent.template_analysis_semantics.shutil.which", return_value="tesseract"), patch(
        "grading_agent.template_analysis_semantics.subprocess.run", return_value=SimpleNamespace(returncode=0, stdout=tsv)) as run:
        tokens, reason = read_static_ocr(image)
    assert reason is None
    assert [item["text"] for item in tokens] == ['"', "(12)", '"x"']
    assert run.call_count == 1
    assert run.call_args.kwargs["timeout"] == 12


@pytest.mark.parametrize("result,exception,reason", [
    (None, subprocess.TimeoutExpired("tesseract", 12, output=b"PRIVATE STUDENT"), "SEMANTIC_OCR_TIMEOUT"),
    (None, OSError("PRIVATE PATH"), "SEMANTIC_OCR_PROCESS_FAILED"),
    (SimpleNamespace(returncode=1, stdout=b"PRIVATE STUDENT"), None, "SEMANTIC_OCR_PROCESS_FAILED"),
    (SimpleNamespace(returncode=0, stdout=b"x" * (4 * 1024 * 1024 + 1)), None, "SEMANTIC_OCR_OUTPUT_TOO_LARGE"),
    (SimpleNamespace(returncode=0, stdout=b"level\ttext\n5\tPRIVATE STUDENT\n"), None, "SEMANTIC_OCR_PARSE_FAILED"),
    (SimpleNamespace(returncode=0, stdout=b"\xff"), None, "SEMANTIC_OCR_PARSE_FAILED"),
])
def test_tesseract_failures_return_distinct_safe_codes_without_retry(result, exception, reason):
    image = np.full((100, 100, 3), 255, np.uint8)
    with patch("grading_agent.template_analysis_semantics.shutil.which", return_value="tesseract"), patch(
        "grading_agent.template_analysis_semantics.subprocess.run", return_value=result, side_effect=exception) as run:
        tokens, actual = read_static_ocr(image)
    assert tokens == []
    assert actual == reason
    assert run.call_count == 1


@pytest.mark.parametrize("bad_row", [
    "5\t2\t1\t1\tinvalid\t10\t10\t10\t90\tPRIVATE",
    "5\t2\t1\t1\t10\t10\t10\t10\t90\tPRIVATE\textra",
    "5\t2\t1",
])
def test_malformed_tesseract_rows_do_not_leave_partial_evidence(bad_row):
    image = np.full((100, 100, 3), 255, np.uint8)
    tsv = ("level\tblock_num\tpar_num\tline_num\tleft\ttop\twidth\theight\tconf\ttext\n"
           "5\t1\t1\t1\t10\t10\t10\t10\t90\t姓名\n"
           f"{bad_row}\n").encode()
    with patch("grading_agent.template_analysis_semantics.shutil.which", return_value="tesseract"), patch(
        "grading_agent.template_analysis_semantics.subprocess.run", return_value=SimpleNamespace(returncode=0, stdout=tsv)):
        tokens, reason = read_static_ocr(image)
    assert tokens == []
    assert reason == "SEMANTIC_OCR_PARSE_FAILED"


@pytest.mark.parametrize("text", ["surname", "username", "nameValue", "姓名张三"])
def test_student_values_and_body_words_are_not_static_identity_labels(text):
    request = payload()
    box = request["candidates"][0]["bbox"]
    factory = evidence_factory([token(text, box)], groups=lambda _: [])
    candidate = TemplateAnalyzer(StructuredModel({"candidates": [
        row("identity", box=box, identity="姓名"),
    ]}), factory, prompt_variant="v6").analyze(request)["candidates"][0]
    assert candidate["semantic_check"] == "needs_review"
    assert candidate["role"] == "uncertain"


@pytest.mark.parametrize("text,supported", [("17", False), ("17.5", False), ("17.", True), ("第17题", True)])
def test_only_question_markers_in_starting_band_support_answer_association(text, supported):
    request = payload()
    box = request["candidates"][0]["bbox"]
    printed = {"x": box["x"] + .01, "y": box["y"] + .005, "width": .04, "height": .01}
    candidate = TemplateAnalyzer(StructuredModel({"candidates": [
        row("answer", "17", box=box),
    ]}), evidence_factory([token(text, printed)], groups=lambda _: []), prompt_variant="v6").analyze(request)["candidates"][0]
    assert (candidate["semantic_check"] == "supported") is supported


def test_forbidden_area_outside_answer_box_does_not_override_local_question_evidence():
    request = payload()
    box = request["candidates"][0]["bbox"]
    content = [token("17.", {"x": box["x"] + .01, "y": box["y"] + .005, "width": .03, "height": .01}),
               token("禁止作答", {"x": box["x"] + .02, "y": box["y"] - .012, "width": .04, "height": .005})]
    candidate = TemplateAnalyzer(StructuredModel({"candidates": [
        row("answer", "17", box=box),
    ]}), evidence_factory(content, groups=lambda _: []), prompt_variant="v6").analyze(request)["candidates"][0]
    assert candidate["semantic_check"] == "supported"
    assert "SEMANTIC_FORBIDDEN_AREA_DETECTED" not in candidate["reason_codes"]


@pytest.mark.parametrize("marker", [
    "请勿填写", "禁止填写", "不得在此处作答", "请勿在此处答题", "非作答区",
])
def test_forbidden_marker_variants_inside_candidate_block_automatic_acceptance(marker):
    request = payload()
    box = request["candidates"][0]["bbox"]
    candidate = row("identity", box=box, identity="姓名")
    result = TemplateAnalyzer(
        StructuredModel({"candidates": [candidate]}),
        evidence_factory([token(marker, box)], groups=lambda _: []),
        prompt_variant="v6",
    ).analyze(request)
    candidate = result["candidates"][0]
    assert candidate["semantic_check"] == "needs_review"
    assert candidate["role"] == "uncertain"
    assert "SEMANTIC_FORBIDDEN_AREA_DETECTED" in candidate["reason_codes"]


def test_split_forbidden_marker_tokens_are_joined_only_within_one_line():
    request = payload()
    box = request["candidates"][0]["bbox"]
    candidate = row("identity", box=box, identity="姓名")
    tokens = [
        token("请勿", {"x": box["x"] + .01, "y": box["y"] + .01, "width": .03, "height": .01}),
        token("填写", {"x": box["x"] + .045, "y": box["y"] + .01, "width": .03, "height": .01}),
    ]
    result = TemplateAnalyzer(
        StructuredModel({"candidates": [candidate]}),
        evidence_factory(tokens, groups=lambda _: []),
        prompt_variant="v6",
    ).analyze(request)
    candidate = result["candidates"][0]
    assert candidate["semantic_check"] == "needs_review"
    assert "SEMANTIC_FORBIDDEN_AREA_DETECTED" in candidate["reason_codes"]


def test_maximum_model_reason_codes_cannot_displace_system_conflicts_or_overflow_saved_draft(tmp_path, monkeypatch):
    monkeypatch.setenv("EDUGRADE_TEMPLATE_ANALYSIS_DIAGNOSTICS_DIR", str(tmp_path))
    monkeypatch.setenv("EDUGRADE_TEMPLATE_ANALYSIS_CAPTURE_CONTENT", "true")
    request = payload(unframed_page(), candidates=[])
    request["writing_state"] = "unknown"
    supplied = [row("answer", "17", box={"x": .1, "y": .1, "width": .8, "height": .5}),
                row("answer", "18", box={"x": .1, "y": .3, "width": .8, "height": .5})]
    model_codes = [f"MODEL_REASON_{index}" for index in range(32)]
    for candidate in supplied:
        candidate["reason_codes"] = model_codes
    def factory(image, page):
        evidence = LocalContentEvidence(image, page, ocr=lambda _: ([], "SEMANTIC_OCR_UNAVAILABLE"))
        evidence._option_groups = lambda _: []
        return evidence
    result = TemplateAnalyzer(StructuredModel({"candidates": supplied}), factory, prompt_variant="v6").analyze(request)
    assert result["semantic_status"] == "completed"
    assert len(result["candidates"]) == 2
    for candidate in result["candidates"]:
        codes = candidate["reason_codes"]
        assert len(codes) <= 36
        assert len(codes) == len(set(codes))
        assert len([code for code in codes if code in model_codes]) == 16
        assert {"MODEL_REASON_CODES_TRUNCATED", "REGION_CONFLICT", "SEMANTIC_REGION_OVERLAP_CONFLICT",
                "SEMANTIC_EVIDENCE_INSUFFICIENT", "SEMANTIC_OCR_UNAVAILABLE"} <= set(codes)
        assert not any(key.startswith("_") for key in candidate)
        assert candidate["semantic_check"] == "needs_review"
    record = json.loads(next(tmp_path.glob("*.json")).read_text(encoding="utf-8"))
    assert json.loads(record["responses"][0]["content_json"])["candidates"][0]["reason_codes"] == model_codes


def test_handwritten_and_reported_conflict_cautions_survive_model_reason_budget():
    request = payload()
    supplied = row("answer", "17", box=request["candidates"][0]["bbox"])
    supplied["reason_codes"] = [*[f"MODEL_REASON_{index}" for index in range(30)],
                               "SEMANTIC_CONTENT_CONFLICT", "HANDWRITTEN_QUESTION_LABEL_REQUIRES_REVIEW"]
    printed = token("17.", {"x": .11, "y": .13, "width": .03, "height": .01})
    candidate = TemplateAnalyzer(StructuredModel({"candidates": [supplied]}),
                                 evidence_factory([printed], groups=lambda _: []), prompt_variant="v6").analyze(request)["candidates"][0]
    assert candidate["semantic_check"] == "needs_review"
    assert "MODEL_CAUTION_REQUIRES_REVIEW" in candidate["reason_codes"]
    assert "HANDWRITTEN_QUESTION_LABEL_REQUIRES_REVIEW" in candidate["reason_codes"]
    assert "MODEL_REASON_CODES_TRUNCATED" in candidate["reason_codes"]
