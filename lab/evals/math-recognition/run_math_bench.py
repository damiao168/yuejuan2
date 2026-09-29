#!/usr/bin/env python3
"""Deterministic MATH-00 contract benchmark.

This runner evaluates supplied predictions only. It does not call a model and
the bundled synthetic fixture is a pipeline smoke test, not a quality claim.
"""

from __future__ import annotations

import argparse
import json
from pathlib import Path
from typing import Any

FORMULA_SUBJECTS = {"mathematics", "physics", "chemistry"}


def load_json(path: Path) -> dict[str, Any]:
    return json.loads(path.read_text(encoding="utf-8"))


def canonical(value: Any) -> str:
    return json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"))


# 这里只忽略空白和括号尺寸命令；代数等价由独立验证指标表达，不在此处推导。
def normalized_formula(value: str) -> str:
    return "".join(value.split()).replace("\\left", "").replace("\\right", "")


def prf(expected: set[Any], actual: set[Any]) -> tuple[int, int, int]:
    return len(expected & actual), len(actual - expected), len(expected - actual)


# 没有分母代表本批未评估该指标，保留 None，不能把空样本当作满分。
def safe_div(numerator: float, denominator: float) -> float | None:
    return numerator / denominator if denominator else None


def score_item(expected: dict[str, Any], actual: dict[str, Any]) -> dict[str, tuple[int, int, int] | int]:
    expected_formulas = {item["id"]: item for item in expected.get("formulas", [])}
    actual_formulas = {item["id"]: item for item in actual.get("formulas", [])}
    formula_total = len(expected_formulas)
    formula_exact = sum(
        1 for key, item in expected_formulas.items()
        if key in actual_formulas and normalized_formula(item.get("latex", "")) == normalized_formula(actual_formulas[key].get("latex", ""))
    )
    ast_total = sum(1 for item in expected_formulas.values() if item.get("ast") is not None)
    ast_exact = sum(
        1 for key, item in expected_formulas.items()
        if item.get("ast") is not None and key in actual_formulas and canonical(item["ast"]) == canonical(actual_formulas[key].get("ast"))
    )
    symbols_expected = set(expected.get("symbols", [])); symbols_actual = set(actual.get("symbols", []))
    relations_expected = {(x["from_id"], x["to_id"], x["kind"]) for x in expected.get("relations", [])}
    relations_actual = {(x["from_id"], x["to_id"], x["kind"]) for x in actual.get("relations", [])}
    steps_expected = {(x["id"], x.get("normalized_text", "")) for x in expected.get("solution_graph", {}).get("steps", [])}
    steps_actual = {(x["id"], x.get("normalized_text", "")) for x in actual.get("solution_graph", {}).get("steps", [])}
    edges_expected = {(x["from_step_id"], x["to_step_id"], x["kind"]) for x in expected.get("solution_graph", {}).get("edges", [])}
    edges_actual = {(x["from_step_id"], x["to_step_id"], x["kind"]) for x in actual.get("solution_graph", {}).get("edges", [])}
    evidence_expected = {(x["rubric_criterion_key"], x["status"]) for x in expected.get("rubric_evidence", [])}
    evidence_actual = {(x["rubric_criterion_key"], x["status"]) for x in actual.get("rubric_evidence", [])}
    equivalence_expected = {(x["id"], x["status"]) for x in expected.get("verifications", []) if x.get("kind") == "equivalence"}
    equivalence_actual = {(x["id"], x["status"]) for x in actual.get("verifications", []) if x.get("kind") == "equivalence"}
    unsafe_suggestion = int(not expected.get("safe_to_suggest", False) and actual.get("suggestion_allowed", False))
    suggestion_total = int(bool(actual.get("suggestion_allowed", False)))
    risky_case = int(bool(expected.get("risky_case", False)))
    risky_recalled = int(bool(expected.get("risky_case", False)) and bool(actual.get("human_review_required", False)))
    return {
        "formula_exact": formula_exact, "formula_total": formula_total,
        "ast_exact": ast_exact, "ast_total": ast_total,
        "symbols": prf(symbols_expected, symbols_actual),
        "relations": prf(relations_expected, relations_actual),
        "steps": prf(steps_expected, steps_actual),
        "edges": prf(edges_expected, edges_actual),
        "rubric_evidence": prf(evidence_expected, evidence_actual),
        "equivalence": prf(equivalence_expected, equivalence_actual),
        "unsafe_suggestion": unsafe_suggestion,
        "suggestion_total": suggestion_total,
        "risky_case": risky_case,
        "risky_recalled": risky_recalled,
    }


def evaluate(
    manifest_path: Path, predictions_dir: Path, dataset: str | None = None
) -> dict[str, Any]:
    totals: dict[str, int] = {"samples": 0, "formula_exact": 0, "formula_total": 0, "ast_exact": 0, "ast_total": 0, "unsafe_suggestion": 0, "suggestion_total": 0, "risky_case": 0, "risky_recalled": 0}
    counts = {name: [0, 0, 0] for name in ("symbols", "relations", "steps", "edges", "rubric_evidence", "equivalence")}
    root = manifest_path.parent
    sample_results: list[dict[str, Any]] = []
    for line in manifest_path.read_text(encoding="utf-8").splitlines():
        if not line.strip():
            continue
        entry = json.loads(line)
        sample_id = entry["sample_id"]
        if entry.get("subject_code") not in FORMULA_SUBJECTS:
            raise ValueError(f"formula benchmark does not accept subject {entry.get('subject_code')!r}")
        expected = load_json(root / entry["ground_truth"])
        actual = load_json(predictions_dir / f"{sample_id}.json")
        scored = score_item(expected, actual)
        totals["samples"] += 1
        for name in ("formula_exact", "formula_total", "ast_exact", "ast_total", "unsafe_suggestion", "suggestion_total", "risky_case", "risky_recalled"):
            totals[name] += int(scored[name])
        for name, values in counts.items():
            triple = scored[name]
            assert isinstance(triple, tuple)
            for index, value in enumerate(triple):
                values[index] += value
        sample_results.append({"sample_id": sample_id, "original": entry["original"]})
    metrics: dict[str, Any] = {
        "formula_exact_accuracy": safe_div(totals["formula_exact"], totals["formula_total"]),
        "ast_exact_accuracy": safe_div(totals["ast_exact"], totals["ast_total"]),
    }
    for name, (tp, fp, fn) in counts.items():
        precision = safe_div(tp, tp + fp)
        recall = safe_div(tp, tp + fn)
        metrics[f"{name}_precision"] = precision
        metrics[f"{name}_recall"] = recall
        metrics[f"{name}_f1"] = (
            safe_div(2 * precision * recall, precision + recall)
            if precision is not None and recall is not None
            else None
        )
    metrics["unsafe_suggestion_rate"] = safe_div(totals["unsafe_suggestion"], totals["suggestion_total"])
    metrics["risky_case_recall"] = safe_div(totals["risky_recalled"], totals["risky_case"])
    return {"schema_version": "math-bench-v1", "dataset": dataset, "sample_count": totals["samples"], "metrics": metrics, "samples": sample_results}


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--manifest", type=Path, required=True)
    parser.add_argument("--predictions", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument(
        "--dataset",
        default=None,
        help="required provenance label for synthetic runs, e.g. 'synthetic'",
    )
    args = parser.parse_args()
    report = evaluate(args.manifest, args.predictions, dataset=args.dataset)
    args.output.parent.mkdir(parents=True, exist_ok=True)
    args.output.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(json.dumps(report["metrics"], ensure_ascii=False, sort_keys=True))


if __name__ == "__main__":
    main()
