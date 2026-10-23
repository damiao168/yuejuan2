import json
import unittest
from unittest.mock import patch

from edugrade_worker_runtime import ResponseValidationError
from ocr_worker.api import (
    PAPER_IMPORT_COMPLETION_TIMEOUT_SECONDS,
    APIError,
    EduGradeClient,
)


class FakeResponse:
    def __init__(self):
        self.headers = {"Content-Type": "application/json"}

    def __enter__(self):
        return self

    def __exit__(self, _exc_type, _exc, _traceback):
        return None

    def read(self, amount=-1):
        return b"{}"[:amount]


class APITests(unittest.TestCase):
    @patch("ocr_worker.api._open_request", return_value=FakeResponse())
    def test_heartbeat_renews_configured_lease_with_short_timeout(self, urlopen):
        client = EduGradeClient(
            base_url="http://api-gateway:8080",
            tenant_code="demo",
            username="ocr-worker",
            password="test-only",
            token="worker-token",
        )

        progress = {"stage": "text_ocr", "completed": 1, "total": 2}
        client.heartbeat_task("runtime-1", "lease-1", "worker-1", 300, 3.0, progress=progress)

        req = urlopen.call_args.args[0]
        payload = json.loads(req.data.decode("utf-8"))
        self.assertEqual(payload["lease_seconds"], 300)
        self.assertEqual(payload["state"], "running")
        self.assertEqual(payload["progress"], progress)
        self.assertEqual(urlopen.call_args.kwargs["timeout"], 3.0)

    def test_download_rejects_cross_origin_url_before_sending_worker_token(self):
        client = EduGradeClient("http://api-gateway:8080", "demo", "worker", "secret", token="worker-token")

        with self.assertRaisesRegex(APIError, "configured API origin") as caught:
            client.download("https://attacker.invalid/collect")
        self.assertFalse(caught.exception.retryable)

    @patch("ocr_worker.api._open_request", return_value=FakeResponse())
    def test_download_preserves_invalid_response_vs_transient_transport_boundary(self, _open):
        client = EduGradeClient("http://api-gateway:8080", "demo", "worker", "test-only", token="worker-token")
        for failure, retryable in ((ResponseValidationError("too large"), False), (OSError("reset"), True)):
            with self.subTest(failure=type(failure).__name__), patch("ocr_worker.api.read_bounded", side_effect=failure):
                with self.assertRaises(APIError) as caught:
                    client.download("/files/page/download")
                self.assertIs(caught.exception.retryable, retryable)

    @patch("ocr_worker.api._open_request", return_value=FakeResponse())
    def test_claimed_task_capability_is_sent_without_caller_selected_tenant(self, urlopen):
        client = EduGradeClient(
            base_url="http://api-gateway:8080",
            tenant_code="platform",
            username="ocr-worker",
            password="test-only",
            token="worker-token",
        )

        client.activate_task({"id": "runtime-1", "lease_token": "lease-1"}, "worker-1")
        client.start_task("task-1")

        req = urlopen.call_args.args[0]
        self.assertEqual(req.headers["X-edugrade-worker-task-id"], "runtime-1")
        self.assertEqual(req.headers["X-edugrade-worker-lease-token"], "lease-1")
        self.assertEqual(req.headers["X-edugrade-worker-service"], "ocr-worker")
        self.assertEqual(req.headers["X-edugrade-worker-instance-id"], "worker-1")
        self.assertNotIn("X-edugrade-tenant-id", req.headers)

    @patch("ocr_worker.api._open_request", return_value=FakeResponse())
    def test_paper_import_completion_allows_local_model_processing_budget(self, urlopen):
        client = EduGradeClient(
            base_url="http://api-gateway:8080",
            tenant_code="demo",
            username="ocr-worker",
            password="test-only",
            token="worker-token",
        )

        client.complete_paper_import_ocr("import-1", {"blocks": [{"text": "question"}]})

        self.assertEqual(
            urlopen.call_args.kwargs["timeout"],
            PAPER_IMPORT_COMPLETION_TIMEOUT_SECONDS,
        )


if __name__ == "__main__":
    unittest.main()
