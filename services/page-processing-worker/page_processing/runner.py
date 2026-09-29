from __future__ import annotations

import hashlib
import json
import sys
import threading
import time
import traceback

from edugrade_worker_runtime import LeaseHeartbeat

from page_processing.api import APIError, Client
from page_processing.barcode import detect_barcodes
from page_processing.config import Config
from page_processing.decoder import DecodeError, decode_document
from page_processing.omr import OMRExtractionError, OMRProfile, extract_marks
from page_processing.registration import (
    RegistrationError,
    TemplateMatchCandidate,
    TemplateRoutingError,
    crop_regions,
    inspect_template_guard,
    match_template_candidates,
    register_page,
    register_page_manual,
)


class _NullHeartbeat:
    """No-op stand-in so handlers can run without a live lease (direct calls in tests)."""

    def raise_if_failed(self) -> None:
        return None

    def stop(self, raise_on_error: bool = True) -> None:
        return None

    def set_progress(self, progress: dict, *, flush: bool = False) -> None:
        return None


_NULL_HEARTBEAT = _NullHeartbeat()


class Runner:
    def __init__(self, client: Client, config: Config) -> None:
        self.client = client
        self.config = config

    def run_once(self) -> int:
        tasks = self.client.claim(self.config.worker_id, self.config.batch_size, self.config.lease_seconds)
        handlers = {
            "layout": self._process_paper_import_decode,
            "capture_file_decode": self._process_capture,
            "page_template_match": self._process_template_match,
            "page_registration": self._process_registration,
            "page_registration_correction_preview": self._process_correction,
            "omr_extract": self._process_omr,
        }
        for task in tasks:
            try:
                activate_task = getattr(self.client, "activate_task", None)
                if callable(activate_task):
                    activate_task(task, self.config.worker_id)
                handler = handlers.get(str(task.get("task_type") or ""))
                if handler is None:
                    self.client.fail_task(task, "unsupported_page_processing_task", {"task_type": task.get("task_type")})
                    continue
                with _LeaseHeartbeat(
                    client=self.client,
                    task=task,
                    worker_id=self.config.worker_id,
                    interval=self.config.heartbeat_interval,
                    lease_seconds=self.config.lease_seconds,
                    request_timeout=self.config.heartbeat_timeout,
                ) as heartbeat:
                    handler(task, heartbeat)
            except Exception as exc:  # noqa: BLE001 - each task must be isolated and reported before the loop continues.
                try:
                    self._fail(task, exc)
                except Exception as fail_exc:  # noqa: BLE001 - reporting can fail after a lease or gateway outage.
                    # A lost lease or gateway outage must not crash the worker loop:
                    # the runtime re-assigns the task once the lease expires. Log
                    # the original failure too — it is the only remaining record
                    # of why the task failed, since the report never landed.
                    print(
                        f"page-processing task {task.get('id')} failed: {type(exc).__name__}: {exc}; "
                        f"fail-report also failed: {type(fail_exc).__name__}: {fail_exc}",
                        file=sys.stderr,
                        flush=True,
                    )
                    traceback.print_exception(exc, file=sys.stderr)
        return len(tasks)

    def _process_paper_import_decode(self, task: dict, heartbeat: _LeaseHeartbeat | _NullHeartbeat = _NULL_HEARTBEAT) -> None:
        started = time.perf_counter()
        payload = task.get("payload") or {}
        if str(task.get("source_type") or "") != "paper_import_job" or not isinstance(payload.get("documents"), list):
            raise DecodeError("unsupported_layout_task")
        results = []
        document_total = len(payload["documents"])
        heartbeat.set_progress({"stage": "page_decode", "phase": "decoding", "completed": 0, "total": document_total, "unit": "document", "message": f"准备处理 {document_total} 份资料"}, flush=True)
        for document_position, document in enumerate(payload["documents"], 1):
            source_id = str(document.get("source_id") or "")
            document_index = int(document.get("document_index", -1))
            if not source_id or document_index < 0:
                raise DecodeError("paper_import_source_invalid")
            source = self.client.download(str(document["download_url"]))
            heartbeat.raise_if_failed()
            heartbeat.set_progress({"stage": "page_decode", "phase": "decoding", "completed": document_position - 1, "total": document_total, "unit": "document", "message": f"正在解析第 {document_position}/{document_total} 份资料"}, flush=True)
            pages, _ = decode_document(
                source, str(document.get("content_type") or ""),
                render_dpi=int(document.get("render_dpi") or 220),
                max_pages=min(int(document.get("max_pages") or self.config.max_pages), self.config.max_pages),
                max_page_pixels=self.config.max_page_pixels,
                max_total_pixels=self.config.max_total_pixels,
            )
            page_total = len(pages)
            for page_position, page in enumerate(pages, 1):
                heartbeat.raise_if_failed()
                heartbeat.set_progress({"stage": "page_decode", "phase": "uploading", "completed": page_position - 1, "total": page_total, "unit": "page", "page_no": page_position, "page_total": page_total, "message": f"正在保存第 {page_position}/{page_total} 页"}, flush=True)
                asset = self.client.upload_asset(task, "import", str(payload["paper_import_id"]), f"source-{document_index:04d}-page-{page.index:04d}.png", page.png)
                results.append({"source_id": source_id, "document_index": document_index, "page_no": page.index, "file_asset_id": asset["id"], "sha256": asset["hash_sha256"], "width": page.width, "height": page.height})
            heartbeat.set_progress({"stage": "page_decode", "phase": "decoding", "completed": document_position, "total": document_total, "unit": "document", "message": f"已处理 {document_position}/{document_total} 份资料，共生成 {len(results)} 页"}, flush=True)
        heartbeat.stop(raise_on_error=False)
        self.client.complete_paper_import_decode(str(payload["paper_import_id"]), {
            "task_id": task["id"], "lease_token": task["lease_token"],
            "duration_ms": int((time.perf_counter() - started) * 1000), "pages": results,
        })

    def run_forever(self) -> None:
        while True:
            if self.run_once() == 0:
                time.sleep(self.config.poll_interval)

    def _process_capture(self, task: dict, heartbeat: _LeaseHeartbeat | _NullHeartbeat = _NULL_HEARTBEAT) -> None:
        started = time.perf_counter()
        payload = task.get("payload") or {}
        source = self.client.download(str(payload["download_url"]))
        heartbeat.raise_if_failed()
        pages, detected_type = decode_document(
            source,
            str(payload.get("content_type") or ""),
            render_dpi=int(payload.get("render_dpi") or 300),
            max_pages=min(int(payload.get("max_pages") or self.config.max_pages), self.config.max_pages),
            max_page_pixels=self.config.max_page_pixels,
            max_total_pixels=self.config.max_total_pixels,
        )
        heartbeat.raise_if_failed()
        page_results = []
        for page in pages:
            heartbeat.raise_if_failed()
            uploaded = self.client.upload_page(task, page.index, page.png)
            page_results.append({"source_index": page.index, "file_asset_id": uploaded["id"], "sha256": uploaded["hash_sha256"], "width": page.width, "height": page.height, "barcodes": detect_barcodes(page.png)})
        result_version = "sha256:" + hashlib.sha256(json.dumps(page_results, sort_keys=True).encode("utf-8")).hexdigest()
        heartbeat.stop(raise_on_error=False)
        self.client.complete(str(payload["capture_file_id"]), {"task_id": task["id"], "lease_token": task["lease_token"], "result_version": result_version, "duration_ms": int((time.perf_counter() - started) * 1000), "decoder_profile": "pdfium-pillow-v1", "pages": page_results, "original_page_count": len(pages), "detected_content_type": detected_type})

    def _process_registration(self, task: dict, heartbeat: _LeaseHeartbeat | _NullHeartbeat = _NULL_HEARTBEAT) -> None:
        started = time.perf_counter()
        payload = task.get("payload") or {}
        source = self.client.download(str(payload["source_download_url"]))
        template = self.client.download(str(payload["template_download_url"]))
        heartbeat.raise_if_failed()
        guard_report = {}
        output = None
        if str(payload.get("routing_mode") or "") in {"bound_auto", "locked_with_guard"}:
            guard = inspect_template_guard(
                source,
                str(payload.get("source_content_type") or "image/png"),
                template,
                str(payload.get("template_content_type") or ""),
                question_regions=list(payload.get("question_regions") or []),
                render_dpi=int(payload.get("render_dpi") or 300),
                template_page_index=int(payload.get("template_page_index") or 1),
            )
            guard_report = {
                "passed": guard.passed,
                "orientation_match": guard.orientation_match,
                "aspect_ratio_delta": guard.aspect_ratio_delta,
                "perceptual_hash_score": guard.perceptual_hash_score,
                "layout_score": guard.layout_score,
                "score": guard.score,
                "profile_version": guard.profile_version,
            }
            if not guard.passed:
                candidates = [TemplateMatchCandidate(
                    template_id=str(payload["template_id"]),
                    template_content_hash=str(payload["template_content_hash"]),
                    data=template,
                    content_type=str(payload.get("template_content_type") or ""),
                    page_index=int(payload.get("template_page_index") or 1),
                    question_regions=list(payload.get("question_regions") or []),
                )]
                for candidate in list(payload.get("fallback_templates") or []):
                    if not isinstance(candidate, dict):
                        continue
                    candidates.append(TemplateMatchCandidate(
                        template_id=str(candidate.get("template_id") or ""),
                        template_content_hash=str(candidate.get("template_content_hash") or ""),
                        data=self.client.download(str(candidate.get("template_download_url") or "")),
                        content_type=str(candidate.get("template_content_type") or ""),
                        page_index=int(candidate.get("template_page_index") or 1),
                        question_regions=list(candidate.get("question_regions") or []),
                        template_name=str(candidate.get("template_name") or ""),
                        version_no=int(candidate.get("version_no") or 0),
                    ))
                    heartbeat.raise_if_failed()
                match = match_template_candidates(
                    source,
                    str(payload.get("source_content_type") or "image/png"),
                    candidates,
                    render_dpi=int(payload.get("render_dpi") or 300),
                )
                guard_report["fallback_decision"] = match.decision
                guard_report["fallback_candidates"] = match.candidates
                guard_report["fallback_margin"] = match.margin
                if match.decision != "matched":
                    code = "ambiguous_template_match" if match.decision == "ambiguous" else "no_template_match"
                    raise TemplateRoutingError(code, {"guard": guard_report, "candidates": match.candidates})
                # 重匹配只用于诊断与验证；候选更匹配也不能在任务内覆盖考试锁定的模板及哈希。
                if match.selected_template_id != str(payload["template_id"]) or match.selected_template_content_hash != str(payload["template_content_hash"]):
                    raise TemplateRoutingError("template_conflict_with_exam_lock", {
                        "locked_template_id": str(payload["template_id"]),
                        "matched_template_id": match.selected_template_id,
                        "score": match.score,
                        "margin": match.margin,
                        "candidates": match.candidates,
                    })
                output = match.registration
        if output is None:
            output = register_page(
                source,
                str(payload.get("source_content_type") or "image/png"),
                template,
                str(payload.get("template_content_type") or ""),
                render_dpi=int(payload.get("render_dpi") or 300),
                template_page_index=int(payload.get("template_page_index") or 1),
            )
        run_id = str(payload["registration_run_id"])
        registered = self.client.upload_asset(task, "page_registration_output", run_id, "registered.png", output.registered_png)
        segments = []
        for index, crop in enumerate(crop_regions(output.registered_png, list(payload.get("question_regions") or [])), start=1):
            heartbeat.raise_if_failed()
            asset = self.client.upload_asset(task, "answer_segment_crop", run_id, f"segment-{index:04d}.png", crop.pop("png"))
            segments.append({**crop, "file_asset_id": asset["id"], "sha256": asset["hash_sha256"]})
        evidence = output.evidence
        result_version = "sha256:" + hashlib.sha256(json.dumps({"registered": registered["hash_sha256"], "segments": segments}, sort_keys=True).encode()).hexdigest()
        heartbeat.stop(raise_on_error=False)
        self.client.complete_registration(run_id, {
            "task_id": task["id"], "lease_token": task["lease_token"], "result_version": result_version,
            "duration_ms": int((time.perf_counter() - started) * 1000), "registered_file_asset_id": registered["id"],
            "registered_sha256": registered["hash_sha256"], "method": evidence.method, "confidence": evidence.confidence,
            "source_to_template_matrix": evidence.source_to_template, "template_to_source_matrix": evidence.template_to_source,
            "feature_count": evidence.feature_count, "match_count": evidence.match_count, "inlier_count": evidence.inlier_count,
            "inlier_ratio": evidence.inlier_ratio, "reprojection_error": evidence.reprojection_error, "coverage": evidence.coverage,
            "guard_report": guard_report,
            "segments": segments,
        })

    def _process_template_match(self, task: dict, heartbeat: _LeaseHeartbeat | _NullHeartbeat = _NULL_HEARTBEAT) -> None:
        started = time.perf_counter()
        payload = task.get("payload") or {}
        source = self.client.download(str(payload["source_download_url"]))
        heartbeat.raise_if_failed()
        candidates = []
        for raw in list(payload.get("candidates") or []):
            if not isinstance(raw, dict) or not raw.get("template_id") or not raw.get("template_download_url"):
                raise RegistrationError("template_match_candidate_invalid")
            candidates.append(TemplateMatchCandidate(
                template_id=str(raw["template_id"]),
                template_content_hash=str(raw.get("template_content_hash") or ""),
                data=self.client.download(str(raw["template_download_url"])),
                content_type=str(raw.get("template_content_type") or ""),
                page_index=int(raw.get("template_page_index") or 1),
                question_regions=list(raw.get("question_regions") or []),
                template_name=str(raw.get("template_name") or ""),
                version_no=int(raw.get("version_no") or 0),
            ))
            heartbeat.raise_if_failed()
        outcome = match_template_candidates(
            source,
            str(payload.get("source_content_type") or "image/png"),
            candidates,
            render_dpi=int(payload.get("render_dpi") or 300),
        )
        result_version = "sha256:" + hashlib.sha256(json.dumps({
            "source": str(payload.get("source_download_url") or ""),
            "decision": outcome.decision,
            "selected_template_id": outcome.selected_template_id,
            "score": outcome.score,
            "margin": outcome.margin,
            "candidates": outcome.candidates,
        }, sort_keys=True).encode()).hexdigest()
        heartbeat.stop(raise_on_error=False)
        self.client.complete_template_match(str(payload["template_match_run_id"]), {
            "task_id": task["id"],
            "lease_token": task["lease_token"],
            "result_version": result_version,
            "duration_ms": int((time.perf_counter() - started) * 1000),
            "decision": outcome.decision,
            "selected_template_id": outcome.selected_template_id or "",
            "selected_template_content_hash": outcome.selected_template_content_hash or "",
            "score": outcome.score,
            "margin": outcome.margin,
            "candidates": outcome.candidates,
        })

    def _process_correction(self, task: dict, heartbeat: _LeaseHeartbeat | _NullHeartbeat = _NULL_HEARTBEAT) -> None:
        started = time.perf_counter()
        payload = task.get("payload") or {}
        source = self.client.download(str(payload["source_download_url"]))
        template = self.client.download(str(payload["template_download_url"]))
        heartbeat.raise_if_failed()
        output = register_page_manual(
            source, str(payload.get("source_content_type") or "image/png"),
            template, str(payload.get("template_content_type") or ""),
            list(payload.get("source_points") or []), list(payload.get("template_points") or []),
            render_dpi=int(payload.get("render_dpi") or 300),
            template_page_index=int(payload.get("template_page_index") or 1),
        )
        correction_id = str(payload["correction_id"])
        registered = self.client.upload_asset(task, "page_registration_correction_preview", correction_id, "registered-preview.png", output.registered_png)
        segments = []
        for index, crop in enumerate(crop_regions(output.registered_png, list(payload.get("question_regions") or [])), start=1):
            heartbeat.raise_if_failed()
            asset = self.client.upload_asset(task, "page_registration_correction_preview", correction_id, f"segment-preview-{index:04d}.png", crop.pop("png"))
            segments.append({**crop, "file_asset_id": asset["id"], "sha256": asset["hash_sha256"]})
        evidence = output.evidence
        result_version = "sha256:" + hashlib.sha256(json.dumps({"registered": registered["hash_sha256"], "segments": segments}, sort_keys=True).encode()).hexdigest()
        heartbeat.stop(raise_on_error=False)
        self.client.complete_correction(correction_id, {
            "task_id": task["id"], "lease_token": task["lease_token"], "result_version": result_version,
            "duration_ms": int((time.perf_counter() - started) * 1000),
            "preview_registered_file_asset_id": registered["id"],
            "preview_registered_sha256": registered["hash_sha256"],
            "source_to_template_matrix": evidence.source_to_template,
            "template_to_source_matrix": evidence.template_to_source,
            "coverage": evidence.coverage, "reprojection_error": evidence.reprojection_error,
            "validation_report": {"passed": True, "orientation_valid": True, "regions_in_bounds": True, "coverage_valid": True},
            "segments": segments,
        })

    def _process_omr(self, task: dict, heartbeat: _LeaseHeartbeat | _NullHeartbeat = _NULL_HEARTBEAT) -> None:
        started = time.perf_counter()
        payload = task.get("payload") or {}
        source = self.client.download(str(payload["source_download_url"]))
        heartbeat.raise_if_failed()
        profile_data = payload.get("profile") or {}
        if not isinstance(profile_data, dict):
            raise OMRExtractionError("omr_profile_invalid")
        profile_hash = str(payload.get("profile_hash") or "").strip()
        if not profile_hash:
            raise OMRExtractionError("omr_profile_hash_missing")
        profile = OMRProfile(
            mode=str(profile_data.get("mode") or "manual_only"),
            marked_threshold=profile_data.get("marked_threshold", 0.18),
            ambiguous_threshold=profile_data.get("ambiguous_threshold", 0.10),
            minimum_margin=profile_data.get("minimum_margin", 0.06),
            border_fraction=profile_data.get("border_fraction", 0.12),
            version=str(profile_data.get("version") or "opencv-fill-v1"),
            reference_mask_dilation_pixels=profile_data.get("reference_mask_dilation_pixels", 0),
        )
        reference_data = payload.get("reference")
        reference = reference_data if isinstance(reference_data, dict) else {}
        reference_bytes: bytes | None = None
        reference_sha256 = ""
        if profile.mode == "template_difference":
            reference_url = str(reference.get("download_url") or "").strip()
            reference_sha256 = str(reference.get("sha256") or "").strip().lower()
            if not str(reference.get("file_asset_id") or "").strip() or not reference_url or not reference_sha256:
                raise OMRExtractionError("omr_reference_missing")
            reference_bytes = self.client.download(reference_url)
            # 差分识别必须使用任务绑定的空白参考字节，不能只信任同一个下载地址。
            if hashlib.sha256(reference_bytes).hexdigest().lower() != reference_sha256:
                raise OMRExtractionError("omr_reference_hash_mismatch")
        result = extract_marks(
            source,
            list(payload.get("option_regions") or []),
            multiple=payload.get("multiple", False),
            profile=profile,
            reference_image_bytes=reference_bytes,
            reference_content_type=str(reference.get("content_type") or ""),
            reference_page_no=int(reference.get("page_no") or 1),
            reference_page_width=int(reference.get("page_width") or 0),
            reference_page_height=int(reference.get("page_height") or 0),
            reference_question_region=reference.get("question_region") if isinstance(reference.get("question_region"), dict) else None,
        )
        result["reference_sha256"] = reference_sha256
        run_id = str(payload["omr_run_id"])
        heartbeat.raise_if_failed()
        overlay = self.client.upload_asset(task, "omr_evidence", run_id, "omr-overlay.png", result.pop("overlay_png"))
        result_version = "sha256:" + hashlib.sha256(json.dumps({**result, "profile_hash": profile_hash}, sort_keys=True).encode()).hexdigest()
        heartbeat.stop(raise_on_error=False)
        self.client.complete_omr(run_id, {
            "task_id": task["id"], "lease_token": task["lease_token"],
            "result_version": result_version,
            "duration_ms": int((time.perf_counter() - started) * 1000),
            "decision": result["decision"], "selected": result["selected"],
            "confidence": result["confidence"], "needs_human_review": result["needs_human_review"],
            "measurements": result["measurements"], "profile_version": result["profile_version"],
            "profile_hash": profile_hash,
            "reference_sha256": result["reference_sha256"],
            "thresholds": result["thresholds"],
            "overlay_file_asset_id": overlay["id"], "overlay_sha256": overlay["hash_sha256"],
        })

    def _fail(self, task: dict, exc: Exception) -> None:
        payload = task.get("payload") or {}
        known_error = isinstance(exc, (DecodeError, RegistrationError, OMRExtractionError, APIError))
        code = str(exc) if known_error else "page_processing_failed"
        detail = {"error_type": type(exc).__name__, "message": str(exc)[:200]}
        error_detail = getattr(exc, "detail", None)
        if isinstance(error_detail, dict):
            detail.update(error_detail)
        retryable = not isinstance(exc, (DecodeError, RegistrationError, OMRExtractionError))
        if isinstance(exc, APIError):
            # A missing/forbidden source is deterministic. Other API failures,
            # including a lost lease while reporting, retain the worker
            # runtime's existing retry behavior.
            retryable = code not in {"source_download_forbidden", "source_not_found"}
        if task.get("task_type") == "capture_file_decode":
            self.client.fail(str(payload["capture_file_id"]), {"task_id": task["id"], "lease_token": task["lease_token"], "retryable": retryable, "error_code": code, "error_detail": detail, "duration_ms": 0})
        elif task.get("task_type") == "page_registration" and payload.get("registration_run_id"):
            self.client.fail_registration(str(payload["registration_run_id"]), {"task_id": task["id"], "lease_token": task["lease_token"], "retryable": retryable, "error_code": code, "error_detail": detail, "duration_ms": 0})
        elif task.get("task_type") == "page_template_match" and payload.get("template_match_run_id"):
            self.client.fail_template_match(str(payload["template_match_run_id"]), {"task_id": task["id"], "lease_token": task["lease_token"], "retryable": retryable, "error_code": code, "error_detail": detail, "duration_ms": 0})
        elif task.get("task_type") == "page_registration_correction_preview" and payload.get("correction_id"):
            self.client.fail_correction(str(payload["correction_id"]), {"task_id": task["id"], "lease_token": task["lease_token"], "retryable": retryable, "error_code": code, "error_detail": detail, "duration_ms": 0})
        elif task.get("task_type") == "omr_extract" and payload.get("omr_run_id"):
            self.client.fail_omr(str(payload["omr_run_id"]), {"task_id": task["id"], "lease_token": task["lease_token"], "retryable": retryable, "error_code": code, "error_detail": detail, "duration_ms": 0})
        elif task.get("task_type") == "layout" and payload.get("paper_import_id"):
            self.client.fail_paper_import(str(payload["paper_import_id"]), task, code, detail, retryable)
        else:
            self.client.fail_task(task, code, detail)


class _LeaseHeartbeat(LeaseHeartbeat):
    def __init__(
        self,
        *,
        client: Client,
        task: dict,
        worker_id: str,
        interval: float,
        lease_seconds: int,
        request_timeout: float,
    ) -> None:
        self.client = client
        self.task = task
        self.worker_id = worker_id
        self._progress: dict[str, object] = {}
        self._progress_lock = threading.Lock()
        super().__init__(
            send=self._send,
            interval=interval,
            lease_seconds=lease_seconds,
            request_timeout=request_timeout,
            thread_name=f"page-processing-heartbeat-{worker_id}",
        )

    def _send(self) -> None:
        with self._progress_lock:
            progress = dict(self._progress)
        self.client.heartbeat(self.task, self.worker_id, self.lease_seconds, timeout=self.request_timeout, progress=progress)

    def set_progress(self, progress: dict, *, flush: bool = False) -> None:
        with self._progress_lock:
            self._progress = dict(progress)
        if flush:
            self._send()
