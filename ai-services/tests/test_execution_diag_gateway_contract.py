"""跨语言诊断必须接受相同事实，防止网关契约把未知用量或结束原因放宽。"""

import copy
import json

import pytest
from helpers import ROOT
from jsonschema import Draft202012Validator


def test_gateway_diagnostic_schema_matches_error_contracts():
    spec = json.loads((ROOT / "services/api-gateway/openapi/edugrade-api.openapi.json").read_text(encoding="utf-8"))
    gateway = copy.deepcopy(spec["components"]["schemas"]["ExecutionDiagnostics"])
    gateway["properties"]["usage"] = spec["components"]["schemas"]["DiagnosticTokenUsage"]
    for version in ("v1", "v2"):
        contract = json.loads((ROOT / f"contracts/grading-agent/{version}/error.schema.json").read_text(encoding="utf-8"))
        assert gateway == contract["$defs"]["execution_diagnostics"]


def test_public_subjective_failure_response_contract_includes_diagnostics():
    spec = json.loads((ROOT / "services/api-gateway/openapi/edugrade-api.openapi.json").read_text(encoding="utf-8"))
    schemas = spec["components"]["schemas"]
    response = spec["paths"]["/api/v1/answer-segments/{id}/subjective-ai-grade"]["post"]["responses"]
    for status in ("502", "503"):
        assert response[status]["content"]["application/json"]["schema"]["$ref"] == "#/components/schemas/SubjectiveAIExecutionFailure"
    schema = copy.deepcopy(schemas["SubjectiveAIExecutionFailure"])
    schema["properties"]["execution_diagnostics"] = copy.deepcopy(schemas["ExecutionDiagnostics"])
    schema["properties"]["execution_diagnostics"]["properties"]["usage"] = schemas["DiagnosticTokenUsage"]
    body = {"error": {"code": "ai_output_incomplete", "message": "AI 建议未完成，请人工评分"},
            "execution_diagnostics": {"failure_kind": "output_truncated", "finish_outcome": "output_limit",
                                      "attempts": 1, "provider_request_count": 1, "usage_status": "unavailable", "recovery_attempted": False},
            "retryable": False, "task_failure_recorded": False}
    validator = Draft202012Validator(schema)
    assert validator.is_valid(body)
    body["provider_body"] = "unfinished model output"
    assert not validator.is_valid(body)


@pytest.mark.parametrize("change", [
    {"failure_kind": "arbitrary_private_provider_error"},
    {"provider_request_count": 2},
    {"usage_status": "reported", "usage": {}},
    {"usage_status": "partial"},
    {"usage": {"input_tokens": 0}},
    {"usage_status": "reported", "usage": {"input_tokens": 1, "output_tokens": 1, "total_tokens": 2**63}},
    {"recovery_attempted": True},
])
def test_gateway_contract_rejects_unsafe_diagnostic_combinations(change):
    spec = json.loads((ROOT / "services/api-gateway/openapi/edugrade-api.openapi.json").read_text(encoding="utf-8"))
    schema = copy.deepcopy(spec["components"]["schemas"]["ExecutionDiagnostics"])
    schema["properties"]["usage"] = spec["components"]["schemas"]["DiagnosticTokenUsage"]
    fact = {"failure_kind": "output_truncated", "finish_outcome": "output_limit", "attempts": 1,
            "provider_request_count": 1, "usage_status": "unavailable", "recovery_attempted": False}
    fact.update(change)
    assert not Draft202012Validator(schema).is_valid(fact)
