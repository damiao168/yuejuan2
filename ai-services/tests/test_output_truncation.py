"""只使用本地 envelope/HTTP 替身验证输出完整性，不调用真实模型。"""

import copy
import json
import threading
import unittest
from contextlib import contextmanager
from urllib import error as urlerror
from urllib import request as urlrequest

from grading_agent.app import GradingAgentApplication
from grading_agent.app_v2 import ProductionMathV2Application
from grading_agent.errors import AgentError
from grading_agent.execution_diagnostics import (
    sanitize_execution_diagnostics,
    usage_facts,
)
from grading_agent.model import DashScopeNativeAdapter, LocalLlamaCppAdapter
from grading_agent.server import GradingAgentHTTPServer
from helpers import ROOT, FakeModel, settings, valid_raw_output, valid_request
from jsonschema import Draft202012Validator, ValidationError
from test_math_v2_production import valid_math_v2_request


def provider_response(native=False, *, content=None, finish="stop", usage=True):
    choice = {"finish_reason": finish, "message": {"role": "assistant", "content": json.dumps(
        valid_raw_output() if content is None else content, ensure_ascii=False)}}
    response = {"output": {"choices": [choice]}} if native else {"choices": [choice]}
    if usage:
        response["usage"] = {"input_tokens": 7, "output_tokens": 3, "total_tokens": 10}
    response["request_id" if native else "id"] = "fixture-provider-id"
    return response


def adapter(native, response, calls=None):
    def transport(_url, _payload, _headers, _timeout):
        if calls is not None:
            calls.append(copy.deepcopy(_payload))
        return copy.deepcopy(response)
    adapter_type = DashScopeNativeAdapter if native else LocalLlamaCppAdapter
    return adapter_type(settings(adapter_type="dashscope_native" if native else "local_llama_cpp"), transport=transport)


def invoke(model, structured):
    if structured:
        return model.request_structured("request-1", [], {"type": "object"}, "fixture")
    return model.request(valid_request())


class OutputCompletionTests(unittest.TestCase):
    def test_provider_correlation_is_bounded_and_not_part_of_public_diagnostics(self):
        for native in (False, True):
            response = provider_response(native, finish="length")
            field = "request_id" if native else "id"
            for provider_id, expected in (("safe-provider-id", "safe-provider-id"), ("https://private.invalid/body", ""), ("x" * 129, "")):
                response[field] = provider_id
                model = adapter(native, response)
                with self.assertRaises(AgentError) as caught:
                    invoke(model, False)
                self.assertEqual(model.last_provider_request_id(), expected)
                self.assertNotIn("provider_request_id", caught.exception.payload()["execution_diagnostics"])

    def test_all_four_paths_reject_valid_json_with_noncompleted_finish_and_keep_usage(self):
        cases = [
            ("length", "output_limit", "output_truncated"),
            ("content_filter", "filtered_or_refused", "content_filtered"),
            ("tool_calls", "unsupported_generation", "unsupported_generation"),
            ("function_call", "unsupported_generation", "unsupported_generation"),
            (None, "finish_missing", "finish_reason_missing"),
            ("supplier-private-message", "finish_unknown", "finish_reason_unknown"),
        ]
        for native in (False, True):
            for structured in (False, True):
                for finish, outcome, kind in cases:
                    with self.subTest(native=native, structured=structured, finish=finish):
                        model = adapter(native, provider_response(native, finish=finish))
                        with self.assertRaises(AgentError) as caught:
                            invoke(model, structured)
                        error = caught.exception
                        facts = error.execution_diagnostics
                        self.assertFalse(error.retryable)
                        self.assertEqual(facts["finish_outcome"], outcome)
                        self.assertEqual(facts["failure_kind"], kind)
                        self.assertEqual(facts["usage"], {"input_tokens": 7, "output_tokens": 3, "total_tokens": 10})
                        self.assertEqual(model.last_usage(), facts["usage"])
                        self.assertNotIn("supplier-private-message", json.dumps(error.payload()))

    def test_stop_is_required_but_not_sufficient_for_refusal_tools_and_role(self):
        for native in (False, True):
            for mutation, kind in (
                ({"refusal": "private refusal"}, "provider_refused"),
                ({"tool_calls": [{"secret": "tool body"}]}, "unsupported_generation"),
                ({"function_call": {"name": "private tool"}}, "unsupported_generation"),
                ({"role": "user"}, "response_envelope_invalid"),
            ):
                response = provider_response(native)
                envelope = response["output"] if native else response
                envelope["choices"][0]["message"].update(mutation)
                with self.subTest(native=native, kind=kind), self.assertRaises(AgentError) as caught:
                    invoke(adapter(native, response), True)
                self.assertEqual(caught.exception.execution_diagnostics["failure_kind"], kind)
                self.assertNotIn("private", json.dumps(caught.exception.payload()))

    def test_completed_output_is_accepted_without_inventing_usage(self):
        for native in (False, True):
            for structured in (False, True):
                model = adapter(native, provider_response(native, usage=False))
                self.assertIsInstance(invoke(model, structured), dict)
                self.assertEqual(model.last_usage(), {})
                self.assertEqual(model.last_execution_diagnostics()["usage_status"], "unavailable")
        self.assertEqual(usage_facts({"usage": {"output_tokens": 0}}), ({"output_tokens": 0}, "partial"))
        self.assertEqual(usage_facts({"usage": {"input_tokens": -1, "output_tokens": 3}}), ({"output_tokens": 3}, "invalid"))

    def test_bad_json_and_schema_retain_known_usage_after_completed_finish(self):
        for native in (False, True):
            response = provider_response(native)
            envelope = response["output"] if native else response
            envelope["choices"][0]["message"]["content"] = "{broken"
            model = adapter(native, response)
            with self.assertRaises(AgentError) as caught:
                invoke(model, True)
            self.assertEqual(caught.exception.execution_diagnostics["failure_kind"], "schema_invalid")
            self.assertEqual(model.last_usage()["total_tokens"], 10)
            model = adapter(native, provider_response(native, content={}))
            with self.assertRaises(AgentError) as caught:
                invoke(model, False)
            self.assertEqual(caught.exception.execution_diagnostics["finish_outcome"], "completed")
            self.assertEqual(model.last_usage()["total_tokens"], 10)

    def test_invalid_transport_envelope_is_terminal_in_all_four_paths(self):
        for native in (False, True):
            for structured in (False, True):
                model = adapter(native, None)
                calls = []
                def transport(*_args):
                    calls.append(1)
                    raise ValueError("private invalid provider JSON")
                model.transport = transport
                with self.subTest(native=native, structured=structured), self.assertRaises(AgentError) as caught:
                    invoke(model, structured)
                facts = caught.exception.execution_diagnostics
                self.assertEqual(facts["finish_outcome"], "envelope_invalid")
                self.assertEqual(facts["failure_kind"], "response_envelope_invalid")
                self.assertEqual(facts["usage_status"], "unavailable")
                self.assertNotIn("private", json.dumps(caught.exception.payload()))
                self.assertEqual(calls, [1])

    def test_known_first_usage_survives_second_transport_failure_as_partial(self):
        calls = []
        response = provider_response(content={})
        def transport(*_args):
            calls.append(1)
            if len(calls) == 1:
                return response
            raise TimeoutError("private upstream timeout")
        model = LocalLlamaCppAdapter(settings(), transport=transport)
        app = GradingAgentApplication(settings(), model=model, logger=lambda _event: None)
        request = valid_request()
        with self.assertRaises(AgentError) as caught:
            app.grade(request, request["request_id"])
        facts = caught.exception.execution_diagnostics
        self.assertEqual(facts["finish_outcome"], "unavailable")
        self.assertNotIn("failure_kind", facts)
        self.assertEqual(facts["provider_request_count"], 2)
        self.assertEqual(facts["usage_status"], "partial")
        self.assertEqual(facts["usage"], {"input_tokens": 7, "output_tokens": 3, "total_tokens": 10})
        self.assertEqual(len(calls), 2)

    def test_schema_repair_followed_by_length_cannot_open_a_third_call(self):
        responses = [provider_response(content={}), provider_response(finish="length")]
        calls = []
        def transport(*_args):
            calls.append(1)
            return responses.pop(0)
        model = LocalLlamaCppAdapter(settings(), transport=transport)
        app = GradingAgentApplication(settings(), model=model, logger=lambda _event: None)
        request = valid_request()
        for _ in range(2):
            with self.assertRaises(AgentError) as caught:
                app.grade(request, request["request_id"])
            facts = caught.exception.execution_diagnostics
            self.assertEqual(facts["failure_kind"], "output_truncated")
            self.assertEqual(facts["provider_request_count"], 2)
            self.assertEqual(facts["usage"]["total_tokens"], 20)
        self.assertEqual(len(calls), 2)

    def test_diagnostic_copy_rejects_cross_field_contradictions(self):
        facts = {"finish_outcome": "completed", "attempts": 1, "provider_request_count": 1,
                 "usage_status": "reported", "usage": {"input_tokens": 7, "output_tokens": 3, "total_tokens": 10},
                 "recovery_attempted": False}
        for change in (
            {"provider_request_count": 0}, {"recovery_attempted": True}, {"usage": {"total_tokens": 10}},
            {"usage_status": "unavailable"}, {"finish_outcome": "output_limit"},
            {"finish_outcome": "unavailable", "failure_kind": "schema_invalid"},
            {"finish_outcome": []}, {"usage_status": {}}, {"failure_kind": []},
            {"failure_kind": "private-provider-error"},
        ):
            with self.subTest(change=change):
                self.assertIsNone(sanitize_execution_diagnostics({**facts, **change}))
        self.assertEqual(sanitize_execution_diagnostics(facts), facts)

    def test_length_is_single_call_and_terminal_replay_preserves_facts(self):
        for native in (False, True):
            calls = []
            model = adapter(native, provider_response(native, finish="length"), calls)
            app = GradingAgentApplication(settings(), model=model, logger=lambda _event: None)
            request = valid_request()
            errors = []
            for _ in range(2):
                with self.assertRaises(AgentError) as caught:
                    app.grade(request, request["request_id"])
                errors.append(caught.exception.payload())
            self.assertEqual(len(calls), 1)
            self.assertEqual(errors[0], errors[1])
            self.assertEqual(errors[0]["execution_diagnostics"]["provider_request_count"], 1)
            changed = copy.deepcopy(request)
            changed["answer_text"] += "changed"
            with self.assertRaises(AgentError) as caught:
                app.grade(changed, request["request_id"])
            self.assertEqual(caught.exception.code, "idempotency_conflict")

    def test_schema_repair_stays_at_two_calls_and_marks_incomplete_usage(self):
        bad = valid_raw_output()
        bad["evidence"][0]["text_excerpt"] = "invented"
        model = FakeModel([bad, valid_raw_output()], usages=[{}, {"input_tokens": 7, "output_tokens": 3, "total_tokens": 10}])
        app = GradingAgentApplication(settings(), model=model, logger=lambda _event: None)
        result, _ = app.grade(valid_request(), valid_request()["request_id"])
        facts = result["telemetry"]["execution_diagnostics"]
        self.assertEqual(facts["attempts"], 2)
        self.assertEqual(facts["usage_status"], "partial")
        self.assertEqual(facts["usage"], result["telemetry"]["usage"])
        self.assertNotIn("failure_kind", facts)

    def test_concurrent_terminal_failure_is_copied_with_diagnostics(self):
        started = threading.Event()
        release = threading.Event()
        calls = []
        def transport(*_args):
            calls.append(1)
            started.set()
            release.wait(2)
            return provider_response(finish="length")
        model = LocalLlamaCppAdapter(settings(), transport=transport)
        app = GradingAgentApplication(settings(), model=model, logger=lambda _event: None)
        errors = []
        def run():
            try:
                app.grade(valid_request(), valid_request()["request_id"])
            except AgentError as exc:
                errors.append(exc.payload())
        owner = threading.Thread(target=run)
        waiter = threading.Thread(target=run)
        owner.start()
        self.assertTrue(started.wait(2))
        waiter.start()
        release.set()
        owner.join(3)
        waiter.join(3)
        self.assertEqual(len(errors), 2)
        self.assertEqual(errors[0], errors[1])
        self.assertEqual(calls, [1])

    def test_native_math_visual_capability_is_rejected_without_dispatch(self):
        calls = []
        model = adapter(True, provider_response(True), calls)
        app = ProductionMathV2Application(settings(), model, logger=lambda _event: None)
        request = valid_math_v2_request()
        for _ in range(2):
            with self.assertRaises(AgentError) as caught:
                app.grade(request, request["request_id"], body_size=len(json.dumps(request).encode()))
            facts = caught.exception.execution_diagnostics
            self.assertEqual(facts["failure_kind"], "capability_unavailable")
            self.assertEqual(facts["provider_request_count"], 0)
            self.assertEqual(facts["attempts"], 0)
        self.assertEqual(calls, [])

    def test_v2_nonretryable_auth_is_not_retried(self):
        class AuthFailureModel:
            calls = 0
            @contextmanager
            def session(self, _request_id):
                yield
            def request_structured(self, *_args):
                self.calls += 1
                raise AgentError("model_unavailable", "authentication failed", status=503, retryable=False)
        model = AuthFailureModel()
        app = ProductionMathV2Application(settings(), model, logger=lambda _event: None)
        request = valid_math_v2_request()
        with self.assertRaises(AgentError):
            app.grade(request, request["request_id"], body_size=len(json.dumps(request).encode()))
        self.assertEqual(model.calls, 1)

    def test_v2_http_errors_use_v2_schema_and_closed_diagnostics(self):
        model = adapter(False, provider_response(finish="length"))
        application = GradingAgentApplication(settings(), model=model, logger=lambda _event: None)
        server = GradingAgentHTTPServer(("127.0.0.1", 0), application)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            payload = valid_math_v2_request()
            request = urlrequest.Request(f"http://127.0.0.1:{server.server_port}/grading/grade-v2",
                data=json.dumps(payload).encode(), headers={"Content-Type": "application/json",
                "Authorization": f"Bearer {application.settings.service_token}", "Idempotency-Key": payload["request_id"]})
            with self.assertRaises(urlerror.HTTPError) as caught:
                urlrequest.urlopen(request, timeout=3)
            error = json.loads(caught.exception.read())
            schema = json.loads((ROOT / "contracts/grading-agent/v2/error.schema.json").read_text(encoding="utf-8"))
            Draft202012Validator(schema).validate(error)
            self.assertEqual(error["schema_version"], "grading-agent-v2")
            self.assertEqual(error["execution_diagnostics"]["failure_kind"], "output_truncated")
            error["execution_diagnostics"]["provider_body"] = "private"
            with self.assertRaises(ValidationError):
                Draft202012Validator(schema).validate(error)
        finally:
            server.shutdown()
            server.server_close()
            thread.join(2)


if __name__ == "__main__":
    unittest.main()
