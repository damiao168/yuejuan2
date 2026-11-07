#!/usr/bin/env python3
"""Deterministic generator for the synthetic-v2 MathBench fixture set.

Everything emitted by this script is SYNTHETIC data with no randomness and no
model involvement: ground truth is hand-authored, and the ``synthetic-v2``
predictions are derived from ground truth by injecting a fixed schedule of
deliberate mistakes so every metric path in ``run_math_bench.py`` gets both
hits and misses. Baselines computed from this data are a harness self-check
only. They are NOT model quality evidence and must never be quoted as pilot
results. The real MathBench (1000+ formula crops, 500+ full answers) waits for
de-identified real answer sheets.

``original`` manifest fields are virtual references: the runner scores JSON
documents only and never loads images, so no PNG files exist for this set.
"""

from __future__ import annotations

import argparse
import copy
import itertools
import json
import re

# CLI 和按文件导入都没有稳定的包名；只在装载同目录模块时补搜索路径，随后恢复。
import sys
from pathlib import Path
from typing import Any

_support_dir = str(Path(__file__).resolve().parent)
_added_support_path = _support_dir not in sys.path
if _added_support_path:
    sys.path.insert(0, _support_dir)
try:
    from synthetic_fixture_specs import (
        CATEGORIES,
        DEFECT_CYCLE,
        ORIGINAL_ENTRY_ID,
        ORIGINAL_PREDICTION_SOURCE,
        ORIGINAL_PREFIX,
        PREDICTION_SET,
        SUBJECT_CODE,
    )
finally:
    if _added_support_path:
        sys.path.remove(_support_dir)
del _support_dir, _added_support_path


# ---------------------------------------------------------------------------
# 规范化规格。
# ---------------------------------------------------------------------------


def normalized_spec(spec: dict[str, Any]) -> dict[str, Any]:
    steps = [{"id": sid, "normalized_text": text} for sid, text in spec["steps"]]
    if "edges" in spec:
        edges = [
            {"from_step_id": f, "to_step_id": t, "kind": k} for f, t, k in spec["edges"]
        ]
    else:
        kind = spec.get("edge_kind", "next")
        ids = [step["id"] for step in steps]
        edges = [
            {"from_step_id": a, "to_step_id": b, "kind": kind}
            for a, b in itertools.pairwise(ids)
        ]
    return {
        "synthetic": True,
        "formulas": [
            {"id": fid, "latex": latex, "ast": ast} for fid, latex, ast in spec["formulas"]
        ],
        "symbols": list(spec["symbols"]),
        "relations": [
            {"from_id": f, "to_id": t, "kind": k} for f, t, k in spec.get("relations", [])
        ],
        "solution_graph": {"steps": steps, "edges": edges},
        "verifications": [
            {"id": vid, "kind": "equivalence", "status": status}
            for vid, status in spec.get("verifications", [("verification-1", "verified")])
        ],
        "rubric_evidence": [
            {"rubric_criterion_key": key, "status": status}
            for key, status in spec.get("rubric", [("solve_equation", "supported")])
        ],
        "safe_to_suggest": spec.get("safe", True),
        "risky_case": spec.get("risky", False),
    }


# ---------------------------------------------------------------------------
# 刻意修改预测结果（全部按确定规则执行）。
# ---------------------------------------------------------------------------


def spacing_variant(latex: str) -> str:
    variant = latex.replace("=", " = ")
    if "(" in latex:
        variant = variant.replace("(", "\\left(").replace(")", "\\right)")
    return variant


def semantic_variant(latex: str) -> str:
    if r"\frac" in latex:
        return latex.replace(r"\frac", r"\tfrac", 1)
    if re.search(r"(?<=\d)(?=[A-Za-z])", latex):
        return re.sub(r"(?<=\d)(?=[A-Za-z])", r"\\cdot ", latex, count=1)
    if r"\sqrt{" in latex:
        return latex.replace(r"\sqrt{", r"\sqrt[2]{", 1)
    return latex + "{}"


def swapped_ast(ast: dict[str, Any]) -> dict[str, Any]:
    children = ast.get("children")
    if isinstance(children, list) and len(children) >= 2:
        mutated = copy.deepcopy(ast)
        mutated["children"] = list(reversed(children))
        return mutated
    return {"kind": "paren", "children": [copy.deepcopy(ast)]}


def flipped_status(status: str) -> str:
    return {"verified": "unverified", "unverified": "verified"}.get(status, "verified")


def _mutate_first_formula(formulas: list[dict[str, Any]], defect: str) -> None:
    if not formulas:
        return
    formula = formulas[0]
    if defect == "latex_normalization_only":
        formula["latex"] = spacing_variant(formula["latex"])
    elif defect == "latex_variant_ast_equal":
        formula["latex"] = semantic_variant(formula["latex"])
    elif defect == "ast_mismatch" and formula["ast"] is not None:
        formula["ast"] = swapped_ast(formula["ast"])


def _mutated_symbols(symbols: list[str]) -> list[str]:
    if symbols:
        symbols = symbols[:-1]
    # 先删末项再检查 Δ；碰撞时固定追加 ω，不额外去重。
    return symbols + (["Δ"] if "Δ" not in symbols else ["ω"])


def _mutated_relations(
    relations: list[dict[str, Any]], formulas: list[dict[str, Any]]
) -> list[dict[str, Any]]:
    # 原本有两项以上才删末项；单项关系保留，仍追加一个错误关系。
    if len(relations) >= 2:
        relations = relations[:-1]
    target = formulas[0]["id"] if formulas else "formula-1"
    return relations + [{"from_id": "region-x", "to_id": target, "kind": "spurious_overlap"}]


def _merged_first_steps(steps: list[dict[str, Any]]) -> list[dict[str, Any]]:
    if len(steps) < 2:
        return steps
    merged = {
        "id": steps[0]["id"],
        "normalized_text": steps[0]["normalized_text"] + ";" + steps[1]["normalized_text"],
    }
    return [merged] + steps[2:]


def _flip_first_equivalence(verifications: list[dict[str, Any]]) -> None:
    # 一个样本只注入一次等价性缺陷，后续验证结果保留原值。
    for verification in verifications:
        if verification["kind"] == "equivalence":
            verification["status"] = flipped_status(verification["status"])
            break


def mutated_prediction(truth: dict[str, Any], defect: str) -> dict[str, Any]:
    # 缺陷只作用于预测副本，嵌套 AST、步骤和验证不能改写标准答案。
    pred = copy.deepcopy(truth)
    formulas = pred["formulas"]
    if defect in ("latex_normalization_only", "latex_variant_ast_equal", "ast_mismatch"):
        _mutate_first_formula(formulas, defect)
    elif defect == "symbol_error":
        pred["symbols"] = _mutated_symbols(pred["symbols"])
    elif defect == "relation_error":
        pred["relations"] = _mutated_relations(pred["relations"], formulas)
    elif defect == "step_grouping_error":
        pred["solution_graph"]["steps"] = _merged_first_steps(pred["solution_graph"]["steps"])
    elif defect == "edge_kind_error":
        edges = pred["solution_graph"]["edges"]
        if edges:
            edges[-1]["kind"] = "spurious_jump"
    elif defect == "equivalence_error":
        _flip_first_equivalence(pred["verifications"])
    elif defect == "rubric_error":
        evidence = pred["rubric_evidence"]
        if evidence:
            evidence = evidence[:-1]
        pred["rubric_evidence"] = evidence + [
            {"rubric_criterion_key": "spurious_criterion", "status": "supported"}
        ]
    return pred


def build_pair(spec: dict[str, Any], defect: str) -> tuple[dict[str, Any], dict[str, Any]]:
    truth = normalized_spec(spec)
    risky = truth["risky_case"] or defect in ("risky_missed", "risky_recalled")
    safe = truth["safe_to_suggest"] and not risky and defect != "unsafe_suggestion"
    truth = {**truth, "safe_to_suggest": safe, "risky_case": risky}
    prediction = mutated_prediction(truth, defect)
    prediction["suggestion_allowed"] = True if defect == "unsafe_suggestion" else safe
    prediction["human_review_required"] = risky and defect != "risky_missed"
    del prediction["safe_to_suggest"]
    del prediction["risky_case"]
    return truth, prediction


def build_all() -> dict[str, dict[str, Any]]:
    samples: dict[str, dict[str, Any]] = {}
    index = 0
    for category, specs in CATEGORIES.items():
        for number, spec in enumerate(specs, start=1):
            defect = DEFECT_CYCLE[index % len(DEFECT_CYCLE)]
            truth, prediction = build_pair(spec, defect)
            samples[f"synthetic-{category}-{number:03d}"] = {
                "category": category,
                "defect": defect,
                "ground_truth": truth,
                "prediction": prediction,
            }
            index += 1
    return samples


# ---------------------------------------------------------------------------
# 幂等输出文件。
# ---------------------------------------------------------------------------


def dumps_line(payload: Any) -> str:
    return json.dumps(payload, ensure_ascii=False) + "\n"


def write_manifest(path: Path, samples: dict[str, dict[str, Any]]) -> list[dict[str, Any]]:
    lines = [line for line in path.read_text(encoding="utf-8").splitlines() if line.strip()]
    kept: list[str] = []
    for line in lines:
        entry = json.loads(line)
        if entry["sample_id"] == ORIGINAL_ENTRY_ID or entry["sample_id"] not in samples:
            kept.append(line)
    if not kept or json.loads(kept[0])["sample_id"] != ORIGINAL_ENTRY_ID:
        raise ValueError(f"manifest at {path} lost the {ORIGINAL_ENTRY_ID} entry")
    for sample_id in samples:
        kept.append(
            dumps_line(
                {
                    "synthetic": True,
                    "sample_id": sample_id,
                    "subject_code": SUBJECT_CODE,
                    "original": f"{ORIGINAL_PREFIX}/{sample_id}.png",
                    "ground_truth": f"ground-truth/{sample_id}.json",
                }
            ).rstrip("\n")
        )
    path.write_text("\n".join(kept) + "\n", encoding="utf-8")
    return [json.loads(line) for line in kept]


# 清理目标目录内所有未列入 keep 的同后缀文件；调用方只能传入专用夹具目录。
def prune_stale(directory: Path, keep: set[str], suffix: str) -> None:
    if not directory.exists():
        return
    for stale in sorted(directory.glob(f"*{suffix}")):
        if stale.stem not in keep:
            stale.unlink()


def generate(fixtures_root: Path) -> dict[str, int]:
    samples = build_all()
    manifest_path = fixtures_root / "manifest.jsonl"
    entries = write_manifest(manifest_path, samples)
    ground_truth_dir = fixtures_root / "ground-truth"
    predictions_dir = fixtures_root / "predictions" / PREDICTION_SET
    ground_truth_dir.mkdir(parents=True, exist_ok=True)
    predictions_dir.mkdir(parents=True, exist_ok=True)
    # 扩展样本也会留在最终清单中；清理必须保留它实际引用的真值文件和同 ID 预测。
    truth_ids = {
        reference.stem
        for entry in entries
        if (reference := (fixtures_root / entry["ground_truth"]).resolve()).parent
        == ground_truth_dir.resolve()
    }
    prune_stale(ground_truth_dir, truth_ids, ".json")
    prune_stale(predictions_dir, {entry["sample_id"] for entry in entries}, ".json")
    for sample_id, bundle in samples.items():
        (ground_truth_dir / f"{sample_id}.json").write_text(
            dumps_line(bundle["ground_truth"]), encoding="utf-8"
        )
        (predictions_dir / f"{sample_id}.json").write_text(
            dumps_line(bundle["prediction"]), encoding="utf-8"
        )
    source = fixtures_root / ORIGINAL_PREDICTION_SOURCE
    original_prediction = json.loads(source.read_text(encoding="utf-8"))
    (predictions_dir / f"{ORIGINAL_ENTRY_ID}.json").write_text(
        dumps_line(original_prediction), encoding="utf-8"
    )
    counts = {category: len(specs) for category, specs in CATEGORIES.items()}
    counts["total"] = len(samples)
    return counts


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--fixtures-root",
        type=Path,
        default=Path(__file__).resolve().parent / "fixtures",
    )
    args = parser.parse_args()
    counts = generate(args.fixtures_root)
    for category, count in counts.items():
        print(f"{category}: {count}")
    print(
        "All fixtures are SYNTHETIC; baseline metrics from them are harness "
        "self-checks only, never model quality evidence."
    )


if __name__ == "__main__":
    main()
