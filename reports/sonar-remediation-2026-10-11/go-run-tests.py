"""保存每批实际 Go 测试事件、退出码和明确的环境跳过记录。"""
import datetime
import hashlib
import json
import os
from pathlib import Path
import subprocess
import sys
import time


root = Path(__file__).resolve().parent
batch, *packages = sys.argv[1:]
cwd = root.parent.parent / "services" / "api-gateway"
env = dict(os.environ)
cleared = []
for name in list(env):
    if name.startswith(("EDUGRADE_", "OPENAI_", "ANTHROPIC_", "SEMGRAP_", "SEMGREP_")) or name in {"DATABASE_URL", "PGHOST", "PGPORT", "PGUSER", "PGPASSWORD", "PGDATABASE"}:
        cleared.append(name)
        del env[name]
env.update(GOTOOLCHAIN="local", GOPROXY="off", GOSUMDB="off", GOMAXPROCS="1")
command = ["go", "test", "-json", "-p", "1", "-count=1", "-timeout=5m", *packages]
source_paths = set(path for path in cwd.rglob("*.go") if not path.name.endswith("_test.go"))
for package in packages:
    source_paths.update((cwd / package).glob("*.go"))
source_before = {str(path.relative_to(cwd)): hashlib.sha256(path.read_bytes()).hexdigest() for path in sorted(source_paths)}
(root / f"{batch}-source-before.json").write_text(json.dumps(source_before, ensure_ascii=False, indent=2), encoding="utf-8")
started = datetime.datetime.now().astimezone().isoformat()
tick = time.monotonic()
with (root / f"{batch}-test-events.jsonl").open("w", encoding="utf-8") as events:
    process = subprocess.Popen(command, cwd=cwd, env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, encoding="utf-8", errors="replace")
    for line in process.stdout:
        events.write(line)
        events.flush()
    exit_code = process.wait()
source_after = {str(path.relative_to(cwd)): hashlib.sha256(path.read_bytes()).hexdigest() for path in sorted(source_paths)}
(root / f"{batch}-source-after.json").write_text(json.dumps(source_after, ensure_ascii=False, indent=2), encoding="utf-8")
rows = []
for line in (root / f"{batch}-test-events.jsonl").read_text(encoding="utf-8").splitlines():
    try:
        rows.append(json.loads(line))
    except json.JSONDecodeError:
        pass
summary = {
    "started_at": started,
    "finished_at": datetime.datetime.now().astimezone().isoformat(),
    "duration_seconds": time.monotonic() - tick,
    "command": command,
    "cwd": str(cwd),
    "exit_code": exit_code,
    "isolation": {"toolchain": "local", "module_proxy": "off", "checksum_service": "off", "GOMAXPROCS": 1, "cleared_environment_names": sorted(cleared)},
    "source_identity": {"before": f"{batch}-source-before.json", "after": f"{batch}-source-after.json", "count": len(source_before), "changed_paths": [path for path, value in source_before.items() if source_after[path] != value]},
    "package_results": [row for row in rows if row.get("Action") in {"pass", "fail", "skip"} and not row.get("Test")],
    "test_result_counts": {action: sum(row.get("Action") == action and bool(row.get("Test")) for row in rows) for action in ("pass", "fail", "skip")},
    "skipped_tests": [row for row in rows if row.get("Action") == "skip" and row.get("Test")],
    "note": "只报告实际运行的包；环境缺失跳过项不算真实数据库或外部模型集成通过。",
}
(root / f"{batch}-tests.json").write_text(json.dumps(summary, ensure_ascii=False, indent=2), encoding="utf-8")
print(json.dumps({"batch": batch, "exit_code": exit_code, "test_result_counts": summary["test_result_counts"], "package_results": summary["package_results"]}, ensure_ascii=True))
sys.exit(exit_code)
