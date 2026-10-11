"""只比较固定清单的源码哈希；不把扫描后的修改算入平台历史结果。"""
import csv
from datetime import datetime, timedelta, timezone
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parent
WORKSPACE = ROOT.parent.parent
MANIFEST = ROOT / "source-manifest-resume-2026-10-11.csv"


def main():
    with MANIFEST.open(encoding="utf-8-sig", newline="") as handle:
        entries = list(csv.DictReader(handle))
    changed, missing, direct = [], [], []
    footer_paths = {
        "ai-services/grading_agent/paper_compact/anchors.py",
        "ai-services/grading_agent/paper_compact/schema.py",
    }
    for entry in entries:
        path = WORKSPACE / entry["path"]
        digest = hashlib.sha256(path.read_bytes()).hexdigest() if path.is_file() else None
        item = {"path": entry["path"], "snapshot_sha256": entry["sha256"], "current_sha256": digest}
        if digest is None:
            missing.append(item)
        elif digest != entry["sha256"]:
            changed.append(item)
        if entry["path"] in footer_paths:
            direct.append({**item, "matches_snapshot": digest == entry["sha256"]})
    result = {
        "checked_at": datetime.now(timezone(timedelta(hours=8))).isoformat(),
        "workspace_root": str(WORKSPACE),
        "manifest": MANIFEST.name,
        "manifest_sha256": hashlib.sha256(MANIFEST.read_bytes()).hexdigest(),
        "manifest_files": len(entries),
        "changed_count": len(changed),
        "missing_count": len(missing),
        "unchanged_count": len(entries) - len(changed) - len(missing),
        "changed": changed,
        "missing": missing,
        "direct_footer_files": direct,
        "new_files_enumerated": False,
        "scope_note": "仅比较固定清单，不枚举新增文件，不重跑平台扫描；两份页脚源码哈希也不能证明全部调用上下文未变。",
    }
    (ROOT / "delivery-workspace-drift-final.json").write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf-8")
    print(json.dumps({"checked_at": result["checked_at"], "files": len(entries), "changed": len(changed), "missing": len(missing), "direct_footer_files_match": len(direct) == 2 and all(row["matches_snapshot"] for row in direct)}))


if __name__ == "__main__":
    main()
