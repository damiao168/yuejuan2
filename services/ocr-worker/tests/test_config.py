import os
import unittest
from unittest.mock import patch

from ocr_worker.config import load_settings, resolve_formula_plan
from ocr_worker.deployment_profile import (
    FORMULA_PROFILE_SCHEMA,
    descriptor_fingerprint,
    formula_hardware_descriptor,
    formula_software_descriptor,
    resolve_formula_runtime_plan,
)

BASE_ENV = {
    "EDUGRADE_API_BASE_URL": "http://api-gateway:8080",
    "EDUGRADE_OCR_WORKER_TENANT_CODE": "demo",
    "EDUGRADE_OCR_WORKER_USERNAME": "ocr-worker",
    "EDUGRADE_OCR_WORKER_PASSWORD": "test-only",
}


class ConfigTests(unittest.TestCase):
    @patch.dict(os.environ, BASE_ENV, clear=True)
    def test_reliable_lease_defaults_are_valid(self):
        settings = load_settings()

        self.assertEqual(settings.batch_size, 1)
        self.assertEqual(settings.lease_seconds, 300)
        self.assertEqual(settings.heartbeat_interval, 10)
        self.assertEqual(settings.heartbeat_timeout, 3)
        self.assertTrue(settings.ocr_runtime_enabled)
        self.assertEqual(settings.cpu_threads, 4)
        self.assertEqual(settings.enable_mkldnn, "auto")
        self.assertTrue(settings.use_textline_orientation)
        self.assertEqual(settings.text_det_limit_side_len, 64)
        self.assertEqual(settings.text_recognition_batch_size, 1)
        self.assertIsNone(settings.formula_recognition_batch_size)
        self.assertEqual(settings.formula_result_cache_size, 2048)
        self.assertEqual(settings.formula_max_padding_height_ratio, 0.75)
        self.assertEqual(settings.formula_model_lifecycle, "auto")
        self.assertFalse(settings.formula_prewarm_fallback)
        self.assertEqual(settings.math_verify_base_url, "https://math-verification-worker:8092")
        plan = resolve_formula_plan(settings)
        self.assertEqual((plan.lifecycle, plan.batch_size), ("per_job", 1))
        self.assertEqual(plan.source, "compatibility_default")

    @patch.dict(
        os.environ,
        {
            **BASE_ENV,
            "EDUGRADE_ENV": "production",
            "EDUGRADE_API_BASE_URL": "https://api-gateway:8443",
            "EDUGRADE_MATH_RUNTIME_ENABLED": "true",
            "EDUGRADE_MATH_VERIFY_TOKEN": "synthetic-math-token-" + "x" * 32,
        },
        clear=True,
    )
    def test_math_runtime_default_verifier_url_is_https_in_production(self):
        settings = load_settings()

        self.assertTrue(settings.math_runtime_enabled)
        self.assertEqual(settings.math_verify_base_url, "https://math-verification-worker:8092")

    @patch.dict(
        os.environ,
        {
            **BASE_ENV,
            "EDUGRADE_ENV": "test",
            "EDUGRADE_MATH_RUNTIME_ENABLED": "true",
            "EDUGRADE_MATH_VERIFY_BASE_URL": "http://127.0.0.1:8092",
            "EDUGRADE_MATH_VERIFY_TOKEN": "synthetic-math-token-" + "x" * 32,
        },
        clear=True,
    )
    def test_explicit_development_verifier_url_keeps_loopback_override(self):
        settings = load_settings()

        self.assertEqual(settings.math_verify_base_url, "http://127.0.0.1:8092")

    @patch.dict(
        os.environ,
        {
            **BASE_ENV,
            "EDUGRADE_ENV": "production",
            "EDUGRADE_API_BASE_URL": "https://api-gateway:8443",
            "EDUGRADE_MATH_RUNTIME_ENABLED": "true",
            "EDUGRADE_MATH_VERIFY_BASE_URL": "http://math-verification-worker:8092",
            "EDUGRADE_MATH_VERIFY_TOKEN": "synthetic-math-token-" + "x" * 32,
        },
        clear=True,
    )
    def test_production_verifier_url_rejects_explicit_non_loopback_http(self):
        with self.assertRaisesRegex(ValueError, "MATH_VERIFY_BASE_URL"):
            load_settings()

    @patch.dict(
        os.environ,
        {
            **BASE_ENV,
            "EDUGRADE_FORMULA_MODEL_LIFECYCLE": "resident",
            "EDUGRADE_FORMULA_RECOGNITION_BATCH_SIZE": "8",
            "EDUGRADE_FORMULA_PREWARM_FALLBACK": "yes",
        },
        clear=True,
    )
    def test_formula_model_lifecycle_can_be_overridden(self):
        settings = load_settings()

        self.assertEqual(settings.formula_model_lifecycle, "resident")
        self.assertEqual(settings.formula_recognition_batch_size, 8)
        self.assertTrue(settings.formula_prewarm_fallback)
        plan = resolve_formula_plan(settings)
        self.assertEqual((plan.lifecycle, plan.batch_size, plan.source), ("resident", 8, "explicit"))

    @patch.dict(os.environ, BASE_ENV, clear=True)
    def test_formula_lifecycle_auto_uses_only_compatible_measured_profile(self):
        settings = load_settings()
        hardware = formula_hardware_descriptor(settings.device)
        profile = {
            "schema_version": FORMULA_PROFILE_SCHEMA,
            "hardware_fingerprint": descriptor_fingerprint(hardware),
            "software": formula_software_descriptor(settings),
            "evidence": {"output_consistency_passed": True},
            "recommendation": {"lifecycle": "resident", "batch_size": 4},
        }

        plan = resolve_formula_runtime_plan(settings, profile)
        self.assertEqual((plan.lifecycle, plan.batch_size, plan.source), ("resident", 4, "measured_profile"))
        profile["hardware_fingerprint"] = "different-machine"
        rejected = resolve_formula_runtime_plan(settings, profile)
        self.assertEqual((rejected.lifecycle, rejected.batch_size), ("per_job", 1))
        self.assertEqual(rejected.reason, "profile_hardware_mismatch")

    @patch.dict(os.environ, {**BASE_ENV, "EDUGRADE_FORMULA_MODEL_LIFECYCLE": "sometimes"}, clear=True)
    def test_formula_lifecycle_must_be_supported(self):
        with self.assertRaisesRegex(ValueError, "MODEL_LIFECYCLE"):
            load_settings()

    @patch.dict(os.environ, {**BASE_ENV, "EDUGRADE_OCR_CPU_THREADS": "0"}, clear=True)
    def test_cpu_threads_are_bounded(self):
        with self.assertRaisesRegex(ValueError, "CPU_THREADS"):
            load_settings()

    @patch.dict(os.environ, {**BASE_ENV, "EDUGRADE_OCR_ENABLE_MKLDNN": "sometimes"}, clear=True)
    def test_mkldnn_mode_is_explicit(self):
        with self.assertRaisesRegex(ValueError, "MKLDNN"):
            load_settings()

    @patch.dict(os.environ, {**BASE_ENV, "EDUGRADE_OCR_TEXT_RECOGNITION_BATCH_SIZE": "0"}, clear=True)
    def test_recognition_batch_size_is_positive(self):
        with self.assertRaisesRegex(ValueError, "RECOGNITION_BATCH_SIZE"):
            load_settings()

    @patch.dict(os.environ, {**BASE_ENV, "EDUGRADE_OCR_BATCH_SIZE": "2"}, clear=True)
    def test_batch_size_greater_than_one_fails_before_claim(self):
        with self.assertRaisesRegex(ValueError, "must be 1"):
            load_settings()

    @patch.dict(
        os.environ,
        {
            **BASE_ENV,
            "EDUGRADE_OCR_LEASE_SECONDS": "30",
            "EDUGRADE_OCR_HEARTBEAT_INTERVAL": "28",
            "EDUGRADE_OCR_HEARTBEAT_TIMEOUT": "3",
        },
        clear=True,
    )
    def test_heartbeat_timeout_must_finish_before_lease_expires(self):
        with self.assertRaisesRegex(ValueError, "interval plus timeout"):
            load_settings()

    @patch.dict(os.environ, {**BASE_ENV, "EDUGRADE_OCR_MIN_CONFIDENCE": "NaN"}, clear=True)
    def test_non_finite_confidence_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "MIN_CONFIDENCE"):
            load_settings()

    @patch.dict(os.environ, {**BASE_ENV, "EDUGRADE_OCR_POLL_INTERVAL": "-1"}, clear=True)
    def test_non_positive_poll_interval_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "POLL_INTERVAL"):
            load_settings()

    @patch.dict(os.environ, {**BASE_ENV, "EDUGRADE_FORMULA_MAX_PADDING_HEIGHT_RATIO": "5"}, clear=True)
    def test_formula_padding_height_ratio_is_bounded(self):
        with self.assertRaisesRegex(ValueError, "MAX_PADDING_HEIGHT_RATIO"):
            load_settings()

    @patch.dict(
        os.environ,
        {**BASE_ENV, "EDUGRADE_OCR_RUNTIME_ENABLED": "false", "EDUGRADE_MATH_RUNTIME_ENABLED": "false"},
        clear=True,
    )
    def test_at_least_one_runtime_must_be_enabled(self):
        with self.assertRaisesRegex(ValueError, "at least one"):
            load_settings()


if __name__ == "__main__":
    unittest.main()
