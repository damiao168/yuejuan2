import importlib.util
import json
from argparse import Namespace
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import Mock, patch

import pytest
from ocr_worker.engine import OCRBlock, PaddleOCREngine

spec = importlib.util.spec_from_file_location("benchmark_local", Path(__file__).parents[1] / "benchmarks" / "benchmark_local.py")
benchmark = importlib.util.module_from_spec(spec)
spec.loader.exec_module(benchmark)


def arguments(manifest, **changes):
    values = {"manifest": manifest, "sample_dir": None, "roi_dataset": None, "count": 0,
              "run_id": "test", "model_version": "ppocr-v5-mobile", "cpu_threads": 4,
              "enable_mkldnn": "false", "enable_hpi": False, "use_textline_orientation": True,
              "text_det_limit_type": "min", "text_det_limit_side_len": 64,
              "text_recognition_batch_size": 1, "preprocess_profile": "default"}
    return Namespace(**(values | changes))


def test_short_bundle_still_warms_up_five_times_and_keeps_answers_private(tmp_path):
    (tmp_path / "page.png").write_bytes(b"test image")
    manifest = tmp_path / "manifest.json"
    manifest.write_text(json.dumps([{"path": "page.png", "text": "private answer"}]))
    engine = PaddleOCREngine(enable_mkldnn="false")
    with patch.object(benchmark, "PaddleOCREngine", return_value=engine), patch.object(engine, "initialize"), patch.object(
        engine, "recognize", return_value=[OCRBlock("private answer", [1, 1, 2, 2], 0.9)]
    ) as recognize:
        result = benchmark.run(arguments(manifest))
    assert recognize.call_count == 6
    assert result["warmup_count"] == 5
    assert result["sample_count"] == 1
    assert result["quality"]["CER"] == 0
    assert result["quality"]["low_confidence_recall"] is None
    assert "private answer" not in json.dumps(result)
    original_hash = result["annotation_hash"]
    manifest.write_text(json.dumps([{"path": "page.png", "text": "changed truth"}]))
    with patch.object(benchmark, "PaddleOCREngine", return_value=engine), patch.object(engine, "initialize"), patch.object(engine, "recognize", return_value=[]):
        changed = benchmark.run(arguments(manifest))
    assert changed["annotation_hash"] != original_hash
    assert changed["input_bundle_hash"] == result["input_bundle_hash"]


def test_baseline_cannot_silently_measure_regions(tmp_path):
    manifest = tmp_path / "manifest.json"
    manifest.write_text(json.dumps([{"path": "page.png", "regions": [{"bbox": [1, 2, 3, 4]}]}]))
    benchmark_arguments = arguments(manifest, run_id='local-baseline-v1')
    with pytest.raises(ValueError, match='full-page input'):
        benchmark.run(benchmark_arguments)


def test_negative_sample_count_is_rejected(tmp_path):
    manifest = tmp_path / "manifest.json"
    manifest.write_text("[]")
    benchmark_arguments = arguments(manifest, count=-1)
    with pytest.raises(ValueError, match='non-negative'):
        benchmark.run(benchmark_arguments)


def test_legacy_baseline_does_not_override_runtime_defaults():
    constructor = Mock()
    engine = benchmark.LegacyBaselineEngine(enable_mkldnn="false")
    with patch.dict("sys.modules", {"paddleocr": SimpleNamespace(PaddleOCR=constructor)}):
        assert engine._load() is engine._load()
    constructor.assert_called_once_with(
        text_detection_model_name="PP-OCRv5_mobile_det",
        text_recognition_model_name="PP-OCRv5_mobile_rec",
        device="cpu", enable_mkldnn=False,
        use_doc_orientation_classify=False, use_doc_unwarping=False,
        use_textline_orientation=True,
    )
