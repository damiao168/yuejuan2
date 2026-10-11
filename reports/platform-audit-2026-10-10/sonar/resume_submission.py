"""恢复已完成的官方 Scanner 目录，只向本机同版本 SonarQube 提交。"""
from __future__ import annotations

import base64
import datetime as dt
import hashlib
import importlib.util
import json
from pathlib import Path
import secrets
import time
import urllib.request
import zipfile


OUT = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("local_sonar", OUT / "run_local_sonar.py")
scan = importlib.util.module_from_spec(spec)
spec.loader.exec_module(scan)
REPORT = scan.RUNTIME / "scanner-work/scanner-report"
ARCHIVE = scan.RUNTIME / "scanner-report-recovered-20261011.zip"


def now():
    return dt.datetime.now().astimezone().isoformat()


def main():
    credentials = json.loads(Path("D:/tools/sonarqube-yuejuan/private/credentials.json").read_text(encoding="utf-8-sig"))
    auth = credentials["login"] + ":" + credentials["password"]
    receipt = OUT / "recovery-submission.json"
    if receipt.exists():
        # 网络响应已保存时仅继续等待和导出，不重复提交同一份分析。
        task_id = json.loads(receipt.read_text(encoding="utf-8"))["taskId"]
    else:
        state = scan.request("/api/ce/component", {"component": scan.PROJECT}, auth)
        if state.get("queue") or state.get("current"):
            raise RuntimeError("Project already has queued/current task; inspect before submitting")
        log = (OUT / "scanner.log").read_text(encoding="utf-8", errors="replace")
        if "Analysis report generated in 8808ms, dir size=45.7 MB" not in log:
            raise RuntimeError("Expected completed attempt3 report generation is absent")
        files = sorted(p for p in REPORT.rglob("*") if p.is_file())
        if len(files) != 12029 or sum(p.stat().st_size for p in files) != 45721127:
            raise RuntimeError("Scanner report directory changed since recovery inspection")
        if scan.PROJECT.encode() not in (REPORT / "metadata.pb").read_bytes():
            raise RuntimeError("Report metadata does not identify this dedicated audit project")
        entries = [{"path": p.relative_to(REPORT).as_posix(), "size": p.stat().st_size, "sha256": hashlib.sha256(p.read_bytes()).hexdigest()} for p in files]
        with zipfile.ZipFile(ARCHIVE, "w", compression=zipfile.ZIP_DEFLATED, compresslevel=1) as archive:
            for p in files:
                archive.write(p, p.relative_to(REPORT).as_posix())
        with zipfile.ZipFile(ARCHIVE) as archive:
            if archive.testzip() is not None or len(archive.infolist()) != len(entries):
                raise RuntimeError("Recovered archive integrity check failed")
            for entry in entries:
                content = archive.read(entry["path"])
                if len(content) != entry["size"] or hashlib.sha256(content).hexdigest() != entry["sha256"]:
                    raise RuntimeError("Recovered archive differs from generated directory")
        evidence = {"recovered_at": now(), "original_scanner_completed_standard_publish": False, "original_scanner_termination": "interrupted during compression; no fresh report-task.txt", "official_report_generated_at": "2026-10-11T11:53:06.087+08:00", "generated_report_files": len(entries), "generated_report_bytes": sum(e["size"] for e in entries), "archive_path": str(ARCHIVE), "archive_bytes": ARCHIVE.stat().st_size, "archive_sha256": hashlib.sha256(ARCHIVE.read_bytes()).hexdigest(), "crc_verified": True, "all_entries_sha256_verified": True, "files": entries}
        scan.save("recovery-integrity.json", evidence)
        token_name = "workspace-recovery-" + secrets.token_hex(6)
        token = scan.request("/api/user_tokens/generate", {"name": token_name, "type": "PROJECT_ANALYSIS_TOKEN", "projectKey": scan.PROJECT}, auth, True)["token"]
        try:
            boundary = "sonar-local-audit-" + secrets.token_hex(16)
            chunks = []
            for key, value in {"projectKey": scan.PROJECT, "projectName": "yuejuan current local workspace 2026-10-10"}.items():
                chunks.append(f'--{boundary}\r\nContent-Disposition: form-data; name="{key}"\r\n\r\n{value}\r\n'.encode())
            chunks.append(f'--{boundary}\r\nContent-Disposition: form-data; name="report"; filename="scanner-report.zip"\r\nContent-Type: application/zip\r\n\r\n'.encode())
            chunks.append(ARCHIVE.read_bytes())
            chunks.append(f"\r\n--{boundary}--\r\n".encode())
            headers = {"Content-Type": "multipart/form-data; boundary=" + boundary, "Authorization": "Basic " + base64.b64encode((token + ":").encode()).decode()}
            req = urllib.request.Request(scan.URL + "/api/ce/submit", data=b"".join(chunks), headers=headers, method="POST")
            with urllib.request.urlopen(req, timeout=120) as response:
                result = json.loads(response.read())
            scan.save("recovery-submission.json", {**result, "submitted_at": now(), "official_api": "POST /api/ce/submit", "local_url": scan.URL, "archive_sha256": evidence["archive_sha256"]})
            task_id = result["taskId"]
            print(json.dumps({"submitted": True, "taskId": task_id, "archive_bytes": evidence["archive_bytes"]}), flush=True)
        finally:
            scan.request("/api/user_tokens/revoke", {"name": token_name}, auth, True)
            scan.save("token-lifecycle.json", {"short_lived_scan_token_revoked": True, "recovery_project_analysis_token_revoked": True, "revoked_at": now(), "secret_value_persisted_in_reports": False})
    scan.save("execution-status.json", {"status": "submitted_recovered_report", "current_results_valid": False, "original_scanner_standard_publish_success": False, "task_id": task_id, "updated_at": now()})
    # 服务器完成校验前，不把已生成或已接收的报告当作有效平台结果。
    started = time.monotonic()
    state = "PENDING"
    while time.monotonic() - started < 1800:
        result = scan.request("/api/ce/task", {"id": task_id}, auth)
        scan.save("compute-engine-task.json", result)
        state = result["task"]["status"]
        if state in {"SUCCESS", "FAILED", "CANCELED"}:
            break
        time.sleep(5)
    if state != "SUCCESS":
        raise RuntimeError("Recovered report compute engine status: " + state)
    # 可见性变更被平台禁止与后台分析并发；成功结束后只调整本次独立项目。
    before = scan.request("/api/components/show", {"component": scan.PROJECT}, auth)["component"]
    try:
        if before.get("visibility") != "private":
            scan.request("/api/projects/update_visibility", {"project": scan.PROJECT, "visibility": "private"}, auth, True)
        after = scan.request("/api/components/show", {"component": scan.PROJECT}, auth)["component"]
        scan.save("project-visibility.json", {"project": scan.PROJECT, "visibility": after.get("visibility"), "previous_visibility": before.get("visibility"), "recorded_at": now(), "other_projects_changed": False})
    except Exception as error:
        scan.save("project-visibility-final-error.json", {"error_type": type(error).__name__, "error": str(error), "recorded_at": now(), "other_projects_changed": False})
    scan.export(auth)
    scan.save("scanner-exit.json", {"exit_code": None, "status": "interrupted_during_compression", "standard_scanner_publish_success": False, "prior_exit_code_was_stale": True})
    scan.save("execution-status.json", {"status": "complete_via_official_local_report_recovery", "completed_at": now(), "current_results_valid": True, "compute_engine_status": state, "original_scanner_standard_publish_success": False, "submission_method": "completed official Scanner report directory zipped and submitted through official local /api/ce/submit"})
    print("Recovered report processed and all platform results exported", flush=True)


if __name__ == "__main__":
    main()
