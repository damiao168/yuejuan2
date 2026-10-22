"""用真实 Nginx 验证双层代理的 OCR Worker 策略及模型文件失败边界。"""

from __future__ import annotations

import argparse
import http.client
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time


ROOT = Path(__file__).resolve().parents[4]
WORKER_PATH = "/assets/worker.min-OCR_test-09.js"
MODEL_PATH = "/ocr/eng.traineddata.gz"
SECURITY_HEADERS = {
    "x-frame-options": "DENY",
    "x-content-type-options": "nosniff",
    "referrer-policy": "strict-origin-when-cross-origin",
    "permissions-policy": "camera=(), microphone=(), geolocation=(), payment=(), usb=()",
}


def free_port() -> int:
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", 0))
        return listener.getsockname()[1]


def request(port: int, path: str, headers: dict[str, str] | None = None) -> tuple[int, list[tuple[str, str]], bytes]:
    connection = http.client.HTTPConnection("127.0.0.1", port, timeout=5)
    try:
        connection.request("GET", path, headers=headers or {})
        response = connection.getresponse()
        return response.status, response.getheaders(), response.read()
    finally:
        connection.close()


def values(headers: list[tuple[str, str]], name: str) -> list[str]:
    return [value for key, value in headers if key.lower() == name]


def check_policy(headers: list[tuple[str, str]], expected_count: int, wasm_allowed: bool) -> None:
    policies = values(headers, "content-security-policy")
    assert len(policies) == expected_count, policies
    for policy in policies:
        directives = {parts[0]: parts[1:] for item in policy.split(";") if (parts := item.split())}
        expected_scripts = ["'self'", "'wasm-unsafe-eval'"] if wasm_allowed else ["'self'"]
        assert directives["script-src"] == expected_scripts, policy
        assert directives["connect-src"] == ["'self'"], policy
        assert directives["object-src"] == ["'none'"], policy
    for name, expected in SECURITY_HEADERS.items():
        assert values(headers, name) == [expected] * expected_count, (name, headers)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--nginx", type=Path, required=True)
    parser.add_argument("--result", type=Path)
    args = parser.parse_args()
    nginx = args.nginx.resolve()
    hidden = {"creationflags": subprocess.CREATE_NO_WINDOW} if os.name == "nt" else {}
    version = subprocess.run([str(nginx), "-v"], capture_output=True, text=True, timeout=10, **hidden)
    cases: list[dict[str, object]] = []
    with tempfile.TemporaryDirectory(prefix="edugrade-ocr-nginx-") as temp:
        prefix = Path(temp)
        web_root = prefix / "html"
        (web_root / "assets").mkdir(parents=True)
        (web_root / "ocr").mkdir()
        (prefix / "logs").mkdir()
        (prefix / "temp").mkdir()
        entry = b"<html>synthetic SPA entry</html>"
        model = b"synthetic OCR model: static serving only"
        (web_root / "index.html").write_bytes(entry)
        (web_root / WORKER_PATH.removeprefix("/")).write_bytes(b"postMessage('synthetic worker');")
        (web_root / "assets/main-test.js").write_bytes(b"console.log('synthetic main');")
        model_path = web_root / MODEL_PATH.removeprefix("/")
        model_path.write_bytes(model)
        app_port, edge_port = free_port(), free_port()
        app = (ROOT / "apps/web-admin/nginx.conf").read_text(encoding="utf-8")
        app = app.replace("listen 80;", f"listen 127.0.0.1:{app_port};")
        app = app.replace("root /usr/share/nginx/html;", f'root "{web_root.as_posix()}";')
        edge = (ROOT / "infra/docker-compose/nginx/nginx.conf").read_text(encoding="utf-8")
        edge = edge.replace("listen 80;", f"listen 127.0.0.1:{edge_port};")
        edge = edge.replace("web-admin:80", f"127.0.0.1:{app_port}")
        edge = edge.replace("api-gateway:8080", f"127.0.0.1:{app_port}")
        # 只改监听、静态根和上游地址；双层 CSP、MIME、缓存与 try_files 均执行仓库配置。
        conf = prefix / "nginx.conf"
        mime = nginx.parent / "conf/mime.types"
        assert mime.is_file(), f"Missing Nginx MIME table: {mime}"
        conf.write_text('daemon off;\nmaster_process off;\npid logs/test.pid;\nevents {}\nhttp {\n'
                        + f'include "{mime.as_posix()}";\ndefault_type application/octet-stream;\n'
                        + app + "\n" + edge + "\n}\n", encoding="utf-8")
        command = [str(nginx), "-p", prefix.as_posix() + "/", "-c", str(conf)]
        syntax = subprocess.run(command + ["-t"], capture_output=True, text=True, timeout=15, **hidden)
        assert syntax.returncode == 0, syntax.stderr
        process = subprocess.Popen(command, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, **hidden)
        try:
            deadline = time.monotonic() + 10
            while True:
                try:
                    request(edge_port, "/health")
                    break
                except OSError:
                    if process.poll() is not None or time.monotonic() > deadline:
                        raise AssertionError((prefix / "logs/error.log").read_text())
                    time.sleep(.05)
            for label, port, policy_count in [("static", app_port, 1), ("proxy", edge_port, 2)]:
                for path in ["/index.html", "/assets/main-test.js", "/admin/exams/test", "/assets/worker.min-test.js.extra"]:
                    status, headers, _ = request(port, path)
                    assert status == 200, (label, path, status)
                    check_policy(headers, policy_count, False)
                    cases.append({"layer": label, "path": path, "status": status, "wasm_allowed": False})
                status, headers, _ = request(port, WORKER_PATH + "?version=1")
                assert status == 200, (label, headers)
                assert values(headers, "content-type") == ["application/javascript"], headers
                check_policy(headers, policy_count, True)
                cases.append({"layer": label, "path": WORKER_PATH, "status": status, "wasm_allowed": True})
                status, headers, body = request(port, MODEL_PATH)
                assert status == 200 and body == model, (label, status, body)
                assert "no-cache" in values(headers, "cache-control"), headers
                assert not values(headers, "etag"), headers
                check_policy(headers, policy_count, False)
                status, _, body = request(port, MODEL_PATH, {"If-None-Match": '"old-model"', "If-Modified-Since": "Wed, 01 Jan 2098 00:00:00 GMT"})
                assert status == 200 and body == model, (label, status, body)
                cases.append({"layer": label, "path": MODEL_PATH, "status": status, "conditional_request": "fresh body"})
                status, headers, body = request(port, "/assets/worker.min-missing.js")
                assert status == 404 and body != entry, (label, status, body)
                check_policy(headers, policy_count, True)
                cases.append({"layer": label, "path": "/assets/worker.min-missing.js", "status": status})
            # 仅删除当前临时目录的合成模型，模拟发布产物缺失，验证不能以 SPA 200 掩盖错误。
            model_path.unlink()
            for label, port, policy_count in [("static", app_port, 1), ("proxy", edge_port, 2)]:
                status, headers, body = request(port, MODEL_PATH)
                assert status == 404 and body != entry, (label, status, body)
                check_policy(headers, policy_count, False)
                cases.append({"layer": label, "path": MODEL_PATH, "status": status, "missing_model": True})
        finally:
            # 只停止本测试独有 pid 路径的 Nginx；不操作本机其他 Nginx 或 Docker 服务。
            subprocess.run(command + ["-s", "quit"], capture_output=True, timeout=10, **hidden)
            try:
                process.wait(timeout=5)
            except subprocess.TimeoutExpired:
                process.terminate()
                process.wait(timeout=5)
    report = {"passed": True, "case_count": len(cases), "nginx_version": version.stderr.strip(),
              "scope": "真实 Nginx 双层回环 HTTP；合成静态文件，不替代真实 OCR 与 Docker DNS 验收", "cases": cases}
    if args.result:
        args.result.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(json.dumps({key: report[key] for key in ["passed", "case_count", "nginx_version"]}, ensure_ascii=False))


if __name__ == "__main__":
    main()
