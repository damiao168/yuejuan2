import copy
import json
import unittest
from pathlib import Path

from grading_agent.capabilities import CapabilityMatrix
from grading_agent.contract import normalize_model_output, validate_request
from grading_agent.errors import AgentError
from helpers import settings, valid_raw_output, valid_request
from jsonschema import Draft202012Validator


class ContractTests(unittest.TestCase):
    def setUp(self):
        self.settings = settings()
        self.matrix = CapabilityMatrix.load(self.settings.contract_root)

    def _normalize(self, request, raw):
        route = self.matrix.route(request["grade_level"], request["subject"], request["question_type"])
        return normalize_model_output(
            raw,
            request,
            route,
            self.settings.model_version,
            self.settings.prompt_version,
            self.matrix.profile_id,
            {
                "adapter": "local_llama_cpp",
                "provider": "local",
                "deployment": "local-qwen3-4b-q4-k-m",
                "region": "on_premise",
                "attempts": 1,
                "repair_attempted": False,
                "prior_error_codes": [],
                "elapsed_ms": 1,
            },
        )

    def test_valid_request_and_output_are_promoted_without_identity(self):
        request = valid_request()
        validate_request(request)
        suggestion = self._normalize(request, valid_raw_output())
        self.assertEqual(suggestion["suggested_score"], 4)
        self.assertEqual(suggestion["confidence"], 0)
        self.assertTrue(suggestion["needs_human_review"])
        self.assertNotIn("tenant_id", suggestion)
        self.assertNotIn("student_id", suggestion)

    def test_forbidden_identity_field_is_rejected(self):
        request = valid_request()
        request["student_name"] = "not allowed"
        with self.assertRaisesRegex(AgentError, "forbidden"):
            validate_request(request)

    def test_model_final_score_authority_is_rejected(self):
        request = valid_request()
        request["output_constraint"]["allow_model_final_score"] = True
        with self.assertRaisesRegex(AgentError, "deny model final-score authority"):
            validate_request(request)

    def test_unapproved_capability_fails_closed(self):
        request = valid_request()
        request["subject"] = "mathematics"
        validate_request(request)
        with self.assertRaises(AgentError) as caught:
            self.matrix.route(request["grade_level"], request["subject"], request["question_type"], request["request_id"])
        self.assertEqual(caught.exception.code, "capability_not_supported")

    def test_canonical_stages_and_nine_subjects_are_accepted(self):
        canonical_subjects = {
            "chinese", "mathematics", "english", "physics", "chemistry",
            "biology", "history", "geography", "ethics_politics",
        }
        for stage in ("junior", "senior"):
            for subject in canonical_subjects:
                request = valid_request()
                request["grade_level"] = stage
                request["subject"] = subject
                with self.subTest(stage=stage, subject=subject):
                    self.assertIs(validate_request(request), request)

    def test_legacy_stage_and_subject_aliases_are_rejected(self):
        for field, value in (("grade_level", "junior_middle"), ("subject", "math"), ("subject", "politics")):
            request = valid_request()
            request[field] = value
            with self.subTest(field=field, value=value), self.assertRaises(AgentError):
                validate_request(request)

    def test_agent_role_is_bounded_to_blind_panel_roles(self):
        for role in ("single", "primary", "arbiter"):
            request = valid_request()
            request["agent_role"] = role
            self.assertIs(validate_request(request), request)
        request = valid_request()
        request["agent_role"] = "primary_a_with_peer_score"
        with self.assertRaises(AgentError):
            validate_request(request)

    def test_v1_json_schema_uses_the_canonical_assessment_vocabulary(self):
        schema_path = Path(self.settings.contract_root) / "request.schema.json"
        schema = json.loads(schema_path.read_text(encoding="utf-8"))
        validator = Draft202012Validator(schema)
        request = valid_request()
        for stage in ("junior", "senior"):
            request["grade_level"] = stage
            self.assertEqual(list(validator.iter_errors(request)), [])
        request["grade_level"] = "junior_middle"
        self.assertTrue(list(validator.iter_errors(request)))

    def test_evidence_excerpt_must_exist_in_answer(self):
        request = valid_request()
        raw = valid_raw_output()
        raw["evidence"][0]["text_excerpt"] = "invented evidence"
        with self.assertRaises(AgentError) as caught:
            self._normalize(request, raw)
        self.assertEqual(caught.exception.code, "evidence_verification_failed")

    def test_model_total_is_ignored_and_recomputed_from_points(self):
        request = valid_request()
        raw = valid_raw_output()
        raw["suggested_score"] = 1
        self.assertEqual(self._normalize(request, raw)["suggested_score"], 4)

    def test_low_ocr_and_injection_force_canonical_risks(self):
        request = valid_request()
        request["ocr_confidence"] = 0.5
        request["prompt_guard"]["suspected_injection"] = True
        suggestion = self._normalize(request, valid_raw_output())
        self.assertIn("ocr_low_confidence", suggestion["risk_flags"])
        self.assertIn("prompt_injection_suspected", suggestion["risk_flags"])

    def test_non_numeric_request_value_is_rejected_as_invalid_request(self):
        request = valid_request()
        request["max_score"] = "four"
        with self.assertRaises(AgentError) as caught:
            validate_request(request)
        self.assertEqual(caught.exception.code, "invalid_request")
        self.assertEqual(caught.exception.status, 400)

    def test_unhashable_model_identifiers_are_rejected_by_the_model_contract(self):
        request = valid_request()
        malformed_outputs = []

        matched_id = valid_raw_output()
        matched_id["matched_points"][0]["rubric_point_id"] = ["p1"]
        malformed_outputs.append(matched_id)

        evidence_link = valid_raw_output()
        evidence_link["matched_points"][0]["evidence_ids"] = [["e1"]]
        malformed_outputs.append(evidence_link)

        evidence_id = valid_raw_output()
        evidence_id["evidence"][0]["evidence_id"] = {"id": "e1"}
        malformed_outputs.append(evidence_id)

        for raw in malformed_outputs:
            with self.subTest(raw=copy.deepcopy(raw)):
                with self.assertRaises(AgentError) as caught:
                    self._normalize(request, raw)
                self.assertIn(caught.exception.code, {"model_output_invalid", "evidence_verification_failed"})


if __name__ == "__main__":
    unittest.main()
