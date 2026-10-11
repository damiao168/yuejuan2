"""固定另一个任务更新的两份 E2E 源码并进行增量复扫，不改旧证据。"""

from datetime import datetime, timezone
import difflib
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tempfile
import time


OUTPUT = Path(__file__).resolve().parent
ROOT = OUTPUT.parents[4]
inventory_before = json.loads((OUTPUT.parent / "rescan/source-inventory.json").read_text(encoding="utf-8"))
previous_snapshot = Path(inventory_before["snapshot"])
before_hashes = {item["path"]: item["sha256"] for item in inventory_before["source_files"]}
targets = ["services/api-gateway/internal/server/e2e_mfa_test.go",
           "services/api-gateway/internal/server/e2e_postgres_test.go"]
snapshot = Path(tempfile.mkdtemp(prefix="yuejuan-semgrep-latest-external-"))
items = []
for relative in targets:
    content = (ROOT / relative).read_bytes()
    target = snapshot / relative
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_bytes(content)
    before = (previous_snapshot / relative).read_bytes()
    if hashlib.sha256(before).hexdigest() != before_hashes[relative]:
        raise RuntimeError("此前固定扫描副本已漂移，不能作为增量审查基线")
    change = "".join(difflib.unified_diff(before.decode("utf-8").splitlines(keepends=True),
                                         content.decode("utf-8").splitlines(keepends=True),
                                         fromfile="previous-fixed-snapshot/" + relative,
                                         tofile="latest-external-change/" + relative))
    (OUTPUT / (Path(relative).name + ".diff")).write_text(change, encoding="utf-8")
    items.append({"path": relative, "sha256": hashlib.sha256(content).hexdigest(), "bytes": len(content),
                  "previous_snapshot_sha256": before_hashes[relative], "external_change": True,
                  "whole_file_review_status": "pending", "review_scope": "增量和原告警相关调用"})
inventory = {"source": str(ROOT), "snapshot": str(snapshot), "previous_snapshot": str(previous_snapshot),
             "fixed_at_utc": datetime.now(timezone.utc).isoformat(), "source_files": items, "files": items,
             "source_count": len(items), "origin": "外部任务在此前全仓/46文件扫描后更新的当前字节"}
(OUTPUT / "source-inventory.json").write_text(json.dumps(inventory, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
rules = OUTPUT.parent / "rescan/merged-official-rules.json"
executable = "C:/Users/pc/AppData/Local/Temp/yuejuan-semgrep-remediation-venv/Scripts/semgrep.exe"
command = [executable, "scan", "--config", str(rules), "--oss-only", "--metrics", "off",
           "--disable-version-check", "--no-git-ignore", "--disable-nosem", "--jobs", "1",
           "--max-memory", "600", "--timeout", "20", "--timeout-threshold", "3", "--json",
           "--output", str(OUTPUT / "semgrep-raw.json"), *targets]
environment = os.environ.copy()
environment.update({"PYTHONUTF8": "1", "PYTHONIOENCODING": "utf-8", "SEMGREP_APP_TOKEN": "",
                    "SEMGREP_SEND_METRICS": "off", "SEMGREP_ENABLE_VERSION_CHECK": "0"})
start = time.monotonic()
metadata = {"scan_kind": "latest_external_change_incremental", "full_repository_rescan": False,
            "source_count": 2, "command": command, "cwd": str(snapshot),
            "started_utc": datetime.now(timezone.utc).isoformat(), "configured_timeout_seconds": 120,
            "engine": "OSS", "metrics": "off", "app_token": "empty", "network_isolation": False,
            "cloud_upload": "not_requested", "rule_count": len(json.loads(rules.read_text(encoding="utf-8"))["rules"]),
            "rule_sha256": hashlib.sha256(rules.read_bytes()).hexdigest()}
with (OUTPUT / "scan.stdout.log").open("wb") as stdout, (OUTPUT / "scan.stderr.log").open("wb") as stderr:
    process = subprocess.Popen(command, cwd=snapshot, env=environment, stdout=stdout, stderr=stderr,
                               creationflags=subprocess.CREATE_NO_WINDOW)
    metadata["owned_pid"] = process.pid
    print("Started own latest external-change scan PID " + str(process.pid), flush=True)
    try:
        metadata["exit_code"] = process.wait(timeout=120)
        metadata["timed_out"] = False
    except subprocess.TimeoutExpired:
        # 只停止当前脚本启动的 PID 树，不触碰其他任务扫描。
        stopped = subprocess.run(["taskkill", "/PID", str(process.pid), "/T", "/F"], capture_output=True,
                                  timeout=20, creationflags=subprocess.CREATE_NO_WINDOW)
        metadata["termination_exit_code"] = stopped.returncode
        metadata["exit_code"] = process.wait(timeout=20)
        metadata["timed_out"] = True
metadata["finished_utc"] = datetime.now(timezone.utc).isoformat()
metadata["elapsed_seconds"] = round(time.monotonic() - start, 3)
raw_path = OUTPUT / "semgrep-raw.json"
metadata["raw_result_present"] = raw_path.is_file()
if raw_path.is_file():
    raw = json.loads(raw_path.read_text(encoding="utf-8"))
    scanned = {item.replace("\\", "/") for item in raw["paths"]["scanned"]}
    metadata["paths_scanned"] = sorted(scanned)
    metadata["scope_complete"] = scanned == set(targets)
    metadata["scope_paths_scanned_verified"] = metadata["scope_complete"]
    metadata["version"] = raw["version"]
    metadata["findings"] = len(raw.get("results", []))
    metadata["finding_count"] = metadata["findings"]
    metadata["diagnostics"] = len(raw.get("errors", []))
    metadata["error_count"] = metadata["diagnostics"]
    metadata["raw_sha256"] = hashlib.sha256(raw_path.read_bytes()).hexdigest()
(OUTPUT / "scan-metadata.json").write_text(json.dumps(metadata, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
print(json.dumps({key: metadata.get(key) for key in
                  ("exit_code", "timed_out", "elapsed_seconds", "scope_complete", "findings", "diagnostics")}), flush=True)
