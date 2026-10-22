import copy
import json
import unittest
from pathlib import Path
from decimal import Decimal

from grading_agent.app import GradingAgentApplication
from grading_agent.contract_v2 import (
    compute_media_binding_hash,
    validate_request_v2,
    validate_response_v2,
)
from grading_agent.errors import AgentError
from helpers import FakeModel, settings
from jsonschema import Draft202012Validator
from referencing import Registry, Resource

FIXTURES = Path(__file__).resolve().parents[2] / "contracts" / "grading-agent" / "v2" / "fixtures"


def fixture(name):
    return json.loads(FIXTURES.joinpath(name).read_text(encoding="utf-8"))


class ContractV2Tests(unittest.TestCase):
    def test_valid_image_request_and_response_pass_offline_validation(self):
        request = fixture("valid-request.json")
        response = fixture("valid-response.json")

        self.assertIs(validate_request_v2(request), request)
        self.assertEqual(compute_media_binding_hash(request), request["media_evidence"]["binding_hash"])
        self.assertIs(validate_response_v2(response, request), response)

    def test_v2_accepts_both_canonical_education_stages(self):
        for stage in ("junior", "senior"):
            request = fixture("valid-request.json")
            request["grade_level"] = stage
            with self.subTest(stage=stage):
                self.assertIs(validate_request_v2(request), request)

    def test_v2_json_schema_accepts_senior_mathematics(self):
        schema_path = FIXTURES.parent / "request.schema.json"
        schema = json.loads(schema_path.read_text(encoding="utf-8"))
        self.assertEqual(schema["properties"]["grade_level"]["enum"], ["junior", "senior"])
        self.assertEqual(
            schema["properties"]["subject"]["enum"],
            [
                "chinese", "mathematics", "english", "physics", "chemistry",
                "biology", "history", "geography", "ethics_politics",
            ],
        )

    def test_remote_url_and_whole_page_fixtures_fail_closed(self):
        cases = (
            ("invalid-request-remote-url.json", "encoding must be base64"),
            ("invalid-request-whole-page.json", "near-whole-page"),
        )
        for name, message in cases:
            request = fixture(name)
            with self.subTest(name=name), self.assertRaisesRegex(AgentError, message):
                validate_request_v2(request)

    def test_media_content_attestations_are_recomputed(self):
        cases = (
            ("sha256", "0" * 64, "does not match decoded content"),
            ("byte_size", 67, "byte_size does not match"),
            ("width_pixels", 2, "dimensions do not match"),
            ("binding_hash", "0" * 64, "binding_hash does not match"),
            ("data_base64", "not-base64", "not valid base64"),
        )
        for field, value, message in cases:
            request = fixture("valid-request.json")
            request["media_evidence"][field] = value
            with self.subTest(field=field), self.assertRaisesRegex(AgentError, message):
                validate_request_v2(request)

    def test_media_metadata_and_bbox_fail_before_decoding_content(self):
        for media_changes, message in (
            ({"width_pixels": 0, "data_base64": "not-base64"}, "dimensions are invalid"),
            ({"normalized_bbox": {"x": 0, "y": 0, "width": 1, "height": 1}, "data_base64": "not-base64"}, "near-whole-page"),
        ):
            request = fixture("valid-request.json")
            request["media_evidence"].update(media_changes)
            with self.subTest(message=message), self.assertRaisesRegex(AgentError, message):
                validate_request_v2(request)

    def test_math_formulas_cannot_reuse_a_step_identity(self):
        request = fixture("valid-request.json")
        formula = request["math_evidence"]["formulas"][0]
        formula["id"] = request["math_evidence"]["steps"][0]["id"]
        with self.assertRaisesRegex(AgentError, "formula identity or status"):
            validate_request_v2(request)

    def test_formula_crop_accepts_full_extent_and_pixel_ratio_precision(self):
        schema = json.loads((FIXTURES.parent / "request.schema.json").read_text(encoding="utf-8"), parse_float=Decimal)
        prior_schema = json.loads((FIXTURES.parent.parent / "v1/request.schema.json").read_text(encoding="utf-8"), parse_float=Decimal)
        # 契约引用只从仓库注册，测试不得为相对 $ref 请求网络。
        registry = Registry().with_resource(prior_schema["$id"], Resource.from_contents(prior_schema))
        for bbox in (
            {"x": 0, "y": 0, "width": 1, "height": 1},
            {"x": 1 / 300, "y": 2 / 300, "width": 1 / 3, "height": 1 / 7},
        ):
            request = fixture("valid-request.json")
            original_binding = request["media_evidence"]["binding_hash"]
            request["math_evidence"]["formulas"][0]["bbox"] = bbox
            with self.subTest(bbox=bbox):
                self.assertIs(validate_request_v2(request), request)
                # multipleOf 属于十进制契约，验证时避免二进制 0.1 / 0.000001 舍入造成假失败。
                decimal_request = json.loads(json.dumps(request), parse_float=Decimal)
                self.assertEqual(list(Draft202012Validator(schema, registry=registry).iter_errors(decimal_request)), [])
                self.assertEqual(compute_media_binding_hash(request), original_binding)

    def test_formula_crop_still_rejects_invalid_coordinates(self):
        for bbox in (
            {"x": -0.1, "y": 0, "width": 1, "height": 1},
            {"x": 0.5, "y": 0, "width": 0.6, "height": 1},
            {"x": 0, "y": 0, "width": 0, "height": 1},
            {"x": float("nan"), "y": 0, "width": 1, "height": 1},
            {"x": False, "y": 0, "width": 1, "height": 1},
        ):
            request = fixture("valid-request.json")
            request["math_evidence"]["formulas"][0]["bbox"] = bbox
            with self.subTest(bbox=bbox), self.assertRaises(AgentError) as caught:
                validate_request_v2(request)
            self.assertEqual(caught.exception.code, "invalid_request")

    def test_reference_solution_error_precedes_answer_part_error(self):
        request = fixture("valid-request.json")
        request["reference_context"] = {
            "source": "confirmed_exam_import_snapshot", "snapshot_hash": "a" * 64,
            "standard_answer": None, "equivalent_answers": [], "solution_text": "",
            "solution_steps": [{"step_no": False, "content": "work"}],
            "answer_parts": [{"part_no": 2, "answer_text": "answer"}],
        }
        with self.assertRaisesRegex(AgentError, "solution_steps.step_no"):
            validate_request_v2(request)

    def test_unknown_storage_and_identity_fields_are_rejected(self):
        for field in ("url", "storage_key", "student_name", "final_score"):
            request = fixture("valid-request.json")
            request[field] = "must-not-pass"
            with self.subTest(field=field), self.assertRaisesRegex(AgentError, "forbidden"):
                validate_request_v2(request)

    def test_request_binding_covers_question_segment_crop_and_bbox(self):
        original = fixture("valid-request.json")
        original_hash = compute_media_binding_hash(original)
        mutations = (
            ("question_id", "question-002"),
            ("answer_segment_id", "segment-002"),
        )
        for field, value in mutations:
            changed = copy.deepcopy(original)
            changed[field] = value
            with self.subTest(field=field):
                self.assertNotEqual(compute_media_binding_hash(changed), original_hash)

        changed = copy.deepcopy(original)
        changed["media_evidence"]["normalized_bbox"]["width"] = 0.5
        self.assertNotEqual(compute_media_binding_hash(changed), original_hash)

    def test_candidate_evidence_must_reference_known_math_artifact(self):
        request = fixture("valid-request.json")
        invalid = fixture("invalid-response-unknown-evidence-id.json")
        with self.assertRaises(AgentError) as caught:
            validate_response_v2(invalid, request)
        self.assertEqual(caught.exception.code, "evidence_verification_failed")
        self.assertIn("evidence link is invalid", caught.exception.message)

    def test_model_response_cannot_reintroduce_a_score(self):
        request = fixture("valid-request.json")
        response = fixture("valid-response.json")
        response["suggested_score"] = 4
        with self.assertRaisesRegex(AgentError, "fields"):
            validate_response_v2(response, request)

    def test_v2_remains_unreachable_from_the_v1_application(self):
        app = GradingAgentApplication(settings(), model=FakeModel())
        request = fixture('valid-request.json')
        with self.assertRaises(AgentError) as caught:
            app.grade(request, 'subjective-grade-image-0001')
        self.assertEqual(caught.exception.code, "invalid_request")


if __name__ == "__main__":
    unittest.main()
