import collections
import csv
import hashlib
import json
import pathlib
import re
import zipfile

ROOT = pathlib.Path(__file__).parent
REPORT = pathlib.Path("D:/project/yuejuan/reports/platform-audit-2026-10-10/codeql")
LANGUAGES = ("python", "javascript", "go", "rust", "actions")


def load(path):
    return json.loads(path.read_text(encoding="utf-8-sig")) if path.exists() else None


def metadata(database):
    path = database / "codeql-database.yml"
    if not path.exists():
        return {}
    text = path.read_text(encoding="utf-8")
    result = {}
    for name in ("baselineLinesOfCode", "primaryLanguage", "buildMode", "finalised", "sourceLocationPrefix"):
        match = re.search(rf"^{name}: (.+)$", text, re.M)
        if match:
            result[name] = match[1]
    archive = database / "src.zip"
    if archive.exists():
        with zipfile.ZipFile(archive) as source:
            result["archived_source_files"] = len(source.infolist())
            (REPORT / f"{result.get('primaryLanguage', database.name)}-archived-source-files.txt").write_text("\n".join(source.namelist()), encoding="utf-8")
    return result


if __name__ == "__main__":
    summary = {"engine": "GitHub CodeQL CLI 2.27.2", "source_snapshot": load(REPORT / "source-snapshot.json"), "languages": {}}
    python_quality = load(REPORT / "python-quality-review.json")
    python_reviews = {item["sarif_result_index_zero_based"]: item for item in python_quality.get("findings", [])} if python_quality else {}
    alerts = []
    for language in LANGUAGES:
        # 人工结论必须按原始 SARIF 的索引、规则与位置逐项匹配，不能套用旧扫描结论。
        finding_review = load(REPORT / f"{language}-finding-review.json")
        sarif_file = REPORT / f"{language}.sarif"
        if finding_review and finding_review.get("sarif_sha256") and sarif_file.is_file():
            # 同行号仍可能对应新的流路径；复扫报告必须重新绑定完整原始 SARIF。
            if hashlib.sha256(sarif_file.read_bytes()).hexdigest() != finding_review["sarif_sha256"]:
                finding_review = None
        finding_reviews = {item["sarif_result_index_zero_based"]: item for item in finding_review.get("findings", [])} if finding_review else {}
        create = load(REPORT / f"{language}-create-execution.json")
        analyze = load(REPORT / f"{language}-analyze-execution.json")
        prior_analysis = None
        if create and analyze and create["command"][3] != analyze["command"][3]:
            # 新库建成而尚在查询时，旧库成功记录不能冒充新库分析完成。
            prior_analysis, analyze = analyze, None
        state = {"create": create, "analyze": analyze, "status": "pending_analysis" if create and create["exit_code"] == 0 else "pending_database"}
        if prior_analysis:
            state["previous_database_analysis"] = prior_analysis
        analysis_log = REPORT / f"{language}-analyze.log"
        if analysis_log.exists():
            logged = analysis_log.read_text(encoding="utf-8", errors="replace")
            state["scanned_files_diagnostic"] = [line for line in logged.splitlines() if line.startswith("CodeQL scanned ")]
            # 加载查询和完成计算不是同一件事；仅计官方日志中的真实 Evaluation done。
            state["query_evaluation_completions_in_log"] = len(re.findall(r"\[\d+/\d+ eval .*?\] Evaluation done", logged))
            if language == "rust":
                metrics = {}
                for field, title in (("files_with_extraction_errors", "with errors"), ("files_without_extraction_error", "without error")):
                    match = re.search(rf"\|\s*Total number of Rust files that were extracted {title}\s*\|\s*(\d+)\s*\|", logged)
                    if match:
                        metrics[field] = int(match[1])
                if metrics:
                    state["official_extraction_metrics"] = metrics
                diagnostics = load(REPORT / "rust-extraction-diagnostics.json")
                if diagnostics and create and diagnostics.get("database") == create["command"][3]:
                    state["extraction_warning_count"] = diagnostics["warnings_count"]
                    state["extraction_limitations"] = diagnostics["limitations"]
                    if diagnostics.get("official_diagnostic_query_counts"):
                        state["official_diagnostic_query_counts"] = diagnostics["official_diagnostic_query_counts"]
                        state["official_diagnostic_query_summary"] = diagnostics["official_diagnostic_query_summary"]
        queries = load(REPORT / f"{language}-query-manifest.json")
        if queries is not None:
            state["selected_queries"] = len(queries)
        if create:
            state["database"] = metadata(pathlib.Path(create["command"][3]))
            if language == "rust":
                context = load(REPORT / "rust-context-source-verification.json")
                source_argument = next((arg.split("=", 1)[1] for arg in create["command"] if arg.startswith("--source-root=")), None)
                if context and source_argument == context["probe_root"]:
                    state["source_context_supplement"] = {
                        "source_root": context["probe_root"],
                        "original_manifest_entries": context["files"],
                        "all_original_manifest_entries_unchanged": context.get("all_original_manifest_entries_unchanged"),
                        "added_assets": context["added_assets"],
                        "generated_build_context": context.get("generated_build_context", []),
                        "verification_file": "rust-context-source-verification.json",
                    }
        sarif = load(REPORT / f"{language}.sarif")
        if analyze and analyze["exit_code"] == 0 and sarif:
            state["status"] = "analyzed"
            groups, levels, severities, locations = collections.Counter(), collections.Counter(), collections.Counter(), set()
            rule_count = 0
            for run in sarif.get("runs", []):
                notifications = collections.Counter()
                for invocation in run.get("invocations", []):
                    for notification in invocation.get("toolExecutionNotifications", []):
                        notifications[notification.get("level", "warning")] += 1
                state["tool_execution_notifications"] = dict(notifications)
                state["sarif_invocations_successful"] = all(invocation.get("executionSuccessful", True) for invocation in run.get("invocations", []))
                rules = {}
                components = [run["tool"]["driver"], *run["tool"].get("extensions", [])]
                for component in components:
                    for rule in component.get("rules", []):
                        rules[rule["id"]] = rule
                rule_count += len(rules)
                for result_index, item in enumerate(run.get("results", [])):
                    rule = rules.get(item.get("ruleId"), {})
                    properties = rule.get("properties", {})
                    score = properties.get("security-severity")
                    security = "not_security" if score is None else "critical" if float(score) >= 9 else "high" if float(score) >= 7 else "medium" if float(score) >= 4 else "low"
                    level = item.get("level", rule.get("defaultConfiguration", {}).get("level", "warning"))
                    location = item.get("locations", [{}])[0].get("physicalLocation", {})
                    path = location.get("artifactLocation", {}).get("uri", "")
                    line = location.get("region", {}).get("startLine", "")
                    record = {"language": language, "sarif_result_index_zero_based": result_index, "rule_id": item.get("ruleId", ""), "title": rule.get("shortDescription", {}).get("text", ""), "level": level, "security_severity": security, "security_score": score or "", "precision": properties.get("precision", ""), "path": path, "line": line, "manual_review_status": "pending", "human_verdict": "pending"}
                    if result_index in finding_reviews:
                        review = finding_reviews[result_index]
                        if review["rule_id"] != item["ruleId"] or review["path"] != path or review["line"] != line:
                            raise RuntimeError(f"{language} finding review no longer matches SARIF result")
                        record.update(manual_review_status=review["manual_review_status"], human_verdict=review["human_verdict"])
                    elif language == "python" and result_index in python_reviews:
                        review = python_reviews[result_index]
                        if review["rule_id"] != item["ruleId"] or review["path"] != path or review["line"] != line:
                            raise RuntimeError("Quality review no longer matches SARIF result")
                        record.update(manual_review_status=review["manual_review_status"], human_verdict=review["human_verdict"])
                    elif language == "python" and item.get("ruleId") == "py/polynomial-redos":
                        record.update(manual_review_status="reviewed_alert_context", human_verdict="confirmed_cpu_amplification_conditional")
                    # 原始 SARIF 留在本地；摘要不复制可能包含密钥的代码或消息片段。
                    alerts.append(record)
                    groups[item.get("ruleId", "")] += 1
                    levels[level] += 1
                    severities[security] += 1
                    locations.add(path)
            state.update(alerts=sum(groups.values()), rules_in_sarif=rule_count, result_rules=dict(groups), levels=dict(levels), security_severity=dict(severities), affected_files=len(locations))
            if language == "python" and python_quality:
                state["quality_human_verdicts"] = python_quality["human_verdict_counts"]
                state["quality_finding_reviews_pending"] = python_quality["finding_reviews_pending"]
            if finding_review:
                state["human_verdicts"] = finding_review["human_verdict_counts"]
                state["finding_reviews_pending"] = finding_review["finding_reviews_pending"]
        elif analyze and analyze["exit_code"] != 0:
            state["status"] = "analysis_failed"
        elif create and create["exit_code"] != 0:
            state["status"] = "database_failed"
        summary["languages"][language] = state
    summary["completed_language_count"] = sum(state["status"] == "analyzed" for state in summary["languages"].values())
    summary["planned_language_count"] = len(LANGUAGES)
    summary["total_alerts"] = len(alerts) if summary["completed_language_count"] else None
    summary["all_planned_language_analyses_successful"] = summary["completed_language_count"] == len(LANGUAGES) and all(state.get("sarif_invocations_successful") is True for state in summary["languages"].values())
    summary["selected_query_count"] = sum(state.get("selected_queries", 0) for state in summary["languages"].values())
    summary["query_evaluation_completions_in_successful_logs"] = sum(state.get("query_evaluation_completions_in_log", 0) for state in summary["languages"].values() if state["status"] == "analyzed")
    summary["all_selected_queries_have_logged_evaluation_completion"] = summary["all_planned_language_analyses_successful"] and all(state.get("query_evaluation_completions_in_log") == state.get("selected_queries") for state in summary["languages"].values())
    summary["human_verdict_counts"] = dict(collections.Counter(record["human_verdict"] for record in alerts))
    summary["finding_reviews_pending"] = sum(record["manual_review_status"] == "pending" for record in alerts)
    (REPORT / "summary.json").write_text(json.dumps(summary, ensure_ascii=False, indent=2), encoding="utf-8")
    with (REPORT / "alerts.csv").open("w", encoding="utf-8-sig", newline="") as output:
        columns = ["language", "sarif_result_index_zero_based", "rule_id", "title", "level", "security_severity", "security_score", "precision", "path", "line", "manual_review_status", "human_verdict"]
        writer = csv.DictWriter(output, fieldnames=columns)
        writer.writeheader()
        writer.writerows(alerts)
    print(json.dumps({"total_alerts": summary["total_alerts"], "completed_language_count": summary["completed_language_count"], "languages": {lang: {key: value for key, value in state.items() if key in ("status", "alerts", "security_severity", "levels", "result_rules")} for lang, state in summary["languages"].items()}}, ensure_ascii=False))
