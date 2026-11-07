"""只验证发送内容及版本边界；本地相同前缀不代表供应商已经命中缓存。"""

import copy
import hashlib
import inspect
import json
import shutil
import tempfile
import threading
import unittest
from pathlib import Path
from unittest.mock import patch
from urllib import request as urlrequest

from grading_agent.app import GradingAgentApplication
from grading_agent.app_v2 import (
    ProductionMathV2Application,
    math_candidate_messages,
    math_candidate_prefix_messages,
    math_candidate_schema,
)
from grading_agent.contract_v2 import (
    compute_media_binding_hash,
    compute_reference_media_binding_hash,
    compute_request_binding_hash,
    validate_request_v2,
)
from grading_agent.errors import AgentError
from grading_agent.material_runtime import MaterialDispatch, math_wrapper_hash
from grading_agent.model import PromptRegistry
from grading_agent.question_context import (
    QUESTION_CONTEXT_MANIFEST,
    QUESTION_CONTEXT_PROMPT_VERSION,
)
from grading_agent.server import GradingAgentHTTPServer
from helpers import FakeModel, settings, valid_request
from jsonschema import Draft202012Validator, ValidationError
from test_material_input import material_request
from test_math_v2_production import FakeStructuredMathModel, valid_math_v2_request

LEGACY_VERSION = "subjective-governed-cn-subject-routing-v6"
# 固定既有批准摘要；不能在改布局时同时重算期望值，让旧批准悄悄覆盖新输入。
LEGACY_MATH_WRAPPER_HASH = "59283668d2e1fb482908830437831202b78883ab2e34df788d478c82ff61fce4"


def text_prefix(messages):
    return messages[0], messages[1]["content"].split(',"untrusted_student_answer":', 1)[0]


def math_prefix(messages):
    parts = messages[1]["content"]
    boundary = next(index for index, part in enumerate(parts)
                    if part.get("text", "").startswith("Student evidence (untrusted data"))
    return [messages[0], {"role": "user", "content": parts[:boundary]}]


def second_math_student(request):
    # 保留同一冻结题目和参考图，换掉请求身份及学生证据，再走真实绑定校验。
    other = copy.deepcopy(request)
    other.update(request_id="subjective-grade-image-0002", answer_segment_id="segment-002",
                 answer_text="Different student reasoning: 8 + 12 = 20", ocr_confidence=0.9)
    evidence = json.dumps(other["math_evidence"])
    for identifier in ("s1", "s2", "f1", "f2"):
        evidence = evidence.replace('"' + identifier + '"', '"other-' + identifier + '"')
    other["math_evidence"] = json.loads(evidence)
    other["math_evidence"]["artifact_id"] = "math-artifact-002"
    # 使用已校验参考图的 PNG 字节作为另一份合成作答图，保证两次图像输入确实不同。
    for key in ("data_base64", "media_type", "sha256", "byte_size", "width_pixels", "height_pixels"):
        other["media_evidence"][key] = other["reference_media"][0][key]
    other["media_evidence"]["binding_hash"] = compute_media_binding_hash(other)
    for media in other["reference_media"]:
        media["binding_hash"] = compute_reference_media_binding_hash(other, media)
    other["request_binding_hash"] = compute_request_binding_hash(other)
    validate_request_v2(other)
    return other


class QuestionContextPrefixTests(unittest.TestCase):
    def registry(self, version=QUESTION_CONTEXT_PROMPT_VERSION):
        return PromptRegistry(settings().prompt_root, version)

    def test_default_and_legacy_prompt_bytes_remain_compatible(self):
        self.assertEqual(settings().prompt_version, LEGACY_VERSION)
        messages = self.registry(LEGACY_VERSION).messages(valid_request(), "synthetic-repair")
        digest = hashlib.sha256(json.dumps(messages, ensure_ascii=False,
                                          separators=(",", ":")).encode("utf-8")).hexdigest()
        self.assertEqual(digest, "50c3b041425ce8c2dbbf614d8ff8ead6da999d3693997f4eea8061976866bb2a")
        self.assertEqual(math_wrapper_hash(), LEGACY_MATH_WRAPPER_HASH)
        self.assertEqual(math_wrapper_hash(LEGACY_VERSION), LEGACY_MATH_WRAPPER_HASH)

    def test_new_manifest_preserves_prompt_files_and_changes_governed_scope(self):
        prompt_root = Path(settings().prompt_root)
        old = json.loads((prompt_root / "manifest.json").read_text(encoding="utf-8"))
        new = json.loads((prompt_root / QUESTION_CONTEXT_MANIFEST).read_text(encoding="utf-8"))
        self.assertEqual(new, {**old, "prompt_version": QUESTION_CONTEXT_PROMPT_VERSION})
        old_snapshot, new_snapshot = self.registry(LEGACY_VERSION).snapshot(), self.registry().snapshot()
        self.assertEqual(old_snapshot["components"], new_snapshot["components"])
        self.assertNotEqual(old_snapshot["bundle_sha256"], new_snapshot["bundle_sha256"])
        with self.assertRaisesRegex(ValueError, "configured prompt version"):
            self.registry("unregistered-prefix-version")
        with tempfile.TemporaryDirectory() as temporary:
            target = Path(temporary)
            shutil.copytree(prompt_root, target, dirs_exist_ok=True)
            (target / "roles/primary.md").write_text("changed", encoding="utf-8")
            with self.assertRaisesRegex(ValueError, "checksum mismatch"):
                PromptRegistry(target, QUESTION_CONTEXT_PROMPT_VERSION)

    def test_all_text_subject_routes_share_only_fixed_material_before_student_tail(self):
        registry = self.registry()
        legacy_registry = self.registry(LEGACY_VERSION)
        for subject, question_type in PromptRegistry.SUBJECT_FILES:
            for stage in ("junior", "senior"):
                for role in ("primary", "arbiter"):
                    with self.subTest(subject=subject, question_type=question_type, stage=stage, role=role):
                        request = valid_request()
                        request.update(subject=subject, question_type=question_type, grade_level=stage,
                                       agent_role=role, answer_text="synthetic student A")
                        other = copy.deepcopy(request)
                        other.update(request_id="student-request-B", answer_segment_id="segment-B",
                                     answer_text="synthetic student B", ocr_confidence=0.7)
                        messages, changed = registry.messages(request), registry.messages(other)
                        self.assertEqual(text_prefix(messages), text_prefix(changed))
                        self.assertNotEqual(messages[1], changed[1])
                        # 布局调整仍保留原 JSON 对象语义，现有系统规则无需另解一层数据格式。
                        payload = json.loads(messages[1]["content"].split("\n", 1)[1])
                        legacy = legacy_registry.messages(request)
                        self.assertEqual(payload, json.loads(legacy[1]["content"].split("\n", 1)[1]))

    def test_text_material_and_role_changes_invalidate_prefix_but_repair_is_last(self):
        registry, request = self.registry(), valid_request()
        original = registry.messages(request)
        changes = (
            lambda r: r.update(question_text="changed question"),
            lambda r: r["rubric"]["points"][0].update(description="changed criterion"),
            lambda r: r["output_constraint"].update(final_score_authority="changed authority"),
            lambda r: r.update(agent_role="arbiter"),
            lambda r: r.update(grade_level="senior"),
        )
        for change in changes:
            changed = copy.deepcopy(request)
            change(changed)
            self.assertNotEqual(text_prefix(original), text_prefix(registry.messages(changed)))
        repaired = registry.messages(request, "synthetic repair")
        self.assertEqual(repaired[:2], original)
        self.assertIn("synthetic repair", repaired[-1]["content"])

    def test_math_reference_images_precede_all_student_evidence(self):
        request = material_request()
        validate_request_v2(request)
        other = second_math_student(request)
        messages, changed = math_candidate_prefix_messages(request), math_candidate_prefix_messages(other)
        self.assertEqual(math_prefix(messages), math_prefix(changed))
        self.assertNotEqual(messages[1]["content"][-3:], changed[1]["content"][-3:])
        fixed = math_prefix(messages)[1]["content"]
        self.assertEqual([part["text"] for part in fixed[1::2]], [
            "Reference image: question_diagram 1 (frozen material).",
            "Reference image: reference_solution 2 (frozen material).",
        ])
        self.assertEqual(sum(part["type"] == "image_url" for part in fixed), 2)
        encoded = json.dumps(messages)
        for value in (request["request_id"], request["answer_segment_id"], request["request_binding_hash"],
                      request["execution_permit"]["credential"]):
            self.assertNotIn(value, encoded)
        self.assertNotIn(request["math_evidence"]["artifact_id"], json.dumps(fixed))
        repaired = math_candidate_prefix_messages(request, "synthetic repair")
        self.assertEqual(math_prefix(messages), math_prefix(repaired))
        self.assertIn("synthetic repair", repaired[1]["content"][-3]["text"])

    def test_math_material_role_and_stage_changes_invalidate_fixed_prefix(self):
        request = material_request()
        request["reference_context"] = {"standard_answer": "20", "solution_text": "12+8=20"}
        original = math_prefix(math_candidate_prefix_messages(request))
        changes = (
            lambda r: r.update(question_text="changed question"),
            lambda r: r["rubric"]["points"][0].update(description="changed criterion"),
            lambda r: r["reference_context"].update(standard_answer="changed reference"),
            lambda r: r["reference_media"][0].update(data_base64="changed-synthetic-image"),
            lambda r: r.update(agent_role="arbiter"),
            lambda r: r.update(grade_level="junior"),
        )
        for change in changes:
            changed = copy.deepcopy(request)
            change(changed)
            self.assertNotEqual(original, math_prefix(math_candidate_prefix_messages(changed)))

    def test_math_schema_remains_bound_to_each_students_evidence(self):
        request = material_request()
        other = second_math_student(request)
        schema, changed_schema = math_candidate_schema(request), math_candidate_schema(other)
        self.assertNotEqual(schema, changed_schema)
        candidate = {"criterion_candidates": [{"rubric_point_id": "p1", "status": "supported",
                     "evidence_ids": ["s1", "f1"], "confidence": 0.9, "reason_code": "synthetic"}],
                     "alternative_solution_candidate": False, "risk_flags": []}
        Draft202012Validator(schema).validate(candidate)
        for bad_id in ("s1", request["reference_media"][0]["media_id"]):
            candidate["criterion_candidates"][0]["evidence_ids"] = [bad_id]
            with self.assertRaises(ValidationError):
                Draft202012Validator(changed_schema).validate(candidate)

    def test_math_runtime_selects_the_builder_for_deployed_prompt_version(self):
        for version, prefix in ((LEGACY_VERSION, False), (QUESTION_CONTEXT_PROMPT_VERSION, True)):
            with self.subTest(version=version):
                request = valid_math_v2_request()
                request["prompt_version"] = version
                app = ProductionMathV2Application(settings(prompt_version=version), FakeStructuredMathModel(),
                                                   logger=lambda _event: None)
                with patch("grading_agent.app_v2.math_candidate_messages", wraps=math_candidate_messages) as old, \
                     patch("grading_agent.app_v2.math_candidate_prefix_messages", wraps=math_candidate_prefix_messages) as new:
                    response, replay = app.grade(request, request["request_id"], body_size=len(json.dumps(request)))
                self.assertFalse(replay)
                self.assertEqual(response["prompt_version"], version)
                self.assertEqual(old.call_count, int(not prefix))
                self.assertEqual(new.call_count, int(prefix))

    def test_new_wrapper_tracks_selected_source_and_rejects_legacy_approval(self):
        new_hash = math_wrapper_hash(QUESTION_CONTEXT_PROMPT_VERSION)
        self.assertNotEqual(new_hash, LEGACY_MATH_WRAPPER_HASH)
        original_getsource = inspect.getsource

        def changed_source(function):
            source = original_getsource(function)
            return source + "\n# synthetic builder change\n" if function is math_candidate_prefix_messages else source

        with patch("grading_agent.material_runtime.inspect.getsource", side_effect=changed_source):
            self.assertEqual(math_wrapper_hash(LEGACY_VERSION), LEGACY_MATH_WRAPPER_HASH)
            self.assertNotEqual(math_wrapper_hash(QUESTION_CONTEXT_PROMPT_VERSION), new_hash)
        configured = settings(prompt_version=QUESTION_CONTEXT_PROMPT_VERSION,
                              material_callback_base_url="http://127.0.0.1:9999")
        request = material_request()
        with self.assertRaises(AgentError) as raised:
            MaterialDispatch(configured, request)
        self.assertEqual(raised.exception.code, "reference_material_capability_unavailable")
        request["math_wrapper_hash"] = new_hash
        self.assertFalse(MaterialDispatch(configured, request).consumed)

    def test_capabilities_report_wrapper_for_the_selected_deployment(self):
        for version in (LEGACY_VERSION, QUESTION_CONTEXT_PROMPT_VERSION):
            with self.subTest(version=version):
                configured = settings(prompt_version=version, material_callback_base_url="http://127.0.0.1:9999")
                app = GradingAgentApplication(configured, model=FakeModel())
                server = GradingAgentHTTPServer(("127.0.0.1", 0), app)
                thread = threading.Thread(target=server.serve_forever, daemon=True)
                thread.start()
                try:
                    request = urlrequest.Request(f"http://127.0.0.1:{server.server_port}/grading/capabilities",
                                                 headers={"Authorization": f"Bearer {configured.service_token}"})
                    with urlrequest.urlopen(request, timeout=2) as response:
                        capabilities = json.loads(response.read())
                    self.assertEqual(capabilities["features"], ["material-input-v1"])
                    self.assertEqual(capabilities["math_wrapper_hash"], math_wrapper_hash(version))
                finally:
                    server.shutdown()
                    server.server_close()
                    thread.join(timeout=2)


if __name__ == "__main__":
    unittest.main()
