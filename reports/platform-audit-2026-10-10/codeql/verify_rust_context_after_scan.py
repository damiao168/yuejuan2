"""核对补充上下文副本的原清单和五项构建输入，保留生成文件的版本事实。"""

import datetime
import hashlib
import json
import pathlib

REPORT = pathlib.Path(__file__).parent


if __name__ == "__main__":
    verification = REPORT / "rust-context-source-verification.json"
    record = json.loads(verification.read_text(encoding="utf-8"))
    source = pathlib.Path(record["probe_root"])
    missing, changed, supplementary = [], [], []
    for item in record["files_before_build"]:
        path = source / item["path"]
        if not path.is_file():
            missing.append(item["path"])
        elif hashlib.sha256(path.read_bytes()).hexdigest() != item["sha256"]:
            changed.append(item["path"])
    for group in ("added_assets", "generated_build_context"):
        for item in record[group]:
            path = source / item["path"]
            actual = hashlib.sha256(path.read_bytes()).hexdigest() if path.is_file() else None
            supplementary.append({"path": item["path"], "expected_sha256": item["sha256"], "actual_sha256": actual, "unchanged": actual == item["sha256"]})
    now = datetime.datetime.now(datetime.timezone(datetime.timedelta(hours=8))).isoformat(timespec="seconds")
    # 发现变化时只记录并退出失败，不把新字节静默当作此前的固定输入。
    result = {"verified_at": now, "source_root": str(source), "original_manifest_entries_checked": len(record["files_before_build"]),
              "original_manifest_entries_changed": changed, "original_manifest_entries_missing": missing,
              "supplementary_context_checked": supplementary,
              "all_original_manifest_entries_unchanged": not changed and not missing,
              "all_supplementary_context_unchanged": all(item["unchanged"] for item in supplementary)}
    (REPORT / "rust-context-after-scan-verification.json").write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf-8")
    if not result["all_original_manifest_entries_unchanged"] or not result["all_supplementary_context_unchanged"]:
        print(json.dumps(result, ensure_ascii=False))
        raise SystemExit(1)
    record.update(final_context_verified_at=now, all_original_manifest_entries_unchanged=True,
                  all_supplementary_context_unchanged=True, original_manifest_entries_checked=len(record["files_before_build"]),
                  original_manifest_entries_changed=[], original_manifest_entries_missing=[])
    verification.write_text(json.dumps(record, ensure_ascii=False, indent=2), encoding="utf-8")
    print(json.dumps({"verified_at": now, "original_manifest_entries": len(record["files_before_build"]), "supplementary_context": len(supplementary), "all_unchanged": True}))
