import copy
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

from grading_agent.image_protocol import ImageGradingApplication, OBSERVE_PROMPT, VERSION
from grading_agent.managed_model import create_managed_model
from helpers import settings
from test_image_protocol import CONFIG, StructuredModel, comparison, member, request


def interval_request():
    payload = request()
    payload["members"][0].update(question_no="14", question_type="fill_blank", question_text="读取学生填写的区间")
    return payload


def interval_member(value, answer_type="interval"):
    observed = member()
    observed["confidence"] = .99
    observed["answer"] = {"type": answer_type, "value": value, "parts": []}
    observed["evidence"][0].update(text="学生实际填写：" + value, bbox=[.2, .25, .4, .2])
    return observed


def question14_r6_first_response():
    # 第14题 r6 首轮响应的精简夹具：保留原答案、置信度和证据，只替换题目身份并省略上下文回显。
    # 测试从本文件读取固定数据，不依赖本机 output 目录，也不重新调用模型。
    return {"members": [{
        "question_id": "q1", "presence": "present", "confidence": .7, "association": "matched",
        "answer": {"type": "interval", "value": "[2,+∞)", "parts": []},
        "evidence": [{"evidence_id": "e1", "text": "手写答案 [2,+∞)",
                      "bbox": {"left": .5, "top": .35, "right": .95, "bottom": .85}}],
    }]}


class ImageObservationSinglePassTests(unittest.TestCase):
    def app(self):
        app = ImageGradingApplication(settings(), object())
        app._visual_capabilities[app._model_binding(CONFIG)] = {"visual_supported": True}
        return app

    def test_interval_and_latex_answers_keep_first_value_evidence_and_confidence(self):
        variants = (("[2,+∞)", "interval"), ("(2,+∞)", "interval"),
                    (r"[2,+\infty]", "interval"), (r"(2,+\infty]", "interval"),
                    (r"$\left[2,+\infty\right)$", "latex"), (r"\((2,+\infty)\)", "latex"))
        for value, answer_type in variants:
            with self.subTest(value=value, answer_type=answer_type):
                original = interval_member(value, answer_type)
                model = StructuredModel([{"members": [original]}])
                payload = interval_request()
                with patch("grading_agent.image_protocol.create_managed_model", return_value=model):
                    result, replay = self.app().execute(payload, payload["request_id"], "observe")
                self.assertFalse(replay)
                self.assertEqual([call[0] for call in model.calls], ["student_observation_v3"])
                self.assertEqual(result["observation"]["members"], [original])
                self.assertEqual(result["observation"]["prompt_version"], "student-observation-v3-20261022-r4")
                self.assertEqual(result["telemetry"]["attempts"], 1)
                self.assertEqual(result["telemetry"]["execution_diagnostics"]["provider_request_count"], 1)

    def test_observe_sends_original_student_image_once_without_reference_or_detail_crop(self):
        payload = interval_request()
        payload.update(answer_text="OCR记录", input_mode="image_text")
        original = interval_member(r"[2,+\infty)")
        model = StructuredModel([{"members": [original]}])
        with patch("grading_agent.image_protocol.create_managed_model", return_value=model):
            result, _ = self.app().execute(payload, payload["request_id"], "observe")
        self.assertEqual([call[0] for call in model.calls], ["student_observation_v3"])
        messages = model.calls[0][1]
        parts = messages[1]["content"]
        images = [part for part in parts if part["type"] == "image_url"]
        self.assertEqual(images, [{"type": "image_url", "image_url": {
            "url": "data:image/png;base64," + payload["student_image"]["data"]}}])
        facts = json.loads(parts[0]["text"])
        self.assertEqual(facts["student_text"], payload["answer_text"])
        self.assertEqual(facts["task"], "fill_observation")
        self.assertNotIn("frozen_reference", facts)
        self.assertNotIn("rubric", facts)
        self.assertEqual(result["observation"]["members"], [original])

    def test_question14_r6_first_response_keeps_closed_interval_and_only_one_trace_response(self):
        raw = question14_r6_first_response()
        calls = []

        def transport(_url, body, _headers, _timeout):
            calls.append(copy.deepcopy(body))
            return {"choices": [{"finish_reason": "stop", "message": {
                "role": "assistant", "content": json.dumps(raw, ensure_ascii=False)}}]}

        model = create_managed_model(settings(), CONFIG)
        model.transport = transport
        payload = interval_request()
        with tempfile.TemporaryDirectory() as directory, patch.dict(os.environ, {
                "EDUGRADE_TEMPLATE_ANALYSIS_DIAGNOSTICS_DIR": directory,
                "EDUGRADE_TEMPLATE_ANALYSIS_CAPTURE_CONTENT": "true"}), patch(
                "grading_agent.image_protocol.create_managed_model", return_value=model):
            result, _ = self.app().execute(payload, payload["request_id"], "observe")
            paths = list(Path(directory).glob("*.json"))
            self.assertEqual(len(paths), 1)
            record = json.loads(paths[0].read_text(encoding="utf-8"))

        self.assertEqual(len(calls), 1)
        observed = result["observation"]["members"][0]
        self.assertEqual(observed["answer"], raw["members"][0]["answer"])
        self.assertEqual(observed["answer"]["value"], "[2,+∞)")
        self.assertEqual(observed["confidence"], .7)
        self.assertEqual(observed["presence"], "present")
        self.assertEqual(observed["association"], "matched")
        self.assertEqual(len(observed["evidence"]), 1)
        self.assertEqual(observed["evidence"][0]["evidence_id"], "e1")
        self.assertEqual(observed["evidence"][0]["text"], "手写答案 [2,+∞)")
        # 模型四边坐标仍按既有协议转成 xywh；删除补读不能改变证据文字或重新定位证据框。
        for actual, expected in zip(observed["evidence"][0]["bbox"], [.5, .35, .45, .5]):
            self.assertAlmostEqual(actual, expected)
        self.assertEqual(result["telemetry"]["attempts"], 1)
        self.assertEqual(result["telemetry"]["execution_diagnostics"]["provider_request_count"], 1)
        self.assertEqual(record["metadata"]["status"], "succeeded")
        self.assertEqual(record["metadata"]["versions"]["observation"], OBSERVE_PROMPT)
        self.assertNotIn("interval_read", record["metadata"]["versions"])
        self.assertNotIn("interval_reads", record["metadata"])
        self.assertEqual(len(record["metadata"]["attempts"]), 1)
        self.assertEqual(len(record["responses"]), 1)
        self.assertEqual(json.loads(json.loads(record["responses"][0]["content_json"])), raw)

    def test_saved_observation_grade_only_compares_without_reobserving_or_rewriting(self):
        for prompt in ("student-observation-v3-20261021", "student-observation-v3-20261021-r3", OBSERVE_PROMPT):
            with self.subTest(prompt=prompt):
                payload = interval_request()
                app = self.app()
                saved = {"members": [interval_member("[2,+∞)")], "schema_version": VERSION,
                         "prompt_version": prompt, "input_version_id": payload["input_version_id"],
                         "crop_sha256": payload["student_image"]["sha256"], "model_binding": app._model_binding(CONFIG)}
                payload["observation"] = copy.deepcopy(saved)
                payload["rubric"] = {"version": "v1", "points": [{"id": "p1", "score": 3}]}
                model = StructuredModel([comparison()])
                with patch("grading_agent.image_protocol.create_managed_model", return_value=model):
                    result, _ = app.execute(payload, payload["request_id"], "grade")
                self.assertEqual([call[0] for call in model.calls], ["frozen_rubric_comparison_v3"])
                self.assertEqual(result["observation"], saved)
                self.assertEqual(payload["observation"], saved)
                self.assertEqual(result["telemetry"]["attempts"], 1)
                facts = json.loads(model.calls[0][1][1]["content"][0]["text"])
                self.assertEqual(facts["student_observation"], saved)


if __name__ == "__main__":
    unittest.main()
