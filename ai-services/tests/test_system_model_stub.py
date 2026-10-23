"""系统协议桩通过真实 HTTP 适配器验证，避免桩格式过时使整套容器验收失败。"""

import importlib.util
import threading
from http.server import ThreadingHTTPServer
from unittest.mock import patch

from grading_agent.model import LocalLlamaCppAdapter
from helpers import ROOT, settings, valid_request


def test_system_stub_supplies_a_completed_provider_envelope():
    spec = importlib.util.spec_from_file_location("story060_model_stub", ROOT / "tests/e2e/story060_model_stub.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    server = ThreadingHTTPServer(("127.0.0.1", 0), module.Handler)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        model = LocalLlamaCppAdapter(settings(
            model_base_url=f"http://127.0.0.1:{server.server_port}/v1",
            model_api_key="protocol-fixture-key",
        ))
        request = valid_request()
        with patch.dict("os.environ", {"EDUGRADE_E2E_MODEL_API_KEY": "protocol-fixture-key"}), model.session(request["request_id"]):
            output = model.request(request)
        assert output["matched_points"]
        assert model.last_execution_diagnostics()["finish_outcome"] == "completed"
        assert model.last_usage()["total_tokens"] == 20
    finally:
        server.shutdown()
        server.server_close()
        thread.join(2)
