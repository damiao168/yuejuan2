import importlib.util
import json
from pathlib import Path

import pytest

SPEC = importlib.util.spec_from_file_location("math_round19_bench", Path(__file__).with_name("run_math_bench.py"))
BENCH = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(BENCH)


@pytest.mark.parametrize(("truth_symbols", "predicted_symbols", "expected_f1"), [
    (["x"], ["y"], 0.0), (["x"], [], 0.0), ([], ["y"], 0.0),
    ([], [], None), (["x"], ["x"], 1.0),
])
def test_f1_distinguishes_zero_hits_from_no_evaluation(tmp_path, truth_symbols, predicted_symbols, expected_f1):
    truth = {"synthetic": True, "symbols": truth_symbols}
    prediction = {"synthetic": True, "symbols": predicted_symbols}
    (tmp_path / "truth.json").write_text(json.dumps(truth), encoding="utf-8")
    (tmp_path / "sample.json").write_text(json.dumps(prediction), encoding="utf-8")
    manifest = tmp_path / "manifest.jsonl"
    manifest.write_text(json.dumps({"sample_id": "sample", "subject_code": "mathematics", "ground_truth": "truth.json", "original": "synthetic.png"}), encoding="utf-8")
    report = BENCH.evaluate(manifest, tmp_path, dataset="synthetic")
    assert report["metrics"]["symbols_f1"] == expected_f1
    assert report["metrics"]["relations_f1"] is None
