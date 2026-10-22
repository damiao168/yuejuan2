import json
import logging
import os
import time
from types import SimpleNamespace
from unittest.mock import Mock

import pytest
from grading_agent.template_analysis import TemplateAnalyzer
from grading_agent.template_analysis_diagnostics import (
    AnalysisTrace,
    capture_provider_response,
)
from grading_agent.template_analysis_model import prepare_template_model
from grading_agent.template_analysis_schema import request_schema
from grading_agent.template_analysis_semantics import SEMANTIC_VERSION
from helpers import settings
from test_template_analysis import StructuredModel, payload


def provider_model(content, finish="stop"):
    response = {"model": "actual-provider-version", "choices": [{"finish_reason": finish,
                "message": {"role": "assistant", "content": content}}]}
    source = SimpleNamespace(settings=settings(adapter_type="openai_compatible", model_api_key="test-private-credential"),
                             transport=Mock(return_value=response))
    return prepare_template_model(source)


def test_parse_failure_is_captured_before_repair_without_logging_content_or_credentials(tmp_path, monkeypatch, caplog):
    monkeypatch.setenv("EDUGRADE_TEMPLATE_ANALYSIS_DIAGNOSTICS_DIR", str(tmp_path))
    monkeypatch.setenv("EDUGRADE_TEMPLATE_ANALYSIS_CAPTURE_CONTENT", "true")
    content = '{"candidates": ["PRIVATE STUDENT",'
    model = provider_model(content, "length")
    with caplog.at_level(logging.INFO):
        result = TemplateAnalyzer(model, prompt_variant="v6").analyze(payload())
    record = json.loads(next(tmp_path.glob("*.json")).read_text(encoding="utf-8"))
    assert result["semantic_status"] == "failed"
    assert record["metadata"]["attempts"][0]["partial"] is True
    assert record["metadata"]["source_sha256"] == result["source_sha256"]
    assert record["metadata"]["versions"]["prompt"].endswith("v6")
    assert record["metadata"]["versions"]["semantic"] == result["versions"]["semantic"] == SEMANTIC_VERSION
    assert json.loads(record["responses"][0]["content_json"]) == content
    assert record["metadata"]["attempts"][0]["actual_model"] == "actual-provider-version"
    assert "PRIVATE STUDENT" not in caplog.text
    assert "test-private-credential" not in caplog.text
    assert "test-private-credential" not in json.dumps(record)


def test_content_capture_is_opt_in_even_when_diagnostic_directory_is_configured(tmp_path, monkeypatch):
    monkeypatch.setenv("EDUGRADE_TEMPLATE_ANALYSIS_DIAGNOSTICS_DIR", str(tmp_path))
    monkeypatch.delenv("EDUGRADE_TEMPLATE_ANALYSIS_CAPTURE_CONTENT", raising=False)
    TemplateAnalyzer(provider_model('{"candidates":[]}'), prompt_variant="v6").analyze(payload())
    record = json.loads(next(tmp_path.glob("*.json")).read_text(encoding="utf-8"))
    assert "responses" not in record
    assert record["metadata"]["content_captured"] is False
    assert len(record["metadata"]["attempts"][0]["content_sha256"]) == 64


def test_nullable_repair_does_not_rewrite_captured_raw_content(tmp_path, monkeypatch):
    monkeypatch.setenv("EDUGRADE_TEMPLATE_ANALYSIS_DIAGNOSTICS_DIR", str(tmp_path))
    monkeypatch.setenv("EDUGRADE_TEMPLATE_ANALYSIS_CAPTURE_CONTENT", "true")
    content = '{"candidates": [{"role":"identity"}]}'
    TemplateAnalyzer(provider_model(content), prompt_variant="v6").analyze(payload())
    record = json.loads(next(tmp_path.glob("*.json")).read_text(encoding="utf-8"))
    captured = json.loads(record["responses"][0]["content_json"])
    assert captured == content
    assert "identity_label" not in captured


def test_invalid_diagnostic_directory_fails_closed_without_breaking_review_result(monkeypatch, caplog):
    monkeypatch.setenv("EDUGRADE_TEMPLATE_ANALYSIS_DIAGNOSTICS_DIR", "relative-not-controlled")
    monkeypatch.setenv("EDUGRADE_TEMPLATE_ANALYSIS_CAPTURE_CONTENT", "true")
    with caplog.at_level(logging.INFO):
        result = TemplateAnalyzer(StructuredModel({"candidates": []}), prompt_variant="v6").analyze(payload())
    assert result["semantic_status"] == "completed"
    assert '"diagnostics_write_failed":true' in caplog.text
    assert '"content_captured":false' in caplog.text


def test_request_scoped_trace_does_not_mix_nested_or_finished_runs():
    first = AnalysisTrace(payload(), {}, None)
    second = AnalysisTrace(payload(), {}, None)
    with first.active():
        capture_provider_response({"choices": [{"message": {"content": "first"}}]})
        with second.active():
            capture_provider_response({"choices": [{"message": {"content": "second"}}]})
        capture_provider_response({"choices": [{"message": {"content": "first-again"}}]})
    capture_provider_response({"choices": [{"message": {"content": "outside"}}]})
    assert len(first.metadata["attempts"]) == 2
    assert len(second.metadata["attempts"]) == 1
    assert first.metadata["run_id"] != second.metadata["run_id"]


def test_trusted_context_and_request_id_are_retained_but_not_forwarded_to_model(tmp_path, monkeypatch):
    monkeypatch.setenv("EDUGRADE_TEMPLATE_ANALYSIS_DIAGNOSTICS_DIR", str(tmp_path))
    request = payload()
    request["diagnostic_context"] = {
        "template_id": "trusted-template", "template_revision": 7,
        "source_pdf_sha256": "a" * 64, "question_snapshot_sha256": "b" * 64,
    }
    model = StructuredModel({"candidates": []})
    TemplateAnalyzer(model, prompt_variant="v6").analyze(request)
    record = json.loads(next(tmp_path.glob("*.json")).read_text(encoding="utf-8"))
    assert record["metadata"]["diagnostic_context"] == request["diagnostic_context"]
    assert record["metadata"]["request_id"] == request["request_id"]
    assert record["metadata"]["page_width"] == request["page"]["width"]
    assert record["metadata"]["page_height"] == request["page"]["height"]
    assert "trusted-template" not in json.dumps(model.calls)
    assert set(request_schema()["properties"]["diagnostic_context"]["required"]) == set(request["diagnostic_context"])


def test_unexpected_processing_failure_is_recorded_without_exception_text(tmp_path, monkeypatch):
    monkeypatch.setenv("EDUGRADE_TEMPLATE_ANALYSIS_DIAGNOSTICS_DIR", str(tmp_path))
    model = StructuredModel({"candidates": []})
    factory = Mock(side_effect=RuntimeError("PRIVATE STUDENT FAILURE"))
    with pytest.raises(RuntimeError, match="PRIVATE STUDENT FAILURE"):
        TemplateAnalyzer(model, factory, prompt_variant="v6").analyze(payload())
    record = json.loads(next(tmp_path.glob("*.json")).read_text(encoding="utf-8"))
    assert record["metadata"]["status"] == "failed"
    assert record["metadata"]["request_count"] == 1
    assert record["metadata"]["reason_codes"] == ["ANALYSIS_PROCESSING_FAILED"]
    assert "PRIVATE STUDENT FAILURE" not in json.dumps(record)


def test_retention_removes_only_expired_controlled_files(tmp_path, monkeypatch):
    monkeypatch.setenv("EDUGRADE_TEMPLATE_ANALYSIS_DIAGNOSTICS_DIR", str(tmp_path))
    monkeypatch.setenv("EDUGRADE_TEMPLATE_ANALYSIS_RETENTION_SECONDS", "10")
    expired = tmp_path / ("1" * 32 + ".json")
    expired.write_text("{}", encoding="utf-8")
    unrelated = tmp_path / "retained.json"
    unrelated.write_text("{}", encoding="utf-8")
    folder = tmp_path / ("2" * 32 + ".json")
    folder.mkdir()
    for item in (expired, unrelated, folder):
        os.utime(item, (time.time() - 60, time.time() - 60))
    AnalysisTrace(payload(), {}, None).finish("completed", [], [])
    assert not expired.exists()
    assert unrelated.is_file()
    assert folder.is_dir()


def test_retention_does_not_unlink_symlinks_or_their_targets(tmp_path, monkeypatch):
    monkeypatch.setenv("EDUGRADE_TEMPLATE_ANALYSIS_DIAGNOSTICS_DIR", str(tmp_path))
    monkeypatch.setenv("EDUGRADE_TEMPLATE_ANALYSIS_MAX_FILES", "1")
    target = tmp_path / "keep.txt"
    target.write_text("private unrelated", encoding="utf-8")
    link = tmp_path / ("3" * 32 + ".json")
    try:
        link.symlink_to(target)
    except OSError:
        pytest.skip("creating symlinks is unavailable on this host")
    AnalysisTrace(payload(), {}, None).finish("completed", [], [])
    assert link.is_symlink()
    assert target.read_text(encoding="utf-8") == "private unrelated"


@pytest.mark.parametrize("setting,limit", [
    ("EDUGRADE_TEMPLATE_ANALYSIS_MAX_FILES", "1"),
    ("EDUGRADE_TEMPLATE_ANALYSIS_MAX_BYTES", "1000"),
])
def test_capacity_limits_remove_oldest_records_first(tmp_path, monkeypatch, setting, limit):
    monkeypatch.setenv("EDUGRADE_TEMPLATE_ANALYSIS_DIAGNOSTICS_DIR", str(tmp_path))
    monkeypatch.setenv(setting, limit)
    older = tmp_path / ("4" * 32 + ".json")
    older.write_text("x" * 2000, encoding="utf-8")
    os.utime(older, (time.time() - 60, time.time() - 60))
    trace = AnalysisTrace(payload(), {}, None)
    trace.finish("completed", [], [])
    assert not older.exists()
    assert (tmp_path / (trace.metadata["run_id"] + ".json")).is_file()


def test_single_record_over_capacity_is_removed_and_reported_as_not_captured(tmp_path, monkeypatch, caplog):
    monkeypatch.setenv("EDUGRADE_TEMPLATE_ANALYSIS_DIAGNOSTICS_DIR", str(tmp_path))
    monkeypatch.setenv("EDUGRADE_TEMPLATE_ANALYSIS_CAPTURE_CONTENT", "true")
    monkeypatch.setenv("EDUGRADE_TEMPLATE_ANALYSIS_MAX_BYTES", "1")
    with caplog.at_level(logging.INFO):
        TemplateAnalyzer(provider_model('{"candidates":[]}'), prompt_variant="v6").analyze(payload())
    assert list(tmp_path.glob("*.json")) == []
    assert '"content_captured":false' in caplog.text


def test_candidate_summary_does_not_keep_model_personal_value_as_unknown_question_label(tmp_path, monkeypatch):
    monkeypatch.setenv("EDUGRADE_TEMPLATE_ANALYSIS_DIAGNOSTICS_DIR", str(tmp_path))
    from test_template_analysis import output_for
    request = payload()
    output = output_for(request)
    output["candidates"][0]["printed_question_label"] = "PRIVATE STUDENT VALUE"
    result = TemplateAnalyzer(StructuredModel(output), prompt_variant="v6").analyze(request)
    record = json.loads(next(tmp_path.glob("*.json")).read_text(encoding="utf-8"))
    assert "PRIVATE STUDENT VALUE" not in json.dumps(result)
    assert "PRIVATE STUDENT VALUE" not in json.dumps(record)


def test_candidate_summary_keeps_local_detector_origin_separate_from_model(tmp_path, monkeypatch):
    monkeypatch.setenv("EDUGRADE_TEMPLATE_ANALYSIS_DIAGNOSTICS_DIR", str(tmp_path))
    trace = AnalysisTrace(payload(), {}, None)
    # 同次运行中同时保留两类候选的来源，复盘时不能把局部补框归因于供应商响应。
    trace.finish("completed", [
        {"candidate_id": "model-candidate", "origin": "model", "semantic_check": "needs_review"},
        {"candidate_id": "local-fill-candidate", "origin": "local_detector", "semantic_check": "needs_review"},
    ], [])
    record = json.loads(next(tmp_path.glob("*.json")).read_text(encoding="utf-8"))
    assert [(row["candidate_id"], row["origin"]) for row in record["candidate_summary"]] == [
        ("model-candidate", "model"), ("local-fill-candidate", "local_detector"),
    ]
    assert record["metadata"]["needs_review_count"] == 2
