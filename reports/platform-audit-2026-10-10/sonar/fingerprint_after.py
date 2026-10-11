"""记录扫描后工作区哈希；它不证明 Scanner 在每个文件读取时见到同一快照。"""
from __future__ import annotations
import csv
import datetime as dt
import hashlib
import json
from pathlib import Path

OUT = Path(__file__).resolve().parent
ROOT = OUT.parents[2]
with (OUT.parent / "source-manifest-resume-2026-10-11.csv").open(encoding="utf-8-sig", newline="") as stream:
    before = list(csv.DictReader(stream))
after = []
changed = []
missing = []
for entry in before:
    path = ROOT / entry["path"]
    if not path.is_file():
        missing.append(entry["path"])
        continue
    content = path.read_bytes()
    digest = hashlib.sha256(content).hexdigest()
    after.append({"path": entry["path"], "sha256": digest, "bytes": len(content), "text_lines": len(content.splitlines()), "manual_review_status": "pending"})
    if digest != entry["sha256"]:
        changed.append({"path": entry["path"], "manifest_sha256": entry["sha256"], "after_sha256": digest})
with (OUT / "source-manifest-after-scan.csv").open("w", encoding="utf-8-sig", newline="") as stream:
    writer = csv.DictWriter(stream, fieldnames=list(after[0]))
    writer.writeheader()
    writer.writerows(after)
result = {"recorded_at": dt.datetime.now().astimezone().isoformat(), "manifest": "../source-manifest-resume-2026-10-11.csv", "input_manifest_files": len(before), "recorded_files": len(after), "changed": changed, "missing": missing, "snapshot_frozen": False, "limitation": "Scanner read the live workspace during its execution interval; this after-hash cannot establish each file's exact read-time content or cross-tool snapshot identity. Other tasks' edits were not reverted."}
(OUT / "source-drift-after-scan.json").write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf-8")
print(json.dumps({"changed": len(changed), "missing": len(missing), "recorded_at": result["recorded_at"]}, ensure_ascii=False))
