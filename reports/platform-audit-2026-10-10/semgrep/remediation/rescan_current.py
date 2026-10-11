"""在独立快照复扫当前工作区，结果另存，不覆盖首次审计证据。"""

import argparse
import ast
from collections import Counter
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import time


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--semgrep", required=True, help="隔离安装的 Semgrep 可执行文件")
    args = parser.parse_args()
    remediation = Path(__file__).resolve().parent
    audit = remediation.parent
    repo = audit.parents[2]
    output = remediation / "rescan"
    output.mkdir(exist_ok=True)
    constants = {}
    # 只读取原执行器的范围常量，不执行它，避免覆盖历史报告。
    for node in ast.parse((audit / "local_scan.py").read_text(encoding="utf-8")).body:
        if (isinstance(node, ast.Assign) and len(node.targets) == 1
                and isinstance(node.targets[0], ast.Name)
                and node.targets[0].id in {"excluded_dirs", "text_extensions"}):
            constants[node.targets[0].id] = ast.literal_eval(node.value)
    snapshot = Path(tempfile.mkdtemp(prefix="yuejuan-semgrep-remediation-"))
    names = subprocess.run(
        ["git", "ls-files", "--cached", "--others", "--exclude-standard", "-z"],
        cwd=repo, check=True, capture_output=True, timeout=90,
    ).stdout
    inventory = []
    excluded = []
    for name in sorted({entry.decode("utf-8") for entry in names.split(b"\0") if entry}):
        relative = Path(name)
        source = repo / relative
        if any(part in constants["excluded_dirs"] or part.startswith(".venv")
               or part.endswith(".egg-info") for part in relative.parts[:-1]):
            excluded.append({"path": name, "reason": "dependency/output/cache/runtime_directory"})
            continue
        if not source.is_file() or source.is_symlink():
            continue
        if (source.name.startswith(".env") and source.name != ".env.example") or source.name == ".cookies":
            excluded.append({"path": name, "reason": "runtime_secret_input"})
            continue
        if (source.name.startswith(".recovery-test-") or name.startswith("apps/desktop-client/src-tauri/gen/")
                or name.startswith("packages/sdk/src/generated/")):
            excluded.append({"path": name, "reason": "generated_artifact"})
            continue
        if (source.suffix.lower() not in constants["text_extensions"]
                and source.name not in {"Dockerfile", "Makefile", ".env.example", ".dockerignore", ".gitignore"}):
            continue
        content = source.read_bytes()
        if b"\0" in content or len(content) > 5_000_000:
            continue
        target = snapshot / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(content)
        inventory.append({"path": name, "sha256": hashlib.sha256(content).hexdigest(), "bytes": len(content)})
    # 空忽略文件仅在新副本中使用，保留仓库原有忽略策略和当前字节。
    (snapshot / ".semgrepignore").write_text("", encoding="utf-8")
    (output / "source-inventory.json").write_text(json.dumps({
        "source": str(repo), "snapshot": str(snapshot), "source_files": inventory, "excluded": excluded,
    }, ensure_ascii=False, indent=2), encoding="utf-8")
    rules = output / "merged-official-rules.json"
    shutil.copyfile(audit / rules.name, rules)
    command = [
        args.semgrep, "scan", "--oss-only", "--config", str(rules),
        "--metrics", "off", "--disable-version-check", "--no-git-ignore", "--disable-nosem",
        "--jobs", "1", "--max-memory", "600", "--timeout", "20", "--timeout-threshold", "3",
        "--max-target-bytes", "5000000", "--json", "--output", str(output / "semgrep-raw.json"),
        str(snapshot),
    ]
    env = dict(os.environ)
    env.update(SEMGREP_SEND_METRICS="off", SEMGREP_ENABLE_VERSION_CHECK="0", SEMGREP_APP_TOKEN="", PYTHONUTF8="1")
    metadata = {"started_utc": datetime.now(timezone.utc).isoformat(), "command": command,
                "source_files": len(inventory), "platform": "Windows native beta",
                "network_isolation": False, "metrics": "off", "cloud_upload": "not_requested"}
    print(json.dumps({"snapshot_files": len(inventory), "snapshot": str(snapshot)}), flush=True)
    started = time.monotonic()
    with (output / "scan.stdout.log").open("w", encoding="utf-8") as stdout, (output / "scan.stderr.log").open("w", encoding="utf-8") as stderr:
        try:
            completed = subprocess.run(command, cwd=snapshot, env=env, stdout=stdout, stderr=stderr, timeout=900)
            metadata["exit_code"] = completed.returncode
        except subprocess.TimeoutExpired:
            metadata["exit_code"] = None
            metadata["failure"] = "scan timeout after 900 seconds"
    metadata.update(ended_utc=datetime.now(timezone.utc).isoformat(), elapsed_seconds=round(time.monotonic() - started, 3))
    (output / "scan-metadata.json").write_text(json.dumps(metadata, ensure_ascii=False, indent=2), encoding="utf-8")
    result_path = output / "semgrep-raw.json"
    if result_path.exists():
        raw = json.loads(result_path.read_text(encoding="utf-8"))
        summary = {"version": raw.get("version"), "findings": len(raw.get("results", [])),
                   "rules": dict(Counter(row["check_id"] for row in raw.get("results", []))),
                   "errors": len(raw.get("errors", [])), "scanned_files": len(raw.get("paths", {}).get("scanned", []))}
        (output / "scan-summary.json").write_text(json.dumps(summary, indent=2), encoding="utf-8")
        print(json.dumps(summary), flush=True)
    return metadata["exit_code"] if metadata["exit_code"] is not None else 1


if __name__ == "__main__":
    raise SystemExit(main())
