"""按实际通过包及单包修复证据结算 43 原项，不把整轮 exit1 写成通过。"""
import hashlib
import importlib.util
import json
from pathlib import Path

root = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("progress", root / "go-record-progress.py")
progress = importlib.util.module_from_spec(spec)
spec.loader.exec_module(progress)
data = json.loads((root / "go-progress.json").read_text(encoding="utf-8"))
initial = json.loads((root / "go-batch13-21-tests.json").read_text(encoding="utf-8"))
retry = json.loads((root / "go-batch13-21-retry-taskmaterials-tests.json").read_text(encoding="utf-8"))
assert initial["exit_code"] == 1 and retry["exit_code"] == 0
assert initial["source_identity"]["changed_paths"] == []
assert retry["source_identity"]["changed_paths"] == []
passed = {item["Package"].removeprefix("edugrade-enterprise/services/api-gateway/") for summary in (initial, retry) for item in summary["package_results"] if item["Action"] == "pass"}
batches = {f"go-batch{number:02d}" for number in range(13, 22)}
selected = [issue for issue in data["issue_states"] if issue.get("batch") in batches]
assert len(selected) == 43
for issue in selected:
    package = str(Path(issue["path"]).parent).replace("\\", "/").removeprefix("services/api-gateway/")
    assert package in passed, (issue["key"], package)
    issue["state"] = "changed_awaiting_sonar"
    if package == "internal/taskmaterials":
        issue["change"] += "；Go overlay 原测试同样失败，严格补齐当前已存在的单份复核 SQL 范围与查询次序后单包通过，生产代码未修改。"
paths = {issue["path"] for issue in selected}
for file in data["files"]:
    if file["path"] in paths:
        file["source_sha256"] = hashlib.sha256(Path(file["path"]).read_bytes()).hexdigest()
for batch in data["batches"]:
    if batch["id"] in batches:
        batch.update(test_state="passed by actual package results; initial failure retained", test_executions=["go-batch13-21-tests.json", "go-batch13-21-retry-taskmaterials-tests.json"])
        batch["test_execution"] = None
progress.save(data)
