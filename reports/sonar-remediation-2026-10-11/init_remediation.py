"""保存修复前的工作区字节与原告警账本，保护已有未提交修改。"""
from collections import Counter
from datetime import datetime, timedelta, timezone
import hashlib
import json
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parent
WORKSPACE = ROOT.parent.parent
ORIGINAL = WORKSPACE / "reports/platform-audit-2026-10-10/sonar/issues.json"
BACKUP = Path("D:/tools/sonar-remediation-20261011/baseline")


def main():
    baseline_path = ROOT / "baseline.json"
    if baseline_path.exists():
        raise SystemExit("Baseline already exists; do not replace prior evidence.")
    issues = json.loads(ORIGINAL.read_text(encoding="utf-8-sig"))["issues"]
    paths = {item["component"].split(":", 1)[1] for item in issues}
    paths.add("docs/architecture/tools/build_preview.py")
    files = []
    for relative in sorted(paths):
        source = WORKSPACE / relative
        if not source.is_file():
            files.append({"path": relative, "exists": False, "file_review_status": "pending"})
            continue
        data = source.read_bytes()
        saved = BACKUP / relative
        saved.parent.mkdir(parents=True, exist_ok=True)
        saved.write_bytes(data)
        files.append({"path": relative, "exists": True, "bytes": len(data), "sha256": hashlib.sha256(data).hexdigest(), "backup": str(saved), "file_review_status": "pending"})
    severity = {"BLOCKER": 0, "CRITICAL": 1, "MAJOR": 2, "MINOR": 3, "INFO": 4}
    order = sorted(issues, key=lambda item: (severity[item["severity"]], item["component"], item.get("line", 0), item["key"]))
    ledger = [{"order": index + 1, "issue_key": item["key"], "rule": item["rule"], "severity": item["severity"], "type": item["type"], "path": item["component"].split(":", 1)[1], "original_line": item.get("line"), "message": item["message"], "status": "pending"} for index, item in enumerate(order)]
    stamp = datetime.now(timezone(timedelta(hours=8))).isoformat()
    baseline = {"recorded_at": stamp, "workspace": str(WORKSPACE), "backup_root": str(BACKUP), "original_issues_sha256": hashlib.sha256(ORIGINAL.read_bytes()).hexdigest(), "issue_count": len(issues), "files": files, "note": "当前工作区基线，不是Git HEAD；未审读文件仍为pending，原扫描报告不修改。"}
    ROOT.mkdir(parents=True, exist_ok=True)
    baseline_path.write_text(json.dumps(baseline, ensure_ascii=False, indent=2), encoding="utf-8")
    (ROOT / "issues-ledger.json").write_text(json.dumps({"created_at": stamp, "issues": ledger}, ensure_ascii=False, indent=2), encoding="utf-8")
    status = subprocess.run(["git", "status", "--porcelain=v1", "-uall"], cwd=WORKSPACE, capture_output=True, check=True)
    (ROOT / "git-status-before.txt").write_bytes(status.stdout)
    print(json.dumps({"issues": len(issues), "affected_files_plus_generator": len(files), "missing": sum(not row["exists"] for row in files), "severities": Counter(item["severity"] for item in issues)}))


if __name__ == "__main__":
    main()
