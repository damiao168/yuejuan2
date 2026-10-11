"""结算最后20原项和5新增项的真实动态证据，平台复扫保持待确认。"""
import hashlib
import importlib.util
import json
from pathlib import Path

root = Path(__file__).resolve().parent
execution = "go-batch22-26-and-new-five-tests.json"
evidence = json.loads((root / execution).read_text(encoding="utf-8"))
assert evidence["exit_code"] == 0
assert evidence["source_identity"]["changed_paths"] == []
spec = importlib.util.spec_from_file_location("progress", root / "go-record-progress.py")
progress = importlib.util.module_from_spec(spec)
spec.loader.exec_module(progress)
data = json.loads((root / "go-progress.json").read_text(encoding="utf-8"))
selected = [issue for issue in data["issue_states"] if issue["state"] == "changed_awaiting_tests"]
assert len(selected) == 20
paths = {issue["path"] for issue in selected}
for issue in selected:
    issue["state"] = "changed_awaiting_sonar"
for file in data["files"]:
    if file["path"] in paths:
        file["source_sha256"] = hashlib.sha256(Path(file["path"]).read_bytes()).hexdigest()
for batch in data["batches"]:
    if batch["id"] in {f"go-batch{number}" for number in range(22, 27)}:
        batch.update(test_state="passed", test_execution=execution)
progress.save(data)
new_path = root / "go-new-findings-progress.json"
new = json.loads(new_path.read_text(encoding="utf-8"))
for issue in new["issue_states"]:
    issue.update(state="changed_awaiting_sonar", test_execution=execution)
for file in new["files"]:
    file["source_sha256"] = hashlib.sha256(Path(file["path"]).read_bytes()).hexdigest()
new.update(dynamically_validated=5, test_execution=execution)
new_path.write_text(json.dumps(new, ensure_ascii=False, indent=2), encoding="utf-8")
print("new five dynamically validated; platform rescan pending")
