import hashlib
from contextlib import contextmanager
from types import SimpleNamespace
from unittest.mock import Mock

import pytest
from ocr_worker import math_runner
from ocr_worker.engine import OCRBlock
from ocr_worker.recognition_router import RegionKind, RoutedRecognitionResult
from ocr_worker.runner import WorkerConfig


@pytest.mark.parametrize("changed", [False, True])
def test_math_runner_never_labels_new_crop_bytes_with_old_input_hash(monkeypatch: pytest.MonkeyPatch, changed: bool) -> None:
    image = b"current-crop-bytes"
    expected = hashlib.sha256(b"old-crop-bytes" if changed else image).hexdigest()
    task = {"answer_segment_id": "segment", "exam_question_snapshot_id": "snapshot", "subject_code": "mathematics", "input_hash": expected, "region_kind": "text"}
    api = Mock()
    api.get_math_task_input.return_value = {"task": task, "image_url": "/crop"}
    api.download.return_value = image
    router = Mock()
    router.recognize.return_value = RoutedRecognitionResult(kind=RegionKind.TEXT, text_blocks=(OCRBlock("answer", [0, 0, 10, 10], .9),), image_size=(10, 10))

    @contextmanager
    def heartbeat(**_kwargs):
        yield SimpleNamespace(stop=Mock())

    monkeypatch.setattr(math_runner, "_LeaseHeartbeat", heartbeat)
    runner = math_runner.MathUnderstandingRunner(api=api, router=router, verifier=Mock(), config=WorkerConfig("worker-1"))
    runner._process({"id": "task-1", "lease_token": "lease-1", "source_type": "answer_segment"})
    if changed:
        router.recognize.assert_not_called()
        api.complete_math_task.assert_not_called()
        api.fail_runtime_task.assert_called_once()
    else:
        api.fail_runtime_task.assert_not_called()
        api.complete_math_task.assert_called_once()
        assert api.complete_math_task.call_args.args[2]["input_hash"] == expected
