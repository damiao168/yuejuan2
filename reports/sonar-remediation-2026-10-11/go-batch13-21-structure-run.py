"""辅助AST复杂度估算，真实规则确认仍由 SonarQube 完成。"""
from pathlib import Path
import os
import json
import subprocess
import sys

root = Path(__file__).resolve().parent
data = json.loads((root / "go-progress.json").read_text(encoding="utf-8"))
paths = sorted({item["path"] for item in data["issue_states"] if item["state"] == "changed_awaiting_tests"})
env = dict(os.environ)
env.update(GOTOOLCHAIN="local", GOPROXY="off", GOSUMDB="off", GOMAXPROCS="1")
run = subprocess.run(["go", "run", str(root / "go-structure-check.go")], input=json.dumps(paths), capture_output=True, text=True, encoding="utf-8", env=env)
(root / "go-batch13-21-structure.json").write_text(run.stdout, encoding="utf-8")
(root / "go-batch13-21-structure-stderr.txt").write_text(run.stderr, encoding="utf-8")
print("structure actual exit", run.returncode)
if run.returncode == 0:
    rows = json.loads(run.stdout)
    print(json.dumps([row for row in rows if row["cognitive_estimate"] > 15 or row["parameters"] > 7], ensure_ascii=True))
sys.exit(run.returncode)
