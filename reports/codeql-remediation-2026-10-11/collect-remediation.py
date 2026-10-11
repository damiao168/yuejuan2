"""将本轮逐条修复记录与原始 SARIF 对齐，保留原扫描证据。"""

from collections import Counter
import csv
import hashlib
import json
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
OUT = Path(__file__).resolve().parent
ORIGINAL = ROOT / "reports/platform-audit-2026-10-10/codeql"
LANGUAGES = ("python", "javascript", "go", "rust", "actions")
STATUSES = {"fixed", "already_fixed", "false_positive", "intentional", "pending"}


def read_json(path):
    return json.loads(path.read_text(encoding="utf-8-sig"))


def sha256(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def collect():
    all_findings = []
    languages = {}
    changed_files = set()
    for language in LANGUAGES:
        source = ORIGINAL / f"{language}.sarif"
        results = read_json(source)["runs"][0].get("results", [])
        remediation_path = OUT / f"{language}-remediation.json"
        remediation = read_json(remediation_path)
        findings = remediation["findings"]
        indices = [entry["resultIndex"] for entry in findings]
        # 完整性由原始 result 索引校验，不能用记录总数替代逐项对应。
        if sorted(indices) != list(range(len(results))):
            raise ValueError(f"{language}: original SARIF indices missing or duplicated")
        for entry in findings:
            result = results[entry["resultIndex"]]
            original_path = result["locations"][0]["physicalLocation"]["artifactLocation"]["uri"]
            if entry["ruleId"] != result["ruleId"] or entry["path"] != original_path:
                raise ValueError(f"{language}: finding identity mismatch at {entry['resultIndex']}")
            if entry["status"] not in STATUSES or not entry["reason"].strip():
                raise ValueError(f"{language}: invalid disposition at {entry['resultIndex']}")
            if entry["status"] == "fixed" and not entry["changedFiles"]:
                raise ValueError(f"{language}: fixed finding has no changed files")
            changed_files.update(entry["changedFiles"])
            all_findings.append({"language": language, **entry})
        changed_files.update(remediation.get("changedFiles", []))
        languages[language] = {
            "originalCount": len(results),
            "originalSarifSha256": sha256(source),
            "statusCounts": dict(Counter(entry["status"] for entry in findings)),
            "tests": remediation.get("tests", []),
            "report": remediation_path.name,
        }

    with (OUT / "source-before.csv").open(encoding="utf-8-sig", newline="") as handle:
        baseline = {entry["path"]: entry["sha256"] for entry in csv.DictReader(handle)}
    verification = []
    for relative in sorted(changed_files):
        path = ROOT / relative
        if not path.is_file():
            raise ValueError(f"changed file not found: {relative}")
        verification.append({
            "path": relative,
            "beforeSha256": baseline.get(relative),
            "afterSha256": sha256(path),
            "newFile": relative not in baseline,
        })

    summary = {
        "scope": "原报告的全部106条候选在当前工作区逐条修复或核验；不代表全仓人工审读或新一轮CodeQL扫描。",
        "originalEvidenceModified": False,
        "newCodeqlScanExecuted": False,
        "originalCount": len(all_findings),
        "statusCounts": dict(Counter(entry["status"] for entry in all_findings)),
        "languages": languages,
        "changedFiles": verification,
        "findings": all_findings,
    }
    (OUT / "summary.json").write_text(json.dumps(summary, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    fields = ["language", "resultIndex", "ruleId", "path", "status", "reason"]
    with (OUT / "findings.csv").open("w", encoding="utf-8-sig", newline="") as handle:
        writer = csv.DictWriter(handle, fields, extrasaction="ignore")
        writer.writeheader()
        writer.writerows(all_findings)
    print(json.dumps({"originalCount": summary["originalCount"], "statusCounts": summary["statusCounts"], "changedFiles": len(verification)}, ensure_ascii=False))


if __name__ == "__main__":
    collect()
