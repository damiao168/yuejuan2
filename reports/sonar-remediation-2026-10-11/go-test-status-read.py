"""只读正在写入的测试事件，不启动或堆积测试进程。"""
from pathlib import Path
import json
import sys

sys.stdout.reconfigure(encoding="utf-8")
path = Path(sys.argv[1])
lines = path.read_text(encoding="utf-8").splitlines()
print("event lines", len(lines))
for line in lines:
    try:
        row = json.loads(line)
    except json.JSONDecodeError:
        print(line)
        continue
    if row.get("Action") in {"pass", "fail", "skip"} and not row.get("Test"):
        print(json.dumps(row, ensure_ascii=False))
for line in lines[-4:]:
    print(line)
