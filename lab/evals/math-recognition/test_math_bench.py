import importlib.util
import json
from pathlib import Path

import pytest

MODULE_PATH = Path(__file__).with_name("run_math_bench.py")
SPEC = importlib.util.spec_from_file_location("run_math_bench", MODULE_PATH)
assert SPEC
assert SPEC.loader
MODULE = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(MODULE)

GENERATOR_PATH = Path(__file__).with_name("generate_synthetic_fixtures.py")
GEN_SPEC = importlib.util.spec_from_file_location("generate_synthetic_fixtures", GENERATOR_PATH)
assert GEN_SPEC
assert GEN_SPEC.loader
GEN = importlib.util.module_from_spec(GEN_SPEC)
GEN_SPEC.loader.exec_module(GEN)

FIXTURES = Path(__file__).parent / "fixtures"
SYNTHETIC_V2_PREDICTIONS = FIXTURES / "predictions" / "synthetic-v2"
ORIGINAL_ENTRY_ID = "synthetic-equation-001"

# 列出执行器可能输出的所有指标键，以及 synthetic-v2
# 语料必须保持严格介于 0 与 1 之间的子集（同时观察到命中和未命中）。
DISCRIMINATING_METRICS = (
    "formula_exact_accuracy",
    "ast_exact_accuracy",
    "symbols_precision",
    "symbols_recall",
    "symbols_f1",
    "relations_precision",
    "relations_recall",
    "relations_f1",
    "steps_precision",
    "steps_recall",
    "steps_f1",
    "edges_precision",
    "edges_recall",
    "edges_f1",
    "rubric_evidence_precision",
    "rubric_evidence_recall",
    "rubric_evidence_f1",
    "equivalence_precision",
    "equivalence_recall",
    "equivalence_f1",
    "unsafe_suggestion_rate",
    "risky_case_recall",
)


def load_manifest() -> list[dict]:
    lines = (FIXTURES / "manifest.jsonl").read_text(encoding="utf-8").splitlines()
    return [json.loads(line) for line in lines if line.strip()]


def test_smoke_fixture_is_reproducible(tmp_path: Path) -> None:
    original_lines = [
        line
        for line in (FIXTURES / "manifest.jsonl").read_text(encoding="utf-8").splitlines()
        if line.strip() and json.loads(line)["sample_id"] == ORIGINAL_ENTRY_ID
    ]
    manifest = tmp_path / "manifest.jsonl"
    manifest.write_text("\n".join(original_lines) + "\n", encoding="utf-8")
    ground_truth_dir = tmp_path / "ground-truth"
    ground_truth_dir.mkdir()
    (ground_truth_dir / f"{ORIGINAL_ENTRY_ID}.json").write_text(
        (FIXTURES / "ground-truth" / f"{ORIGINAL_ENTRY_ID}.json").read_text(encoding="utf-8"),
        encoding="utf-8",
    )
    first = MODULE.evaluate(manifest, FIXTURES / "predictions" / "smoke-v1")
    second = MODULE.evaluate(manifest, FIXTURES / "predictions" / "smoke-v1")
    assert first == second
    assert first["sample_count"] == 1
    assert first["metrics"]["formula_exact_accuracy"] == 1.0
    assert first["metrics"]["rubric_evidence_f1"] == 1.0
    assert first["metrics"]["equivalence_precision"] == 1.0
    # 冒烟测试语料也覆盖关系为空时的分母处理。
    assert first["metrics"]["relations_f1"] is None
    assert first["metrics"]["edges_f1"] is None
    assert first["metrics"]["risky_case_recall"] is None


def test_humanities_are_rejected_from_formula_benchmark(tmp_path: Path) -> None:
    manifest = tmp_path / "manifest.jsonl"
    manifest.write_text(json.dumps({"sample_id": "history-1", "subject_code": "history", "ground_truth": "truth.json", "original": "page.png"}), encoding="utf-8")
    with pytest.raises(ValueError, match="does not accept subject"):
        MODULE.evaluate(manifest, tmp_path)


def test_manifest_keeps_original_entry_and_expands_synthetic_corpus() -> None:
    entries = load_manifest()
    assert entries[0]["sample_id"] == ORIGINAL_ENTRY_ID
    ids = [entry["sample_id"] for entry in entries]
    assert len(ids) == len(set(ids))
    for entry in entries:
        assert entry["sample_id"].startswith("synthetic-")
        assert entry["subject_code"] in MODULE.FORMULA_SUBJECTS
        assert (FIXTURES / entry["ground_truth"]).is_file()
        assert (SYNTHETIC_V2_PREDICTIONS / f"{entry['sample_id']}.json").is_file()


def test_every_category_has_at_least_two_samples() -> None:
    entries = load_manifest()
    manifest_categories: dict[str, int] = {}
    for entry in entries:
        if entry["sample_id"] == ORIGINAL_ENTRY_ID:
            continue
        category, number = entry["sample_id"].rsplit("-", 1)
        assert number.isdigit()
        category = category.removeprefix("synthetic-")
        manifest_categories[category] = manifest_categories.get(category, 0) + 1
    assert set(manifest_categories) == set(GEN.CATEGORIES)
    for category, count in manifest_categories.items():
        assert count >= 2, f"category {category} has only {count} samples"
    total = sum(manifest_categories.values())
    assert total >= 50


def test_generator_is_deterministic_and_matches_disk() -> None:
    first = GEN.build_all()
    second = GEN.build_all()
    assert first == second
    assert len(first) == 54
    for sample_id, bundle in first.items():
        truth = json.loads(
            (FIXTURES / "ground-truth" / f"{sample_id}.json").read_text(encoding="utf-8")
        )
        prediction = json.loads(
            (SYNTHETIC_V2_PREDICTIONS / f"{sample_id}.json").read_text(encoding="utf-8")
        )
        assert truth == bundle["ground_truth"], sample_id
        assert prediction == bundle["prediction"], sample_id
    smoke_source = json.loads(
        (FIXTURES / "predictions" / "smoke-v1" / f"{ORIGINAL_ENTRY_ID}.json").read_text(
            encoding="utf-8"
        )
    )
    copied = json.loads(
        (SYNTHETIC_V2_PREDICTIONS / f"{ORIGINAL_ENTRY_ID}.json").read_text(encoding="utf-8")
    )
    assert copied == smoke_source
    generated_ids = {entry["sample_id"] for entry in load_manifest()} - {ORIGINAL_ENTRY_ID}
    assert generated_ids == set(first)


def test_every_defect_is_scheduled_for_metric_coverage() -> None:
    bundles = GEN.build_all()
    defects = {bundle["defect"] for bundle in bundles.values()}
    assert defects == set(GEN.DEFECT_CYCLE)
    assert {"exact", "unsafe_suggestion", "risky_missed", "risky_recalled"} <= defects


def test_synthetic_v2_report_is_labelled_and_discriminating() -> None:
    report = json.loads(
        (Path(__file__).parent / "reports" / "synthetic-v2.json").read_text(encoding="utf-8")
    )
    assert report["dataset"] == "synthetic"
    assert report["schema_version"] == "math-bench-v1"
    entries = load_manifest()
    assert report["sample_count"] == len(entries)
    assert [sample["sample_id"] for sample in report["samples"]] == [
        entry["sample_id"] for entry in entries
    ]
    for name in DISCRIMINATING_METRICS:
        value = report["metrics"][name]
        assert value is not None, f"{name} has no denominator in the corpus"
        assert 0.0 < value < 1.0, f"{name}={value} does not discriminate"
    # 报告不得声称使用了真实数据。
    assert report["samples"][0]["sample_id"] == ORIGINAL_ENTRY_ID


def test_full_synthetic_run_is_reproducible() -> None:
    first = MODULE.evaluate(
        FIXTURES / "manifest.jsonl", SYNTHETIC_V2_PREDICTIONS, dataset="synthetic"
    )
    second = MODULE.evaluate(
        FIXTURES / "manifest.jsonl", SYNTHETIC_V2_PREDICTIONS, dataset="synthetic"
    )
    assert first == second
    assert first["dataset"] == "synthetic"
    assert first["sample_count"] == 55
    assert 0.0 < first["metrics"]["formula_exact_accuracy"] < 1.0
