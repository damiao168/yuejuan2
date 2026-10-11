"""用回环合成上游验证三份 Nginx 配置的 authority 转发边界。"""

from __future__ import annotations

import argparse
import http.client
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import threading
import time


CONFIG_DIR = Path(__file__).resolve().parents[1]


class EchoUpstream(BaseHTTPRequestHandler):
    def do_GET(self) -> None:
        if self.path == "/front-redirect":
            self.send_response(302)
            self.send_header("Location", f"http://{self.headers['Host']}/front-landing")
            self.send_header("Content-Length", "0")
            self.end_headers()
            return
        payload = json.dumps({"path": self.path, "headers": dict(self.headers)}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(payload)))
        self.end_headers()
        self.wfile.write(payload)

    def log_message(self, *_: object) -> None:
        pass


def request(port: int, path: str, host: str) -> tuple[int, dict[str, str], bytes]:
    connection = http.client.HTTPConnection("127.0.0.1", port, timeout=5)
    try:
        connection.request("GET", path, headers={
            "Host": host,
            "X-Forwarded-Host": "forged.example",
            "Forwarded": 'host="forged.example";proto=https',
        })
        response = connection.getresponse()
        return response.status, dict(response.getheaders()), response.read()
    finally:
        connection.close()


def free_port() -> int:
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return listener.getsockname()[1]


def check_config(nginx: Path, config_name: str, upstream_port: int) -> dict[str, object]:
    source = (CONFIG_DIR / config_name).read_text(encoding="utf-8")
    port = free_port()
    # 只替换测试监听及上游端点；proxy/header/health/redirect 指令使用仓库原文。
    # Docker DNS 与 Linux 镜像运行身份仍需另外验证，回环测试不替代 Compose 验收。
    adapted = source.replace("listen 80;", f"listen 127.0.0.1:{port};")
    adapted = adapted.replace("api-gateway:8080", f"127.0.0.1:{upstream_port}")
    adapted = adapted.replace("web-admin:80", f"127.0.0.1:{upstream_port}")
    prefix_name = {"nginx.conf": "edugrade", "story056-e2e.conf": "story056", "story060-e2e.conf": "story060"}[config_name]
    with tempfile.TemporaryDirectory(prefix="edugrade-nginx-authority-") as temp:
        prefix = Path(temp)
        (prefix / "logs").mkdir()
        (prefix / "temp").mkdir()
        conf = prefix / "nginx.conf"
        conf.write_text("daemon off;\nmaster_process off;\npid logs/test.pid;\nevents {}\nhttp {\n" + adapted + "\n}\n", encoding="utf-8")
        command = [str(nginx), "-p", prefix.as_posix() + "/", "-c", str(conf)]
        hidden = {"creationflags": subprocess.CREATE_NO_WINDOW} if os.name == "nt" else {}
        syntax = subprocess.run(command + ["-t"], capture_output=True, text=True, timeout=15, **hidden)
        if syntax.returncode:
            raise AssertionError(syntax.stderr)
        process = subprocess.Popen(command, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, **hidden)
        cases: list[dict[str, object]] = []
        try:
            deadline = time.monotonic() + 10
            while True:
                try:
                    request(port, "/health", "localhost")
                    break
                except OSError:
                    if process.poll() is not None or time.monotonic() > deadline:
                        raise AssertionError((prefix / "logs/error.log").read_text())
                    time.sleep(0.05)

            hosts = ["unexpected.example", f"127.0.0.1:{port}", f"gateway.e2e.invalid:{port}"]
            paths = [("/api/echo?limit=1", f"{prefix_name}_api", "/api/echo?limit=1"),
                     ("/ui-test?limit=1", f"{prefix_name}_web", "/ui-test?limit=1")]
            if config_name == "nginx.conf":
                paths.extend((path, "edugrade_api", target) for path, target in [
                    ("/health/live", "/health/live"), ("/health/ready", "/health/ready"),
                    ("/ready", "/ready"), ("/backend-health", "/health/live")])
            for host in hosts:
                for path, upstream_host, upstream_path in paths:
                    status, headers, body = request(port, path, host)
                    assert status == 200, (config_name, path, status)
                    captured = json.loads(body)
                    forwarded = {key.lower(): value for key, value in captured["headers"].items()}
                    assert forwarded["host"] == upstream_host, captured
                    assert "x-forwarded-host" not in forwarded, captured
                    assert "forwarded" not in forwarded, captured
                    assert captured["path"] == upstream_path, captured
                    if config_name == "nginx.conf":
                        assert headers["X-Frame-Options"] == "DENY", headers
                        assert headers["X-Content-Type-Options"] == "nosniff", headers
                    cases.append({"path": path, "incoming_host": host, "upstream_host": forwarded["host"]})

                status, headers, body = request(port, "/health", host)
                assert status == 200 and b"ok\n" in body, (config_name, status, body)
                if config_name == "nginx.conf":
                    assert headers["X-Frame-Options"] == "DENY", headers
                status, headers, _ = request(port, "/front-redirect", host)
                assert status == 302, status
                assert headers["Location"].endswith("/front-landing"), headers
                assert f"{prefix_name}_web" not in headers["Location"], headers
                cases.extend([{"path": "/health", "incoming_host": host, "status": 200},
                              {"path": "/front-redirect", "incoming_host": host, "location": headers["Location"]}])
        finally:
            # 使用当前测试专属 pid 路径停止 nginx，不触碰用户已有的 nginx 或 Docker。
            subprocess.run(command + ["-s", "quit"], capture_output=True, timeout=10, **hidden)
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.terminate()
                process.wait(timeout=5)
        return {"config": config_name, "syntax": syntax.stderr.strip(), "cases": cases}


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--nginx", type=Path, required=True, help="已安装的 nginx 或官方便携版 nginx.exe")
    parser.add_argument("--result", type=Path)
    args = parser.parse_args()
    nginx = args.nginx.resolve()
    hidden = {"creationflags": subprocess.CREATE_NO_WINDOW} if os.name == "nt" else {}
    version = subprocess.run([str(nginx), "-v"], capture_output=True, text=True, timeout=10, **hidden)
    server = ThreadingHTTPServer(("127.0.0.1", 0), EchoUpstream)
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        results = [check_config(nginx, name, server.server_port) for name in
                   ("nginx.conf", "story056-e2e.conf", "story060-e2e.conf")]
    finally:
        server.shutdown()
        server.server_close()
        thread.join(timeout=5)
    report = {"nginx_version": version.stderr.strip(), "platform": os.name,
              "endpoint_adaptation": "只替换 listener 与固定 upstream 地址为回环测试端点",
              "passed": True, "case_count": sum(len(result["cases"]) for result in results), "results": results}
    if args.result:
        args.result.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(json.dumps({"passed": True, "case_count": report["case_count"], "nginx_version": report["nginx_version"]}, ensure_ascii=False))


if __name__ == "__main__":
    main()
