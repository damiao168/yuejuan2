"""汇总本次基础设施修复证据，不改写原始审计结果。"""

import hashlib
import json
from pathlib import Path

import yaml


ROOT = Path(__file__).resolve().parents[4]
RESULT_DIR = Path(__file__).resolve().parent
BASELINE = RESULT_DIR / "baseline/files"
dependabot = ".github/dependabot.yml"
nginx_files = ["infra/docker-compose/nginx/" + name for name in
               ("nginx.conf", "story056-e2e.conf", "story060-e2e.conf")]
client_dockerfile = "services/api-gateway/tools/story056-real-worker-e2e/Dockerfile"
postgres_dockerfile = "services/api-gateway/tools/story056-postgres-e2e/Dockerfile"
config = yaml.safe_load((ROOT / dependabot).read_text(encoding="utf-8"))
assert len(config["updates"]) == 11
assert all(update["cooldown"] == {"default-days": 7} for update in config["updates"])
assert all(update["schedule"]["interval"] == "weekly" for update in config["updates"])
postgres = (ROOT / postgres_dockerfile).read_bytes()
assert postgres == (BASELINE / postgres_dockerfile).read_bytes()
dockerfile = (ROOT / client_dockerfile).read_text(encoding="utf-8")
assert dockerfile.rfind("USER 10001:10001") > dockerfile.rfind("FROM ")
assert "chown 10001:10001 /home/e2e" in dockerfile
runtime = json.loads((RESULT_DIR / "nginx-authority-runtime.json").read_text(encoding="utf-8"))
assert runtime["passed"] and runtime["case_count"] == 48
findings = []
for number in range(1, 12):
    findings.append({"id": f"SG-{number:03d}", "status": "fixed", "files": [dependabot],
                     "change": "显式普通版本 cooldown 7 天；GitHub 安全更新豁免", "validation": "11 项 YAML 真实解析检查通过"})
for number, filename in [(44, nginx_files[0]), (45, nginx_files[0]), (47, nginx_files[1]),
                         (48, nginx_files[1]), (50, nginx_files[2]), (51, nginx_files[2])]:
    findings.append({"id": f"SG-{number:03d}", "status": "fixed", "files": [filename],
                     "change": "固定 upstream Host $proxy_host，删除外部 Forwarded/X-Forwarded-Host",
                     "validation": "nginx/1.29.0 原代理指令回环集成验收，三 conf 合计 48 case",
                     "limits": "listener/upstream 适配为回环；不证明 Docker DNS 或真实部署入口已验证"})
findings.extend([
    {"id": "SG-090", "status": "already_fixed_verified", "files": [postgres_dockerfile],
     "change": "保留既有 USER 10001:10001；与本次 baseline 完全一致", "validation": "逐字节与 SHA-256 核对",
     "limits": "Docker runtime 未验证"},
    {"id": "SG-091", "status": "fixed", "files": [client_dockerfile],
     "change": "最终 USER 10001:10001 和可写 HOME；stdout 输出不需挂载改权",
     "validation": "Dockerfile 静态身份/目录检查 + Go 客户端包测试通过", "limits": "Docker build/runtime 未验证"},
])
file_statuses = [(dependabot, "annotated"), *[(name, "annotated") for name in nginx_files],
                 (client_dockerfile, "annotated"), (postgres_dockerfile, "existing_sufficient"),
                 ("infra/docker-compose/nginx/README.md", "documentation"),
                 ("infra/docker-compose/nginx/tests/check_proxy_authority.py", "annotated")]
files = []
for name, status in file_statuses:
    content = (ROOT / name).read_bytes()
    baseline = BASELINE / name
    files.append({"path": name, "review_status": status, "current_sha256": hashlib.sha256(content).hexdigest(),
                  "baseline_sha256": hashlib.sha256(baseline.read_bytes()).hexdigest() if baseline.exists() else None,
                  "changed_in_this_task": not baseline.exists() or content != baseline.read_bytes()})
report = {
    "task": "infra_hardening", "date": "2026-10-11", "timezone": "Asia/Shanghai",
    "finding_ids": [finding["id"] for finding in findings], "findings": findings, "files": files,
    "validation": [
        {"name": "dependabot_yaml", "status": "passed", "update_count": 11, "cooldown_days": 7},
        {"name": "nginx_native_loopback", "status": "passed", "case_count": 48,
         "version": runtime["nginx_version"], "evidence": "nginx-authority-runtime.json"},
        {"name": "go_client_tests", "status": "passed", "command": "go test ./tools/story056-real-worker-e2e",
         "cwd": "services/api-gateway", "output": "ok edugrade-enterprise/services/api-gateway/tools/story056-real-worker-e2e 2.348s"},
        {"name": "SG-090_preservation", "status": "passed", "sha256": hashlib.sha256(postgres).hexdigest()},
        {"name": "SG-091_static_final_user_home", "status": "passed"},
        {"name": "python_compile_and_git_diff_check", "status": "passed"},
        {"name": "docker_runtime", "status": "not_run", "reason": "desktop-linux Docker version 15 秒超时；未重启或停止引擎"},
    ],
    "limitations": ["Nginx 回环验收替换 listener/upstream 端点，不是 Docker DNS 或 Linux 容器验证",
                    "未执行完整浏览器 E2E；源码未发现生产 Host 消费点或 WebSocket 入口",
                    "server_name _ 继续接收未知域名，收敛的是上游 authority，不声称已验证公网 allowlist/TLS",
                    "客户端镜像 build/runtime UID 和宿主挂载未验收；当前 Compose 客户端没有输出目录挂载"],
    "sources": ["https://docs.github.com/en/code-security/reference/supply-chain-security/dependabot-options-reference#cooldown",
                "https://nginx.org/en/docs/http/ngx_http_proxy_module.html#proxy_set_header"],
    "test_runtime_download": {"url": "https://nginx.org/download/nginx-1.29.0.zip",
                              "sha256": "fed5551ac776a9001eb26d43134695403d6703e9c3c85759c592aea4d223ef09",
                              "purpose": "官方便携版仅用于 isolated native loopback nginx 验证",
                              "cleanup_status": "自动审批策略拒绝 Remove-Item 清理；文件保留在 reports 下，测试进程已退出",
                              "path": "reports/platform-audit-2026-10-10/semgrep/remediation/test-runtime/nginx-1.29.0"},
}
(RESULT_DIR / "infra-results.json").write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
print(json.dumps({"fixed": 18, "already_fixed_verified": 1, "findings": len(findings)}, ensure_ascii=False))
