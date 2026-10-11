from __future__ import annotations

import hashlib
import json
import os
import unittest
from contextlib import ExitStack
from copy import deepcopy
from dataclasses import replace
from unittest.mock import Mock, patch

from ocr_worker import __main__ as worker_main
from ocr_worker import deployment_profile as profile
from ocr_worker.config import Settings, load_settings

BASE_ENV = {
    "EDUGRADE_API_BASE_URL": "http://api-gateway:8080",
    "EDUGRADE_OCR_WORKER_TENANT_CODE": "demo",
    "EDUGRADE_OCR_WORKER_USERNAME": "ocr-worker",
    "EDUGRADE_OCR_WORKER_PASSWORD": "test-only",
}


def worker_settings(**overrides):
    return replace(
        Settings(
            api_base_url=BASE_ENV["EDUGRADE_API_BASE_URL"],
            tenant_code="demo",
            username="ocr-worker",
            password="test-only",
            formula_deployment_profile_path="D:/tools/test-formula-profile.json",
        ),
        **overrides,
    )


class StartupConfigBatch36Tests(unittest.TestCase):
    def test_first_error_keeps_validation_order_across_groups(self):
        # 同时设置多个错误，保护提取职责后仍保留的首次失败优先顺序。
        cases = [
            ({"EDUGRADE_API_BASE_URL": "invalid", "EDUGRADE_OCR_WORKER_TENANT_CODE": ""}, "API_BASE_URL"),
            ({"EDUGRADE_OCR_WORKER_TENANT_CODE": "", "EDUGRADE_OCR_MIN_CONFIDENCE": "NaN"}, "TENANT_CODE"),
            ({"EDUGRADE_OCR_WORKER_USERNAME": "", "EDUGRADE_OCR_WORKER_PASSWORD": ""}, "USERNAME"),
            ({"EDUGRADE_OCR_PREPROCESS_PROFILE": " ", "EDUGRADE_OCR_MIN_CONFIDENCE": "NaN"}, "PREPROCESS_PROFILE"),
            ({"EDUGRADE_OCR_MIN_CONFIDENCE": "NaN", "EDUGRADE_OCR_POLL_INTERVAL": "-1"}, "MIN_CONFIDENCE"),
            ({"EDUGRADE_OCR_POLL_INTERVAL": "-1", "EDUGRADE_OCR_BATCH_SIZE": "2"}, "POLL_INTERVAL"),
            ({"EDUGRADE_OCR_BATCH_SIZE": "2", "EDUGRADE_FORMULA_RECOGNITION_BATCH_SIZE": "0"}, "OCR_BATCH_SIZE"),
            ({"EDUGRADE_FORMULA_RECOGNITION_BATCH_SIZE": "0", "EDUGRADE_FORMULA_RESULT_CACHE_SIZE": "-1"}, "FORMULA_RECOGNITION_BATCH_SIZE"),
            ({"EDUGRADE_FORMULA_MAX_PADDING_HEIGHT_RATIO": "inf", "EDUGRADE_OCR_LEASE_SECONDS": "1"}, "MAX_PADDING_HEIGHT_RATIO"),
            ({"EDUGRADE_FORMULA_DEPLOYMENT_PROFILE_PATH": " ", "EDUGRADE_OCR_LEASE_SECONDS": "1"}, "DEPLOYMENT_PROFILE_PATH"),
            ({"EDUGRADE_OCR_LEASE_SECONDS": "1", "EDUGRADE_OCR_RUNTIME_ENABLED": "false"}, "lease_seconds"),
            ({"EDUGRADE_OCR_RUNTIME_ENABLED": "false"}, "at least one"),
        ]
        for overrides, first_error in cases:
            with self.subTest(first_error=first_error), patch.dict(os.environ, {**BASE_ENV, **overrides}, clear=True):
                with self.assertRaisesRegex(ValueError, first_error):
                    load_settings()

    def test_math_validation_precedes_governed_paper_model_pair(self):
        math_env = {
            **BASE_ENV,
            "EDUGRADE_MATH_RUNTIME_ENABLED": "true",
            "EDUGRADE_PAPER_FORMULA_RUNTIME_ENABLED": "true",
        }
        cases = [
            ({"EDUGRADE_MATH_VERIFY_BASE_URL": "invalid", "EDUGRADE_FORMULA_MODEL_VERSION": "unknown"}, "MATH_VERIFY_BASE_URL"),
            ({"EDUGRADE_FORMULA_MODEL_VERSION": "unknown"}, "MATH_VERIFY_TOKEN"),
            ({"EDUGRADE_MATH_VERIFY_TOKEN": "t" * 32, "EDUGRADE_FORMULA_MODEL_VERSION": "unknown"}, "FORMULA_MODEL_VERSION"),
            ({"EDUGRADE_MATH_VERIFY_TOKEN": "t" * 32, "EDUGRADE_FORMULA_MODEL_VERSION": "PP-FormulaNet_plus-L"}, "governed plus-M then plus-L"),
        ]
        for overrides, first_error in cases:
            with self.subTest(first_error=first_error), patch.dict(os.environ, {**math_env, **overrides}, clear=True):
                with self.assertRaisesRegex(ValueError, first_error):
                    load_settings()

    def test_numeric_bounds_and_non_finite_values_keep_original_contract(self):
        bounded_fields = {
            "EDUGRADE_OCR_MIN_CONFIDENCE": ("0", "1"),
            "EDUGRADE_OCR_CPU_THREADS": ("1", "64"),
            "EDUGRADE_OCR_TEXT_DET_LIMIT_SIDE_LEN": ("32", "4096"),
            "EDUGRADE_OCR_TEXT_RECOGNITION_BATCH_SIZE": ("1", "64"),
            "EDUGRADE_FORMULA_RECOGNITION_BATCH_SIZE": ("1", "16"),
            "EDUGRADE_FORMULA_RESULT_CACHE_SIZE": ("0", "100000"),
            "EDUGRADE_FORMULA_MAX_PADDING_PIXELS": ("12", "512"),
            "EDUGRADE_FORMULA_MAX_PADDING_HEIGHT_RATIO": ("0.25", "2"),
            "EDUGRADE_FORMULA_RENDER_SIMILARITY_THRESHOLD": ("0", "1"),
        }
        for boundary in (0, 1):
            overrides = {name: limits[boundary] for name, limits in bounded_fields.items()}
            with self.subTest(boundary=boundary), patch.dict(os.environ, {**BASE_ENV, **overrides}, clear=True):
                settings = load_settings()
                self.assertEqual(settings.formula_recognition_batch_size, (1, 16)[boundary])
                self.assertEqual(settings.formula_result_cache_size, (0, 100000)[boundary])
        for name in (
            "EDUGRADE_OCR_MIN_CONFIDENCE",
            "EDUGRADE_OCR_POLL_INTERVAL",
            "EDUGRADE_FORMULA_MAX_PADDING_HEIGHT_RATIO",
            "EDUGRADE_FORMULA_RENDER_SIMILARITY_THRESHOLD",
        ):
            for value in ("NaN", "inf", "-inf"):
                with self.subTest(name=name, value=value), patch.dict(os.environ, {**BASE_ENV, name: value}, clear=True):
                    with self.assertRaisesRegex(ValueError, name):
                        load_settings()


class FormulaProfileBatch36Tests(unittest.TestCase):
    def setUp(self):
        self.settings = worker_settings()
        self.hardware = {"device": "cpu", "cpu_model": "fixture"}
        self.software = {"python": "fixture", "primary_model": "PP-FormulaNet_plus-M"}
        self.payload = {
            "schema_version": profile.FORMULA_PROFILE_SCHEMA,
            "hardware_fingerprint": profile.descriptor_fingerprint(self.hardware),
            "software": self.software,
            "evidence": {"output_consistency_passed": True},
            "recommendation": {"lifecycle": "resident", "batch_size": 4},
        }
        stack = ExitStack()
        self.addCleanup(stack.close)
        stack.enter_context(patch.object(profile, "formula_hardware_descriptor", return_value=self.hardware))
        stack.enter_context(patch.object(profile, "formula_software_descriptor", return_value=self.software))

    def test_file_profile_keeps_original_byte_digest(self):
        raw = json.dumps(self.payload, indent=2).encode()
        with patch.object(profile.Path, "read_bytes", return_value=raw) as read_bytes:
            plan = profile.resolve_formula_runtime_plan(self.settings)
        read_bytes.assert_called_once_with()
        self.assertEqual((plan.lifecycle, plan.batch_size, plan.source), ("resident", 4, "measured_profile"))
        self.assertEqual(plan.reason, "compatible_measured_profile")
        self.assertEqual(plan.profile_sha256, hashlib.sha256(raw).hexdigest())
        self.assertEqual(plan.hardware_fingerprint, self.payload["hardware_fingerprint"])

    def test_supplied_profile_keeps_canonical_digest_without_file_read(self):
        canonical = json.dumps(self.payload, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()
        with patch.object(profile.Path, "read_bytes") as read_bytes:
            plan = profile.resolve_formula_runtime_plan(self.settings, self.payload)
        read_bytes.assert_not_called()
        self.assertEqual((plan.lifecycle, plan.batch_size, plan.source), ("resident", 4, "measured_profile"))
        self.assertEqual(plan.profile_sha256, hashlib.sha256(canonical).hexdigest())

    def test_two_explicit_overrides_skip_file_read(self):
        settings = replace(self.settings, formula_model_lifecycle="per_job", formula_recognition_batch_size=8)
        with patch.object(profile.Path, "read_bytes") as read_bytes:
            plan = profile.resolve_formula_runtime_plan(settings)
        read_bytes.assert_not_called()
        self.assertEqual((plan.lifecycle, plan.batch_size, plan.source), ("per_job", 8, "explicit"))
        self.assertEqual(plan.reason, "lifecycle_and_batch_explicit")
        self.assertEqual(plan.profile_sha256, "")

    def test_one_explicit_override_still_reads_measured_recommendation(self):
        raw = json.dumps(self.payload).encode()
        cases = [
            (replace(self.settings, formula_model_lifecycle="per_job"), ("per_job", 4)),
            (replace(self.settings, formula_recognition_batch_size=8), ("resident", 8)),
        ]
        for settings, expected in cases:
            with self.subTest(expected=expected), patch.object(profile.Path, "read_bytes", return_value=raw) as read_bytes:
                plan = profile.resolve_formula_runtime_plan(settings)
            read_bytes.assert_called_once_with()
            self.assertEqual((plan.lifecycle, plan.batch_size), expected)
            self.assertEqual(plan.source, "measured_profile")
            self.assertEqual(plan.profile_sha256, hashlib.sha256(raw).hexdigest())

    def test_unreadable_or_invalid_file_profiles_keep_safe_fallback(self):
        cases = [
            (FileNotFoundError("missing"), b"", "profile_missing_or_unreadable"),
            (TypeError("unreadable"), b"", "profile_missing_or_unreadable"),
            (None, b"{broken", "profile_missing_or_unreadable"),
            (None, b"null", "profile_missing_or_unreadable"),
            (None, b"[]", "profile_schema_mismatch"),
        ]
        for error, raw, reason in cases:
            with self.subTest(reason=reason, raw=raw), patch.object(profile.Path, "read_bytes", side_effect=error, return_value=raw):
                plan = profile.resolve_formula_runtime_plan(self.settings)
            self.assertEqual((plan.lifecycle, plan.batch_size, plan.source), ("per_job", 1, "compatibility_default"))
            self.assertEqual(plan.reason, reason)
            self.assertEqual(plan.profile_sha256, "")

    def test_incompatible_profiles_keep_rejection_reason_and_hide_digest(self):
        cases = [
            ("schema_version", "unknown", "profile_schema_mismatch"),
            ("hardware_fingerprint", "other-hardware", "profile_hardware_mismatch"),
            ("software", {"python": "other-version"}, "profile_software_mismatch"),
            ("evidence", {"output_consistency_passed": False}, "profile_evidence_incomplete"),
            ("recommendation", None, "profile_recommendation_missing"),
            ("recommendation", {"lifecycle": "unknown", "batch_size": 4}, "profile_lifecycle_invalid"),
            ("recommendation", {"lifecycle": "resident", "batch_size": True}, "profile_batch_size_invalid"),
            ("recommendation", {"lifecycle": "resident", "batch_size": 0}, "profile_batch_size_invalid"),
            ("recommendation", {"lifecycle": "resident", "batch_size": 17}, "profile_batch_size_invalid"),
        ]
        for field, value, reason in cases:
            payload = deepcopy(self.payload)
            payload[field] = value
            with self.subTest(reason=reason, value=value), patch.object(profile.Path, "read_bytes") as read_bytes:
                plan = profile.resolve_formula_runtime_plan(self.settings, payload)
            read_bytes.assert_not_called()
            self.assertEqual((plan.lifecycle, plan.batch_size, plan.source), ("per_job", 1, "compatibility_default"))
            self.assertEqual(plan.reason, reason)
            self.assertEqual(plan.profile_sha256, "")

    def test_supplied_profile_is_still_validated_with_two_explicit_overrides(self):
        settings = replace(self.settings, formula_model_lifecycle="per_job", formula_recognition_batch_size=8)
        canonical = json.dumps(self.payload, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()
        with patch.object(profile.Path, "read_bytes") as read_bytes:
            plan = profile.resolve_formula_runtime_plan(settings, self.payload)
        read_bytes.assert_not_called()
        self.assertEqual((plan.lifecycle, plan.batch_size, plan.source), ("per_job", 8, "explicit"))
        self.assertEqual(plan.profile_sha256, hashlib.sha256(canonical).hexdigest())

    def test_unserializable_supplied_payload_still_propagates_type_error(self):
        with patch.object(profile.Path, "read_bytes") as read_bytes, self.assertRaises(TypeError):
            profile.resolve_formula_runtime_plan(self.settings, {"unserializable": object()})
        read_bytes.assert_not_called()


class WorkerMainBatch36Tests(unittest.TestCase):
    def setUp(self):
        # 所有模型和运行器都使用替身；只验证正式 main 的启动、退避和退出边界。
        self.stack = ExitStack()
        self.addCleanup(self.stack.close)
        self.settings = worker_settings()
        self.events = []
        self.models = []
        self.detectors = []
        self.formula_init_error = None
        self.detector_init_error = None
        self.log = Mock()
        self.engine = Mock(model_version=self.settings.model_version, device="cpu")
        self.engine.initialize.side_effect = lambda: self.events.append("ocr:ready")
        self.client = Mock(token=None)
        self.client.login.side_effect = self._login
        self.ocr = Mock()
        self.math = Mock()
        self.symbolic = Mock()
        self.paper = Mock()
        self.ocr.process_once.side_effect = self._processing("ocr:process", [KeyboardInterrupt()])
        self.math.process_once.side_effect = self._processing("math:process", [0])
        self.symbolic.process_once.side_effect = self._processing("symbolic:process", [0])
        self.paper.process_once.side_effect = self._processing("paper:process", [0])
        self.plan = profile.FormulaRuntimePlan("per_job", 4, "measured_profile", "compatible_measured_profile", "fixture", "digest")
        self._patch("load_settings", side_effect=lambda: self.settings)
        self._patch("PaddleOCREngine", side_effect=lambda **_: self._record("ocr:constructed", self.engine))
        self._patch("EduGradeClient", side_effect=lambda **_: self._record("client:constructed", self.client))
        self.ocr_factory = self._patch("OCRRunner", side_effect=lambda **_: self._record("ocr-runner:constructed", self.ocr))
        self.math_factory = self._patch("MathUnderstandingRunner", side_effect=lambda **_: self._record("math-runner:constructed", self.math))
        self.symbolic_factory = self._patch("SymbolicVerificationRunner", side_effect=lambda **_: self._record("symbolic-runner:constructed", self.symbolic))
        self.paper_factory = self._patch("PaperFormulaRunner", side_effect=lambda **_: self._record("paper-runner:constructed", self.paper))
        self.formula_factory = self._patch("PaddleFormulaNetEngine", side_effect=self._new_formula_model)
        self.detector_factory = self._patch("PaddleFormulaLayoutDetector", side_effect=self._new_detector)
        self.router_factory = self._patch("RecognitionRouter", return_value=Mock())
        self.verifier_factory = self._patch("MathVerificationClient", side_effect=[Mock(), Mock()])
        self.validator_factory = self._patch("FormulaValidator", return_value=Mock())
        self._patch("resolve_formula_plan", side_effect=lambda _: self._record("plan:resolved", self.plan))
        self.stack.enter_context(patch.object(worker_main.logging, "getLogger", return_value=self.log))
        self.stack.enter_context(patch.object(worker_main.logging, "basicConfig"))
        self.sleep = self.stack.enter_context(patch.object(worker_main.time, "sleep", side_effect=KeyboardInterrupt))

    def _patch(self, name, **kwargs):
        return self.stack.enter_context(patch.object(worker_main, name, **kwargs))

    def _record(self, event, result):
        self.events.append(event)
        return result

    def _login(self):
        self.events.append("login")
        self.client.token = "authenticated"

    def _processing(self, event, outcomes):
        steps = iter(outcomes)

        def process():
            self.events.append(event)
            outcome = next(steps)
            if isinstance(outcome, BaseException):
                raise outcome
            return outcome

        return process

    def _new_formula_model(self, **kwargs):
        label = f"formula-{len(self.models)}"
        model = Mock(model_version=kwargs["model_version"])

        def initialize():
            self.events.append(f"{label}:ready")
            if self.formula_init_error is not None:
                raise self.formula_init_error

        model.initialize.side_effect = initialize
        self.models.append(model)
        return self._record(f"{label}:constructed", model)

    def _new_detector(self, *_):
        label = f"detector-{len(self.detectors)}"
        detector = Mock()

        def initialize():
            self.events.append(f"{label}:ready")
            if self.detector_init_error is not None:
                raise self.detector_init_error

        detector.initialize.side_effect = initialize
        self.detectors.append(detector)
        return self._record(f"{label}:constructed", detector)

    def _enable_math(self):
        self.settings = replace(self.settings, math_runtime_enabled=True, math_verify_token="t" * 32)

    def _enable_paper(self, lifecycle="per_job", prewarm=False):
        self.settings = replace(self.settings, paper_formula_runtime_enabled=True, formula_prewarm_fallback=prewarm)
        self.plan = replace(self.plan, lifecycle=lifecycle)

    def test_all_models_and_runners_are_ready_before_authentication(self):
        self._enable_math()
        self._enable_paper(lifecycle="resident", prewarm=True)
        with self.assertRaises(KeyboardInterrupt):
            worker_main.main()
        for event in ("ocr:ready", "formula-0:ready", "detector-0:ready", "detector-1:ready", "formula-1:ready", "formula-2:ready"):
            self.assertLess(self.events.index(event), self.events.index("login"))
        self.assertLess(self.events.index("ocr:ready"), self.events.index("client:constructed"))
        self.assertLess(self.events.index("ocr-runner:constructed"), self.events.index("math-runner:constructed"))
        self.assertLess(self.events.index("symbolic-runner:constructed"), self.events.index("paper-runner:constructed"))
        self.assertLess(self.events.index("formula-2:constructed"), self.events.index("plan:resolved"))
        self.assertLess(self.events.index("detector-1:ready"), self.events.index("formula-1:ready"))
        self.assertLess(self.events.index("formula-1:ready"), self.events.index("formula-2:ready"))

    def test_math_only_keeps_text_initialization_and_two_verifiers(self):
        self._enable_math()
        self.settings = replace(self.settings, ocr_runtime_enabled=False)
        self.symbolic.process_once.side_effect = self._processing("symbolic:process", [KeyboardInterrupt()])
        with self.assertRaises(KeyboardInterrupt):
            worker_main.main()
        self.engine.initialize.assert_called_once_with()
        self.ocr_factory.assert_not_called()
        self.assertEqual(self.events[-2:], ["math:process", "symbolic:process"])
        router_args = self.router_factory.call_args.kwargs
        self.assertIs(router_args["text_engine"], self.engine)
        self.assertEqual(router_args["formula_batch_size"], 4)
        self.assertEqual(self.verifier_factory.call_count, 2)
        self.assertIsNot(self.math_factory.call_args.kwargs["verifier"], self.symbolic_factory.call_args.kwargs["verifier"])

    def test_formula_initialization_failure_keeps_layout_and_symbolic_runner(self):
        self._enable_math()
        self.settings = replace(self.settings, ocr_runtime_enabled=False)
        self.formula_init_error = RuntimeError("formula unavailable")
        self.math.process_once.side_effect = self._processing("math:process", [KeyboardInterrupt()])
        with self.assertRaises(KeyboardInterrupt):
            worker_main.main()
        router_args = self.router_factory.call_args.kwargs
        self.assertIsNone(router_args["formula_engine"])
        self.assertIs(router_args["formula_detector"], self.detectors[0])
        self.detectors[0].initialize.assert_called_once_with()
        self.symbolic_factory.assert_called_once()
        self.log.exception.assert_called_once_with("formula engine unavailable; mathematical formula regions will require human review")

    def test_layout_initialization_failure_keeps_formula_and_symbolic_runner(self):
        self._enable_math()
        self.settings = replace(self.settings, ocr_runtime_enabled=False)
        self.detector_init_error = RuntimeError("layout unavailable")
        self.math.process_once.side_effect = self._processing("math:process", [KeyboardInterrupt()])
        with self.assertRaises(KeyboardInterrupt):
            worker_main.main()
        router_args = self.router_factory.call_args.kwargs
        self.assertIs(router_args["formula_engine"], self.models[0])
        self.assertIsNone(router_args["formula_detector"])
        self.symbolic_factory.assert_called_once()
        self.log.exception.assert_called_once_with("formula layout detector unavailable; mixed mathematical regions will require human review")

    def test_formula_constructor_failure_still_attempts_layout(self):
        self._enable_math()
        self.settings = replace(self.settings, ocr_runtime_enabled=False)
        self.formula_factory.side_effect = RuntimeError("formula construction failed")
        self.math.process_once.side_effect = self._processing("math:process", [KeyboardInterrupt()])
        with self.assertRaises(KeyboardInterrupt):
            worker_main.main()
        self.detector_factory.assert_called_once()
        self.assertIsNone(self.router_factory.call_args.kwargs["formula_engine"])
        self.assertIs(self.router_factory.call_args.kwargs["formula_detector"], self.detectors[0])

    def test_layout_constructor_failure_keeps_initialized_formula(self):
        self._enable_math()
        self.settings = replace(self.settings, ocr_runtime_enabled=False)
        self.detector_factory.side_effect = RuntimeError("layout construction failed")
        self.math.process_once.side_effect = self._processing("math:process", [KeyboardInterrupt()])
        with self.assertRaises(KeyboardInterrupt):
            worker_main.main()
        self.models[0].initialize.assert_called_once_with()
        self.assertIs(self.router_factory.call_args.kwargs["formula_engine"], self.models[0])
        self.assertIsNone(self.router_factory.call_args.kwargs["formula_detector"])

    def test_per_job_paper_waits_until_work_then_drains(self):
        self._enable_paper()
        self.settings = replace(self.settings, ocr_runtime_enabled=False)
        self.paper.process_once.side_effect = self._processing("paper:process", [0, 2, 0])
        self.sleep.side_effect = None
        worker_main.main()
        self.engine.initialize.assert_not_called()
        for model in self.models + self.detectors:
            model.initialize.assert_not_called()
        self.client.login.assert_called_once_with()
        self.assertEqual(self.paper.process_once.call_count, 3)
        self.sleep.assert_called_once_with(self.settings.poll_interval)
        args = self.paper_factory.call_args.kwargs
        self.assertEqual(args["batch_size"], 4)
        self.assertEqual(args["runtime_plan"], {"runtime_mode": "per_job", "runtime_plan_source": "measured_profile", "runtime_batch_size": 4})
        self.assertEqual(args["max_padding"], 96)
        self.assertEqual(args["max_padding_height_ratio"], 0.75)
        self.validator_factory.assert_called_once_with(render_similarity_threshold=0.34)

    def test_resident_paper_prewarm_happens_before_login_and_idle_continues(self):
        self._enable_paper(lifecycle="resident", prewarm=True)
        self.settings = replace(self.settings, ocr_runtime_enabled=False)
        with self.assertRaises(KeyboardInterrupt):
            worker_main.main()
        self.engine.initialize.assert_not_called()
        for event in ("detector-0:ready", "formula-0:ready", "formula-1:ready"):
            self.assertLess(self.events.index(event), self.events.index("login"))
        self.assertEqual(self.paper.process_once.call_count, 1)
        self.sleep.assert_called_once_with(self.settings.poll_interval)

    def test_resident_paper_does_not_prewarm_fallback_by_default(self):
        self._enable_paper(lifecycle="resident")
        self.settings = replace(self.settings, ocr_runtime_enabled=False)
        with self.assertRaises(KeyboardInterrupt):
            worker_main.main()
        self.detectors[0].initialize.assert_called_once_with()
        self.models[0].initialize.assert_called_once_with()
        self.models[1].initialize.assert_not_called()

    def test_paper_initialization_failure_stops_before_login(self):
        self._enable_paper(lifecycle="resident")
        self.settings = replace(self.settings, ocr_runtime_enabled=False)
        self.detector_init_error = RuntimeError("paper detector unavailable")
        with self.assertRaisesRegex(RuntimeError, "paper detector unavailable"):
            worker_main.main()
        self.client.login.assert_not_called()
        self.paper_factory.assert_not_called()
        self.sleep.assert_not_called()

    def test_four_queues_keep_poll_order_and_paper_drain_exit_with_other_work(self):
        self._enable_math()
        self._enable_paper()
        self.ocr.process_once.side_effect = self._processing("ocr:process", [1, 1])
        self.math.process_once.side_effect = self._processing("math:process", [0, 0])
        self.symbolic.process_once.side_effect = self._processing("symbolic:process", [0, 0])
        self.paper.process_once.side_effect = self._processing("paper:process", [1, 0])
        worker_main.main()
        calls = [event for event in self.events if event.endswith(":process")]
        self.assertEqual(calls, ["ocr:process", "math:process", "symbolic:process", "paper:process"] * 2)
        self.sleep.assert_not_called()

    def test_authentication_error_clears_token_before_next_login(self):
        self.client.token = "expired"
        self.ocr.process_once.side_effect = self._processing("ocr:process", [worker_main.AuthenticationError("expired"), KeyboardInterrupt()])
        self.sleep.side_effect = None
        observed_tokens = []

        def login():
            observed_tokens.append(self.client.token)
            self._login()

        self.client.login.side_effect = login
        with self.assertRaises(KeyboardInterrupt):
            worker_main.main()
        self.assertEqual(observed_tokens, [None])
        self.assertEqual(self.client.token, "authenticated")
        self.sleep.assert_called_once_with(self.settings.poll_interval)
        self.log.warning.assert_called_once_with("worker authentication expired; login will be retried")

    def test_api_error_retries_without_resetting_existing_token(self):
        self.client.token = "existing"
        self.ocr.process_once.side_effect = self._processing("ocr:process", [worker_main.APIError("request failed"), KeyboardInterrupt()])
        self.sleep.side_effect = None
        with self.assertRaises(KeyboardInterrupt):
            worker_main.main()
        self.client.login.assert_not_called()
        self.assertEqual(self.client.token, "existing")
        self.sleep.assert_called_once_with(self.settings.poll_interval)
        self.log.warning.assert_called_once_with("worker API request failed; polling will continue", extra={"error_type": "APIError"})

    def test_unexpected_error_retries_and_keyboard_interrupt_still_escapes(self):
        self.ocr.process_once.side_effect = self._processing("ocr:process", [RuntimeError("unexpected"), KeyboardInterrupt()])
        self.sleep.side_effect = None
        with self.assertRaises(KeyboardInterrupt):
            worker_main.main()
        self.client.login.assert_called_once_with()
        self.sleep.assert_called_once_with(self.settings.poll_interval)
        self.log.exception.assert_called_once_with("unexpected OCR worker error; polling will continue")


if __name__ == "__main__":
    unittest.main()

