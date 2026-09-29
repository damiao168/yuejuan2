from __future__ import annotations

import hashlib
import io

import pytest
from page_processing.config import Config
from page_processing.omr import OMRExtractionError
from page_processing.runner import Runner
from PIL import Image, ImageDraw


class FakeClient:
    def __init__(self, image: bytes, reference: bytes | None = None) -> None:
        self.image = image
        self.reference = reference
        self.completed: tuple[str, dict] | None = None

    def heartbeat(self, task: dict, worker_id: str, lease_seconds: int, timeout: float | None = None, progress: dict | None = None) -> None:
        assert task["lease_token"] == "lease-1"

    def download(self, path: str) -> bytes:
        if path == "/api/v1/answer-segments/segment-1/image":
            return self.image
        if path == "/api/v1/files/reference-1/download" and self.reference is not None:
            return self.reference
        raise AssertionError(f"unexpected download path: {path}")

    def upload_asset(self, task: dict, owner_type: str, owner_id: str, filename: str, data: bytes) -> dict:
        assert owner_type == "omr_evidence"
        assert owner_id == "run-1"
        assert filename == "omr-overlay.png"
        assert data.startswith(b"\x89PNG")
        return {"id": "overlay-1", "hash_sha256": "overlay-hash"}

    def complete_omr(self, run_id: str, payload: dict) -> None:
        self.completed = (run_id, payload)


def _sheet(marked: str | None = "B", printed: str | None = None) -> bytes:
    image = Image.new("RGB", (160, 70), "white")
    draw = ImageDraw.Draw(image)
    for label, x in (("A", 20), ("B", 70), ("C", 120)):
        draw.rectangle((x, 20, x + 28, 48), outline="black", width=2)
        if label == printed:
            draw.rectangle((x + 7, 27, x + 21, 41), fill="black")
        if label == marked:
            draw.ellipse((x + 6, 26, x + 22, 42), fill="black")
    output = io.BytesIO()
    image.save(output, "PNG")
    return output.getvalue()


def test_runner_executes_omr_and_returns_evidence() -> None:
    client = FakeClient(_sheet())
    runner = Runner(client, Config("http://api", "demo", "worker", "secret", "worker-1"))
    task = {
        "id": "task-1", "lease_token": "lease-1", "task_type": "omr_extract",
        "payload": {
            "exam_id": "exam-1", "omr_run_id": "run-1",
            "source_download_url": "/api/v1/answer-segments/segment-1/image",
            "profile_hash": "sha256:manual-only-test-profile",
            "option_regions": [
                {"label": "A", "x": 20, "y": 20, "width": 28, "height": 28},
                {"label": "B", "x": 70, "y": 20, "width": 28, "height": 28},
                {"label": "C", "x": 120, "y": 20, "width": 28, "height": 28},
            ],
        },
    }
    runner._process_omr(task)
    assert client.completed is not None
    run_id, result = client.completed
    assert run_id == "run-1"
    assert result["decision"] == "selected"
    assert result["selected"] == ["B"]
    assert result["profile_hash"] == "sha256:manual-only-test-profile"
    assert result["overlay_file_asset_id"] == "overlay-1"


def test_runner_rejects_omr_task_without_server_profile_hash() -> None:
    client = FakeClient(_sheet())
    runner = Runner(client, Config("http://api", "demo", "worker", "secret", "worker-1"))
    task = {
        "id": "task-1", "lease_token": "lease-1", "task_type": "omr_extract",
        "payload": {
            "exam_id": "exam-1", "omr_run_id": "run-1",
            "source_download_url": "/api/v1/answer-segments/segment-1/image",
            "option_regions": [{"label": "A", "x": 20, "y": 20, "width": 28, "height": 28}],
        },
    }
    with pytest.raises(OMRExtractionError, match="omr_profile_hash_missing"):
        runner._process_omr(task)


def test_runner_uses_verified_template_reference_for_difference_profile() -> None:
    reference = _sheet(None, printed="A")
    client = FakeClient(_sheet("B", printed="A"), reference)
    runner = Runner(client, Config("http://api", "demo", "worker", "secret", "worker-1"))
    task = {
        "id": "task-1", "lease_token": "lease-1", "task_type": "omr_extract",
        "payload": {
            "exam_id": "exam-1", "omr_run_id": "run-1",
            "source_download_url": "/api/v1/answer-segments/segment-1/image",
            "profile_hash": "sha256:template-difference-test-profile",
            "profile": {
                "mode": "template_difference", "version": "opencv-template-difference-bubble-v1",
                "marked_threshold": 0.12, "ambiguous_threshold": 0.04, "minimum_margin": 0.04,
                "border_fraction": 0.12, "reference_mask_dilation_pixels": 1,
            },
            "reference": {
                "file_asset_id": "reference-1", "download_url": "/api/v1/files/reference-1/download",
                "sha256": hashlib.sha256(reference).hexdigest(), "content_type": "image/png", "page_no": 1,
                "question_region": {"x": 0, "y": 0, "width": 1, "height": 1},
            },
            "option_regions": [
                {"label": "A", "x": 20, "y": 20, "width": 28, "height": 28},
                {"label": "B", "x": 70, "y": 20, "width": 28, "height": 28},
                {"label": "C", "x": 120, "y": 20, "width": 28, "height": 28},
            ],
        },
    }
    runner._process_omr(task)
    assert client.completed is not None
    _, result = client.completed
    assert result["selected"] == ["B"]
    assert result["reference_sha256"] == hashlib.sha256(reference).hexdigest()
    assert result["measurements"][0]["source_fill_ratio"] > 0
    assert result["measurements"][0]["foreground_delta"] == 0


# 人为增加参考图一个像素，保护配准取整差异不会把印刷痕迹误当作学生填涂。
def test_runner_aligns_minor_reference_crop_rounding_difference() -> None:
    source = _sheet("B", printed="A")
    with Image.open(io.BytesIO(_sheet(None, printed="A"))) as image:
        wider = image.resize((image.width + 1, image.height))
        output = io.BytesIO()
        wider.save(output, "PNG")
        reference = output.getvalue()
    client = FakeClient(source, reference)
    runner = Runner(client, Config("http://api", "demo", "worker", "secret", "worker-1"))
    task = {
        "id": "task-1", "lease_token": "lease-1", "task_type": "omr_extract",
        "payload": {
            "exam_id": "exam-1", "omr_run_id": "run-1",
            "source_download_url": "/api/v1/answer-segments/segment-1/image",
            "profile_hash": "sha256:template-difference-test-profile",
            "profile": {
                "mode": "template_difference", "version": "opencv-template-difference-bubble-v1",
                "marked_threshold": 0.12, "ambiguous_threshold": 0.04, "minimum_margin": 0.04,
                "border_fraction": 0.12, "reference_mask_dilation_pixels": 1,
            },
            "reference": {
                "file_asset_id": "reference-1", "download_url": "/api/v1/files/reference-1/download",
                "sha256": hashlib.sha256(reference).hexdigest(), "content_type": "image/png", "page_no": 1,
                "question_region": {"x": 0, "y": 0, "width": 1, "height": 1},
            },
            "option_regions": [
                {"label": "A", "x": 20, "y": 20, "width": 28, "height": 28},
                {"label": "B", "x": 70, "y": 20, "width": 28, "height": 28},
                {"label": "C", "x": 120, "y": 20, "width": 28, "height": 28},
            ],
        },
    }

    runner._process_omr(task)

    assert client.completed is not None
    assert client.completed[1]["selected"] == ["B"]


def test_runner_rejects_template_reference_hash_mismatch() -> None:
    client = FakeClient(_sheet("B"), _sheet(None))
    runner = Runner(client, Config("http://api", "demo", "worker", "secret", "worker-1"))
    task = {
        "id": "task-1", "lease_token": "lease-1", "task_type": "omr_extract",
        "payload": {
            "exam_id": "exam-1", "omr_run_id": "run-1",
            "source_download_url": "/api/v1/answer-segments/segment-1/image",
            "profile_hash": "sha256:template-difference-test-profile",
            "profile": {"mode": "template_difference", "version": "opencv-template-difference-bubble-v1"},
            "reference": {"file_asset_id": "reference-1", "download_url": "/api/v1/files/reference-1/download", "sha256": "wrong", "content_type": "image/png", "page_no": 1, "question_region": {"x": 0, "y": 0, "width": 1, "height": 1}},
            "option_regions": [{"label": "A", "x": 20, "y": 20, "width": 28, "height": 28}],
        },
    }
    with pytest.raises(OMRExtractionError, match="omr_reference_hash_mismatch"):
        runner._process_omr(task)
