"""Reproducible local CPU OCR benchmark.

The benchmark deliberately measures the project's engine and sample bundle, not a
vendor showcase. Inputs are sorted by relative path and reports contain only hashes,
paths, and aggregate quality metrics (never student identity or OCR text).
"""
from __future__ import annotations

import argparse
import hashlib
import json
import platform
import statistics
import sys
import time
from pathlib import Path
from typing import Any

# 允许从仓库根目录调用，无需以可编辑模式安装。
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from ocr_worker.engine import PaddleOCREngine


class LegacyBaselineEngine(PaddleOCREngine):
    """Use exactly the pre-optimization constructor, including runtime defaults."""

    def _load(self):
        if self._ocr is None:
            from paddleocr import PaddleOCR

            self._ocr = PaddleOCR(
                text_detection_model_name="PP-OCRv5_mobile_det",
                text_recognition_model_name="PP-OCRv5_mobile_rec",
                device="cpu", enable_mkldnn=False,
                use_doc_orientation_classify=False, use_doc_unwarping=False,
                use_textline_orientation=True,
            )
        return self._ocr


def _percentile(values: list[float], percentile: float) -> float:
    if not values:
        return 0.0
    ordered = sorted(values)
    index = (len(ordered) - 1) * percentile / 100
    lower, upper = int(index), min(int(index) + 1, len(ordered) - 1)
    return ordered[lower] + (ordered[upper] - ordered[lower]) * (index - lower)


def _bundle_hash(items: list[dict[str, Any]]) -> str:
    digest = hashlib.sha256()
    for item in items:
        path = Path(item["path"])
        digest.update(path.name.encode())
        digest.update(path.read_bytes())
    return digest.hexdigest()


def _rss_bytes() -> int | None:
    # 操作系统的 RSS 峰值包含页面间推理产生的临时分配。
    try:
        import resource
        value = resource.getrusage(resource.RUSAGE_SELF).ru_maxrss
        return int(value * (1 if sys.platform == "darwin" else 1024))
    except Exception:  # noqa: BLE001 - 尽力探测 RSS，失败可忽略。
        try:
            import psutil  # type: ignore
            info = psutil.Process().memory_info()
            return int(info.peak_wset) if hasattr(info, "peak_wset") else None
        except Exception:  # noqa: BLE001 - 尽力探测 RSS，失败可忽略。
            return None


def _edit_distance(left: list[str], right: list[str]) -> int:
    row = list(range(len(right) + 1))
    for i, a in enumerate(left, 1):
        next_row = [i]
        for j, b in enumerate(right, 1):
            next_row.append(min(next_row[-1] + 1, row[j] + 1, row[j - 1] + (a != b)))
        row = next_row
    return row[-1]


def _load_manifest(manifest: Path | None, sample_dir: Path | None) -> list[dict[str, Any]]:
    if manifest:
        payload = json.loads(manifest.read_text(encoding="utf-8"))
        rows = payload.get("samples", payload) if isinstance(payload, dict) else payload
        if not isinstance(rows, list):
            raise ValueError("manifest must contain a samples array")
        result = [_manifest_sample(row, manifest.parent) for row in rows]
    elif sample_dir:
        result = [{"path": str(path)} for path in sorted(sample_dir.rglob("*")) if path.suffix.lower() in {".png", ".jpg", ".jpeg", ".tif", ".tiff"}]
    else:
        raise ValueError("provide --manifest or --sample-dir")
    result.sort(key=lambda row: Path(row["path"]).as_posix())
    return result


def _manifest_sample(row: Any, parent: Path) -> dict[str, Any]:
    if isinstance(row, str):
        row = {"path": row}
    if not isinstance(row, dict) or not isinstance(row.get("path"), str):
        raise ValueError("manifest sample requires path")  # noqa: TRY004
    item = dict(row)
    item["path"] = str((parent / item["path"]).resolve())
    return item


def _load_roi_dataset(dataset_dir: Path) -> list[dict[str, Any]]:
    candidates = json.loads((dataset_dir / "candidates.json").read_text(encoding="utf-8"))
    region_map = json.loads((dataset_dir / "regions.json").read_text(encoding="utf-8"))
    items: list[dict[str, Any]] = []
    for candidate in candidates:
        candidate_no = candidate["candidate_no"]
        for page_no, relative_path in enumerate(candidate["page_files"], 1):
            regions = []
            for question_no, bbox in sorted(region_map[candidate_no].items(), key=lambda pair: int(pair[0][1:])):
                question_number = int(question_no[1:])
                if (question_number <= 16) != (page_no == 1):
                    continue
                expected = candidate["answers"][question_no]["text"]
                if isinstance(expected, list):
                    expected = "".join(str(value) for value in expected)
                regions.append({"id": question_no, "bbox": bbox, "text": str(expected)})
            items.append({"path": str((dataset_dir / relative_path).resolve()), "regions": regions})
    items.sort(key=lambda item: Path(item["path"]).as_posix())
    return items


def _crop_normalized(image: Any, bbox: dict[str, Any]) -> bytes:
    import io

    width, height = image.size
    left = round(float(bbox["x"]) * width)
    top = round(float(bbox["y"]) * height)
    right = round((float(bbox["x"]) + float(bbox["width"])) * width)
    bottom = round((float(bbox["y"]) + float(bbox["height"])) * height)
    if left < 0 or top < 0 or right > width or bottom > height or right <= left or bottom <= top:
        raise ValueError("invalid normalized benchmark region")
    output = io.BytesIO()
    image.convert("RGB").crop((left, top, right, bottom)).save(output, format="PNG")
    return output.getvalue()


def _configure_legacy_baseline(args: argparse.Namespace, items: list[dict[str, Any]]) -> None:
    # local-baseline-v1 刻意作为兼容配置，匹配
    # 优化前的引擎：普通 CPU 执行、关闭 MKLDNN、开启方向识别，
    # 并采用旧识别设置。后续所有运行都必须与它比较。
    if args.run_id == "local-baseline-v1":
        if args.roi_dataset or any(item.get("regions") for item in items):
            raise ValueError("local-baseline-v1 requires full-page input without ROI")
        args.model_version = "ppocr-v5-mobile"
        args.enable_hpi = False
        args.enable_mkldnn = "false"
        args.use_textline_orientation = True
        args.text_recognition_batch_size = 1
        args.text_det_limit_side_len = 0
        args.text_det_limit_type = None


def _warmup_engine(engine: Any, warmup: list[dict[str, Any]]) -> None:
    for item in warmup:
        if item.get("regions"):
            from PIL import Image

            with Image.open(item["path"]) as image:
                image.load()
                for region in item["regions"]:
                    engine.recognize_region(_crop_normalized(image, region["bbox"]))
        else:
            engine.recognize(Path(item["path"]).read_bytes())


class _BenchmarkCounters:
    # 页面中途失败也保留已处理区域的累计指标，不能按整页成功才统一入账。
    def __init__(self) -> None:
        self.failures = 0
        self.cer_errors = 0
        self.cer_total = 0
        self.wer_errors = 0
        self.wer_total = 0
        self.expected_count = 0
        self.region_count = 0
        self.region_hits = 0
        self.blank_regions = 0
        self.blank_false_positives = 0
        self.review_triggers = 0
        self.result_block_count = 0


def _record_text_quality(actual: str, expected: str, counters: _BenchmarkCounters) -> None:
    counters.cer_errors += _edit_distance(list(actual), list(expected))
    counters.cer_total += max(1, len(expected))
    actual_words, expected_words = actual.split(), expected.split()
    counters.wer_errors += _edit_distance(actual_words, expected_words)
    counters.wer_total += max(1, len(expected_words))


def _measure_benchmark_region(engine: Any, image: Any, region: dict[str, Any], counters: _BenchmarkCounters) -> None:
    blocks = engine.recognize_region(_crop_normalized(image, region["bbox"]))
    counters.result_block_count += len(blocks)
    counters.region_count += 1
    actual = "".join(block.text for block in blocks)
    expected = region["text"]
    if actual:
        counters.region_hits += 1
    if not expected:
        counters.blank_regions += 1
        counters.blank_false_positives += int(bool(actual))
    counters.expected_count += 1
    _record_text_quality(actual, expected, counters)
    counters.review_triggers += int(not blocks or any(block.confidence < 0.8 for block in blocks))


def _measure_benchmark_page(engine: Any, item: dict[str, Any], counters: _BenchmarkCounters) -> None:
    if item.get("regions"):
        from PIL import Image

        with Image.open(item["path"]) as image:
            image.load()
            for region in item["regions"]:
                _measure_benchmark_region(engine, image, region, counters)
    else:
        blocks = engine.recognize(Path(item["path"]).read_bytes())
        counters.result_block_count += len(blocks)
        expected = item.get("text")
        if isinstance(expected, str):
            counters.expected_count += 1
            actual = "".join(block.text for block in blocks)
            _record_text_quality(actual, expected, counters)


def run(args: argparse.Namespace) -> dict[str, Any]:
    items = _load_roi_dataset(args.roi_dataset) if args.roi_dataset else _load_manifest(args.manifest, args.sample_dir)
    if args.count < 0:
        raise ValueError("sample count must be non-negative")
    if args.count:
        items = items[: args.count]
    if len(items) < 1:
        raise ValueError("sample bundle is empty")
    _configure_legacy_baseline(args, items)
    engine_type = LegacyBaselineEngine if args.run_id == "local-baseline-v1" else PaddleOCREngine
    engine = engine_type(
        device="cpu", model_version=args.model_version, cpu_threads=args.cpu_threads,
        enable_mkldnn=args.enable_mkldnn, enable_hpi=args.enable_hpi,
        use_textline_orientation=args.use_textline_orientation,
        text_det_limit_type=args.text_det_limit_type,
        text_det_limit_side_len=args.text_det_limit_side_len or None,
        text_recognition_batch_size=args.text_recognition_batch_size,
    )
    if args.run_id == "local-baseline-v1":
        # 不要把 CLI 的候选线程数误报为旧版隐式值。
        engine.cpu_threads = None
    # 就绪检查和随后五轮预热不计入页面计时，报告主要反映热运行耗时。
    engine.initialize()
    warmup = [items[index % len(items)] for index in range(5)]
    _warmup_engine(engine, warmup)
    durations: list[float] = []
    counters = _BenchmarkCounters()
    peak_rss = _rss_bytes()
    for item in items:
        started = time.perf_counter()
        try:
            _measure_benchmark_page(engine, item, counters)
        except Exception:  # noqa: BLE001 - 基准测试记录失败，不中止运行。
            counters.failures += 1
        durations.append((time.perf_counter() - started) * 1000)
        current_rss = _rss_bytes()
        if current_rss is not None:
            peak_rss = max(peak_rss or current_rss, current_rss)
    return _benchmark_summary(args, engine, items, warmup, durations, peak_rss, counters)


def _benchmark_summary(
    args: argparse.Namespace,
    engine: Any,
    items: list[dict[str, Any]],
    warmup: list[dict[str, Any]],
    durations: list[float],
    peak_rss: int | None,
    counters: _BenchmarkCounters,
) -> dict[str, Any]:
    summary: dict[str, Any] = {
        "run_id": args.run_id,
        "model_version": engine.model_version,
        "engine_version": "paddleocr-3.7.0",
        "device": engine.device,
        "cpu_threads": engine.cpu_threads,
        "enable_mkldnn": args.enable_mkldnn,
        "effective_mkldnn": engine.effective_mkldnn,
        "enable_hpi": engine.enable_hpi,
        "use_textline_orientation": engine.use_textline_orientation,
        "text_det_limit_type": engine.text_det_limit_type,
        "text_det_limit_side_len": engine.text_det_limit_side_len,
        "text_recognition_batch_size": engine.text_recognition_batch_size,
        "preprocess_profile": args.preprocess_profile,
        "config_hash": hashlib.sha256((
            f"engine=paddleocr|engine_version=pp-ocrv5|model_version={engine.model_version}|device=cpu|"
            f"preprocess_profile={args.preprocess_profile}|cpu_threads={engine.cpu_threads}|effective_mkldnn={engine.effective_mkldnn}|"
            f"enable_hpi={engine.enable_hpi}|use_textline_orientation={engine.use_textline_orientation}|"
            f"text_det_limit_type={engine.text_det_limit_type}|text_det_limit_side_len={engine.text_det_limit_side_len}|"
            f"text_recognition_batch_size={engine.text_recognition_batch_size}|roi_text_det_limit_type=min|roi_text_det_limit_side_len=64"
        ).encode()).hexdigest(),
        "input_bundle_hash": _bundle_hash(items),
        "annotation_hash": hashlib.sha256(json.dumps(
            [{"text": item.get("text"), "regions": item.get("regions")} for item in items],
            ensure_ascii=False, sort_keys=True, separators=(",", ":"),
        ).encode()).hexdigest(),
        "benchmark_version": 2,
        "inference_scope": "regions" if any(item.get("regions") for item in items) else "full_page",
        "roi_text_det_limit_type": "min",
        "roi_text_det_limit_side_len": 64,
        "hardware": {"platform": platform.platform(), "processor": platform.processor(), "python": platform.python_version()},
        "sample_count": len(items),
        "warmup_count": len(warmup),
        "average_duration_ms/page": statistics.fmean(durations),
        "p50_duration_ms/page": _percentile(durations, 50),
        "p95_duration_ms/page": _percentile(durations, 95),
        "pages_per_minute": 60000 / statistics.fmean(durations) if durations and statistics.fmean(durations) else 0,
        "task_average_duration_ms": statistics.fmean(durations),
        "process_peak_rss_bytes": peak_rss,
        "failure_rate": counters.failures / len(items),
        "result_block_count": counters.result_block_count,
        "region_count": counters.region_count,
        # 没有标注答案时不报告 CER/WER，空缺的质量证据不能当作通过。
        "quality": {"CER": counters.cer_errors / counters.cer_total if counters.expected_count else None, "WER": counters.wer_errors / counters.wer_total if counters.expected_count else None, "region_hit_rate": counters.region_hits / counters.region_count if counters.region_count else None, "bbox_iou": None, "page_success_rate": 1 - counters.failures / len(items), "blank_false_positive_rate": counters.blank_false_positives / counters.blank_regions if counters.blank_regions else None, "low_confidence_recall": None, "review_trigger_rate": counters.review_triggers / counters.region_count if counters.region_count else None},
    }
    return summary


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--manifest", type=Path)
    parser.add_argument("--sample-dir", type=Path)
    parser.add_argument("--roi-dataset", type=Path, help="dataset with candidates.json, regions.json, and page_files")
    parser.add_argument("--count", type=int, default=0)
    parser.add_argument("--run-id", default=time.strftime("local-%Y%m%d-%H%M%S"))
    parser.add_argument("--model-version", default="ppocr-v5-mobile")
    parser.add_argument("--cpu-threads", type=int, default=4)
    parser.add_argument("--enable-mkldnn", choices=("auto", "true", "false"), default="auto")
    parser.add_argument("--enable-hpi", action="store_true")
    parser.add_argument("--use-textline-orientation", action=argparse.BooleanOptionalAction, default=True)
    parser.add_argument("--text-det-limit-type", choices=("max", "min"), default="min")
    parser.add_argument("--text-det-limit-side-len", type=int, default=64)
    parser.add_argument("--text-recognition-batch-size", type=int, default=1)
    parser.add_argument("--preprocess-profile", default="default")
    parser.add_argument("--output-dir", type=Path, default=Path("reports/ocr-evaluation"))
    args = parser.parse_args()
    summary = run(args)
    output = args.output_dir / args.run_id
    output.mkdir(parents=True, exist_ok=True)
    (output / "summary.json").write_text(json.dumps(summary, ensure_ascii=False, indent=2), encoding="utf-8")
    lines = [f"# OCR benchmark {args.run_id}", "", "| Metric | Value |", "| --- | --- |"]
    for key, value in summary.items():
        if isinstance(value, dict):
            for field, measurement in value.items():
                lines.append(f"| {key}.{field} | {measurement if measurement is not None else 'N/A'} |")
        else:
            lines.append(f"| {key} | {value if value is not None else 'N/A'} |")
    lines.extend(["", "Missing ground truth is N/A, never a passed quality gate. Task duration here is local inference per page and excludes queue, network, and API writeback."])
    (output / "summary.md").write_text("\n".join(lines) + "\n", encoding="utf-8")
    print(output)


if __name__ == "__main__":
    main()
