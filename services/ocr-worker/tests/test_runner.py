import io
import threading
import time
import unittest
from unittest.mock import patch

from ocr_worker.api import APIError, AuthenticationError
from ocr_worker.engine import OCRBlock
from ocr_worker.runner import (
    OCRRunner,
    WorkerConfig,
    _decode_region_page,
    _LeaseHeartbeat,
)
from PIL import Image


class FakeAPI:
    def __init__(self):
        self.started = []
        self.completed = []
        self.failed = []
        self.downloads = {}
        self.pending_error = None
        self.heartbeats = []
        self.heartbeat_progress = []
        self.runtime_failed = []
        self.paper_completed = []
        self.paper_failed = []

    def claim_tasks(self, worker_instance_id, limit, lease_seconds):
        if self.pending_error:
            raise self.pending_error
        return [{
            "id": "runtime-1",
            "tenant_id": "tenant-1",
            "source_id": "task-1",
            "lease_token": "lease-1",
            "payload": {
                "ocr_task_id": "task-1",
                "engine": "paddleocr",
                "engine_version": "pp-ocrv5",
            },
        }]

    def heartbeat_task(self, runtime_task_id, lease_token, worker_instance_id, lease_seconds, timeout_seconds, tenant_id=None, progress=None):
        self.heartbeats.append((runtime_task_id, lease_token, worker_instance_id, lease_seconds, timeout_seconds))
        self.heartbeat_progress.append(progress)

    def start_task(self, task_id, tenant_id=None):
        self.started.append(task_id)

    def get_task_input(self, task_id, tenant_id=None):
        return {"pages": [{"id": "page-1", "download_url": "/files/file-1/download", "file_asset_id": "file-1"}]}

    def download(self, url, tenant_id=None):
        value = self.downloads.get(url, b"image")
        if isinstance(value, Exception):
            raise value
        return value

    def complete_task(self, task_id, payload, tenant_id=None):
        self.completed.append((task_id, payload))

    def fail_task(self, task_id, message, runtime_task_id, lease_token, retryable, tenant_id=None):
        self.failed.append((task_id, message))
        self.runtime_failed.append((runtime_task_id, lease_token, message, retryable, 0))

    def complete_paper_import_ocr(self, import_id, payload, tenant_id=None):
        self.paper_completed.append((import_id, payload))

    def fail_paper_import(self, import_id, runtime_task_id, lease_token, error_code, retryable, tenant_id=None):
        self.paper_failed.append((import_id, error_code, retryable))


class FakeEngine:
    model_version = "ppocr-v5-server"
    device = "cpu"

    def __init__(self, blocks):
        self.blocks = blocks

    def initialize(self):
        pass

    def recognize(self, image_bytes):
        return self.blocks


class RunnerTests(unittest.TestCase):
    def test_progress_events_distinguish_real_change_from_periodic_heartbeat(self):
        api = FakeAPI()
        heartbeat = _LeaseHeartbeat(
            api=api,
            runtime_task_id="runtime-1",
            lease_token="lease-1",
            worker_id="worker-a",
            interval=10,
            lease_seconds=300,
            request_timeout=3,
            tenant_id="tenant-1",
        )

        heartbeat.set_progress({"stage": "formula_recognition", "completed": 1, "total": 2}, flush=True)
        heartbeat._send()
        heartbeat.set_progress({"stage": "formula_recognition", "completed": 2, "total": 2}, flush=True)

        self.assertEqual([item["event_seq"] for item in api.heartbeat_progress], [1, 1, 2])
        self.assertEqual(api.heartbeat_progress[0]["progress_changed_at"], api.heartbeat_progress[1]["progress_changed_at"])

    def test_multiple_fractional_regions_decode_once_and_preserve_pixel_offsets(self):
        api = FakeAPI()
        encoded = io.BytesIO()
        Image.new("RGB", (100, 80), (255, 0, 0)).save(encoded, format="PNG")
        api.downloads["/files/file-1/download"] = encoded.getvalue()
        api.get_task_input = lambda *_: {"pages": [{"id": "page-1", "download_url": "/files/file-1/download", "regions": [
            {"bbox": [10.5, 20.25, 30, 20]}, {"bbox": [50.25, 5.5, 10, 10]},
        ]}]}
        crops = []

        class RegionEngine(FakeEngine):
            def recognize(self, image_bytes):
                with Image.open(io.BytesIO(image_bytes)) as crop:
                    crops.append((crop.size, crop.getpixel((0, 0))))
                return [OCRBlock(text="answer", bbox=[2, 3, 5, 5], confidence=0.9)]

        runner = OCRRunner(api=api, engine=RegionEngine([]), config=WorkerConfig(worker_id="worker-a"))
        with patch("ocr_worker.runner._decode_region_page", wraps=_decode_region_page) as decode:
            runner.process_once()
        self.assertEqual(decode.call_count, 1)
        self.assertEqual(crops, [((31, 21), (255, 0, 0)), ((11, 11), (255, 0, 0))])
        self.assertEqual([row["bbox"] for row in api.completed[0][1]["results"]], [[12, 23, 5, 5], [52, 8, 5, 5]])

    def test_invalid_region_geometry_rejects_before_inference(self):
        encoded = io.BytesIO()
        Image.new("RGB", (100, 80), "white").save(encoded, format="PNG")
        for bbox in ([90, 70, 30, 20], [0, 0, 0, 2], [0, 0, -1, 2], [float("nan"), 0, 2, 2], [0, 0, float("inf"), 2]):
            with self.subTest(bbox=bbox):
                api = FakeAPI()
                api.downloads["/files/file-1/download"] = encoded.getvalue()
                api.get_task_input = lambda *_, region_bbox=bbox: {"pages": [{"id": "page-1", "download_url": "/files/file-1/download", "regions": [{"bbox": region_bbox}]}]}
                engine = FakeEngine([])
                with patch.object(engine, "recognize") as recognize:
                    OCRRunner(api=api, engine=engine, config=WorkerConfig(worker_id="worker-a")).process_once()
                recognize.assert_not_called()
                self.assertEqual(api.failed, [("task-1", "invalid_ocr_region")])
                self.assertEqual(api.completed, [])

    def test_roi_ocr_translates_crop_bbox_to_page_coordinates(self):
        api = FakeAPI()
        source = Image.new("RGB", (100, 80), "white")
        encoded = io.BytesIO(); source.save(encoded, format="PNG")
        api.downloads["/files/file-1/download"] = encoded.getvalue()
        api.get_task_input = lambda *_: {"pages": [{"id": "page-1", "download_url": "/files/file-1/download", "regions": [{"bbox": [10, 20, 30, 20]}]}]}
        class RegionEngine(FakeEngine):
            def recognize(self, image_bytes):
                self.seen_size = Image.open(io.BytesIO(image_bytes)).size
                return [OCRBlock(text="answer", bbox=[2, 3, 10, 5], confidence=0.9)]
        engine = RegionEngine([])
        runner = OCRRunner(api=api, engine=engine, config=WorkerConfig(worker_id="worker-a"))
        runner.process_once()
        self.assertEqual(engine.seen_size, (30, 20))
        self.assertEqual(api.completed[0][1]["results"][0]["bbox"], [12.0, 23.0, 10, 5])

    def test_invalid_roi_fails_closed(self):
        api = FakeAPI()
        api.get_task_input = lambda *_: {"pages": [{"id": "page-1", "download_url": "/files/file-1/download", "regions": [{"bbox": [90, 70, 30, 20]}]}]}
        runner = OCRRunner(api=api, engine=FakeEngine([OCRBlock(text="must not run", bbox=[1, 2, 3, 4], confidence=0.9)]), config=WorkerConfig(worker_id="worker-a"))
        runner.process_once()
        self.assertEqual(api.failed, [("task-1", "invalid_ocr_region")])

    def test_paper_import_pages_use_existing_ocr_engine(self):
        api = FakeAPI()
        api.claim_tasks = lambda *_: [{
            "id": "runtime-paper", "tenant_id": "tenant-1", "source_id": "import-1",
            "source_type": "paper_import_job", "lease_token": "lease-paper",
            "payload": {"engine": "paddleocr", "engine_version": "pp-ocrv5", "pages": [
                {"source_id": "source-paper", "document_index": 0, "page_no": 1, "download_url": "/files/paper-page"},
                {"source_id": "source-answer", "document_index": 1, "page_no": 1, "download_url": "/files/answer-page"},
            ]},
        }]
        runner = OCRRunner(api=api, engine=FakeEngine([OCRBlock(text="Q1 answer", bbox=[1, 2, 30, 10], confidence=0.7)]), config=WorkerConfig(worker_id="worker-a"))
        self.assertEqual(runner.process_once(), 1)
        self.assertEqual(api.paper_failed, [])
        import_id, payload = api.paper_completed[0]
        self.assertEqual(import_id, "import-1")
        self.assertEqual({block["source_id"] for block in payload["blocks"]}, {"source-paper", "source-answer"})
        self.assertEqual([block["document_index"] for block in payload["blocks"]], [0, 1])
        self.assertEqual(payload["blocks"][0]["confidence"], 0.7)

    def test_incompatible_runtime_task_is_rejected_before_start(self):
        api = FakeAPI()
        original_claim = api.claim_tasks

        def incompatible_claim(worker_instance_id, limit, lease_seconds):
            tasks = original_claim(worker_instance_id, limit, lease_seconds)
            tasks[0]["payload"]["engine"] = "other-engine"
            return tasks

        api.claim_tasks = incompatible_claim
        engine = FakeEngine([OCRBlock(text="must not run", bbox=[1, 2, 30, 10], confidence=0.9)])
        runner = OCRRunner(api=api, engine=engine, config=WorkerConfig(worker_id="worker-a"))

        self.assertEqual(runner.process_once(), 1)

        self.assertEqual(api.started, [])
        self.assertEqual(api.heartbeats, [])
        self.assertEqual(api.completed, [])
        self.assertEqual(api.runtime_failed[0][2:4], ("unsupported_ocr_engine", False))

    def test_missing_runtime_capability_is_terminally_rejected(self):
        api = FakeAPI()
        original_claim = api.claim_tasks

        def invalid_claim(worker_instance_id, limit, lease_seconds):
            tasks = original_claim(worker_instance_id, limit, lease_seconds)
            tasks[0]["payload"] = {"ocr_task_id": "task-1"}
            return tasks

        api.claim_tasks = invalid_claim
        runner = OCRRunner(api=api, engine=FakeEngine([]), config=WorkerConfig(worker_id="worker-a"))

        self.assertEqual(runner.process_once(), 1)
        self.assertEqual(api.started, [])
        self.assertEqual(api.runtime_failed[0][2:4], ("invalid_ocr_runtime_payload", False))

    def test_low_confidence_result_is_completed_for_human_review_gate(self):
        api = FakeAPI()
        engine = FakeEngine([OCRBlock(text="student answer", bbox=[1, 2, 30, 10], confidence=0.52)])
        runner = OCRRunner(api=api, engine=engine, config=WorkerConfig(worker_id="worker-a", batch_size=1))

        processed = runner.process_once()

        self.assertEqual(processed, 1)
        self.assertEqual(api.started, ["task-1"])
        self.assertEqual(api.heartbeats, [("runtime-1", "lease-1", "worker-a", 300, 3.0)])
        self.assertEqual(api.failed, [])
        task_id, payload = api.completed[0]
        self.assertEqual(task_id, "task-1")
        self.assertEqual(payload["worker_id"], "worker-a")
        self.assertEqual(payload["model_version"], engine.model_version)
        self.assertEqual(payload["results"][0]["confidence"], 0.52)
        self.assertEqual(payload["runtime_task_id"], "runtime-1")

    def test_empty_ocr_result_fails_task(self):
        api = FakeAPI()
        runner = OCRRunner(api=api, engine=FakeEngine([]), config=WorkerConfig(worker_id="worker-a", batch_size=1))

        processed = runner.process_once()

        self.assertEqual(processed, 1)
        self.assertEqual(api.completed, [])
        self.assertEqual(api.failed, [("task-1", "empty_ocr_result")])
        self.assertEqual(api.runtime_failed[0][2:4], ("empty_ocr_result", False))

    def test_download_failure_fails_task(self):
        api = FakeAPI()
        api.downloads["/files/file-1/download"] = APIError("download failed")
        engine = FakeEngine([OCRBlock(text="never used", bbox=[1, 2, 30, 10], confidence=0.9)])
        runner = OCRRunner(api=api, engine=engine, config=WorkerConfig(worker_id="worker-a", batch_size=1))

        processed = runner.process_once()

        self.assertEqual(processed, 1)
        self.assertEqual(api.completed, [])
        self.assertEqual(api.failed, [("task-1", "download_failed")])
        self.assertEqual(api.runtime_failed[0][2:4], ("download_failed", True))

    def test_authentication_failure_stops_poll_cycle(self):
        api = FakeAPI()
        api.pending_error = AuthenticationError("unauthorized")
        engine = FakeEngine([OCRBlock(text="unused", bbox=[1, 2, 30, 10], confidence=0.9)])
        runner = OCRRunner(api=api, engine=engine, config=WorkerConfig(worker_id="worker-a", batch_size=1))

        with self.assertRaises(AuthenticationError):
            runner.process_once()
        self.assertEqual(api.started, [])
        self.assertEqual(api.completed, [])
        self.assertEqual(api.failed, [])

    def test_engine_failure_is_reported_as_retryable(self):
        class FailingEngine:
            model_version = "ppocr-v5-server"
            device = "cpu"

            def recognize(self, image_bytes):
                raise RuntimeError("engine unavailable")

        api = FakeAPI()
        runner = OCRRunner(api=api, engine=FailingEngine(), config=WorkerConfig(worker_id="worker-a", batch_size=1))

        processed = runner.process_once()

        self.assertEqual(processed, 1)
        self.assertEqual(api.completed, [])
        self.assertEqual(api.runtime_failed[0][2:4], ("ocr_engine_failed", True))

    def test_long_running_ocr_renews_lease_while_engine_is_busy(self):
        api = FakeAPI()

        class WaitingEngine:
            model_version = "ppocr-v5-server"
            device = "cpu"

            def recognize(self, image_bytes):
                deadline = time.monotonic() + 1.0
                while len(api.heartbeats) < 2 and time.monotonic() < deadline:
                    time.sleep(0.01)
                return [OCRBlock(text="answer", bbox=[1, 2, 30, 10], confidence=0.9)]

        runner = OCRRunner(
            api=api,
            engine=WaitingEngine(),
            config=WorkerConfig(worker_id="worker-a", batch_size=1, heartbeat_interval=0.1),
        )

        processed = runner.process_once()

        self.assertEqual(processed, 1)
        self.assertGreaterEqual(len(api.heartbeats), 2)
        self.assertEqual(len(api.completed), 1)

    # 故意让第二次心跳阻塞而推理结束，验证终态写入会等待在途心跳退出。
    def test_heartbeat_stops_before_terminal_completion(self):
        class BlockingHeartbeatAPI(FakeAPI):
            def __init__(self):
                super().__init__()
                self.second_heartbeat_started = threading.Event()
                self.heartbeat_active = threading.Event()
                self.terminal_while_heartbeat_active = False

            def heartbeat_task(self, runtime_task_id, lease_token, worker_instance_id, lease_seconds, timeout_seconds, tenant_id=None, progress=None):
                super().heartbeat_task(
                    runtime_task_id,
                    lease_token,
                    worker_instance_id,
                    lease_seconds,
                    timeout_seconds,
                    tenant_id,
                    progress,
                )
                if len(self.heartbeats) != 2:
                    return
                self.heartbeat_active.set()
                self.second_heartbeat_started.set()
                time.sleep(0.15)
                self.heartbeat_active.clear()

            def complete_task(self, task_id, payload, tenant_id=None):
                self.terminal_while_heartbeat_active = self.heartbeat_active.is_set()
                super().complete_task(task_id, payload, tenant_id)

        class FinishWhileHeartbeatIsBlocked(FakeEngine):
            def recognize(self, image_bytes):
                if not api.second_heartbeat_started.wait(timeout=1):
                    raise AssertionError("periodic heartbeat did not start")
                return self.blocks

        api = BlockingHeartbeatAPI()
        runner = OCRRunner(
            api=api,
            engine=FinishWhileHeartbeatIsBlocked([OCRBlock(text="answer", bbox=[1, 2, 30, 10], confidence=0.9)]),
            config=WorkerConfig(
                worker_id="worker-a",
                batch_size=1,
                heartbeat_interval=0.05,
                heartbeat_timeout=0.2,
            ),
        )

        runner.process_once()

        self.assertEqual(len(api.completed), 1)
        self.assertFalse(api.terminal_while_heartbeat_active)
        heartbeat_count = len(api.heartbeats)
        time.sleep(0.1)
        self.assertEqual(len(api.heartbeats), heartbeat_count)

    def test_periodic_heartbeat_failure_never_submits_terminal_state(self):
        class FailingHeartbeatAPI(FakeAPI):
            def __init__(self):
                super().__init__()
                self.periodic_attempted = threading.Event()

            def heartbeat_task(self, runtime_task_id, lease_token, worker_instance_id, lease_seconds, timeout_seconds, tenant_id=None, progress=None):
                super().heartbeat_task(
                    runtime_task_id,
                    lease_token,
                    worker_instance_id,
                    lease_seconds,
                    timeout_seconds,
                    tenant_id,
                    progress,
                )
                if len(self.heartbeats) >= 2:
                    self.periodic_attempted.set()
                    raise APIError("heartbeat unavailable", status_code=409)

        class WaitForFailedHeartbeat(FakeEngine):
            def recognize(self, image_bytes):
                if not api.periodic_attempted.wait(timeout=1):
                    raise AssertionError("periodic heartbeat was not attempted")
                return self.blocks

        api = FailingHeartbeatAPI()
        runner = OCRRunner(
            api=api,
            engine=WaitForFailedHeartbeat([OCRBlock(text="answer", bbox=[1, 2, 30, 10], confidence=0.9)]),
            config=WorkerConfig(worker_id="worker-a", heartbeat_interval=0.05, heartbeat_timeout=0.2),
        )

        with self.assertRaisesRegex(APIError, "heartbeat unavailable"):
            runner.process_once()

        self.assertEqual(api.completed, [])
        self.assertEqual(api.failed, [])

    def test_sequential_worker_rejects_batch_claims(self):
        with self.assertRaisesRegex(ValueError, "exactly one"):
            WorkerConfig(worker_id="worker-a", batch_size=2)

    def test_heartbeat_interval_must_fit_inside_lease(self):
        with self.assertRaisesRegex(ValueError, "shorter than lease_seconds"):
            WorkerConfig(worker_id="worker-a", lease_seconds=30, heartbeat_interval=30, heartbeat_timeout=1)

    def test_invalid_nan_result_is_not_submitted(self):
        api = FakeAPI()
        engine = FakeEngine([OCRBlock(text="invalid", bbox=[1, 2, 30, 10], confidence=float("nan"))])
        runner = OCRRunner(api=api, engine=engine, config=WorkerConfig(worker_id="worker-a", batch_size=1))

        runner.process_once()

        self.assertEqual(api.completed, [])
        self.assertEqual(api.runtime_failed[0][2:4], ("empty_ocr_result", False))


if __name__ == "__main__":
    unittest.main()
