import json
from copy import deepcopy
from types import SimpleNamespace

import pytest
from ocr_worker.api import APIError
from ocr_worker.math_runner import MathVerificationClient
from ocr_worker.runner import WorkerConfig
from ocr_worker.symbolic_runner import (
    SymbolicVerificationRunner,
    build_symbolic_verifications,
)


def equation(value):
    return {"kind": "equation", "children": [{"kind": "symbol", "value": "x"}, {"kind": "number", "value": str(value)}]}


def contract():
    return {
        "formulas": [{"id": "f1", "ast": equation(2)}, {"id": "f2", "ast": equation(3)}],
        "solution_graph": {
            "steps": [{"id": "s1", "formula_ids": ["f1"]}, {"id": "s2", "formula_ids": ["f2"]}],
            "edges": [{"from_step_id": "s1", "to_step_id": "s2", "kind": "derives"}],
        },
    }


class Verifier:
    def verify_transition(self, previous, following):
        assert previous == equation(2)
        assert following == equation(3)
        return {"status": "contradicted", "relation": "different_solution_set", "evidence": ["{2}", "{3}"],
                "engine_version": "1.14.0", "ruleset_version": "v1"}

    def solve(self, ast, variable):
        assert variable == "x"
        return {"status": "verified", "solution_set": "{3}", "engine_version": "1.14.0", "ruleset_version": "v1"}


def test_symbolic_http_client_uses_restricted_ast_and_real_domain(monkeypatch):
    sent = []

    class Response:
        def __init__(self):
            self.headers = {"Content-Type": "application/json"}

        def __enter__(self):
            return self

        def __exit__(self, *args):
            pass

        def read(self, amount=-1):
            return b'{"status":"verified"}'[:amount]

    def urlopen(req, timeout):
        sent.append((req.full_url, json.loads(req.data), req.headers, timeout))
        return Response()

    monkeypatch.setattr("ocr_worker.math_runner._open_request", urlopen)
    client = MathVerificationClient("http://math:8092", "token")
    client.normalize("x=2")
    client.verify_transition(equation(2), equation(3))
    client.solve(equation(3), "x")
    assert [item[0].rsplit("/", 1)[1] for item in sent] == ["normalize", "verify-transition", "solve"]
    assert all(item[1]["domain"] == {"domain": "real"} and item[3] == 5 for item in sent)
    assert sent[1][1]["previous_ast"] == equation(2)
    assert sent[2][1]["variable"] == "x"


def test_derives_edges_and_leaf_solution_sets_preserve_fact_provenance():
    source = contract()
    original = deepcopy(source)
    checks = build_symbolic_verifications(source, Verifier())
    assert source == original
    assert len(checks) == 2
    assert checks[0]["status"] == "contradicted"
    assert checks[0]["reason_code"] == "different_solution_set"
    assert checks[0]["details"]["from_step_id"] == "s1"
    assert checks[0]["details"]["from_formula_id"] == "f1"
    assert checks[0]["formula_id"] == "f2"
    assert checks[1]["reason_code"] == "solution_set_computed"
    assert checks[1]["kind"] == "constraint"
    assert checks[1]["details"]["solution_set"] == "{3}"


def test_reading_order_is_not_treated_as_equivalence():
    source = contract()
    source["solution_graph"]["edges"][0]["kind"] = "next"
    checks = build_symbolic_verifications(source, Verifier())
    assert all(check["kind"] != "equivalence" for check in checks)


def test_missing_ast_and_no_supported_checks_fail_closed():
    source = contract()
    for formula in source["formulas"]:
        del formula["ast"]
        formula["canonical_latex"] = "unsupported"
    verifier = SimpleNamespace(normalize=lambda _: {"status": "uncertain"})
    checks = build_symbolic_verifications(source, verifier)
    assert all(check["status"] == "uncertain" and check["confidence"] == 0 for check in checks)
    no_formulas = {"formulas": [], "solution_graph": {"steps": [], "edges": []}}
    assert build_symbolic_verifications(no_formulas, verifier)[0]["reason_code"] == "no_supported_symbolic_checks"


class API:
    def __init__(self, *, mismatch=False, unavailable=False):
        self.binding = {"id": "task-1", "artifact_id": "artifact-1", "artifact_version": 2,
                        "input_hash": "sha256:test", "correction_revision": 3}
        self.task = {"id": "task-1", "lease_token": "lease", "tenant_id": "tenant-1",
                     "source_type": "math_understanding_artifact", "source_id": "artifact-1",
                     "payload": {key: value for key, value in self.binding.items() if key != "id"}}
        self.mismatch, self.unavailable = mismatch, unavailable
        self.completed, self.failed = [], []

    def claim_math_verification_tasks(self, worker, lease):
        return [self.task]

    def activate_task(self, task, worker):
        assert task is self.task

    def heartbeat_task(self, runtime_task_id, lease_token, worker_instance_id, lease_seconds, timeout_seconds, *, progress=None):
        pass

    def get_math_verification_input(self, task):
        if self.unavailable:
            raise APIError("unavailable")
        binding = dict(self.binding)
        if self.mismatch:
            binding["artifact_version"] += 1
        return {"task": binding, "contract": contract()}

    def complete_math_verification_task(self, task, payload):
        self.completed.append(payload)

    def fail_math_verification_task(self, task, payload):
        self.failed.append(payload)


@pytest.mark.parametrize("mismatch,unavailable,retryable", [(False, False, None), (True, False, False), (False, True, True)])
def test_runner_binds_revision_and_uses_source_specific_terminal_adapter(mismatch, unavailable, retryable):
    api = API(mismatch=mismatch, unavailable=unavailable)
    runner = SymbolicVerificationRunner(api=api, verifier=Verifier(), config=WorkerConfig(worker_id="worker-1"))
    assert runner.process_once() == 1
    if retryable is None:
        assert api.failed == []
        assert api.completed[0]["artifact_id"] == "artifact-1"
        assert api.completed[0]["artifact_version"] == 2
        assert api.completed[0]["correction_revision"] == 3
        assert len(api.completed[0]["verifications"]) == 2
    else:
        assert api.completed == []
        assert api.failed[0]["retryable"] is retryable
