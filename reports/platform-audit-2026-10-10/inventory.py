"""记录本地源码清单与哈希；自动盘点不代表人工审读。"""
from __future__ import annotations

import csv
import hashlib
import json
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
OUT = Path(__file__).resolve().parent
EXCLUDED = {
    ".git", "node_modules", ".venv", "venv", "__pycache__", ".pytest_cache",
    "dist", "build", "target", ".next", "output", "reports", "playwright-report",
    "test-results", "vendor", "coverage", ".sonarqube", ".scannerwork",
}
CODE_EXTENSIONS = {
    ".go", ".py", ".js", ".mjs", ".cjs", ".ts", ".tsx", ".jsx", ".rs",
    ".sql", ".sh", ".ps1", ".yaml", ".yml", ".html", ".css",
}
CONFIG_NAMES = {"Dockerfile", "go.mod", "package.json", "pyproject.toml", "Cargo.toml"}


def collect() -> list[dict]:
    # 只读取本地 Git 索引及工作区；未跟踪、未被忽略的维护源码也纳入盘点。
    raw = subprocess.check_output(["git", "ls-files", "-co", "--exclude-standard", "-z"], cwd=ROOT)
    paths = sorted({x.decode("utf-8") for x in raw.split(b"\0") if x})
    records = []
    for rel in paths:
        path = ROOT / rel
        if not path.is_file() or EXCLUDED.intersection(Path(rel).parts):
            continue
        if path.suffix.lower() not in CODE_EXTENSIONS and path.name not in CONFIG_NAMES:
            continue
        content = path.read_bytes()
        records.append({
            "path": rel,
            "bytes": len(content),
            "sha256": hashlib.sha256(content).hexdigest(),
            "text_lines": len(content.splitlines()),
            "manual_review_status": "pending",
            "protected_file": rel.startswith("packages/sdk/src/generated/") or "/migrations/" in rel or rel.endswith(".sql"),
        })
    return records


def main() -> None:
    mode = sys.argv[1] if len(sys.argv) > 1 else "resume"
    records = collect()
    target = OUT / ("source-manifest-resume-2026-10-11.csv" if mode == "resume" else "source-manifest-after.csv")
    with target.open("w", encoding="utf-8-sig", newline="") as stream:
        writer = csv.DictWriter(stream, fieldnames=list(records[0]))
        writer.writeheader()
        writer.writerows(records)
    if mode == "resume":
        summary = {
            "head": subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=ROOT, text=True).strip(),
            "repository_remote_not_queried": True,
            "source_inventory_files": len(records),
            "physical_text_lines": sum(x["text_lines"] for x in records),
            "inventory_is_not_manual_review": True,
            "manifest": target.name,
        }
        (OUT / "scope-summary-resume-2026-10-11.json").write_text(json.dumps(summary, ensure_ascii=False, indent=2), encoding="utf-8")
        status = subprocess.check_output(["git", "status", "--porcelain=v1"], cwd=ROOT)
        (OUT / "baseline-status-resume-2026-10-11.txt").write_bytes(status)
        print(json.dumps(summary, ensure_ascii=False))
    else:
        with (OUT / "source-manifest-resume-2026-10-11.csv").open(encoding="utf-8-sig", newline="") as stream:
            before = {row["path"]: row["sha256"] for row in csv.DictReader(stream)}
        after = {row["path"]: row["sha256"] for row in records}
        diff = {
            "changed": sorted(p for p in before.keys() & after.keys() if before[p] != after[p]),
            "added": sorted(after.keys() - before.keys()),
            "removed": sorted(before.keys() - after.keys()),
        }
        (OUT / "source-drift.json").write_text(json.dumps(diff, ensure_ascii=False, indent=2), encoding="utf-8")
        print(json.dumps({k: len(v) for k, v in diff.items()}))


if __name__ == "__main__":
    main()
