"""保存前端和部署告警的独立核验；不修改业务源码。"""
import json
from pathlib import Path

OUT = Path(__file__).resolve().parent
review = {}


def record(ids, status, severity, notes):
    for number in ids:
        review[f"SG-{number:03}"] = {"human_status": status, "human_severity": severity, "human_notes": notes}


record(range(1, 12), "hardening", "low", "11 个 Dependabot 更新项确实没有 cooldown；已有每周检查和人工 PR 流程。属于依赖更新缓冲策略建议，不是已确认依赖漏洞或代码执行。可评估为新版本设置缓冲期；不应延迟必要的安全更新。")
record(range(25, 31), "false_positive", None, "apps/web-admin/nginx.conf:14-24 的 /health location 已显式重复所有 server 安全头；17 行 Content-Type 并未造成 CSP、X-Frame-Options 等丢失。19-23 行正是修复继承边界的防护代码。")
record(range(31, 34), "false_positive", None, "mfaFlow.test.ts:194-199 向 mock response 注入固定 <script> 字符串，断言 confirm 返回 uncertain 且流程关闭；这里不是脚本模板插值或 DOM 执行。同一行的三条命中属于同一负向测试。")
record([34, 35], "context_limited", "low", "build_diagrams.py:132 自行生成固定本地 drawio，134 行立即回读验证；未发现不可信上传 XML 进入此路径。不是已确认生产 XXE。若将来接受外部 XML，应限制解析资源并使用安全解析器。")
record([36, 37], "context_limited", "low", "verify_delivery.py:26-31 读取 EXPECTED 固定列表中的本地架构图，不接受网络请求或任意用户路径。维护者若主动导入不可信图纸仍有解析资源风险；本次没有证明生产接口存在 XXE。")
record(range(38, 44), "false_positive", None, "infra/docker-compose/nginx/nginx.conf:30-40 在 /health 中显式重复 server 层全部五个安全头，注释也解释 add_header 的继承边界。规则报告的是存在 add_header，而不是遗漏防护。")
record([44, 45], "needs_deployment_review", "low", "生产 Nginx 确实把请求 $host 转给固定上游；代理目的地不是由 Host 决定。未在当前网关非测试 Go 源码中找到使用请求 Host 构造重置链接/跳转的证据。公网允许域名与入口网关配置未在本次运行验证，建议结合部署评审，不标成已确认 Host 注入漏洞。")
record([46, 49], "false_positive", None, "对应 story056/story060 E2E Nginx 的 server 块没有 add_header 安全头，/health 中只有 Content-Type，不存在规则所称覆盖上级安全头。此配置仅用于 E2E。")
record([47, 48, 50, 51], "test_only", "low", "这些 $host 转发位于 story056/story060 E2E 配置，目的地为固定测试上游；未证实生产利用链。若复用为公网配置，需另审允许域名和 Host 消费方。")
record([90], "test_only", "low", "扫描时的 ZIP 快照内 story056-postgres-e2e/Dockerfile 最终阶段没有 USER，属于 E2E 镜像默认 root 加固项。之后核验时工作区已由其他工作改成最终 USER 10001:10001，当前问题已修；不能把扫描后的修复当作原告警误报。本次审计未修改该 Dockerfile。")
record([91], "test_only", "low", "独立 story056-real-worker-e2e 测试客户端镜像最终没有 USER，使用 alpine 默认 root；Compose 仅在 e2e profile 启动它，没有公网服务入口。这是确实存在的测试镜像加固项，建议独立非 root 用户；不是已确认生产越权漏洞。")
record([96], "false_positive", None, "OCR Dockerfile:6 的 USER root 只用于安装依赖；文件末尾已切换 USER nobody，之后 CMD python -m ocr_worker。Semgrep 同时报告此 Dockerfile 的部分解析错误，不能把解析到的最后 root 当作真实最终用户。")

(OUT / "root-triage.json").write_text(json.dumps(review, ensure_ascii=False, indent=2), encoding="utf-8")
print(f"Saved {len(review)} reviewed findings")
