import io
import json
from email.message import Message
from unittest.mock import patch
from urllib.error import HTTPError

import pytest
from image_quality.api import APIError, EduGradeImageQualityClient


def test_retry_reuses_only_the_same_page_normalized_asset():
    client = EduGradeImageQualityClient.__new__(EduGradeImageQualityClient)
    job = {"submission_page_id": "page", "submission_id": "answer", "exam_id": "exam"}
    original = {"id": "existing", "owner_type": "submission_page_normalized", "owner_id": "page", "submission_id": "answer", "hash_sha256": "hash"}
    for field, value in [(None, None), ("owner_id", "another-page"), ("submission_id", "another-answer"), ("hash_sha256", "another-hash")]:
        existing = dict(original)
        if field:
            existing[field] = value
        data = json.dumps({"error": {"code": "duplicate_file"}, "existing_file": existing}).encode()
        headers = Message()
        headers["Content-Type"] = "application/json"
        headers["Content-Length"] = str(len(data))
        conflict = HTTPError("http://api/upload", 409, "duplicate", headers, io.BytesIO(data))
        from urllib.request import Request
        with patch.object(client, "_build_request", return_value=Request("http://api/upload")), patch("image_quality.api._open_request", side_effect=conflict):
            if field:
                with pytest.raises(APIError):
                    client.upload_normalized({}, job, b"png", "hash")
            else:
                assert client.upload_normalized({}, job, b"png", "hash") == "existing"
