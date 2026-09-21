import copy
import json
import unittest
from pathlib import Path

from grading_agent.app import GradingAgentApplication
from grading_agent.contract_v2 import (
    compute_media_binding_hash,
    validate_request_v2,
    validate_response_v2,
)
from grading_agent.errors import AgentError
from helpers import FakeModel, settings

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
            with self.subTest(name=name), self.assertRaisesRegex(AgentError, message):
                validate_request_v2(fixture(name))

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
        with self.assertRaises(AgentError) as caught:
            app.grade(fixture("valid-request.json"), "subjective-grade-image-0001")
        self.assertEqual(caught.exception.code, "invalid_request")


if __name__ == "__main__":
    unittest.main()
