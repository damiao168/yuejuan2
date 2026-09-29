#!/usr/bin/env python3
"""Offline AI grading evaluation for synthetic EduGrade samples.

The script does not call a model. It evaluates prediction records that were
produced elsewhere and keeps synthetic/mock baselines clearly labeled.
"""

from __future__ import annotations

import argparse
import json
import math
from collections import defaultdict
from datetime import datetime, timezone
from pathlib import Path
from typing import Any


REQUIRED_SAMPLE_FIELDS = {
    "sample_id",
    "synthetic",
    "question",
    "rubric",
    "answer",
    "human_score",
    "human_rationale",
    "expected_points",
}

REQUIRED_PREDICTION_FIELDS = {
    "sample_id",
    "synthetic",
    "model_version",
    "prompt_version",
    "suggested_score",
    "confidence",
    "needs_human_review",
}


def load_jsonl(path: Path) -> list[dict[str, Any]]:
    records: list[dict[str, Any]] = []
    with path.open("r", encoding="utf-8") as handle:
        for line_no, line in enumerate(handle, start=1):
            line = line.strip()
            if not line:
                continue
            try:
                record = json.loads(line)
            except json.JSONDecodeError as exc:
                raise ValueError(f"{path}:{line_no} is not valid JSON: {exc}") from exc
            if not isinstance(record, dict):
                raise ValueError(f"{path}:{line_no} must be a JSON object")
            records.append(record)
    return records


def validate_samples(samples: list[dict[str, Any]]) -> dict[str, dict[str, Any]]:
    by_id: dict[str, dict[str, Any]] = {}
    for record in samples:
        missing = REQUIRED_SAMPLE_FIELDS - record.keys()
        if missing:
            raise ValueError(f"sample {record.get('sample_id', '<unknown>')} missing fields: {sorted(missing)}")
        if record["synthetic"] is not True or record.get("answer", {}).get("synthetic") is not True:
            raise ValueError(f"sample {record['sample_id']} must be explicitly synthetic")
        sample_id = str(record["sample_id"])
        if sample_id in by_id:
            raise ValueError(f"duplicate sample_id: {sample_id}")
        human_score = float(record["human_score"])
        max_score = float(record["question"]["max_score"])
        if human_score < 0 or human_score > max_score:
            raise ValueError(f"sample {sample_id} has human_score outside [0, max_score]")
        by_id[sample_id] = record
    return by_id


def validate_predictions(predictions: list[dict[str, Any]], samples_by_id: dict[str, dict[str, Any]]) -> None:
    for record in predictions:
        missing = REQUIRED_PREDICTION_FIELDS - record.keys()
        if missing:
            raise ValueError(f"prediction for {record.get('sample_id', '<unknown>')} missing fields: {sorted(missing)}")
        if record["synthetic"] is not True:
            raise ValueError(f"prediction for {record['sample_id']} must be explicitly synthetic")
        sample_id = str(record["sample_id"])
        if sample_id not in samples_by_id:
            raise ValueError(f"prediction references unknown sample_id: {sample_id}")
        confidence = float(record["confidence"])
        if confidence < 0 or confidence > 1:
            raise ValueError(f"prediction for {sample_id} has confidence outside [0, 1]")
        suggested_score = float(record["suggested_score"])
        max_score = float(samples_by_id[sample_id]["question"]["max_score"])
        if suggested_score < 0 or suggested_score > max_score:
            raise ValueError(f"prediction for {sample_id} has suggested_score outside [0, max_score]")


def joined_rows(predictions: list[dict[str, Any]], samples_by_id: dict[str, dict[str, Any]]) -> list[dict[str, Any]]:
    rows: list[dict[str, Any]] = []
    for prediction in predictions:
        sample = samples_by_id[str(prediction["sample_id"])]
        rows.append(
            {
                "sample_id": str(prediction["sample_id"]),
                "model_version": str(prediction["model_version"]),
                "prompt_version": str(prediction["prompt_version"]),
                "human_score": float(sample["human_score"]),
                "suggested_score": float(prediction["suggested_score"]),
                "confidence": float(prediction["confidence"]),
                "needs_human_review": bool(prediction["needs_human_review"]),
                "risk_flags": list(prediction.get("risk_flags", [])),
                "max_score": float(sample["question"]["max_score"]),
            }
        )
    return rows


def compute_metrics(
    rows: list[dict[str, Any]],
    *,
    low_confidence_threshold: float,
    adjacent_tolerance: float,
    exact_tolerance: float,
) -> dict[str, Any]:
    count = len(rows)
    if count == 0:
        return empty_metrics()
    errors = [row["suggested_score"] - row["human_score"] for row in rows]
    abs_errors = [abs(value) for value in errors]
    low_confidence = [row for row in rows if row["confidence"] < low_confidence_threshold]
    routed = [row for row in rows if row["needs_human_review"]]
    low_confidence_routed = [row for row in low_confidence if row["needs_human_review"]]
    risky_not_routed = [row for row in rows if row["risk_flags"] and not row["needs_human_review"]]
    low_confidence_not_routed = [row for row in low_confidence if not row["needs_human_review"]]
    return {
        "sample_count": count,
        "mae": mean(abs_errors),
        "rmse": math.sqrt(mean([value * value for value in errors])),
        "exact_agreement": safe_div(sum(1 for value in abs_errors if value <= exact_tolerance), count),
        "adjacent_agreement": safe_div(sum(1 for value in abs_errors if value <= adjacent_tolerance), count),
        "score_bias": mean(errors),
        "low_confidence": {
            "threshold": low_confidence_threshold,
            "count": len(low_confidence),
            "rate": safe_div(len(low_confidence), count),
            "routed_to_human_count": len(low_confidence_routed),
            "routing_coverage": safe_div(len(low_confidence_routed), len(low_confidence)),
            "not_routed_sample_ids": [row["sample_id"] for row in low_confidence_not_routed],
        },
        "human_review": {
            "routed_count": len(routed),
            "routed_rate": safe_div(len(routed), count),
            "high_confidence_routed_count": len([row for row in routed if row["confidence"] >= low_confidence_threshold]),
            "risky_not_routed_sample_ids": [row["sample_id"] for row in risky_not_routed],
        },
    }


def empty_metrics() -> dict[str, Any]:
    return {
        "sample_count": 0,
        "mae": None,
        "rmse": None,
        "exact_agreement": None,
        "adjacent_agreement": None,
        "score_bias": None,
        "low_confidence": {
            "threshold": None,
            "count": 0,
            "rate": None,
            "routed_to_human_count": 0,
            "routing_coverage": None,
            "not_routed_sample_ids": [],
        },
        "human_review": {
            "routed_count": 0,
            "routed_rate": None,
            "high_confidence_routed_count": 0,
            "risky_not_routed_sample_ids": [],
        },
    }


def group_rows(rows: list[dict[str, Any]], keys: tuple[str, ...]) -> list[dict[str, Any]]:
    grouped: dict[tuple[str, ...], list[dict[str, Any]]] = defaultdict(list)
    for row in rows:
        grouped[tuple(str(row[key]) for key in keys)].append(row)
    out: list[dict[str, Any]] = []
    for values, items in sorted(grouped.items()):
        item = {key: value for key, value in zip(keys, values)}
        item["rows"] = items
        out.append(item)
    return out


def build_report(
    *,
    samples_path: Path,
    predictions_path: Path,
    samples_by_id: dict[str, dict[str, Any]],
    predictions: list[dict[str, Any]],
    low_confidence_threshold: float,
    adjacent_tolerance: float,
    exact_tolerance: float,
) -> dict[str, Any]:
    # 按预测记录计数，同一样本可有多个模型/提示词结果；整体指标不等于去重样本上的指标。
    rows = joined_rows(predictions, samples_by_id)
    metric_kwargs = {
        "low_confidence_threshold": low_confidence_threshold,
        "adjacent_tolerance": adjacent_tolerance,
        "exact_tolerance": exact_tolerance,
    }
    by_variant = []
    for group in group_rows(rows, ("model_version", "prompt_version")):
        metrics = compute_metrics(group.pop("rows"), **metric_kwargs)
        by_variant.append({**group, "metrics": metrics})
    by_model = []
    for group in group_rows(rows, ("model_version",)):
        metrics = compute_metrics(group.pop("rows"), **metric_kwargs)
        by_model.append({**group, "metrics": metrics})
    by_prompt = []
    for group in group_rows(rows, ("prompt_version",)):
        metrics = compute_metrics(group.pop("rows"), **metric_kwargs)
        by_prompt.append({**group, "metrics": metrics})
    return {
        "generated_at": datetime.now(timezone.utc).isoformat(),
        "dataset": {
            "samples_path": str(samples_path),
            "predictions_path": str(predictions_path),
            "sample_count": len(samples_by_id),
            "prediction_count": len(predictions),
            "synthetic_only": True,
        },
        "config": {
            "low_confidence_threshold": low_confidence_threshold,
            "adjacent_tolerance": adjacent_tolerance,
            "exact_tolerance": exact_tolerance,
        },
        "overall": compute_metrics(rows, **metric_kwargs),
        "by_variant": by_variant,
        "by_model_version": by_model,
        "by_prompt_version": by_prompt,
        "limitations": [
            "This report uses synthetic samples only.",
            "Included prediction fixtures may be mock baselines and must not be presented as real model capability.",
            "Metrics measure agreement with provided human_score labels; they do not prove fairness, robustness, or production readiness.",
        ],
    }


def write_reports(report: dict[str, Any], out_dir: Path) -> tuple[Path, Path]:
    out_dir.mkdir(parents=True, exist_ok=True)
    json_path = out_dir / "ai_evaluation_report.json"
    md_path = out_dir / "ai_evaluation_report.md"
    json_path.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    md_path.write_text(render_markdown(report), encoding="utf-8")
    return json_path, md_path


def render_markdown(report: dict[str, Any]) -> str:
    lines = [
        "# AI Evaluation Report",
        "",
        "> Synthetic evaluation only. Do not present this report as proof of real model production capability.",
        "",
        "## Dataset",
        "",
        f"- Samples: {report['dataset']['sample_count']}",
        f"- Predictions: {report['dataset']['prediction_count']}",
        f"- Synthetic only: {report['dataset']['synthetic_only']}",
        "",
        "## Variant Comparison",
        "",
        "| model_version | prompt_version | n | MAE | RMSE | exact | adjacent | bias | low-conf routed | review rate |",
        "| --- | --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: | ---: |",
    ]
    for item in report["by_variant"]:
        metrics = item["metrics"]
        lines.append(
            "| {model} | {prompt} | {n} | {mae} | {rmse} | {exact} | {adjacent} | {bias} | {low_route} | {review_rate} |".format(
                model=item["model_version"],
                prompt=item["prompt_version"],
                n=metrics["sample_count"],
                mae=fmt(metrics["mae"]),
                rmse=fmt(metrics["rmse"]),
                exact=fmt_pct(metrics["exact_agreement"]),
                adjacent=fmt_pct(metrics["adjacent_agreement"]),
                bias=fmt(metrics["score_bias"]),
                low_route=fmt_pct(metrics["low_confidence"]["routing_coverage"]),
                review_rate=fmt_pct(metrics["human_review"]["routed_rate"]),
            )
        )
    lines.extend(
        [
            "",
            "## Model Version Comparison",
            "",
            "| model_version | n | MAE | RMSE | exact | adjacent | bias |",
            "| --- | ---: | ---: | ---: | ---: | ---: | ---: |",
        ]
    )
    for item in report["by_model_version"]:
        metrics = item["metrics"]
        lines.append(
            f"| {item['model_version']} | {metrics['sample_count']} | {fmt(metrics['mae'])} | {fmt(metrics['rmse'])} | {fmt_pct(metrics['exact_agreement'])} | {fmt_pct(metrics['adjacent_agreement'])} | {fmt(metrics['score_bias'])} |"
        )
    lines.extend(
        [
            "",
            "## Prompt Version Comparison",
            "",
            "| prompt_version | n | MAE | RMSE | exact | adjacent | bias |",
            "| --- | ---: | ---: | ---: | ---: | ---: | ---: |",
        ]
    )
    for item in report["by_prompt_version"]:
        metrics = item["metrics"]
        lines.append(
            f"| {item['prompt_version']} | {metrics['sample_count']} | {fmt(metrics['mae'])} | {fmt(metrics['rmse'])} | {fmt_pct(metrics['exact_agreement'])} | {fmt_pct(metrics['adjacent_agreement'])} | {fmt(metrics['score_bias'])} |"
        )
    lines.extend(["", "## Low Confidence Routing", ""])
    for item in report["by_variant"]:
        metrics = item["metrics"]
        low_conf = metrics["low_confidence"]
        review = metrics["human_review"]
        lines.extend(
            [
                f"### {item['model_version']} / {item['prompt_version']}",
                "",
                f"- Low confidence threshold: {low_conf['threshold']}",
                f"- Low confidence samples: {low_conf['count']} ({fmt_pct(low_conf['rate'])})",
                f"- Low confidence routed to human: {low_conf['routed_to_human_count']} ({fmt_pct(low_conf['routing_coverage'])})",
                f"- Overall human review rate: {fmt_pct(review['routed_rate'])}",
                f"- Low confidence not routed sample IDs: {', '.join(low_conf['not_routed_sample_ids']) or 'none'}",
                f"- Risk-flagged but not routed sample IDs: {', '.join(review['risky_not_routed_sample_ids']) or 'none'}",
                "",
            ]
        )
    lines.extend(["## Limitations", ""])
    for limitation in report["limitations"]:
        lines.append(f"- {limitation}")
    lines.append("")
    return "\n".join(lines)


def mean(values: list[float]) -> float:
    return sum(values) / len(values)


def safe_div(numerator: int | float, denominator: int | float) -> float | None:
    if denominator == 0:
        return None
    return numerator / denominator


def fmt(value: float | None) -> str:
    if value is None:
        return "-"
    return f"{value:.3f}"


def fmt_pct(value: float | None) -> str:
    if value is None:
        return "-"
    return f"{value * 100:.1f}%"


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Evaluate synthetic AI grading predictions.")
    parser.add_argument("--samples", type=Path, required=True, help="Path to synthetic sample JSONL.")
    parser.add_argument("--predictions", type=Path, required=True, help="Path to prediction JSONL.")
    parser.add_argument("--out-dir", type=Path, default=Path("tests/ai-evaluation/reports"), help="Directory for JSON/Markdown reports.")
    parser.add_argument("--low-confidence-threshold", type=float, default=0.8)
    parser.add_argument("--adjacent-tolerance", type=float, default=1.0)
    parser.add_argument("--exact-tolerance", type=float, default=1e-9)
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    samples = load_jsonl(args.samples)
    samples_by_id = validate_samples(samples)
    predictions = load_jsonl(args.predictions)
    validate_predictions(predictions, samples_by_id)
    report = build_report(
        samples_path=args.samples,
        predictions_path=args.predictions,
        samples_by_id=samples_by_id,
        predictions=predictions,
        low_confidence_threshold=args.low_confidence_threshold,
        adjacent_tolerance=args.adjacent_tolerance,
        exact_tolerance=args.exact_tolerance,
    )
    json_path, md_path = write_reports(report, args.out_dir)
    print(f"Wrote {json_path}")
    print(f"Wrote {md_path}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
