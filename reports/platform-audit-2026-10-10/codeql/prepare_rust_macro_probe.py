"""复制固定清单到独立诊断目录，避免构建脚本写回原扫描快照。"""

import csv
import argparse
import datetime
import hashlib
import json
import pathlib
import shutil

REPORT = pathlib.Path(__file__).parent
SOURCE = pathlib.Path("D:/tools/audit-codeql-20261010/source-20261011")


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--with-icon", action="store_true")
    args = parser.parse_args()
    destination_name = "source-rust-context-20261011" if args.with_icon else "source-rust-macro-probe-20261011"
    destination = pathlib.Path("D:/tools/audit-codeql-20261010") / destination_name
    # 独立目录不覆盖旧证据；只复制原清单文件，不添加猜测的图标或构建产物。
    destination.mkdir(exist_ok=False)
    manifests = [REPORT.parent / "source-manifest-resume-2026-10-11.csv", REPORT / "build-context-manifest.csv"]
    entries = []
    for manifest in manifests:
        with manifest.open(encoding="utf-8-sig", newline="") as stream:
            for row in csv.DictReader(stream):
                relative = pathlib.PurePosixPath(row["path"])
                if relative.is_absolute() or ".." in relative.parts:
                    raise ValueError("清单路径越过复制边界")
                original, target = SOURCE / relative, destination / relative
                data = original.read_bytes()
                if hashlib.sha256(data).hexdigest() != row["sha256"]:
                    raise ValueError(f"原固定快照与清单不匹配: {relative}")
                target.parent.mkdir(parents=True, exist_ok=True)
                shutil.copy2(original, target)
                copied = hashlib.sha256(target.read_bytes()).hexdigest()
                if copied != row["sha256"]:
                    raise ValueError(f"诊断副本字节不匹配: {relative}")
                entries.append({"path": str(relative), "bytes": len(data), "sha256": copied, "manifest": manifest.name})
    assets = []
    if args.with_icon:
        # 前次普通 Cargo 已明确此真实本地图标是 Windows 构建输入；不改配置或造资产。
        relative = pathlib.PurePosixPath("apps/desktop-client/src-tauri/icons/icon.ico")
        original = pathlib.Path("D:/project/yuejuan") / relative
        stat = original.stat()
        data = original.read_bytes()
        digest = hashlib.sha256(data).hexdigest()
        target = destination / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copy2(original, target)
        if hashlib.sha256(target.read_bytes()).hexdigest() != digest or original.stat().st_mtime_ns != stat.st_mtime_ns:
            raise ValueError("图标在复制期间变化或校验失败")
        assets.append({"path": str(relative), "origin": str(original), "bytes": len(data), "sha256": digest,
                       "origin_mtime_ns": stat.st_mtime_ns, "proof": "rust-macro-probe-attempt2.stderr.log: icons/icon.ico not found; required for generating a Windows Resource file during tauri-build"})
    metadata = {
        "created_at": datetime.datetime.now(datetime.timezone(datetime.timedelta(hours=8))).isoformat(timespec="seconds"),
        "source_root": str(SOURCE), "probe_root": str(destination),
        "files": len(entries), "all_original_manifest_entries_match": True,
        "added_assets": assets, "files_before_build": entries,
        "purpose": "普通 Cargo 宏诊断可能运行 build.rs 并写生成输入，故在独立副本执行；此副本不是原始 CodeQL 分析数据库。",
    }
    output_name = "rust-context-source-verification.json" if args.with_icon else "rust-macro-probe-source-verification.json"
    (REPORT / output_name).write_text(json.dumps(metadata, ensure_ascii=False, indent=2), encoding="utf-8")
    print(json.dumps({key: metadata[key] for key in ("probe_root", "files", "all_original_manifest_entries_match", "added_assets")}))
