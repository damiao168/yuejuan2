import importlib.util
import json
import sys
import tempfile
import unittest
from unittest.mock import patch
from pathlib import Path


sys.dont_write_bytecode = True
MODULE_PATH = Path(__file__).with_name("evaluate.py")
SPEC = importlib.util.spec_from_file_location("ai_evaluation_evaluate", MODULE_PATH)
evaluate = importlib.util.module_from_spec(SPEC)
assert SPEC.loader is not None
SPEC.loader.exec_module(evaluate)


class EvaluationMetricsTest(unittest.TestCase):
    def test_compute_metrics_includes_required_quality_metrics(self):
        rows = [
            {
                "sample_id": "s1",
                "human_score": 5.0,
                "suggested_score": 5.0,
                "confidence": 0.9,
                "needs_human_review": False,
                "risk_flags": [],
            },
            {
                "sample_id": "s2",
                "human_score": 4.0,
                "suggested_score": 3.0,
                "confidence": 0.7,
                "needs_human_review": True,
                "risk_flags": ["low_model_confidence"],
            },
            {
                "sample_id": "s3",
                "human_score": 2.0,
                "suggested_score": 4.0,
                "confidence": 0.6,
                "needs_human_review": False,
                "risk_flags": ["prompt_injection_suspected"],
            },
        ]

        metrics = evaluate.compute_metrics(rows, low_confidence_threshold=0.8, adjacent_tolerance=1.0, exact_tolerance=1e-9)

        self.assertEqual(metrics["sample_count"], 3)
        self.assertAlmostEqual(metrics["mae"], 1.0)
        self.assertAlmostEqual(metrics["rmse"], (5 / 3) ** 0.5)
        self.assertAlmostEqual(metrics["exact_agreement"], 1 / 3)
        self.assertAlmostEqual(metrics["adjacent_agreement"], 2 / 3)
        self.assertAlmostEqual(metrics["score_bias"], 1 / 3)
        self.assertEqual(metrics["low_confidence"]["count"], 2)
        self.assertAlmostEqual(metrics["low_confidence"]["routing_coverage"], 0.5)
        self.assertEqual(metrics["human_review"]["risky_not_routed_sample_ids"], ["s3"])

    def test_build_report_groups_by_model_and_prompt(self):
        samples = evaluate.validate_samples(
            [
                sample("s1", 5, 5),
                sample("s2", 4, 4),
            ]
        )
        predictions = [
            prediction("s1", "m1", "p1", 5, 0.9, False),
            prediction("s2", "m1", "p1", 3, 0.7, True),
            prediction("s1", "m1", "p2", 4, 0.8, False),
            prediction("s2", "m2", "p2", 4, 0.95, False),
        ]
        evaluate.validate_predictions(predictions, samples)

        report = evaluate.build_report(
            samples_path=Path("samples.jsonl"),
            predictions_path=Path("predictions.jsonl"),
            samples_by_id=samples,
            predictions=predictions,
            low_confidence_threshold=0.8,
            adjacent_tolerance=1.0,
            exact_tolerance=1e-9,
        )

        self.assertIs(report["dataset"]["synthetic_only"], True)
        self.assertEqual(len(report["by_variant"]), 3)
        self.assertEqual({item["model_version"] for item in report["by_model_version"]}, {"m1", "m2"})
        self.assertEqual({item["prompt_version"] for item in report["by_prompt_version"]}, {"p1", "p2"})

    def test_write_reports_outputs_json_and_markdown(self):
        samples = evaluate.validate_samples([sample("s1", 5, 5)])
        predictions = [prediction("s1", "m1", "p1", 5, 0.9, False)]
        report = evaluate.build_report(
            samples_path=Path("samples.jsonl"),
            predictions_path=Path("predictions.jsonl"),
            samples_by_id=samples,
            predictions=predictions,
            low_confidence_threshold=0.8,
            adjacent_tolerance=1.0,
            exact_tolerance=1e-9,
        )

        with tempfile.TemporaryDirectory() as tmp:
            json_path, md_path = evaluate.write_reports(report, Path(tmp))
            self.assertTrue(json_path.exists())
            self.assertTrue(md_path.exists())
            loaded = json.loads(json_path.read_text(encoding="utf-8"))
            self.assertEqual(loaded["overall"]["sample_count"], 1)
            markdown = md_path.read_text(encoding="utf-8")
            self.assertIn("Synthetic evaluation only", markdown)
            self.assertIn("Variant Comparison", markdown)

    def test_rejects_non_synthetic_samples(self):
        synthetic_sample = sample('s1', 5, 5)
        with self.assertRaisesRegex(ValueError, 'must be explicitly synthetic'):
            evaluate.validate_samples([{**synthetic_sample, 'synthetic': False}])

    def test_rejects_invalid_numbers_in_samples_and_predictions(self):
        for value in (float("nan"), float("inf"), -float("inf"), "NaN", "Infinity", "5", True, 10**1000):
            for field in ("human_score", "max_score"):
                with self.subTest(value=repr(value), field=field):
                    record = sample("s1", 5, 5)
                    if field == "max_score":
                        record["question"][field] = value
                    else:
                        record[field] = value
                    with self.assertRaisesRegex(ValueError, "finite number"):
                        evaluate.validate_samples([record])
            samples = evaluate.validate_samples([sample("s1", 5, 5)])
            for field in ("suggested_score", "confidence"):
                with self.subTest(value=repr(value), field=field):
                    record = prediction("s1", "m", "p", 5, .9, False)
                    record[field] = value
                    with self.assertRaisesRegex(ValueError, "finite number"):
                        evaluate.validate_predictions([record], samples)

    def test_jsonl_rejects_nonstandard_constants_with_source_line(self):
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "records.jsonl"
            for value in ("NaN", "Infinity", "-Infinity"):
                path.write_text('\n{"score":' + value + '}\n', encoding="utf-8")
                with self.assertRaisesRegex(ValueError, ":2 is not valid JSON"):
                    evaluate.load_jsonl(path)

    def test_cli_and_empty_metrics_reject_invalid_configuration(self):
        cases = (("low-confidence-threshold", "nan"), ("low-confidence-threshold", "1.1"),
                 ("adjacent-tolerance", "inf"), ("adjacent-tolerance", "-1"),
                 ("exact-tolerance", "nan"), ("exact-tolerance", "-1"))
        for option, value in cases:
            with self.subTest(option=option, value=value), patch.object(sys, "argv", ["evaluate", "--samples", "missing", "--predictions", "missing", f"--{option}={value}"]), patch("sys.stderr"):
                with self.assertRaises(SystemExit) as error:
                    evaluate.parse_args()
                self.assertEqual(error.exception.code, 2)
        with self.assertRaisesRegex(ValueError, "finite number"):
            evaluate.compute_metrics([], low_confidence_threshold=.8, adjacent_tolerance=1, exact_tolerance=float("nan"))

    def test_invalid_report_preserves_existing_outputs(self):
        with tempfile.TemporaryDirectory() as tmp:
            directory = Path(tmp)
            outputs = [directory / "ai_evaluation_report.json", directory / "ai_evaluation_report.md"]
            for path in outputs:
                path.write_text("previous report", encoding="utf-8")
            with self.assertRaises(ValueError):
                evaluate.write_reports({"metric": float("nan")}, directory)
            for path in outputs:
                self.assertEqual(path.read_text(encoding="utf-8"), "previous report")


def sample(sample_id, human_score, max_score):
    return {
        "sample_id": sample_id,
        "synthetic": True,
        "question": {"id": f"q-{sample_id}", "question_type": "short_answer", "max_score": max_score},
        "rubric": {"version": "synthetic", "max_score": max_score, "points": []},
        "answer": {"synthetic": True, "text": "Synthetic answer."},
        "human_score": human_score,
        "human_rationale": "Synthetic rationale.",
        "expected_points": [],
    }


def prediction(sample_id, model, prompt, score, confidence, needs_review):
    return {
        "sample_id": sample_id,
        "synthetic": True,
        "model_version": model,
        "prompt_version": prompt,
        "suggested_score": score,
        "confidence": confidence,
        "needs_human_review": needs_review,
        "risk_flags": [],
        "matched_points": [],
    }


if __name__ == "__main__":
    unittest.main()
