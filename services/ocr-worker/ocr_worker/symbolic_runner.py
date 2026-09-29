from __future__ import annotations

import time
from typing import Any

from .api import APIError, AuthenticationError
from .math_runner import MathVerificationClient
from .runner import WorkerConfig, _LeaseHeartbeat


class SymbolicVerificationRunner:
    """Second-stage consumer of immutable mathematical evidence, never images or scores."""

    def __init__(self, *, api: Any, verifier: MathVerificationClient, config: WorkerConfig) -> None:
        self.api = api
        self.verifier = verifier
        self.config = config

    def process_once(self) -> int:
        tasks = self.api.claim_math_verification_tasks(self.config.worker_id, self.config.lease_seconds)
        for task in tasks:
            self._process(task)
        return len(tasks)

    def _process(self, runtime_task: dict[str, Any]) -> None:
        started = time.monotonic()
        task_id = str(runtime_task["id"])
        lease = str(runtime_task["lease_token"])
        tenant_id = str(runtime_task["tenant_id"])
        self.api.activate_task(runtime_task, self.config.worker_id)
        with _LeaseHeartbeat(
            api=self.api, runtime_task_id=task_id, lease_token=lease, worker_id=self.config.worker_id,
            interval=self.config.heartbeat_interval, lease_seconds=self.config.lease_seconds,
            request_timeout=self.config.heartbeat_timeout, tenant_id=tenant_id,
        ) as heartbeat:
            try:
                if runtime_task.get("source_type") != "math_understanding_artifact":
                    raise ValueError("invalid symbolic task source")
                task_input = self.api.get_math_verification_input(task_id, tenant_id)
                binding = task_input["task"]
                payload = runtime_task["payload"]
                if binding["id"] != task_id or binding["artifact_id"] != runtime_task["source_id"]:
                    raise ValueError("symbolic task identity mismatch")
                # 绑定冻结证据版本及人工修订号，防止旧任务把核验结果写到更新后的证据上。
                for key in ("artifact_id", "artifact_version", "input_hash", "correction_revision"):
                    if binding[key] != payload[key]:
                        raise ValueError("symbolic task version mismatch")
                checks = build_symbolic_verifications(task_input["contract"], self.verifier, heartbeat)
                heartbeat.raise_if_failed()
                heartbeat.stop()
                self.api.complete_math_verification_task(task_id, {
                    "lease_token": lease, "duration_ms": int((time.monotonic() - started) * 1000),
                    "artifact_id": binding["artifact_id"], "artifact_version": binding["artifact_version"],
                    "correction_revision": binding["correction_revision"], "verifications": checks,
                }, tenant_id)
            except AuthenticationError:
                raise
            except (APIError, KeyError, TypeError, ValueError) as exc:
                heartbeat.stop()
                self.api.fail_math_verification_task(task_id, {
                    "lease_token": lease, "retryable": isinstance(exc, APIError),
                    "error_code": "math_symbolic_verification_failed", "error_detail": {},
                    "duration_ms": int((time.monotonic() - started) * 1000),
                }, tenant_id)


def build_symbolic_verifications(contract: dict[str, Any], verifier: Any, heartbeat: Any = None) -> list[dict[str, Any]]:
    graph = contract["solution_graph"]
    steps = {step["id"]: step for step in graph["steps"]}
    active_ids = {formula_id for step in graph["steps"] for formula_id in step.get("formula_ids", [])}
    formulas = {formula["id"]: formula for formula in contract["formulas"] if formula["id"] in active_ids}
    checks: list[dict[str, Any]] = []
    normalized_asts: dict[str, dict[str, Any]] = {}
    # Re-normalize in stage two, so temporary parser/service outages in stage
    # one do not permanently prevent mathematical verification.
    for formula_id, formula in formulas.items():
        if heartbeat is not None:
            heartbeat.raise_if_failed()
        ast = formula.get("ast")
        if not isinstance(ast, dict):
            result = verifier.normalize(formula.get("canonical_latex") or formula.get("raw_latex") or "")
            ast = result.get("ast")
        if isinstance(ast, dict):
            normalized_asts[formula_id] = ast
        else:
            checks.append(_check(f"sympy-parse-{formula_id}", "constraint", {
                "status": "uncertain", "reason_code": "unsupported_expression",
            }, formula_id=formula_id))

    seen_edges: set[tuple[str, str]] = set()
    for edge in graph.get("edges", []):
        # Reading order alone is not evidence of an equivalent transformation.
        if edge["kind"] != "derives":
            continue
        source, target = steps[edge["from_step_id"]], steps[edge["to_step_id"]]
        source_ids, target_ids = source.get("formula_ids", []), target.get("formula_ids", [])
        if not source_ids or not target_ids:
            continue
        from_id, to_id = source_ids[-1], target_ids[0]
        if (from_id, to_id) in seen_edges:
            continue
        seen_edges.add((from_id, to_id))
        if heartbeat is not None:
            heartbeat.raise_if_failed()
        result = {"status": "uncertain", "reason_code": "missing_formula_ast"}
        if from_id in normalized_asts and to_id in normalized_asts:
            result = verifier.verify_transition(normalized_asts[from_id], normalized_asts[to_id])
        check = _check(f"sympy-transition-{from_id}-{to_id}", "equivalence", result,
                       formula_id=to_id, step_id=target["id"])
        check["details"].update({"from_step_id": source["id"], "from_formula_id": from_id})
        checks.append(check)

    outgoing = {edge["from_step_id"] for edge in graph.get("edges", [])}
    for step in graph["steps"]:
        if step["id"] in outgoing:
            continue
        for formula_id in step.get("formula_ids", []):
            ast = normalized_asts.get(formula_id)
            if ast is None or ast.get("kind") != "equation":
                continue
            symbols = _symbols(ast)
            if len(symbols) != 1:
                continue
            if heartbeat is not None:
                heartbeat.raise_if_failed()
            result = verifier.solve(ast, next(iter(symbols)))
            # Computing a set is not proof that the student's conclusion is
            # complete. Preserve it as a constraint fact, not a rubric award.
            check = _check(f"sympy-solve-{formula_id}", "constraint", result,
                           formula_id=formula_id, step_id=step["id"])
            check["reason_code"] = result.get("reason_code", "solution_set_computed")
            checks.append(check)
    if not checks:
        checks.append(_check("sympy-no-supported-checks", "constraint", {
            "status": "uncertain", "reason_code": "no_supported_symbolic_checks",
        }))
    return checks


def _symbols(ast: dict[str, Any]) -> set[str]:
    result = {str(ast["value"])} if ast.get("kind") == "symbol" else set()
    for child in ast.get("children", []):
        result.update(_symbols(child))
    return result


def _check(check_id: str, kind: str, result: dict[str, Any], *, formula_id: str = "", step_id: str = "") -> dict[str, Any]:
    status = result.get("status", "uncertain")
    if status not in {"verified", "contradicted", "uncertain", "not_applicable"}:
        status = "uncertain"
    return {
        "id": check_id, "kind": kind, "formula_id": formula_id, "step_id": step_id, "status": status,
        "reason_code": result.get("reason_code", result.get("relation", "unknown")),
        "domain": result.get("domain", "real"), "constraints": list(result.get("constraints", [])),
        "engine": "sympy", "engine_version": result.get("engine_version", "unavailable"),
        "ruleset_version": result.get("ruleset_version", "yuejuan-math-rules-v1"),
        "confidence": 1.0 if status in {"verified", "contradicted"} else 0.0,
        "details": {key: result[key] for key in ("relation", "evidence", "solution_set") if key in result},
    }
