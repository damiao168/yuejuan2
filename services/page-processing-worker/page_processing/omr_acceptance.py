from __future__ import annotations

import io
import json
import time
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import numpy as np
from PIL import Image, ImageDraw

from .omr import extract_marks

OPTION_REGIONS = [
    {"label": "A", "x": 20, "y": 20, "width": 30, "height": 30},
    {"label": "B", "x": 70, "y": 20, "width": 30, "height": 30},
    {"label": "C", "x": 120, "y": 20, "width": 30, "height": 30},
]


@dataclass(frozen=True)
class SyntheticOMRCase:
    sample_id: str
    question_type: str
    marked: tuple[str, ...]
    multiple: bool
    expected_review: bool
    mark_style: str


def build_synthetic_acceptance_cases() -> list[SyntheticOMRCase]:
    cases: list[SyntheticOMRCase] = []

    def add(count: int, question_type: str, marked: tuple[str, ...], multiple: bool, expected_review: bool, mark_style: str) -> None:
        for _ in range(count):
            cases.append(SyntheticOMRCase(
                sample_id=f"story056-synthetic-{len(cases)+1:03d}",
                question_type=question_type,
                marked=marked,
                multiple=multiple,
                expected_review=expected_review,
                mark_style=mark_style,
            ))

    # Clear, deterministic selections across all objective question families.
    for index in range(30):
        add(1, "single_choice", (("A", "B", "C")[index % 3],), False, False, "clear")
    for index in range(12):
        add(1, "true_false", (("A", "B")[index % 2],), False, False, "clear")
    multiple_answers = (("A", "B"), ("A", "C"), ("B", "C"))
    for index in range(18):
        add(1, "multiple_choice", multiple_answers[index % len(multiple_answers)], True, False, "clear")

    # Risk cases must route to humans rather than becoming automatic zeroes.
    add(15, "single_choice", (), False, True, "blank")
    add(10, "single_choice", ("A", "C"), False, True, "clear")
    for index in range(15):
        add(1, "true_false", (("A", "B")[index % 2],), False, True, "light")

    if len(cases) != 100:
        raise AssertionError(f"synthetic acceptance suite must contain 100 cases, got {len(cases)}")
    return cases


def render_synthetic_case(case: SyntheticOMRCase) -> bytes:
    image = Image.new("RGB", (180, 70), "white")
    draw = ImageDraw.Draw(image)
    for region in OPTION_REGIONS:
        x = int(region["x"])
        y = int(region["y"])
        width = int(region["width"])
        height = int(region["height"])
        box = (x, y, x + width, y + height)
        draw.rectangle(box, outline="black", width=2)
        if region["label"] not in case.marked:
            continue
        if case.mark_style == "light":
            # The inner square is intentionally between the ambiguous and marked thresholds.
            draw.rectangle((x + 11, y + 11, x + 18, y + 18), fill="black")
        else:
            draw.ellipse((x + 6, y + 6, x + width - 6, y + height - 6), fill="black")

    # Add deterministic low-level scan noise outside answer interiors so the
    # acceptance path is not only a byte-for-byte duplicate of one image.
    pixels = np.asarray(image).copy()
    seed = sum(ord(char) for char in case.sample_id)
    rng = np.random.default_rng(seed)
    for x, y in rng.integers([0, 0], [180, 70], size=(12, 2)):
        if y < 15 or y > 55:
            pixels[y, x] = (220, 220, 220)
    output = io.BytesIO()
    Image.fromarray(pixels).save(output, format="PNG")
    return output.getvalue()


# 合成用例检验选择和复核分流，不能把本套件的召回率当作真实答题卡准确率。
def evaluate_synthetic_acceptance() -> dict[str, Any]:
    cases = build_synthetic_acceptance_cases()
    confusion = {
        "expected_auto": {"auto": 0, "review": 0},
        "expected_review": {"auto": 0, "review": 0},
    }
    samples: list[dict[str, Any]] = []
    durations_ms: list[float] = []
    review_expected = 0
    review_recalled = 0
    false_zero_routed = 0
    silent_zero_count = 0
    wrong_auto_selection = 0

    for case in cases:
        started = time.perf_counter_ns()
        result = extract_marks(render_synthetic_case(case), OPTION_REGIONS, multiple=case.multiple)
        duration_ms = (time.perf_counter_ns() - started) / 1_000_000
        durations_ms.append(duration_ms)
        actual_review = bool(result["needs_human_review"])
        expected_bucket = "expected_review" if case.expected_review else "expected_auto"
        actual_bucket = "review" if actual_review else "auto"
        confusion[expected_bucket][actual_bucket] += 1
        selected = tuple(result["selected"])

        if case.expected_review:
            review_expected += 1
            if actual_review:
                review_recalled += 1
        elif actual_review:
            false_zero_routed += 1
        elif result["decision"] == "blank":
            silent_zero_count += 1
        elif selected != case.marked:
            wrong_auto_selection += 1

        samples.append({
            "sample_id": case.sample_id,
            "submission_id": case.sample_id,
            "question_type": case.question_type,
            "expected_review": case.expected_review,
            "expected_selected": list(case.marked),
            "decision": result["decision"],
            "selected": result["selected"],
            "needs_human_review": actual_review,
            "duration_ms": round(duration_ms, 3),
        })

    ordered = sorted(durations_ms)
    p95_index = max(0, int(np.ceil(len(ordered) * 0.95)) - 1)
    return {
        "suite": "story056-synthetic-omr-v1",
        "fixture_kind": "synthetic",
        "total_submissions": len(cases),
        "question_type_counts": {
            "single_choice": sum(case.question_type == "single_choice" for case in cases),
            "true_false": sum(case.question_type == "true_false" for case in cases),
            "multiple_choice": sum(case.question_type == "multiple_choice" for case in cases),
        },
        "omr_confusion_matrix": confusion,
        "ambiguous_recall": review_recalled / review_expected if review_expected else 1.0,
        "manual_routing_rate": sum(sample["needs_human_review"] for sample in samples) / len(samples),
        "false_zero_routed_count": false_zero_routed,
        "silent_zero_count": silent_zero_count,
        "wrong_auto_selection_count": wrong_auto_selection,
        "average_duration_ms": round(float(np.mean(durations_ms)), 3),
        "p95_duration_ms": round(float(ordered[p95_index]), 3),
        "samples": samples,
    }


def write_synthetic_acceptance_report(output_dir: Path) -> dict[str, Any]:
    report = evaluate_synthetic_acceptance()
    output_dir.mkdir(parents=True, exist_ok=True)
    (output_dir / "summary.json").write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
    matrix = report["omr_confusion_matrix"]
    markdown = "\n".join([
        "# STORY-056 Synthetic OMR Acceptance Report",
        "",
        "This report uses generated images only. It validates routing behavior and is not a substitute for authorized real-answer-sheet evaluation.",
        "",
        f"- Total synthetic submissions: {report['total_submissions']}",
        f"- Ambiguous/unsafe review recall: {report['ambiguous_recall']:.3f}",
        f"- Manual routing rate: {report['manual_routing_rate']:.3f}",
        f"- Silent zero count: {report['silent_zero_count']}",
        f"- Wrong automatic selection count: {report['wrong_auto_selection_count']}",
        f"- Average / P95 duration ms: {report['average_duration_ms']} / {report['p95_duration_ms']}",
        "",
        "| Expected route | Automatic route | Review route |",
        "| --- | ---: | ---: |",
        f"| Automatic | {matrix['expected_auto']['auto']} | {matrix['expected_auto']['review']} |",
        f"| Review | {matrix['expected_review']['auto']} | {matrix['expected_review']['review']} |",
        "",
        "The governed external answer-sheet result is recorded separately in docs/evaluation/story056-external-omr-report.md.",
        "",
    ])
    (output_dir / "summary.md").write_text(markdown, encoding="utf-8")
    return report
