"""在 gofmt 后同步真实源码哈希，只更新本智能体实际审读和修改的文件。"""
import datetime
import hashlib
import importlib.util
import json
from pathlib import Path

root = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("progress", root / "go-record-progress.py")
progress = importlib.util.module_from_spec(spec)
spec.loader.exec_module(progress)
data = json.loads((root / "go-progress.json").read_text(encoding="utf-8"))
for file in data["files"]:
    if file["review_state"] == "read_and_refactored":
        file["source_sha256"] = hashlib.sha256(Path(file["path"]).read_bytes()).hexdigest()
progress.save(data)
new_path = root / "go-new-findings-progress.json"
new = json.loads(new_path.read_text(encoding="utf-8"))
for file in new["files"]:
    file["source_sha256"] = hashlib.sha256(Path(file["path"]).read_bytes()).hexdigest()
new["updated_at"] = datetime.datetime.now().astimezone().isoformat()
new_path.write_text(json.dumps(new, ensure_ascii=False, indent=2), encoding="utf-8")
print("new source SHA refreshed", len(new["files"]))
