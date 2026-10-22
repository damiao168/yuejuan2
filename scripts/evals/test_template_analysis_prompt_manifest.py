"""登记检查只保护冻结输入；不证明多模态识别效果或供应商输出模式。"""

import copy
import hashlib
import json

import pytest

from scripts.evals import check_template_analysis_prompt_manifest as tool


@pytest.fixture(scope="module")
def current_manifest():
    return tool.build_manifest()


def test_generated_manifest_matches_source_and_frozen_v6(current_manifest, monkeypatch):
    from grading_agent.template_analysis import TemplateAnalyzer

    monkeypatch.delenv("EDUGRADE_TEMPLATE_ANALYSIS_PROMPT_VARIANT", raising=False)
    assert TemplateAnalyzer().prompt_variant == current_manifest["default_prompt_variant"] == "v7-evidence"
    assert tool.check_manifest(current_manifest) == "PROMPT_MANIFEST_MATCH"
    versions = current_manifest["variants"]
    assert set(versions) == {"v6", "v7-text", "v7-evidence", "v8-layout"}
    assert versions["v6"]["system_prompt_sha256"] == (
        "088644693ed68bc951f1dfdee34351a8b55ef73f61cc99341be846873262a5ee")
    assert versions["v6"]["reference_model_output_schema_sha256"] == (
        versions["v7-text"]["reference_model_output_schema_sha256"])
    assert versions["v7-evidence"]["reference_model_output_schema_sha256"] != (
        versions["v7-text"]["reference_model_output_schema_sha256"])
    assert [versions[key]["include_observations"] for key in ("v6", "v7-text", "v7-evidence")] == [False, False, True]


def test_builder_uses_actual_messages_without_calling_analyzer(current_manifest, monkeypatch):
    import grading_agent.template_analysis as analysis

    original = analysis._messages
    observed = []

    def messages(payload, variant):
        observed.append((payload["page"]["page_no"], payload["candidates"], variant))
        output = original(payload, variant)
        if variant == "v7-evidence":
            output[0]["content"] += "synthetic-contract-drift"
        return output

    def forbidden(*_args, **_kwargs):
        raise AssertionError("manifest generation must not run analysis")

    monkeypatch.setattr(analysis, "_messages", messages)
    monkeypatch.setattr(analysis.TemplateAnalyzer, "analyze", forbidden)
    changed = tool.build_manifest()
    assert observed == [(1, [], key) for key in ("v6", "v7-text", "v7-evidence", "v8-layout")]
    assert changed["variants"]["v7-evidence"]["system_prompt_sha256"] != (
        current_manifest["variants"]["v7-evidence"]["system_prompt_sha256"])
    assert tool.check_manifest(changed) == "PROMPT_MANIFEST_DRIFT"


def test_reference_schema_digest_is_canonical_and_explicitly_not_per_request(current_manifest):
    from grading_agent.template_analysis_schema import model_output_schema

    reference = current_manifest["reference_schema"]
    assert reference["scope"] == "reference_only_not_per_request"
    assert reference["candidate_ids"] == [] and reference["page_no"] == 1
    assert "request_contract" in reference["per_request_digest"]
    schema = model_output_schema([], 1, True)
    digest = hashlib.sha256(json.dumps(schema, ensure_ascii=False, sort_keys=True,
                                      separators=(",", ":"), allow_nan=False).encode("utf-8")).hexdigest()
    assert current_manifest["variants"]["v7-evidence"]["reference_model_output_schema_sha256"] == digest
    changed = model_output_schema(["synthetic-candidate"], 2, True)
    assert hashlib.sha256(tool.canonical_json(changed).encode("utf-8")).hexdigest() != digest
    with pytest.raises(ValueError):
        tool.canonical_json({"value": float("nan")})


def test_default_cli_never_writes_and_explicit_write_regenerates(current_manifest, monkeypatch, tmp_path, capsys):
    path = tmp_path / "new-directory" / "manifest.v7.json"
    monkeypatch.setattr(tool, "DEFAULT_MANIFEST", path)
    assert tool.main([]) == 1
    assert capsys.readouterr().out.strip() == "PROMPT_MANIFEST_MISSING"
    assert not path.parent.exists()
    assert tool.main(["--write"]) == 0
    assert capsys.readouterr().out.strip() == "PROMPT_MANIFEST_WRITTEN"
    before, stat = path.read_bytes(), path.stat().st_mtime_ns
    assert tool.main([]) == 0
    assert capsys.readouterr().out.strip() == "PROMPT_MANIFEST_MATCH"
    assert path.read_bytes() == before and path.stat().st_mtime_ns == stat
    changed = copy.deepcopy(current_manifest)
    changed["variants"]["v7-text"]["system_prompt_sha256"] = "0" * 64
    path.write_text(json.dumps(changed), encoding="utf-8")
    assert tool.main([]) == 1
    assert capsys.readouterr().out.strip() == "PROMPT_MANIFEST_DRIFT"
    assert tool.main(["--write"]) == 0
    assert tool.check_manifest(current_manifest, path) == "PROMPT_MANIFEST_MATCH"


@pytest.mark.parametrize("content", [
    b"not-json", b"\xff", b'{"value":NaN}', b'{"value":1,"value":2}', b"x" * (64 * 1024 + 1),
], ids=["malformed", "invalid-utf8", "nonfinite", "duplicate-key", "oversized"])
def test_invalid_manifest_has_fixed_error_without_echoing_content(current_manifest, tmp_path, content):
    path = tmp_path / "manifest.json"
    path.write_bytes(content)
    assert tool.check_manifest(current_manifest, path) == "PROMPT_MANIFEST_INVALID"


def test_json_boolean_cannot_impersonate_manifest_integer(current_manifest, tmp_path):
    # Python 中 True == 1，直接比较 dict 会把登记格式漂移漏掉；使用规范化 JSON 比较。
    changed = copy.deepcopy(current_manifest)
    changed["schema_version"] = True
    path = tmp_path / "manifest.json"
    path.write_text(json.dumps(changed), encoding="utf-8")
    assert tool.check_manifest(current_manifest, path) == "PROMPT_MANIFEST_DRIFT"
