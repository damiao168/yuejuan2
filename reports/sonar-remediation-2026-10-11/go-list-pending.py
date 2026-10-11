"""只读列出当前智能体归属的待处理原问题。"""
import json
from pathlib import Path
import sys

sys.stdout.reconfigure(encoding="utf-8")
root = Path(__file__).resolve().parent
data = json.loads((root / "go-progress.json").read_text(encoding="utf-8"))
for issue in data["issue_states"]:
    path = issue["path"]
    if issue["state"] != "pending" or "/internal/server/" in path or "/internal/modelgovernance/" in path or "/tools/import-fujian-2024-math-simulation/" in path:
        continue
    if len(sys.argv) > 1 and sys.argv[1] not in path:
        continue
    print(issue["severity"], path, issue["original_line"], issue["key"])
