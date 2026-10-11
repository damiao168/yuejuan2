"""核对交付中的原始计数与引擎状态，不把未执行的云平台写成通过。"""
from collections import Counter
import argparse
from datetime import datetime, timedelta, timezone
import csv
import hashlib
import json
from pathlib import Path
import re

ROOT = Path(__file__).resolve().parent


def read(path):
    return json.loads((ROOT / path).read_text(encoding="utf-8-sig"))


def main(preflight=False):
    checks = []
    with (ROOT / "source-manifest-resume-2026-10-11.csv").open(encoding="utf-8-sig", newline="") as f:
        source_hashes = {row["path"]: row["sha256"] for row in csv.DictReader(f)}

    def check(name, passed, detail):
        checks.append({"check": name, "passed": bool(passed), "detail": detail})

    raw = read("semgrep/semgrep-raw.json")
    triage = read("semgrep/findings-triage.json")
    summary = read("semgrep/scan-summary.json")
    check("semgrep_execution", read("semgrep/scan-metadata.json")["exit_code"] == 0, "Official engine exit status")
    check("semgrep_count", len(raw["results"]) == len(triage) == summary["findings"], len(triage))
    check("semgrep_verdict_count", dict(Counter(x["human_status"] for x in triage)) == summary["human_status"], summary["human_status"])
    check("semgrep_parse_limits", len(raw.get("errors", [])) == summary["errors"], summary["errors"])

    codeql = read("codeql/summary.json")
    expected_queries = {"python": 174, "javascript": 203, "go": 59, "rust": 41, "actions": 28}
    check("codeql_language_set", set(codeql["languages"]) == set(expected_queries), sorted(codeql["languages"]))
    check("codeql_planned_languages_completed", codeql["completed_language_count"] == codeql["planned_language_count"], {"completed": codeql["completed_language_count"], "planned": codeql["planned_language_count"]})
    check("codeql_complete_suite_selection", codeql["selected_query_count"] == sum(expected_queries.values()), codeql["selected_query_count"])
    check("codeql_complete_suite_execution", codeql["all_selected_queries_have_logged_evaluation_completion"] and codeql["query_evaluation_completions_in_successful_logs"] == sum(expected_queries.values()), codeql["query_evaluation_completions_in_successful_logs"])
    total = 0
    for language, item in codeql["languages"].items():
        check(f"codeql_{language}_analysis_completed", item["status"] == "analyzed", item["status"])
        if item["status"] != "analyzed":
            continue
        check(f"codeql_{language}_query_scope", item["selected_queries"] == expected_queries[language], item["selected_queries"])
        create_command = item["create"]["command"]
        analyze_command = item["analyze"]["command"]
        create_database = Path(create_command[create_command.index("create") + 1]).resolve()
        analyze_database = Path(analyze_command[analyze_command.index("analyze") + 1]).resolve()
        check(f"codeql_{language}_database_pair", create_database == analyze_database, "Successful creation and analysis reference the same local database")
        sarif = read(f"codeql/{language}.sarif")
        results = [x for run in sarif["runs"] for x in run.get("results", [])]
        successful = all(inv["executionSuccessful"] for run in sarif["runs"] for inv in run["invocations"])
        check(f"codeql_{language}_execution", successful and item["analyze"]["exit_code"] == 0 and item["create"]["exit_code"] == 0, "Create and analyze exit status plus SARIF invocation")
        check(f"codeql_{language}_count", len(results) == item["alerts"], len(results))
        total += len(results)
        review_path = "codeql/python-quality-review.json" if language == "python" else f"codeql/{language}-finding-review.json"
        if (ROOT / review_path).exists():
            review = read(review_path)
            sarif_sha = hashlib.sha256((ROOT / f"codeql/{language}.sarif").read_bytes()).hexdigest()
            check(f"codeql_{language}_review_source", review["sarif_sha256"] == sarif_sha, "Review references the delivered SARIF hash")
            indices = [x["sarif_result_index_zero_based"] for x in review["findings"]]
            rules_match = all(0 <= x["sarif_result_index_zero_based"] < len(results) and results[x["sarif_result_index_zero_based"]]["ruleId"] == x["rule_id"] for x in review["findings"])
            check(f"codeql_{language}_review_alignment", len(indices) == len(set(indices)) and rules_match, len(indices))
            locations_match = all(results[x["sarif_result_index_zero_based"]]["locations"][0]["physicalLocation"]["artifactLocation"]["uri"] == x["path"] and results[x["sarif_result_index_zero_based"]]["locations"][0]["physicalLocation"]["region"]["startLine"] == x["line"] for x in review["findings"])
            check(f"codeql_{language}_review_locations", locations_match, "Paths and start lines match the original SARIF")
            check(f"codeql_{language}_review_snapshot_hashes", all(source_hashes.get(x["path"]) == x["snapshot_sha256"] for x in review["findings"]), "Reviewed primary alert files match the fixed manifest")
            expected_indices = {i for i, result in enumerate(results) if language != "python" or result["ruleId"] != "py/polynomial-redos"}
            check(f"codeql_{language}_review_scope", set(indices) == expected_indices and review["finding_reviews_pending"] == 0, {"reviewed": len(indices), "expected": len(expected_indices)})
            check(f"codeql_{language}_review_counts", dict(Counter(x["human_verdict"] for x in review["findings"])) == review["human_verdict_counts"], review["human_verdict_counts"])
    check("codeql_total", total == codeql["total_alerts"], total)
    snapshot = read("codeql/source-after-scan-verification.json")
    check("codeql_snapshot_integrity", snapshot["all_manifest_files_unchanged"] and all(not snapshot[k]["changed"] and not snapshot[k]["missing"] for k in ["maintenance_source", "build_context"]), snapshot["verified_at"])
    manifest_sha = hashlib.sha256((ROOT / "source-manifest-resume-2026-10-11.csv").read_bytes()).hexdigest()
    check("codeql_snapshot_manifest", manifest_sha == codeql["source_snapshot"]["manifest_sha256"], manifest_sha)
    context_path = ROOT / "codeql/rust-context-source-verification.json"
    if context_path.exists():
        context = read("codeql/rust-context-source-verification.json")
        context_root = Path(context["probe_root"])
        rust_command = codeql["languages"]["rust"]["create"]["command"]
        rust_source_args = [arg.split("=", 1)[1] for arg in rust_command if arg.startswith("--source-root=")]
        check("codeql_rust_final_context_selected", len(rust_source_args) == 1 and Path(rust_source_args[0]).resolve() == context_root.resolve(), "Final Rust database uses the asset-complete independent source copy")
        check("codeql_rust_original_context_preserved", context["files"] == 2514 and context["all_original_manifest_entries_match"] and context["all_original_manifest_entries_unchanged"] and not context["original_manifest_entries_changed"] and not context["original_manifest_entries_missing"], "All 2502 maintenance files and 12 build context files retain the original bytes")
        context_rows = context["files_before_build"] + context["added_assets"] + context["generated_build_context"]
        context_hashes_match = all((context_root / row["path"]).is_file() and hashlib.sha256((context_root / row["path"]).read_bytes()).hexdigest() == row["sha256"] for row in context_rows)
        check("codeql_rust_context_file_hashes", context_hashes_match, {"original_files": len(context["files_before_build"]), "local_assets": len(context["added_assets"]), "generated_build_context": len(context["generated_build_context"])})
        before_hashes = {row["path"]: row["sha256"] for row in context["files_before_build"]}
        check("codeql_rust_maintenance_snapshot_matches", all(before_hashes.get(path) == digest for path, digest in source_hashes.items()), "The final Rust copy retains every fixed maintenance source hash")
        macro_probe = read("codeql/" + context["macro_probe_execution_file"])
        check("codeql_rust_offline_context_compile", macro_probe["exit_code"] == 0 and "--offline" in macro_probe["command"] and "--locked" in macro_probe["command"], "Actual offline locked Cargo check succeeds; this is not application execution or a Rust test run")

    # 恢复提交可以成功，但它不能覆盖或伪装已中断 Scanner 的退出状态。
    state = read("sonar/execution-status.json")
    ce = read("sonar/compute-engine-task.json")["task"]
    issues = read("sonar/issues.json")
    sonar = read("sonar/summary.json")
    hotspots = read("sonar/hotspots.json")
    check("sonar_platform_processing", state["current_results_valid"] and ce["status"] == "SUCCESS", ce["status"])
    check("sonar_issue_export", len(issues["issues"]) == issues["total"] == issues["exported"] == sonar["issue_count"], issues["total"])
    check("sonar_unique_issues", len({x["key"] for x in issues["issues"]}) == issues["total"], "Unique issue keys")
    check("sonar_hotspot_export", len(hotspots["hotspots"]) == hotspots["total"] == sonar["hotspot_count"], hotspots["total"])
    scanner_exit = read("sonar/scanner-exit.json")
    check("sonar_recovery_is_explicit", scanner_exit["exit_code"] is None and scanner_exit["standard_scanner_publish_success"] is False and state["original_scanner_standard_publish_success"] is False, "Interrupted Scanner publish and successful platform processing are recorded separately")
    review = read("sonar/finding-review.json")
    issue_keys = {x["key"] for x in issues["issues"]}
    review_keys = [x["issue_key"] for x in review["findings"]]
    check("sonar_review_alignment", len(review_keys) == len(set(review_keys)) and set(review_keys).issubset(issue_keys), len(review_keys))
    check("sonar_review_scope", review["reviewed_alerts"] + review["finding_reviews_pending"] == issues["total"], {"reviewed": review["reviewed_alerts"], "pending": review["finding_reviews_pending"]})

    actions = read("actions/execution.json")
    check("local_actionlint_execution", actions["exit_code"] == 0 and not actions["diagnostics"], "Local static check only; GitHub Actions platform not run")

    coverage = read("coverage/coverage-summary.json")
    tests = read("coverage/go-test-resume-execution.json")
    gate = read("coverage/local-gate-execution.json")
    check("coverage_local_execution", coverage["valid_six_package_measurement"] and tests["test_process_exit_code"] == tests["cover_tool_exit_code"] == 0 and tests["source_unchanged"], "Six local Go packages plus official coverage tool")
    check("coverage_cloud_limits", coverage["codecov_platform_executed"] is False and coverage["cloud_upload"] is False and tests["cloud_platform_executed"] is False and tests["codecov_upload"] is False, "No cloud upload or run")
    profile = ROOT / "coverage/go-core-resume.cover"
    check("coverage_profile_hash", hashlib.sha256(profile.read_bytes()).hexdigest() == tests["profile_sha256"], "Completed profile, separate from old interrupted header")
    totals = {name: [0, 0] for name in coverage["packages"]}
    profile_rows = profile.read_text(encoding="utf-8").splitlines()
    valid_rows = profile_rows[0] == "mode: atomic" and len(profile_rows) > 1
    for line in profile_rows[1:]:
        match = re.fullmatch(r"(.+):\d+\.\d+,\d+\.\d+ (\d+) (\d+)", line)
        if not match:
            valid_rows = False
            continue
        path, count, hits = match.groups()
        package = next((name for name in totals if f"/internal/{name}/" in path), None)
        if package is None:
            valid_rows = False
            continue
        totals[package][1] += int(count)
        totals[package][0] += int(count) if int(hits) > 0 else 0
    check("coverage_profile_records", valid_rows, len(profile_rows) - 1)
    check("coverage_package_statements", all(totals[name] == [item["covered_statements"], item["total_statements"]] for name, item in coverage["packages"].items()), totals)
    covered = sum(value[0] for value in totals.values())
    statements = sum(value[1] for value in totals.values())
    check("coverage_weighted_total", covered == coverage["total_covered_statements"] and statements == coverage["total_statements"] and abs(covered * 100 / statements - coverage["weighted_total_percent"]) < 1e-9, {"covered": covered, "statements": statements})
    failed_packages = [name for name, (hits, count) in totals.items() if hits * 100 / count < coverage["packages"][name]["floor_percent"]]
    check("coverage_floor_result_is_explicit", failed_packages == ["releasegate"] and coverage["packages"]["releasegate"]["meets_configured_floor"] is False and gate["exit_code"] == 1 and gate["new_go_tests_executed"] is False, {"failed_packages": failed_packages, "local_gate_exit": gate["exit_code"]})

    result = {"verified_at": datetime.now(timezone(timedelta(hours=8))).isoformat(), "verification_scope": "原始执行证据与派生统计的一致性；不代表所有项目质量、覆盖率或架构门禁通过。", "checks": checks, "passed": all(x["passed"] for x in checks), "cloud_actions_run": False, "codecov_upload": False}
    if preflight:
        result["phase"] = "preflight_not_final_delivery"
        (ROOT / "delivery-preflight.json").write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf-8")
        print(json.dumps({"passed": result["passed"], "checks": len(checks), "failed_checks": [item for item in checks if not item["passed"]]}))
        return 0 if result["passed"] else 1
    (ROOT / "delivery-verification.json").write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding="utf-8")
    # 只遍历审计目录，不枚举外部凭据、工具缓存或业务工作区。
    # 核验时留存于审计目录的少量源码附件也纳入清单。
    artifacts = []
    for path in sorted(ROOT.rglob("*")):
        if not path.is_file() or "__pycache__" in path.parts or path.name == "artifact-manifest.json":
            continue
        artifacts.append({"path": path.relative_to(ROOT).as_posix(), "bytes": path.stat().st_size, "sha256": hashlib.sha256(path.read_bytes()).hexdigest()})
    (ROOT / "artifact-manifest.json").write_text(json.dumps({"recorded_at": result["verified_at"], "files": artifacts}, ensure_ascii=False, indent=2), encoding="utf-8")
    print(json.dumps({"passed": result["passed"], "checks": len(checks), "failed_checks": [item for item in checks if not item["passed"]], "artifact_files": len(artifacts)}))
    return 0 if result["passed"] else 1


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--preflight", action="store_true", help="仅保存过程核验，不生成最终交付指纹")
    raise SystemExit(main(parser.parse_args().preflight))
