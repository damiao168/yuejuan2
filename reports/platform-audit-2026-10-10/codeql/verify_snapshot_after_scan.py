import csv
import datetime
import hashlib
import json
from pathlib import Path


REPORT = Path(__file__).parent
SOURCE = Path("D:/tools/audit-codeql-20261010/source-20261011")
TZ = datetime.timezone(datetime.timedelta(hours=8))


def check_manifest(path):
    # 对照原清单检查实际扫描副本，区分源码和随后补入的构建配置。
    rows = list(csv.DictReader(path.open(encoding="utf-8-sig", newline="")))
    changed, missing = [], []
    for row in rows:
        actual = SOURCE / row["path"]
        if not actual.is_file():
            missing.append(row["path"])
            continue
        digest = hashlib.sha256(actual.read_bytes()).hexdigest()
        if digest != row["sha256"]:
            changed.append({"path": row["path"], "manifest_sha256": row["sha256"], "after_scan_sha256": digest})
    return {"manifest": str(path), "checked_files": len(rows), "changed": changed, "missing": missing}


if __name__ == "__main__":
    result = {
        "verified_at": datetime.datetime.now(TZ).isoformat(timespec="seconds"),
        "source_root": str(SOURCE),
        "maintenance_source": check_manifest(REPORT.parent / "source-manifest-resume-2026-10-11.csv"),
        "build_context": check_manifest(REPORT / "build-context-manifest.csv"),
    }
    result["all_manifest_files_unchanged"] = all(not part["changed"] and not part["missing"] for part in (result["maintenance_source"], result["build_context"]))
    (REPORT / "source-after-scan-verification.json").write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf-8")
    print(json.dumps(result, ensure_ascii=False))
