import copy
import json
from contextlib import contextmanager
from pathlib import Path

from grading_agent.config import Settings

ROOT = Path(__file__).resolve().parents[2]


def settings(**overrides):
    values = {
        "host": "127.0.0.1",
        "port": 0,
        "service_token": "test-service-token-with-at-least-32-characters",
        "contract_root": str(ROOT / "contracts" / "grading-agent" / "v1"),
        "prompt_root": str(ROOT / "ai-services" / "prompts"),
        "model_timeout_seconds": 1,
        "model_ready_timeout_seconds": 1,
        "model_queue_timeout_seconds": 1,
        "model_max_retries": 1,
    }
    values.update(overrides)
    return Settings(**values)


def valid_request():
    path = ROOT / "contracts" / "grading-agent" / "v1" / "fixtures" / "valid-request.json"
    return json.loads(path.read_text(encoding="utf-8"))


def valid_raw_output():
    return {
        "suggested_score": 0,
        "confidence": 0.91,
        "matched_points": [
            {"rubric_point_id": "p1", "score": 2, "evidence_ids": ["e1"]},
            {"rubric_point_id": "p2", "score": 2, "evidence_ids": ["e2"]},
        ],
        "missing_points": [],
        "deductions": [],
        "evidence": [
            {
                "evidence_id": "e1",
                "rubric_point_id": "p1",
                "text_excerpt": "对家乡有责任感",
                "location": "answer_text",
                "confidence": 0.9,
            },
            {
                "evidence_id": "e2",
                "rubric_point_id": "p2",
                "text_excerpt": "帮助村里的孩子继续读书",
                "location": "answer_text",
                "confidence": 0.9,
            },
        ],
        "risk_flags": [],
        "needs_human_review": False,
        "student_feedback": "model feedback is not authoritative",
        "teacher_note": "model note is not authoritative",
    }


class FakeModel:
    def __init__(self, outputs=None, ready=True, usages=None):
        self.outputs = list(outputs or [valid_raw_output()])
        self.usages = list(usages or [])
        self._last_usage = {}
        self.calls = []
        self.ready_value = ready
        self.ready_calls = 0

    @contextmanager
    def session(self, request_id):
        self.calls.append(("session", request_id))
        yield

    def request(self, request, repair_reason=None):
        self.calls.append(("request", request["request_id"], repair_reason))
        if not self.outputs:
            raise AssertionError("fake model received more calls than configured")
        output = self.outputs.pop(0)
        self._last_usage = copy.deepcopy(self.usages.pop(0)) if self.usages else {}
        if isinstance(output, Exception):
            raise output
        return copy.deepcopy(output)

    def last_usage(self):
        return copy.deepcopy(self._last_usage)

    def ready(self):
        self.ready_calls += 1
        return self.ready_value
