"""引用实际 server 包证据，保留题库五场景的条件跳过而不记为 PostgreSQL 通过。"""
import datetime
import hashlib
import json
import os
from pathlib import Path

root = Path(__file__).resolve().parent
path = root / "go-server-question-bank-progress.json"
data = json.loads(path.read_text(encoding="utf-8"))
tests = json.loads((root / "go-server-batch04-tests.json").read_text(encoding="utf-8"))
sha = json.loads((root / "go-server-batch04-source-sha.json").read_text(encoding="utf-8"))["files"]
events = [json.loads(line) for line in (root / "go-server-batch04-events.jsonl").read_text(encoding="utf-8").splitlines() if line]
names = {
    "e2e_question_bank_acl_ux_test.go": "TestE2EPostgresUXHistoricalACLAndActualMemberResponsibilities",
    "e2e_question_bank_import_test.go": "TestE2EPostgresQuestionBankFrozenImport",
    "e2e_question_bank_metadata_acl_search_test.go": "TestE2EPostgresQuestionBankMetadataACLSearch",
    "e2e_question_bank_publication_test.go": "TestE2EPostgresQuestionBankPublication",
    "e2e_question_bank_test.go": "TestE2EPostgresQuestionBank",
}
assert tests["exit_code"] == 0
assert tests["source_changed_during_test"] == []
details = {}
for file in data["files"]:
    file_path = file["path"]
    name = names[Path(file_path).name]
    current = hashlib.sha256(Path(file_path).read_bytes()).hexdigest()
    assert current == sha[file_path] == file["source_sha256"], file_path
    results = [item for item in tests["all_test_results"] if item.get("Test") == name and item["Action"] in {"pass", "skip", "fail"}]
    assert len(results) == 1 and results[0]["Action"] == "skip", name
    reasons = [item["Output"].strip() for item in events if item.get("Test") == name and item.get("Action") == "output" and ('requires isolated PostgreSQL' in item.get("Output", '') or 'EDUGRADE_E2E_DATABASE_URL is required' in item.get("Output", ''))]
    assert len(reasons) == 1, name
    details[file_path] = dict(test=name, result="conditionally_skipped", reason=reasons[0], source_sha256=current)
    file["validation_state"] = "compiled_and_package_regression_passed"
    file["pg_scenario_result"] = details[file_path]
for issue in data["issue_states"]:
    issue["state"] = "changed_awaiting_sonar"
    issue["test_validation"] = "compiled_and_package_regression_passed"
    issue["pg_scenario_result"] = details[issue["path"]]
data.update(updated_at=datetime.datetime.now().astimezone().isoformat(), package_regression_validated=5, dynamically_validated=0, pg_scenarios_passed=0, pg_scenarios_conditionally_skipped=5)
data["verification_evidence"]["dynamic"] = "go-server-batch04-tests.json"
data["verification_evidence"]["event_log"] = "go-server-batch04-events.jsonl"
data["verification_evidence"]["source_sha"] = "go-server-batch04-source-sha.json"
data["package_validation"] = dict(command=tests["command"], actual_exit_code=tests["exit_code"], test_result_counts=tests["test_result_counts"], source_changed_during_test=[], cleared_environment_names=tests["cleared_environment_names"], note="server包实际回归188 pass/62条件skip；题库5已编译，真实PG场景因缺少隔离数据库条件全部skip，不计动态PG通过。")
temporary = path.with_suffix(".json.tmp")
temporary.write_text(json.dumps(data, ensure_ascii=False, indent=2), encoding="utf-8")
os.replace(temporary, path)
print("5 compiled / package regression passed; 5 PostgreSQL scenarios conditionally skipped; source SHA unchanged")
