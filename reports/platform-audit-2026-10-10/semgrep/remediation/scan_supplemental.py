"""固定副本扫描复扫后的 XML 增量，保留此前全仓快照和扫描结果。"""

import hashlib
import json
import os
import shutil
import subprocess
import tempfile
import time
from datetime import datetime, timezone
from pathlib import Path

ROOT = Path(__file__).resolve().parent
REPO = ROOT.parents[3]
OUTPUT = ROOT / "rescan-supplemental"
TARGETS = ["docs/architecture/tools/build_preview.py", "docs/architecture/tools/verify_typography.py",
           "docs/architecture/tools/test_xml_supplemental_security.py",
           "docs/architecture/tools/requirements.txt", "docs/architecture/DIAGRAM_MAINTENANCE.md"]


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main():
    OUTPUT.mkdir(exist_ok=True)
    snapshot = Path(tempfile.mkdtemp(prefix="yuejuan-semgrep-xml-supplemental-"))
    files = []
    for target in TARGETS:
        source, destination = REPO / target, snapshot / target
        destination.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source, destination)
        files.append({"path": target, "sha256": digest(destination), "full_file_review": "pending"})
    (snapshot / ".semgrepignore").write_text("", encoding="utf-8")
    (OUTPUT / "source-inventory.json").write_text(json.dumps({"snapshot": str(snapshot), "files": files},
                                                             ensure_ascii=False, indent=2), encoding="utf-8")
    executable = Path("C:/Users/pc/AppData/Local/Temp/yuejuan-semgrep-remediation-venv/Scripts/semgrep.exe")
    rules = ROOT / "rescan/merged-official-rules.json"
    command = [str(executable), "scan", "--config", str(rules), "--oss-only", "--metrics", "off",
               "--disable-version-check", "--no-git-ignore", "--disable-nosem", "--jobs", "1",
               "--max-memory", "600", "--timeout", "20", "--timeout-threshold", "3", "--json",
               "--output", str(OUTPUT / "semgrep-raw.json"), *TARGETS]
    environment = os.environ.copy()
    environment.update(PYTHONUTF8="1", PYTHONIOENCODING="utf-8", SEMGREP_APP_TOKEN="",
                       SEMGREP_SEND_METRICS="off", SEMGREP_ENABLE_VERSION_CHECK="0")
    metadata = {"scan_kind": "supplemental_xml_fixed_snapshot", "scope_count": len(TARGETS),
                "snapshot": str(snapshot), "command": command, "cwd": str(snapshot),
                "started_utc": datetime.now(timezone.utc).isoformat(), "configured_timeout_seconds": 180,
                "rule_sha256": digest(rules), "rule_count": 735, "engine": "OSS", "metrics": "off",
                "app_token": "empty", "full_repository_rescan": False, "network_isolation": False,
                "cloud_upload": "not_requested"}
    clock = time.monotonic()
    with (OUTPUT / "scan.stdout.log").open("wb") as stdout, (OUTPUT / "scan.stderr.log").open("wb") as stderr:
        process = subprocess.Popen(command, cwd=snapshot, env=environment, stdout=stdout, stderr=stderr,
                                   creationflags=subprocess.CREATE_NO_WINDOW)
        metadata["owned_pid"] = process.pid
        print(f"XML supplement: {len(TARGETS)} files; own PID {process.pid}", flush=True)
        try:
            metadata.update(exit_code=process.wait(timeout=180), timed_out=False)
        except subprocess.TimeoutExpired:
            # 只停止本次扫描进程树，不能影响其它任务的扫描或容器。
            stopped = subprocess.run(["taskkill", "/PID", str(process.pid), "/T", "/F"],
                                     capture_output=True, check=False, timeout=20, creationflags=subprocess.CREATE_NO_WINDOW)
            metadata.update(timed_out=True, termination_exit_code=stopped.returncode,
                            exit_code=process.wait(timeout=20))
    metadata.update(finished_utc=datetime.now(timezone.utc).isoformat(), elapsed_seconds=round(time.monotonic() - clock, 3))
    raw = json.loads((OUTPUT / "semgrep-raw.json").read_text(encoding="utf-8"))
    scanned = {Path(path).as_posix() if not Path(path).is_absolute()
               else Path(path).relative_to(snapshot).as_posix() for path in raw["paths"]["scanned"]}
    metadata.update(finding_count=len(raw["results"]), error_count=len(raw["errors"]),
                    scope_paths_scanned_verified=set(TARGETS).issubset(scanned),
                    current_hashes_match_snapshot=all(digest(REPO / row["path"]) == row["sha256"] for row in files),
                    raw_sha256=digest(OUTPUT / "semgrep-raw.json"))
    (OUTPUT / "scan-metadata.json").write_text(json.dumps(metadata, ensure_ascii=False, indent=2), encoding="utf-8")
    assert metadata["exit_code"] == 0 and metadata["scope_paths_scanned_verified"]
    assert metadata["current_hashes_match_snapshot"]
    print(json.dumps({key: metadata[key] for key in ("exit_code", "finding_count", "error_count", "elapsed_seconds")}), flush=True)


if __name__ == "__main__":
    main()
