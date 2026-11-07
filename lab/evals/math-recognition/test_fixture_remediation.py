import copy
import importlib.util
import json
from pathlib import Path

import sympy


def load_module(filename: str, name: str):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(filename))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


GEN = load_module("generate_synthetic_fixtures.py", "fixture_remediation_generator")
BENCH = load_module("run_math_bench.py", "fixture_remediation_bench")
SPECS = load_module("synthetic_fixture_specs.py", "fixture_remediation_specs")
FIXTURES = Path(__file__).with_name("fixtures")


def test_generate_retains_extension_actual_truth_path_and_prediction(tmp_path):
    truth_dir = tmp_path / "ground-truth"
    truth_dir.mkdir()
    source = tmp_path / GEN.ORIGINAL_PREDICTION_SOURCE
    source.parent.mkdir(parents=True)
    original = {"synthetic": True}
    source.write_text(json.dumps(original), encoding="utf-8")
    (truth_dir / f"{GEN.ORIGINAL_ENTRY_ID}.json").write_text(json.dumps(original), encoding="utf-8")
    # 扩展条目的真值文件名可以不同于 sample_id；清理须以清单实际引用为准。
    extension = {"synthetic": True, "sample_id": "synthetic-custom", "subject_code": "mathematics",
                 "ground_truth": "ground-truth/custom-truth.json", "original": "synthetic-custom.png"}
    entries = [{**extension, "sample_id": GEN.ORIGINAL_ENTRY_ID,
                "ground_truth": f"ground-truth/{GEN.ORIGINAL_ENTRY_ID}.json"}, extension]
    (tmp_path / "manifest.jsonl").write_text("\n".join(json.dumps(item) for item in entries) + "\n", encoding="utf-8")
    prediction_dir = tmp_path / "predictions" / GEN.PREDICTION_SET
    prediction_dir.mkdir()
    protected = [truth_dir / "custom-truth.json", prediction_dir / "synthetic-custom.json"]
    for file in protected:
        file.write_text(json.dumps(original) + "\n", encoding="utf-8")
    before = {file: file.read_bytes() for file in protected}
    stale = [truth_dir / "unreferenced.json", prediction_dir / "unreferenced.json"]
    for file in stale:
        file.write_text(json.dumps(original), encoding="utf-8")
    for _ in range(2):
        GEN.generate(tmp_path)
        assert {file: file.read_bytes() for file in protected} == before
        assert not any(file.exists() for file in stale)
        report = BENCH.evaluate(tmp_path / "manifest.jsonl", prediction_dir, dataset="synthetic")
        assert report["sample_count"] == len(GEN.build_all()) + 2


def test_limit_semantics_and_evaluator_distinguish_radians_from_degrees():
    x = sympy.Symbol("x")
    # 独立数学真值防止标准与预测共同携带相同错误时，精确比对仍给满分。
    assert sympy.limit(sympy.sin(x) / x, x, 0) == 1
    assert sympy.limit(sympy.sin(x * sympy.pi / 180) / x, x, 0) == sympy.pi / 180
    expected = GEN.build_all()["synthetic-limit-001"]["ground_truth"]
    trig = expected["formulas"][0]["ast"]["children"][2]["children"][0]
    assert trig["unit"] == "rad"
    correct = copy.deepcopy(expected)
    degrees = copy.deepcopy(expected)
    degrees["formulas"][0]["ast"]["children"][2]["children"][0]["unit"] = "deg"
    assert BENCH.score_item(expected, correct)["ast_exact"] == 1
    assert BENCH.score_item(expected, degrees)["ast_exact"] == 0
    assert SPECS.trig("sin", SPECS.num("30"))["unit"] == "deg"
    stored_truth = json.loads((FIXTURES / "ground-truth/synthetic-limit-001.json").read_text(encoding="utf-8"))
    stored_prediction = json.loads((FIXTURES / "predictions/synthetic-v2/synthetic-limit-001.json").read_text(encoding="utf-8"))
    assert stored_truth == expected
    assert stored_prediction == GEN.build_all()["synthetic-limit-001"]["prediction"]
