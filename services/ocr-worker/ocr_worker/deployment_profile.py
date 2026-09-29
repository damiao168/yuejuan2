from __future__ import annotations

import hashlib
import importlib.metadata
import json
import os
import platform
from dataclasses import dataclass
from pathlib import Path
from typing import Any

FORMULA_PROFILE_SCHEMA = "edugrade.formula-deployment-profile.v1"
SAFE_FORMULA_BATCH_SIZE = 1


@dataclass(frozen=True)
class FormulaRuntimePlan:
    lifecycle: str
    batch_size: int
    source: str
    reason: str
    hardware_fingerprint: str
    profile_sha256: str = ""


def resolve_formula_runtime_plan(settings: Any, profile_payload: dict[str, Any] | None = None) -> FormulaRuntimePlan:
    """Resolve only from an explicit setting or compatible measured profile.

    Capacity alone is deliberately not a selector. A missing, malformed, or
    hardware-incompatible profile falls back to the lowest-risk runtime plan.
    """

    hardware = formula_hardware_descriptor(str(settings.device))
    fingerprint = descriptor_fingerprint(hardware)
    requested_lifecycle = str(settings.formula_model_lifecycle)
    explicit_batch = settings.formula_recognition_batch_size

    payload = profile_payload
    profile_sha256 = ""
    if payload is None and (requested_lifecycle == "auto" or explicit_batch is None):
        profile_path = Path(str(settings.formula_deployment_profile_path))
        try:
            raw = profile_path.read_bytes()
            payload = json.loads(raw)
            profile_sha256 = hashlib.sha256(raw).hexdigest()
        except (OSError, json.JSONDecodeError, TypeError):
            payload = None

    recommendation, profile_reason = _validated_recommendation(
        payload,
        hardware_fingerprint=fingerprint,
        software=formula_software_descriptor(settings),
    )
    if payload is not None and not profile_sha256:
        profile_sha256 = hashlib.sha256(
            json.dumps(payload, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()
        ).hexdigest()

    if requested_lifecycle == "auto":
        lifecycle = recommendation.get("lifecycle", "per_job") if recommendation else "per_job"
    else:
        lifecycle = requested_lifecycle

    if explicit_batch is not None:
        batch_size = explicit_batch
    elif recommendation:
        batch_size = int(recommendation["batch_size"])
    else:
        batch_size = SAFE_FORMULA_BATCH_SIZE

    if requested_lifecycle != "auto" and explicit_batch is not None:
        source, reason = "explicit", "lifecycle_and_batch_explicit"
    elif recommendation:
        source, reason = "measured_profile", "compatible_measured_profile"
    else:
        source, reason = "compatibility_default", profile_reason
    return FormulaRuntimePlan(
        lifecycle=lifecycle,
        batch_size=batch_size,
        source=source,
        reason=reason,
        hardware_fingerprint=fingerprint,
        profile_sha256=profile_sha256 if recommendation else "",
    )


def _validated_recommendation(
    payload: object,
    *,
    hardware_fingerprint: str,
    software: dict[str, Any],
) -> tuple[dict[str, Any] | None, str]:
    if payload is None:
        return None, "profile_missing_or_unreadable"
    if not isinstance(payload, dict) or payload.get("schema_version") != FORMULA_PROFILE_SCHEMA:
        return None, "profile_schema_mismatch"
    if payload.get("hardware_fingerprint") != hardware_fingerprint:
        return None, "profile_hardware_mismatch"
    # 同一硬件上的模型或运行库版本改变后，也不能沿用旧性能测量。
    if payload.get("software") != software:
        return None, "profile_software_mismatch"
    evidence = payload.get("evidence")
    if not isinstance(evidence, dict) or evidence.get("output_consistency_passed") is not True:
        return None, "profile_evidence_incomplete"
    recommendation = payload.get("recommendation")
    if not isinstance(recommendation, dict):
        return None, "profile_recommendation_missing"
    lifecycle = recommendation.get("lifecycle")
    batch_size = recommendation.get("batch_size")
    if lifecycle not in {"resident", "per_job"}:
        return None, "profile_lifecycle_invalid"
    if not isinstance(batch_size, int) or isinstance(batch_size, bool) or not 1 <= batch_size <= 16:
        return None, "profile_batch_size_invalid"
    return recommendation, "compatible_measured_profile"


def formula_hardware_descriptor(device: str) -> dict[str, Any]:
    return {
        "system": platform.system().lower(),
        "machine": platform.machine().lower(),
        "cpu_model": _cpu_model(),
        "logical_cpu_count": os.cpu_count() or 0,
        "cpu_quota": _cpu_quota(),
        "memory_limit_bytes": _visible_memory_limit_bytes(),
        "device": device.strip().lower(),
    }


def formula_software_descriptor(settings: Any) -> dict[str, Any]:
    return {
        "python": platform.python_version(),
        "paddleocr": _package_version("paddleocr"),
        "paddlepaddle": _package_version("paddlepaddle"),
        "layout_model": str(settings.formula_layout_model_version),
        "primary_model": str(settings.formula_model_version),
        "fallback_model": str(settings.formula_fallback_model_version),
    }


def descriptor_fingerprint(descriptor: dict[str, Any]) -> str:
    canonical = json.dumps(descriptor, ensure_ascii=False, sort_keys=True, separators=(",", ":"))
    return hashlib.sha256(canonical.encode()).hexdigest()


def _package_version(name: str) -> str:
    try:
        return importlib.metadata.version(name)
    except importlib.metadata.PackageNotFoundError:
        return "unavailable"


def _cpu_model() -> str:
    for path in (Path("/proc/cpuinfo"),):
        try:
            for line in path.read_text(encoding="utf-8").splitlines():
                if line.lower().startswith(("model name", "hardware")) and ":" in line:
                    return " ".join(line.split(":", 1)[1].split()).lower()
        except OSError:
            pass
    return " ".join((platform.processor() or "unknown").split()).lower()


def _cpu_quota() -> float | None:
    try:
        quota, period = Path("/sys/fs/cgroup/cpu.max").read_text(encoding="utf-8").split()[:2]
        if quota != "max":
            return round(int(quota) / int(period), 4)
    except (OSError, ValueError, IndexError, ZeroDivisionError):
        pass
    try:
        quota = int(Path("/sys/fs/cgroup/cpu/cpu.cfs_quota_us").read_text(encoding="utf-8"))
        period = int(Path("/sys/fs/cgroup/cpu/cpu.cfs_period_us").read_text(encoding="utf-8"))
        if quota > 0 and period > 0:
            return round(quota / period, 4)
    except (OSError, ValueError, ZeroDivisionError):
        pass
    return None


def _visible_memory_limit_bytes() -> int:
    candidates: list[int] = []
    for path in (Path("/sys/fs/cgroup/memory.max"), Path("/sys/fs/cgroup/memory/memory.limit_in_bytes")):
        try:
            raw = path.read_text(encoding="utf-8").strip()
            if raw and raw != "max":
                value = int(raw)
                if 0 < value < 1 << 60:
                    candidates.append(value)
        except (OSError, ValueError):
            pass
    try:
        for line in Path("/proc/meminfo").read_text(encoding="utf-8").splitlines():
            if line.startswith("MemTotal:"):
                candidates.append(int(line.split()[1]) * 1024)
                break
    except (OSError, ValueError, IndexError):
        pass
    return min(candidates) if candidates else 0
