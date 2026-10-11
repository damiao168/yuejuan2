"""仅与 localhost SonarQube 交换审计数据；缓存与结果写入本次审计目录。"""
from __future__ import annotations

import base64
import csv
import datetime as dt
import json
import os
from pathlib import Path
import secrets
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request

ROOT = Path(__file__).resolve().parents[3]
OUT = Path(__file__).resolve().parent
URL = "http://127.0.0.1:19000"
PROJECT = "yuejuan-local-workspace-20261010"
CONTAINER = "yuejuan-audit-sonar-20261010"
NETWORK = "yuejuan-audit-sonar-net-20261010"
RUNTIME = Path("D:/tools/sonarqube-yuejuan/20261010")
RUNTIME.mkdir(parents=True, exist_ok=True)


def save(name: str, value: object) -> None:
    (OUT / name).write_text(json.dumps(value, ensure_ascii=False, indent=2), encoding="utf-8")


def request(path: str, values: dict | None = None, auth: str | None = None, post: bool = False) -> dict:
    data = urllib.parse.urlencode(values or {}).encode()
    target = URL + path
    if not post and data:
        target += "?" + data.decode()
    headers = {"Authorization": "Basic " + base64.b64encode(auth.encode()).decode()} if auth else {}
    req = urllib.request.Request(target, data=data if post else None, headers=headers)
    with urllib.request.urlopen(req, timeout=60) as response:
        body = response.read()
    return json.loads(body) if body else {}


def main() -> None:
    # 维护清单来自当前本地 Git 索引和未跟踪文件，不把被忽略的第三方工具当源码。
    manifest = OUT.parent / "source-manifest-resume-2026-10-11.csv"
    with manifest.open(encoding="utf-8-sig", newline="") as handle:
        records = list(csv.DictReader(handle))
    maintained = [record["path"] for record in records]
    tests = [path for path in maintained if path.endswith(("_test.go", ".test.ts", ".test.tsx", ".spec.ts", ".spec.tsx", ".test.mjs", ".test.cjs")) or "/tests/" in "/" + path or Path(path).name.startswith("test_") and path.endswith(".py")]
    base = (OUT / "local-scan.properties").read_text(encoding="utf-8")
    lines = [line for line in base.splitlines() if not line.startswith(("sonar.test.inclusions=", "sonar.inclusions="))]
    effective = OUT / "effective-scan.properties"
    effective.write_text("\n".join(lines) + "\nsonar.inclusions=" + ",".join(maintained) + "\nsonar.test.inclusions=" + ",".join(tests) + "\n", encoding="utf-8")
    save("source-scope.json", {"manifest": manifest.name, "manifest_files": len(records), "maintained_paths": maintained, "test_paths": tests, "physical_lines_in_manifest": sum(int(record["text_lines"]) for record in records), "manual_review": "pending unless separately reviewed; tool enumeration is not manual review"})
    start = time.monotonic()
    save("execution-status.json", {"status": "running", "started_at": dt.datetime.now().astimezone().isoformat(), "current_results_valid": False})
    while time.monotonic() - start < 360:
        try:
            status = request("/api/system/status")
            if status.get("status") == "UP":
                save("server-status.json", status)
                break
        except (urllib.error.URLError, TimeoutError, ConnectionError):
            pass
        time.sleep(5)
    else:
        raise RuntimeError("Local SonarQube did not become UP in 360 seconds")

    # 复用已有 localhost 服务的合法本地凭据，只创建本次独立项目和短期 token。
    credentials = Path("D:/tools/sonarqube-yuejuan/private/credentials.json")
    local_auth = json.loads(credentials.read_text(encoding="utf-8-sig"))
    auth = local_auth["login"] + ":" + local_auth["password"]
    existing = request("/api/projects/search", {"projects": PROJECT}, auth)
    if not existing.get("components"):
        request("/api/projects/create", {"project": PROJECT, "name": "yuejuan current local workspace 2026-10-10", "visibility": "private"}, auth, True)
    token_name = "workspace-scan-" + secrets.token_hex(6)
    token = request("/api/user_tokens/generate", {"name": token_name, "type": "USER_TOKEN"}, auth, True)["token"]
    try:
        env = dict(os.environ)
        env["SONAR_TOKEN"] = token
        env["SONAR_HOST_URL"] = URL
        env["JAVA_HOME"] = "D:/xic/tps/win64/jre21.0.1_12"
        env["SONAR_SCANNER_OPTS"] = "-Xmx48m -Xms16m -XX:+UseSerialGC -XX:TieredStopAtLevel=1 -XX:ReservedCodeCacheSize=24m -XX:ActiveProcessorCount=2"
        env["SONAR_SCANNER_JAVA_OPTS"] = "-Xmx1536m -Xms64m -Xss512k -XX:+UseSerialGC -XX:TieredStopAtLevel=1 -XX:ReservedCodeCacheSize=48m -XX:ActiveProcessorCount=2"
        env["SONAR_USER_HOME"] = str(RUNTIME / "scanner-cache")
        command = [
            "cmd.exe", "/d", "/c", str(RUNTIME / "sonar-scanner-8.1.0.6389/bin/sonar-scanner.bat"),
            "-Dproject.settings=" + str(effective),
            "-Dsonar.projectBaseDir=" + str(ROOT),
            "-Dsonar.working.directory=" + str(RUNTIME / "scanner-work"),
            "-Dsonar.nodejs.executable=D:/node.js/node.exe",
            "-Dsonar.python.analysis.parallel=false",
            "-Dsonar.javascript.node.maxspace=2048",
            "-Dsonar.typescript.tsconfigPaths=apps/desktop-client/tsconfig.json,apps/student-portal/tsconfig.json,apps/web-admin/tsconfig.json,packages/sdk/tsconfig.json",
            "-Dsonar.scanner.metadataFilePath=" + str(OUT / "report-task.txt"),
        ]
        save("scanner-command.json", {"command": command, "secret_delivery": "Short-lived SONAR_TOKEN inherited from process environment, value omitted and revoked at completion", "network": "Scanner exclusively targets 127.0.0.1; JRE provisioning disabled; existing service global settings unchanged", "source_mode": "native read; all caches/work/output redirected outside business files", "scan_started_at": dt.datetime.now().astimezone().isoformat(), "server_mode": "existing localhost SonarQube with unique new audit project; existing projects unchanged"})
        (OUT / "report-task.txt").unlink(missing_ok=True)
        with (OUT / "scanner.log").open("w", encoding="utf-8") as log:
            process = subprocess.Popen(command, cwd=ROOT, env=env, stdout=log, stderr=subprocess.STDOUT)
            code = process.wait()
        save("scanner-exit.json", {"exit_code": code})
        if code:
            raise RuntimeError(f"Scanner exited with {code}; inspect scanner.log")
        if not (OUT / "report-task.txt").exists():
            raise RuntimeError("Scanner returned without a fresh task receipt; analysis is incomplete")
        task_props = dict(line.split("=", 1) for line in (OUT / "report-task.txt").read_text().splitlines() if "=" in line)
        task_id = task_props["ceTaskId"]
        started = time.monotonic()
        while time.monotonic() - started < 900:
            task = request("/api/ce/task", {"id": task_id}, auth)
            save("compute-engine-task.json", task)
            state = task["task"]["status"]
            if state in {"SUCCESS", "FAILED", "CANCELED"}:
                break
            time.sleep(5)
        if state != "SUCCESS":
            raise RuntimeError(f"Compute engine ended with {state}")
        export(auth)
        save("execution-status.json", {"status": "complete", "completed_at": dt.datetime.now().astimezone().isoformat(), "current_results_valid": True, "compute_engine_status": state})
    finally:
        request("/api/user_tokens/revoke", {"name": token_name}, auth, True)
        save("token-lifecycle.json", {"short_lived_scan_token_revoked": True, "revoked_at": dt.datetime.now().astimezone().isoformat(), "secret_value_persisted_in_reports": False})


def export(auth: str) -> None:
    save("plugins.json", request("/api/plugins/installed", auth=auth))
    save("languages.json", request("/api/languages/list", auth=auth))
    save("quality-profiles.json", request("/api/qualityprofiles/search", {"project": PROJECT}, auth))
    save("quality-gate.json", request("/api/qualitygates/project_status", {"projectKey": PROJECT}, auth))
    metric_keys = "ncloc,files,lines,complexity,cognitive_complexity,functions,classes,bugs,vulnerabilities,code_smells,security_hotspots,duplicated_lines_density,duplicated_lines,duplicated_blocks,duplicated_files,coverage,uncovered_lines,lines_to_cover,sqale_index,sqale_rating,reliability_rating,security_rating,security_review_rating,security_hotspots_reviewed,alert_status,ncloc_language_distribution"
    metrics = request("/api/measures/component", {"component": PROJECT, "metricKeys": metric_keys}, auth)
    save("metrics.json", metrics)
    issues = []
    pages = []
    initial = request("/api/issues/search", {"componentKeys": PROJECT, "ps": 1, "resolved": "false"}, auth)
    total = initial["paging"]["total"]
    partitions = [("all", {})]
    if total > 10000:
        # Sonar 单次查询最多翻到 10,000 条，按互斥严重级别和类型分组导出。
        partitions = [(severity + "-" + kind, {"severities": severity, "types": kind}) for severity in ("BLOCKER", "CRITICAL", "MAJOR", "MINOR", "INFO") for kind in ("BUG", "VULNERABILITY", "CODE_SMELL")]
    for label, filters in partitions:
        page = 1
        exported = 0
        while True:
            result = request("/api/issues/search", {"componentKeys": PROJECT, "p": page, "ps": 500, "resolved": "false", "additionalFields": "_all", **filters}, auth)
            save(f"issues-{label}-page-{page:03}.json", result)
            issues.extend(result["issues"])
            exported += len(result["issues"])
            pages.append({"partition": label, **result["paging"]})
            if exported >= result["paging"]["total"]:
                break
            if page * 500 >= 10000:
                raise RuntimeError("An issue partition exceeded 10,000 results; further partitioning required")
            page += 1
    unique = {issue["key"]: issue for issue in issues}
    issues = list(unique.values())
    if len(issues) != total:
        raise RuntimeError(f"Incomplete issue export: {len(issues)} of {total}")
    save("issues.json", {"total": total, "exported": len(issues), "issues": issues, "paging": pages})
    hotspots = []
    page = 1
    while True:
        result = request("/api/hotspots/search", {"projectKey": PROJECT, "p": page, "ps": 500}, auth)
        save(f"hotspots-page-{page:03}.json", result)
        hotspots.extend(result.get("hotspots", []))
        if len(hotspots) >= result["paging"]["total"]:
            break
        page += 1
    details = []
    for hotspot in hotspots:
        details.append(request("/api/hotspots/show", {"hotspot": hotspot["key"]}, auth))
    save("hotspots.json", {"total": len(hotspots), "hotspots": hotspots, "details": details})
    files = []
    page = 1
    while True:
        result = request("/api/measures/component_tree", {"component": PROJECT, "metricKeys": "ncloc,complexity,cognitive_complexity,duplicated_lines_density,bugs,vulnerabilities,code_smells", "qualifiers": "FIL", "p": page, "ps": 500}, auth)
        save(f"file-metrics-page-{page:03}.json", result)
        files.extend(result.get("components", []))
        if len(files) >= result["paging"]["total"]:
            break
        page += 1
    save("file-metrics.json", {"total": len(files), "files": files})
    keys = sorted({issue["rule"] for issue in issues} | {hotspot.get("ruleKey", "") for hotspot in hotspots})
    rules = {}
    for key in keys:
        if key:
            rules[key] = request("/api/rules/show", {"key": key}, auth)
    save("rules.json", rules)
    with (OUT / "issues.csv").open("w", encoding="utf-8-sig", newline="") as handle:
        writer = csv.writer(handle)
        writer.writerow(["key", "severity", "type", "rule", "file", "line", "message", "effort", "status"])
        for issue in issues:
            writer.writerow([issue.get("key"), issue.get("severity"), issue.get("type"), issue.get("rule"), issue.get("component", "").removeprefix(PROJECT + ":"), issue.get("line"), issue.get("message"), issue.get("effort"), issue.get("status")])
    save("export-manifest.json", {"complete": True, "issues": len(issues), "hotspots": len(hotspots), "files": len(files), "rules": len(rules), "exported_at": dt.datetime.now().astimezone().isoformat(), "project": PROJECT, "local_url": URL + "/dashboard?id=" + PROJECT})
    print(json.dumps({"complete": True, "issues": len(issues), "hotspots": len(hotspots), "files": len(files)}, ensure_ascii=False), flush=True)


if __name__ == "__main__":
    main()
