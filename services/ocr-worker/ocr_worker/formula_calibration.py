from __future__ import annotations

import argparse
import hashlib
import io
import json
import statistics
import sys
import time
from collections.abc import Callable
from pathlib import Path
from typing import Any

from .config import Settings, load_settings
from .deployment_profile import (
    FORMULA_PROFILE_SCHEMA,
    descriptor_fingerprint,
    formula_hardware_descriptor,
    formula_software_descriptor,
)
from .math_layout import PaddleFormulaLayoutDetector
from .recognition_router import PaddleFormulaNetEngine

BUILTIN_FORMULAS = (
    r"$x^2+2x+1=(x+1)^2$",
    r"$\frac{-b\pm\sqrt{b^2-4ac}}{2a}$",
    r"$\sum_{i=1}^{n}i=\frac{n(n+1)}{2}$",
    r"$\int_0^1 x^2\,dx=\frac{1}{3}$",
    r"$a_n=a_1+(n-1)d$",
    r"$\sin^2\theta+\cos^2\theta=1$",
    r"$\left|\frac{x-1}{x+1}\right|\geq 2$",
    r"$f'(x)=\lim_{h\to0}\frac{f(x+h)-f(x)}{h}$",
)


def calibrate_formula_runtime(
    settings: Settings,
    images: list[bytes],
    *,
    batch_sizes: list[int],
    repeats: int,
    lifecycle_goal: str,
    engine: Any | None = None,
    detector: Any | None = None,
    clock: Callable[[], float] = time.perf_counter,
    progress: Callable[[dict[str, Any]], None] | None = None,
) -> dict[str, Any]:
    if not images or any(not image for image in images):
        raise ValueError("formula calibration requires non-empty images")
    if repeats < 1:
        raise ValueError("formula calibration repeats must be positive")
    candidates = sorted(set(batch_sizes))
    if not candidates or candidates[0] < 1 or candidates[-1] > 16:
        raise ValueError("formula calibration batch sizes must be between 1 and 16")
    if lifecycle_goal not in {"compatibility", "latency"}:
        raise ValueError("formula calibration lifecycle goal must be compatibility or latency")

    detector = detector or PaddleFormulaLayoutDetector(settings.formula_layout_model_version, settings.device)
    engine = engine or PaddleFormulaNetEngine(
        model_version=settings.formula_model_version,
        device=settings.device,
        cache_size=0,
    )
    load_started = clock()
    detector.initialize()
    engine.initialize()
    model_load_ms = max(0.0, (clock() - load_started) * 1000)
    if progress is not None:
        progress(
            {
                "phase": "model_ready",
                "completed_runs": 0,
                "total_runs": len(candidates) * repeats,
            }
        )

    baseline_started = clock()
    baseline_results = engine.recognize_formulas(images, batch_size=1)
    baseline_duration = max(0.0, (clock() - baseline_started) * 1000)
    # 单张批次是输出一致性基准；没有标注答案，不能把一致性视为公式识别准确率。
    baseline = [result.canonical_latex for result in baseline_results]
    measurements: list[dict[str, Any]] = []
    completed_runs = 0
    for batch_size in candidates:
        durations: list[float] = [baseline_duration] if batch_size == 1 else []
        consistent = True
        recognized = sum(bool(value) for value in baseline) if batch_size == 1 else 0
        if batch_size == 1:
            completed_runs += 1
            if progress is not None:
                progress(
                    {
                        "phase": "candidate_run",
                        "batch_size": 1,
                        "repeat_no": 1,
                        "repeat_total": repeats,
                        "completed_runs": completed_runs,
                        "total_runs": len(candidates) * repeats,
                    }
                )
        for repeat_index in range(len(durations), repeats):
            started = clock()
            results = engine.recognize_formulas(images, batch_size=batch_size)
            durations.append(max(0.0, (clock() - started) * 1000))
            observed = [result.canonical_latex for result in results]
            consistent = consistent and observed == baseline
            recognized = max(recognized, sum(bool(value) for value in observed))
            completed_runs += 1
            if progress is not None:
                progress(
                    {
                        "phase": "candidate_run",
                        "batch_size": batch_size,
                        "repeat_no": repeat_index + 1,
                        "repeat_total": repeats,
                        "completed_runs": completed_runs,
                        "total_runs": len(candidates) * repeats,
                    }
                )
        average_ms = statistics.fmean(durations)
        measurements.append(
            {
                "batch_size": batch_size,
                "repeat_count": repeats,
                "average_ms_per_bundle": average_ms,
                "p50_ms_per_bundle": _percentile(durations, 50),
                "p95_ms_per_bundle": _percentile(durations, 95),
                "throughput_roi_per_second": len(images) * 1000 / average_ms if average_ms > 0 else 0.0,
                "output_consistency_passed": consistent,
                "recognized_roi_count": recognized,
            }
        )
    usable = [row for row in measurements if row["output_consistency_passed"] and row["recognized_roi_count"] > 0]
    if not usable:
        raise RuntimeError("no formula batch candidate produced consistent recognized output")
    selected = max(usable, key=lambda row: (row["throughput_roi_per_second"], -row["batch_size"]))
    hardware = formula_hardware_descriptor(settings.device)
    lifecycle = "resident" if lifecycle_goal == "latency" else "per_job"
    return {
        "schema_version": FORMULA_PROFILE_SCHEMA,
        "created_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
        "hardware": hardware,
        "hardware_fingerprint": descriptor_fingerprint(hardware),
        "software": formula_software_descriptor(settings),
        "evidence": {
            "benchmark_scope": "deployment_performance_and_batch_output_consistency",
            "absolute_accuracy_evaluated": False,
            "sample_count": len(images),
            "sample_bundle_sha256": _bundle_hash(images),
            "model_load_ms": model_load_ms,
            "process_peak_rss_bytes": _rss_bytes(),
            "output_consistency_passed": True,
            "measurements": measurements,
        },
        "recommendation": {
            "objective": "warm_latency" if lifecycle_goal == "latency" else "memory_compatibility",
            "lifecycle": lifecycle,
            "batch_size": int(selected["batch_size"]),
        },
    }


def _builtin_samples() -> list[bytes]:
    try:
        from matplotlib.mathtext import math_to_image
    except ImportError as exc:
        raise RuntimeError("matplotlib is required for built-in formula calibration samples") from exc
    images: list[bytes] = []
    for formula in BUILTIN_FORMULAS:
        output = io.BytesIO()
        math_to_image(formula, output, dpi=180, format="png")
        images.append(output.getvalue())
    return images


def _load_samples(sample_dir: Path | None) -> list[bytes]:
    if sample_dir is None:
        return _builtin_samples()
    paths = [
        path
        for path in sorted(sample_dir.rglob("*"))
        if path.suffix.lower() in {".png", ".jpg", ".jpeg", ".tif", ".tiff"}
    ]
    if not paths:
        raise ValueError("formula calibration sample directory contains no supported images")
    return [path.read_bytes() for path in paths]


def _bundle_hash(images: list[bytes]) -> str:
    digest = hashlib.sha256()
    for image in images:
        digest.update(hashlib.sha256(image).digest())
    return digest.hexdigest()


def _percentile(values: list[float], percentile: float) -> float:
    if not values:
        return 0.0
    ordered = sorted(values)
    position = (len(ordered) - 1) * percentile / 100
    lower = int(position)
    upper = min(lower + 1, len(ordered) - 1)
    return ordered[lower] + (ordered[upper] - ordered[lower]) * (position - lower)


def _rss_bytes() -> int | None:
    try:
        import resource

        value = resource.getrusage(resource.RUSAGE_SELF).ru_maxrss
        return int(value * (1 if sys.platform == "darwin" else 1024))
    except (ImportError, OSError):
        try:
            import psutil  # type: ignore[import-not-found]

            process = psutil.Process()
            info = process.memory_info()
            return int(getattr(info, "peak_wset", info.rss))
        except (ImportError, OSError):
            return None


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Measure formula inference on this deployment; never infer a plan from RAM capacity alone.",
    )
    parser.add_argument("--sample-dir", type=Path, help="Optional representative formula ROI directory")
    parser.add_argument("--batch-sizes", default="1,2,4,8", help="Comma-separated candidate batch sizes")
    parser.add_argument("--repeats", type=int, default=3)
    parser.add_argument(
        "--lifecycle-goal",
        choices=("compatibility", "latency"),
        default="compatibility",
        help="compatibility releases models after a queue burst; latency keeps them resident",
    )
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    settings = load_settings()
    batch_sizes = [int(value.strip()) for value in args.batch_sizes.split(",") if value.strip()]
    profile = calibrate_formula_runtime(
        settings,
        _load_samples(args.sample_dir),
        batch_sizes=batch_sizes,
        repeats=args.repeats,
        lifecycle_goal=args.lifecycle_goal,
        progress=lambda event: print(json.dumps(event, ensure_ascii=False), flush=True),
    )
    output = args.output or Path(settings.formula_deployment_profile_path)
    output.parent.mkdir(parents=True, exist_ok=True)
    temporary = output.with_suffix(output.suffix + ".tmp")
    temporary.write_text(json.dumps(profile, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    temporary.replace(output)
    print(output)


if __name__ == "__main__":
    main()
