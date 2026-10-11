"""隔离复制本地文本源码，运行禁网容器中的真实 Semgrep CLI。"""

import collections
import datetime
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import time
import zipfile

source_root = Path("/src")
scan_root = Path("/tmp/yuejuan2-semgrep-source")
report_root = Path("/audit")
excluded_dirs = {
    ".git", "node_modules", ".venv", "venv", "__pycache__", "target", "dist",
    "build", "coverage", ".cache", ".pytest_cache", ".ruff_cache", ".mypy_cache",
    ".gotmp", ".playwright-cli", ".vite", ".tmp-pg-migration-verify", ".claude",
    "bin", "output", "reports", "tmp", "backups", "certs", ".cookies",
    "frontend-review-report", "edugrade_worker_runtime.egg-info",
    ".downloads", ".models", ".runtime",
}
text_extensions = {
    ".go", ".py", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".rs",
    ".yaml", ".yml", ".json", ".sql", ".html", ".css", ".sh", ".ps1",
    ".md", ".toml", ".xml", ".vue", ".svelte", ".graphql", ".txt", ".conf",
    ".mod", ".sum", ".dockerignore", ".gitignore", ".example",
}
inventory = []
skipped = []
scan_root.mkdir(parents=True, exist_ok=True)
snapshot_zip = Path("/snapshot/source.zip")
if snapshot_zip.exists():
    manifest = json.loads((report_root / "source-inventory.json").read_text(encoding="utf-8"))
    inventory = manifest["source_files"]
    skipped = manifest["excluded"]
    with zipfile.ZipFile(snapshot_zip) as archive:
        archive.extractall(scan_root)
for directory, dir_names, file_names in ([] if snapshot_zip.exists() else os.walk(source_root)):
    original_dir_names = list(dir_names)
    dir_names[:] = [name for name in dir_names if name not in excluded_dirs and not name.endswith(".egg-info")]
    for name in original_dir_names:
        if name not in dir_names:
            skipped.append({"path": str((Path(directory) / name).relative_to(source_root)), "reason": "dependency/output/cache/runtime_directory"})
    for name in file_names:
        source = Path(directory) / name
        relative = source.relative_to(source_root)
        if source.is_symlink():
            skipped.append({"path": str(relative), "reason": "symlink_not_followed"})
            continue
        if (name.startswith(".env") and name != ".env.example") or name == ".cookies":
            skipped.append({"path": str(relative), "reason": "runtime_secret_input"})
            continue
        if name.startswith(".recovery-test-") or relative.as_posix().startswith("apps/desktop-client/src-tauri/gen/") or relative.as_posix().startswith("packages/sdk/src/generated/"):
            skipped.append({"path": str(relative), "reason": "generated_artifact"})
            continue
        if source.suffix.lower() not in text_extensions and name not in {"Dockerfile", "Makefile", ".env.example", ".dockerignore", ".gitignore"}:
            continue
        content = source.read_bytes()
        if b"\0" in content or len(content) > 5_000_000:
            skipped.append({"path": str(relative), "reason": "binary_or_over_5MB"})
            continue
        target = scan_root / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(content)
        inventory.append({"path": str(relative), "bytes": len(content), "sha256": hashlib.sha256(content).hexdigest(), "human_review": "pending"})

# 空忽略文件只写到隔离副本。原工作区只读；不沿用默认对测试与文档的忽略。
(scan_root / ".semgrepignore").write_text("", encoding="utf-8")
if not snapshot_zip.exists():
    (report_root / "source-inventory.json").write_text(json.dumps({"source": str(source_root), "source_files": inventory, "excluded": skipped}, ensure_ascii=False, indent=2), encoding="utf-8")
os.environ["SEMGREP_SEND_METRICS"] = "off"
os.environ["SEMGREP_ENABLE_VERSION_CHECK"] = "0"
os.environ["SEMGREP_APP_TOKEN"] = ""
os.environ["SEMGREP_USER_AGENT_APPEND"] = "local-offline-workspace-audit"
command = [
    "semgrep", "scan", "--oss-only",
    "--config", str(report_root / "merged-official-rules.json"),
    "--metrics", "off", "--disable-version-check", "--no-git-ignore", "--disable-nosem",
    "--jobs", "2", "--max-memory", "600", "--timeout", "20", "--timeout-threshold", "3",
    "--max-target-bytes", "5000000", "--json", "--output", str(report_root / "semgrep-raw.json"),
    str(scan_root),
]
started = datetime.datetime.now(datetime.timezone.utc).isoformat()
begin = time.monotonic()
with (report_root / "scan.stderr.log").open("w", encoding="utf-8") as stderr:
    completed = subprocess.run(command, cwd=scan_root, stdout=subprocess.DEVNULL, stderr=stderr, check=False)
metadata = {
    "started_utc": started,
    "ended_utc": datetime.datetime.now(datetime.timezone.utc).isoformat(),
    "elapsed_seconds": round(time.monotonic() - begin, 3),
    "command": command,
    "exit_code": completed.returncode,
    "inventory_files": len(inventory),
    "inventory_bytes": sum(item["bytes"] for item in inventory),
    "excluded_entries": len(skipped),
    "container_network": "none",
    "source_mount": "read-only",
    "snapshot_mode": "local-Windows-ZIP" if snapshot_zip.exists() else "read-only-bind-copy",
}
version = subprocess.run(["semgrep", "--version", "--disable-version-check"], text=True, capture_output=True, check=False)
metadata["version"] = version.stdout.strip()
(report_root / "scan-metadata.json").write_text(json.dumps(metadata, ensure_ascii=False, indent=2), encoding="utf-8")
print(json.dumps(metadata, ensure_ascii=False, indent=2))
raw_path = report_root / "semgrep-raw.json"
if raw_path.exists():
    result = json.loads(raw_path.read_text(encoding="utf-8"))
    summary = {
        "version": result.get("version"),
        "findings": len(result.get("results", [])),
        "severities": dict(collections.Counter(item["extra"]["severity"] for item in result.get("results", []))),
        "errors": len(result.get("errors", [])),
        "scanned_files": len(result.get("paths", {}).get("scanned", [])),
    }
    (report_root / "scan-summary.json").write_text(json.dumps(summary, indent=2), encoding="utf-8")
    print(json.dumps(summary, indent=2))
sys.exit(completed.returncode)
