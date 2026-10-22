import base64
import copy
import hashlib
import json
import logging
import os

import cv2
import numpy as np
import pytest

from scripts.evals.replay_template_analysis_v7 import (
    ReplayInputError,
    load_corpus,
    main,
    replay_corpus,
)

REPORT_CASE_FIELDS = {
    "case_id", "prompt_variant", "semantic_status", "reason_codes", "candidate_count", "needs_review_count",
    "supported_count", "model_calls", "request_contract", "error_code", "unreported_reason_count",
}


def _corpus():
    raw = cv2.imencode(".png", np.full((80, 100, 3), 255, np.uint8))[1].tobytes()
    box = {"x": .1, "y": .1, "width": .7, "height": .6}
    request = {"request_id": "offline-v7-contract-0001", "schema_version": "answer-sheet-analysis-v1",
               "page": {"page_no": 1, "width": 100, "height": 80, "media_type": "image/png",
                        "data_base64": base64.b64encode(raw).decode(), "sha256": hashlib.sha256(raw).hexdigest()},
               "candidates": [{"candidate_id": "synthetic-1", "bbox": box, "evidence_refs": ["synthetic:1"]}],
               "question_labels": ["1", "15"], "objective_question_labels": ["1"], "writing_state": "blank"}
    output = {"candidates": [{"candidate_id": "synthetic-1", "page_no": 1, "role": "objective_group",
                              "printed_question_label": None, "identity_label": None, "proposed_bbox": box,
                              "association_status": "unassociated", "evidence_refs": ["synthetic:1"],
                              "reason_codes": ["PRIVATE_STUDENT_946281"]}]}
    return {"schema_version": 1, "cases": [{"case_id": "synthetic-legacy", "prompt_variant": "v7-text",
                                           "request": request, "model_output": output}]}


@pytest.mark.parametrize("change", ["id", "variant", "duplicate", "extra"])
def test_invalid_corpus_has_fixed_error_without_echoing_input(change):
    corpus = _corpus()
    if change == "id":
        corpus["cases"][0]["case_id"] = "private student path /etc"
    elif change == "variant":
        corpus["cases"][0]["prompt_variant"] = ["not-a-variant"]
    elif change == "duplicate":
        corpus["cases"].append(copy.deepcopy(corpus["cases"][0]))
    else:
        corpus["cases"][0]["executable"] = "ignored malicious fixture code"
    with pytest.raises(ReplayInputError, match="^REPLAY_CASE_INVALID$"):
        replay_corpus(corpus)


def test_nonfinite_json_and_large_file_fail_before_analysis(tmp_path):
    fixture = tmp_path / "fixture.json"
    fixture.write_text('{"private":NaN}')
    with pytest.raises(ReplayInputError, match="^REPLAY_JSON_NONFINITE$"):
        load_corpus(fixture)
    with fixture.open("wb") as handle:
        handle.truncate(16 * 1024 * 1024 + 1)
    with pytest.raises(ReplayInputError, match="^REPLAY_INPUT_TOO_LARGE$"):
        load_corpus(fixture)


def test_report_does_not_echo_model_codes_observations_or_page_data():
    report = replay_corpus(_corpus())
    text = json.dumps(report)
    assert report["contract_replay_only"] is True and report["supplier_called"] is False
    assert report["external_ocr_called"] is False and report["database_written"] is False
    assert report["cases"][0]["semantic_status"] == "completed"
    assert report["cases"][0]["request_contract"]["requested_output_mode"] == "schema_contract"
    assert report["cases"][0]["supported_count"] == 0
    assert report["cases"][0]["unreported_reason_count"] >= 1
    for private in ("PRIVATE_STUDENT_946281", "data_base64", "visual_evidence", "observed_question_labels"):
        assert private not in text


@pytest.mark.parametrize("count", [0, 17])
def test_corpus_case_count_is_bounded_before_analyzer_import(count):
    corpus = _corpus()
    corpus["cases"] *= count
    with pytest.raises(ReplayInputError, match="^REPLAY_CORPUS_INVALID$"):
        replay_corpus(corpus)


def test_report_has_same_fixed_fields_for_accepted_and_rejected_requests():
    corpus = _corpus()
    rejected = copy.deepcopy(corpus["cases"][0])
    rejected["case_id"] = "synthetic-rejected"
    rejected["request"]["page"]["sha256"] = "0" * 64
    corpus["cases"].append(rejected)
    report = replay_corpus(corpus)
    assert all(set(case) == REPORT_CASE_FIELDS for case in report["cases"])
    assert report["cases"][1]["error_code"] == "REPLAY_REQUEST_REJECTED"
    assert report["cases"][1]["model_calls"] == 0
    assert report["cases"][1]["unreported_reason_count"] == 0


def test_replay_disables_inherited_content_capture_and_external_processes(monkeypatch, tmp_path):
    # 即使维护者的 shell 开了原文诊断，离线夹具也不能触发网络、OCR 进程或内容落盘。
    monkeypatch.setenv("EDUGRADE_TEMPLATE_ANALYSIS_DIAGNOSTICS_DIR", str(tmp_path))
    monkeypatch.setenv("EDUGRADE_TEMPLATE_ANALYSIS_CAPTURE_CONTENT", "true")

    def forbidden(*_args, **_kwargs):
        raise AssertionError("offline replay attempted external IO")

    monkeypatch.setattr("socket.create_connection", forbidden)
    monkeypatch.setattr("subprocess.run", forbidden)
    monkeypatch.setattr("grading_agent.managed_model.public_json_transport", forbidden)
    monkeypatch.setattr("grading_agent.template_analysis_semantics.read_static_ocr", forbidden)
    monkeypatch.setattr("grading_agent.template_analysis_semantics.detect_barcode", forbidden)
    report = replay_corpus(_corpus())
    assert report["cases"][0]["model_calls"] == 1
    assert report["cases"][0]["error_code"] is None
    assert list(tmp_path.iterdir()) == []
    assert report["raw_content_emitted"] is False


@pytest.mark.parametrize("role,static,identity,expected", [
    ("objective_group", "选择题", "name_line", "MODEL_OBS_OBJECTIVE_IDENTITY_CONFLICT"),
    ("other", "姓名", "name_line", "MODEL_OBS_OTHER_CONTENT_CONFLICT"),
])
def test_fixed_cross_category_observation_reasons_are_reported_without_observation_text(role, static, identity, expected):
    corpus = _corpus()
    case = corpus["cases"][0]
    case["prompt_variant"] = "v7-evidence"
    row = case["model_output"]["candidates"][0]
    row.update(role=role, observed_question_labels=["1"], objective_items=[], visual_evidence={
        "static_label": static, "option_pattern": "bubble_options", "identity_pattern": identity,
        "barcode_pattern": "none", "boundary_basis": "printed_frame", "anchor_bbox": None,
    })
    result = replay_corpus(corpus)["cases"][0]
    assert expected in result["reason_codes"]
    assert result["needs_review_count"] == 1 and result["supported_count"] == 0
    assert "visual_evidence" not in json.dumps(result)


def test_cli_rejects_invalid_corpus_with_fixed_error_and_without_input_echo(monkeypatch, tmp_path, capsys):
    corpus = _corpus()
    corpus["cases"][0]["case_id"] = "PRIVATE_STUDENT_946281 invalid ID"
    path = tmp_path / "private-input.json"
    path.write_text(json.dumps(corpus), encoding="utf-8")
    monkeypatch.setattr("sys.argv", ["replay_template_analysis_v7.py", str(path)])
    assert main() == 1
    captured = capsys.readouterr()
    assert captured.out == ""
    assert json.loads(captured.err) == {"error_code": "REPLAY_CASE_INVALID", "contract_replay_only": True}


def test_library_replay_does_not_echo_fixture_request_identifier_to_inherited_info_logs(caplog):
    corpus = _corpus()
    corpus["cases"][0]["request"]["request_id"] = "PRIVATE_STUDENT_946281"
    with caplog.at_level(logging.INFO):
        report = replay_corpus(corpus)
    assert report["cases"][0]["semantic_status"] == "completed"
    assert "PRIVATE_STUDENT_946281" not in caplog.text
    assert "PRIVATE_STUDENT_946281" not in json.dumps(report)


@pytest.mark.parametrize("alias", ["same", "dotdot", "hardlink", "symlink"])
def test_cli_refuses_input_alias_as_report_without_modifying_corpus(alias, monkeypatch, tmp_path, capsys):
    source = tmp_path / "corpus.json"
    original = json.dumps(_corpus()).encode()
    source.write_bytes(original)
    output = source
    if alias == "dotdot":
        child = tmp_path / "child"
        child.mkdir()
        output = child / ".." / source.name
    elif alias == "hardlink":
        output = tmp_path / "report.json"
        os.link(source, output)
    elif alias == "symlink":
        output = tmp_path / "report.json"
        try:
            output.symlink_to(source)
        except OSError:
            pytest.skip("Windows symlink privilege is unavailable")
    monkeypatch.setattr("sys.argv", ["replay_template_analysis_v7.py", str(source), "--output", str(output)])
    assert main() == 1
    captured = capsys.readouterr()
    assert captured.out == ""
    assert json.loads(captured.err) == {"error_code": "REPLAY_OUTPUT_ALIASES_INPUT", "contract_replay_only": True}
    assert source.read_bytes() == original
    assert output.read_bytes() == original


def test_bad_request_is_reported_with_fixed_code_and_no_model_call():
    corpus = _corpus()
    corpus["cases"][0]["request"]["page"]["sha256"] = "0" * 64
    report = replay_corpus(corpus)
    assert report["cases"][0]["error_code"] == "REPLAY_REQUEST_REJECTED"
    assert report["cases"][0]["model_calls"] == 0 and report["cases"][0]["candidate_count"] == 0
