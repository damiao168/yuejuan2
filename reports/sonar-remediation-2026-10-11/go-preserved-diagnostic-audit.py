"""辅助比较已处理文件的原失败诊断文本计数；只提供静态保留证据，不能替代测试。"""
from collections import Counter
import datetime
import json
from pathlib import Path
import re

root = Path(__file__).resolve().parent
data = json.loads((root / "go-progress.json").read_text(encoding="utf-8"))
paths = {issue["path"] for issue in data["issue_states"] if issue["state"] == "changed_awaiting_tests"}
patterns = {
    "fatal_diagnostic_strings": r'\.(?:Fatalf|Fatal|Errorf|Error)\(\s*((?:"(?:\\.|[^"\\])*"|`[^`]*`))',
    "returned_error_strings": r'(?:errors\.New|fmt\.Errorf)\(\s*((?:"(?:\\.|[^"\\])*"|`[^`]*`))',
}
rows = []
for path in sorted(paths):
    baseline = Path("D:/tools/sonar-remediation-20261011/baseline") / path
    if not baseline.exists():
        rows.append(dict(path=path, state="baseline_missing"))
        continue
    before = baseline.read_text(encoding="utf-8-sig")
    after = Path(path).read_text(encoding="utf-8-sig")
    row = dict(path=path, checks={})
    for name, pattern in patterns.items():
        original = Counter(re.findall(pattern, before))
        current = Counter(re.findall(pattern, after))
        row["checks"][name] = dict(before=sum(original.values()), after=sum(current.values()), removed=dict(original - current), added=dict(current - original))
    rows.append(row)
summary = dict(updated_at=datetime.datetime.now().astimezone().isoformat(), files=rows, note="比对原失败诊断和错误消息，无移除不等于逻辑完全等价；动态测试与平台复扫另有证据。")
(root / "go-batch22-26-diagnostic-audit.json").write_text(json.dumps(summary, ensure_ascii=False, indent=2), encoding="utf-8")
print(json.dumps([row for row in rows if row.get("state") or any(check["removed"] or check["added"] for check in row["checks"].values())], ensure_ascii=True))
