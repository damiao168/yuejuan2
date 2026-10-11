import csv
import hashlib
import json
import pathlib
import urllib.parse

SOURCE = pathlib.Path(__file__).parent / "source-20261011"
WORKSPACE = pathlib.Path("D:/project/yuejuan")
REPORT = WORKSPACE / "reports/platform-audit-2026-10-10/codeql"


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest() if path.is_file() else ""


if __name__ == "__main__":
    records = []
    with (REPORT / "alerts.csv").open(encoding="utf-8-sig", newline="") as input_file:
        for record in csv.DictReader(input_file):
            relative = urllib.parse.unquote(record["path"]).replace("\\", "/")
            prefix = SOURCE.as_posix() + "/"
            if relative.startswith("file:///"):
                relative = relative[8:]
            if relative.lower().startswith(prefix.lower()):
                relative = relative[len(prefix):]
            local = pathlib.Path(relative)
            if local.is_absolute() or ".." in local.parts:
                record.update(snapshot_sha256="", current_sha256="", current_workspace_state="unresolved_path")
            else:
                snapshot = digest(SOURCE / local)
                current = digest(WORKSPACE / local)
                # 当前其他开发任务的修复不能追溯为扫描快照的误报。
                state = "same_as_scan" if snapshot and current == snapshot else "changed_after_snapshot" if current else "missing_current_file"
                record.update(snapshot_sha256=snapshot, current_sha256=current, current_workspace_state=state)
            records.append(record)
    output = REPORT / "alerts-with-current-drift.csv"
    if records:
        with output.open("w", encoding="utf-8-sig", newline="") as target:
            writer = csv.DictWriter(target, fieldnames=list(records[0]))
            writer.writeheader()
            writer.writerows(records)
    print(json.dumps({"alerts": len(records), "current_workspace_changes": sum(record["current_workspace_state"] == "changed_after_snapshot" for record in records)}, ensure_ascii=False))
