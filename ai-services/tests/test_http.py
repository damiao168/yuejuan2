import json
import threading
import unittest
from urllib import error as urlerror
from urllib import request as urlrequest

from grading_agent.app import GradingAgentApplication
from grading_agent.server import GradingAgentHTTPServer
from helpers import FakeModel, settings, valid_request


class HTTPTests(unittest.TestCase):
    def setUp(self):
        self.settings = settings()
        self.app = GradingAgentApplication(self.settings, model=FakeModel())
        self.server = GradingAgentHTTPServer(("127.0.0.1", 0), self.app)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.base_url = f"http://127.0.0.1:{self.server.server_port}"

    def tearDown(self):
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=2)

    def test_health_and_readiness(self):
        with urlrequest.urlopen(f"{self.base_url}/health", timeout=2) as response:
            health = json.loads(response.read())
        with urlrequest.urlopen(f"{self.base_url}/ready", timeout=2) as response:
            ready = json.loads(response.read())
        self.assertEqual(health["mode"], "shadow")
        self.assertEqual(ready["status"], "ready")

        with urlrequest.urlopen(f"{self.base_url}/ready", timeout=2) as response:
            repeated = json.loads(response.read())
        self.assertEqual(repeated["status"], "ready")
        self.assertEqual(self.app.model.ready_calls, 1)

    def test_grade_requires_service_auth_and_idempotency(self):
        body = json.dumps(valid_request(), ensure_ascii=False).encode("utf-8")
        unauthorized = urlrequest.Request(
            f"{self.base_url}/grading/grade",
            data=body,
            headers={"Content-Type": "application/json"},
            method="POST",
        )
        with self.assertRaises(urlerror.HTTPError) as caught:
            urlrequest.urlopen(unauthorized, timeout=2)
        self.assertEqual(caught.exception.code, 401)

        grading_request = valid_request()
        authorized = urlrequest.Request(
            f"{self.base_url}/grading/grade",
            data=json.dumps(grading_request, ensure_ascii=False).encode("utf-8"),
            headers={
                "Content-Type": "application/json",
                "Authorization": f"Bearer {self.settings.service_token}",
                "Idempotency-Key": grading_request["request_id"],
            },
            method="POST",
        )
        with urlrequest.urlopen(authorized, timeout=2) as response:
            suggestion = json.loads(response.read())
            replay = response.headers["Idempotent-Replay"]
        self.assertEqual(suggestion["status"], "suggestion")
        self.assertEqual(replay, "false")

    def test_current_prompt_requires_auth_and_reports_loaded_content(self):
        request = urlrequest.Request(
            f"{self.base_url}/grading/prompts/current",
            headers={"Authorization": f"Bearer {self.settings.service_token}"},
        )
        with urlrequest.urlopen(request, timeout=2) as response:
            prompt = json.loads(response.read())["prompt"]
        self.assertEqual(prompt["prompt_version"], self.settings.prompt_version)
        self.assertFalse(prompt["mutable_at_runtime"])
        self.assertEqual(len(prompt["bundle_sha256"]), 64)
        component_keys = {item["key"] for item in prompt["components"]}
        self.assertIn("base", component_keys)
        self.assertIn("structured", component_keys)
        self.assertIn("subject.chinese.essay", component_keys)
        self.assertIn("subject.mathematics.calculation", component_keys)
        self.assertIn("role.primary", component_keys)
        self.assertIn("role.arbiter", component_keys)
        self.assertIn("stage.junior", component_keys)
        self.assertIn("stage.senior", component_keys)
        self.assertNotIn("calculation", component_keys)

    def test_paper_parse_can_stream_factual_progress_and_result(self):
        paper = {
            "request_id": "paper-stream-1",
            "subject": "数学",
            "documents": [
                {
                    "source_id": "source-1",
                    "file_asset_id": "file-1",
                    "document_index": 0,
                    "role_hint": "auto",
                    "content": "北师保研分享会\n会议号：120732118\n发起人：任辰红\n最近入会\n参会时长\n回放",
                    "blocks": [],
                }
            ],
        }
        request = urlrequest.Request(
            f"{self.base_url}/paper/parse",
            data=json.dumps(paper, ensure_ascii=False).encode("utf-8"),
            headers={
                "Content-Type": "application/json",
                "Authorization": f"Bearer {self.settings.service_token}",
                "Accept": "application/x-ndjson",
            },
            method="POST",
        )

        with urlrequest.urlopen(request, timeout=2) as response:
            self.assertIn("application/x-ndjson", response.headers["Content-Type"])
            events = [json.loads(line) for line in response if line.strip()]

        self.assertEqual(events[0]["type"], "progress")
        self.assertEqual(events[-1]["type"], "result")
        self.assertEqual(events[-2]["progress"]["route"], "unrelated_guard")
        self.assertEqual(events[-1]["result"]["question_candidates"], [])


if __name__ == "__main__":
    unittest.main()
