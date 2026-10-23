"""Deterministic external model protocol emulator for system integration.

This proves the real grading-agent adapter and persistence path. It is not
model-quality evidence and must never be used outside the isolated E2E stack.
"""

from __future__ import annotations

import json
import os
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer


def _grading_output(request_payload: dict[str, object]) -> dict[str, object]:
    messages = request_payload.get("messages")
    if not isinstance(messages, list):
        raise TypeError("messages must be an array")
    user_message = next(
        item for item in messages if isinstance(item, dict) and item.get("role") == "user"
    )
    content = str(user_message.get("content", ""))
    _, separator, payload_text = content.partition("\n")
    if not separator or not payload_text.strip():
        raise ValueError("missing grading payload")
    grading_request = json.loads(payload_text)
    answer = str(grading_request["untrusted_student_answer"])
    points = grading_request["rubric"]["points"]

    matched_points: list[dict[str, object]] = []
    evidence: list[dict[str, object]] = []
    total_score = 0.0
    for index, point in enumerate(points, start=1):
        evidence_id = f"e{index}"
        score = float(point["score"])
        total_score += score
        matched_points.append(
            {
                "rubric_point_id": point["id"],
                "score": score,
                "evidence_ids": [evidence_id],
            }
        )
        evidence.append(
            {
                "evidence_id": evidence_id,
                "rubric_point_id": point["id"],
                "text_excerpt": answer,
                "location": "answer_text",
                "confidence": 0.9,
            }
        )
    return {
        "suggested_score": total_score,
        "confidence": 0.9,
        "matched_points": matched_points,
        "missing_points": [],
        "deductions": [],
        "evidence": evidence,
        "risk_flags": [],
        "needs_human_review": False,
        "student_feedback": "Deterministic integration fixture; teacher review remains required.",
        "teacher_note": "External model protocol emulator output; not model-quality evidence.",
    }


class Handler(BaseHTTPRequestHandler):
    def do_GET(self) -> None:
        if self.path != "/v1/models":
            self._json(404, {"error": "not_found"})
            return
        self._json(200, {"data": [{"id": "story060-deterministic-model"}]})

    def do_POST(self) -> None:
        expected = f"Bearer {os.environ['EDUGRADE_E2E_MODEL_API_KEY']}"
        if self.path != "/v1/chat/completions" or self.headers.get("Authorization") != expected:
            self._json(401, {"error": "unauthorized"})
            return
        try:
            length = int(self.headers.get("Content-Length", "0"))
            payload = json.loads(self.rfile.read(length).decode("utf-8"))
            output = _grading_output(payload)
        except (KeyError, StopIteration, TypeError, ValueError):
            self._json(400, {"error": "invalid_grading_request"})
            return
        # 模拟完整的供应商完成信封；缺少 finish_reason 时，正式适配器必须拒绝把内容当成完整结果。
        self._json(200, {
            "id": "story060-protocol-fixture", "object": "chat.completion",
            "choices": [{"index": 0, "finish_reason": "stop", "message": {"role": "assistant", "content": json.dumps(output)}}],
            "usage": {"prompt_tokens": 10, "completion_tokens": 10, "total_tokens": 20},
        })

    def _json(self, status: int, payload: dict[str, object]) -> None:
        body = json.dumps(payload).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, _format: str, *_args: object) -> None:
        return


if __name__ == "__main__":
    ThreadingHTTPServer(("0.0.0.0", 8087), Handler).serve_forever()
