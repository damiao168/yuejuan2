from __future__ import annotations

from types import SimpleNamespace

import pytest
from ocr_worker.deployment_profile import FORMULA_PROFILE_SCHEMA
from ocr_worker.formula_calibration import calibrate_formula_runtime
from ocr_worker.recognition_router import FormulaRecognitionResult


class FakeDetector:
    def __init__(self) -> None:
        self.initialized = False

    def initialize(self) -> None:
        self.initialized = True


class FakeEngine:
    def __init__(self) -> None:
        self.initialized = False

    def initialize(self) -> None:
        self.initialized = True

    def recognize_formulas(self, images, batch_size=1):
        return [
            FormulaRecognitionResult("x", "x", 1, "fake", "fake-v1", "recognized")
            for _ in images
        ]


def settings():
    return SimpleNamespace(
        device="cpu",
        formula_layout_model_version="layout-v1",
        formula_model_version="primary-v1",
        formula_fallback_model_version="fallback-v1",
    )


def test_calibration_records_evidence_and_never_claims_absolute_accuracy(monkeypatch):
    ticks = iter([0, 1, 2, 4, 5, 6, 7, 7.5])
    events = []
    monkeypatch.setattr("ocr_worker.formula_calibration._rss_bytes", lambda: 123)
    result = calibrate_formula_runtime(
        settings(),
        [b"one", b"two"],
        batch_sizes=[1, 2],
        repeats=1,
        lifecycle_goal="compatibility",
        engine=FakeEngine(),
        detector=FakeDetector(),
        clock=lambda: next(ticks),
        progress=events.append,
    )

    assert result["schema_version"] == FORMULA_PROFILE_SCHEMA
    assert result["evidence"]["output_consistency_passed"] is True
    assert result["evidence"]["absolute_accuracy_evaluated"] is False
    assert result["evidence"]["process_peak_rss_bytes"] == 123
    assert result["recommendation"]["lifecycle"] == "per_job"
    assert result["recommendation"]["batch_size"] == 2
    assert events[0] == {"phase": "model_ready", "completed_runs": 0, "total_runs": 2}
    assert [(event["batch_size"], event["completed_runs"]) for event in events[1:]] == [(1, 1), (2, 2)]


def test_latency_goal_is_explicit_not_inferred_from_capacity():
    ticks = iter([0, 1, 2, 3])
    result = calibrate_formula_runtime(
        settings(),
        [b"one"],
        batch_sizes=[1],
        repeats=1,
        lifecycle_goal="latency",
        engine=FakeEngine(),
        detector=FakeDetector(),
        clock=lambda: next(ticks),
    )

    assert result["recommendation"]["lifecycle"] == "resident"
    assert result["recommendation"]["objective"] == "warm_latency"


@pytest.mark.parametrize("batch_sizes", [[], [0], [17]])
def test_invalid_batch_sweep_is_rejected(batch_sizes):
    test_settings = settings()
    engine = FakeEngine()
    detector = FakeDetector()
    with pytest.raises(ValueError, match='batch sizes'):
        calibrate_formula_runtime(test_settings, [b'one'], batch_sizes=batch_sizes, repeats=1, lifecycle_goal='compatibility', engine=engine, detector=detector)
