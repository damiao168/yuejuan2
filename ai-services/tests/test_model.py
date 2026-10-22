import json
import shutil
import tempfile
import unittest
from pathlib import Path
from urllib import error as urlerror

from grading_agent.errors import AgentError
from grading_agent.model import (
    DashScopeNativeAdapter,
    LocalLlamaCppAdapter,
    PromptRegistry,
    _openai_usage,
    grading_output_schema,
)
from grading_agent.paper_parser import PaperParser
from helpers import settings, valid_raw_output, valid_request


class ModelAdapterTests(unittest.TestCase):
    @staticmethod
    def structured_schema():
        return {
            "type": "object",
            "additionalProperties": False,
            "required": ["kind", "count", "items"],
            "properties": {
                "kind": {"type": "string", "enum": ["accepted"]},
                "count": {"type": "number", "minimum": 1, "maximum": 2},
                "items": {
                    "type": "array",
                    "items": {
                        "type": "object",
                        "additionalProperties": False,
                        "required": ["id"],
                        "properties": {"id": {"type": "string"}},
                    },
                },
            },
        }

    @staticmethod
    def structured_adapter(provider, output):
        if provider == "local":
            def transport(_url, _payload, _headers, _timeout):
                content = output if isinstance(output, str) else json.dumps(output)
                return {"choices": [{"finish_reason": "stop", "message": {"role": "assistant", "content": content}}]}

            return LocalLlamaCppAdapter(settings(), transport=transport)

        def transport(_url, _payload, _headers, _timeout):
            content = output if isinstance(output, str) else json.dumps(output)
            return {
                "output": {"choices": [{
                    "finish_reason": "stop",
                    "message": {"role": "assistant", "content": content},
                }]},
                "usage": {"input_tokens": 1, "output_tokens": 1, "total_tokens": 2},
                "request_id": "dashscope-fixture-1",
            }

        return DashScopeNativeAdapter(
            settings(
                adapter_type="dashscope_native",
                model_base_url="https://dashscope.aliyuncs.com/api/v1",
                model_api_key="synthetic-key-with-16-characters",
            ),
            transport=transport,
        )

    def test_llama_cpp_request_uses_strict_schema_and_untrusted_answer_boundary(self):
        calls = []

        def transport(url, payload, headers, timeout):
            calls.append((url, payload, headers, timeout))
            return {
                "choices": [
                    {
                        "finish_reason": "stop",
                        "message": {
                            "role": "assistant",
                            "content": json.dumps(
                                valid_raw_output(), ensure_ascii=False
                            )
                        }
                    }
                ]
            }

        adapter = LocalLlamaCppAdapter(
            settings(model_api_key="model-secret"), transport=transport
        )
        raw = adapter.request(valid_request())
        self.assertEqual(raw["matched_points"][0]["rubric_point_id"], "p1")
        url, payload, headers, _timeout = calls[0]
        self.assertTrue(url.endswith("/chat/completions"))
        self.assertEqual(headers["Authorization"], "Bearer model-secret")
        self.assertTrue(payload["response_format"]["json_schema"]["strict"])
        self.assertFalse(payload["chat_template_kwargs"]["enable_thinking"])
        self.assertIn("untrusted_student_answer", payload["messages"][1]["content"])

    def test_dynamic_schema_restricts_rubric_point_ids(self):
        schema = grading_output_schema(valid_request())
        point_enum = schema["properties"]["matched_points"]["items"]["properties"][
            "rubric_point_id"
        ]["enum"]
        self.assertEqual(point_enum, ["p1", "p2"])

    def test_all_providers_accept_the_same_valid_structured_output(self):
        output = {"kind": "accepted", "count": 1, "items": [{"id": "one"}]}
        for provider in ("local", "dashscope"):
            with self.subTest(provider=provider):
                adapter = self.structured_adapter(provider, output)
                self.assertEqual(
                    adapter.request_structured(
                        "request-1", [], self.structured_schema(), "fixture"
                    ),
                    output,
                )

    # 两种协议共用同一组坏输出，防止切换供应商后放宽本地校验或改变重试语义。
    def test_all_providers_reject_schema_violations_with_identical_semantics(self):
        invalid_outputs = {
            "required": {"kind": "accepted", "count": 1},
            "additional": {
                "kind": "accepted", "count": 1, "items": [], "extra": True
            },
            "enum": {"kind": "rejected", "count": 1, "items": []},
            "range": {"kind": "accepted", "count": 3, "items": []},
            "array item": {
                "kind": "accepted", "count": 1, "items": [{"unknown": "one"}]
            },
        }
        for case, output in invalid_outputs.items():
            for provider in ("local", "dashscope"):
                with self.subTest(case=case, provider=provider):
                    adapter = self.structured_adapter(provider, output)
                    schema = self.structured_schema()
                    with self.assertRaises(AgentError) as raised:
                        adapter.request_structured('request-1', [], schema, 'fixture')
                    self.assertEqual(raised.exception.code, "model_output_invalid")
                    self.assertEqual(raised.exception.status, 502)
                    self.assertFalse(raised.exception.retryable)
                    self.assertEqual(raised.exception.request_id, "request-1")

    def test_all_providers_keep_malformed_json_as_invalid_model_output(self):
        for provider in ("local", "dashscope"):
            with self.subTest(provider=provider):
                adapter = self.structured_adapter(provider, "{not-json")
                schema = self.structured_schema()
                with self.assertRaises(AgentError) as raised:
                    adapter.request_structured('request-1', [], schema, 'fixture')
                self.assertEqual(raised.exception.code, "model_output_invalid")
                self.assertEqual(raised.exception.status, 502)

    def test_wrapped_json_selects_longest_object_and_keeps_first_equal_length_candidate(self):
        accepted = {"kind": "accepted", "count": 1, "items": [{"id": "one"}]}
        for provider in ("local", "dashscope"):
            with self.subTest(provider=provider):
                adapter = self.structured_adapter(
                    provider, '{broken prefix {}\n' + json.dumps(accepted) + '\n{"short":true}'
                )
                self.assertEqual(
                    adapter.request_structured("wrapped", [], self.structured_schema(), "fixture"),
                    accepted,
                )
        schema = {"type": "object", "required": ["n"]}
        adapter = self.structured_adapter("local", 'prefix {"n":1} {"n":2}')
        self.assertEqual(adapter.request_structured("tie", [], schema, "fixture"), {"n": 1})

    def test_usage_aliases_reject_boolean_fractional_and_negative_counts(self):
        cases = (
            ({"prompt_tokens": True, "input_tokens": 7, "completion_tokens": -2,
              "output_tokens": 3, "total_tokens": 0, "prompt_tokens_details": {"cached_tokens": -9},
              "prompt_cache_hit_tokens": 4, "completion_tokens_details": {"reasoning_tokens": 1.5}},
             {"input_tokens": 7, "cached_input_tokens": 4, "output_tokens": 3,
              "reasoning_tokens": 0, "total_tokens": 10}),
            ({"prompt_tokens": 10, "input_tokens": 90, "completion_tokens": 2, "output_tokens": 20,
              "total_tokens": 99, "prompt_tokens_details": {"cached_tokens": 8},
              "prompt_cache_hit_tokens": 5, "completion_tokens_details": {"reasoning_tokens": 2}},
             {"input_tokens": 10, "cached_input_tokens": 8, "output_tokens": 2,
              "reasoning_tokens": 2, "total_tokens": 99}),
            ({"input_tokens": 2.5, "output_tokens": False, "total_tokens": -1,
              "prompt_tokens_details": [], "prompt_cache_hit_tokens": True,
              "completion_tokens_details": "unknown"},
             {"input_tokens": 0, "cached_input_tokens": 0, "output_tokens": 0,
              "reasoning_tokens": 0, "total_tokens": 0}),
        )
        for usage, expected in cases:
            with self.subTest(usage=usage):
                self.assertEqual(_openai_usage({"usage": usage}), expected)
        self.assertEqual(_openai_usage({"usage": []}), {})

    def test_native_strict_json_rejects_nested_duplicates_and_nonfinite_constants(self):
        cases = (b'{"nested":{"same":1,"same":2}}', b'{"value":NaN}',
                 b'{"value":Infinity}', b'{"value":-Infinity}', b'{"value":"\xff"}')
        for raw in cases:
            with self.subTest(raw=raw), self.assertRaises(ValueError):
                DashScopeNativeAdapter._strict_json(raw)
        self.assertEqual(DashScopeNativeAdapter._strict_json(b'{"nested":{"value":1}}'),
                         {"nested": {"value": 1}})

    def test_grading_output_uses_local_schema_validation_for_every_provider(self):
        output = valid_raw_output()
        output.pop("teacher_note")
        for provider in ("local", "dashscope"):
            with self.subTest(provider=provider):
                adapter = self.structured_adapter(provider, output)
                request = valid_request()
                with self.assertRaises(AgentError) as raised:
                    adapter.request(request)
                self.assertEqual(raised.exception.code, "model_output_invalid")

    def test_paper_parser_uses_provider_local_schema_validation(self):
        invalid_paper = {
            "questions": [{
                "question_no": "1", "question_type": "calculation", "score": 10,
                "stem": "计算", "knowledge_points": ["函数"],
                "answer_key": {"standard_answer": "2", "equivalent_answers": [], "tolerance": {}},
                "rubric": {"status": "draft", "max_score": 10, "points": [{"id": "p1", "description": "正确", "score": 10, "required": True}], "deductions": [], "examples": []},
                "confidence": 2, "issues": [],
            }],
            "issues": [],
        }
        adapter = self.structured_adapter("local", invalid_paper)
        parser = PaperParser(adapter)
        with self.assertRaises(AgentError) as raised:
            parser.parse({'request_id': 'paper-1', 'subject': '数学', 'paper_text': '第一题，请计算函数结果。' * 3, 'answer_text': '第一题答案为2。'})
        self.assertEqual(raised.exception.code, "model_output_invalid")

    def test_local_model_maps_request_rejection_without_claiming_unavailability(self):
        def transport(_url, _payload, _headers, _timeout):
            raise urlerror.HTTPError("http://model", 400, "bad request", {}, None)

        adapter = LocalLlamaCppAdapter(settings(), transport=transport)
        request = valid_request()
        with self.assertRaises(AgentError) as raised:
            adapter.request(request)

        self.assertEqual(raised.exception.code, "model_request_rejected")
        self.assertEqual(raised.exception.status, 502)
        self.assertFalse(raised.exception.retryable)

    def test_local_model_maps_rate_limit_as_retryable(self):
        def transport(_url, _payload, _headers, _timeout):
            raise urlerror.HTTPError("http://model", 429, "rate limited", {}, None)

        adapter = LocalLlamaCppAdapter(settings(), transport=transport)
        request = valid_request()
        with self.assertRaises(AgentError) as raised:
            adapter.request(request)

        self.assertEqual(raised.exception.code, "model_rate_limited")
        self.assertTrue(raised.exception.retryable)

    def test_prompt_registry_rejects_unversioned_content_change(self):
        source = Path(settings().prompt_root)
        with tempfile.TemporaryDirectory() as temporary:
            target = Path(temporary)
            shutil.copytree(source, target, dirs_exist_ok=True)
            target.joinpath("base_grading.md").write_text("changed", encoding="utf-8")
            test_settings = settings()
            with self.assertRaisesRegex(ValueError, 'checksum mismatch'):
                PromptRegistry(target, test_settings.prompt_version)

    def test_prompt_registry_routes_by_subject_and_question_type(self):
        registry = PromptRegistry(settings().prompt_root, settings().prompt_version)
        math_request = valid_request()
        math_request.update({"subject": "mathematics", "question_type": "calculation"})
        system = registry.messages(math_request)[0]["content"]
        self.assertIn("【数学·计算与证明题】", system)
        self.assertNotIn("【物理·计算题】", system)

        chinese_request = valid_request()
        chinese_request.update({"subject": "chinese", "question_type": "essay"})
        system = registry.messages(chinese_request)[0]["content"]
        self.assertIn("【语文·作文题】", system)
        self.assertNotIn("【英语·书面表达】", system)

    def test_prompt_registry_separates_primary_and_blind_arbiter_roles(self):
        registry = PromptRegistry(settings().prompt_root, settings().prompt_version)
        primary = valid_request()
        primary["agent_role"] = "primary"
        primary_system = registry.messages(primary)[0]["content"]
        self.assertIn("【独立主评角色】", primary_system)
        self.assertNotIn("【独立盲仲裁角色】", primary_system)

        arbiter = valid_request()
        arbiter["agent_role"] = "arbiter"
        arbiter_system = registry.messages(arbiter)[0]["content"]
        self.assertIn("【独立盲仲裁角色】", arbiter_system)
        self.assertNotIn("【独立主评角色】", arbiter_system)

    def test_prompt_registry_routes_junior_and_senior_stage_guidance(self):
        registry = PromptRegistry(settings().prompt_root, settings().prompt_version)
        junior = valid_request()
        junior["grade_level"] = "junior"
        senior = valid_request()
        senior["grade_level"] = "senior"
        junior_system = registry.messages(junior)[0]["content"]
        senior_system = registry.messages(senior)[0]["content"]
        self.assertIn("【初中学段】", junior_system)
        self.assertNotIn("【高中学段】", junior_system)
        self.assertIn("【高中学段】", senior_system)
        self.assertNotIn("【初中学段】", senior_system)

    def test_prompt_registry_fails_closed_for_unsupported_pair(self):
        registry = PromptRegistry(settings().prompt_root, settings().prompt_version)
        request = valid_request()
        request.update({"subject": "history", "question_type": "calculation"})
        with self.assertRaisesRegex(Exception, "no governed prompt is registered"):
            registry.messages(request)


if __name__ == "__main__":
    unittest.main()
