"""只读官方复扫差集，新发现与原告警账本分开。"""
import json
from pathlib import Path
import sys

sys.stdout.reconfigure(encoding="utf-8")
root = Path(__file__).resolve().parent
old = json.loads((root.parent / "platform-audit-2026-10-10/sonar/issues.json").read_text(encoding="utf-8"))
new = json.loads((root / "full-checkpoint-02/issues.json").read_text(encoding="utf-8"))
old_keys = {issue["key"] for issue in old["issues"]}
for issue in new["issues"]:
    if issue["key"] not in old_keys and issue["rule"].startswith("go:"):
        print(json.dumps({key: issue.get(key) for key in ("key", "rule", "severity", "component", "line", "message", "scope")}, ensure_ascii=False))
