import http.client
import importlib.util
from http.server import ThreadingHTTPServer
from pathlib import Path
import threading

import pytest


@pytest.fixture(scope="module")
def authority_server():
    path = Path(__file__).resolve().parents[2] / "infra/docker-compose/nginx/tests/check_proxy_authority.py"
    spec = importlib.util.spec_from_file_location("proxy_authority_fixture", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    server = ThreadingHTTPServer(("127.0.0.1", 0), module.EchoUpstream)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        yield server
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)


def redirect(server, host):
    connection = http.client.HTTPConnection("127.0.0.1", server.server_port, timeout=5)
    try:
        connection.request("GET", "/front-redirect", headers={"Host": host})
        response = connection.getresponse()
        return response.status, response.getheader("Location"), response.read()
    finally:
        connection.close()


@pytest.mark.parametrize("host", ["edugrade_web", "story056_web", "story060_web"])
def test_configured_internal_authority_keeps_redirect_rewrite_fixture(authority_server, host):
    assert redirect(authority_server, host) == (302, f"http://{host}/front-landing", b"")


@pytest.mark.parametrize("host", ["unexpected.example", "edugrade_web:80", "edugrade_web@unexpected.example", "edugrade_web/front-landing"])
def test_unconfigured_authority_is_not_copied_to_response_header(authority_server, host):
    assert redirect(authority_server, host) == (400, None, b"")
