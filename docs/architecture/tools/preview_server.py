"""仅在回环地址提供架构产物并接收渲染结果，不开放业务目录或任意文件写入。"""
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
import re
from urllib.parse import unquote, urlparse

BASE = Path(__file__).resolve().parents[1]

class Handler(SimpleHTTPRequestHandler):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, directory=str(BASE), **kwargs)

    def log_message(self, *args):
        # 本地预览批量加载图纸时不逐请求打印日志；HTTP 错误仍由响应状态返回。
        pass

    def do_POST(self):
        path = unquote(urlparse(self.path).path)
        if not re.fullmatch(r"/(previews/[0-9A-Za-z_.-]+\.(?:svg|png|html)|evidence/[0-9A-Za-z_.-]+\.json)", path):
            self.send_error(400)
            return
        size = int(self.headers.get("Content-Length", "0"))
        if not 0 < size <= 20 * 1024 * 1024:
            self.send_error(413)
            return
        target = (BASE / path[1:]).resolve()
        if not target.is_relative_to(BASE):
            self.send_error(400)
            return
        target.parent.mkdir(exist_ok=True, parents=True)
        target.write_bytes(self.rfile.read(size))
        self.send_response(200)
        self.end_headers()
        self.wfile.write(b"saved")

if __name__ == "__main__":
    ThreadingHTTPServer(("127.0.0.1", 8766), Handler).serve_forever()
