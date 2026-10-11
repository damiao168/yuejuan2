"""记录题库五文件的实际 Go AST 辅助检查；Sonar 关闭仍须平台复扫。"""
import datetime
import json
import os
from pathlib import Path
import subprocess
import sys

root = Path(__file__).resolve().parent
paths = [item["path"] for item in json.loads((root / "go-server-question-bank-progress.json").read_text(encoding="utf-8"))["files"]]
env = dict(os.environ)
env.update(GOTOOLCHAIN="local", GOPROXY="off", GOSUMDB="off", GOMAXPROCS="1")
fmt = subprocess.run(["gofmt", "-w", *paths], capture_output=True, text=True, encoding="utf-8", env=env)
assert fmt.returncode == 0, fmt.stderr
command = ["go", "run", str(root / "go-structure-check.go")]
run = subprocess.run(command, input=json.dumps(paths), capture_output=True, text=True, encoding="utf-8", env=env)
(root / "go-server-question-bank-structure.json").write_text(run.stdout, encoding="utf-8")
(root / "go-server-question-bank-structure-stderr.txt").write_text(run.stderr, encoding="utf-8")
(root / "go-server-question-bank-structure-execution.json").write_text(json.dumps(dict(command=command, actual_exit_code=run.returncode, paths=paths, updated_at=datetime.datetime.now().astimezone().isoformat()), ensure_ascii=False, indent=2), encoding="utf-8")
print("structure actual exit", run.returncode)
if run.returncode == 0:
    print(json.dumps([row for row in json.loads(run.stdout) if row["cognitive_estimate"] > 15 or row["parameters"] > 7], ensure_ascii=True))
sys.exit(run.returncode)
