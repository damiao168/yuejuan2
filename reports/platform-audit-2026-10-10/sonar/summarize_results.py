"""把本次 SonarQube API 原始结果汇总为中文可读审计报告。"""
from __future__ import annotations

from collections import Counter, defaultdict
import json
from pathlib import Path

OUT = Path(__file__).resolve().parent
ROOT = OUT.parents[2]
PROJECT = "yuejuan-local-workspace-20261010"


def read(name):
    return json.loads((OUT / name).read_text(encoding="utf-8-sig"))


def main():
    manifest = read("export-manifest.json")
    execution = read("execution-status.json")
    task = read("compute-engine-task.json")["task"]
    if not manifest.get("complete") or not execution.get("current_results_valid") or task.get("status") != "SUCCESS":
        raise RuntimeError("Official platform processing or full export has not completed")
    metrics = {x["metric"]: x.get("value") for x in read("metrics.json")["component"]["measures"]}
    issues = read("issues.json")["issues"]
    hotspot_export = read("hotspots.json")
    hotspots = hotspot_export["hotspots"]
    files = read("file-metrics.json")["files"]
    # API 分页与合并清单必须一致；重复条目不能充当已导出更多问题的证据。
    validation = {
        "issues_expected": manifest["issues"], "issues_exported": len(issues),
        "issue_keys_unique": len({x["key"] for x in issues}),
        "files_expected": manifest["files"], "files_exported": len(files),
        "file_keys_unique": len({x["key"] for x in files}),
        "hotspots_expected": manifest["hotspots"], "hotspots_exported": len(hotspots),
        "rules_expected": manifest["rules"], "rules_exported": len(read("rules.json")),
    }
    if not (validation["issues_expected"] == validation["issues_exported"] == validation["issue_keys_unique"]
            and validation["files_expected"] == validation["files_exported"] == validation["file_keys_unique"]
            and validation["hotspots_expected"] == validation["hotspots_exported"]
            and validation["rules_expected"] == validation["rules_exported"]):
        raise RuntimeError("Exported results do not reconcile with the official API totals")
    validation["all_counts_reconciled"] = True
    (OUT / "export-validation.json").write_text(json.dumps(validation, ensure_ascii=False, indent=2), encoding="utf-8")
    component_paths = {item["key"]: item.get("path", item["key"].removeprefix(PROJECT + ":")) for item in files}
    for detail in hotspot_export.get("details", []):
        component = detail.get("component", {})
        if isinstance(component, dict) and component.get("key"):
            component_paths[component["key"]] = component.get("path", component.get("longName", component["key"]))

    def issue_path(item):
        component = item.get("component", "")
        if isinstance(component, dict):
            return component.get("path", component.get("longName", component.get("key", "")))
        return component_paths.get(component, component.removeprefix(PROJECT + ":"))
    severity = Counter(x["severity"] for x in issues)
    kinds = Counter(x["type"] for x in issues)
    rules = Counter(x["rule"] for x in issues)
    modules = Counter("/".join(issue_path(x).split("/")[:2]) for x in issues)
    file_metrics = []
    for item in files:
        mm = {x["metric"]: float(x.get("value", 0)) for x in item.get("measures", [])}
        file_metrics.append({"path": item.get("path", item["key"].removeprefix(PROJECT + ":")), **mm})
    summary = {"platform": "SonarQube Community Build", "server_version": read("server-status.json")["version"], "scanner": "SonarScanner CLI 8.1.0.6389", "scan_started_at": read("scanner-command.json")["scan_started_at"], "exported_at": manifest["exported_at"], "scanner_exit": read("scanner-exit.json")["exit_code"], "compute_engine_status": read("compute-engine-task.json")["task"]["status"], "metrics": metrics, "quality_gate": read("quality-gate.json")["projectStatus"], "issue_count": len(issues), "hotspot_count": len(hotspots), "by_severity": severity, "by_type": kinds, "top_rules": rules.most_common(25), "by_module": modules.most_common(), "top_complexity_files": sorted(file_metrics, key=lambda x: x.get("cognitive_complexity", 0), reverse=True)[:30], "findings_are_tool_candidates": True, "remote_github_access": False, "cloud_upload": False, "coverage_imported": False}
    summary["execution_status"] = execution
    summary["export_validation"] = validation
    summary["compute_engine_task_warnings"] = task.get("warnings", [])
    summary["quality_gate_definition"] = read("quality-gate-definition.json")
    summary["rating_letters"] = {"maintainability": "A", "reliability": "D", "security": "C"}
    summary["candidate_composition"] = {"css_font_fallback_rule": rules["css:S4649"], "bugs_except_css_font_fallback_rule": sum(x["type"] == "BUG" and x["rule"] != "css:S4649" for x in issues), "docs_architecture_candidates": modules["docs/architecture"], "triggered_rule_types": len(rules)}
    if (OUT / "recovery-submission.json").exists():
        recovery = read("recovery-integrity.json")
        summary["submission_recovery"] = {key: value for key, value in recovery.items() if key != "files"}
        summary["submission_recovery"]["receipt"] = read("recovery-submission.json")
    log_lines = (OUT / "scanner.log").read_text(encoding="utf-8", errors="replace").splitlines()
    warnings = [line for line in log_lines if " WARN " in line or " ERROR " in line]
    summary["analyzer_warnings_and_errors"] = warnings
    task_warnings = []
    if (OUT / "compute-engine-progress.log").exists():
        task_warnings = [line for line in (OUT / "compute-engine-progress.log").read_text(encoding="utf-8").splitlines() if " WARN " in line or " ERROR " in line]
    summary["compute_engine_warnings_and_errors"] = task_warnings
    summary["analyzer_scope"] = {"manifest_files": 2502, "preprocessed_files": 2498, "indexed_files": 2495, "detected_languages": 10, "go_reported_files": 1026, "go_reported_folders": 74, "python_reported_files": 182, "python_file_with_analysis_failure": "services/ocr-worker/tests/test_config.py", "javascript_typescript_css_reported_files": 1015, "javascript_typescript_cache_miss_files": 954, "text_and_secrets_reported_files": 2293, "docker_reported_files": 10, "cpd_files": 1200, "rust_clippy_executed": False}
    if (OUT / "project-visibility.json").exists():
        summary["project_visibility"] = read("project-visibility.json")
    if (OUT / "source-drift-after-scan.json").exists():
        summary["source_drift"] = read("source-drift-after-scan.json")
    (OUT / "analyzer-limitations.json").write_text(json.dumps({"warnings_and_errors": warnings}, ensure_ascii=False, indent=2), encoding="utf-8")
    (OUT / "summary.json").write_text(json.dumps(summary, ensure_ascii=False, indent=2), encoding="utf-8")
    lines = ["# SonarQube 本地扫描详细结果", "", f"本次真实执行 SonarQube Community Build {summary['server_version']} + SonarScanner CLI 8.1.0.6389。开始：{summary['scan_started_at']}；导出：{summary['exported_at']}。扫描目录名沿用 2026-10-10，实际执行时间以此处和原始日志为准。", "", "## 执行完成与边界", "", f"Scanner 退出码 `{summary['scanner_exit']}`，Compute Engine 状态 `{summary['compute_engine_status']}`。原始 issue 全量导出 `{len(issues)}` 条，热点 `{len(hotspots)}` 条。仅向 `http://127.0.0.1:19000` 本机已有 SonarQube 新建独立项目 `{PROJECT}` 提交分析。未访问远端 GitHub，未上传 SonarCloud。旧项目、全局配置、旧凭据均未修改；扫描 token 在导出后撤销。", "", "当前本地维护文件范围来自 `../source-manifest-resume-2026-10-11.csv`（含未提交和未跟踪源码），自动盘点不等于人工审读。`source-scope.json` 记录输入范围，`effective-scan.properties` 记录真正使用的包含/排除规则。第三方依赖、lab 下载/模型/运行目录、构建缓存、报告、生成 SDK、真实环境密钥、测试夹具/数据集排除；正式测试文件作为测试输入分析。SQL/Shell/PowerShell/YAML 等不受本 Community Build 完整分析支持的文件即使在维护清单，也不能据此称已完成同等深度检查。", "", "## 平台指标", "", "| 指标 | 实际值 |", "| --- | --- |"]
    if "submission_recovery" in summary:
        recovery = summary["submission_recovery"]
        lines[lines.index(next(line for line in lines if line.startswith("Scanner 退出码")))] = f"Compute Engine 状态 `{summary['compute_engine_status']}`，原始 issue 全量导出 `{len(issues)}` 条，热点 `{len(hotspots)}` 条。本次 Scanner 已完成全部分析器、重复代码分析和官方报告目录生成，但进程在 ZIP 压缩期间中断，未获得标准 Scanner 退出成功与 report-task.txt。随后验证目录 `{recovery['generated_report_files']}` 文件、`{recovery['generated_report_bytes']}` 字节，重新压缩并逐条核对 SHA-256 和 ZIP CRC，经同版本本机官方 `POST /api/ce/submit` 提交；平台最终成功处理，因此指标和问题是真实 SonarQube 结果，不能写作原始 Scanner 标准发布成功。恢复 ZIP SHA-256：`{recovery['archive_sha256']}`。只向 `http://127.0.0.1:19000` 本机已有 SonarQube 的独立项目 `{PROJECT}` 提交，未访问远端 GitHub，未上传 SonarCloud。原中断 token 和恢复用项目分析 token 均已撤销；旧项目、全局配置与旧凭据未修改。"
    scope_line = next(line for line in lines if line.startswith("当前本地维护文件范围"))
    lines[lines.index(scope_line)] = scope_line.replace("构建缓存、报告、生成 SDK、真实环境密钥、测试夹具/数据集排除", "构建缓存、reports/output 目录、生成 SDK、真实环境密钥、测试夹具/数据集排除；维护清单包含的 docs/architecture HTML/CSS、辅助脚本和 frontend-review-report HTML 仍在本次扫描范围内")
    if "project_visibility" in summary:
        visibility = summary["project_visibility"]
        lines.insert(lines.index("## 平台指标"), f"本次独立本地项目在平台处理结束后已确认可见性 `{visibility['visibility']}`，记录时间 {visibility['recorded_at']}；此前进行中的可见性变更被平台拒绝，原始错误保留在 `project-visibility-change-error.json`。仅调整本项目可见性，服务监听 `127.0.0.1:19000`。\n")
    labels = {"ncloc": "非注释代码行", "files": "平台计数文件（主要源码指标范围）", "bugs": "Bug 候选", "vulnerabilities": "漏洞候选", "code_smells": "可维护性问题", "security_hotspots": "安全热点", "sqale_rating": "可维护性评级（1=A）", "reliability_rating": "可靠性评级（4=D）", "security_rating": "安全评级（3=C）", "sqale_index": "修复工作量估算（分钟）", "complexity": "圈复杂度总和", "cognitive_complexity": "认知复杂度总和", "duplicated_lines_density": "重复代码行比例（%）", "duplicated_lines": "重复代码行", "duplicated_blocks": "重复代码块", "coverage": "平台覆盖率（未导入覆盖率，不代表实际测试覆盖率）", "ncloc_language_distribution": "分语言代码行"}
    for key, label in labels.items():
        lines.append(f"| {label} | {metrics.get(key, '未提供')} |")
    lines += ["", "平台指标中的 1339 个文件和 227136 行是主要源码指标范围，索引和传感器还包含测试输入及其他语言文件；不能把索引 2495、维护清单 2502 与这一指标直接相减后宣布遗漏。部分配置虽被识别语言，仍没有该语言的专用质量规则，见下方配置表。", "", "1653 个 Bug 候选中，1645 条来自同一 CSS 通用字体回退规则 `css:S4649`，其余 8 条来自 TypeScript、JavaScript 或 HTML 规则。`docs/architecture` 共 1741 个问题，占全量问题约 78.3%；本次维护清单包含文档中的 HTML/CSS 和审查辅助脚本，不能把这些数字当作 1653 个已证实业务运行缺陷。也不能因 CSS 告警占多数而忽略其余安全与可维护性候选。", "", "可维护性 A、可靠性 D、安全 C 是当前平台评级，保留平台原始数据，不是人工最终风险评级。6898 分钟约 115 小时，是规则默认修复工作量的估算，不是经项目排期核实的工时承诺。", "", "没有导入覆盖率文件，因此本报告不对真实测试覆盖率作零覆盖或达标判断；不执行测试也不能判断阅卷评分准确率、跨校访问是否绝对安全或并发正确性。", "", "## 质量门禁", "", "```json", json.dumps(summary["quality_gate"], ensure_ascii=False, indent=2), "```", "", "本次返回 `OK`，但 `conditions=[]`，没有逐条件的评估记录。默认质量门禁主要用于新代码；首次本地扫描没有可比历史分支基线。本结果不能证明全仓质量条件全部达标，更不能抵消当前可靠性 D、安全 C 和未复核候选。", "", "## 问题分布", "", "| 级别 | 数量 |", "| --- | --- |"]
    lines.extend(f"| {key} | {value} |" for key, value in severity.items())
    gate_lines = ["", "只读查询确认本项目使用内置默认 `Sonar way`，其定义包含以下 4 个新代码条件；定义与本次评估响应分别保存在 `quality-gate-definition.json` 和 `quality-gate.json`。", "", "| 新代码指标 | 失败条件 |", "| --- | --- |"]
    gate_lines.extend(f"| `{x['metric']}` | `{x['op']} {x['error']}` |" for x in summary["quality_gate_definition"]["conditions"])
    gate_lines += ["", "`GT` 表示大于，`LT` 表示小于。条件定义并不等于本次有可计算的新代码指标：本次响应没有任何条件条目，因此不能把 0.0% 总体覆盖率与 80% 新代码覆盖率门禁直接比较后宣布门禁有效通过。", ""]
    # 在问题分布前呈现门禁定义，避免把规则定义误认为已评估的结果。
    gate_index = lines.index("## 问题分布")
    lines[gate_index:gate_index] = gate_lines
    lines += ["", "| 类型 | 数量 |", "| --- | --- |"]
    lines.extend(f"| {key} | {value} |" for key, value in kinds.items())
    lines += ["", "| 模块 | 数量 |", "| --- | --- |"]
    lines.extend(f"| `{key}` | {value} |" for key, value in modules.most_common())
    lines += ["", "## 高频规则", "", "| 规则 | 数量 |", "| --- | --- |"]
    lines.extend(f"| `{key}` | {value} |" for key, value in rules.most_common(25))
    lines += ["", "## 高复杂度文件", "", "以下为整文件复杂度总和，不是单函数复杂度，也不等于功能缺陷。多个短函数组成的文件仍可能总分较高；宜结合职责、事务边界、权限和测试分析，不只按行数或总分重构。Go 201 条 `go:S3776` 是真正的单函数认知复杂度规则命中，具体函数位置见完整问题表。", "", "| 文件 | 代码行 | 圈复杂度 | 认知复杂度 |", "| --- | --- | --- | --- |"]
    lines.extend(f"| `{x['path']}` | {int(x.get('ncloc',0))} | {int(x.get('complexity',0))} | {int(x.get('cognitive_complexity',0))} |" for x in summary["top_complexity_files"])
    priority = {"BLOCKER": 0, "CRITICAL": 1, "MAJOR": 2, "MINOR": 3, "INFO": 4}
    # 高频 CSS 规则已有完整清单；代表表优先留下不同规则，避免遮住全部安全候选。
    selected = sorted((x for x in issues if x["rule"] != "css:S4649"), key=lambda x: (0 if x["type"] == "VULNERABILITY" else 1 if x["type"] == "BUG" else 2, priority[x["severity"]], x["component"], x.get("line", 0)))[:59]
    selected += [next(x for x in issues if x["rule"] == "css:S4649")]
    lines += ["", "## 重点候选问题与定位", "", "以下保留平台消息，尚未逐项人工确认为可达生产缺陷。命中测试代码、既有保护或平台误报时应单独核实。完整清单见 `issues.csv` 和 `issues.json`。", "", "| 类型/级别 | 规则 | 文件:行 | 平台消息 |", "| --- | --- | --- | --- |"]
    for item in selected:
        path = issue_path(item)
        msg = item["message"].replace("|", "\\|").replace("\n", " ")
        lines.append(f"| {item['type']}/{item['severity']} | `{item['rule']}` | `{path}:{item.get('line','文件级')}` | {msg} |")
    lines += ["", "## 安全热点", "", "热点表示需要人工判断安全上下文，不是已确认漏洞。`hotspots.json` 含全部原始详情、规则说明、位置和状态。", "", "| 优先级 | 类别 | 文件:行 | 消息 |", "| --- | --- | --- | --- |"]
    for item in hotspots:
        path = issue_path(item)
        lines.append(f"| {item.get('vulnerabilityProbability','')} | {item.get('securityCategory','')} | `{path}:{item.get('line','')}` | {item.get('message','').replace('|','/')} |")
    lines += ["", "## 本次分析器与规则配置", "", "以下版本直接取自本机平台 `plugins.json`。", "", "| 分析器 | 版本 |", "| --- | --- |"]
    plugin_keys = {"go", "javascript", "python", "rust", "web", "iac", "text"}
    lines.extend(f"| {x['name']} | `{x['version']}` |" for x in read("plugins.json")["plugins"] if x["key"] in plugin_keys)
    lines += ["", "扫描日志实际选用以下 10 个语言的内置 Sonar way 配置；`quality-profiles.json` 还包含服务器安装的其他语言配置，不能把其他语言配置计作本次已执行规则。触发的规则类型为 51，不等于本次仅运行 51 条规则。", "", "| 语言 | 配置 | 启用规则数 | 规则更新时间（平台原值） |", "| --- | --- | --- | --- |"]
    detected = {"css", "docker", "go", "js", "json", "py", "rust", "ts", "web", "yaml"}
    lines.extend(f"| `{x['language']}` | {x['name']} | {x.get('activeRuleCount', '未提供')} | `{x.get('rulesUpdatedAt', '未提供')}` |" for x in read("quality-profiles.json")["profiles"] if x["language"] in detected)
    lines += ["", "JSON/YAML 配置启用规则数为 0；文本/密钥扫描及适用的 IaC 传感器可能另行处理文件，但不能宣称已对所有 JSON/YAML 配置完成专用语义安全检查。", "", "## 本次配置与重现", "", "Scanner 引擎堆上限 1536 MiB，Node 堆设置 2048 MiB，ActiveProcessorCount=2。TypeScript 配置只使用 `apps/desktop-client/tsconfig.json`、`apps/student-portal/tsconfig.json`、`apps/web-admin/tsconfig.json`、`packages/sdk/tsconfig.json`，避免默认发现 output 和其他工作树的历史配置。", "", "```powershell", "python reports/platform-audit-2026-10-10/sonar/run_local_sonar.py", "python reports/platform-audit-2026-10-10/sonar/fingerprint_after.py", "python reports/platform-audit-2026-10-10/sonar/summarize_results.py", "```", "", "重现依赖本机现有 SonarQube 26.9 服务和本地凭据文件；凭据内容不在报告中。独立 19100 新实例启动失败后已自行退出，本次不停止其他会话的 19000 服务。恢复脚本 `resume_submission.py` 处理已有 Scanner 报告目录；有本次接收回执时只等待/导出，不重复提交分析。", "", "第 2 次完整 Scanner 通过 Go/Python/Rust/Text，但默认发现 22 个 TypeScript 配置时 Node 内存上限 960 MiB 耗尽；`scanner-attempt2.log` 是失败证据。该尝试未提交分析，不能计入已完成问题数量。之后采用 4 个真实配置并将 Node 设为 2048 MiB。"]
    if "source_drift" in summary:
        drift = summary["source_drift"]
        lines += ["", "## 扫描期间本地内容变化", "", f"本次 Scanner 在开始至服务器接收报告的区间内读取 live 工作区。输入清单仅用于盘点和限定范围，未冻结内容。扫描后 {drift['recorded_at']} 再次计算哈希，与恢复盘点相比 {len(drift['changed'])} 文件内容变化、{len(drift['missing'])} 文件缺失；其他本地任务的改动未回滚。`source-manifest-after-scan.csv` 和 `source-drift-after-scan.json` 保存具体哈希和路径。", "", "扫描后哈希不能证明每个文件在 Scanner 实际读取时的内容，因此本次结果不能声称与 Semgrep/CodeQL 使用完全相同冻结快照；关键问题定位仍需按当前代码核实。", "", "| 变化文件 |", "| --- |"]
        lines.extend(f"| `{item['path']}` |" for item in drift["changed"])
    lines += ["", "## 分析器局限与执行警告", "", "维护清单 2502 个文件，官方预处理 2498、索引 2495，识别 10 种语言。JS/TS/CSS 传感器记录 1015/1015；其中 JS/TS 缓存统计为 954 个，不能把两者混为同一数量。Text/Secrets 2293/2293、Docker 10/10、重复代码计算 1200 个文件。不同传感器可能处理同一文件，不能相加当作文件总数。", "", "Python 日志记录 182/182，但明确警告 `services/ocr-worker/tests/test_config.py` 因行偏移异常无法分析，不能称全部 Python 文件检查无遗漏。该文件在扫描后哈希与盘点不一致，期间工作区变化可能相关；现有证据不足以确定根因。Rust `queue.rs` 与 `spool.rs` 的注释高亮偏移异常影响高亮，不能直接归类为业务缺陷。根目录没有 Cargo manifest，Clippy 传感器明确跳过；因此未完成 Rust 编译器/Clippy 检查。", "", "以下来自最后完成分析器的原始日志。", "", "```text", *warnings, "```", "", "## 原始证据与重现", "", "- `scanner.log`：官方 Scanner 日志，全部语言分析器结束，最终停在报告生成后压缩阶段。", "- `scanner-command.json`、`effective-scan.properties`：真实命令、资源上限、范围；token 值不输出。", "- `compute-engine-task.json`、`recovery-submission.json`：官方本机接口接收和计算成功证据；原始运行没有新 report-task.txt。", "- `recovery-integrity.json`、`official-submit-api.json`、`resume_submission.py`：官方接口定义、重新压缩 ZIP 与全部条目哈希、恢复过程。", "- `metrics.json`、`quality-gate.json`、`issues-*-page-*.json`、`hotspots-page-*.json`：平台 API 分页原始响应。", "- `plugins.json`、`quality-profiles.json`、`languages.json`、`rules.json`：本次规则版本和配置依据。", "- `issues.csv`、`summary.json`：全量问题表及程序可读摘要。", "", "Docker 官方服务器镜像首次下载发生 EOF；随后独立原生实例启动发现 H2 默认端口 9092 与已有实例冲突。本次最终改用已有 localhost 实例中的独立新项目完成分析；`pull-server.log`、`server-native.*.log` 是失败路径证据，不能作为最终分析成功证据。第一次 Scanner 使用 384 MiB 堆，在 Go auth 包转换 AST 时发生 Java heap space；`scanner-attempt1.log` 保留失败证据。随后提高为 1536 MiB 重新完整执行，最终状态以上方报告恢复和 Compute Engine 证据为准。", "", "本次只扫描并写审计产物，不自动修复或改动业务代码。扫描并非全文件人工评审；所有未核实文件和问题仍为 pending。"]
    (OUT / "REPORT.md").write_text("\n".join(lines) + "\n", encoding="utf-8")
    if task.get("warnings"):
        with (OUT / "REPORT.md").open("a", encoding="utf-8") as report:
            report.write("\n## Compute Engine 官方任务警告\n\n本次 task API 返回 2 条警告；以下保留完整原值。平台成功处理不保证所有文件高亮、符号均完整。\n\n```text\n" + "\n".join(task["warnings"]) + "\n```\n")
    if task_warnings:
        with (OUT / "REPORT.md").open("a", encoding="utf-8") as report:
            report.write("\n## Compute Engine 执行警告\n\n平台成功处理仍可能带有局部限制；以下只截取本次 task 的后台警告。符号数据不一致时该文件的符号会被忽略，不能因此称该文件完成全部功能检查。\n\n```text\n" + "\n".join(task_warnings) + "\n```\n")
    if (OUT / "candidate-review.md").exists():
        with (OUT / "REPORT.md").open("a", encoding="utf-8") as report:
            report.write("\n## 人工复核附录\n\n安全和 Bug 代表的只读复核见 [candidate-review.md](candidate-review.md)。该附录按真实上下文解释代表问题，平台原始候选计数保持不变；没有逐项复核的告警仍为 pending。\n")
    if (OUT / "complexity-context-review.md").exists():
        with (OUT / "REPORT.md").open("a", encoding="utf-8") as report:
            report.write("\n高复杂度前三文件的局部职责核对见 [complexity-context-review.md](complexity-context-review.md)。该核对只覆盖明确列出的行段，文件级审读状态仍为 pending。\n")
    print(json.dumps({"metrics": metrics, "types": kinds, "severity": severity, "top_rules": rules.most_common(10)}, ensure_ascii=False))


if __name__ == "__main__":
    main()

