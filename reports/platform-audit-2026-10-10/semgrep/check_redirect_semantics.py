"""仅构建标准库重定向请求，使用合成凭据；不联网、不启动项目服务。"""

import json
from pathlib import Path
import sys
from urllib.request import HTTPRedirectHandler, Request

original = Request(
    "https://api.synthetic.invalid/api/v1/files/example/download",
    headers={
        "Authorization": "Bearer SYNTHETIC_ONLY_NOT_A_SECRET",
        "X-EduGrade-Worker-Lease-Token": "SYNTHETIC_ONLY_NOT_A_SECRET",
    },
    method="GET",
)
redirect = HTTPRedirectHandler().redirect_request(
    original, None, 302, "Found", {}, "https://other.synthetic.invalid/receive"
)
result = {
    "python_version": sys.version.split()[0],
    "network_used": False,
    "application_started": False,
    "cross_origin_url_accepted": redirect.full_url == "https://other.synthetic.invalid/receive",
    "authorization_header_preserved": redirect.has_header("Authorization"),
    "worker_lease_header_preserved": redirect.has_header("X-edugrade-worker-lease-token"),
    "scope": "标准库 HTTPRedirectHandler 行为核验；未证明生产网关存在可利用的重定向路由。",
}
assert result["cross_origin_url_accepted"]
assert result["authorization_header_preserved"]
assert result["worker_lease_header_preserved"]
(Path(__file__).parent / "redirect-semantics.json").write_text(
    json.dumps(result, ensure_ascii=False, indent=2), encoding="utf-8"
)
print(json.dumps(result, ensure_ascii=False, indent=2))
