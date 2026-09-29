from __future__ import annotations

import json
import time
from typing import Any
from urllib import error, request

from edugrade_worker_runtime import ResponseValidationError, read_json_response

from .api import APIError
from .recognition_router import RecognitionRouter, RegionKind
from .runner import WorkerConfig, _LeaseHeartbeat


class MathVerificationClient:
    def __init__(self, base_url: str, token: str, timeout: float = 5.0) -> None:
        self.base_url = base_url.rstrip("/")
        self.token = token
        self.timeout = timeout

    def normalize(self, latex: str) -> dict[str, Any]:
        return self._post("normalize", {"latex": latex, "domain": {"domain": "real"}})

    def verify_transition(self, previous_ast: dict[str, Any], next_ast: dict[str, Any]) -> dict[str, Any]:
        return self._post("verify-transition", {
            "previous_ast": previous_ast, "next_ast": next_ast, "domain": {"domain": "real"},
        })

    def solve(self, ast: dict[str, Any], variable: str) -> dict[str, Any]:
        return self._post("solve", {"ast": ast, "variable": variable, "domain": {"domain": "real"}})

    def _post(self, operation: str, payload: dict[str, Any]) -> dict[str, Any]:
        body = json.dumps(payload).encode()
        req = request.Request(
            f"{self.base_url}/internal/math/{operation}",
            data=body,
            headers={"Content-Type": "application/json", "X-EduGrade-Internal-Token": self.token},
            method="POST",
        )
        try:
            with request.urlopen(req, timeout=self.timeout) as response:
                return json.loads(read_json_response(response).decode())
        except error.HTTPError as exc:
            if exc.code == 422:
                return {"status": "uncertain", "reason_code": "unsupported_expression"}
            raise APIError("math verification request failed") from exc
        except (OSError, ResponseValidationError) as exc:
            raise APIError("math verification unavailable") from exc


class MathUnderstandingRunner:
    """Consumes the existing Worker Runtime; it does not create a second task system."""

    def __init__(
        self,
        *,
        api: Any,
        router: RecognitionRouter,
        verifier: MathVerificationClient,
        config: WorkerConfig,
    ) -> None:
        self.api = api
        self.router = router
        self.verifier = verifier
        self.config = config

    def process_once(self) -> int:
        tasks = self.api.claim_math_tasks(self.config.worker_id, self.config.lease_seconds)
        for task in tasks:
            self._process(task)
        return len(tasks)

    def _process(self, runtime_task: dict[str, Any]) -> None:
        started = time.monotonic()
        runtime_id = str(runtime_task.get("id", ""))
        lease = str(runtime_task.get("lease_token", ""))
        tenant_id = str(runtime_task.get("tenant_id", ""))
        self.api.activate_task(runtime_task, self.config.worker_id)
        if runtime_task.get("source_type") != "answer_segment" or not runtime_id or not lease:
            self.api.fail_runtime_task(runtime_id, lease, "invalid_math_runtime_payload", False, 0, tenant_id)
            return
        with _LeaseHeartbeat(
            api=self.api,
            runtime_task_id=runtime_id,
            lease_token=lease,
            worker_id=self.config.worker_id,
            interval=self.config.heartbeat_interval,
            lease_seconds=self.config.lease_seconds,
            request_timeout=self.config.heartbeat_timeout,
            tenant_id=tenant_id,
        ) as heartbeat:
            try:
                task_input = self.api.get_math_task_input(runtime_id, tenant_id)
                task = task_input["task"]
                subject = str(task["subject_code"])
                if subject not in {"mathematics", "physics", "chemistry"}:
                    raise ValueError("formula recognition is not enabled for this subject")
                image = self.api.download(str(task_input["image_url"]), tenant_id)
                kind = RegionKind(str(task.get("region_kind", "unknown")))
                routed = self.router.recognize(subject, kind, image)
                artifact = _build_artifact(task, routed, self.verifier)
                heartbeat.stop()
                self.api.complete_math_task(
                    runtime_id,
                    lease,
                    artifact,
                    int((time.monotonic() - started) * 1000),
                    tenant_id,
                )
            except (APIError, KeyError, TypeError, ValueError):
                heartbeat.stop()
                self.api.fail_runtime_task(
                    runtime_id,
                    lease,
                    "math_understanding_failed",
                    True,
                    int((time.monotonic() - started) * 1000),
                    tenant_id,
                )


def _build_artifact(task: dict[str, Any], routed: Any, verifier: MathVerificationClient) -> dict[str, Any]:
    segment_id = str(task["answer_segment_id"])
    input_hash = str(task["input_hash"])
    full_box = {"x": 0.0, "y": 0.0, "width": 1.0, "height": 1.0}
    blocks: list[dict[str, Any]] = []
    formulas: list[dict[str, Any]] = []
    verifications: list[dict[str, Any]] = []
    image_size = routed.image_size if getattr(routed, "image_size", None) else None

    if routed.kind in {RegionKind.TEXT, RegionKind.MIXED}:
        for index, text_block in enumerate(routed.text_blocks, start=1):
            block_id = f"block-text-{index}" if routed.kind is RegionKind.MIXED else f"block-{index}"
            blocks.append(
                {
                    "id": block_id,
                    "kind": "text",
                    "status": "active",
                    "bbox": _box(text_block.bbox, image_size),
                    "text": text_block.text,
                    "normalized": " ".join(text_block.text.split()),
                    "recognition_engine": "paddleocr",
                    "recognition_version": "pp-ocrv5",
                    "recognition_confidence": _confidence(text_block.confidence),
                    "structure_confidence": _confidence(text_block.confidence),
                    "source_image_hash": input_hash,
                    "source_refs": ["mixed-layout:text"] if routed.kind is RegionKind.MIXED else [],
                }
            )
    if routed.kind is RegionKind.MIXED:
        for index, region in enumerate(routed.formula_regions, start=1):
            formula = region.formula
            block_id = f"block-formula-{index}"
            bbox = _box(region.bbox, image_size)
            recognition_confidence = _confidence(formula.confidence if formula else 0.0)
            structure_confidence = _confidence(region.detector_confidence)
            blocks.append(
                {
                    "id": block_id,
                    "kind": "formula" if formula else "unknown",
                    "status": "active" if formula else "uncertain",
                    "bbox": bbox,
                    "normalized": formula.canonical_latex if formula else "",
                    "recognition_engine": formula.engine if formula else "formula-layout",
                    "recognition_version": formula.engine_version if formula else (routed.layout_version or "layout-v1"),
                    "recognition_confidence": recognition_confidence,
                    "structure_confidence": structure_confidence,
                    "source_image_hash": input_hash,
                    "source_refs": ["mixed-layout:formula", *region.warnings],
                }
            )
            if formula:
                _append_formula_artifact(
                    formulas,
                    verifications,
                    verifier,
                    formula=formula,
                    block_id=block_id,
                    bbox=bbox,
                    warnings=region.warnings,
                    candidates=region.candidates,
                    selected_candidate=region.selected_candidate,
                )
    elif routed.kind is not RegionKind.TEXT:
        formula = routed.formula
        confidence = _confidence(formula.confidence if formula else 0.0)
        blocks.append(
            {
                "id": "block-1",
                "kind": "formula" if formula else "unknown",
                "status": "active",
                "bbox": full_box,
                "normalized": formula.canonical_latex if formula else "",
                "recognition_engine": formula.engine if formula else "formula-router",
                "recognition_version": formula.engine_version if formula else "v1",
                "recognition_confidence": confidence,
                "structure_confidence": confidence,
                "source_image_hash": input_hash,
            }
        )
        if formula:
            _append_formula_artifact(
                formulas,
                verifications,
                verifier,
                formula=formula,
                block_id="block-1",
                bbox=full_box,
                warnings=formula.warnings,
                candidates=(formula,),
                selected_candidate=0,
            )

    if not blocks:
        raise ValueError("recognition produced no evidence")
    blocks.sort(key=lambda block: (block["bbox"]["y"], block["bbox"]["x"], block["id"]))
    step_formula_ids = [formula["id"] for formula in formulas]
    overall = min(block["recognition_confidence"] for block in blocks)
    return {
        "subject_code": str(task["subject_code"]),
        "answer_segment_id": segment_id,
        "exam_question_snapshot_id": str(task["exam_question_snapshot_id"]),
        "input_hash": input_hash,
        "engine_version": "math-runtime-v2" if routed.kind is RegionKind.MIXED else "math-runtime-v1",
        "blocks": blocks,
        "formulas": formulas,
        "relations": [],
        "solution_graph": {
            "id": f"solution-{segment_id}",
            "answer_segment_id": segment_id,
            "builder_version": "math-runtime-v2" if routed.kind is RegionKind.MIXED else "math-runtime-v1",
            "formula_model_version": formulas[0]["recognition_version"] if formulas else (routed.layout_version or "text-only"),
            "overall_confidence": overall,
            "requires_human_review": routed.requires_human_review or overall < 0.75,
            "steps": [
                {
                    "id": "step-1",
                    "order_hint": 1,
                    "block_ids": [block["id"] for block in blocks],
                    "formula_ids": step_formula_ids,
                    "normalized_text": " ".join(str(block.get("normalized", "")) for block in blocks).strip(),
                    "confidence": overall,
                }
            ],
            "edges": [],
        },
        "verifications": verifications,
        "rubric_evidence": [],
    }


def _append_formula_artifact(
    formulas: list[dict[str, Any]],
    verifications: list[dict[str, Any]],
    verifier: MathVerificationClient,
    *,
    formula: Any,
    block_id: str,
    bbox: dict[str, float],
    warnings: tuple[str, ...],
    candidates: tuple[Any, ...],
    selected_candidate: int | None,
) -> None:
    try:
        normalized = verifier.normalize(formula.canonical_latex) if formula.canonical_latex else {}
    except APIError:
        # Recognition evidence remains useful while the symbolic service is down.
        normalized = {"status": "uncertain", "reason_code": "verification_unavailable"}
    ast = normalized.get("ast") if isinstance(normalized.get("ast"), dict) else None
    parsed = ast is not None
    formula_id = f"formula-{len(formulas) + 1}"
    confidence = _confidence(formula.confidence)
    formulas.append(
        {
            "id": formula_id,
            "block_id": block_id,
            "bbox": bbox,
            "raw_latex": formula.raw_latex,
            "canonical_latex": formula.canonical_latex,
            "recognition_engine": formula.engine,
            "recognition_version": formula.engine_version,
            "parser_version": "restricted-v1",
            "parse_status": "parsed" if parsed else "unsupported",
            "confidence": confidence,
            "ast": ast,
            "warnings": list(dict.fromkeys([*formula.warnings, *warnings])),
            "candidates": [
                {
                    "latex": candidate.canonical_latex or candidate.raw_latex,
                    "engine": candidate.engine,
                    "version": candidate.engine_version,
                    "confidence": _confidence(candidate.confidence),
                    "syntax_valid": bool(candidate.canonical_latex) and candidate.status == "recognized",
                }
                for candidate in candidates
            ],
            "selected_candidate": selected_candidate if selected_candidate is not None else 0,
        }
    )
    # 此处的 verified 仅指受限解析器生成了 AST；推导等价与解集由第二阶段另行核验。
    verifications.append(
        {
            "id": f"verification-{len(verifications) + 1}",
            "formula_id": formula_id,
            "kind": "syntax",
            "status": "verified" if parsed else "uncertain",
            "reason_code": "ast_well_formed" if parsed else str(normalized.get("reason_code", "unsupported_expression")),
            "domain": "real",
            "engine": "math-verification-worker",
            "engine_version": "restricted-v1",
            "ruleset_version": "restricted-v1",
            "confidence": 1.0 if parsed else 0.0,
        }
    )


def _confidence(value: object) -> float:
    try:
        return max(0.0, min(1.0, float(value)))
    except (TypeError, ValueError):
        return 0.0


def _box(value: object, image_size: tuple[int, int] | None = None) -> dict[str, float]:
    if isinstance(value, (list, tuple)) and len(value) == 4:
        x, y, width, height = (float(item) for item in value)
        if image_size and image_size[0] > 0 and image_size[1] > 0:
            image_width, image_height = image_size
            x, width = x / image_width, width / image_width
            y, height = y / image_height, height / image_height
        if 0 <= x < 1 and 0 <= y < 1 and width > 0 and height > 0:
            right = min(1.0, x + width)
            bottom = min(1.0, y + height)
            return {
                "x": x,
                "y": y,
                "width": max(0.000001, right - x),
                "height": max(0.000001, bottom - y),
            }
    return {"x": 0.0, "y": 0.0, "width": 1.0, "height": 1.0}
