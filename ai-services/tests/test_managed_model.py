import json
import socket
import unittest
from types import SimpleNamespace
from unittest.mock import Mock, patch

from grading_agent.errors import AgentError
from grading_agent.managed_model import paper_model, public_json_transport
from grading_agent.model import _decode_structured_content
from helpers import settings


def config(name="school-a"):
    return {"adapter_type": "openai_compatible", "base_url": "https://models.example.test/v1",
            "api_key": f"synthetic-secret-for-{name}", "model_name": name, "model_version": "v1"}


class ManagedPaperModelTests(unittest.TestCase):
    def setUp(self):
        self.app = SimpleNamespace(settings=settings(), model=object())

    def test_school_models_are_request_scoped_and_credentials_are_not_prompted(self):
        payload_a = {"managed_model": config()}
        payload_b = {"managed_model": config("school-b")}
        a = paper_model(self.app, payload_a)
        b = paper_model(self.app, payload_b)
        self.assertNotIn("managed_model", payload_a)
        self.assertEqual(a.settings.model_name, "school-a")
        self.assertEqual(b.settings.model_name, "school-b")
        self.assertNotEqual(a.settings.model_api_key, b.settings.model_api_key)
        self.assertIs(paper_model(self.app, {}), self.app.model)
        schema = {"type": "object", "required": ["ok"], "properties": {"ok": {"type": "boolean"}}}
        with patch("grading_agent.managed_model.public_json_transport", return_value={"choices": [{"message": {"content": '{"ok":true}'}}]}) as send:
            self.assertEqual(a.request_structured("request-a", [{"role": "user", "content": "synthetic exam"}], schema, "paper"), {"ok": True})
            url, body, headers, _ = send.call_args.args
            self.assertEqual(url, "https://models.example.test/v1/chat/completions")
            self.assertEqual(headers["Authorization"], "Bearer " + config()["api_key"])
            self.assertNotIn(config()["api_key"], json.dumps(body))
            self.assertEqual(body["response_format"], {"type": "json_object"})
            self.assertNotIn("chat_template_kwargs", body)

    def test_managed_output_still_requires_schema_validation(self):
        model = paper_model(self.app, {"managed_model": config()})
        with patch("grading_agent.managed_model.public_json_transport", return_value={"choices": [{"message": {"content": '{}'}}]}):
            with self.assertRaises(AgentError) as error:
                model.request_structured("test", [], {"type": "object", "required": ["ok"]}, "test")
            self.assertEqual(error.exception.code, "model_output_invalid")

    def test_compatible_model_accepts_a_single_json_object_inside_provider_wrapping(self):
        self.assertEqual(
            _decode_structured_content("```json\n{\"ok\":true}\n```", "wrapped"),
            {"ok": True},
        )
        self.assertEqual(
            _decode_structured_content("以下是结果：\n{\"ok\":true}", "prefixed"),
            {"ok": True},
        )

    def test_openai_compatible_model_preserves_visual_parts_and_expands_output_budget(self):
        model = paper_model(self.app, {"managed_model": config()})
        messages = [{"role": "user", "content": [
            {"type": "text", "text": "page 1"},
            {"type": "image_url", "image_url": {"url": "data:image/png;base64,cG5n", "detail": "original"}},
        ]}]
        schema = {"type": "object", "required": ["ok"], "properties": {"ok": {"type": "boolean"}}}
        with patch("grading_agent.managed_model.public_json_transport", return_value={
            "choices": [{"message": {"content": '{"ok":true}'}}],
            "usage": {
                "prompt_tokens": 2400, "completion_tokens": 600, "total_tokens": 3000,
                "prompt_tokens_details": {"cached_tokens": 120},
                "prompt_cache_hit_tokens": 180,
            },
        }) as send:
            self.assertEqual(model.request_structured("visual", messages, schema, "visual"), {"ok": True})

        body = send.call_args.args[1]
        self.assertEqual(body["messages"][0]["role"], "system")
        self.assertIn("Return a JSON object matching this schema", body["messages"][0]["content"])
        self.assertEqual(body["messages"][-1]["content"][1]["type"], "image_url")
        self.assertEqual(body["messages"][-1]["content"][1]["image_url"]["detail"], "original")
        self.assertEqual(body["max_tokens"], 8_192)
        self.assertEqual(model.last_usage(), {
            "input_tokens": 2400, "cached_input_tokens": 180,
            "output_tokens": 600, "reasoning_tokens": 0, "total_tokens": 3000,
        })

    def test_deepseek_visual_request_explicitly_disables_default_thinking(self):
        deepseek = {
            **config("deepseek-flash"),
            "base_url": "https://api.deepseek.com",
            "model_version": "v4.1",
        }
        model = paper_model(self.app, {"managed_model": deepseek})
        schema = {"type": "object", "required": ["ok"], "properties": {"ok": {"type": "boolean"}}}
        with patch("grading_agent.managed_model.public_json_transport", return_value={"choices": [{"message": {"content": '{"ok":true}'}}]}) as send:
            model.request_structured("deepseek-visual", [{"role": "user", "content": "exam"}], schema, "visual")

        body = send.call_args.args[1]
        self.assertEqual(body["thinking"], {"type": "disabled"})
        self.assertEqual(body["reasoning_effort"], "none")

    def test_invalid_managed_config_never_falls_back_to_local_model(self):
        for changes in [{"base_url": "http://example.test"}, {"adapter_type": "unsupported"},
                        {"base_url": "https://user:pass@example.test"}, {"api_key": ""},
                        {"adapter_type": "dashscope_native"}]:
            with self.subTest(changes=changes), self.assertRaises(AgentError):
                paper_model(self.app, {"managed_model": {**config(), **changes}})

    def test_native_adapter_uses_native_generation_protocol(self):
        native = {**config(), "adapter_type": "dashscope_native", "base_url": "https://dashscope.aliyuncs.com/api/v1"}
        with patch("grading_agent.managed_model.public_json_transport", return_value={"output": {"choices": [{"message": {"content": '{}'}}]}}) as send:
            model = paper_model(self.app, {"managed_model": native})
            model.request_structured("test", [], {"type": "object"}, "test")
            self.assertTrue(send.call_args.args[0].endswith("/services/aigc/text-generation/generation"))

    def test_private_or_mixed_dns_is_rejected_before_any_connection(self):
        for addresses in [["127.0.0.1"], ["169.254.169.254"], ["10.0.0.1"], ["8.8.8.8", "::1"]]:
            resolved = [(socket.AF_INET, socket.SOCK_STREAM, 6, "", (ip, 443)) for ip in addresses]
            with patch("socket.getaddrinfo", return_value=resolved), patch("http.client.HTTPSConnection") as connect:
                with self.assertRaises(AgentError):
                    public_json_transport("https://example.test/v1", {}, {}, 1)
                connect.assert_not_called()

    def test_public_transport_pins_ip_and_never_follows_redirect(self):
        response = Mock(status=302)
        connection = Mock()
        connection.getresponse.return_value = response
        resolved = [(socket.AF_INET, socket.SOCK_STREAM, 6, "", ("8.8.8.8", 443))]
        with patch("socket.getaddrinfo", return_value=resolved), patch("http.client.HTTPSConnection", return_value=connection) as factory:
            with self.assertRaises(AgentError):
                public_json_transport("https://example.test/v1", {}, {}, 1)
            self.assertEqual(factory.call_args.args[0], "example.test")
            with patch("socket.create_connection") as connect:
                connection._create_connection(("example.test", 443), 1)
                self.assertEqual(connect.call_args.args[0], ("8.8.8.8", 443))
            connection.request.assert_called_once()
            connection.close.assert_called_once()
