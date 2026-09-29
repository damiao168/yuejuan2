import base64
import copy
import json
import unittest
from dataclasses import replace
from pathlib import Path

from grading_agent.app import GradingAgentApplication
from grading_agent.dashscope_native_contract import (
    DASHSCOPE_MULTIMODAL_GENERATION_PATH,
    ApprovedImageCrop,
    build_dashscope_multimodal_grading_payload,
    build_dashscope_multimodal_payload,
    parse_dashscope_multimodal_response,
)
from grading_agent.errors import AgentError
from grading_agent.model import PromptRegistry
from helpers import FakeModel, settings, valid_request

FIXTURES = Path(__file__).parent / "fixtures" / "dashscope-native"
IMAGE_BASE64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAusB9Y9Zl1sAAAAASUVORK5CYII="
IMAGE_SHA256 = "d126d616641d42ac8b0a07ec302c9fa1ec049f86200931a0a60c8f3080284b77"
CROP_BINDING = "a" * 64


def fixture(name):
    return json.loads(FIXTURES.joinpath(name).read_text(encoding="utf-8"))


# 单像素合成图只用于裁剪传输契约，不用于衡量图像识别质量。
def approved_crop(**changes):
    crop = ApprovedImageCrop(
        binding=CROP_BINDING,
        scope="answer_segment_crop",
        media_type="image/png",
        data_base64=IMAGE_BASE64,
        sha256=IMAGE_SHA256,
        width_pixels=1,
        height_pixels=1,
        normalized_bbox=(0.1, 0.2, 0.4, 0.3),
    )
    return replace(crop, **changes)


class DashScopeMultimodalContractTests(unittest.TestCase):
    def test_native_multimodal_fixture_matches_offline_builder(self):
        expected = fixture("request-image.json")
        payload = build_dashscope_multimodal_payload(
            model=expected["model"],
            system_message=expected["input"]["messages"][0]["content"],
            user_message=expected["input"]["messages"][1]["content"][1]["text"],
            crop=approved_crop(),
            expected_binding=CROP_BINDING,
            max_completion_tokens=384,
        )
        self.assertEqual(payload, expected)
        self.assertEqual(
            DASHSCOPE_MULTIMODAL_GENERATION_PATH,
            "/services/aigc/multimodal-generation/generation",
        )
        self.assertNotIn("compatible-mode", DASHSCOPE_MULTIMODAL_GENERATION_PATH)

    def test_payload_embeds_one_crop_and_never_uses_remote_or_file_urls(self):
        payload = fixture("request-image.json")
        image = payload["input"]["messages"][1]["content"][0]["image"]
        self.assertTrue(image.startswith("data:image/png;base64,"))
        self.assertNotIn("http://", image)
        self.assertNotIn("https://", image)
        self.assertNotIn("file://", image)
        self.assertNotIn("oss://", image)
        self.assertEqual(json.dumps(payload).count('"image"'), 1)

    def test_governed_projection_omits_ids_and_local_attestation(self):
        current = settings()
        request = valid_request()
        payload = build_dashscope_multimodal_grading_payload(
            grading_request=request,
            prompt_registry=PromptRegistry(current.prompt_root, current.prompt_version),
            model="qwen3-vl-plus",
            crop=approved_crop(),
            expected_binding=CROP_BINDING,
            max_completion_tokens=384,
        )
        serialized = json.dumps(payload, ensure_ascii=False)
        for value in (
            request["request_id"],
            request["question_id"],
            request["answer_segment_id"],
            CROP_BINDING,
            IMAGE_SHA256,
        ):
            self.assertNotIn(value, serialized)

    def test_cross_question_crop_binding_is_rejected(self):
        with self.assertRaisesRegex(ValueError, "not bound"):
            build_dashscope_multimodal_payload(
                model="qwen3-vl-plus",
                system_message="Return JSON.",
                user_message="Inspect the synthetic crop and return JSON.",
                crop=approved_crop(),
                expected_binding="b" * 64,
                max_completion_tokens=384,
            )

    def test_whole_page_and_wrong_scope_are_rejected(self):
        with self.assertRaisesRegex(ValueError, "near-whole-page"):
            build_dashscope_multimodal_payload(
                model="qwen3-vl-plus",
                system_message="Return JSON.",
                user_message="Inspect the synthetic crop and return JSON.",
                crop=approved_crop(normalized_bbox=(0.0, 0.0, 1.0, 1.0)),
                expected_binding=CROP_BINDING,
                max_completion_tokens=384,
            )
        with self.assertRaisesRegex(ValueError, "answer-segment crops"):
            build_dashscope_multimodal_payload(
                model="qwen3-vl-plus",
                system_message="Return JSON.",
                user_message="Inspect the synthetic crop and return JSON.",
                crop=approved_crop(scope="submission_page"),
                expected_binding=CROP_BINDING,
                max_completion_tokens=384,
            )

    def test_mime_content_hash_and_base64_are_verified(self):
        cases = (
            (approved_crop(media_type="image/jpeg"), "media type"),
            (approved_crop(sha256="0" * 64), "content hash"),
            (approved_crop(data_base64="not-base64"), "valid base64"),
        )
        for crop, message in cases:
            with self.subTest(message=message), self.assertRaisesRegex(ValueError, message):
                build_dashscope_multimodal_payload(
                    model="qwen3-vl-plus",
                    system_message="Return JSON.",
                    user_message="Inspect the synthetic crop and return JSON.",
                    crop=crop,
                    expected_binding=CROP_BINDING,
                    max_completion_tokens=384,
                )

    def test_attested_dimensions_must_match_decoded_png(self):
        with self.assertRaisesRegex(ValueError, "decoded PNG"):
            build_dashscope_multimodal_payload(
                model="qwen3-vl-plus",
                system_message="Return JSON.",
                user_message="Inspect the synthetic crop and return JSON.",
                crop=approved_crop(width_pixels=2),
                expected_binding=CROP_BINDING,
                max_completion_tokens=384,
            )

    def test_oversized_crop_dimensions_are_rejected(self):
        with self.assertRaisesRegex(ValueError, "dimensions"):
            build_dashscope_multimodal_payload(
                model="qwen3-vl-plus",
                system_message="Return JSON.",
                user_message="Inspect the synthetic crop and return JSON.",
                crop=approved_crop(width_pixels=4000, height_pixels=4000),
                expected_binding=CROP_BINDING,
                max_completion_tokens=384,
            )

    def test_multimodal_response_preserves_usage_and_passes_grading_contract(self):
        response = fixture("response-image.json")
        original = copy.deepcopy(response)
        result = parse_dashscope_multimodal_response(response)
        self.assertEqual(response, original)
        self.assertEqual(result.request_id, "fixture-dashscope-image-0001")
        self.assertEqual((result.input_tokens, result.output_tokens, result.total_tokens), (264, 184, 448))
        app = GradingAgentApplication(settings(), model=FakeModel([result.output]))
        suggestion, replayed = app.grade(valid_request(), "subjective-grade-0001")
        self.assertFalse(replayed)
        self.assertEqual(suggestion["suggested_score"], 4)
        self.assertTrue(suggestion["needs_human_review"])

    def test_multimodal_response_requires_exactly_one_text_part(self):
        response = fixture("response-image.json")
        response["output"]["choices"][0]["message"]["content"].append({"text": "{}"})
        with self.assertRaisesRegex(AgentError, "exactly one text result"):
            parse_dashscope_multimodal_response(response)
        response = fixture("response-image.json")
        response["output"]["choices"][0]["message"]["content"] = "not-an-array"
        with self.assertRaisesRegex(AgentError, "not an array"):
            parse_dashscope_multimodal_response(response)

    def test_decoded_fixture_is_a_bounded_png(self):
        decoded = base64.b64decode(IMAGE_BASE64, validate=True)
        self.assertLess(len(decoded), 1024)
        self.assertTrue(decoded.startswith(b"\x89PNG\r\n\x1a\n"))


if __name__ == "__main__":
    unittest.main()
