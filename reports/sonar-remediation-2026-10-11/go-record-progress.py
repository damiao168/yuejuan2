"""记录本智能体逐原告警及已完整审读文件的批次进度；平台关闭另由官方复扫证明。"""
from pathlib import Path
import collections
import datetime
import hashlib
import json


ROOT = Path(__file__).resolve().parent


def save(data):
    counts = collections.Counter(item["state"] for item in data["issue_states"])
    data.update(
        updated_at=datetime.datetime.now().astimezone().isoformat(),
        changed_awaiting_sonar=counts["changed_awaiting_sonar"],
        changed_awaiting_tests=counts["changed_awaiting_tests"],
        pending=counts["pending"],
    )
    (ROOT / "go-progress.json").write_text(json.dumps(data, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    lines = [
        "# Go原告警处理进度", "", f"更新时间：{data['updated_at']}", "",
        f"原209项：关联包测试通过、待Sonar复扫 {counts['changed_awaiting_sonar']} 项；源码已修改、待验证 {counts['changed_awaiting_tests']} 项；尚未处理 {counts['pending']} 项。平台确认关闭 {data.get('platform_verified_resolved', 0)} 项。", "",
        "原告警全部在测试代码。保留原断言、失败短路与状态变更次序；不使用排除、抑制或放宽规则。",
        "server 62项、Fujian导入工具6项、modelgovernance 12项由其他智能体独占，本文件未自动宣称其已修复。", "",
        "|批次|原问题数|实际验证证据|状态|", "|---|---:|---|---|",
    ]
    for batch in data["batches"]:
        executions = ", ".join(batch.get("test_executions", [])) or batch.get("test_execution") or "尚未执行"
        lines.append(f"|{batch['id']}|{batch['count']}|{executions}|{batch.get('test_state', 'passed')}|")
    lines += [
        "", "首次内存/编译失败证据保留，只有真实完成的关联包验证才计入通过。环境条件跳过见测试JSON，不能作为真实数据库或外部模型集成通过。", "",
        "逐原 issue key 与逐文件审读/SHA256记录见 go-progress.json。未完整审读文件保持 pending；辅助AST估值不替代平台复扫。",
    ]
    (ROOT / "go-progress.md").write_text("\n".join(lines) + "\n", encoding="utf-8")
    print(json.dumps(dict(counts)))


def record_batch(batch_id, changes, expected_count, execution=None):
    data = json.loads((ROOT / "go-progress.json").read_text(encoding="utf-8"))
    state = "changed_awaiting_tests"
    if execution is not None:
        evidence = json.loads((ROOT / execution).read_text(encoding="utf-8"))
        assert evidence["exit_code"] == 0, (execution, evidence["exit_code"])
        state = "changed_awaiting_sonar"
    affected = 0
    for issue in data["issue_states"]:
        if issue["path"] in changes:
            issue.update(state=state, batch=batch_id, change=changes[issue["path"]])
            affected += 1
    assert affected == expected_count, (batch_id, affected, expected_count)
    for file in data["files"]:
        if file["path"] in changes:
            file.update(review_state="read_and_refactored", maintenance_points=changes[file["path"]])
            file["source_sha256"] = hashlib.sha256(Path(file["path"]).read_bytes()).hexdigest()
    data["batches"] = [batch for batch in data["batches"] if batch["id"] != batch_id] + [dict(
        id=batch_id, count=expected_count, test_execution=execution,
        test_state="passed" if execution else "waiting serial verification", sonar_rescan="pending",
    )]
    save(data)


if __name__ == "__main__":
    data = json.loads((ROOT / "go-progress.json").read_text(encoding="utf-8"))
    evidence = json.loads((ROOT / "go-batch12-tests.json").read_text(encoding="utf-8"))
    assert evidence["exit_code"] == 0
    for issue in data["issue_states"]:
        if issue["batch"] == "go-batch12":
            issue["state"] = "changed_awaiting_sonar"
    for batch in data["batches"]:
        if batch["id"] == "go-batch12":
            batch["test_state"] = "passed"
    save(data)
