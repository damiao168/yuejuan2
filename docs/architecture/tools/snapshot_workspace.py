"""只记录工作区文件指纹，不读取或输出凭据正文；验证文档交付未覆盖原有修改。"""
from pathlib import Path
import hashlib
import json
import subprocess
import sys
from datetime import datetime, timezone

ROOT = Path(__file__).resolve().parents[3]
OUT = ROOT / "docs/architecture/evidence"

def git(*args):
    return subprocess.check_output(["git", *args], cwd=ROOT)

def digest(path):
    h = hashlib.sha256()
    with path.open("rb") as f:
        for part in iter(lambda: f.read(1024 * 1024), b""):
            h.update(part)
    return h.hexdigest()

def snapshot():
    OUT.mkdir(parents=True, exist_ok=True)
    paths = sorted(set(git("ls-files", "--cached", "--others", "--exclude-standard", "-z").decode("utf-8").split("\0")) - {""})
    # 本任务新产物不属于原始工作区基线；既有架构说明仍纳入，不能借目录排除覆盖。
    paths = [p for p in paths if not p.startswith(("docs/architecture/tools/", "docs/architecture/specs/", "docs/architecture/evidence/", "docs/architecture/diagrams/", "docs/architecture/previews/"))]
    files = {p: digest(ROOT / p) if (ROOT / p).is_file() else None for p in paths}
    status = git("status", "--porcelain=v1", "-z").decode("utf-8")
    record = {"branch": git("branch", "--show-current").decode().strip(), "commit": git("rev-parse", "HEAD").decode().strip(), "client_date": "2026-10-10", "status_porcelain_z": status, "files": files}
    (OUT / "workspace-baseline.json").write_text(json.dumps(record, ensure_ascii=False, indent=2), encoding="utf-8")
    print(json.dumps({"baseline_files": len(files), "commit": record["commit"]}))

def verify():
    baseline = json.loads((OUT / "workspace-baseline.json").read_text(encoding="utf-8"))
    changed = []
    changed_hashes = []
    for p, expected in baseline["files"].items():
        actual = digest(ROOT / p) if (ROOT / p).is_file() else None
        if actual != expected:
            changed.append(p)
            changed_hashes.append({"file": p, "baseline_sha256": expected, "current_sha256": actual})
    report = {"checked_at_utc": datetime.now(timezone.utc).isoformat(), "baseline_files": len(baseline["files"]), "changed_original_files": changed, "changed_file_hashes": changed_hashes, "commit_unchanged": baseline["commit"] == git("rev-parse", "HEAD").decode().strip()}
    (OUT / "workspace-preservation.json").write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
    print(json.dumps({k:v for k,v in report.items() if k != 'changed_file_hashes'}, ensure_ascii=False))
    return 1 if changed or not report["commit_unchanged"] else 0

if __name__ == "__main__":
    sys.exit(verify() if "--verify" in sys.argv else snapshot())
