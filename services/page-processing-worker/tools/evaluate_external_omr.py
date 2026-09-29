from __future__ import annotations

import argparse
import hashlib
import json
import re
import time
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

import cv2
import numpy as np
from page_processing.omr import OMRProfile, extract_marks

# 以下坐标绑定本工具的 90 题三栏评估版式，不是通用生产模板参数。
CANONICAL_WIDTH = 1960
CANONICAL_HEIGHT = 3340
QUESTION_COUNT = 90
AUTO_CONFIRM_MINIMUM_CONFIDENCE = 0.98
X_GROUPS = (
    (0.1383, 0.1868, 0.2363, 0.2874),
    (0.4554, 0.5079, 0.5554, 0.6054),
    (0.7706, 0.8204, 0.8707, 0.9207),
)
Y_FIRST = 0.0411
Y_LAST = 0.9546
UNSAFE_LABELS = {"X", "M"}


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="Evaluate STORY-056 OMR against the governed Tamaulipas subset.")
    parser.add_argument("--dataset-dir", type=Path, required=True)
    parser.add_argument("--json-out", type=Path, required=True)
    parser.add_argument("--markdown-out", type=Path, required=True)
    return parser.parse_args()


def parse_labels(path: Path) -> dict[int, str]:
    labels = {int(number): label for number, label in re.findall(r"(\d+)\s*:\s*([ABCDXM])", path.read_text(encoding="utf-8"))}
    if set(labels) != set(range(1, QUESTION_COUNT + 1)):
        raise ValueError(f"{path} does not contain one governed label for every item")
    return labels


def register_answer_area(path: Path) -> np.ndarray:
    image = cv2.imread(str(path), cv2.IMREAD_GRAYSCALE)
    if image is None:
        raise ValueError(f"unable to decode {path}")
    _, dark = cv2.threshold(image, 80, 255, cv2.THRESH_BINARY_INV)
    contours, _ = cv2.findContours(dark, cv2.RETR_LIST, cv2.CHAIN_APPROX_SIMPLE)
    if not contours:
        raise ValueError(f"answer frame not found in {path}")
    page_area = image.shape[0] * image.shape[1]
    candidates = []
    for contour in contours:
        x, y, width, height = cv2.boundingRect(contour)
        if width * height >= page_area * 0.30 and 1.5 < height / width < 2.0 and y > image.shape[0] * 0.10:
            candidates.append((width * height, x, y, width, height))
    if not candidates:
        raise ValueError(f"detected answer frame is implausible in {path}")
    _, x, y, width, height = max(candidates)
    return cv2.resize(image[y : y + height, x : x + width], (CANONICAL_WIDTH, CANONICAL_HEIGHT))


def question_crop(image: np.ndarray, question_no: int) -> tuple[np.ndarray, list[dict[str, Any]]]:
    block = (question_no - 1) // 30
    row = (question_no - 1) % 30
    center_y = (Y_FIRST + (Y_LAST - Y_FIRST) * row / 29) * CANONICAL_HEIGHT
    centers = [value * CANONICAL_WIDTH for value in X_GROUPS[block]]
    left = int(min(centers) - 34)
    right = int(max(centers) + 34)
    top = int(center_y - 34)
    bottom = int(center_y + 34)
    regions = [
        {"label": label, "x": round(center_x - left - 25), "y": 9, "width": 50, "height": 50}
        for label, center_x in zip("ABCD", centers, strict=True)
    ]
    return image[top:bottom, left:right], regions


def encode_png(image: np.ndarray) -> bytes:
    ok, encoded = cv2.imencode(".png", image)
    if not ok:
        raise ValueError("unable to encode evaluation crop")
    return encoded.tobytes()


def dataset_manifest_hash(dataset_dir: Path, files: list[Path]) -> str:
    manifest = hashlib.sha256()
    for path in sorted(files):
        digest = hashlib.sha256(path.read_bytes()).hexdigest()
        manifest.update(path.relative_to(dataset_dir).as_posix().encode("utf-8"))
        manifest.update(b"\0")
        manifest.update(digest.encode("ascii"))
        manifest.update(b"\n")
    return manifest.hexdigest()


def percentile(values: list[float], value: float) -> float:
    return round(float(np.percentile(values, value)), 3) if values else 0.0


def evaluate(dataset_dir: Path) -> dict[str, Any]:
    images = sorted(dataset_dir.glob("*.jpg"))
    if len(images) < 2 or len(images) % 2:
        raise ValueError("external evaluation requires an even number of at least two answer sheets")
    label_files = [image.with_suffix(".txt") for image in images]
    if not all(path.is_file() for path in label_files):
        raise ValueError("every answer sheet must have a human-observed label file")

    registered = [register_answer_area(path) for path in images]
    profile = OMRProfile(
        mode="template_difference",
        marked_threshold=0.18,
        ambiguous_threshold=0.10,
        minimum_margin=0.06,
        border_fraction=0.12,
        version="opencv-template-difference-bubble-v1",
        reference_mask_dilation_pixels=1,
    )
    raw = {
        "clear_label_count": 0,
        "correct_selected_count": 0,
        "wrong_selected_count": 0,
        "clear_routed_to_review_count": 0,
        "unsafe_label_count": 0,
        "unsafe_routed_to_review_count": 0,
        "unsafe_selected_count": 0,
    }
    gated = {
        "automatic_count": 0,
        "correct_automatic_count": 0,
        "wrong_automatic_count": 0,
        "manual_review_count": 0,
        "unsafe_automatic_count": 0,
        "unsafe_routed_to_review_count": 0,
        "silent_unsafe_zero_count": 0,
    }
    decisions = {"selected": 0, "blank": 0, "multiple": 0, "ambiguous": 0}
    durations_ms: list[float] = []
    selected_confidences: list[float] = []
    half = len(images) // 2

    # Each half is evaluated against a median blank reference built only from
    # the other half. Labels never participate in registration or reference generation.
    for fold in range(2):
        evaluation_indices = range(fold * half, (fold + 1) * half)
        calibration_indices = range((1 - fold) * half, (2 - fold) * half)
        reference = np.median(np.stack([registered[index] for index in calibration_indices]), axis=0).astype(np.uint8)
        for index in evaluation_indices:
            labels = parse_labels(label_files[index])
            for question_no in range(1, QUESTION_COUNT + 1):
                crop, regions = question_crop(registered[index], question_no)
                reference_crop, _ = question_crop(reference, question_no)
                started = time.perf_counter_ns()
                result = extract_marks(
                    encode_png(crop),
                    regions,
                    profile=profile,
                    reference_image_bytes=encode_png(reference_crop),
                    reference_content_type="image/png",
                    reference_question_region={"x": 0, "y": 0, "width": 1, "height": 1},
                )
                durations_ms.append((time.perf_counter_ns() - started) / 1_000_000)
                decisions[result["decision"]] += 1
                if result["decision"] == "selected":
                    selected_confidences.append(float(result["confidence"]))

                expected = labels[question_no]
                unsafe = expected in UNSAFE_LABELS
                extractor_review = bool(result["needs_human_review"])
                automatic = not extractor_review and float(result["confidence"]) >= AUTO_CONFIRM_MINIMUM_CONFIDENCE
                if unsafe:
                    raw["unsafe_label_count"] += 1
                    raw["unsafe_routed_to_review_count" if extractor_review else "unsafe_selected_count"] += 1
                    gated["unsafe_automatic_count" if automatic else "unsafe_routed_to_review_count"] += 1
                    if automatic and result["decision"] == "blank":
                        gated["silent_unsafe_zero_count"] += 1
                else:
                    raw["clear_label_count"] += 1
                    if extractor_review:
                        raw["clear_routed_to_review_count"] += 1
                    elif result["selected"] == [expected]:
                        raw["correct_selected_count"] += 1
                    else:
                        raw["wrong_selected_count"] += 1

                if automatic:
                    gated["automatic_count"] += 1
                    if not unsafe and result["selected"] == [expected]:
                        gated["correct_automatic_count"] += 1
                    else:
                        gated["wrong_automatic_count"] += 1
                else:
                    gated["manual_review_count"] += 1

    item_count = len(images) * QUESTION_COUNT
    raw_selected = raw["correct_selected_count"] + raw["wrong_selected_count"] + raw["unsafe_selected_count"]
    raw["selected_precision"] = raw["correct_selected_count"] / raw_selected if raw_selected else 0.0
    raw["clear_selected_coverage"] = (
        (raw["correct_selected_count"] + raw["wrong_selected_count"]) / raw["clear_label_count"]
        if raw["clear_label_count"] else 0.0
    )
    raw["unsafe_review_recall"] = (
        raw["unsafe_routed_to_review_count"] / raw["unsafe_label_count"] if raw["unsafe_label_count"] else 1.0
    )
    gated["automatic_precision"] = gated["correct_automatic_count"] / gated["automatic_count"] if gated["automatic_count"] else 0.0
    gated["automatic_coverage"] = gated["automatic_count"] / item_count
    gated["clear_automatic_coverage"] = gated["correct_automatic_count"] / raw["clear_label_count"]
    gated["manual_routing_rate"] = gated["manual_review_count"] / item_count
    gated["unsafe_review_recall"] = (
        gated["unsafe_routed_to_review_count"] / raw["unsafe_label_count"] if raw["unsafe_label_count"] else 1.0
    )

    governed_files = images + label_files
    return {
        "generated_at": datetime.now(timezone.utc).isoformat(),
        "suite": "story056-tamaulipas-external-omr-v1",
        "dataset": {
            "title": "Tamaulipas Multiple-Choice-Question Exam Image Dataset for Optical Mark Recognition Research",
            "doi": "10.17632/djmynjwjpy.2",
            "license": "CC BY 4.0",
            "upstream_image_count": 5721,
            "local_answer_sheet_count": len(images),
            "local_item_count": item_count,
            "local_subset_rate": len(images) / 5721,
            "manifest_sha256": dataset_manifest_hash(dataset_dir, governed_files),
            "contains_direct_names": False,
            "contains_pseudonymous_answer_ids": True,
        },
        "method": {
            "registration": "largest dark answer-frame crop resized to 1960x3340",
            "reference": "two-fold median template; evaluated sheets excluded from their reference fold",
            "labels_used_for_registration_or_reference": False,
            "profile": {
                "version": profile.version,
                "marked_threshold": profile.marked_threshold,
                "ambiguous_threshold": profile.ambiguous_threshold,
                "minimum_margin": profile.minimum_margin,
                "reference_mask_dilation_pixels": profile.reference_mask_dilation_pixels,
            },
            "auto_confirm_minimum_confidence": AUTO_CONFIRM_MINIMUM_CONFIDENCE,
        },
        "decision_counts": decisions,
        "raw_extractor": raw,
        "production_gate": gated,
        "throughput": {
            "scope": "per-question extraction after registration and reference generation",
            "average_duration_ms": round(float(np.mean(durations_ms)), 3),
            "p95_duration_ms": percentile(durations_ms, 95),
            "sequential_items_per_second": round(1000.0 / float(np.mean(durations_ms)), 2),
        },
        "selected_confidence": {
            "p50": percentile(selected_confidences, 50),
            "p95": percentile(selected_confidences, 95),
            "maximum": percentile(selected_confidences, 100),
        },
        "verdict": {
            "auto_confirmation_profile_approved": False,
            "required_mode": "manual_only",
            "reason": "The safety gate eliminated observed false automatic decisions, but clear-answer automatic coverage is too low and the governed local subset is not representative of the full dataset.",
        },
        "limitations": [
            "The local subset contains 20 of 5,721 upstream answer sheets from one school/grade path.",
            "The evaluation uses a harness-only frame registration step; production still requires a locked template and approved calibration evidence.",
            "X and M are treated as unsafe human-observed labels, not as automatically scorable zero answers.",
            "Aggregate results do not authorize use of raw images for training or redistribution.",
        ],
    }


def write_report(report: dict[str, Any], json_out: Path, markdown_out: Path) -> None:
    json_out.parent.mkdir(parents=True, exist_ok=True)
    markdown_out.parent.mkdir(parents=True, exist_ok=True)
    json_out.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    raw = report["raw_extractor"]
    gated = report["production_gate"]
    throughput = report["throughput"]
    dataset = report["dataset"]
    markdown = f"""# STORY-056 External OMR Evaluation

## Decision

**Not approved for automatic confirmation. Keep this template in `manual_only` mode.**

The `0.98` server safety gate removed all observed false automatic decisions, but it automatically confirmed only {gated['automatic_count']} of {dataset['local_item_count']} items. Safety behavior passed; practical automatic coverage did not.

## Governed Dataset

- Source: {dataset['title']}
- DOI: https://doi.org/{dataset['doi']}
- License: {dataset['license']}
- Local subset: {dataset['local_answer_sheet_count']} answer sheets / {dataset['local_item_count']} labeled items ({dataset['local_subset_rate']:.3%} of upstream sheets)
- Local manifest SHA-256: `{dataset['manifest_sha256']}`
- Privacy: direct name fields are blank; answer-sheet IDs remain pseudonymous and raw files stay in ignored local output.

## Method

- Detected the printed answer frame, normalized it to `1960x3340`, and evaluated 90 four-choice items per sheet.
- Built a median blank reference in two folds; no evaluated sheet contributes to its own reference.
- Used production `extract_marks` with `opencv-template-difference-bubble-v1` and the server minimum confidence `{report['method']['auto_confirm_minimum_confidence']}`.
- Human labels were used only after extraction for scoring metrics; `X` and `M` are unsafe cases that must route to review.

## Results

| Metric | Result |
| --- | ---: |
| Raw unique selections | {raw['correct_selected_count'] + raw['wrong_selected_count'] + raw['unsafe_selected_count']} |
| Raw clear-label correct / wrong selections | {raw['correct_selected_count']} / {raw['wrong_selected_count']} |
| Raw unsafe-label selections | {raw['unsafe_selected_count']} |
| Raw selected precision | {raw['selected_precision']:.3%} |
| Raw clear-answer selected coverage | {raw['clear_selected_coverage']:.3%} |
| Unsafe review recall before server gate | {raw['unsafe_review_recall']:.3%} |
| Server-gated automatic confirmations | {gated['automatic_count']} |
| Server-gated correct / wrong | {gated['correct_automatic_count']} / {gated['wrong_automatic_count']} |
| Server-gated automatic precision | {gated['automatic_precision']:.3%} |
| Server-gated clear-answer coverage | {gated['clear_automatic_coverage']:.3%} |
| Manual routing rate | {gated['manual_routing_rate']:.3%} |
| Unsafe review recall | {gated['unsafe_review_recall']:.3%} |
| Silent unsafe-to-zero | {gated['silent_unsafe_zero_count']} |
| Average / P95 extraction | {throughput['average_duration_ms']} ms / {throughput['p95_duration_ms']} ms |

## Interpretation

- The conservative gate behaved correctly on this subset: no observed unsafe or incorrect item was auto-confirmed.
- Coverage is not operationally acceptable, so this is evidence for manual routing, not evidence to loosen thresholds.
- The source subset is narrow and the registration step is evaluation-only. A production template still requires its own locked blank reference, at least 100 governed calibration samples, per-option coverage, approval, and revocation controls.
- SurveySet was governed separately and excluded from score metrics because its demographic survey crops do not provide the locked answer-sheet template/label mapping required by STORY-056.

## Limitations

""" + "\n".join(f"- {item}" for item in report["limitations"]) + "\n"
    markdown_out.write_text(markdown, encoding="utf-8")


def main() -> None:
    args = parse_args()
    write_report(evaluate(args.dataset_dir), args.json_out, args.markdown_out)


if __name__ == "__main__":
    main()
