import csv
import datetime
import json
import re
import zipfile
from pathlib import Path


REPORT = Path(__file__).parent
TZ = datetime.timezone(datetime.timedelta(hours=8))


def load(path):
    return json.loads(path.read_text(encoding="utf-8-sig")) if path.is_file() else None


def collect(language, suffix, manifest):
    expected = {row["path"] for row in manifest if row["path"].endswith(suffix)}
    execution = load(REPORT / f"{language}-create-execution.json")
    analysis = load(REPORT / f"{language}-analyze-execution.json")
    found, outside, archives = set(), [], []
    if execution and execution["exit_code"] == 0:
        source_root = Path(next(arg.split("=", 1)[1] for arg in execution["command"] if arg.startswith("--source-root=")))
        archive = Path(execution["command"][3]) / "src.zip"
        if archive.is_file():
            with zipfile.ZipFile(archive) as source:
                archives = source.namelist()
            for name in archives:
                # 源码归档数量反映被提取文件，不代表每个分支或每个平台配置都已分析。
                if not name.endswith(suffix):
                    continue
                # 补充真实构建资产时使用独立副本；按实际命令中的根目录核对同一清单。
                relative = name.partition(f"/{source_root.name}/")[2]
                if relative:
                    found.add(relative)
                else:
                    outside.append(name)
    text = (REPORT / f"{language}-create.log").read_text(encoding="utf-8", errors="replace")
    problems = [line for line in text.splitlines() if re.search(r"\b(error|failed|failure|cannot|could not|unable|warning)\b", line, re.I)]
    result = {
        "database_create_exit_code": execution["exit_code"] if execution else None,
        "database_analyze_exit_code": analysis["exit_code"] if analysis else None,
        "manifest_language_files": len(expected),
        "archived_manifest_language_files": len(expected & found),
        "manifest_files_missing_from_archive": sorted(expected - found),
        "archived_language_files_outside_source_root": outside,
        "archived_language_files_inside_source_not_in_manifest": sorted(found - expected),
        "archived_total_files": len(archives),
        "create_log_diagnostic_candidates": problems,
        "limitations": "归档和诊断是抽取证据；不能推导完整跨平台、配置分支或业务语义正确性。依赖路径若出现保留并单列，不算维护源码。",
    }
    sarif = load(REPORT / f"{language}.sarif")
    if analysis and analysis["exit_code"] == 0 and sarif:
        result["sarif_execution_successful"] = [inv.get("executionSuccessful") for run in sarif["runs"] for inv in run.get("invocations", [])]
        result["coverage_diagnostics"] = [item["message"]["text"] for run in sarif["runs"] for inv in run.get("invocations", []) for item in inv.get("toolExecutionNotifications", []) if "CodeQL scanned" in item.get("message", {}).get("text", "")]
        result["analysis_coverage_log_lines"] = [line for line in (REPORT / f"{language}-analyze.log").read_text(encoding="utf-8", errors="replace").splitlines() if line.startswith("CodeQL scanned ")]
    return result


if __name__ == "__main__":
    manifest = list(csv.DictReader((REPORT.parent / "source-manifest-resume-2026-10-11.csv").open(encoding="utf-8-sig", newline="")))
    result = {"collected_at": datetime.datetime.now(TZ).isoformat(timespec="seconds"), "languages": {}}
    for language, suffix in (("go", ".go"), ("rust", ".rs")):
        if (REPORT / f"{language}-create.log").is_file():
            result["languages"][language] = collect(language, suffix, manifest)
    (REPORT / "extraction-coverage.json").write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf-8")
    print(json.dumps({"languages": {lang: {key: value for key, value in data.items() if key in ("database_create_exit_code", "database_analyze_exit_code", "manifest_language_files", "archived_manifest_language_files", "manifest_files_missing_from_archive", "archived_total_files")} for lang, data in result["languages"].items()}}, ensure_ascii=False))
