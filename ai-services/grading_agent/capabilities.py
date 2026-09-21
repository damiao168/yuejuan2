import json
from pathlib import Path

from .errors import AgentError


class CapabilityMatrix:
    def __init__(self, payload):
        self.payload = payload
        self._validate()

    @classmethod
    def load(cls, contract_root):
        path = Path(contract_root) / "capability-matrix.json"
        with path.open("r", encoding="utf-8") as handle:
            return cls(json.load(handle))

    def _validate(self):
        if self.payload.get("profile_id") != "local-pilot-v1":
            raise ValueError("unsupported capability profile")
        if self.payload.get("final_grade_publication_allowed") is not False:
            raise ValueError("capability matrix must disable final grade publication")
        if self.payload.get("grade_levels") != ["junior", "senior"]:
            raise ValueError("local pilot must use canonical junior/senior stages")
        for item in self.payload.get("capabilities", []):
            if item.get("review_policy") != "always":
                raise ValueError("all model-backed capabilities must require review")
            if item.get("question_type") in {"essay", "discussion"} and item.get("delivery") != "shadow_only":
                raise ValueError("long-form capabilities must remain shadow-only")

    @property
    def profile_id(self):
        return self.payload["profile_id"]

    def route(self, grade_level, subject, question_type, request_id=""):
        if grade_level not in self.payload["grade_levels"]:
            raise AgentError(
                "capability_not_supported",
                "grade level is outside the approved grading-agent profile",
                status=422,
                request_id=request_id,
            )
        for item in self.payload["capabilities"]:
            if item["question_type"] == question_type and item["subject"] in {subject, "*"}:
                return dict(item)
        raise AgentError(
            "capability_not_supported",
            "subject and question type are outside the approved grading-agent profile",
            status=422,
            request_id=request_id,
        )
