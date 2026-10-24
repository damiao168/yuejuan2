import base64
import copy
import hashlib
import json
import os
import struct
import tempfile
import threading
import unittest
import zlib
from concurrent.futures import ThreadPoolExecutor
from contextlib import contextmanager
from pathlib import Path
from unittest.mock import patch

from grading_agent.errors import AgentError
from grading_agent.image_protocol import (
    VERSION,
    ImageGradingApplication,
    _probe_png,
    validate_observation,
)
from grading_agent.managed_model import create_managed_model
from helpers import settings

CONFIG = {"adapter_type": "openai_compatible", "provider_key": "fixture", "base_url": "https://fixture.example/v1",
          "api_key": "private-test-credential", "model_name": "fixture-vision", "model_version": "vision-v1", "config_revision": "revision-1"}


def member(question="q1", presence="present"):
    return {"question_id": question, "presence": presence, "association": "unresolved" if presence == "uncertain" else "matched",
            "confidence": .97, "answer": {"type": "latex", "value": "-1/2 m", "parts": []} if presence == "present" else None,
            "evidence": [{"evidence_id": "student-" + question, "text": "-1/2 m" if presence == "present" else "空白区域",
                          "bbox": [.1, .2, .3, .1]}] if presence in {"present", "blank"} else []}


def request():
    _, png = _probe_png()
    return {"schema_version": VERSION, "request_id": "image-request-1", "input_version_id": "immutable-input", "input_mode": "image",
            "answer_text": "", "student_image": {"role": "student_answer", "media_type": "image/png", "sha256": hashlib.sha256(png).hexdigest(),
                                                "byte_size": len(png), "data": base64.b64encode(png).decode()},
            "members": [{"question_id": "q1", "question_no": "15", "question_type": "calculation", "question_text": "计算"}],
            "model_policy": {"model_version": "vision-v1"}, "managed_model": copy.deepcopy(CONFIG)}


def comparison(evidence="student-q1"):
    return {"judgments": [{"criterion_id": "p1", "status": "supported", "evidence_ids": [evidence], "reason": "可见学生步骤"}],
            "student_feedback": "请教师核对", "teacher_note": "仅建议"}


def read_probe(url):
    raw = base64.b64decode(url.split(",", 1)[1])
    offset, compressed = 8, b""
    while offset < len(raw):
        size = struct.unpack(">I", raw[offset:offset + 4])[0]
        if raw[offset + 4:offset + 8] == b"IDAT":
            compressed += raw[offset + 8:offset + 8 + size]
        offset += size + 12
    pixels = zlib.decompress(compressed)
    names = {(255, 0, 0): "red", (0, 255, 0): "green", (0, 0, 255): "blue", (255, 255, 0): "yellow"}
    return [names[tuple(pixels[(row * 32 + 16) * 289 + 1 + (column * 32 + 16) * 3:][:3])] for row in range(3) for column in range(3)]


class StructuredModel:
    def __init__(self, outputs=()):
        self.outputs, self.calls = list(outputs), []

    @contextmanager
    def session(self, _request_id):
        yield

    def request_structured(self, _request_id, messages, _schema, name):
        self.calls.append((name, copy.deepcopy(messages)))
        if name == "image_transport_probe_v3":
            return {"colors": read_probe(messages[1]["content"][0]["image_url"]["url"])}
        output = self.outputs.pop(0)
        if isinstance(output, Exception):
            raise output
        return copy.deepcopy(output)

    def last_usage(self):
        return {"input_tokens": 3, "output_tokens": 2, "total_tokens": 5}

    def last_execution_diagnostics(self):
        return {"finish_outcome": "completed", "attempts": 1, "provider_request_count": 1, "usage_status": "reported",
                "usage": self.last_usage(), "recovery_attempted": False}


class ImageProtocolTests(unittest.TestCase):
    def app(self, model):
        app = ImageGradingApplication(settings(), object())
        probe, _ = app.probe({"request_id": "probe-request-1", "managed_model": CONFIG}, "probe-request-1")
        self.assertTrue(probe["capability"]["visual_supported"])
        return app

    def test_capability_status_is_read_only_and_selection_changes_reuse_same_connection_probe(self):
        model = StructuredModel()
        app = ImageGradingApplication(settings(), object())
        first = dict(CONFIG, capability_revision="stable-connection-revision")
        with patch("grading_agent.image_protocol.create_managed_model", return_value=model):
            status, _ = app.probe({"request_id": "status-request-1", "managed_model": first, "status_only": True}, "status-request-1")
            self.assertEqual("not_checked", status["vision_probe_status"])
            self.assertEqual([], model.calls)
            app.probe({"request_id": "probe-request-1", "managed_model": first}, "probe-request-1")
            second = dict(first, config_revision="other-selection-revision")
            cached, replayed = app.probe({"request_id": "probe-request-2", "managed_model": second}, "probe-request-2")
            self.assertTrue(replayed)
            self.assertTrue(cached["cache_hit"])
            self.assertEqual(app._model_binding(second), cached["capability"]["model_binding"])
            self.assertEqual(1, len(model.calls))

    def test_independent_reviewer_reuses_frozen_source_observation_without_changing_its_identity(self):
        first_model = StructuredModel([{"members": [member()]}])
        second_model = StructuredModel([comparison()])
        first = dict(CONFIG, capability_revision="connection-a")
        second = dict(CONFIG, model_name="different-review-model", model_version="review-v2", config_revision="review-revision", capability_revision="connection-b")
        app = ImageGradingApplication(settings(), object())
        def selected_model(_settings, config):
            return second_model if config["model_name"] == second["model_name"] else first_model
        with patch("grading_agent.image_protocol.create_managed_model", side_effect=selected_model):
            app.probe({"request_id": "probe-request-a", "managed_model": first}, "probe-request-a")
            app.probe({"request_id": "probe-request-b", "managed_model": second}, "probe-request-b")
            observed_request = request()
            observed_request["managed_model"] = first
            observed, _ = app.execute(observed_request, observed_request["request_id"], "observe")
            grading_request = copy.deepcopy(observed_request)
            grading_request.update(request_id="independent-review-b", managed_model=second, agent_role="primary_b", observation=observed["observation"], observation_source_binding=observed["observation"]["model_binding"], rubric={"version": "rubric-v1", "points": [{"id": "p1", "score": 3}]})
            grading_request["model_policy"]["model_version"] = second["model_version"]
            graded, _ = app.execute(grading_request, grading_request["request_id"], "grade")
            self.assertEqual(observed["observation"], graded["observation"])
            self.assertEqual(["image_transport_probe_v3", "frozen_rubric_comparison_v3"], [name for name, _ in second_model.calls])
            self.assertNotIn("primary_a_score", json.dumps(second_model.calls, ensure_ascii=False))

    def test_image_only_observation_never_receives_teacher_reference_and_grade_stays_suggestion(self):
        model = StructuredModel([{"members": [member()]}, comparison()])
        with patch("grading_agent.image_protocol.create_managed_model", return_value=model):
            app = self.app(model)
            payload = request()
            payload.update(rubric={"version": "rubric-v1", "points": [{"id": "p1", "score": 3}]}, frozen_reference={"standard_answer": "teacher-private-answer"})
            result, replay = app.execute(payload, payload["request_id"], "grade")
            self.assertFalse(replay)
            observation_messages = model.calls[1][1]
            self.assertNotIn("teacher-private-answer", json.dumps(observation_messages))
            self.assertNotIn("rubric", json.dumps(observation_messages))
            self.assertEqual(observation_messages[1]["content"][1]["type"], "image_url")
            self.assertEqual(result["observation"]["members"][0]["answer"]["value"], "-1/2 m")
            self.assertTrue(result["needs_human_review"])
            self.assertNotIn("suggested_score", result)
            self.assertEqual(result["telemetry"]["attempts"], 2)
            self.assertEqual(result["telemetry"]["execution_diagnostics"]["provider_request_count"], 2)
            result2, replay = app.execute(payload, payload["request_id"], "grade")
            self.assertTrue(replay)
            self.assertEqual(result2, result)
            self.assertEqual(len(model.calls), 3)

    def test_shared_crop_observed_once_and_single_member_grade_reuses_immutable_observation(self):
        model = StructuredModel([{"members": [member("q12"), member("q13", "blank"), member("q14", "uncertain")]}, comparison("student-q12")])
        with patch("grading_agent.image_protocol.create_managed_model", return_value=model):
            app = self.app(model)
            payload = request()
            payload["members"] = [{"question_id": key, "question_type": "fill_blank"} for key in ("q12", "q13", "q14")]
            observed, _ = app.execute(payload, payload["request_id"], "observe")
            self.assertEqual([m["presence"] for m in observed["observation"]["members"]], ["present", "blank", "uncertain"])
            graded = copy.deepcopy(payload)
            graded["request_id"] = "grade-request-1"
            graded["members"] = [payload["members"][0]]
            graded["observation"] = copy.deepcopy(observed["observation"])
            graded["observation"]["prompt_version"] = "student-observation-v3-20261021"
            graded["observation"]["members"] = [graded["observation"]["members"][0]]
            graded["rubric"] = {"version": "v1", "points": [{"id": "p1", "score": 3}]}
            result, _ = app.execute(graded, graded["request_id"], "grade")
            self.assertEqual(result["telemetry"]["attempts"], 1)
            self.assertEqual([call[0] for call in model.calls], ["image_transport_probe_v3", "student_observation_v3", "frozen_rubric_comparison_v3"])
            graded["request_id"] = "grade-request-stale"
            graded["observation"]["crop_sha256"] = "wrong-crop"
            with self.assertRaises(AgentError) as raised:
                app.execute(graded, graded["request_id"], "grade")
            self.assertEqual(raised.exception.code, "invalid_request")
            self.assertEqual(len(model.calls), 3)

    def test_no_managed_model_never_calls_local_text_model(self):
        model = StructuredModel()
        app = ImageGradingApplication(settings(), model)
        payload = request()
        for variant in ({key: value for key, value in payload.items() if key != "managed_model"}, {**payload, "managed_model": {}}):
            with self.assertRaises(AgentError) as raised:
                app.execute(variant, payload["request_id"], "observe")
            self.assertEqual(raised.exception.code, "visual_model_unavailable")
        self.assertEqual(model.calls, [])

    def test_first_image_request_probes_current_connection_and_reuses_success(self):
        model = StructuredModel([{"members": [member()]}, {"members": [member()]}])
        app = ImageGradingApplication(settings(), object())
        with patch("grading_agent.image_protocol.create_managed_model", return_value=model):
            first = request()
            app.execute(first, first["request_id"], "observe")
            second = {**request(), "request_id": "image-request-2"}
            app.execute(second, second["request_id"], "observe")
        self.assertEqual(["image_transport_probe_v3", "student_observation_v3", "student_observation_v3"], [name for name, _ in model.calls])

    def test_changed_connection_requires_new_successful_probe(self):
        model = StructuredModel([{"members": [member()]}, {"members": [member()]}])
        app = ImageGradingApplication(settings(), object())
        with patch("grading_agent.image_protocol.create_managed_model", return_value=model):
            first = request()
            app.execute(first, first["request_id"], "observe")
            second = {**request(), "request_id": "image-request-2", "managed_model": {**CONFIG, "config_revision": "revision-2"}}
            app.execute(second, second["request_id"], "observe")
        self.assertEqual(2, sum(name == "image_transport_probe_v3" for name, _ in model.calls))

    def test_restart_reestablishes_probe_without_manual_model_configuration(self):
        model = StructuredModel([{"members": [member()]}, {"members": [member()]}])
        with patch("grading_agent.image_protocol.create_managed_model", return_value=model):
            for _ in range(2):
                app = ImageGradingApplication(settings(), object())
                payload = request()
                app.execute(payload, payload["request_id"], "observe")
        self.assertEqual(2, sum(name == "image_transport_probe_v3" for name, _ in model.calls))

    def test_automatic_transient_failure_is_shared_until_existing_ttl_expires(self):
        class RecoveringModel(StructuredModel):
            def request_structured(self, request_id, messages, schema, name):
                if not self.calls:
                    self.calls.append((name, copy.deepcopy(messages)))
                    raise AgentError("model_timeout", "temporary probe timeout", status=504)
                return super().request_structured(request_id, messages, schema, name)

        model = RecoveringModel([{"members": [member()]}])
        app = ImageGradingApplication(settings(), object())
        with patch("grading_agent.image_protocol.create_managed_model", return_value=model):
            for key in ("first-image-failure", "second-image-failure"):
                with self.assertRaises(AgentError):
                    app.execute({**request(), "request_id": key}, key, "observe")
            self.assertEqual(1, len(model.calls))
            created = next(iter(app._automatic_probe_keys.values()))[0]
            with patch("grading_agent.image_protocol.time.monotonic", return_value=created + app.settings.idempotency_ttl_seconds + 1):
                payload = {**request(), "request_id": "image-after-transient-ttl"}
                app.execute(payload, payload["request_id"], "observe")
        self.assertEqual(["image_transport_probe_v3", "image_transport_probe_v3", "student_observation_v3"], [name for name, _ in model.calls])

    def test_parallel_first_requests_share_probe_before_observing_students(self):
        entered, release = threading.Event(), threading.Event()

        class PausingModel(StructuredModel):
            def request_structured(self, request_id, messages, schema, name):
                if name == "image_transport_probe_v3":
                    entered.set()
                    if not release.wait(5):
                        raise RuntimeError("test probe was not released")
                return super().request_structured(request_id, messages, schema, name)

        model = PausingModel([{"members": [member()]}, {"members": [member()]}])
        app = ImageGradingApplication(settings(), object())
        with patch("grading_agent.image_protocol.create_managed_model", return_value=model), ThreadPoolExecutor(max_workers=2) as executor:
            first = request()
            pending = executor.submit(app.execute, first, first["request_id"], "observe")
            self.assertTrue(entered.wait(5))
            second = {**request(), "request_id": "parallel-request-2"}
            other = executor.submit(app.execute, second, second["request_id"], "observe")
            release.set()
            pending.result(timeout=5)
            other.result(timeout=5)
        self.assertEqual(1, sum(name == "image_transport_probe_v3" for name, _ in model.calls))

    def test_provider_question_number_echo_must_match_input_and_unknown_fields_still_fail(self):
        payload = request()
        observed = {"members": [{**member(), "question_no": "15", "question_text": "context echo", "question_type": "calculation"}]}
        self.assertEqual(validate_observation(observed, payload), observed)
        self.assertNotIn("question_no", observed["members"][0])
        self.assertNotIn("question_text", observed["members"][0])
        self.assertNotIn("question_type", observed["members"][0])
        for extra in ({"question_no": "16"}, {"unexpected_field": "ignored?"}):
            with self.subTest(extra=extra), self.assertRaises(AgentError):
                validate_observation({"members": [{**member(), **extra}]}, payload)

    def test_named_model_edges_normalize_without_guessing_legacy_coordinate_format(self):
        for bbox, valid in (({"left": .04, "top": .54, "right": .98, "bottom": .94}, True),
                            ({"left": .98, "top": .54, "right": .04, "bottom": .94}, False),
                            ([.04, .54, .98, .94], False)):
            observed = {"members": [member()]}
            observed["members"][0]["evidence"][0]["bbox"] = bbox
            if not valid:
                with self.assertRaises(AgentError):
                    validate_observation(observed, request())
                continue
            normalized = validate_observation(observed, request())["members"][0]["evidence"][0]["bbox"]
            for actual, expected in zip(normalized, [.04, .54, .94, .40]):
                self.assertAlmostEqual(actual, expected)

    def test_invalid_provider_output_is_captured_before_schema_validation_without_credentials(self):
        def transport(*_args):
            return {"choices": [{"finish_reason": "stop", "message": {"role": "assistant", "content": '{"members":[]}'}}]}
        model = create_managed_model(settings(), CONFIG)
        model.transport = transport
        app = ImageGradingApplication(settings(), object())
        binding = app._model_binding(CONFIG)
        app._visual_capabilities[binding] = {"visual_supported": True}
        with tempfile.TemporaryDirectory() as directory, patch.dict(os.environ, {
                "EDUGRADE_TEMPLATE_ANALYSIS_DIAGNOSTICS_DIR": directory,
                "EDUGRADE_TEMPLATE_ANALYSIS_CAPTURE_CONTENT": "true"}), patch(
                "grading_agent.image_protocol.create_managed_model", return_value=model):
            payload = request()
            with self.assertRaises(AgentError):
                app.execute(payload, payload["request_id"], "observe")
            records = list(Path(directory).glob("*.json"))
            self.assertEqual(len(records), 1)
            record = json.loads(records[0].read_text())
            self.assertEqual(record["metadata"]["status"], "failed")
            self.assertEqual(record["metadata"]["event"], "image_grading")
            self.assertEqual(json.loads(record["responses"][0]["content_json"]), '{"members":[]}')
            self.assertNotIn(CONFIG["api_key"], records[0].read_text())

    def test_probe_reads_pixels_and_caches_by_configuration_revision(self):
        model = StructuredModel()
        with patch("grading_agent.image_protocol.create_managed_model", return_value=model):
            app = self.app(model)
            _, replay = app.probe({"request_id": "probe-request-2", "managed_model": CONFIG}, "probe-request-2")
            self.assertTrue(replay)
            self.assertEqual(len(model.calls), 1)
            changed = {**CONFIG, "config_revision": "revision-2"}
            app.probe({"request_id": "probe-request-3", "managed_model": changed}, "probe-request-3")
            self.assertEqual(len(model.calls), 2)

    def test_probe_mismatch_keeps_raw_assistant_response_and_validation_in_controlled_trace(self):
        def transport(*_args):
            return {"choices": [{"finish_reason": "stop", "message": {"role": "assistant", "content": json.dumps({"colors": ["red"] * 9})}}]}

        model = create_managed_model(settings(), CONFIG)
        model.transport = transport
        _, png = _probe_png()
        app = ImageGradingApplication(settings(), object())
        with tempfile.TemporaryDirectory() as directory, patch.dict(os.environ, {
                "EDUGRADE_TEMPLATE_ANALYSIS_DIAGNOSTICS_DIR": directory,
                "EDUGRADE_TEMPLATE_ANALYSIS_CAPTURE_CONTENT": "true"}), patch(
                "grading_agent.image_protocol.create_managed_model", return_value=model), patch(
                "grading_agent.image_protocol._probe_png", return_value=(["green"] * 9, png)):
            with self.assertRaises(AgentError):
                app.probe({"request_id": "probe-mismatch-1", "managed_model": CONFIG}, "probe-mismatch-1")
            records = list(Path(directory).glob("*.json"))
            self.assertEqual(len(records), 1)
            record = json.loads(records[0].read_text())
            self.assertEqual(record["metadata"]["event"], "image_model_probe")
            self.assertEqual(record["metadata"]["status"], "failed")
            self.assertFalse(record["metadata"]["probe_validation"]["matches"])
            self.assertEqual(record["metadata"]["probe_validation"]["observed_colors"], ["red"] * 9)
            self.assertEqual(json.loads(json.loads(record["responses"][0]["content_json"])), {"colors": ["red"] * 9})
            self.assertNotIn(CONFIG["api_key"], records[0].read_text())
            self.assertNotIn("data:image", records[0].read_text())

    def test_transient_probe_failure_keeps_same_request_failure_but_allows_new_verified_probe(self):
        class TransientModel(StructuredModel):
            def request_structured(self, request_id, messages, schema, name):
                if not self.calls:
                    self.calls.append((name, copy.deepcopy(messages)))
                    raise AgentError("model_request_rejected", "provider returned HTTP 503", status=502,
                                     upstream_http_status=503, upstream_error_code="provider-secret-raw-code")
                return super().request_structured(request_id, messages, schema, name)

        model = TransientModel([{"members": [member()]}])
        app = ImageGradingApplication(settings(), object())
        with tempfile.TemporaryDirectory() as directory, patch.dict(os.environ, {
                "EDUGRADE_TEMPLATE_ANALYSIS_DIAGNOSTICS_DIR": directory}), patch(
                "grading_agent.image_protocol.create_managed_model", return_value=model):
            # 同一失败身份不能重新派发；正式请求可建立新探测，但仍须读对图片才能观察学生作答。
            for _ in range(2):
                with self.assertRaises(AgentError):
                    app.probe({"request_id": "probe-transient-1", "managed_model": CONFIG}, "probe-transient-1")
            self.assertEqual(len(model.calls), 1)
            app.execute(request(), "image-request-1", "observe")
            result, replay = app.probe({"request_id": "probe-transient-2", "managed_model": CONFIG}, "probe-transient-2")
            self.assertTrue(result["capability"]["visual_supported"])
            self.assertTrue(replay)
            self.assertEqual(len(model.calls), 3)
            records = [json.loads(path.read_text()) for path in Path(directory).glob("*.json")]
            failed = next(record for record in records if record["metadata"]["status"] == "failed")
            self.assertEqual(failed["metadata"]["probe_failure"]["upstream_http_status"], 503)
            self.assertNotIn("provider-secret-raw-code", json.dumps(records))

    def test_probe_visual_mismatch_still_blocks_new_requests_for_same_config(self):
        class MismatchModel(StructuredModel):
            def request_structured(self, _request_id, messages, _schema, name):
                self.calls.append((name, copy.deepcopy(messages)))
                return {"colors": ["red"] * 9}

        model = MismatchModel()
        _, png = _probe_png()
        app = ImageGradingApplication(settings(), object())
        with patch("grading_agent.image_protocol.create_managed_model", return_value=model), patch(
                "grading_agent.image_protocol._probe_png", return_value=(["green"] * 9, png)):
            for key in ("probe-mismatch-1", "probe-mismatch-2"):
                with self.assertRaises(AgentError):
                    app.probe({"request_id": key, "managed_model": CONFIG}, key)
            self.assertEqual(len(model.calls), 1)
            with self.assertRaises(AgentError):
                app.execute(request(), "image-request-1", "observe")
            self.assertEqual(len(model.calls), 1)

    def test_missing_input_blank_without_evidence_and_teacher_evidence_are_rejected(self):
        payload = request()
        invalid = {"members": [member(presence="blank")]}
        invalid["members"][0]["evidence"] = []
        with self.assertRaises(AgentError):
            validate_observation(invalid, payload)
        model = StructuredModel([{"members": [member()]}, comparison("teacher-step")])
        with patch("grading_agent.image_protocol.create_managed_model", return_value=model):
            app = self.app(model)
            payload.update(rubric={"version": "v1", "points": [{"id": "p1", "score": 3}]})
            with self.assertRaises(AgentError) as raised:
                app.execute(payload, payload["request_id"], "grade")
            self.assertEqual(raised.exception.code, "model_output_invalid")
            with self.assertRaises(AgentError):
                app.execute(payload, payload["request_id"], "grade")
            self.assertEqual(len(model.calls), 3)
            missing = request()
            missing.pop("student_image")
            with self.assertRaises(AgentError) as raised:
                app.execute(missing, missing["request_id"], "observe")
            self.assertEqual(raised.exception.code, "invalid_request")

    def test_observation_endpoint_rejects_reference_payload_before_any_dispatch(self):
        app = ImageGradingApplication(settings(), object())
        payload = request()
        payload["frozen_reference"] = {"standard_answer": "secret"}
        with self.assertRaises(AgentError) as raised:
            app.execute(payload, payload["request_id"], "observe")
        self.assertEqual(raised.exception.code, "invalid_request")

    def test_native_and_compatible_transports_contain_real_student_image(self):
        _, png = _probe_png()
        data_url = "data:image/png;base64," + base64.b64encode(png).decode()
        messages = [{"role": "system", "content": "observe"}, {"role": "user", "content": [{"type": "image_url", "image_url": {"url": data_url}}]}]
        schema = {"type": "object", "additionalProperties": False, "properties": {"ok": {"type": "boolean"}}, "required": ["ok"]}
        for adapter in ("openai_compatible", "dashscope_native"):
            captured = []
            def transport(url, body, _headers, _timeout, captured=captured, adapter=adapter):
                captured.append((url, copy.deepcopy(body)))
                choice = {"finish_reason": "stop", "message": {"role": "assistant", "content": [{"text": '{"ok":true}'}] if adapter == "dashscope_native" else '{"ok":true}'}}
                return {"output": {"choices": [choice]}} if adapter == "dashscope_native" else {"choices": [choice]}
            config = {**CONFIG, "adapter_type": adapter}
            if adapter == "dashscope_native":
                config["base_url"] = "https://dashscope.aliyuncs.com/api/v1"
            with patch("grading_agent.managed_model.public_json_transport", transport):
                model = create_managed_model(settings(), config)
                with model.session("transport-test"):
                    self.assertEqual(model.request_structured("transport-test", messages, schema, "test"), {"ok": True})
            url, body = captured[0]
            if adapter == "dashscope_native":
                self.assertTrue(url.endswith("/multimodal-generation/generation"))
                self.assertEqual(body["input"]["messages"][1]["content"][0]["image"], data_url)
            else:
                self.assertTrue(url.endswith("/chat/completions"))
                self.assertIn(data_url, json.dumps(body["messages"]))


if __name__ == "__main__":
    unittest.main()
