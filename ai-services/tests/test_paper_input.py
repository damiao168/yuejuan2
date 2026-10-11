import base64
import hashlib
import unittest
from unittest.mock import patch

from grading_agent.errors import AgentError
from grading_agent.paper_input import clean_visual_pages, normalize_paper_input


def visual_page(page_no, raw=b"page"):
    return {
        "source_id": "question",
        "document_index": 0,
        "page_no": page_no,
        "media_type": "image/png",
        "data_base64": base64.b64encode(raw).decode("ascii"),
        "sha256": hashlib.sha256(raw).hexdigest(),
    }


class PaperInputTests(unittest.TestCase):
    def setUp(self):
        self.documents = [{"source_id": "question", "document_index": 0}]

    def test_legacy_input_preserves_source_role_and_original_slot_index(self):
        request_id, subject, documents = normalize_paper_input(
            {"request_id": "job", "subject": " MATHEMATICS ", "paper_text": " ", "answer_text": " answer "}
        )
        self.assertEqual((request_id, subject), ("job", "mathematics"))
        self.assertEqual(
            [(document["source_id"], document["document_index"], document["role_hint"], document["content"]) for document in documents],
            [("legacy-answer", 1, "answer", "answer")],
        )

    def test_duplicate_document_identity_is_rejected(self):
        for second in (
            {"source_id": "question", "document_index": 1, "content": "second"},
            {"source_id": "answer", "document_index": 0, "content": "second"},
        ):
            with self.subTest(second=second):
                with self.assertRaisesRegex(AgentError, "unique"):
                    normalize_paper_input(
                        {
                            "request_id": "job",
                            "subject": "数学",
                            "documents": [{"source_id": "question", "document_index": 0, "content": "first"}, second],
                        }
                    )

    def test_visual_pages_are_sorted_after_all_pages_are_validated(self):
        pages = clean_visual_pages([visual_page(2), visual_page(1)], self.documents, "job")
        self.assertEqual([page["page_no"] for page in pages], [1, 2])
        self.assertEqual(self.documents[0]["_visual_page_nos"], [1, 2])

    def test_duplicate_visual_page_is_rejected_before_document_metadata_changes(self):
        page = visual_page(1)
        page_2 = visual_page(1)
        with self.assertRaisesRegex(AgentError, 'identity or media type'):
            clean_visual_pages([page, page_2], self.documents, 'job')
        self.assertNotIn("_visual_page_nos", self.documents[0])

    @patch("grading_agent.paper_input.MAX_VISUAL_PAGE_BYTES", 4)
    @patch("grading_agent.paper_input.MAX_VISUAL_PAYLOAD_BYTES", 7)
    def test_total_decoded_byte_budget_is_independent_of_single_page_budget(self):
        page = visual_page(1)
        page_2 = visual_page(2)
        with self.assertRaisesRegex(AgentError, 'size or checksum'):
            clean_visual_pages([page, page_2], self.documents, 'job')
        self.assertNotIn("_visual_page_nos", self.documents[0])

    def test_invalid_base64_keeps_specific_transport_error(self):
        page = visual_page(1)
        page["data_base64"] = "not!base64"
        with self.assertRaisesRegex(AgentError, "visual page data is invalid"):
            clean_visual_pages([page], self.documents, "job")
