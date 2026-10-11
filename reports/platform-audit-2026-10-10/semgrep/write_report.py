"""从真实扫描结果和逐条人工核验生成 UTF-8 中文报告。"""

import collections
import csv
import datetime
import hashlib
import json
from pathlib import Path

report = Path(__file__).resolve().parent
repository = report.parents[2]
findings = json.loads((report / "findings-triage.json").read_text(encoding="utf-8"))
summary = json.loads((report / "scan-summary.json").read_text(encoding="utf-8"))
metadata = json.loads((report / "scan-metadata.json").read_text(encoding="utf-8"))
inventory = json.loads((report / "source-inventory.json").read_text(encoding="utf-8"))
rules = json.loads((report / "rule-manifest.json").read_text(encoding="utf-8"))
raw = json.loads((report / "semgrep-raw.json").read_text(encoding="utf-8"))
status_labels = {
    "hardening": "加固建议",
    "false_positive": "误报/已有控制",
    "needs_review": "条件性风险待核实",
    "context_limited": "工具场景受限",
    "needs_deployment_review": "部署前提待核实",
    "test_only": "测试工具范围",
}
severity_labels = {"low": "低", "medium": "中", None: "不适用"}
hash_changes = []
for item in inventory["source_files"]:
    current = repository / item["path"]
    if not current.is_file() or hashlib.sha256(current.read_bytes()).hexdigest() != item["sha256"]:
        hash_changes.append(item["path"])
(report / "snapshot-current-check.json").write_text(json.dumps({
    "checked_utc": datetime.datetime.now(datetime.timezone.utc).isoformat(),
    "files_checked": len(inventory["source_files"]),
    "changed_or_missing": hash_changes,
}, ensure_ascii=False, indent=2), encoding="utf-8")

lines = [
    "# Semgrep 本地工作区扫描详细报告",
    "",
    "实际执行日期：2026-10-11（Asia/Shanghai）。报告目录沿用 2026-10-10 创建的审计目录。",
    "",
    "使用官方 Semgrep 1.179.0 的 OSS 引擎完成真实本地扫描，发现 **105 条规则命中**。逐条结合调用和配置核验后：**68 条误报或已有防护，13 条条件性重定向风险待核实，24 条低风险加固、工具场景或部署检查项（含 1 条扫描后已解决）**。未确认可直接利用的生产高危漏洞；这不等于证明没有漏洞。",
    "",
    "扫描包含 16 个 PartialParsing 警告，不能称为 100% 完整解析。退出码 0 表示扫描命令成功，不能当作零告警或安全门禁通过。",
    "",
    "## 1. 执行与边界",
    "",
    "| 项目 | 实际结果 |",
    "| --- | --- |",
    "| 扫描平台/引擎 | 官方 Semgrep CLI 1.179.0 / `engine_requested=OSS` |",
    "| 官方镜像 | `semgrep/semgrep@sha256:93963d9295a366f59e4850127b1550400ee7b388f04fe144e4a1f6325d96e01b` |",
    f'| 分析开始/结束（UTC） | {metadata["started_utc"]} → {metadata["ended_utc"]} |',
    f'| 实际分析耗时 | {metadata["elapsed_seconds"]} 秒 |',
    "| 环境 | Docker，本次容器 2 CPU / 2 GiB 上限，2 jobs，单文件扫描内存上限 600 MB |",
    "| 联网 | 扫描容器 `--network none`；`--metrics off`、`--disable-version-check`、空平台令牌 |",
    "| 工作区 | `D:/project/yuejuan` 当前本地字节；含未提交修改与未跟踪维护源码 |",
    "| 文件来源 | 本地 `git ls-files --cached --others --exclude-standard -z`，没有访问远端 GitHub |",
    f'| 快照文本文件 | {len(inventory["source_files"])} 个、{metadata["inventory_bytes"]} 字节；另加副本专用空 `.semgrepignore`，CLI 共扫描 3197 目标 |',
    f'| 快照 ZIP SHA-256 | `{inventory["snapshot_sha256"]}` |',
    "| 规则 | 7 个官方公开规则包；按 ID 去重加载 735 条，CLI 报告实际运行 434 条 |",
    "| 原始结果 | ERROR 7、WARNING 87、MEDIUM 11；分布在 19 种规则中 |",
    "| 扫描错误 | 16 条 PartialParsing；约 99.9% 解析行，无 raw skipped_rules 条目 |",
    "| 工作区写入 | 仅审计目录、工具准备和独立本地快照；未修复业务代码 |",
    "",
    "只从官方 Registry 下载规则并下载官方镜像。没有使用 `semgrep ci`、云端项目、托管扫描或上传功能。官方说明本地 YAML 规则可由 CLI 执行，metrics 可显式关闭，见 [本地规则](https://docs.semgrep.dev/running-rules)、[metrics 设置](https://docs.semgrep.dev/metrics)。",
    "",
    "运行命令的完整参数在 `scan-metadata.json`，Windows 可复现入口是 `run-local.ps1`。原始 JSON 不经改写；`findings-triage.json` 与 `findings.csv` 是带人工结论的派生文件。工具安装、启动入口与范围纠正过程另见 `setup-notes.md`，失败准备步骤未冒充正式扫描。",
    "",
    "### 规则覆盖的实际限制",
    "",
    "735 条规则成功加载，不表示每条都作用于本仓库。按实际文件语言与 generic/regex 匹配，有 452 条具备目标语言、283 条没有对应语言；实际引擎汇总为 434 条运行。301 条加载规则未计入运行，其中剩余差额没有逐规则执行原因，不能全部归类为 Pro 跳过。原始 `skipped_rules=[]` 只是该字段未列出跳过项。",
    "",
    "规则配置中 28 条带 interfile 选项，本次明确使用 OSS，引擎没有提供 Pro 跨文件分析结果。本次没有云端有效密钥验证、商业规则、SCA 依赖漏洞扫描或运行时渗透测试。Registry 响应的 `missed` 字段原样记录在 `rule-manifest.json`，这些未下载规则未计入已执行覆盖。",
    "",
    "| 公开规则包 | 下载条数（去重前） | 响应 missed 字段 |",
    "| --- | ---: | ---: |",
]
for pack in rules["packs"]:
    lines.append(f'| [{pack["pack"]}]({pack["source"]}) | {pack["rule_count"]} | {pack["missed"]} |')
lines.extend([
    "",
    "规则原文、各包 SHA-256、合并规则与可复现执行器均保存在本目录。未改变 `.gitignore`、原 `.semgrepignore` 或业务输入；空忽略文件只存在隔离副本，用于扫描测试与维护文档。",
    "",
    "### 排除范围",
    "",
    "排除 `.git`、node_modules、第三方下载/模型/运行时缓存（含 `lab/.downloads`、`lab/.models`、`lab/.runtime`）、虚拟环境、构建产物、覆盖率产物、reports、output、tmp、备份、证书、实际 `.env` 等运行秘密输入、二进制、超过 5 MB 的单个文件、生成 SDK 与 Tauri 生成 schema。`.env.example` 可扫描。历史 SQL 迁移只读扫描，没有改写。",
    "",
    "这些排除项不是已经安全：未扫描实际 `.env`，因此本报告不能确认运行密钥不存在泄露；没有 secrets 规则命中也不能替代密钥轮换、运行环境检查或有效性验证。枚举与逐文件哈希详见 `source-inventory.json`。",
    "",
    "## 2. 人工核验后的优先处理项",
    "",
    "### 条件性中风险：Python 凭据请求自动重定向边界（13 条）",
    "",
    "涉及 SG-013～015、SG-092～094、SG-097～102、SG-104。模型服务、图像质量、OCR、数学验证、页面处理与主观题 Worker 使用 `urllib.request.urlopen`。初始 URL 来自受信部署配置或经过同源校验，响应有 timeout、JSON/图像大小限制；没有证据支持把这些调用一律定为 SSRF。",
    "",
    "剩余边界是默认重定向处理器：初始 URL 校验不会自动再次作用于跳转目标。对 Python 3.11.9 的无网络合成请求核验显示，跨域 302 GET 跳转会保留 `Authorization` 和 Worker Lease 等自定义头（`redirect-semantics.json`；脚本不打印凭据值、不启动应用、不发网络请求）。",
    "",
    "**触发前提**：配置的 API、供应商或代理必须返回攻击者可控制的重定向。目前没有确认普通用户能控制这样的路由，未证明生产环境实际可利用，故归为待核实的条件性风险。",
    "",
    "**影响**：一旦前提成立，可能向另一个 origin 泄露服务令牌或租约能力，并绕过初始的目标边界；不应把它直接表述为已发生跨学校越权。",
    "",
    "**建议**：对带凭据的内部/供应商请求默认禁止自动跳转；确需跳转时逐跳校验 scheme、origin 和允许目的地址，并剥离所有敏感头。Go 受管模型客户端及 Python `public_json_transport` 已有相应限制，可参考其边界设计。之后用合成服务验证跨域跳转被拒绝、同源允许策略与过期租约拒绝，不能只改注释承诺所有请求始终同源。",
    "",
    "| 位置 | 命中编号 |",
    "| --- | --- |",
    "| `ai-services/grading_agent/model.py:456,604,733` | SG-013～015 |",
    "| `services/image-quality-worker/image_quality/api.py:94,121,145` | SG-092～094 |",
    "| `services/ocr-worker/ocr_worker/api.py:266,290` | SG-097～098 |",
    "| `services/ocr-worker/ocr_worker/math_runner.py:41` | SG-099 |",
    "| `services/page-processing-worker/page_processing/api.py:71,102,160` | SG-100～102 |",
    "| `services/subjective-grading-worker/subjective_grading_worker/api.py:94` | SG-104 |",
    "",
    "### 低风险或场景受限事项（24 条，含 1 条扫描后已解决）",
    "",
    "- SG-001～011：11 个 Dependabot 配置未设 cooldown，属于新版本引入缓冲建议，不能据此确认依赖存在漏洞；评估策略时不要延误必要的安全更新。",
    "- SG-034～037：两个本地架构工具使用普通 XML 解析器，但当前只处理维护者固定生成的 drawio 产物。没有确认接受不可信 XML 的路径；今后支持外部文件时应限制来源并使用安全解析器。",
    "- SG-044～045：生产 Nginx 转发 `$host`，目标 upstream 固定，未发现网关据此构造敏感重置链接或跳转。虚拟主机 allowlist 仍需部署检查，当前不算已确认 Host 注入。",
    "- SG-047～048、050～051：只在 E2E Nginx 配置出现同类 Host 用法，测试范围，不能套用生产严重性。",
    "- SG-090：原快照的 E2E Go 测试镜像最终未设置 USER，属于真实低风险加固项。报告核验时当前文件已经改为多阶段且最终 USER 10001:10001，扫描后已解决，不能反标原扫描误报。",
    "- SG-091：E2E 客户端镜像最终未设置 USER，确实可以加固；受 E2E profile 和客户端用途限制，未发现公共服务入口，不能宣称生产容器以 root 运行。",
    "- SG-105：E2E 工具的动态 URL 来自测试配置，可补拒绝跳转与限额读取，但未发现生产用户提供 URL 的路径。",
    "",
    "### 重要误报示例（68 条）",
    "",
    "- SG-059、070：生产 SQL 只拼固定查询片段和程序生成的 `$N` 占位符，tenant、id、状态、关键词、cursor 等通过参数绑定。不是已经确认的 SQL 注入。",
    "- SG-053～056：Cookie 已有 `Secure: h.cookieSecure`、HttpOnly、SameSiteLax；生产配置拒绝 Secure=false，规则无法理解动态布尔值。其他 Cookie 命中是 httptest 请求构造器，其属性不代表服务 Set-Cookie。",
    "- SG-069：math/rand 用于只读重试的毫秒级抖动，不生成认证令牌或加密材料。",
    "- SG-095：调用 pickle.dumps 计算可信测试对象的序列化大小，没有从不可信输入反序列化。",
    "- SG-096：OCR Dockerfile 最终阶段已有 USER nobody。规则命中前面的构建阶段不能代表运行身份；与扫描后才修复的 SG-090 需区分。",
    "- Nginx header-redefinition 命中中，生产 health location 已重复完整安全头，测试配置没有需要继承的上级 add_header，不能直接报告响应头缺失。",
    "",
    "## 3. 命中分布和逐条核验",
    "",
    "原始 severity 保留工具值；ERROR/WARNING/MEDIUM 是规则作者给出的等级，不能机械映射为已确认漏洞的高/中/低。人工风险独立列出。",
    "",
    "| 人工状态 | 条数 |",
    "| --- | ---: |",
])
for status, count in summary["human_status"].items():
    lines.append(f'| {status_labels[status]} | {count} |')
lines.extend([
    "",
    "以下每行是一个真实扫描命中，不合并重复告警。完整英文规则消息与 confidence 保存在 JSON/CSV，原始代码片段只留在本地原始扫描文件，不在报告中复制潜在秘密。",
    "",
    "| ID | 文件及起始行 | 工具级别 | 人工状态 / 风险 | 核验依据 |",
    "| --- | --- | --- | --- | --- |",
])
for finding in findings:
    notes = finding["human_notes"].replace("|", "\\|").replace("\n", " ")
    lines.append(f'| {finding["id"]} | `{finding["path"]}:{finding["line"]}` | {finding["severity"]} | {status_labels[finding["human_status"]]} / {severity_labels[finding["human_severity"]]} | {notes} |')
lines.extend([
    "",
    "### 按规则汇总",
    "",
    "| 规则 ID | 命中数 |",
    "| --- | ---: |",
])
for rule, count in summary["rules"].items():
    lines.append(f'| `{rule}` | {count} |')
lines.extend([
    "",
    "## 4. 解析缺口",
    "",
    "全部 16 条错误均为 PartialParsing，具体位置如下；文件其余部分可能已经扫描，但不能视为全部语法均被理解。特别是 `.github/workflows/ci.yml`、Web Dockerfile 和 OCR Dockerfile 的相关段落需要结合本地 CI/构建检查。",
    "",
    "| 文件 | 受影响起始行 |",
    "| --- | --- |",
])
for error in raw["errors"]:
    kind = error.get("type")
    positions = kind[1] if isinstance(kind, list) and len(kind) > 1 and isinstance(kind[1], list) else []
    path = str(error.get("path", "" )).removeprefix("/tmp/yuejuan2-semgrep-source/")
    if not path and positions:
        path = positions[0]["path"].removeprefix("/tmp/yuejuan2-semgrep-source/")
    positions_text = ", ".join(str(position["start"]["line"]) for position in positions) or "见原始 JSON"
    lines.append(f'| `{path}` | {positions_text} |')
lines.extend([
    "",
    "## 5. 不能从本次结果推出的结论",
    "",
    "- 规则扫描未自动证明学校/租户数据隔离、数据库 RLS、业务状态机、并发成绩写入或阅卷准确率正确；这些仍需专项代码审查、数据库集成测试与业务评测。",
    "- 已核验的是 105 条告警的位置、输入来源和相关保护控制，不是逐个文件完成全面人工审读。清单中全部文件人工审读状态仍为 pending，不能用自动枚举或扫描完成替代逐文件审读。",
    "- 无网络合成重定向核验只证明标准库请求构造行为，没有向现有服务发请求，也没有证明生产部署可被利用。",
    "- 没有自动修复或重新格式化业务代码；所有已有未提交修改均保留。",
    "",
    "## 6. 交付文件",
    "",
    "| 文件 | 用途 |",
    "| --- | --- |",
    "| `semgrep-raw.json` | 平台原始结果：105 命中、16 解析警告、OSS 标记、扫描路径 |",
    "| `scan.stderr.log` / `docker-run.log` | 引擎状态、434 实际运行规则、命令执行结果 |",
    "| `scan-metadata.json` | 版本、完整参数、耗时、退出码、隔离条件 |",
    "| `findings-triage.json` / `findings.csv` | 105 条逐条人工核验，全部非 pending |",
    "| `root-triage.json` / `backend-triage.json` | 并行审查分工证据 |",
    "| `source-inventory.json` | 本地快照逐文件 SHA-256、排除项、pending 人工文件状态 |",
    "| `snapshot-current-check.json` | 报告生成时对照快照检查文件漂移 |",
    "| `rule-manifest.json` / `rule-capabilities.json` | 官方规则来源、哈希、语言能力和明确限制 |",
    "| `redirect-semantics.json` / `check_redirect_semantics.py` | 无网络合成凭据重定向边界核验 |",
    "| `run-local.ps1` / `prepare_snapshot.py` / `local_scan.py` | 用当前工作区再次执行真实本地扫描 |",
    "| `setup-notes.md` / `pip-install.log` / `docker-pull.log` | 准备步骤、失败与范围纠正说明 |",
    "",
    f'报告生成时检查 {len(inventory["source_files"])} 个快照文件，发现当前字节变化或缺失 {len(hash_changes)} 个。正式引擎结果对应 10:55 的固定快照，不能冒充扫描后的最新文件。变化影响 SG-090、SG-105 所在文件：SG-090 当前已设置非 root USER；SG-105 默认 Agent URL 已由 HTTP 改为 HTTPS，动态 URL 的测试工具范围结论不变。其他变化文件没有对应本次告警，仍不等于已经按新字节扫描。',
])
if hash_changes:
    lines.extend(["", "变化文件：", ""] + [f"- `{path}`" for path in hash_changes])
text = "\n".join(lines) + "\n"
assert "\ufffd" not in text
(report / "REPORT.md").write_text(text, encoding="utf-8")
print(json.dumps({"report": str(report / "REPORT.md"), "findings": len(findings), "snapshot_drift": len(hash_changes), "utf8_replacement_character_count": text.count("\ufffd")}, indent=2))
