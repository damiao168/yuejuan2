import csv
import hashlib
import pathlib
import shutil
import subprocess

WORKSPACE = pathlib.Path("D:/project/yuejuan")
SOURCE = pathlib.Path(__file__).parent / "source-20261011"
REPORT = WORKSPACE / "reports/platform-audit-2026-10-10/codeql"
NAMES = {"go.sum", "Cargo.lock", "package-lock.json", "pnpm-lock.yaml", "yarn.lock", "tauri.conf.json", "tauri.windows.conf.json", "rust-toolchain", "rust-toolchain.toml", "requirements.txt", ".npmrc", "biome.json"}
EXCLUDED = {"node_modules", "target", "dist", "output", "reports", ".downloads", ".runtime", ".models", ".venv", "__pycache__"}


def eligible(path):
    if EXCLUDED.intersection(path.parts):
        return False
    return path.name in NAMES or path.name.startswith("tsconfig") and path.suffix == ".json" or path.as_posix().startswith("apps/desktop-client/src-tauri/capabilities/") and path.suffix == ".json"


if __name__ == "__main__":
    raw = subprocess.check_output(["git", "ls-files", "-co", "--exclude-standard", "-z"], cwd=WORKSPACE)
    records = []
    for name in sorted({item.decode("utf-8") for item in raw.split(b"\0") if item}):
        relative = pathlib.Path(name)
        origin = WORKSPACE / relative
        if not origin.is_file() or not eligible(relative):
            continue
        # 上下文必须来自已授权的本地维护文件，不包含环境凭据或第三方缓存。
        data = origin.read_bytes()
        target = SOURCE / relative
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(origin, target)
        records.append({"path": name, "bytes": len(data), "sha256": hashlib.sha256(data).hexdigest(), "purpose": "build_context", "manual_review_status": "pending"})
    with (REPORT / "build-context-manifest.csv").open("w", newline="", encoding="utf-8-sig") as output:
        writer = csv.DictWriter(output, fieldnames=["path", "bytes", "sha256", "purpose", "manual_review_status"])
        writer.writeheader()
        writer.writerows(records)
    print({"additional_context_files": len(records), "bytes": sum(record["bytes"] for record in records)})
