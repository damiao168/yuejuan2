"""限时重扫报告涉及的固定副本路径，不代表全仓复扫。"""

from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import subprocess
import time


OUTPUT = Path(__file__).resolve().parent
scope = json.loads((OUTPUT / "scope.json").read_text(encoding="utf-8"))
snapshot = Path(scope["snapshot"])
targets = scope["paths"]
if len(targets) != 46 or any(not (snapshot / target).is_file() for target in targets):
    raise RuntimeError("报告定向范围必须是已存在的 46 个固定副本文件")
executable = Path("C:/Users/pc/AppData/Local/Temp/yuejuan-semgrep-remediation-venv/Scripts/semgrep.exe")
rules = OUTPUT.parent / "rescan/merged-official-rules.json"
command = [str(executable), "scan", "--config", str(rules), "--oss-only", "--metrics", "off",
           "--disable-version-check", "--no-git-ignore", "--disable-nosem", "--jobs", "1",
           "--max-memory", "600", "--timeout", "20", "--timeout-threshold", "3", "--json",
           "--output", str(OUTPUT / "semgrep-raw.json"), *targets]
environment = os.environ.copy()
environment.update({"PYTHONUTF8": "1", "PYTHONIOENCODING": "utf-8", "SEMGREP_APP_TOKEN": "",
                    "SEMGREP_SEND_METRICS": "off", "SEMGREP_ENABLE_VERSION_CHECK": "0"})
started = datetime.now(timezone.utc).isoformat()
clock = time.monotonic()
metadata = {"scan_kind": "report_scope_fixed_snapshot", "scope_count": len(targets),
            "snapshot": str(snapshot), "command": command, "cwd": str(snapshot), "started_utc": started,
            "configured_timeout_seconds": 240, "rule_sha256": hashlib.sha256(rules.read_bytes()).hexdigest(),
            "rule_count": len(json.loads(rules.read_text(encoding="utf-8"))["rules"]),
            "scope_sha256": hashlib.sha256((OUTPUT / "scope.json").read_bytes()).hexdigest(),
            "engine": "OSS", "metrics": "off", "app_token": "empty", "full_repository_rescan": False,
            "network_isolation": False, "cloud_upload": "not_requested"}
with (OUTPUT / "scan.stdout.log").open("wb") as stdout, (OUTPUT / "scan.stderr.log").open("wb") as stderr:
    process = subprocess.Popen(command, cwd=snapshot, env=environment, stdout=stdout, stderr=stderr,
                               creationflags=subprocess.CREATE_NO_WINDOW)
    metadata["owned_pid"] = process.pid
    (OUTPUT / "scan-metadata.json").write_text(json.dumps(metadata, ensure_ascii=False, indent=2), encoding="utf-8")
    print(f"Started own scoped Semgrep process PID {process.pid}, 46 files, timeout 240 s", flush=True)
    try:
        metadata["exit_code"] = process.wait(timeout=240)
        metadata["timed_out"] = False
    except subprocess.TimeoutExpired:
        # 只取消本脚本启动的扫描树；其他并行扫描和 Docker 进程不在该 PID 树内。
        killed = subprocess.run(["taskkill", "/PID", str(process.pid), "/T", "/F"], capture_output=True,
                                timeout=20, creationflags=subprocess.CREATE_NO_WINDOW)
        metadata["timed_out"] = True
        metadata["termination_exit_code"] = killed.returncode
        metadata["exit_code"] = process.wait(timeout=20)
metadata["finished_utc"] = datetime.now(timezone.utc).isoformat()
metadata["elapsed_seconds"] = round(time.monotonic() - clock, 3)
raw = OUTPUT / "semgrep-raw.json"
metadata["raw_result_present"] = raw.is_file()
if raw.is_file():
    metadata["raw_sha256"] = hashlib.sha256(raw.read_bytes()).hexdigest()
    result = json.loads(raw.read_text(encoding="utf-8"))
    metadata["finding_count"] = len(result.get("results", []))
    metadata["error_count"] = len(result.get("errors", []))
(OUTPUT / "scan-metadata.json").write_text(json.dumps(metadata, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
print(json.dumps({key: metadata.get(key) for key in
                  ("exit_code", "timed_out", "elapsed_seconds", "raw_result_present", "finding_count", "error_count")}), flush=True)
