"""正式移交的题库5原项独立账本；接手时保留 frontend 的 qbRequest 参数结构与调用。"""
import datetime
import hashlib
import json
from pathlib import Path

root = Path(__file__).resolve().parent
keys = {
    "7cb650e1-5588-44da-a10b-b257e3c7552f",
    "5964a35d-7fae-423d-a1ed-a4a30ede5062",
    "401d2b22-9756-477e-bc8b-3e8ec7e98bd4",
    "d3266c6c-bdb5-4c2f-b29f-bb05a0814c28",
    "b4fa2740-1843-412d-9e61-3213f6e47909",
}
original = json.loads((root.parent / "platform-audit-2026-10-10/sonar/issues.json").read_text(encoding="utf-8"))
issues = [issue for issue in original["issues"] if issue["key"] in keys]
assert len(issues) == 5
data = dict(updated_at=datetime.datetime.now().astimezone().isoformat(), ownership="root正式移交server题库5原项；frontend其余server及既有S107处理不计入本账。", count=5, source_changed=0, dynamically_validated=0, platform_verified_resolved=0, issue_states=[], files=[])
snapshot = root / "go-server-question-bank-handoff-source"
snapshot.mkdir(exist_ok=True)
for issue in issues:
    path = issue["component"].split(":", 1)[1]
    raw = Path(path).read_bytes()
    (snapshot / Path(path).name).write_bytes(raw)
    data["issue_states"].append(dict(key=issue["key"], rule=issue["rule"], severity=issue["severity"], path=path, original_line=issue["line"], state="read_pending_refactor", sonar_rescan="pending"))
    data["files"].append(dict(path=path, review_state="read", handoff_source_sha256=hashlib.sha256(raw).hexdigest(), source_sha256=hashlib.sha256(raw).hexdigest(), maintenance_points="已完整审读原初始化/断言/并发/事务/范围及前端qbRequest改动，尚未改写。"))
(root / "go-server-question-bank-progress.json").write_text(json.dumps(data, ensure_ascii=False, indent=2), encoding="utf-8")
print("5 files read and handoff preserved")
