from __future__ import annotations

import hashlib
import io
import logging
import math
import threading
import time
from dataclasses import dataclass
from datetime import UTC, datetime
from typing import Any

from edugrade_worker_runtime import LeaseHeartbeat, validate_lease_timing

from .api import APIError, AuthenticationError
from .engine import OCREngine

log = logging.getLogger(__name__)


@dataclass(frozen=True)
class WorkerConfig:
    worker_id: str
    batch_size: int = 1
    engine: str = "paddleocr"
    engine_version: str = "pp-ocrv5"
    config_hash: str = ""
    preprocess_profile: str = "default"
    lease_seconds: int = 300
    heartbeat_interval: float = 10.0
    heartbeat_timeout: float = 3.0
    cpu_threads: int = 4
    enable_mkldnn: str = "auto"
    enable_hpi: bool = False
    use_textline_orientation: bool = True
    text_det_limit_type: str = "min"
    text_det_limit_side_len: int = 64
    text_recognition_batch_size: int = 1

    def __post_init__(self) -> None:
        if self.batch_size != 1:
            raise ValueError("the sequential OCR worker must claim exactly one task")
        if not self.engine or not self.engine_version:
            raise ValueError("engine and engine_version must be configured")
        if self.cpu_threads < 1 or self.cpu_threads > 64:
            raise ValueError("cpu_threads must be between 1 and 64")
        validate_lease_timing(self.lease_seconds, self.heartbeat_interval, self.heartbeat_timeout)


class OCRRunner:
    def __init__(self, *, api: Any, engine: OCREngine, config: WorkerConfig) -> None:
        self.api = api
        self.engine = engine
        self.config = config

    def process_once(self) -> int:
        tasks = self.api.claim_tasks(self.config.worker_id, self.config.batch_size, self.config.lease_seconds)
        processed = 0
        for task in tasks:
            self._process_task(task)
            processed += 1
        return processed

    def _process_task(self, runtime_task: dict[str, Any]) -> None:
        start = time.monotonic()
        runtime_task_id = str(runtime_task["id"])
        lease_token = str(runtime_task["lease_token"])
        task_id = str(runtime_task["source_id"])
        tenant_id = str(runtime_task["tenant_id"])
        activate_task = getattr(self.api, "activate_task", None)
        if callable(activate_task):
            activate_task(runtime_task, self.config.worker_id)
        if str(runtime_task.get("source_type") or "") == "paper_import_job":
            self._process_paper_import(runtime_task, start, tenant_id)
            return
        incompatibility = self._runtime_incompatibility(runtime_task)
        if incompatibility is not None:
            self.api.fail_task(task_id, incompatibility, runtime_task_id, lease_token, False, tenant_id)
            return
        with _LeaseHeartbeat(
            api=self.api,
            runtime_task_id=runtime_task_id,
            lease_token=lease_token,
            worker_id=self.config.worker_id,
            interval=self.config.heartbeat_interval,
            lease_seconds=self.config.lease_seconds,
            request_timeout=self.config.heartbeat_timeout,
            tenant_id=tenant_id,
        ) as heartbeat:
            self.api.start_task(task_id, tenant_id)
            task_input = self.api.get_task_input(task_id, tenant_id)
            image_hash = hashlib.sha256()
            results: list[dict[str, Any]] = []
            download_ms = decode_ms = ocr_ms = 0
            region_count = 0
            for page in task_input.get("pages", []):
                heartbeat.raise_if_failed()
                page_id = str(page["id"])
                download_start = time.monotonic()
                try:
                    image_bytes = self.api.download(str(page["download_url"]), tenant_id)
                except AuthenticationError:
                    raise
                except APIError:
                    heartbeat.stop()
                    self.api.fail_task(task_id, "download_failed", runtime_task_id, lease_token, True, tenant_id)
                    return
                download_ms += int((time.monotonic() - download_start) * 1000)
                image_hash.update(image_bytes)
                regions = page.get("regions")
                if regions is not None and not isinstance(regions, list):
                    heartbeat.stop()
                    self.api.fail_task(task_id, "invalid_ocr_region", runtime_task_id, lease_token, False, tenant_id)
                    return
                page_regions = regions if isinstance(regions, list) and regions else [None]
                decoded_page = None
                if isinstance(regions, list) and regions:
                    region_count += len(regions)
                    decode_start = time.monotonic()
                    try:
                        decoded_page = _decode_region_page(image_bytes)
                    except ValueError:
                        heartbeat.stop()
                        self.api.fail_task(task_id, "invalid_ocr_region", runtime_task_id, lease_token, False, tenant_id)
                        return
                    decode_ms += int((time.monotonic() - decode_start) * 1000)
                for region in page_regions:
                    input_bytes = image_bytes
                    offset = (0.0, 0.0)
                    if region is not None:
                        if not isinstance(region, dict):
                            heartbeat.stop()
                            self.api.fail_task(task_id, "invalid_ocr_region", runtime_task_id, lease_token, False, tenant_id)
                            return
                        decode_start = time.monotonic()
                        try:
                            input_bytes, offset = _crop_region(decoded_page, region.get("bbox"))
                        except (ValueError, TypeError):
                            heartbeat.stop()
                            self.api.fail_task(task_id, "invalid_ocr_region", runtime_task_id, lease_token, False, tenant_id)
                            return
                        decode_ms += int((time.monotonic() - decode_start) * 1000)
                    ocr_start = time.monotonic()
                    try:
                        recognize_region = getattr(self.engine, "recognize_region", None)
                        blocks = recognize_region(input_bytes) if region is not None and callable(recognize_region) else self.engine.recognize(input_bytes)
                    except Exception:  # noqa: BLE001 - Paddle backends raise heterogeneous runtime exceptions.
                        heartbeat.stop()
                        self.api.fail_task(task_id, "ocr_engine_failed", runtime_task_id, lease_token, True, tenant_id)
                        return
                    ocr_ms += int((time.monotonic() - ocr_start) * 1000)
                    for block in blocks:
                        if not _valid_block(block.text, block.bbox, block.confidence):
                            continue
                        bbox = list(block.bbox)
                        if region is not None:
                            # 用实际取整后的裁剪偏移还原整页像素坐标，保留证据定位一致性。
                            bbox[0] += offset[0]
                            bbox[1] += offset[1]
                        results.append(
                            {
                                "submission_page_id": page_id,
                                "text": block.text,
                                "bbox": bbox,
                                "confidence": block.confidence,
                            }
                        )
                if decoded_page is not None:
                    decoded_page.close()
            heartbeat.raise_if_failed()
            if not results:
                heartbeat.stop()
                self.api.fail_task(task_id, "empty_ocr_result", runtime_task_id, lease_token, False, tenant_id)
                return
            duration_ms = int((time.monotonic() - start) * 1000)
            payload = {
                "worker_id": self.config.worker_id,
                "model_version": self.engine.model_version,
                "config_hash": self._config_hash(),
                "input_hash": image_hash.hexdigest(),
                "duration_ms": duration_ms,
                "preprocess_profile": self.config.preprocess_profile,
                "runtime_task_id": runtime_task_id,
                "runtime_lease_token": lease_token,
                "results": results,
            }
            heartbeat.stop()
            log.info(
                "ocr task completed",
                extra={
                    "worker_id": self.config.worker_id,
                    "task_id": task_id,
                    "page_count": len(task_input.get("pages", [])),
                    "region_count": region_count,
                    "model_version": self.engine.model_version,
                    "config_hash": self._config_hash(),
                    "duration_ms": duration_ms,
                    "download_duration_ms": download_ms,
                    "decode_duration_ms": decode_ms,
                    "ocr_duration_ms": ocr_ms,
                    "result_block_count": len(results),
                },
            )
            self.api.complete_task(task_id, payload, tenant_id)

    def _process_paper_import(self, runtime_task: dict[str, Any], start: float, tenant_id: str) -> None:
        runtime_task_id = str(runtime_task["id"])
        lease_token = str(runtime_task["lease_token"])
        import_id = str(runtime_task["source_id"])
        payload = runtime_task.get("payload") or {}
        incompatibility = self._runtime_incompatibility(runtime_task)
        if incompatibility is not None:
            self.api.fail_paper_import(import_id, runtime_task_id, lease_token, incompatibility, False, tenant_id)
            return
        pages = payload.get("pages")
        if not isinstance(pages, list) or not pages:
            self.api.fail_paper_import(import_id, runtime_task_id, lease_token, "invalid_paper_ocr_payload", False, tenant_id)
            return
        with _LeaseHeartbeat(
            api=self.api, runtime_task_id=runtime_task_id, lease_token=lease_token,
            worker_id=self.config.worker_id, interval=self.config.heartbeat_interval,
            lease_seconds=self.config.lease_seconds, request_timeout=self.config.heartbeat_timeout,
            tenant_id=tenant_id,
        ) as heartbeat:
            blocks: list[dict[str, Any]] = []
            try:
                page_total = len(pages)
                heartbeat.set_progress({"stage": "text_ocr", "phase": "recognizing", "completed": 0, "total": page_total, "unit": "page", "page_total": page_total, "model": self.engine.model_version, "message": "正在识别第 1 页"}, flush=True)
                for page_index, page in enumerate(pages, 1):
                    heartbeat.raise_if_failed()
                    heartbeat.set_progress({"stage": "text_ocr", "phase": "recognizing", "completed": page_index - 1, "total": page_total, "unit": "page", "page_no": page_index, "page_total": page_total, "model": self.engine.model_version, "message": f"正在识别第 {page_index}/{page_total} 页"}, flush=True)
                    source_id = str(page.get("source_id") or "")
                    document_index = int(page.get("document_index", -1))
                    page_no = int(page.get("page_no") or 0)
                    if not source_id or document_index < 0 or page_no <= 0:
                        raise ValueError("invalid_paper_ocr_page")
                    image = self.api.download(str(page["download_url"]), tenant_id)
                    for block in self.engine.recognize(image):
                        if _valid_block(block.text, block.bbox, block.confidence):
                            blocks.append({"source_id": source_id, "document_index": document_index, "page_no": page_no, "block_id": f"{source_id}:{page_no}:{len(blocks)+1}", "text": block.text, "bbox": block.bbox, "confidence": block.confidence})
                    heartbeat.set_progress({"stage": "text_ocr", "phase": "recognizing", "completed": page_index, "total": page_total, "unit": "page", "page_no": page_index, "page_total": page_total, "model": self.engine.model_version, "message": f"已识别 {page_index}/{page_total} 页，累计 {len(blocks)} 个文本块"}, flush=True)
                if not blocks:
                    raise ValueError("empty_ocr_result")
            except AuthenticationError:
                raise
            except Exception as exc:  # noqa: BLE001 - OCR backends expose heterogeneous errors.
                heartbeat.stop()
                self.api.fail_paper_import(import_id, runtime_task_id, lease_token, str(exc)[:80] or "paper_ocr_failed", False, tenant_id)
                return
            heartbeat.stop()
            duration_ms = int((time.monotonic() - start) * 1000)
            log.info(
                "paper import OCR task completed",
                extra={
                    "worker_id": self.config.worker_id,
                    "task_id": import_id,
                    "page_count": len(pages),
                    "region_count": 0,
                    "model_version": self.engine.model_version,
                    "config_hash": self._config_hash(),
                    "duration_ms": duration_ms,
                    "download_duration_ms": None,
                    "decode_duration_ms": None,
                    "ocr_duration_ms": None,
                    "result_block_count": len(blocks),
                },
            )
            self.api.complete_paper_import_ocr(import_id, {
                "task_id": runtime_task_id, "lease_token": lease_token,
                "duration_ms": duration_ms, "blocks": blocks,
            }, tenant_id)

    def _config_hash(self) -> str:
        if self.config.config_hash:
            return self.config.config_hash
        raw = (
            f"engine={self.config.engine}|engine_version={self.config.engine_version}|model_version={self.engine.model_version}|"
            f"device={self.engine.device}|preprocess_profile={self.config.preprocess_profile}|"
            f"cpu_threads={self.config.cpu_threads}|effective_mkldnn={getattr(self.engine, 'effective_mkldnn', self.config.enable_mkldnn == 'true')}|"
            f"enable_hpi={self.config.enable_hpi}|use_textline_orientation={self.config.use_textline_orientation}|"
            f"text_det_limit_type={self.config.text_det_limit_type}|text_det_limit_side_len={self.config.text_det_limit_side_len}|"
            f"text_recognition_batch_size={self.config.text_recognition_batch_size}|roi_text_det_limit_type=min|roi_text_det_limit_side_len=64"
        ).encode()
        return hashlib.sha256(raw).hexdigest()

    def _runtime_incompatibility(self, runtime_task: dict[str, Any]) -> str | None:
        payload = runtime_task.get("payload")
        if not isinstance(payload, dict):
            return "invalid_ocr_runtime_payload"
        engine = payload.get("engine")
        engine_version = payload.get("engine_version")
        if not isinstance(engine, str) or not engine.strip():
            return "invalid_ocr_runtime_payload"
        if not isinstance(engine_version, str) or not engine_version.strip():
            return "invalid_ocr_runtime_payload"
        if engine != self.config.engine:
            return "unsupported_ocr_engine"
        if engine_version != self.config.engine_version:
            return "unsupported_ocr_engine_version"
        return None


class _LeaseHeartbeat(LeaseHeartbeat):
    def __init__(
        self,
        *,
        api: Any,
        runtime_task_id: str,
        lease_token: str,
        worker_id: str,
        interval: float,
        lease_seconds: int,
        request_timeout: float,
        tenant_id: str,
    ) -> None:
        self.api = api
        self.runtime_task_id = runtime_task_id
        self.lease_token = lease_token
        self.worker_id = worker_id
        self.tenant_id = tenant_id
        self._progress: dict[str, Any] = {}
        self._progress_event_seq = 0
        self._progress_lock = threading.Lock()
        super().__init__(
            send=self._send,
            interval=interval,
            lease_seconds=lease_seconds,
            request_timeout=request_timeout,
            thread_name=f"ocr-heartbeat-{worker_id}",
        )

    def _send(self) -> None:
        with self._progress_lock:
            progress = dict(self._progress)
        self.api.heartbeat_task(
            self.runtime_task_id,
            self.lease_token,
            self.worker_id,
            self.lease_seconds,
            self.request_timeout,
            self.tenant_id,
            progress=progress,
        )

    def set_progress(self, progress: dict[str, Any], *, flush: bool = False) -> None:
        with self._progress_lock:
            self._progress_event_seq += 1
            self._progress = {
                **progress,
                "event_seq": self._progress_event_seq,
                "progress_changed_at": datetime.now(UTC).isoformat(),
            }
        # 锁只保护快照和序号；网络发送在锁外，避免慢心跳阻塞进度更新。
        if flush:
            self._send()


def _valid_block(text: str, bbox: list[float], confidence: float) -> bool:
    if not text.strip() or len(bbox) != 4 or not math.isfinite(confidence) or confidence < 0 or confidence > 1:
        return False
    if not all(math.isfinite(value) for value in bbox):
        return False
    return bbox[0] >= 0 and bbox[1] >= 0 and bbox[2] > 0 and bbox[3] > 0


def _decode_region_page(image_bytes: bytes) -> Any:
    """Decode once per page, sharing the RGB pixels across all answer regions."""
    try:
        from PIL import Image
        with Image.open(io.BytesIO(image_bytes)) as source:
            source.load()
            return source.convert("RGB")
    except ImportError as exc:
        raise ValueError("Pillow is required for ROI OCR") from exc
    except Exception as exc:
        raise ValueError("page decode failed") from exc


def _crop_region(source: Any, bbox: object) -> tuple[bytes, tuple[float, float]]:
    """Crop one answer_segment and return its actual integer page offset.

    Regions are deliberately validated strictly. A malformed segment fails the task
    instead of silently changing answer/evidence coordinates.
    """
    if not isinstance(bbox, (list, tuple)) or len(bbox) != 4:
        raise ValueError("bbox must be [x,y,w,h]")
    values = [float(value) for value in bbox]
    if not all(math.isfinite(value) for value in values):
        raise ValueError("bbox must be finite")
    x, y, width, height = values
    if x < 0 or y < 0 or width <= 0 or height <= 0:
        raise ValueError("bbox dimensions are invalid")
    image_width, image_height = source.size
    if x + width > image_width or y + height > image_height:
        raise ValueError("bbox exceeds page bounds")
    # Round outward to retain all pixels covered by fractional segment bounds.
    left, top = math.floor(x), math.floor(y)
    right, bottom = math.ceil(x + width), math.ceil(y + height)
    with source.crop((left, top, right, bottom)) as crop:
        output = io.BytesIO()
        crop.save(output, format="PNG")
        return output.getvalue(), (float(left), float(top))
