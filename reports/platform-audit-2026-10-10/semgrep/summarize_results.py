"""导出位置与规则信息；控制台不打印命中的源码或潜在秘密值。"""

import collections
import csv
import json
from pathlib import Path

report = Path(__file__).resolve().parent
rules = json.loads((report / "merged-official-rules.json").read_text(encoding="utf-8"))["rules"]
target_languages = {"go", "python", "js", "javascript", "ts", "typescript", "generic", "regex", "json", "yaml", "dockerfile", "html", "bash"}
capabilities = {
    "loaded_rules": len(rules),
    "rules_with_target_language": sum(bool(set(rule.get("languages", [])) & target_languages) for rule in rules),
    "no_target_language_rules": sum(not bool(set(rule.get("languages", [])) & target_languages) for rule in rules),
    "interfile_option_rules": [rule["id"] for rule in rules if rule.get("options", {}).get("interfile")],
    "scope_note": "语言匹配仅表示可应用规则；实际错误与跳过以原始 JSON 和日志为准。CE 不提供 Pro 跨文件分析。",
}
(report / "rule-capabilities.json").write_text(json.dumps(capabilities, ensure_ascii=False, indent=2), encoding="utf-8")
raw = json.loads((report / "semgrep-raw.json").read_text(encoding="utf-8"))
findings = []
for index, item in enumerate(raw.get("results", []), 1):
    extra = item.get("extra", {})
    metadata = extra.get("metadata", {})
    findings.append({
        "id": f"SG-{index:03d}",
        "path": item["path"].removeprefix("/tmp/yuejuan2-semgrep-source/"),
        "line": item["start"]["line"],
        "end_line": item["end"]["line"],
        "rule": item["check_id"],
        "severity": extra.get("severity"),
        "confidence": metadata.get("confidence", "UNKNOWN"),
        "subcategory": metadata.get("subcategory", []),
        "message": extra.get("message", ""),
        "human_status": "pending",
        "human_severity": None,
        "human_notes": "尚未结合调用关系和保护措施核验，不计为已确认漏洞。",
    })
(report / "findings-triage.json").write_text(json.dumps(findings, ensure_ascii=False, indent=2), encoding="utf-8")
with (report / "findings.csv").open("w", encoding="utf-8-sig", newline="") as output:
    writer = csv.DictWriter(output, fieldnames=list(findings[0]) if findings else ["id", "path", "line", "rule", "severity", "human_status"])
    writer.writeheader()
    for finding in findings:
        writer.writerow(finding)
summary = {
    "version": raw.get("version"),
    "findings": len(findings),
    "rules_with_findings": len(set(item["rule"] for item in findings)),
    "severities": dict(collections.Counter(item["severity"] for item in findings)),
    "errors": len(raw.get("errors", [])),
    "scanned_files": len(raw.get("paths", {}).get("scanned", [])),
    "rules": dict(collections.Counter(item["rule"] for item in findings)),
    "errors_by_type": dict(collections.Counter(str(item.get("type", "unknown")) for item in raw.get("errors", []))),
    "rule_skips": raw.get("skipped_rules", []),
    "rules_error_count": len(raw.get("rules", {}).get("errors", [])) if isinstance(raw.get("rules"), dict) else None,
}
(report / "scan-summary.json").write_text(json.dumps(summary, ensure_ascii=False, indent=2), encoding="utf-8")
print(json.dumps(summary, ensure_ascii=False, indent=2))
for finding in findings:
    print(f'{finding["id"]} {finding["severity"]} {finding["path"]}:{finding["line"]} {finding["rule"]}')
