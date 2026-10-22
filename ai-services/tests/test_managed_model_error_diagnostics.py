import http.client
import json
import socket
from unittest.mock import Mock, patch

import pytest
from grading_agent.errors import SAFE_UPSTREAM_ERROR_CODES, AgentError
from grading_agent.managed_model import MAX_PROVIDER_ERROR_BYTES, public_json_transport


def transport_error(status, body=None, read_error=None):
    response = Mock(status=status)
    response.read.return_value = body
    response.read.side_effect = read_error
    connection = Mock()
    connection.getresponse.return_value = response
    addresses = [(socket.AF_INET, socket.SOCK_STREAM, 6, "", ("8.8.8.8", 443))]
    with (patch("socket.getaddrinfo", return_value=addresses),
          patch("http.client.HTTPSConnection", return_value=connection), pytest.raises(AgentError) as caught):
        public_json_transport("https://example.test/v1", {}, {}, 1)
    response.read.assert_called_once_with(MAX_PROVIDER_ERROR_BYTES + 1)
    connection.close.assert_called_once()
    return caught.value


@pytest.mark.parametrize("provider_code", sorted(SAFE_UPSTREAM_ERROR_CODES))
def test_provider_diagnostics_only_preserve_allowlisted_error_codes(provider_code):
    body = json.dumps({"error": {"code": provider_code, "message": "private-key-and-student-content"}}).encode()
    error = transport_error(400, body)
    assert error.code == "model_request_rejected"
    assert error.message == "provider returned HTTP 400"
    assert error.upstream_diagnostics() == {"upstream_http_status": 400, "upstream_error_code": provider_code}
    assert "private-key-and-student-content" not in json.dumps(error.payload())
    assert "upstream_http_status" not in error.payload()["error"]


@pytest.mark.parametrize("status, code", [(401, "model_auth_failed"), (403, "model_auth_failed"),
                                        (429, "model_rate_limited"), (503, "model_request_rejected")])
def test_http_diagnostics_do_not_change_existing_model_error_classification(status, code):
    error = transport_error(status, b'{"error":{"code":"unknown-private-provider-code"}}')
    assert error.code == code
    assert error.status == 502
    assert error.upstream_diagnostics() == {"upstream_http_status": status}
    assert "unknown-private-provider-code" not in str(error)


@pytest.mark.parametrize("provider_code", ["private-key-and-student-content", "invalid_parameter: private-key",
                                           {"echo": "private-key"}, ["invalid_api_key"], 42, None])
def test_provider_error_code_echo_or_wrong_type_is_discarded(provider_code):
    body = json.dumps({"error": {"code": provider_code, "message": "private-key-and-student-content"}}).encode()
    error = transport_error(422, body)
    assert error.upstream_diagnostics() == {"upstream_http_status": 422}
    assert "private-key" not in json.dumps(error.payload())


@pytest.mark.parametrize("body", [b"not-json-private-key", b"\xff", b"[]", b'{"error":"private-key"}',
                                 b'{"error":{"code":"invalid_api_key"},"echo":"' + b"x" * MAX_PROVIDER_ERROR_BYTES])
def test_invalid_or_oversized_provider_error_keeps_http_status_without_raw_body(body):
    error = transport_error(400, body)
    assert error.upstream_diagnostics() == {"upstream_http_status": 400}
    assert error.message == "provider returned HTTP 400"


def test_deeply_nested_provider_error_keeps_original_http_error_without_body_echo():
    body = b"[" * 2000 + b'"private-provider-body"' + b"]" * 2000
    assert len(body) < MAX_PROVIDER_ERROR_BYTES
    error = transport_error(404, body)
    assert error.code == "model_request_rejected"
    assert error.upstream_diagnostics() == {"upstream_http_status": 404}
    assert error.message == "provider returned HTTP 404"
    assert "private-provider-body" not in json.dumps(error.payload())


@pytest.mark.parametrize("read_error", [OSError("private-read-error"), http.client.IncompleteRead(b"private-body")])
def test_provider_error_read_failure_does_not_replace_original_http_status(read_error):
    error = transport_error(400, read_error=read_error)
    assert error.upstream_diagnostics() == {"upstream_http_status": 400}
    assert "private" not in json.dumps(error.payload())


def test_successful_http_response_with_incomplete_body_is_a_safe_transport_failure():
    response = Mock(status=200)
    response.headers = {"Content-Type": "application/json"}
    response.read.side_effect = http.client.IncompleteRead(b"private-student-body", 100)
    connection = Mock()
    connection.getresponse.return_value = response
    addresses = [(socket.AF_INET, socket.SOCK_STREAM, 6, "", ("8.8.8.8", 443))]
    with (patch("socket.getaddrinfo", return_value=addresses),
          patch("http.client.HTTPSConnection", return_value=connection), pytest.raises(AgentError) as caught):
        public_json_transport("https://private-provider.example.test/v1", {}, {"Authorization": "Bearer private-key"}, 1)
    error = caught.value
    assert error.code == "model_unavailable"
    assert error.status == 503
    assert error.retryable is True
    assert error.upstream_diagnostics() == {}
    assert "private" not in json.dumps(error.payload())
    assert "private" not in str(error)
    connection.request.assert_called_once()
    response.read.assert_called_once_with(4_000_001)
    connection.close.assert_called_once()


@pytest.mark.parametrize("status", [99, 600, True, "400", None])
def test_agent_error_rejects_untrusted_upstream_status_values(status):
    error = AgentError("model_request_rejected", "request failed", upstream_http_status=status,
                       upstream_error_code="echo-private-key")
    assert error.upstream_diagnostics() == {}
