import inspect
import io
import json
from types import SimpleNamespace
from unittest.mock import call, create_autospec, patch

import pytest

from ocr_worker.api import EduGradeClient
from ocr_worker.math_runner import MathUnderstandingRunner
from ocr_worker.paper_formula import PaperFormulaRunner
from ocr_worker.recognition_router import RecognitionRouter, RegionKind
from ocr_worker.runner import OCRRunner, WorkerConfig, _LeaseHeartbeat
from ocr_worker.symbolic_runner import SymbolicVerificationRunner


CLIENT_CALLS = [
    ("get_math_verification_input", ("task-51",)),
    ("complete_math_verification_task", ("task-51", {})),
    ("fail_math_verification_task", ("task-51", {})),
    ("get_math_task_input", ("runtime-51",)),
    ("complete_math_task", ("runtime-51", "lease-51", {}, 123)),
    ("fail_runtime_task", ("runtime-51", "lease-51", "failure", True, 123)),
    ("heartbeat_task", ("runtime-51", "lease-51", "worker-51", 300, 3.0)),
    ("start_task", ("task-51",)),
    ("get_task_input", ("task-51",)),
    ("complete_task", ("task-51", {})),
    ("fail_task", ("task-51", "failure", "runtime-51", "lease-51", True)),
    ("complete_paper_import_ocr", ("import-51", {})),
    ("complete_paper_import_formula", ("import-51", {})),
    ("fail_paper_import", ("import-51", "runtime-51", "lease-51", "failure", True)),
    ("download", ("/files/file-51/download",)),
    ("_request", ("GET", "/api/v1/ocr-tasks/task-51/input", None, True, 60)),
    ("_build_request", ("GET", "/api/v1/ocr-tasks/task-51/input", None, True)),
]


@pytest.mark.parametrize("method,args", CLIENT_CALLS)
def test_transport_signatures_reject_legacy_tenant_without_shifting_arguments(method, args):
    client = EduGradeClient("http://api-gateway:8080", "platform", "worker", "test-only")
    signature = inspect.signature(getattr(client, method))
    assert "tenant_id" not in signature.parameters
    signature.bind(*args)
    with pytest.raises(TypeError):
        signature.bind(*args, tenant_id="school-51")
    with pytest.raises(TypeError):
        signature.bind(*args, "school-51")


@pytest.mark.parametrize(
    "method,option", [("heartbeat_task", "progress"), ("fail_paper_import", "error_detail"), ("fail_paper_import", "duration_ms")],
)
def test_shift_sensitive_options_are_keyword_only(method, option):
    assert inspect.signature(getattr(EduGradeClient, method)).parameters[option].kind is inspect.Parameter.KEYWORD_ONLY


def test_heartbeat_wrapper_sends_five_core_arguments_and_named_progress():
    api = create_autospec(EduGradeClient, instance=True)
    heartbeat = _LeaseHeartbeat(
        api=api, runtime_task_id="runtime-51", lease_token="lease-51", worker_id="worker-51",
        interval=10, lease_seconds=300, request_timeout=3,
    )
    # 不启动线程；真实 wrapper 的发送仍由 client 签名约束，旧第六位会直接失败。
    heartbeat.set_progress({"stage": "text_ocr", "completed": 1, "total": 2}, flush=True)
    sent = api.heartbeat_task.call_args
    assert sent.args == ("runtime-51", "lease-51", "worker-51", 300, 3)
    assert set(sent.kwargs) == {"progress"}
    progress = sent.kwargs["progress"]
    assert progress['stage'] == 'text_ocr'
    assert progress['completed'] == 1
    assert progress['total'] == 2
    assert progress["event_seq"] == 1
    assert isinstance(progress["progress_changed_at"], str)
    api.heartbeat_task.assert_called_once_with(*sent.args, progress=progress)
    heartbeat._send()
    assert api.heartbeat_task.call_count == 2
    assert api.heartbeat_task.call_args == sent


def _claimed_case(kind, api):
    config = WorkerConfig(worker_id="worker-51")
    task = {
        "id": "runtime-51", "lease_token": "lease-51", "source_id": "source-51",
        "source_type": "answer_segment", "payload": {"engine": "paddleocr", "engine_version": "pp-ocrv5"},
    }
    if kind in {"ocr", "paper_ocr"}:
        if kind == "paper_ocr":
            task["source_type"] = "paper_import_job"
        runner = OCRRunner(api=api, engine=SimpleNamespace(), config=config)
        claim_name, claim_args = "claim_tasks", ("worker-51", 1, 300)
    elif kind == "paper_formula":
        runner = PaperFormulaRunner(
            api=api, config=config, detector=SimpleNamespace(), primary=SimpleNamespace(),
            fallback=SimpleNamespace(), validator=SimpleNamespace(),
        )
        claim_name, claim_args = "claim_paper_formula_tasks", ("worker-51", 300)
    elif kind == "symbolic":
        runner = SymbolicVerificationRunner(api=api, verifier=SimpleNamespace(), config=config)
        claim_name, claim_args = "claim_math_verification_tasks", ("worker-51", 300)
    else:
        assert kind == "math"
        runner = MathUnderstandingRunner(api=api, router=SimpleNamespace(), verifier=SimpleNamespace(), config=config)
        claim_name, claim_args = "claim_math_tasks", ("worker-51", 300)
    getattr(api, claim_name).return_value = [task]
    return runner, task, getattr(call, claim_name)(*claim_args)


@pytest.mark.parametrize("kind", ["ocr", "paper_ocr", "paper_formula", "symbolic"])
def test_missing_tenant_metadata_after_claim_does_not_activate_or_enter_processing(kind):
    api = create_autospec(EduGradeClient, instance=True)
    runner, task, claimed = _claimed_case(kind, api)
    assert "tenant_id" not in task
    # 缺字段在领取返回后抛出，不能进入激活、心跳、输入读取或成功/失败终态。
    with pytest.raises(KeyError) as caught:
        runner.process_once()
    assert caught.value.args == ("tenant_id",)
    assert api.mock_calls == [claimed]


@pytest.mark.parametrize("kind", ["ocr", "paper_ocr", "paper_formula", "symbolic"])
@pytest.mark.parametrize("value", [None, ""])
def test_present_empty_tenant_metadata_does_not_gain_a_new_validation_rule(kind, value):
    api = create_autospec(EduGradeClient, instance=True)
    runner, task, claimed = _claimed_case(kind, api)
    task["tenant_id"] = value
    activation = RuntimeError("activation reached")
    api.activate_task.side_effect = activation
    with pytest.raises(RuntimeError) as caught:
        runner.process_once()
    assert caught.value is activation
    assert api.mock_calls == [claimed, call.activate_task(task, "worker-51")]


def test_math_missing_tenant_metadata_keeps_activation_and_route_failure():
    api = create_autospec(EduGradeClient, instance=True)
    runner, task, claimed = _claimed_case("math", api)
    task["source_type"] = "invalid"
    assert runner.process_once() == 1
    assert api.mock_calls == [
        claimed, call.activate_task(task, "worker-51"),
        call.fail_runtime_task("runtime-51", "lease-51", "invalid_math_runtime_payload", False, 0),
    ]


def test_math_workflow_uses_strict_transport_calls_and_stops_before_completion():
    api = create_autospec(EduGradeClient, instance=True)
    runner, task, claimed = _claimed_case("math", api)
    task_input = {"subject_code": "mathematics", "region_kind": "formula"}
    api.get_math_task_input.return_value = {"task": task_input, "image_url": "/files/file-51/download"}
    api.download.return_value = b"image-51"
    routed = SimpleNamespace()
    router = create_autospec(RecognitionRouter, instance=True)
    router.recognize.return_value = routed
    runner.router = router
    artifact = {"input_hash": "hash-51"}
    with (
        patch("ocr_worker.math_runner._LeaseHeartbeat") as heartbeat_factory,
        patch("ocr_worker.math_runner._build_artifact", return_value=artifact) as build_artifact,
    ):
        heartbeat = heartbeat_factory.return_value.__enter__.return_value

        def complete(runtime_id, lease_token, result, duration_ms):
            heartbeat.stop.assert_called_once_with()
            assert (runtime_id, lease_token, result) == ("runtime-51", "lease-51", artifact)
            assert isinstance(duration_ms, int)
            assert duration_ms >= 0

        api.complete_math_task.side_effect = complete
        assert runner.process_once() == 1
        heartbeat_factory.assert_called_once_with(
            api=api, runtime_task_id="runtime-51", lease_token="lease-51", worker_id="worker-51",
            interval=10, lease_seconds=300, request_timeout=3,
        )
        build_artifact.assert_called_once_with(task_input, routed, runner.verifier)
    assert "tenant_id" not in task
    assert api.mock_calls[0] == claimed
    api.activate_task.assert_called_once_with(task, "worker-51")
    api.get_math_task_input.assert_called_once_with("runtime-51")
    api.download.assert_called_once_with("/files/file-51/download")
    router.recognize.assert_called_once_with("mathematics", RegionKind.FORMULA, b"image-51")
    api.complete_math_task.assert_called_once()
    api.fail_runtime_task.assert_not_called()


def test_paper_failure_keeps_capability_headers_and_named_detail_duration():
    client = EduGradeClient("http://api-gateway:8080", "platform", "worker", "test-only", token="worker-token")
    client.activate_task({"id": "runtime-51", "lease_token": "lease-51", "tenant_id": "school-51"}, "worker-51")
    detail = {"error_type": "ValueError", "message": "invalid formula"}
    response = io.BytesIO(b"{}")
    response.headers = {"Content-Type": "application/json"}
    with patch("ocr_worker.api._open_request", return_value=response) as urlopen:
        client.fail_paper_import("import-51", "runtime-51", "lease-51", "failure", True, error_detail=detail, duration_ms=123)
    req = urlopen.call_args.args[0]
    assert req.get_method() == "POST"
    assert req.full_url == "http://api-gateway:8080/api/v1/internal/paper-imports/import-51/failure"
    assert json.loads(req.data.decode()) == {
        "task_id": "runtime-51", "lease_token": "lease-51", "retryable": True,
        "error_code": "failure", "error_detail": detail, "duration_ms": 123,
    }
    assert urlopen.call_args.kwargs == {"timeout": 60}
    assert req.headers["Authorization"] == "Bearer worker-token"
    assert req.headers["X-edugrade-worker-task-id"] == "runtime-51"
    assert req.headers["X-edugrade-worker-lease-token"] == "lease-51"
    assert req.headers["X-edugrade-worker-service"] == "ocr-worker"
    assert req.headers["X-edugrade-worker-instance-id"] == "worker-51"
    assert "X-edugrade-tenant-id" not in req.headers
