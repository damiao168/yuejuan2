import copy
import importlib.util
import json
from pathlib import Path
from types import ModuleType

import pytest


def _load_module(filename: str, name: str) -> ModuleType:
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(filename))
    assert spec
    assert spec.loader
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


GEN = _load_module("generate_synthetic_fixtures.py", "math_batch28_generator")
BENCH = _load_module("run_math_bench.py", "math_batch28_bench")
SYSTEM_TEXT = "x=2,y=1"
LINEAR_TEXT = "2x+1=7"
FIRST_FORMULA_ID = "formula-a"
SECOND_FORMULA_ID = "formula-b"
SPURIOUS_SOURCE = "region-x"
FIRST_RELATION_ID = "relation-0"
TRUTH_A_NAME = "truth-a.json"
TRUTH_B_NAME = "truth-b.json"
AST = {
    "kind": "equation",
    "children": [{"kind": "symbol", "value": "x"}, {"kind": "number", "value": "2"}],
}
PRF_NAMES = ("symbols", "relations", "steps", "edges", "rubric_evidence", "equivalence")


def _truth() -> dict:
    return {
        "synthetic": True,
        "formulas": [
            {"id": FIRST_FORMULA_ID, "latex": "x=2", "ast": copy.deepcopy(AST)},
            {"id": SECOND_FORMULA_ID, "latex": "y=1", "ast": None},
        ],
        "symbols": ["x", "2"],
        "relations": [],
        "solution_graph": {"steps": [], "edges": []},
        "verifications": [],
        "rubric_evidence": [],
        "safe_to_suggest": True,
        "risky_case": False,
    }


def test_category_text_and_defect_schedule_are_unchanged() -> None:
    system = GEN.CATEGORIES["system-of-equations"][0]
    matrix = GEN.CATEGORIES["matrix"][1]
    assert system["formulas"][1][1] == SYSTEM_TEXT
    assert system["steps"][-1][1] == SYSTEM_TEXT
    assert matrix["steps"][-1][1] == SYSTEM_TEXT
    for category in ("vertical-step", "solution-chain"):
        spec = GEN.CATEGORIES[category][0]
        assert spec["formulas"][0][1] == LINEAR_TEXT
        assert spec["steps"][0][1] == LINEAR_TEXT
    # 类别插入顺序决定全局缺陷索引，排序会改变整个合成语料。
    assert tuple(GEN.CATEGORIES) == (
        "fraction-stacked", "fraction-inline", "radical", "exponent", "subscript",
        "system-of-equations", "inequality", "absolute-value", "trigonometric",
        "geometry-symbol", "vector", "limit", "summation", "integral", "matrix",
        "chinese-mixed", "horizontal-step", "vertical-step", "two-column", "insert",
        "crossed-out", "scratch", "low-quality", "alternative-method", "solution-chain",
    )
    assert sum(len(specs) for specs in GEN.CATEGORIES.values()) == 54
    assert GEN.DEFECT_CYCLE == (
        "exact", "latex_normalization_only", "latex_variant_ast_equal", "relation_error",
        "symbol_error", "exact", "step_grouping_error", "equivalence_error",
        "unsafe_suggestion", "latex_normalization_only", "ast_mismatch", "edge_kind_error",
        "risky_recalled", "rubric_error", "latex_variant_ast_equal", "risky_missed",
    )


@pytest.mark.parametrize(
    ("defect", "latex", "ast", "expected_latex", "expected_ast"),
    [
        ("latex_normalization_only", "(x+1)=2", AST, r"\left(x+1\right) = 2", AST),
        ("latex_normalization_only", "x=2", AST, "x = 2", AST),
        ("latex_variant_ast_equal", r"\frac{2x}{\sqrt{2}}", AST, r"\tfrac{2x}{\sqrt{2}}", AST),
        ("latex_variant_ast_equal", r"2x+\sqrt{2}", AST, r"2\cdot x+\sqrt{2}", AST),
        ("latex_variant_ast_equal", r"\sqrt{x}", AST, r"\sqrt[2]{x}", AST),
        ("latex_variant_ast_equal", "x=2", AST, "x=2{}", AST),
        ("ast_mismatch", "x=2", AST, "x=2", {"kind": "equation", "children": [{"kind": "number", "value": "2"}, {"kind": "symbol", "value": "x"}]}),
        ("ast_mismatch", "x=2", {"kind": "symbol", "value": "x"}, "x=2", {"kind": "paren", "children": [{"kind": "symbol", "value": "x"}]}),
        ("ast_mismatch", "x=2", None, "x=2", None),
        ("latex_normalization_only", None, None, None, None),
        ("latex_variant_ast_equal", None, None, None, None),
        ("ast_mismatch", None, None, None, None),
    ],
)
def test_formula_defects_preserve_priority_and_truth(
    defect: str, latex: str | None, ast: dict | None,
    expected_latex: str | None, expected_ast: dict | None,
) -> None:
    truth = _truth()
    if latex is None:
        truth["formulas"] = []
    else:
        truth["formulas"][0].update(latex=latex, ast=copy.deepcopy(ast))
    original = copy.deepcopy(truth)
    prediction = GEN.mutated_prediction(truth, defect)
    assert truth == original
    assert prediction is not truth
    assert prediction["solution_graph"] is not truth["solution_graph"]
    if latex is None:
        assert prediction == truth
    else:
        assert prediction["formulas"][0] == {
            "id": FIRST_FORMULA_ID, "latex": expected_latex, "ast": expected_ast,
        }
        assert prediction["formulas"][1] == truth["formulas"][1]
        assert prediction["formulas"][1] is not truth["formulas"][1]


@pytest.mark.parametrize(
    ("symbols", "expected"),
    [([], ["Δ"]), (["x"], ["Δ"]), (["Δ", "x"], ["Δ", "ω"]),
     (["x", "Δ"], ["x", "Δ"]), (["Δ", "ω", "x"], ["Δ", "ω", "ω"])],
)
def test_symbol_defect_checks_collision_after_removal(symbols: list, expected: list) -> None:
    truth = _truth()
    truth["symbols"] = symbols
    original = copy.deepcopy(truth)
    prediction = GEN.mutated_prediction(truth, "symbol_error")
    assert prediction["symbols"] == expected
    assert truth == original


@pytest.mark.parametrize(
    ("count", "empty_formulas", "expected_sources", "target"),
    [(0, False, [SPURIOUS_SOURCE], FIRST_FORMULA_ID),
     (1, False, [FIRST_RELATION_ID, SPURIOUS_SOURCE], FIRST_FORMULA_ID),
     (2, False, [FIRST_RELATION_ID, SPURIOUS_SOURCE], FIRST_FORMULA_ID),
     (3, False, [FIRST_RELATION_ID, "relation-1", SPURIOUS_SOURCE], FIRST_FORMULA_ID),
     (0, True, [SPURIOUS_SOURCE], "formula-1")],
)
def test_relation_defect_keeps_single_relation_and_fallback_target(
    count: int, empty_formulas: bool, expected_sources: list, target: str,
) -> None:
    truth = _truth()
    truth["relations"] = [
        {"from_id": f"relation-{index}", "to_id": SECOND_FORMULA_ID, "kind": "left_of"}
        for index in range(count)
    ]
    if empty_formulas:
        truth["formulas"] = []
    original = copy.deepcopy(truth)
    prediction = GEN.mutated_prediction(truth, "relation_error")
    assert [item["from_id"] for item in prediction["relations"]] == expected_sources
    assert prediction["relations"][-1] == {
        "from_id": SPURIOUS_SOURCE, "to_id": target, "kind": "spurious_overlap",
    }
    assert truth == original


@pytest.mark.parametrize(
    ("count", "expected_texts"),
    [(0, []), (1, ["text-0"]), (2, ["text-0;text-1"]),
     (3, ["text-0;text-1", "text-2"])],
)
def test_step_and_edge_defects_preserve_other_graph_entries(count: int, expected_texts: list) -> None:
    truth = _truth()
    truth["solution_graph"] = {
        "steps": [{"id": f"step-{index}", "normalized_text": f"text-{index}"} for index in range(count)],
        "edges": [
            {"from_step_id": "step-0", "to_step_id": "step-1", "kind": "next"},
            {"from_step_id": "step-1", "to_step_id": "step-2", "kind": "next"},
        ],
    }
    original = copy.deepcopy(truth)
    grouped = GEN.mutated_prediction(truth, "step_grouping_error")
    assert [step["normalized_text"] for step in grouped["solution_graph"]["steps"]] == expected_texts
    if count:
        assert grouped["solution_graph"]["steps"][0]["id"] == "step-0"
    assert grouped["solution_graph"]["edges"] == truth["solution_graph"]["edges"]
    edge_error = GEN.mutated_prediction(truth, "edge_kind_error")
    assert [edge["kind"] for edge in edge_error["solution_graph"]["edges"]] == ["next", "spurious_jump"]
    assert edge_error["solution_graph"]["steps"] == truth["solution_graph"]["steps"]
    assert truth == original
    empty = _truth()
    assert GEN.mutated_prediction(empty, "edge_kind_error") == empty


@pytest.mark.parametrize(("status", "flipped"), [("verified", "unverified"), ("unverified", "verified"), ("unknown", "verified")])
def test_verification_and_evidence_defects_are_local(status: str, flipped: str) -> None:
    truth = _truth()
    truth["verifications"] = [
        {"id": "other", "kind": "syntax", "status": status},
        {"id": "first", "kind": "equivalence", "status": status},
        {"id": "second", "kind": "equivalence", "status": status},
    ]
    truth["rubric_evidence"] = [
        {"rubric_criterion_key": "kept", "status": "supported"},
        {"rubric_criterion_key": "removed", "status": "unsupported"},
    ]
    original = copy.deepcopy(truth)
    verification_error = GEN.mutated_prediction(truth, "equivalence_error")
    assert [item["status"] for item in verification_error["verifications"]] == [status, flipped, status]
    assert verification_error["rubric_evidence"] == truth["rubric_evidence"]
    evidence_error = GEN.mutated_prediction(truth, "rubric_error")
    spurious = {"rubric_criterion_key": "spurious_criterion", "status": "supported"}
    assert evidence_error["rubric_evidence"] == [truth["rubric_evidence"][0], spurious]
    empty = _truth()
    assert GEN.mutated_prediction(empty, "rubric_error")["rubric_evidence"] == [spurious]
    assert GEN.mutated_prediction(empty, "equivalence_error") == empty
    for defect in ("exact", "unknown", "risky_missed", "risky_recalled", "unsafe_suggestion"):
        assert GEN.mutated_prediction(truth, defect) == truth
    assert truth == original


def test_evaluate_aggregates_counts_and_keeps_repeated_sample_order(tmp_path: Path, monkeypatch: pytest.MonkeyPatch) -> None:
    first = {"synthetic": True, "formulas": [{"id": FIRST_FORMULA_ID, "latex": "x=2", "ast": AST}], "symbols": ["x"],
             "verifications": [{"id": "check", "kind": "equivalence", "status": "verified"}], "safe_to_suggest": True}
    first_prediction = copy.deepcopy(first)
    first_prediction["formulas"][0]["latex"] = "x = 2"
    first_prediction["symbols"] = ["x", "extra"]
    first_prediction["verifications"][0]["status"] = "unverified"
    first_prediction["suggestion_allowed"] = True
    second = {"synthetic": True, "formulas": [{"id": SECOND_FORMULA_ID, "latex": "y=1", "ast": None}], "symbols": ["y"],
              "safe_to_suggest": False, "risky_case": True}
    second_prediction = {"synthetic": True, "formulas": [{"id": SECOND_FORMULA_ID, "latex": "y=2", "ast": None}],
                         "suggestion_allowed": True, "human_review_required": False}
    documents = {TRUTH_A_NAME: first, "a.json": first_prediction, TRUTH_B_NAME: second, "b.json": second_prediction}
    loaded = []

    def load_json(path: Path) -> dict:
        loaded.append(path)
        return documents[path.name]

    monkeypatch.setattr(BENCH, "load_json", load_json)
    entries = [
        {"sample_id": "a", "subject_code": "mathematics", "ground_truth": TRUTH_A_NAME, "original": "a-1.png"},
        {"sample_id": "b", "subject_code": "physics", "ground_truth": TRUTH_B_NAME, "original": "b.png"},
        {"sample_id": "a", "subject_code": "chemistry", "ground_truth": TRUTH_A_NAME, "original": "a-2.png"},
    ]
    manifest = tmp_path / "manifest.jsonl"
    manifest.write_text("\n\t\n" + "\n\n".join(json.dumps(entry) for entry in entries) + "\n", encoding="utf-8")
    predictions = tmp_path / "predictions"
    report = BENCH.evaluate(manifest, predictions, dataset="synthetic")
    assert loaded == [tmp_path / TRUTH_A_NAME, predictions / "a.json", tmp_path / TRUTH_B_NAME,
                      predictions / "b.json", tmp_path / TRUTH_A_NAME, predictions / "a.json"]
    expected_metrics = {f"{name}_{metric}": None for name in PRF_NAMES for metric in ("precision", "recall", "f1")}
    expected_metrics.update(
        formula_exact_accuracy=2 / 3, ast_exact_accuracy=1.0,
        symbols_precision=0.5, symbols_recall=2 / 3, symbols_f1=pytest.approx(4 / 7),
        equivalence_precision=0.0, equivalence_recall=0.0,
        unsafe_suggestion_rate=1 / 3, risky_case_recall=0.0,
    )
    assert report == {
        "schema_version": "math-bench-v1", "dataset": "synthetic", "sample_count": 3,
        "metrics": expected_metrics,
        "samples": [{"sample_id": "a", "original": "a-1.png"}, {"sample_id": "b", "original": "b.png"},
                    {"sample_id": "a", "original": "a-2.png"}],
    }


@pytest.mark.parametrize("subject", [None, "history", ""])
def test_evaluate_empty_denominators_and_rejection_before_loading(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, subject: str | None,
) -> None:
    manifest = tmp_path / "manifest.jsonl"
    manifest.write_text("\n \t\n", encoding="utf-8")
    report = BENCH.evaluate(manifest, tmp_path)
    assert report["dataset"] is None
    assert report["sample_count"] == 0
    assert report["samples"] == []
    assert len(report["metrics"]) == 22
    assert all(value is None for value in report["metrics"].values())

    def unexpected_load(path: Path) -> dict:
        pytest.fail(f"unsupported subject should fail before loading {path}")

    monkeypatch.setattr(BENCH, "load_json", unexpected_load)
    manifest.write_text(json.dumps({"sample_id": "rejected", "subject_code": subject}), encoding="utf-8")
    with pytest.raises(ValueError) as error:
        BENCH.evaluate(manifest, tmp_path)
    assert str(error.value) == f"formula benchmark does not accept subject {subject!r}"
