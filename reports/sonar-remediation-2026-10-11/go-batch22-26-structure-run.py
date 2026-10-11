"""检查最后20原项及5新告警的辅助结构估算，平台关闭仍待正式复扫。"""
from pathlib import Path
import os
import json
import subprocess
import sys

root = Path(__file__).resolve().parent
original = json.loads((root / "go-progress.json").read_text(encoding="utf-8"))
new = json.loads((root / "go-new-findings-progress.json").read_text(encoding="utf-8"))
paths = sorted({item["path"] for item in original["issue_states"] if item["state"] == "changed_awaiting_tests"} | {item["path"] for item in new["issue_states"]})
env = dict(os.environ)
env.update(GOTOOLCHAIN="local", GOPROXY="off", GOSUMDB="off", GOMAXPROCS="1")
command = ["go", "run", str(root / "go-structure-check.go")]
run = subprocess.run(command, input=json.dumps(paths), capture_output=True, text=True, encoding="utf-8", env=env)
(root / "go-batch22-26-structure.json").write_text(run.stdout, encoding="utf-8")
(root / "go-batch22-26-structure-stderr.txt").write_text(run.stderr, encoding="utf-8")
(root / "go-batch22-26-structure-execution.json").write_text(json.dumps(dict(command=command, exit_code=run.returncode, paths=paths), ensure_ascii=False, indent=2), encoding="utf-8")
print("structure actual exit", run.returncode)
if run.returncode == 0:
    rows = json.loads(run.stdout)
    print(json.dumps([row for row in rows if row["cognitive_estimate"] > 15 or row["parameters"] > 7], ensure_ascii=True))
sys.exit(run.returncode)
