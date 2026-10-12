from __future__ import annotations

import importlib.util
import json
import sys
import unittest
from argparse import Namespace
from contextlib import ExitStack
from copy import deepcopy
from pathlib import Path
from tempfile import TemporaryDirectory
from types import SimpleNamespace
from unittest.mock import Mock, call, patch

from ocr_worker import formula_calibration as calibration
from ocr_worker import symbolic_runner as symbolic
from ocr_worker.api import AuthenticationError

spec = importlib.util.spec_from_file_location(
    "benchmark_batch37", Path(__file__).parents[1] / "benchmarks" / "benchmark_local.py",
)
benchmark = importlib.util.module_from_spec(spec)
spec.loader.exec_module(benchmark)


def benchmark_arguments(manifest: Path, **changes) -> Namespace:
    values = {
        "manifest": manifest, "sample_dir": None, "roi_dataset": None, "count": 0,
        "run_id": "batch37", "model_version": "ppocr-v5-mobile", "cpu_threads": 4,
        "enable_mkldnn": "false", "enable_hpi": False, "use_textline_orientation": True,
        "text_det_limit_type": "min", "text_det_limit_side_len": 64,
        "text_recognition_batch_size": 1, "preprocess_profile": "default",
    }
    return Namespace(**(values | changes))


def benchmark_engine() -> SimpleNamespace:
    return SimpleNamespace(
        model_version="ppocr-v5-mobile", device="cpu", cpu_threads=4,
        effective_mkldnn=False, enable_hpi=False, use_textline_orientation=True,
        text_det_limit_type="min", text_det_limit_side_len=64,
        text_recognition_batch_size=1, initialize=Mock(),
        recognize=Mock(return_value=[]), recognize_region=Mock(return_value=[]),
    )


def text_block(text: str, confidence: float = 0.9) -> SimpleNamespace:
    return SimpleNamespace(text=text, confidence=confidence)


def equation(value: int, symbols: tuple[str, ...] = ("x",)) -> dict:
    return {
        "kind": "equation",
        "children": [{"kind": "symbol", "value": symbol} for symbol in symbols]
        + [{"kind": "number", "value": str(value)}],
    }


def symbolic_contract() -> dict:
    return {
        "formulas": [{"id": "f1", "ast": equation(2)}, {"id": "f2", "ast": equation(3)}],
        "solution_graph": {
            "steps": [{"id": "s1", "formula_ids": ["f1"]}, {"id": "s2", "formula_ids": ["f2"]}],
            "edges": [{"from_step_id": "s1", "to_step_id": "s2", "kind": "derives"}],
        },
    }


def symbolic_verifier() -> SimpleNamespace:
    return SimpleNamespace(
        normalize=Mock(return_value={"ast": equation(2)}),
        verify_transition=Mock(return_value={"status": "contradicted", "relation": "different_solution_set"}),
        solve=Mock(return_value={"status": "verified", "solution_set": "{3}"}),
    )


class OCRWorkflowsBatch37Tests(unittest.TestCase):
    def setUp(self) -> None:
        temporary = TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.directory = Path(temporary.name)

    def manifest(self, rows) -> Path:
        path = self.directory / "manifest.json"
        path.write_text(json.dumps(rows), encoding="utf-8")
        for name in ("a.png", "b.jpg"):
            (self.directory / name).write_bytes(name.encode())
        return path

    def run_benchmark(self, args, engine, *, ticks=(1, 2), rss=(10, 20), roi=False):
        with ExitStack() as stack:
            stack.enter_context(patch.object(benchmark, "PaddleOCREngine", return_value=engine))
            stack.enter_context(patch.object(benchmark.time, "perf_counter", side_effect=ticks))
            stack.enter_context(patch.object(benchmark, "_rss_bytes", side_effect=rss))
            if roi:
                # 只模拟图像 I/O，正式入口仍执行全部预热、区域调度和指标累计；不加载原生模型。
                image = Mock()
                opened = Mock()
                opened.__enter__ = Mock(return_value=image)
                opened.__exit__ = Mock(return_value=False)
                pil = SimpleNamespace(Image=SimpleNamespace(open=Mock(return_value=opened)))
                stack.enter_context(patch.dict(sys.modules, {"PIL": pil}))
                stack.enter_context(patch.object(benchmark, "_crop_normalized", return_value=b"crop"))
            return benchmark.run(args)

    def calibration_profile(self, images=None, **changes):
        values = {
            "batch_sizes": [1], "repeats": 1, "lifecycle_goal": "compatibility",
            "engine": SimpleNamespace(
                initialize=Mock(),
                recognize_formulas=Mock(side_effect=lambda rows, batch_size: [SimpleNamespace(canonical_latex="x") for _ in rows]),
            ),
            "detector": SimpleNamespace(initialize=Mock()), "clock": iter(range(100)).__next__,
        }
        values.update(changes)
        settings = SimpleNamespace(
            device="cpu", formula_layout_model_version="layout", formula_model_version="primary",
            formula_fallback_model_version="fallback",
        )
        with patch.object(calibration, "formula_hardware_descriptor", return_value={"device": "cpu"}), patch.object(
            calibration, "formula_software_descriptor", return_value={"model": "primary"}
        ), patch.object(calibration, "_rss_bytes", return_value=123):
            return calibration.calibrate_formula_runtime(settings, [b"one"] if images is None else images, **values)

    def test_manifest_preserves_fields_resolves_paths_and_sorts(self):
        source = {"samples": [{"path": "b.jpg", "text": "b", "regions": []}, "a.png"]}
        path = self.manifest(source)
        result = benchmark._load_manifest(path, None)
        self.assertEqual([Path(row["path"]).name for row in result], ["a.png", "b.jpg"])
        self.assertEqual(result[1]["text"], "b")
        self.assertEqual(result[1]["regions"], [])
        self.assertTrue(all(Path(row["path"]).is_absolute() for row in result))
        self.assertEqual(source["samples"][0]["path"], "b.jpg")

    def test_manifest_has_priority_over_sample_directory(self):
        path = self.manifest(["b.jpg"])
        result = benchmark._load_manifest(path, self.directory)
        self.assertEqual([Path(row["path"]).name for row in result], ["b.jpg"])

    def test_manifest_validation_preserves_error_messages(self):
        for payload, message in (({"samples": {}}, "samples array"), ([{}], "requires path"), ([{"path": 1}], "requires path")):
            manifest_path = self.manifest(payload)
            with self.subTest(payload=payload), self.assertRaisesRegex(ValueError, message):
                benchmark._load_manifest(manifest_path, None)
        with self.assertRaisesRegex(ValueError, "provide --manifest or --sample-dir"):
            benchmark._load_manifest(None, None)

    def test_sample_directory_filters_supported_suffixes(self):
        self.manifest([])
        (self.directory / "upper.TIFF").write_bytes(b"upper")
        (self.directory / "ignore.txt").write_bytes(b"ignore")
        self.assertEqual(
            [Path(row["path"]).name for row in benchmark._load_manifest(None, self.directory)],
            ["a.png", "b.jpg", "upper.TIFF"],
        )

    def test_baseline_keeps_argument_mutation_and_implicit_threads(self):
        engine = benchmark_engine()
        args = benchmark_arguments(
            self.manifest(["a.png"]), run_id="local-baseline-v1", model_version="candidate",
            enable_hpi=True, enable_mkldnn="true", use_textline_orientation=False,
            text_recognition_batch_size=8, text_det_limit_side_len=512, text_det_limit_type="max",
        )
        engine.initialize.side_effect = lambda: self.assertIsNone(engine.cpu_threads)
        with patch.object(benchmark, "LegacyBaselineEngine", return_value=engine) as constructor:
            result = self.run_benchmark(args, engine)
        self.assertEqual((args.model_version, args.enable_hpi, args.enable_mkldnn), ("ppocr-v5-mobile", False, "false"))
        self.assertEqual((args.use_textline_orientation, args.text_recognition_batch_size), (True, 1))
        self.assertEqual((args.text_det_limit_type, args.text_det_limit_side_len), (None, 0))
        self.assertIsNone(constructor.call_args.kwargs["text_det_limit_side_len"])
        self.assertIsNone(result["cpu_threads"])
        self.assertEqual(engine.recognize.call_count, 6)

    def test_initialization_and_warmup_failures_propagate(self):
        args = benchmark_arguments(self.manifest(["a.png"]))
        for operation in ("initialize", "recognize"):
            engine = benchmark_engine()
            getattr(engine, operation).side_effect = RuntimeError(operation)
            with self.subTest(operation=operation), self.assertRaisesRegex(RuntimeError, operation):
                self.run_benchmark(args, engine)

    def test_negative_count_precedes_empty_bundle_error(self):
        path = self.manifest([])
        arguments = benchmark_arguments(path, count=-1)
        with self.assertRaisesRegex(ValueError, 'non-negative'):
            benchmark.run(arguments)
        arguments = benchmark_arguments(path)
        with self.assertRaisesRegex(ValueError, 'empty'):
            benchmark.run(arguments)

    def test_missing_ground_truth_keeps_quality_unavailable(self):
        engine = benchmark_engine()
        engine.recognize.return_value = [text_block("private")]
        result = self.run_benchmark(benchmark_arguments(self.manifest(["a.png"])), engine)
        self.assertIsNone(result["quality"]["CER"])
        self.assertIsNone(result["quality"]["WER"])
        self.assertEqual(result["result_block_count"], 1)
        self.assertNotIn("private", json.dumps(result))

    def test_second_roi_failure_preserves_first_roi_metrics_and_timing(self):
        regions = [{"bbox": {}, "text": "a"}, {"bbox": {}, "text": "b"}]
        engine = benchmark_engine()
        engine.recognize_region.side_effect = [[]] * 10 + [[text_block("a")], RuntimeError("second ROI")]
        result = self.run_benchmark(
            benchmark_arguments(self.manifest([{"path": "a.png", "regions": regions}])),
            engine, ticks=(2, 2.5), rss=(20, 10), roi=True,
        )
        self.assertEqual(engine.recognize_region.call_count, 12)
        self.assertEqual((result["region_count"], result["result_block_count"]), (1, 1))
        self.assertEqual((result["failure_rate"], result["quality"]["page_success_rate"]), (1, 0))
        self.assertEqual((result["quality"]["CER"], result["quality"]["WER"]), (0, 0))
        self.assertEqual(result["quality"]["region_hit_rate"], 1)
        self.assertEqual(result["average_duration_ms/page"], 500)
        self.assertEqual(result["process_peak_rss_bytes"], 20)

    def test_roi_blank_and_confidence_boundaries_use_original_denominators(self):
        regions = [{"bbox": {}, "text": value} for value in ("", "", "xy", "p")]
        engine = benchmark_engine()
        engine.recognize_region.side_effect = [[]] * 20 + [[], [text_block("ghost", 0.8)], [text_block("xy", 0.79)], [text_block("p", 0.8)]]
        result = self.run_benchmark(
            benchmark_arguments(self.manifest([{"path": "a.png", "regions": regions}])), engine, roi=True,
        )
        self.assertEqual(result["region_count"], 4)
        self.assertEqual(result["quality"]["blank_false_positive_rate"], 0.5)
        self.assertEqual(result["quality"]["review_trigger_rate"], 0.5)
        self.assertEqual(result["quality"]["region_hit_rate"], 0.75)
        self.assertEqual(result["quality"]["CER"], 1)
        self.assertEqual(result["quality"]["WER"], 0.25)

    def test_full_page_failure_continues_and_updates_peak_rss(self):
        engine = benchmark_engine()
        engine.recognize.side_effect = [[]] * 5 + [RuntimeError("page"), [text_block("b")]]
        result = self.run_benchmark(
            benchmark_arguments(self.manifest([{"path": "a.png", "text": "a"}, {"path": "b.jpg", "text": "b"}])),
            engine, ticks=(1, 2, 3, 5), rss=(None, 10, 30),
        )
        self.assertEqual((result["failure_rate"], result["sample_count"]), (0.5, 2))
        self.assertEqual(result["average_duration_ms/page"], 1500)
        self.assertEqual(result["process_peak_rss_bytes"], 30)
        self.assertEqual(result["quality"]["CER"], 0)

    def test_calibration_input_validation_keeps_first_error(self):
        scenarios = [([], 0, [], "bad", "non-empty"), ([b""], 0, [], "bad", "non-empty"),
                     ([b"x"], 0, [], "bad", "positive"), ([b"x"], 1, [17], "bad", "batch sizes"),
                     ([b"x"], 1, [1], "bad", "lifecycle goal")]
        for images, repeats, batches, goal, message in scenarios:
            with self.subTest(message=message), self.assertRaisesRegex(ValueError, message):
                self.calibration_profile(images, repeats=repeats, batch_sizes=batches, lifecycle_goal=goal)

    def test_calibration_reuses_baseline_sorts_deduplicates_and_counts_progress(self):
        engine = SimpleNamespace(initialize=Mock(), recognize_formulas=Mock(return_value=[SimpleNamespace(canonical_latex="x")]))
        events = []
        result = self.calibration_profile(engine=engine, batch_sizes=[4, 2, 1, 2], repeats=2, progress=events.append)
        self.assertEqual([item.kwargs["batch_size"] for item in engine.recognize_formulas.call_args_list], [1, 1, 2, 2, 4, 4])
        self.assertEqual(events[0], {"phase": "model_ready", "completed_runs": 0, "total_runs": 6})
        self.assertEqual([(event["batch_size"], event["repeat_no"], event["completed_runs"]) for event in events[1:]],
                         [(1, 1, 1), (1, 2, 2), (2, 1, 3), (2, 2, 4), (4, 1, 5), (4, 2, 6)])
        self.assertEqual(result["recommendation"]["batch_size"], 1)
        self.assertFalse(result["evidence"]["absolute_accuracy_evaluated"])

    def test_calibration_baseline_outside_candidates_does_not_count_progress(self):
        engine = SimpleNamespace(initialize=Mock(), recognize_formulas=Mock(return_value=[SimpleNamespace(canonical_latex="x")]))
        events = []
        self.calibration_profile(engine=engine, batch_sizes=[4, 2], repeats=2, progress=events.append)
        self.assertEqual([item.kwargs["batch_size"] for item in engine.recognize_formulas.call_args_list], [1, 2, 2, 4, 4])
        self.assertEqual([event["completed_runs"] for event in events], [0, 1, 2, 3, 4])
        self.assertEqual([event["batch_size"] for event in events[1:]], [2, 2, 4, 4])

    def test_calibration_clock_initialization_and_progress_order(self):
        trace = []
        ticks = iter(range(20))
        def clock():
            value = next(ticks)
            trace.append(("clock", value))
            return value
        detector = SimpleNamespace(initialize=lambda: trace.append("detector"))
        engine = SimpleNamespace(
            initialize=lambda: trace.append("engine"),
            recognize_formulas=lambda rows, batch_size: trace.append(("recognize", batch_size)) or [SimpleNamespace(canonical_latex="x")],
        )
        self.calibration_profile(engine=engine, detector=detector, clock=clock, batch_sizes=[1, 2],
                                 progress=lambda event: trace.append((event["phase"], event["completed_runs"])))
        self.assertEqual(trace, [("clock", 0), "detector", "engine", ("clock", 1), ("model_ready", 0),
                                 ("clock", 2), ("recognize", 1), ("clock", 3), ("candidate_run", 1),
                                 ("clock", 4), ("recognize", 2), ("clock", 5), ("candidate_run", 2)])

    def test_calibration_inconsistency_latches_and_recognized_count_is_maximum(self):
        outputs = [["x", "x"], ["x", "x"], ["x", "x"], ["wrong", "wrong"], ["x", "x"], ["", ""]]
        engine = SimpleNamespace(initialize=Mock(), recognize_formulas=Mock(
            side_effect=[[SimpleNamespace(canonical_latex=value) for value in row] for row in outputs],
        ))
        result = self.calibration_profile([b"one", b"two"], engine=engine, batch_sizes=[1, 2], repeats=3)
        row = result["evidence"]["measurements"][1]
        self.assertFalse(row["output_consistency_passed"])
        self.assertEqual(row["recognized_roi_count"], 2)
        self.assertEqual(result["recommendation"]["batch_size"], 1)

    def test_calibration_negative_durations_clamp_and_latency_is_explicit(self):
        result = self.calibration_profile(clock=iter([5, 4, 3, 2]).__next__, lifecycle_goal="latency")
        self.assertEqual(result["evidence"]["model_load_ms"], 0)
        row = result["evidence"]["measurements"][0]
        self.assertEqual((row["average_ms_per_bundle"], row["throughput_roi_per_second"]), (0, 0))
        self.assertEqual(result["recommendation"], {"objective": "warm_latency", "lifecycle": "resident", "batch_size": 1})

    def test_calibration_no_usable_output_fails_before_hardware_probe(self):
        engine = SimpleNamespace(initialize=Mock(), recognize_formulas=Mock(return_value=[]))
        settings = SimpleNamespace(device="cpu")
        detector = SimpleNamespace(initialize=Mock())
        clock_values = iter(range(10))
        with patch.object(calibration, 'formula_hardware_descriptor') as hardware, self.assertRaisesRegex(RuntimeError, 'no formula batch'):
            calibration.calibrate_formula_runtime(settings, [b'x'], batch_sizes=[1], repeats=1, lifecycle_goal='compatibility', engine=engine, detector=detector, clock=clock_values.__next__)
        hardware.assert_not_called()

    def test_calibration_model_and_progress_errors_propagate(self):
        for stage in ("detector", "engine", "model_ready", "candidate_run"):
            engine = SimpleNamespace(initialize=Mock(), recognize_formulas=Mock(return_value=[SimpleNamespace(canonical_latex="x")]))
            detector = SimpleNamespace(initialize=Mock())
            if stage in {"detector", "engine"}:
                (detector if stage == "detector" else engine).initialize.side_effect = RuntimeError(stage)
            def progress(event, stage=stage):
                if event["phase"] == stage:
                    raise RuntimeError(stage)
            with self.subTest(stage=stage), self.assertRaisesRegex(RuntimeError, stage):
                self.calibration_profile(engine=engine, detector=detector, progress=progress)
            if stage in {"detector", "engine", "model_ready"}:
                engine.recognize_formulas.assert_not_called()

    def test_symbolic_active_formulas_preserve_fallback_and_normalize_order(self):
        source = {"formulas": [{"id": "f1", "canonical_latex": "canonical", "raw_latex": "raw"},
                               {"id": "unused", "raw_latex": "inactive"}, {"id": "f2", "canonical_latex": "", "raw_latex": "fallback"}],
                  "solution_graph": {"steps": [{"id": "s1", "formula_ids": ["f1", "f2"]}], "edges": []}}
        original = deepcopy(source)
        verifier = symbolic_verifier()
        verifier.normalize.return_value = {"ast": {}}
        checks = symbolic.build_symbolic_verifications(source, verifier)
        self.assertEqual(verifier.normalize.call_args_list, [call("canonical"), call("fallback")])
        self.assertEqual(checks[0]["reason_code"], "no_supported_symbolic_checks")
        self.assertEqual(source, original)

    def test_symbolic_parse_checks_precede_transitions_and_terminal_solutions(self):
        source = symbolic_contract()
        source["formulas"].insert(0, {"id": "bad", "raw_latex": "unsupported"})
        source["solution_graph"]["steps"][0]["formula_ids"].insert(0, "bad")
        verifier = symbolic_verifier()
        verifier.normalize.return_value = {"status": "uncertain"}
        checks = symbolic.build_symbolic_verifications(source, verifier)
        self.assertEqual([check["id"] for check in checks], ["sympy-parse-bad", "sympy-transition-f1-f2", "sympy-solve-f2"])
        self.assertEqual(checks[1]["details"], {"relation": "different_solution_set", "from_step_id": "s1", "from_formula_id": "f1"})
        self.assertEqual(checks[2]["reason_code"], "solution_set_computed")

    def test_symbolic_uses_last_source_and_first_target_formula(self):
        source = symbolic_contract()
        source["formulas"] += [{"id": "first", "ast": equation(1)}, {"id": "last", "ast": equation(4, ("x", "y"))}]
        source["solution_graph"]["steps"][0]["formula_ids"].insert(0, "first")
        source["solution_graph"]["steps"][1]["formula_ids"].append("last")
        verifier = symbolic_verifier()
        symbolic.build_symbolic_verifications(source, verifier)
        verifier.verify_transition.assert_called_once_with(equation(2), equation(3))
        verifier.solve.assert_called_once_with(equation(3), "x")

    def test_symbolic_duplicate_transition_skips_heartbeat_and_verifier(self):
        source = symbolic_contract()
        source["solution_graph"]["edges"] *= 2
        verifier = symbolic_verifier()
        heartbeat = SimpleNamespace(raise_if_failed=Mock())
        checks = symbolic.build_symbolic_verifications(source, verifier, heartbeat)
        self.assertEqual(len(checks), 2)
        self.assertEqual(heartbeat.raise_if_failed.call_count, 4)
        verifier.verify_transition.assert_called_once()
        verifier.solve.assert_called_once()

    def test_symbolic_reading_order_edges_still_exclude_nonterminal_solutions(self):
        source = symbolic_contract()
        source["solution_graph"]["edges"][0]["kind"] = "next"
        verifier = symbolic_verifier()
        checks = symbolic.build_symbolic_verifications(source, verifier)
        verifier.verify_transition.assert_not_called()
        verifier.solve.assert_called_once_with(equation(3), "x")
        self.assertEqual([check["id"] for check in checks], ["sympy-solve-f2"])

    def test_symbolic_missing_ast_keeps_uncertain_transition(self):
        source = symbolic_contract()
        del source["formulas"][0]["ast"]
        verifier = symbolic_verifier()
        verifier.normalize.return_value = {"status": "uncertain"}
        checks = symbolic.build_symbolic_verifications(source, verifier)
        self.assertEqual([check["reason_code"] for check in checks],
                         ["unsupported_expression", "missing_formula_ast", "solution_set_computed"])
        self.assertEqual(checks[1]["confidence"], 0)
        verifier.verify_transition.assert_not_called()

    def test_symbolic_empty_dict_ast_is_not_renormalized(self):
        source = {"formulas": [{"id": "f", "ast": {}, "raw_latex": "x"}],
                  "solution_graph": {"steps": [{"id": "s", "formula_ids": ["f"]}], "edges": []}}
        verifier = symbolic_verifier()
        checks = symbolic.build_symbolic_verifications(source, verifier)
        verifier.normalize.assert_not_called()
        verifier.solve.assert_not_called()
        self.assertEqual(checks[0]["reason_code"], "no_supported_symbolic_checks")

    def test_symbolic_same_formula_in_two_terminal_steps_is_not_deduplicated(self):
        source = {"formulas": [{"id": "f", "ast": equation(3)}], "solution_graph": {
            "steps": [{"id": "s1", "formula_ids": ["f"]}, {"id": "s2", "formula_ids": ["f"]}], "edges": []}}
        verifier = symbolic_verifier()
        checks = symbolic.build_symbolic_verifications(source, verifier)
        self.assertEqual([check["step_id"] for check in checks], ["s1", "s2"])
        self.assertEqual(verifier.solve.call_count, 2)

    def test_symbolic_heartbeat_failure_stops_each_stage_before_remote_call(self):
        # 两次活跃公式心跳之后才进入推导，第三次推导心跳之后才进入终端求解。
        for failed_call, transitions in ((1, 0), (3, 0), (4, 1)):
            source = symbolic_contract()
            if failed_call == 1:
                del source["formulas"][0]["ast"]
            verifier = symbolic_verifier()
            heartbeat = SimpleNamespace(raise_if_failed=Mock(side_effect=[None] * (failed_call - 1) + [RuntimeError("lease")]))
            with self.subTest(failed_call=failed_call), self.assertRaisesRegex(RuntimeError, "lease"):
                symbolic.build_symbolic_verifications(source, verifier, heartbeat)
            verifier.normalize.assert_not_called()
            self.assertEqual(verifier.verify_transition.call_count, transitions)
            verifier.solve.assert_not_called()

    def test_symbolic_authentication_error_propagates_without_failure_writeback(self):
        runtime = {"id": "task", "lease_token": "lease", "tenant_id": "tenant",
                   "source_type": "math_understanding_artifact", "source_id": "artifact", "payload": {}}
        api = SimpleNamespace(activate_task=Mock(), get_math_verification_input=Mock(side_effect=AuthenticationError("expired")),
                              fail_math_verification_task=Mock(), complete_math_verification_task=Mock())
        heartbeat = Mock()
        context = Mock()
        context.__enter__ = Mock(return_value=heartbeat)
        context.__exit__ = Mock(return_value=False)
        config = SimpleNamespace(worker_id="worker", heartbeat_interval=10, lease_seconds=300, heartbeat_timeout=3)
        runner = symbolic.SymbolicVerificationRunner(api=api, verifier=symbolic_verifier(), config=config)
        with patch.object(symbolic, "_LeaseHeartbeat", return_value=context), self.assertRaises(AuthenticationError):
            runner._process(runtime)
        api.fail_math_verification_task.assert_not_called()
        api.complete_math_verification_task.assert_not_called()


if __name__ == "__main__":
    unittest.main()
