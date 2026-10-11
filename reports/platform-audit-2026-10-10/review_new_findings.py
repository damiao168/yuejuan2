"""把本次已逐条阅读的告警结论写入旁置报告；不修改源码或平台原始结果。"""
from __future__ import annotations

import csv
import hashlib
import json
from collections import Counter
from datetime import datetime, timedelta, timezone
from pathlib import Path

REPORT = Path(__file__).resolve().parent
WORKSPACE = REPORT.parents[1]
SNAPSHOT = Path("D:/tools/audit-codeql-20261010/source-20261011")
NOW = datetime.now(timezone(timedelta(hours=8))).isoformat()


def digest(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def read_json(path: Path):
    return json.loads(path.read_text(encoding="utf-8"))


def write_json(path: Path, value):
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


def excerpt(path: Path, first: int, last: int) -> list[str]:
    lines = path.read_text(encoding="utf-8").splitlines()
    return [f"{i + 1}: {lines[i]}" for i in range(first - 1, min(last, len(lines)))]


def md_path(path: str, line: int, snapshot: bool = False) -> str:
    root = SNAPSHOT if snapshot else WORKSPACE
    return f"[{path}:{line}](<{root.as_posix()}/{path}:{line}>)"


def review_javascript():
    folder = REPORT / "codeql"
    sarif = read_json(folder / "javascript.sarif")
    run = sarif["runs"][0]
    rules = {rule["id"]: rule for rule in run["tool"]["driver"]["rules"]}
    manifest_path = REPORT / "source-manifest-resume-2026-10-11.csv"
    with manifest_path.open(encoding="utf-8-sig", newline="") as stream:
        manifest = {row["path"]: row["sha256"] for row in csv.DictReader(stream)}

    local_race = (
        "检查路径存在/类型之后，后续读写再次按路径打开，确有检查与使用之间的窗口。"
        "本次调用位于维护者本地/CI 的契约生成与对账脚本，路径由工作区根和固定产物路径组成；"
        "未找到教师/学生请求控制文件系统目标或切换链接的入口。"
        "只有另一进程能在该维护工作区改名、删除或替换链接，且脚本具有该进程没有的权限时，才形成安全攻击前提。"
        "不据此认定四个线上高危漏洞；同权限并发维护仍可能导致读写失败或产物不一致。"
    )
    local_race_fix = (
        "后续维护可用打开后的同一文件描述符做读取/校验，明确符号链接政策；"
        "生成写入考虑同目录临时文件和原子替换，并保留现有 Windows 权限/文件锁兼容约束。"
        "在普通可信工作区作为低优先级加固；若 CI 使用共享不可信可写目录或提升权限运行，再提高优先级。"
    )
    choices = {
        0: ("conditional_local_hardening", "low", local_race, local_race_fix, [("scripts/ci/check-generated-contracts.mjs", 7, 38)]),
        1: ("conditional_local_hardening", "low", local_race, local_race_fix, [("scripts/lib/openapi-sdk.mjs", 247, 258), ("scripts/generate-sdk.mjs", 5, 12), ("scripts/update-openapi-baseline.mjs", 5, 9)]),
        2: ("conditional_local_hardening", "low", local_race, local_race_fix, [("scripts/lib/openapi-sdk.mjs", 247, 258), ("scripts/generate-sdk.mjs", 5, 12), ("scripts/update-openapi-baseline.mjs", 5, 9)]),
        3: ("conditional_local_hardening", "low", local_race, local_race_fix, [("scripts/update-api-route-coverage.mjs", 109, 128)]),
        4: ("low_maintenance", "low", "examWorkspace 只初始化读入源码，变量在该固定快照脚本中未被后续断言使用。不是生产页面的未使用状态。", "后续删除无用途的读取，或补上原计划的路由契约断言；不要把读取本身当作已经检查了该页面。", [("apps/web-admin/scripts/check-production-routes.mjs", 11, 31)]),
        5: ("low_maintenance", "low", "allUiSource 对 src 做递归读入并 join，随后没有使用；其他断言使用各自具体源码或 gradingFeature。存在无用途的扫描开销。", "后续删除无用途的全 src 读取或明确其实际断言用途。", [("apps/web-admin/scripts/check-production-routes.mjs", 33, 39), ("apps/web-admin/scripts/check-production-routes.mjs", 155, 189)]),
        6: ("intentional_behavior", "none", "baseInput 测试夹具先取 overrides.rubric 或默认量规，...overrides 后再次写 rubric，确保最终使用同一个已选择量规；重复键是夹具覆盖次序，未发现值丢失。", "可删除 spread 前面的第一个 shorthand rubric 简化对象；必须保留最终覆盖规则及 max_score 来源，不能只删除最后一个 rubric。", [("lab/tests/helpers.js", 18, 37)]),
        7: ("intentional_behavior", "none", "该文件保存 async(page)=>{...} 给 Playwright runner 作为函数输入，函数体由 runner 调用；作为独立 node 脚本会只生成未使用的函数值。现有架构维护文档记录该运行方式。", "如需要独立 Node 入口，另外提供显式 runner；在质量扫描中用精确路径标记此类工具输入，不全局关闭 useless-expression。", [("docs/architecture/tools/capture_preview_qa.js", 1, 31)]),
        8: ("intentional_behavior", "none", "该文件是 Playwright run-code 使用的 async(page) 函数输入，并非自动调用的 Node 入口；函数由外部浏览器 runner 注入 page 后执行。文档和本地证据记有 render_native.js 的运行方式。", "在文件说明和扫描配置中明确 runner 输入用途；不要为了消除告警直接加自调用而缺少 page。", [("docs/architecture/tools/render_native.js", 1, 8), ("docs/architecture/tools/render_native.js", 80, 90)]),
        9: ("false_positive_context", "none", "StorageEvent 的标准构造器接收 type 和可选 StorageEventInit；此处 storage + {key:null} 合法。SARIF relatedLocations 指向 CodeQL 自带 externs/web/webstorage.js:106 的旧零参数模型 function StorageEvent() {}，不是业务声明。", "保留测试语义；可向 CodeQL 反馈标准 DOM 模型过时，并对两条精确告警记录误报理由。", [("apps/web-admin/src/features/grading/workbench/hooks/useGradingKeyboard.test.tsx", 8, 18)]),
        10: ("false_positive_context", "none", "StorageEvent 的两个参数符合标准接口；第二个字典中 key 用于模拟跨标签页快捷键设置变更。CodeQL 将标准构造器建模为零参数导致多余参数告警。", "保留测试的 key；不要删除参数破坏跨标签页设置变化场景。记录精确误报。", [("apps/web-admin/src/features/grading/workbench/hooks/useGradingKeyboard.test.tsx", 55, 61)]),
        11: ("low_maintenance", "low", "同一 save 回调 line154 在无 await 之前已因 !tenantID 或 loadingConfigs 返回；line161 再查这两个闭包值确为冗余。后续 !config/canDisableMissing/prompt/strength_rank/capability 检查仍有效。", "后续可去掉第二处重复子条件，保留入口同步 ref/state 边界与其他字段校验。不是缺失鉴权或异步竞态的证据。", [("apps/web-admin/src/pages/PanelModelBindingsSection.tsx", 148, 166)]),
        12: ("low_maintenance", "low", "该 !tenantID 子表达式在 line154 提前 guard 后恒 false，原因是正常入口前置拒绝；不表示租户校验失效。", "与同一行另外两条冗余条件一并整理，入口租户 guard 应保留。", [("apps/web-admin/src/pages/PanelModelBindingsSection.tsx", 152, 165)]),
        13: ("low_maintenance", "low", "该 loadingConfigs 子表达式在 line154 提前 guard 后恒 false，两次检查之间无 await；同步入口仍使用 loadingRef 防 state 未重绘的窗口。", "可去掉第二次 loadingConfigs 检查，保留 loadingRef 和入口 guard 的业务说明。", [("apps/web-admin/src/pages/PanelModelBindingsSection.tsx", 152, 165)]),
    }
    assert len(run["results"]) == 14 and set(choices) == set(range(14)), "新结果需重新人工审读后再生成"
    findings = []
    for i, result in enumerate(run["results"]):
        physical = result["locations"][0]["physicalLocation"]
        path = physical["artifactLocation"]["uri"]
        verdict, severity, reason, recommendation, support = choices[i]
        rule = rules[result["ruleId"]]
        evidence = []
        for source, first, last in support:
            file = SNAPSHOT / source
            source_hash = digest(file)
            assert source_hash == manifest[source], source
            evidence.append({"path": source, "start_line": first, "end_line": last, "snapshot_sha256": source_hash, "lines": excerpt(file, first, last)})
        source_hash = digest(SNAPSHOT / path)
        assert source_hash == manifest[path], path
        findings.append({
            "review_id": f"CQ-JS-{i + 1:03}", "sarif_result_index_zero_based": i,
            "rule_id": result["ruleId"], "path": path, "line": physical["region"]["startLine"],
            "raw_message": result["message"]["text"], "raw_problem_severity": rule.get("properties", {}).get("problem.severity"),
            "raw_security_severity": rule.get("properties", {}).get("security-severity"),
            "security_finding": "security" in rule.get("properties", {}).get("tags", []),
            "manual_review_status": "reviewed_alert_context", "human_verdict": verdict, "human_severity": severity,
            "reason": reason, "recommendation": recommendation, "evidence": evidence,
            "snapshot_sha256": source_hash, "current_workspace_used_for_verdict": False,
        })
    value = {
        "reviewed_at": NOW, "codeql_version": "2.27.2", "sarif_sha256": digest(folder / "javascript.sarif"),
        "source_root": str(SNAPSHOT), "manifest_sha256": digest(manifest_path),
        "raw_javascript_results_total": len(findings), "raw_security_results_reviewed": 4,
        "raw_quality_results_reviewed": 10, "finding_reviews_pending": 0,
        "confirmed_production_exploits_in_reviewed_results": 0,
        "confirmed_runtime_defects_in_reviewed_quality_results": 0,
        "human_verdict_counts": dict(Counter(x["human_verdict"] for x in findings)),
        "scope": "Each alert and listed support context reviewed; not whole-file or whole-repository review.",
        "source_validation": "All fixed snapshot alert and support file hashes match the scan manifest.",
        "external_support": [
            {"title": "WHATWG HTML Standard, StorageEvent interface", "url": "https://html.spec.whatwg.org/multipage/webstorage.html#the-storageevent-interface", "checked_at": NOW, "claim": "constructor accepts event type and optional StorageEventInit"},
            {"path": "D:/tools/audit-codeql-20261010/codeql/javascript/tools/data/externs/web/webstorage.js", "line": 106, "sha256": digest(Path("D:/tools/audit-codeql-20261010/codeql/javascript/tools/data/externs/web/webstorage.js")), "claim": "CodeQL external constructor model is zero-parameter"},
            {"path": "docs/architecture/DIAGRAM_MAINTENANCE.md", "line": 46, "sha256": digest(WORKSPACE / "docs/architecture/DIAGRAM_MAINTENANCE.md"), "scope": "Current local documentation, not included in fixed source snapshot; supporting runner intent only"},
        ],
        "findings": findings,
    }
    write_json(folder / "javascript-finding-review.json", value)
    md = ["# JavaScript / TypeScript CodeQL 候选人工复核", "", f"复核时间：{NOW}。平台原始结果 [javascript.sarif](javascript.sarif)，SHA-256 `{value['sarif_sha256']}`。", "", "203 条官方 security-and-quality 查询的 14 条候选全部逐条阅读：4 条安全类文件系统检查/使用竞态，10 条质量类。人工结论为 4 条有条件的本地脚本加固、5 条低优先级维护、3 条有意的覆盖/runner 输入、2 条标准 DOM 模型误报。没有把 7.7 的工具分数直接转换成四个已证实线上漏洞。", "", "CodeQL 判断只用 2026-10-11 10:55:42 固定工作区副本，所列源文件及调用上下文 SHA-256 全部匹配清单。外部 runner 用途另参考当前本地文档，单独记录其哈希；未运行服务、未访问学生数据、未进行云端上传。告警上下文复核不代表相关文件全篇或全仓均已人工审读。", "", "| 项 | 数量 |", "| --- | ---: |", "| 原始 security-high（四条均 7.7） | 4 |", "| 原始质量候选 | 10 |", "| 该 SARIF 未分配人工结论 | 0 |", "| 复核质量候选中确认的运行缺陷 | 0 |", "", "文件系统风险的前提是维护工作目录可被另一个非可信主体并发修改，并且维护脚本具有额外读写权限。当前固定调用链没有教师/学生 API 路径进入这几个函数。代码确有 TOCTOU 窗口，不能以开发脚本为由宣称绝对无风险；优先级随本机/CI 权限和目录隔离情况变化。", "", "StorageEvent 两条误报的依据包括工具自身 `externs/web/webstorage.js:106` 零参数声明与 [WHATWG 标准构造器](https://html.spec.whatwg.org/multipage/webstorage.html#the-storageevent-interface)。这里允许 type 和初始化字典，删除参数会损坏测试场景。", ""]
    for f in findings:
        md += [f"## {f['review_id']} · {f['rule_id']}", "", md_path(f["path"], f["line"], snapshot=True), "", f"人工状态：`{f['manual_review_status']}`；结论 `{f['human_verdict']}`；人工优先级 `{f['human_severity']}`。原始安全分数：{f['raw_security_severity'] or '无'}。", "", f["reason"], "", f"建议：{f['recommendation']}", "", f"固定源文件 SHA-256：`{f['snapshot_sha256']}`。相关证据与所读范围见 [javascript-finding-review.json](javascript-finding-review.json)。", ""]
    (folder / "javascript-finding-review.md").write_text("\n".join(md), encoding="utf-8")
    return value


def review_sonar():
    folder = REPORT / "sonar"
    issues = read_json(folder / "issues.json")["issues"]
    source_manifest = read_json(folder / "review-source-manifest.json")
    sources = {row["path"]: row for row in source_manifest["sources"]}
    css = [x for x in issues if x["rule"] == "css:S4649"]
    css_review_keys = {css[0]["key"], css[1]["key"], css[len(css) // 2]["key"], css[-1]["key"]}
    findings = []
    for item in issues:
        path = item["component"].split(":", 1)[1]
        if item["type"] == "VULNERABILITY":
            if item["rule"] == "python:S2245":
                verdict, severity = "false_positive_context", "none"
                reason = "种子化随机数用于匿名 synthetic 仿真答题卡的笔迹颜色、填涂偏移、图像噪声和像素坐标；不生成密钥、认证令牌、身份或安全挑战。可预测性用于复现模拟图像，在该用途不是安全缺陷。"
                recommendation = "保留可复现种子；对这六处记录仿真用途，不应统一替换成密码学随机数破坏图像复现。"
                support = [(path, max(1, item["line"] - 10), item["line"] + 9)]
            elif path == "ai-services/grading_agent/server.py":
                verdict, severity = "false_positive_context", "none"
                reason = "实际被 Sonar 保存的源代码先调用 settings._validate_credentials_and_listener，再构造服务器；配置证书时用 TLS>=1.2 包装 socket 后才 serve_forever。被分析的 config.py 限定无证书只能显式开发环境、显式布尔 opt-in、字面 IPv4 回环地址，其余拒绝启动。HTTPServer 基类/serve_forever 名称不能证明生产明文暴露。"
                recommendation = "保留生产 TLS 和开发回环例外边界；这条应记录上下文误报。TLS 部署证书和实际运行配置未做线上验证。"
                support = [(path, 279, 294), ("ai-services/grading_agent/config.py", 121, 152)]
            elif path.endswith("math_verification_worker/server.py"):
                verdict, severity = "false_positive_context", "none"
                reason = "实际分析源包含生产环境必须证书、开发 HTTP 必须明确 opt-in 和字面 IPv4 回环的监听策略；main 在绑定端口前调用策略，证书存在时 socket 先包 TLS>=1.2 再 serve_forever。自动规则未识别包装/拒绝启动边界，不能当作线上明文传输证据。"
                recommendation = "保留在端口绑定前的校验及 TLS 包装；记录误报上下文。未验证真实部署环境或证书。"
                support = [(path, 145, 189)]
            elif path.endswith("preview_server.py"):
                verdict, severity = "false_positive_context", "none"
                reason = "文档预览工具在被分析源码中固定监听 127.0.0.1:8766，提供本地架构图纸预览，不是业务服务/学生阅卷接口。规则的 HTTP 类告警存在，但没有对外监听或跨主机凭据传输证据。"
                recommendation = "保持回环监听及文档工具范围；不可因为此告警推断业务 API 无 TLS。"
                support = [(path, 1, 36)]
            else:
                assert item["rule"] == "Web:S5725", item
                verdict, severity = "low_document_integrity", "low"
                reason = "独立前端审查 HTML 报告引用固定版本 CDN ECharts 脚本，缺少 integrity/crossorigin，规则确有代码依据。该文件为本地文档，未发现应用路由加载它，未确认 CDN 被篡改或业务会话利用路径。风险是打开报告时执行的外部资源完整性。"
                recommendation = "后续为报告构建使用本地固定资产，或根据实际字节计算 SRI 并配 crossorigin；不要捏造 integrity 哈希，也不要把此项报成生产业务已被入侵。"
                support = [(path, 554, 560)]
        elif item["type"] == "BUG" and item["rule"] != "css:S4649":
            if item["rule"] == "typescript:S6544":
                verdict, severity = "false_positive_context", "none"
                reason = ".some 回调只检测 column.sorter / filters / filterDropdown 属性是否存在，返回值按 JavaScript truthiness 判断；没有调用异步函数或等待 Promise。即使类型联合中含 Promise/函数，也只是能力存在标记，外层 Boolean 再得到布尔值。未发现被遗忘的异步执行。"
                recommendation = "如需提高可读性，后续显式 Boolean 属性组合；无需按异步结果丢失修复。"
                support = [(path, 90, 99)]
            elif item["rule"] == "typescript:S7727":
                verdict, severity = "false_positive_context", "none"
                reason = "normalizeRubricPoint(value,index) 明确使用 .map 提供的第二参数 index 生成评分点 fallback id/说明，.map 的实参次序正好匹配；第三参数未使用。不是 parseInt 一类把索引错当参数的情形。"
                recommendation = "保留索引语义，可在后续维护注释解释 fallback id 的来源；本次不改功能。"
                support = [(path, 18, 39)]
            elif item["rule"] == "javascript:S7727":
                verdict, severity = "false_positive_context", "none"
                reason = "questionGroupKey 只声明/读取 sample；map 额外 index/array 被 JavaScript 忽略。该函数按题目与量规版本求隔离组，没有接收 radix 或 options 参数，未发现数据拆分语义错误。"
                recommendation = "保留按题目及量规版本隔离；函数实际实现另读当前本地 governance.js 与固定 CodeQL 快照，Sonar 项目未保存此支持文件的 raw source，因此不宣称其为 Sonar 分析源码的逐字副本。"
                support = [(path, 69, 81)]
            elif item["rule"] == "javascript:S2871":
                verdict, severity = "false_positive_context", "none"
                reason = "输入在排序前被正则限制为六位 ASCII 十进制迁移号，或以同样六位号开头的文件名；固定位数字符串字典序与数字版本顺序一致。当前默认 .sort() 满足取最新迁移的语义，CRITICAL 自动标签不等于真实版本排序缺陷。"
                recommendation = "保留六位编号约束；未来若接受不等长/非 ASCII 版本号，再改显式数字或版本比较并增加对应契约验证。"
                support = [(path, 66, 81)] if item["line"] == 70 else [(path, 16, 21)]
            elif item["rule"] == "javascript:S905":
                verdict, severity = "intentional_behavior", "none"
                reason = "文档浏览器工具将 async(page)=>{...} 文件作为 Playwright run-code 输入，由 runner 注入 page 并调用；作为单独 Node 程序确实不执行函数体，但这不是记录的使用方式。"
                recommendation = "精确标记 runner 输入文件用途或配置扫描范围；不要添加缺少 page 的自调用。"
                support = [(path, 1, 8)]
            else:
                assert item["rule"] == "Web:InputWithoutLabelCheck", item
                verdict, severity = "low_document_accessibility", "low"
                reason = "架构图纸预览的 select#picker 没有关联 label、aria-label 或 aria-labelledby；标题 h1 不构成该控件的可访问名称。是独立本地文档工具的无障碍缺口，不是成绩写入或评分运行错误。"
                recommendation = "在图纸预览正式生成源中增加选择控件名称/label，再按生成流程更新预览；不要只批改大段生成 SVG。"
                support = []
        elif item["key"] in css_review_keys:
            verdict, severity = "low_document_font_fallback", "low"
            reason = "被分析 HTML 内嵌 SVG foreignObject 的 style 显式设置 font-family:'Microsoft YaHei'，这一字体列表未含 generic fallback。外层 :root 的 sans-serif 不会自动拼接进子元素已显式覆盖的列表；缺少该字体环境仍有浏览器缺字/默认回退策略，但展示可控性较弱。代表告警有规则依据，不是 1645 个独立业务运行缺陷。"
            recommendation = "后续在图纸/预览生成流程为内联字体补通用 sans-serif 后重新生成；按文档维护优先级处理。这里只人工抽查四条，其他 1641 条不自动记为人工已审读。"
            support = []
        else:
            continue
        source = sources[path]
        evidence = []
        for source_path, first, last in support:
            source_record = sources[source_path]
            evidence.append({"path": source_path, "start_line": first, "end_line": last, "sonar_raw_source_sha256": source_record["sonar_exported_source_sha256"], "lines": excerpt(folder / "review-sources" / source_path, first, last)})
        if item["key"] in css_review_keys or item["rule"] == "Web:InputWithoutLabelCheck":
            region = item["textRange"]
            line = (folder / "review-sources" / path).read_text(encoding="utf-8").splitlines()[region["startLine"] - 1]
            lo, hi = region["startOffset"], region["endOffset"]
            evidence.append({"path": path, "line": region["startLine"], "start_offset": lo, "end_offset": hi, "token": line[lo:hi], "surround": line[max(0, lo - 120):hi + 160], "sonar_raw_source_sha256": source["sonar_exported_source_sha256"]})
        findings.append({
            "issue_key": item["key"], "rule_id": item["rule"], "path": path, "line": item.get("line"),
            "raw_type": item["type"], "raw_severity": item["severity"], "raw_message": item["message"],
            "manual_review_status": "reviewed_alert_context", "human_verdict": verdict,
            "human_severity": severity, "reason": reason, "recommendation": recommendation,
            "sonar_raw_source_sha256": source["sonar_exported_source_sha256"], "evidence": evidence,
            "current_workspace_used_as_only_source": False,
        })
    assert len(findings) == 22, len(findings)
    reviewed = {f["issue_key"] for f in findings}
    pending = [{"issue_key": item["key"], "rule_id": item["rule"], "path": item["component"].split(":", 1)[1], "line": item.get("line"), "raw_type": item["type"], "manual_review_status": "pending"} for item in issues if item["key"] not in reviewed]
    value = {
        "reviewed_at": NOW, "source": "Local SonarQube API sources/raw returned analyzed source, stored locally with hashes; not remote GitHub.",
        "issues_json_sha256": digest(folder / "issues.json"), "raw_issues_total": len(issues),
        "raw_type_counts": dict(Counter(item["type"] for item in issues)),
        "reviewed_alerts": len(findings), "reviewed_vulnerability_candidates": 10,
        "reviewed_non_css_bug_candidates": 8, "reviewed_css_representatives": 4,
        "css_rule_total": len(css), "css_pending_manual_reviews": len(css) - 4,
        "code_smell_pending_manual_reviews": 560, "finding_reviews_pending": len(pending),
        "human_verdict_counts": dict(Counter(f["human_verdict"] for f in findings)),
        "confirmed_business_runtime_defects_in_reviewed_results": 0,
        "scope": "Review is alert-specific. 2201 other Sonar findings and all unreviewed source-file portions stay pending. Static enumeration is not manual reading.",
        "source_manifest": "review-source-manifest.json", "hash_caveat": source_manifest["source_hash_caveat"],
        "support_source_limitations": ["Sonar does not retain a component for lab/src/datasets/governance.js; function signature checked against fixed CodeQL snapshot and current local source, not attributed to Sonar raw source.", "Runner intention references current local DIAGRAM_MAINTENANCE.md and evidence/acceptance-report.md; not an execution of the browser tools during this scan."],
        "findings": findings, "pending": pending,
    }
    write_json(folder / "finding-review.json", value)
    md = ["# SonarQube 高风险类别及 BUG 候选人工核验", "", f"核验时间：{NOW}。这是 [SonarQube 官方引擎结果](issues.json) 的人工解释附录。原始 2223 条候选、10 条 VULNERABILITY / 1653 条 BUG / 560 条 CODE_SMELL 完整保留；未改平台问题状态或源码。", "", "已逐条检查 10 个 VULNERABILITY、8 个非 CSS BUG，再抽查 4 个 CSS 字体代表，共 22 个告警上下文。其余 2201 条继续为 pending（1641 CSS + 560 CODE_SMELL）。没有把代表抽查或自动枚举说成全仓人工审读。", "", "| 已核部分 | 人工解释 |", "| --- | --- |", "| 6 个 Python 随机数 VULNERABILITY | 用于种子化匿名仿真图像；不是密钥/认证随机数，安全上下文误报 |", "| 3 个 HTTP VULNERABILITY | 2 个服务在真实分析源码中已有生产 TLS / 显式开发回环边界，1 个文档工具固定回环；上下文误报 |", "| 1 个外部脚本完整性 VULNERABILITY | 本地 HTML 报告缺 SRI；低优先级文档资源加固 |", "| 8 个非 CSS BUG | 5 个上下文误报、2 个外部 runner 输入的有意表达式、1 个文档选择器缺少可访问名称 |", "| 4 个 CSS 代表（原始总数 1645） | 内嵌 SVG 的 Microsoft YaHei 显式字体列表缺通用字体；低优先级文档显示回退问题 |", "", "为防止并行任务改动源码造成错误行号/结论，本附录从 `http://127.0.0.1:19000/api/sources/raw` 只读取得本机平台保存的实际分析源，存为 [review-sources](review-sources)。[source manifest](review-source-manifest.json) 记录每份返回源及当前工作区哈希。该 API 统一 LF 并增加末尾空行，原始字节 SHA 不同本身不能证明漂移；另比较仅忽略 CRLF/LF 与末尾空行的文本。预览 HTML 有实质漂移，所以 CSS offsets 的核验使用平台保存版本。", "", "两个 HTTP 业务服务的判断不使用旧快照：扫描保存的 serve_forever 前确有 TLS 包装和启动前监听策略。这个判断限于源码边界，未测试真实部署的证书、代理或运行环境。另一个随机数结论限于仿真图像用途，不构成全仓 RNG 安全证明。", "", "1645 个 CSS 自动 BUG 都来自同一份压缩成少量长行的图纸预览 HTML 中多个内联字体声明，重复呈现放大了数量。外层 :root 已有 sans-serif，但内层显式 font-family 会覆盖继承列表；不能据此认定规则全误报。它们也不能合并算作 1645 个阅卷运行故障。应通过图纸/预览正式生成流程修订样式。", ""]
    for f in findings:
        md += [f"## {f['rule_id']} · {f['issue_key']}", "", md_path(f["path"], f["line"] or 1), "", f"平台：{f['raw_type']} / {f['raw_severity']}；人工：`{f['human_verdict']}` / `{f['human_severity']}`。", "", f["reason"], "", f"建议：{f['recommendation']}", "", f"平台保存源 SHA-256：`{f['sonar_raw_source_sha256']}`。读过的具体行/内联 offsets 见 [finding-review.json](finding-review.json)。", ""]
    (folder / "finding-review.md").write_text("\n".join(md), encoding="utf-8")
    return value


if __name__ == "__main__":
    js = review_javascript()
    sonar = review_sonar()
    print(json.dumps({"javascript_reviewed": len(js["findings"]), "javascript_verdicts": js["human_verdict_counts"], "sonar_reviewed": sonar["reviewed_alerts"], "sonar_pending": sonar["finding_reviews_pending"], "sonar_verdicts": sonar["human_verdict_counts"]}, ensure_ascii=False))
