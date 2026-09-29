import copy
import json
import threading
import unittest
from pathlib import Path

from grading_agent.app_v2 import (
    MAX_V2_REQUEST_BYTES,
    GradingAgentV2ApplicationSeam,
    OfflineV2FixtureAdapter,
)
from grading_agent.contract_v2 import compute_media_binding_hash
from grading_agent.errors import AgentError

FIXTURES = Path(__file__).resolve().parents[2] / "contracts" / "grading-agent" / "v2" / "fixtures"


def fixture(name):
    return json.loads(FIXTURES.joinpath(name).read_text(encoding="utf-8"))


def body_size(request):
    return len(json.dumps(request, ensure_ascii=False, separators=(",", ":")).encode("utf-8"))


class ApplicationV2Tests(unittest.TestCase):
    def test_fixture_seam_validates_and_replays_without_caching_media(self):
        request = fixture("valid-request.json")
        adapter = OfflineV2FixtureAdapter(fixture("valid-response.json"))
        events = []
        app = GradingAgentV2ApplicationSeam(adapter, logger=events.append)

        suggestion, replayed = app.grade(
            request,
            request["request_id"],
            body_size=body_size(request),
        )
        replay, replayed_again = app.grade(
            request,
            request["request_id"],
            body_size=body_size(request),
        )

        self.assertFalse(replayed)
        self.assertTrue(replayed_again)
        self.assertEqual(suggestion, replay)
        self.assertTrue(suggestion["needs_human_review"])
        self.assertEqual(len(adapter.calls), 1)
        cached = json.dumps(app._cache, ensure_ascii=False, default=list)
        self.assertNotIn(request["media_evidence"]["data_base64"], cached)
        self.assertNotIn(request["answer_text"], cached)
        logged = json.dumps(events, ensure_ascii=False)
        self.assertNotIn(request["media_evidence"]["data_base64"], logged)
        self.assertNotIn(request["answer_text"], logged)

    def test_transport_seam_has_an_independent_limit_and_rejects_encoding(self):
        request = fixture("valid-request.json")
        adapter = OfflineV2FixtureAdapter(fixture("valid-response.json"))
        app = GradingAgentV2ApplicationSeam(adapter)
        cases = (
            (MAX_V2_REQUEST_BYTES + 1, "", 413),
            (body_size(request), "gzip", 415),
        )
        for size, encoding, status in cases:
            with self.subTest(size=size, encoding=encoding), self.assertRaises(AgentError) as caught:
                app.grade(
                    request,
                    request["request_id"],
                    body_size=size,
                    content_encoding=encoding,
                )
            self.assertEqual(caught.exception.status, status)
        self.assertEqual(adapter.calls, [])

    def test_idempotency_conflict_uses_safe_digest(self):
        request = fixture("valid-request.json")
        adapter = OfflineV2FixtureAdapter(fixture("valid-response.json"))
        app = GradingAgentV2ApplicationSeam(adapter)
        app.grade(request, request["request_id"], body_size=body_size(request))

        changed = copy.deepcopy(request)
        changed["answer_text"] += " changed"
        with self.assertRaises(AgentError) as caught:
            app.grade(changed, changed["request_id"], body_size=body_size(changed))
        self.assertEqual(caught.exception.code, "idempotency_conflict")
        self.assertEqual(len(adapter.calls), 1)

    def test_concurrent_identical_requests_share_one_fixture_inference(self):
        request = fixture("valid-request.json")
        adapter = OfflineV2FixtureAdapter(fixture("valid-response.json"), delay_seconds=0.1)
        app = GradingAgentV2ApplicationSeam(adapter)
        barrier = threading.Barrier(3)
        results = []
        errors = []

        def run():
            try:
                barrier.wait()
                results.append(
                    app.grade(request, request["request_id"], body_size=body_size(request))
                )
            except Exception as exc:  # noqa: BLE001 - test captures thread failures.
                errors.append(exc)

        threads = [threading.Thread(target=run) for _ in range(2)]
        for thread in threads:
            thread.start()
        barrier.wait()
        for thread in threads:
            thread.join()

        self.assertEqual(errors, [])
        self.assertEqual(len(results), 2)
        self.assertEqual(sorted(replayed for _, replayed in results), [False, True])
        self.assertEqual(len(adapter.calls), 1)

    # 两个不同请求各执行一次；fixture seam 的并发上限与同键幂等合并是两条独立规则。
    def test_distinct_v2_requests_are_serialized_to_one_inference(self):
        first = fixture("valid-request.json")
        second = copy.deepcopy(first)
        second["request_id"] = "subjective-grade-image-0002"
        second["media_evidence"]["binding_hash"] = compute_media_binding_hash(second)
        adapter = OfflineV2FixtureAdapter(fixture("valid-response.json"), delay_seconds=0.1)
        app = GradingAgentV2ApplicationSeam(adapter)
        barrier = threading.Barrier(3)
        errors = []

        def run(request):
            try:
                barrier.wait()
                app.grade(request, request["request_id"], body_size=body_size(request))
            except Exception as exc:  # noqa: BLE001 - test captures thread failures.
                errors.append(exc)

        threads = [
            threading.Thread(target=run, args=(first,)),
            threading.Thread(target=run, args=(second,)),
        ]
        for thread in threads:
            thread.start()
        barrier.wait()
        for thread in threads:
            thread.join()

        self.assertEqual(errors, [])
        self.assertEqual(len(adapter.calls), 2)
        self.assertEqual(adapter.max_active_calls, 1)

    def test_invalid_fixture_output_fails_without_caching_a_suggestion(self):
        request = fixture("valid-request.json")
        response = fixture("invalid-response-unknown-evidence-id.json")
        adapter = OfflineV2FixtureAdapter(response)
        events = []
        app = GradingAgentV2ApplicationSeam(adapter, logger=events.append)

        with self.assertRaises(AgentError) as caught:
            app.grade(request, request["request_id"], body_size=body_size(request))
        self.assertEqual(caught.exception.code, "evidence_verification_failed")
        self.assertEqual(len(app._cache), 0)
        self.assertEqual(len(app._inflight), 0)
        logged = json.dumps(events, ensure_ascii=False)
        self.assertNotIn(request["media_evidence"]["data_base64"], logged)
        self.assertNotIn(request["answer_text"], logged)

    def test_external_or_production_adapter_cannot_be_registered(self):
        class UnapprovedAdapter:
            pass

        with self.assertRaisesRegex(TypeError, "OfflineV2FixtureAdapter"):
            GradingAgentV2ApplicationSeam(UnapprovedAdapter())


if __name__ == "__main__":
    unittest.main()
