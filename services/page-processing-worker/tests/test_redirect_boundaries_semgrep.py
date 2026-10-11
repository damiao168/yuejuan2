from __future__ import annotations

import io
import json
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from types import SimpleNamespace
from urllib import error, request

import pytest
from page_processing.api import APIError, Client, _RejectRedirects


@pytest.mark.parametrize("status", [301, 302, 303, 307, 308])
def test_rejected_redirect_closes_original_response(status):
    response = io.BytesIO(b"synthetic-response")
    req = request.Request("http://127.0.0.1/synthetic")
    headers = {"Location": "http://127.0.0.1/other", "Retry-After": "2"}
    handler = getattr(_RejectRedirects(), f"http_error_{status}")
    with pytest.raises(error.HTTPError) as caught:
        handler(req, response, status, "redirect", headers)
    assert response.closed
    assert caught.value.code == status
    assert caught.value.headers is headers
    caught.value.close()


RESPONSE = {"access_token": "synthetic-access-token", "file": {"id": "asset-1", "hash_sha256": "synthetic-sha256"}}
ACTIONS = ['get', 'post', 'download', 'upload', 'login']


@pytest.fixture(scope="module")
def origins():
    # 两个本机端口代表不同 origin；目标服务记录实际收到的请求，不能只检查重定向异常。
    states = [SimpleNamespace(status=200, location="", received=[], reject_stale=False) for _ in range(2)]
    servers = []
    threads = []
    for state in states:
        def handler_for(state):
            class Handler(BaseHTTPRequestHandler):
                def _respond(self):
                    body = self.rfile.read(int(self.headers.get("Content-Length", "0")))
                    headers = {key.lower(): value for key, value in self.headers.items()}
                    state.received.append({"path": self.path, "headers": headers, "body": body})
                    status = state.status
                    if state.reject_stale and headers.get("x-edugrade-worker-lease-token") != "synthetic-lease":
                        status = 403
                    payload = json.dumps(RESPONSE).encode()
                    self.send_response(status)
                    if state.location:
                        self.send_header("Location", state.location)
                    self.send_header("Content-Type", "application/json")
                    self.send_header("Content-Length", str(len(payload)))
                    self.end_headers()
                    self.wfile.write(payload)

                do_GET = _respond
                do_POST = _respond

                def log_message(self, *_args):
                    pass
            return Handler

        server = ThreadingHTTPServer(("127.0.0.1", 0), handler_for(state))
        thread = threading.Thread(target=server.serve_forever, kwargs={"poll_interval": 0.01}, daemon=True)
        thread.start()
        servers.append(server)
        threads.append(thread)
    try:
        yield SimpleNamespace(
            source=states[0], target=states[1],
            base_url=f"http://127.0.0.1:{servers[0].server_port}",
            target_url=f"http://127.0.0.1:{servers[1].server_port}/collect",
        )
    finally:
        for server, thread in zip(servers, threads):
            server.shutdown()
            thread.join(timeout=2)
            server.server_close()


@pytest.fixture
def gateway(origins, monkeypatch):
    monkeypatch.setenv("NO_PROXY", "127.0.0.1,localhost")
    monkeypatch.setenv("no_proxy", "127.0.0.1,localhost")
    origins.source.status = 200
    origins.source.location = ""
    origins.source.reject_stale = False
    origins.source.received.clear()
    origins.target.received.clear()
    return origins


def _call(action, base_url, lease="synthetic-lease"):
    client = Client(base_url, "synthetic-tenant", "synthetic-worker", "synthetic-password", token="synthetic-access-token")
    task = {"id": "synthetic-task", "lease_token": lease, "payload": {"exam_id": "exam-1"}}
    client.activate_task(task, "synthetic-instance")
    if action == "login":
        client.login()
        return client.token
    if action == "download":
        return client.download("/asset")
    if action == "upload":
        return client.upload_asset(task, "synthetic-asset", "synthetic-owner", "synthetic.png", b"synthetic-png")
    return client._json("GET" if action == "get" else "POST", "/operation", {"synthetic": True})


@pytest.mark.parametrize("action", ACTIONS)
@pytest.mark.parametrize("status", [301, 302, 303, 307, 308])
def test_cross_origin_redirect_never_reaches_target(gateway, action, status):
    gateway.source.status = status
    gateway.source.location = gateway.target_url
    with pytest.raises(APIError) as caught:
        _call(action, gateway.base_url)
    assert isinstance(caught.value.__cause__, error.HTTPError)
    assert caught.value.__cause__.code == status
    assert len(gateway.source.received) == 1
    assert gateway.target.received == []
    sent = gateway.source.received[0]
    if action == "login":
        assert json.loads(sent["body"])["password"] == "synthetic-password"
        assert "authorization" not in sent["headers"]
    elif action == "math":
        assert sent["headers"]["x-edugrade-internal-token"] == "synthetic-internal-token"
    else:
        assert sent["headers"]["authorization"] == "Bearer synthetic-access-token"
        assert sent["headers"]["x-edugrade-worker-lease-token"] == "synthetic-lease"


@pytest.mark.parametrize("action", ACTIONS)
def test_same_origin_redirect_is_also_rejected(gateway, action):
    gateway.source.status = 302
    gateway.source.location = gateway.base_url + "/redirect-target"
    with pytest.raises(APIError) as caught:
        _call(action, gateway.base_url)
    assert isinstance(caught.value.__cause__, error.HTTPError)
    assert caught.value.__cause__.code == 302
    assert len(gateway.source.received) == 1
    assert gateway.target.received == []


@pytest.mark.parametrize("action", ACTIONS)
def test_direct_request_still_succeeds(gateway, action):
    result = _call(action, gateway.base_url)
    assert result
    assert len(gateway.source.received) == 1
    assert gateway.target.received == []


@pytest.mark.parametrize("action", [action for action in ACTIONS if action not in {"login", "math"}])
def test_expired_lease_remains_a_failure(gateway, action):
    # 服务端按租约拒绝过期能力；传输层不能将 403 转成成功或再次请求其他地址。
    gateway.source.reject_stale = True
    with pytest.raises(APIError) as caught:
        _call(action, gateway.base_url, lease="expired-synthetic-lease")
    assert isinstance(caught.value.__cause__, error.HTTPError)
    assert caught.value.__cause__.code == 403
    assert gateway.source.received[0]["headers"]["x-edugrade-worker-lease-token"] == "expired-synthetic-lease"
    assert len(gateway.source.received) == 1
    assert gateway.target.received == []
