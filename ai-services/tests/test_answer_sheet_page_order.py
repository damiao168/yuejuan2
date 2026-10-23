import base64
import copy
import hashlib
import json
from contextlib import nullcontext
from unittest.mock import patch
from urllib import error as urlerror
from urllib import request as urlrequest

import cv2
import numpy as np
import pytest
from grading_agent.answer_sheet_page_order import (
    SCHEMA_VERSION,
    AnswerSheetPageOrderAnalyzer,
)
from grading_agent.errors import AgentError
from helpers import settings
from test_template_analysis import (
    http_server as http_server,  # noqa: PLC0414 - pytest 通过同名导入复用服务 fixture。
)


def page(number):
    image = np.full((80, 120, 3), 255, np.uint8)
    cv2.putText(image, str(number), (5, 50), cv2.FONT_HERSHEY_SIMPLEX, 1, (0, 0, 0), 2)
    raw = cv2.imencode(".jpg", image)[1].tobytes()
    return {"page_id": f"p{number}", "width": 120, "height": 80, "media_type": "image/jpeg",
            "data_base64": base64.b64encode(raw).decode(), "sha256": hashlib.sha256(raw).hexdigest()}


def payload(count=2):
    return {"request_id": "page-order-test-request-0001", "schema_version": SCHEMA_VERSION,
            "pages": [page(number) for number in range(count, 0, -1)]}


class PageOrderModel:
    def __init__(self, count=2, transform=None):
        self.settings = settings(model_name="vision-fixture", model_version="fixture-v1", provider_key="fixture")
        self.calls = []
        self.count = count
        self.transform = transform

    def session(self, _request_id):
        return nullcontext()

    def request_structured(self, request_id, messages, schema, name):
        self.calls.append((request_id, messages, schema, name))
        ids = [json.loads(part["text"])["page_id"] for part in messages[1]["content"] if part["type"] == "text"]
        output = {"pages": [{"page_id": key, "printed_page_no": int(key[1:]), "printed_total_pages": self.count,
                             "question_numbers": [], "starts_with_continuation": False}
                            for key in ids], "document_conflict": False,
                  "proposed_order": sorted(ids, key=lambda key: int(key[1:])), "order_basis": "printed_page_numbers"}
        if self.transform:
            self.transform(output, len(self.calls))
        return output


def test_ordering_uses_real_image_parts_and_only_validated_printed_metadata():
    request = payload()
    original = copy.deepcopy(request)
    model = PageOrderModel()
    result = AnswerSheetPageOrderAnalyzer(model).analyze(request)
    assert request == original
    assert result["status"] == "ordered" and result["page_order"] == ["p1", "p2"]
    assert result["reason_codes"] == ["PAGE_ORDER_DOES_NOT_VERIFY_STUDENT_IDENTITY"]
    parts = model.calls[0][1][1]["content"]
    assert len(parts) == 4
    assert parts[1]["image_url"]["url"].endswith(request["pages"][0]["data_base64"])
    assert parts[3]["image_url"]["detail"] == "high"
    system = model.calls[0][1][0]["content"]
    assert "不要求每张都有页码或总页数" in system and "禁止抄录任何实际姓名" in system


def test_one_hundred_pages_are_batched_and_merged_into_a_complete_sequence():
    model = PageOrderModel(100)
    result = AnswerSheetPageOrderAnalyzer(model).analyze(payload(100))
    assert result["status"] == "ordered"
    assert result["page_order"] == [f"p{number}" for number in range(1, 101)]
    assert len(model.calls) == 13
    assert all(len(call[1][1]["content"]) <= 16 for call in model.calls)


@pytest.mark.parametrize("case,reason", [
    ("missing page number", "PAGE_ORDER_EVIDENCE_INSUFFICIENT"),
    ("repeated number", "DUPLICATE_PRINTED_PAGE_NUMBER"),
    ("question number used as page", "PRINTED_TOTAL_PAGE_COUNT_CONFLICT"),
    ("missing last page", "PRINTED_TOTAL_PAGE_COUNT_CONFLICT"),
    ("different documents", "MULTIPLE_DOCUMENTS_SUSPECTED"),
    ("repeated id", "MODEL_OUTPUT_INVALID"),
    ("personal value", "MODEL_OUTPUT_INVALID"),
])
def test_uncertain_page_evidence_never_publishes_partial_or_guessed_order(case, reason):
    def change(output, _batch):
        row = output["pages"][0]
        if case == "missing page number": row["printed_page_no"] = None
        elif case == "repeated number": row["printed_page_no"] = 1
        elif case == "question number used as page": row["printed_page_no"] = 17
        elif case == "missing last page": row["printed_total_pages"] = 3
        elif case == "different documents": output["document_conflict"] = True
        elif case == "repeated id": row["page_id"] = "p1"
        else: row["student_name"] = "测试学生甲"
    result = AnswerSheetPageOrderAnalyzer(PageOrderModel(transform=change)).analyze(payload())
    assert result["status"] == "uncertain" and result["page_order"] == []
    assert reason in result["reason_codes"]
    assert "测试学生甲" not in json.dumps(result, ensure_ascii=False)


def test_missing_printed_total_does_not_block_unambiguous_page_order():
    def change(output, _batch):
        for row in output["pages"]:
            row["printed_total_pages"] = None
    result = AnswerSheetPageOrderAnalyzer(PageOrderModel(transform=change)).analyze(payload())
    assert result["status"] == "ordered" and result["page_order"] == ["p1", "p2"]
    assert "PAGE_COUNT_NOT_VERIFIED" in result["reason_codes"]


def test_question_ranges_order_unpaginated_pages_across_batches():
    def change(output, _batch):
        for row in output["pages"]:
            number = int(row["page_id"][1:])
            row.update(printed_page_no=None, printed_total_pages=None, question_numbers=[number * 3, number * 3 + 1])
        output.update(order_basis="question_sequence")
    result = AnswerSheetPageOrderAnalyzer(PageOrderModel(10, change)).analyze(payload(10))
    assert result["status"] == "ordered"
    assert result["page_order"] == [f"p{number}" for number in range(1, 11)]
    assert "PAGE_ORDER_FROM_QUESTION_SEQUENCE" in result["reason_codes"]


def test_continuation_can_order_same_question_without_using_input_order():
    def change(output, _batch):
        for row in output["pages"]:
            row.update(printed_page_no=None, printed_total_pages=None, question_numbers=[17],
                       starts_with_continuation=row["page_id"] == "p2")
        output.update(order_basis="content_continuity", proposed_order=["p1", "p2"])
    result = AnswerSheetPageOrderAnalyzer(PageOrderModel(transform=change)).analyze(payload())
    assert result["status"] == "ordered" and result["page_order"] == ["p1", "p2"]
    assert "PAGE_ORDER_FROM_CONTENT_CONTINUITY" in result["reason_codes"]


def test_unpaginated_pages_with_no_content_evidence_remain_uncertain():
    def change(output, _batch):
        for row in output["pages"]:
            row.update(printed_page_no=None, printed_total_pages=None)
        output.update(order_basis="uncertain", proposed_order=[])
    result = AnswerSheetPageOrderAnalyzer(PageOrderModel(transform=change)).analyze(payload())
    assert result["status"] == "uncertain" and result["page_order"] == []
    assert "PAGE_ORDER_EVIDENCE_INSUFFICIENT" in result["reason_codes"]


def test_failure_in_later_batch_does_not_return_the_previous_batch_order():
    def fail_later(_output, batch):
        if batch == 2: raise AgentError("model_timeout", "private provider body", status=504)
    model = PageOrderModel(10, fail_later)
    result = AnswerSheetPageOrderAnalyzer(model).analyze(payload(10))
    assert result["status"] == "uncertain" and result["page_order"] == []
    assert len(model.calls) == 2 and "MODEL_TIMEOUT" in result["reason_codes"]
    assert "private provider" not in json.dumps(result)


def test_time_budget_ends_between_batches_without_issuing_more_model_calls():
    ticks = iter([0, 0, 181])
    model = PageOrderModel(10)
    result = AnswerSheetPageOrderAnalyzer(model, clock=lambda: next(ticks)).analyze(payload(10))
    assert result["status"] == "uncertain" and result["page_order"] == []
    assert result["reason_codes"][-1] == "PAGE_ORDER_TIME_BUDGET_EXCEEDED" and len(model.calls) == 1


@pytest.mark.parametrize("change", ["hash", "dimensions", "duplicate id", "page id", "extra credentials", "page count", "image too big", "wrong media"])
def test_invalid_images_and_page_identity_fail_before_spending_model_calls(change):
    request = payload()
    if change == "hash": request["pages"][0]["sha256"] = "a" * 64
    elif change == "dimensions": request["pages"][0]["width"] = 1001
    elif change == "duplicate id": request["pages"][0]["page_id"] = "p1"
    elif change == "page id": request["pages"][0]["page_id"] = "student/name"
    elif change == "extra credentials": request["managed_model"] = {"api_key": "private"}
    elif change == "page count": request["pages"] = []
    elif change == "image too big": request["pages"][0]["data_base64"] = "A" * 200000
    else: request["pages"][0]["media_type"] = "image/png"
    model = PageOrderModel()
    with pytest.raises(AgentError) as caught:
        AnswerSheetPageOrderAnalyzer(model).analyze(request)
    assert caught.value.status == 400 and model.calls == []


def test_duplicate_page_images_do_not_get_a_fabricated_sequence():
    request = payload()
    request["pages"][0] = {**request["pages"][1], "page_id": "p2"}
    model = PageOrderModel()
    result = AnswerSheetPageOrderAnalyzer(model).analyze(request)
    assert result["status"] == "uncertain" and result["page_order"] == []
    assert "PAGE_IMAGE_DUPLICATED" in result["reason_codes"] and model.calls == []


def test_internal_page_order_endpoint_authenticates_and_uses_managed_vision(http_server):
    base_url, app = http_server
    body = payload()
    body["managed_model"] = {"gateway_fixture": True}
    request = urlrequest.Request(f"{base_url}/answer-sheet/page-order", data=json.dumps(body).encode(),
                                 headers={"Content-Type": "application/json"}, method="POST")
    with pytest.raises(urlerror.HTTPError) as caught: urlrequest.urlopen(request, timeout=3)
    assert caught.value.code == 401
    request.add_header("Authorization", f"Bearer {app.settings.service_token}")
    model = PageOrderModel()
    def select(_app, value):
        value.pop("managed_model")
        return model
    with patch("grading_agent.server.paper_model", side_effect=select), urlrequest.urlopen(request, timeout=3) as response:
        result = json.loads(response.read())
    assert result["status"] == "ordered" and result["page_order"] == ["p1", "p2"]
