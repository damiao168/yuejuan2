"""在 Windows 本机读取当前源码，避免容器挂载逐文件复制的延迟。"""

import ast
import datetime
import hashlib
import json
import os
from pathlib import Path
import tempfile
import zipfile
import subprocess

report_dir = Path(__file__).resolve().parent
source_root = report_dir.parents[2]
tool_tree = ast.parse((report_dir / "local_scan.py").read_text(encoding="utf-8"))
constants = {}
for node in tool_tree.body:
    if isinstance(node, ast.Assign) and len(node.targets) == 1 and isinstance(node.targets[0], ast.Name) and node.targets[0].id in {"excluded_dirs", "text_extensions"}:
        constants[node.targets[0].id] = ast.literal_eval(node.value)
excluded_dirs = constants["excluded_dirs"]
text_extensions = constants["text_extensions"]
snapshot_dir = Path(tempfile.gettempdir()) / "yuejuan2-semgrep-snapshot-20261011"
snapshot_dir.mkdir(parents=True, exist_ok=True)
snapshot_path = snapshot_dir / "source.zip"
inventory = []
skipped = []
started = datetime.datetime.now(datetime.timezone.utc).isoformat()
tracked = subprocess.run(["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z"], cwd=source_root, check=True, stdout=subprocess.PIPE).stdout
local_files = sorted(set(item.decode("utf-8") for item in tracked.split(b"\0") if item))
with zipfile.ZipFile(snapshot_path, "w", compression=zipfile.ZIP_STORED) as archive:
    for relative in local_files:
        source = source_root / relative
        name = source.name
        if not source.is_file():
            skipped.append({"path": relative, "reason": "deleted_or_not_a_file"})
            continue
        if any(part in excluded_dirs or part.startswith(".venv") or part.endswith(".egg-info") for part in Path(relative).parts[:-1]):
            skipped.append({"path": relative, "reason": "dependency/output/cache/runtime_directory"})
            continue
        if source.is_symlink():
            skipped.append({"path": relative, "reason": "symlink_not_followed"})
            continue
        if (name.startswith(".env") and name != ".env.example") or name == ".cookies":
            skipped.append({"path": relative, "reason": "runtime_secret_input"})
            continue
        if name.startswith(".recovery-test-") or relative.startswith("apps/desktop-client/src-tauri/gen/") or relative.startswith("packages/sdk/src/generated/"):
            skipped.append({"path": relative, "reason": "generated_artifact"})
            continue
        if source.suffix.lower() not in text_extensions and name not in {"Dockerfile", "Makefile", ".env.example", ".dockerignore", ".gitignore"}:
            continue
        content = source.read_bytes()
        if b"\0" in content or len(content) > 5_000_000:
            skipped.append({"path": relative, "reason": "binary_or_over_5MB"})
            continue
        archive.writestr(relative, content)
        inventory.append({"path": relative, "bytes": len(content), "sha256": hashlib.sha256(content).hexdigest(), "human_review": "pending"})
    # 只有副本使用空忽略文件；原始源码和注释完全保持现状。
    archive.writestr(".semgrepignore", "")
manifest = {
    "source": str(source_root),
    "snapshot_started_utc": started,
    "snapshot_ended_utc": datetime.datetime.now(datetime.timezone.utc).isoformat(),
    "snapshot_zip": str(snapshot_path),
    "snapshot_sha256": hashlib.sha256(snapshot_path.read_bytes()).hexdigest(),
    "enumeration": "local git ls-files --cached --others --exclude-standard -z; no remote access",
    "source_files": inventory,
    "excluded": skipped,
}
(report_dir / "source-inventory.json").write_text(json.dumps(manifest, ensure_ascii=False, indent=2), encoding="utf-8")
print(json.dumps({"snapshot": str(snapshot_path), "files": len(inventory), "bytes": sum(item["bytes"] for item in inventory), "zip_sha256": manifest["snapshot_sha256"]}, indent=2))
