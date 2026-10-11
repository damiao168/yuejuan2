import json
import unittest
from pathlib import Path
from unittest.mock import patch

from grading_agent.dashscope_native_contract import (
    DASHSCOPE_MULTIMODAL_GENERATION_PATH,
    DASHSCOPE_TEXT_GENERATION_PATH,
)
from grading_agent.dashscope_native_transport import (
    FIXTURE_AUTHORIZATION,
    MAX_DASHSCOPE_RESPONSE_BYTES,
    MAX_DASHSCOPE_TEXT_REQUEST_BYTES,
    DashScopeNativeTransportSeam,
    FixtureDashScopeResponse,
    FixtureDashScopeTransport,
)
from grading_agent.errors import AgentError
from grading_agent.provider_adapter import default_provider_adapter_registry

FIXTURES = Path(__file__).parent / "fixtures" / "dashscope-native"


def fixture(name):
    return json.loads(FIXTURES.joinpath(name).read_text(encoding="utf-8"))


class DashScopeNativeTransportTests(unittest.TestCase):
    def test_text_and_multimodal_fixtures_execute_on_exact_native_paths(self):
        transport = FixtureDashScopeTransport(
            [
                FixtureDashScopeResponse.from_json(200, fixture("response-text.json")),
                FixtureDashScopeResponse.from_json(200, fixture("response-image.json")),
            ]
        )
        seam = DashScopeNativeTransportSeam(transport)

        text = seam.execute_text(fixture("request-text.json"))
        image = seam.execute_multimodal(fixture("request-image.json"))

        self.assertEqual(text.output["suggested_score"], 4)
        self.assertEqual(image.output["suggested_score"], 4)
        self.assertEqual(
            [call["path"] for call in transport.calls],
            [DASHSCOPE_TEXT_GENERATION_PATH, DASHSCOPE_MULTIMODAL_GENERATION_PATH],
        )
        self.assertTrue(all("compatible-mode" not in call["path"] for call in transport.calls))

    def test_fixture_transport_records_only_safe_request_facts(self):
        request = fixture("request-image.json")
        transport = FixtureDashScopeTransport(
            [FixtureDashScopeResponse.from_json(200, fixture("response-image.json"))]
        )
        events = []
        seam = DashScopeNativeTransportSeam(transport, logger=events.append)
        seam.execute_multimodal(request)

        recorded = json.dumps(transport.calls, ensure_ascii=False)
        logged = json.dumps(events, ensure_ascii=False)
        image = request["input"]["messages"][1]["content"][0]["image"]
        for sensitive in (image, FIXTURE_AUTHORIZATION):
            self.assertNotIn(sensitive, recorded)
            self.assertNotIn(sensitive, logged)
        self.assertNotIn("body", transport.calls[0])
        self.assertIn("body_sha256", transport.calls[0])

    def test_retryable_native_error_retries_once_then_succeeds(self):
        throttled = fixture("error-throttled.json")
        transport = FixtureDashScopeTransport(
            [
                FixtureDashScopeResponse.from_json(throttled["http_status"], throttled["body"]),
                FixtureDashScopeResponse.from_json(200, fixture("response-text.json")),
            ]
        )
        seam = DashScopeNativeTransportSeam(transport)

        result = seam.execute_text(fixture("request-text.json"))
        self.assertEqual(result.output["suggested_score"], 4)
        self.assertEqual(len(transport.calls), 2)

    def test_authentication_failure_is_sanitized_and_not_retried(self):
        authentication = fixture("error-auth.json")
        transport = FixtureDashScopeTransport(
            [FixtureDashScopeResponse.from_json(authentication["http_status"], authentication["body"])]
        )
        seam = DashScopeNativeTransportSeam(transport)

        request = fixture('request-text.json')
        with self.assertRaises(AgentError) as caught:
            seam.execute_text(request)
        self.assertEqual(caught.exception.code, "model_unavailable")
        self.assertFalse(caught.exception.retryable)
        self.assertEqual(len(transport.calls), 1)
        self.assertNotIn(authentication["body"]["message"], caught.exception.message)

    def test_timeout_is_bounded_and_mapped_after_one_retry(self):
        transport = FixtureDashScopeTransport([TimeoutError(), TimeoutError()])
        seam = DashScopeNativeTransportSeam(transport, timeout_seconds=1)

        request = fixture('request-text.json')
        with self.assertRaises(AgentError) as caught:
            seam.execute_text(request)
        self.assertEqual(caught.exception.code, "model_timeout")
        self.assertEqual(len(transport.calls), 2)
        self.assertTrue(all(call["timeout_seconds"] == 1 for call in transport.calls))

    def test_request_and_response_limits_fail_closed(self):
        transport = FixtureDashScopeTransport([])
        seam = DashScopeNativeTransportSeam(transport)
        oversized_request = fixture("request-text.json")
        oversized_request["input"]["messages"][1]["content"] = (
            "x" * MAX_DASHSCOPE_TEXT_REQUEST_BYTES
        )
        with self.assertRaises(AgentError) as request_error:
            seam.execute_text(oversized_request)
        self.assertEqual(request_error.exception.status, 413)
        self.assertEqual(transport.calls, [])

        oversized_response = FixtureDashScopeResponse(
            status=200,
            content_type="application/json",
            body=b"{" + b"x" * MAX_DASHSCOPE_RESPONSE_BYTES + b"}",
        )
        transport = FixtureDashScopeTransport([oversized_response])
        seam = DashScopeNativeTransportSeam(transport)
        request = fixture('request-text.json')
        with self.assertRaises(AgentError) as response_error:
            seam.execute_text(request)
        self.assertEqual(response_error.exception.code, "model_output_invalid")

    def test_payload_validation_rejects_identity_fields_and_remote_images(self):
        transport = FixtureDashScopeTransport([])
        seam = DashScopeNativeTransportSeam(transport)
        text = fixture("request-text.json")
        text["student_id"] = "must-not-export"
        with self.assertRaises(AgentError) as identity_error:
            seam.execute_text(text)
        self.assertEqual(identity_error.exception.code, "invalid_request")

        image = fixture("request-image.json")
        image["input"]["messages"][1]["content"][0]["image"] = "https://example.invalid/crop.png"
        with self.assertRaises(AgentError) as image_error:
            seam.execute_multimodal(image)
        self.assertEqual(image_error.exception.code, "invalid_request")
        self.assertEqual(transport.calls, [])

    def test_inline_prefix_cannot_hide_external_resource_schemes(self):
        for scheme in ("http", "HTTPS", "File", "oSs"):
            with self.subTest(scheme=scheme):
                image = fixture("request-image.json")
                image["input"]["messages"][1]["content"][0]["image"] = (
                    "data:image/png;base64," + scheme + "://example.invalid/crop"
                )
                transport = FixtureDashScopeTransport([])
                seam = DashScopeNativeTransportSeam(transport)
                with self.assertRaisesRegex(AgentError, "one inline PNG"):
                    seam.execute_multimodal(image)
                self.assertEqual(transport.calls, [])

    # 只观察 seam 交给替身的同一缓冲引用；清理不能表述为擦除了调用方的请求对象。
    def test_request_buffer_is_cleared_after_success_invalid_output_and_timeout(self):
        cases = (
            (FixtureDashScopeResponse.from_json(200, fixture("response-text.json")), None),
            (FixtureDashScopeResponse(200, "application/json", b'{"value":Infinity}'),
             "model_output_invalid"),
            (TimeoutError(), "model_timeout"),
        )
        for response, error_code in cases:
            with self.subTest(error_code=error_code):
                captured = []

                def send(_transport, *, body, headers, **_kwargs):
                    captured.append(body)
                    self.assertEqual(headers["X-DashScope-SSE"], "disable")
                    self.assertTrue(body)
                    if isinstance(response, Exception):
                        raise response
                    return response

                seam = DashScopeNativeTransportSeam(FixtureDashScopeTransport([]), max_retries=0)
                with patch.object(FixtureDashScopeTransport, "send", send):
                    if error_code is None:
                        seam.execute_text(fixture("request-text.json"))
                    else:
                        request = fixture('request-text.json')
                        with self.assertRaises(AgentError) as caught:
                            seam.execute_text(request)
                        self.assertEqual(caught.exception.code, error_code)
                self.assertEqual(len(captured), 1)
                self.assertIsInstance(captured[0], bytearray)
                self.assertEqual(captured[0], bytearray())

    def test_response_requires_json_content_type_and_strict_json(self):
        cases = (
            FixtureDashScopeResponse.from_json(
                200,
                fixture("response-text.json"),
                content_type="text/plain",
            ),
            FixtureDashScopeResponse(
                status=200,
                content_type="application/json",
                body=b'{"request_id":"first","request_id":"second"}',
            ),
            FixtureDashScopeResponse(
                status=200,
                content_type="application/json",
                body=b'{"value":NaN}',
            ),
        )
        for response in cases:
            with self.subTest(response=response):
                seam = DashScopeNativeTransportSeam(FixtureDashScopeTransport([response]))
                request = fixture('request-text.json')
                with self.assertRaises(AgentError) as caught:
                    seam.execute_text(request)
                self.assertEqual(caught.exception.code, "model_output_invalid")

    def test_fixture_seam_still_rejects_network_transport(self):
        class NetworkCapableTransport:
            def send(self, **_kwargs):
                raise AssertionError("must remain unreachable")

        transport = NetworkCapableTransport()
        with self.assertRaisesRegex(TypeError, 'FixtureDashScopeTransport'):
            DashScopeNativeTransportSeam(transport)
        self.assertEqual(
            default_provider_adapter_registry().enabled_types(),
            ("dashscope_native", "local_llama_cpp"),
        )


if __name__ == "__main__":
    unittest.main()
