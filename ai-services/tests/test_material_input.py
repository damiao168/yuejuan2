import base64
import copy
import hashlib
import io
import json
import threading
import unittest
import zlib
from concurrent.futures import ThreadPoolExecutor
from contextlib import contextmanager
from dataclasses import replace
from decimal import Decimal
from pathlib import Path
from unittest.mock import patch

from PIL import Image
from grading_agent.app_v2 import GradingAgentV2ApplicationSeam, OfflineV2FixtureAdapter, ProductionMathV2Application, math_candidate_messages
from grading_agent.contract_v2 import compute_reference_media_binding_hash, compute_request_binding_hash, validate_request_v2, validate_response_v2
from grading_agent.errors import AgentError
from grading_agent.material_runtime import MaterialDispatch, math_wrapper_hash
from grading_agent.model import LocalLlamaCppAdapter
from helpers import settings
from jsonschema import Draft202012Validator
from referencing import Registry, Resource


def material_request():
    fixture = Path(__file__).resolve().parents[2] / "contracts/grading-agent/v2/fixtures/valid-request.json"
    request = json.loads(fixture.read_text(encoding="utf-8"))
    raw = io.BytesIO()
    Image.new("RGB", (16, 12), "white").save(raw, "PNG")
    data = raw.getvalue()
    request["required_features"] = ["material-input-v1"]
    request["reference_manifest"] = {"mode": "reference_images", "manifest_hash": "a" * 64,
        "assessment_snapshot_id": request["math_evidence"]["exam_question_snapshot_id"], "assessment_snapshot_hash": "b" * 64,
        "required_media_ids": ["ref-diagram", "ref-solution"]}
    request["reference_media"] = []
    for index, role in enumerate(("question_diagram", "reference_solution")):
        media = {"media_id": request["reference_manifest"]["required_media_ids"][index], "role": role, "ordinal": index + 1,
                 "encoding": "base64", "media_type": "image/png", "sha256": hashlib.sha256(data).hexdigest(),
                 "byte_size": len(data), "width_pixels": 16, "height_pixels": 12, "data_base64": base64.b64encode(data).decode()}
        media["binding_hash"] = compute_reference_media_binding_hash(request, media)
        request["reference_media"].append(media)
    request["request_binding_hash"] = compute_request_binding_hash(request)
    request["execution_permit"] = {"run_id": "run-1", "permit_id": "permit-1", "execution_attempt_id": "attempt-1",
        "credential": "11111111-1111-1111-1111-111111111111:" + "f" * 64}
    request["math_wrapper_hash"] = math_wrapper_hash()
    return request


class MaterialInputTests(unittest.TestCase):
    def test_binding_vectors_match_go_length_prefixed_identity(self):
        # Go material_input_test.go 固定同一组期望值；角色、快照或字段顺序漂移不能由两端各自重算掩盖。
        request = {"request_id": "subjective-material-vector-0001", "question_id": "question:1", "answer_segment_id": "segment.1",
                   "reference_manifest": {"manifest_hash": "a" * 64, "assessment_snapshot_id": "snapshot-1", "assessment_snapshot_hash": "b" * 64},
                   "media_evidence": {"sha256": "c" * 64},
                   "reference_media": [{"media_id": "ref-diagram", "role": "question_diagram", "ordinal": 1, "sha256": "d" * 64}]}
        self.assertEqual(compute_reference_media_binding_hash(request, request["reference_media"][0]),
                         "6f6810a9db1fdea1a7bb98adb92a53b4b4e5677ff93edfd97b6bd800609c2bfe")
        self.assertEqual(compute_request_binding_hash(request),
                         "0140eb7f168a21455b2a41bb8445992822d6dc694c5c8188e31c83c4995174ea")

    def test_extension_schema_keeps_legacy_single_image_shape(self):
        root = Path(__file__).resolve().parents[2] / "contracts/grading-agent"
        schema = json.loads((root / "v2/request.schema.json").read_text(encoding="utf-8"), parse_float=Decimal)
        previous = json.loads((root / "v1/request.schema.json").read_text(encoding="utf-8"), parse_float=Decimal)
        registry = Registry().with_resource(previous["$id"], Resource.from_contents(previous))
        validator = Draft202012Validator(schema, registry=registry)
        request = material_request()
        self.assertEqual(list(validator.iter_errors(json.loads(json.dumps(request), parse_float=Decimal))), [])
        legacy = json.loads((root / "v2/fixtures/valid-request.json").read_text(encoding="utf-8"), parse_float=Decimal)
        self.assertEqual(list(validator.iter_errors(legacy)), [])
        for key in ("required_features", "reference_manifest", "reference_media", "request_binding_hash", "math_wrapper_hash", "execution_permit"):
            partial = copy.deepcopy(legacy)
            partial[key] = request[key]
            with self.subTest(field=key):
                self.assertTrue(list(validator.iter_errors(partial)))
        metadata = json.loads((root / "v2/contract.json").read_text(encoding="utf-8"))
        self.assertEqual(metadata["allowed_media_count"], 1)
        extension = metadata["optional_extensions"]["material-input-v1"]
        self.assertEqual(extension["maximum_total_media_count"], 5)
        self.assertEqual(extension["maximum_provider_requests_per_permit"], 1)
        self.assertFalse(extension["automatic_repair_allowed"])
        self.assertFalse(extension["production_enablement_verified"])

    def test_frozen_order_bindings_and_independent_reference_images(self):
        request = material_request()
        self.assertIs(validate_request_v2(request), request)
        messages = math_candidate_messages(request)
        content = messages[1]["content"]
        self.assertEqual(sum(part["type"] == "image_url" for part in content), 3)
        labels = [part["text"] for part in content if part["type"] == "text"][1:]
        self.assertIn("question_diagram", labels[0])
        self.assertIn("reference_solution", labels[1])
        self.assertIn("Student answer", labels[2])
        self.assertNotIn(request["execution_permit"]["credential"], json.dumps(messages))

    def test_partial_extension_duplicate_or_changed_identity_is_rejected(self):
        for mutate in (
            lambda r: r.pop("reference_media"),
            lambda r: r["reference_media"].reverse(),
            lambda r: r["reference_media"][0].update(sha256="0" * 64),
            lambda r: r["reference_manifest"].update(required_media_ids=["ref-diagram", "ref-diagram"]),
            lambda r: r["reference_manifest"].update(assessment_snapshot_id="other-snapshot"),
            lambda r: r.update(request_binding_hash="0" * 64),
        ):
            request = material_request()
            mutate(request)
            with self.subTest(request=request["request_id"]), self.assertRaises(AgentError):
                validate_request_v2(request)

    def test_damaged_png_is_rejected_after_hashes_are_rebound(self):
        original = base64.b64decode(material_request()["reference_media"][0]["data_base64"])
        idat = original.index(b"IDAT")
        length = int.from_bytes(original[idat - 4:idat], "big")
        bad_crc = bytearray(original)
        bad_crc[idat + 4 + length] ^= 1
        invalid_pixels = bytearray(original)
        invalid_pixels[idat + 4:idat + 4 + length] = b"\x00" * length
        crc = zlib.crc32(invalid_pixels[idat:idat + 4 + length])
        invalid_pixels[idat + 4 + length:idat + 8 + length] = crc.to_bytes(4, "big")
        for data in (original[:33], original[:-12], original[:-4], bytes(bad_crc), bytes(invalid_pixels)):
            request = material_request()
            media = request["reference_media"][0]
            media.update(data_base64=base64.b64encode(data).decode(), byte_size=len(data), sha256=hashlib.sha256(data).hexdigest())
            media["binding_hash"] = compute_reference_media_binding_hash(request, media)
            request["request_binding_hash"] = compute_request_binding_hash(request)
            with self.subTest(size=len(data)), self.assertRaisesRegex(AgentError, "PNG is damaged"):
                validate_request_v2(request)

    def test_reference_image_identity_cannot_be_used_as_student_evidence(self):
        request = material_request()
        fixture = Path(__file__).resolve().parents[2] / "contracts/grading-agent/v2/fixtures/valid-response.json"
        response = json.loads(fixture.read_text(encoding="utf-8"))
        response.update(accepted_features=["material-input-v1"], manifest_hash=request["reference_manifest"]["manifest_hash"],
                        request_binding_hash=request["request_binding_hash"])
        response["criterion_candidates"][0]["evidence_ids"] = [request["reference_media"][0]["media_id"]]
        with self.assertRaises(AgentError) as caught:
            validate_response_v2(response, request)
        self.assertEqual(caught.exception.code, "evidence_verification_failed")

    def test_consumption_failure_never_calls_the_provider(self):
        request, configured = self._configured()
        calls = []
        model = LocalLlamaCppAdapter(configured, transport=lambda *args: calls.append(args))
        with patch("grading_agent.model._open_model_request", side_effect=OSError("synthetic callback unavailable")):
            with self.assertRaises(AgentError):
                ProductionMathV2Application(configured, model, logger=lambda _: None).grade(request, request["request_id"], body_size=len(json.dumps(request)))
        self.assertEqual(calls, [])

    def test_real_adapter_receives_three_images_and_records_response(self):
        request, configured = self._configured()
        events = []
        class CallbackResponse:
            status = 200
            def read(self, _limit): return b'{"accepted":true}'
        @contextmanager
        def callback(req, timeout):
            events.append(json.loads(req.data))
            yield CallbackResponse()
        def provider(_url, payload, _headers, _timeout):
            images = [part for part in payload["messages"][1]["content"] if part["type"] == "image_url"]
            self.assertEqual(len(images), 3)
            self.assertEqual(events[0]["stage"], "consume")
            output = {"criterion_candidates": [], "alternative_solution_candidate": False, "risk_flags": []}
            return {"id": "synthetic-response", "choices": [{"message": {"role":"assistant", "content": json.dumps(output)}, "finish_reason": "stop"}],
                    "usage": {"prompt_tokens": 2, "completion_tokens": 3, "total_tokens": 5}}
        model = LocalLlamaCppAdapter(configured, transport=provider)
        with patch("grading_agent.model._open_model_request", side_effect=callback):
            result, replay = ProductionMathV2Application(configured, model, logger=lambda _: None).grade(request, request["request_id"], body_size=len(json.dumps(request)))
        self.assertFalse(replay)
        self.assertEqual([event["stage"] for event in events], ["consume", "provider_request_attempted", "provider_response_received"])
        self.assertEqual(result["accepted_features"], ["material-input-v1"])
        self.assertEqual(result["request_binding_hash"], request["request_binding_hash"])
        self.assertEqual(len(events[0]["wire_payload_hash"]), 64)
        self.assertEqual(events[-1]["usage"]["total_tokens"], 5)

    def test_timeout_does_not_repair_or_reconsume(self):
        request, configured = self._configured()
        calls = []
        def provider(*args):
            calls.append(args)
            raise TimeoutError("synthetic timeout")
        class CallbackResponse:
            status = 200
            def read(self, _limit): return b'{"accepted":true}'
        events = []
        @contextmanager
        def callback(req, timeout):
            events.append(json.loads(req.data)["stage"])
            yield CallbackResponse()
        with patch("grading_agent.model._open_model_request", side_effect=callback):
            with self.assertRaises(AgentError):
                ProductionMathV2Application(configured, LocalLlamaCppAdapter(configured, transport=provider), logger=lambda _: None).grade(request, request["request_id"], body_size=len(json.dumps(request)))
        self.assertEqual(len(calls), 1)
        self.assertEqual(events, ["consume", "provider_request_attempted", "unknown_after_attempt"])
        self.assertTrue(all(part["image_url"]["url"] == "" for part in calls[0][1]["messages"][1]["content"] if part["type"] == "image_url"))

    def test_schema_failure_does_not_repair_or_reconsume(self):
        request, configured = self._configured()
        calls, events = [], []
        def provider(*args):
            calls.append(args)
            return {"id": "synthetic-invalid-schema", "choices": [{"message": {"role": "assistant", "content": "{}"}, "finish_reason": "stop"}]}
        with patch("grading_agent.model._open_model_request", side_effect=self._callback(events)):
            with self.assertRaises(AgentError):
                ProductionMathV2Application(configured, LocalLlamaCppAdapter(configured, transport=provider), logger=lambda _: None).grade(request, request["request_id"], body_size=len(json.dumps(request)))
        self.assertEqual(len(calls), 1)
        self.assertEqual([event["stage"] for event in events], ["consume", "provider_request_attempted", "provider_response_received"])

    def test_material_dispatch_cannot_consume_twice(self):
        request, configured = self._configured()
        events, callback_requests = [], []
        dispatch = MaterialDispatch(configured, request)
        with patch("grading_agent.model._open_model_request", side_effect=self._callback(events, callback_requests)):
            dispatch.consume({"synthetic": "payload"})
            with self.assertRaises(AgentError):
                dispatch.consume({"synthetic": "payload"})
        self.assertEqual(len(events), 1)
        self.assertNotIn("Authorization", callback_requests[0].headers)

    def test_failed_callback_cannot_be_reused_and_clears_authorization(self):
        request, configured = self._configured()
        callback_requests = []
        def unavailable(req, timeout):
            callback_requests.append(req)
            raise OSError("synthetic callback unavailable")
        dispatch = MaterialDispatch(configured, request)
        with patch("grading_agent.model._open_model_request", side_effect=unavailable):
            for _ in range(2):
                with self.assertRaises(AgentError):
                    dispatch.consume({"synthetic": "payload"})
        self.assertEqual(len(callback_requests), 1)
        self.assertNotIn("Authorization", callback_requests[0].headers)

    def test_validation_failure_clears_copied_images_and_permit(self):
        request, configured = self._configured()
        request["reference_media"][0]["sha256"] = "0" * 64
        captured = []
        def observe(payload):
            captured.append(payload)
            return validate_request_v2(payload)
        with patch("grading_agent.app_v2.validate_request_v2", side_effect=observe):
            app = ProductionMathV2Application(configured, object(), logger=lambda _: None)
            with self.assertRaises(AgentError):
                app.grade(request, request["request_id"], body_size=len(json.dumps(request)))
        self._assert_material_cleared(captured[0])
        self.assertTrue(request["media_evidence"]["data_base64"])
        self.assertTrue(request["execution_permit"]["credential"])

    def test_success_replay_and_waiter_clear_copies_without_changing_caller(self):
        # 先阻塞拥有者，再让同命令调用进入等待；重放和等待者都不经过 _infer 的出口。
        request, configured = self._configured()
        original = copy.deepcopy(request)
        captured, events, calls = [], [], []
        provider_started, release_provider, waiter_started = threading.Event(), threading.Event(), threading.Event()
        def provider(*args):
            calls.append(args)
            provider_started.set()
            if not release_provider.wait(5):
                raise AssertionError("provider gate timed out")
            return {"id": "synthetic-response", "choices": [{"message": {"role": "assistant", "content": json.dumps({"criterion_candidates": [], "alternative_solution_candidate": False, "risk_flags": []})}, "finish_reason": "stop"}]}
        def observe(payload):
            captured.append(payload)
            return validate_request_v2(payload)
        app = ProductionMathV2Application(configured, LocalLlamaCppAdapter(configured, transport=provider), logger=lambda _: None)
        wait_original = app._wait_for_inflight
        def wait(inflight, request_id):
            waiter_started.set()
            return wait_original(inflight, request_id)
        grade = lambda: app.grade(request, request["request_id"], body_size=len(json.dumps(request)))
        with patch("grading_agent.model._open_model_request", side_effect=self._callback(events)), \
             patch("grading_agent.app_v2.validate_request_v2", side_effect=observe), \
             patch.object(app, "_wait_for_inflight", side_effect=wait), \
             ThreadPoolExecutor(max_workers=2) as pool:
            owner = pool.submit(grade)
            try:
                self.assertTrue(provider_started.wait(5))
                waiter = pool.submit(grade)
                self.assertTrue(waiter_started.wait(5))
            finally:
                release_provider.set()
            self.assertFalse(owner.result(timeout=5)[1])
            self.assertTrue(waiter.result(timeout=5)[1])
            self.assertTrue(grade()[1])
        self.assertEqual(len(calls), 1)
        self.assertEqual(len(captured), 3)
        for payload in captured:
            self._assert_material_cleared(payload)
        self.assertEqual(request, original)
        self.assertTrue(all(part["image_url"]["url"] == "" for part in calls[0][1]["messages"][1]["content"] if part["type"] == "image_url"))
        cached = json.dumps(app._cache, default=str)
        self.assertNotIn(original["execution_permit"]["credential"], cached)
        self.assertNotIn(original["reference_media"][0]["data_base64"], cached)

    def test_offline_seam_validation_failure_clears_copied_permit(self):
        request = material_request()
        captured = []
        def observe(payload):
            captured.append(payload)
            return validate_request_v2(payload)
        request["reference_media"][0]["sha256"] = "0" * 64
        seam = GradingAgentV2ApplicationSeam(OfflineV2FixtureAdapter({}), logger=lambda _: None)
        with patch("grading_agent.app_v2.validate_request_v2", side_effect=observe):
            with self.assertRaises(AgentError):
                seam.grade(request, request["request_id"], body_size=len(json.dumps(request)))
        self._assert_material_cleared(captured[0])
        self.assertTrue(request["execution_permit"]["credential"])

    def _assert_material_cleared(self, payload):
        self.assertEqual(payload["media_evidence"]["data_base64"], "")
        self.assertTrue(all(media["data_base64"] == "" for media in payload["reference_media"]))
        self.assertEqual(payload["execution_permit"]["credential"], "")

    @staticmethod
    def _callback(events, requests=None):
        class CallbackResponse:
            status = 200
            def read(self, _limit): return b'{"accepted":true}'
        @contextmanager
        def callback(req, timeout):
            events.append(json.loads(req.data))
            if requests is not None:
                requests.append(req)
            yield CallbackResponse()
        return callback

    def _configured(self):
        request = material_request()
        configured = replace(settings(), material_callback_base_url="http://127.0.0.1:9999")
        request["model_policy"]["model_version"] = configured.model_version
        request["prompt_version"] = configured.prompt_version
        return request, configured


if __name__ == "__main__":
    unittest.main()
