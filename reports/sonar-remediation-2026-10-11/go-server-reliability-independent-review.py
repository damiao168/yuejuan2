"""落盘已实际逐阶段审读的独立审查范围和静态证据，不修改冻结源代码。"""
import collections
import datetime
import hashlib
import json
from pathlib import Path
import re

root = Path(__file__).resolve().parent
rel = "services/api-gateway/internal/server/e2e_reliability_command_test.go"
before_path = Path("D:/tools/sonar-remediation-20261011/baseline") / rel
current_path = Path(rel)
old_raw, raw = before_path.read_bytes(), current_path.read_bytes()
old, current = old_raw.decode("utf-8"), raw.decode("utf-8")
strings = re.compile(r'"(?:[^"\\]|\\.)*"|`[^`]*`')
diagnostics = re.compile(r'\.(?:Fatalf?|Errorf?|Skipf?)\(("(?:[^"\\]|\\.)*")')
literal_before, literal_after = collections.Counter(strings.findall(old)), collections.Counter(strings.findall(current))
diagnostic_before, diagnostic_after = collections.Counter(diagnostics.findall(old)), collections.Counter(diagnostics.findall(current))
def methods(source):
    source = strings.sub('""', source)
    source = re.sub(r'//[^\n]*|/\*.*?\*/', '', source, flags=re.S)
    return collections.Counter(re.findall(r'\.(\w+)\s*\(', source))
method_before, method_after = methods(old), methods(current)
tests = json.loads((root / "go-server-batch04-tests.json").read_text(encoding="utf-8"))
sha = json.loads((root / "go-server-batch04-source-sha.json").read_text(encoding="utf-8"))["files"]
handoff = json.loads((root / "go-server-handoff.json").read_text(encoding="utf-8"))
checks = [
    dict(topic="多个命令与身份", baseline_lines="69–144、225–233", current_lines="74–80、185–196、719–812", conclusion="逐客户端CommandID与同索引SessionID配对；与scenario.CommandID区分。修改负载返回并回写SessionInput后才构造独立操作；并发相同命令共用开始屏障并收齐两个结果。"),
    dict(topic="跨阶段导入状态", baseline_lines="237–451", current_lines="83–93、872–1160", conclusion="FileID/Paper/ImportInput/Job、Dispatch、OldClaim、Current、FailureTask/FailureClaim、FailedJob、Retried按原先后顺序返回并回写。代次1取消、代次2旧回调拒绝、代次2故障后重试到3、取消完成竞态均使用对应原运行和租约。"),
    dict(topic="投影归属与版本", baseline_lines="453–677", current_lines="94–104、1163–1418、200–234", conclusion="手工派发与确认历史保留；转移仍先目标后原考试刷新。Claim时版本与后来requested_version分离；外持有者失败后原持有者发布，old/fresh同owner租约也分别校验。"),
    dict(topic="准备重试与耗尽", baseline_lines="679–820", current_lines="237–428", conclusion="三次逐次领取同代次，首轮仅延长数据库上同一租约再用原claimed失败。终态回滚注入在第三轮：CREATE触发器、协调失败、核对未部分提交、DROP、fresh协调。lastClaim/lastOwner阶段返回供终态迟到回调检查，Fatal仍中止同一个子测试。"),
    dict(topic="最终运行时耗尽", baseline_lines="821–878", current_lines="431–498", conclusion="max_attempts=1运行先过期并由新worker领取/终结，再注入来源协调失败；任务/作业/attempt原断言以及DROP后重建Store、重复恢复无变更顺序保持。"),
    dict(topic="采集和冻结边界", baseline_lines="879–1047", current_lines="502–716", conclusion="嵌套采集命令子测试仍先完成；同键重放、失收据接管、删除恢复页修订、重开异常与冻结考试只允许已接受命令恢复的断言保持。"),
    dict(topic="资源/defer/事务归属", baseline_lines="193–235、1050–1213", current_lines="145–182、1420–1633", conclusion="实际客户端server.Close和超时cancel仍在原CMD子测试且cancel先执行；临时文件/二进制仍用同一testing.T的TempDir。该文件未新增显式BeginTx/Commit/Rollback；故障触发器按原顺序移除，生产Store事务语义不由本次静态审查证明。"),
    dict(topic="迁移与修复幂等", baseline_lines="1050–1213", current_lines="1420–1633", conclusion="先升级至000122并种旧数据、再全迁移、种新可恢复命令、查未知历史；修复先限定import预演/应用/重复零变更，再全租户预演/应用。原传输RequestHash、201响应和业务Session.ID、needs_review以及第二次状态/更新时间均原值核对。"),
]
partial = [
    dict(path="services/api-gateway/internal/server/e2e_subjective_command_test.go", review_state="affected_scope_reviewed_only", full_file_review_state="pending", baseline_lines="90–114", current_lines="19–72、104–137", conclusion="scope loop由identity改名scopedIdentity以免遮蔽业务身份。调用仍传input.IdempotencyKey；循环tenant/actor取scopedIdentity，而幂等键始终取业务identity.IdempotencyKey，保持隔离检查。"),
    dict(path="services/api-gateway/internal/server/e2e_worker_runtime_reliability_test.go", review_state="affected_scope_reviewed_only", full_file_review_state="pending", baseline_lines="123–199", current_lines="189–278", conclusion="reoldTask拼写改回reclaimed后，断言仍比较reclaimed[0]与由原claimed[0]返回的oldTask租约；SKIP LOCKED空轮询在两旧操作返回后重试，未改新/旧租约分类或历史预期。"),
    dict(path="services/api-gateway/internal/server/review_route_test.go", review_state="affected_scope_reviewed_only", full_file_review_state="pending", baseline_lines="202–264", current_lines="208–256", conclusion="helper中第一req/rec用:=，第二及后续使用=；每轮仍新建请求/Recorder并交router，保留两个任务/操作者路径与403/404/200预期。"),
    dict(path="services/api-gateway/internal/server/e2e_review_recovery_test.go", review_state="affected_scope_reviewed_only", full_file_review_state="pending", current_lines="246–327", conclusion="RefreshExam用局部err:=后仍先失败Fatal再ListExceptions；后者声明并读取自己的err，未跳过错误检查。未完整对照此文件基线，不作全文件语义结论。"),
    dict(path="services/api-gateway/internal/server/e2e_paper_import_reconciliation_test.go", review_state="affected_scope_reviewed_only", full_file_review_state="pending", current_lines="480–552", conclusion="去掉未使用外层job别名；两个并发闭包自己的job依然来自Save/Replace结果，开始屏障、结果读取与成功/冲突期望未变。未完整对照此文件基线，不作全文件语义结论。"),
]
data = dict(updated_at=datetime.datetime.now().astimezone().isoformat(), reviewer="sonar_go_fix独立只读审查", scope_note="仅reliability command文件基线1213行/当前1633行已全读；另外5文件只审查明确列出的编译修正影响范围，未全读均pending。", source_edits=False,
    primary_file=dict(path=rel, baseline=str(before_path), baseline_line_count=len(old.splitlines()), current_line_count=len(current.splitlines()), baseline_sha256=hashlib.sha256(old_raw).hexdigest(), current_sha256=hashlib.sha256(raw).hexdigest(), review_state="baseline_and_current_fully_read", original_issue_keys=[item["key"] for item in handoff["issues"] if item["path"] == rel], matches_successful_batch04_sha=hashlib.sha256(raw).hexdigest() == sha[rel]),
    checks=checks, affected_scopes=partial, findings=[], resolved_observation=dict(topic="客户端逐命令迭代", note="batch02/03编译修正中曾丢失range commandID，独立审查指出并经frontend恢复，batch04当前版for i,commandID正确；本报告未改源码。"),
    mechanical_support=dict(removed_string_literals=list((literal_before-literal_after).elements()), added_string_literals=list((literal_after-literal_before).elements()), removed_diagnostics=list((diagnostic_before-diagnostic_after).elements()), added_diagnostics=list((diagnostic_after-diagnostic_before).elements()), removed_selector_calls=dict(method_before-method_after), added_selector_calls=dict(method_after-method_before), note="多重集只支持逐阶段人工审读，不证明顺序与参数；额外database/sql只供新fixture类型声明，Helper为新增诊断定位。"),
    runtime_evidence=dict(report="go-server-batch04-tests.json", event_log="go-server-batch04-events.jsonl", sha="go-server-batch04-source-sha.json", actual_exit_code=tests["exit_code"], package_results=tests["test_result_counts"], reviewed_test_results=[item for item in tests["all_test_results"] if item.get("Test") in {"TestReliabilityCommandRecoveryWithPostgresTestDatabase", "TestReliabilityMigrationFrom000122PostgresTestDatabase"}], limitation="两主测试缺EDUGRADE_E2E_DATABASE_URL均实际skip；本审查为静态语义核对及编译/包回归证据，不计真实PostgreSQL业务通过。"))
(root / "go-server-reliability-independent-review.json").write_text(json.dumps(data, ensure_ascii=False, indent=2), encoding="utf-8")
print(json.dumps({"findings": data["findings"], "matches_batch04": data["primary_file"]["matches_successful_batch04_sha"], "mechanical_support": data["mechanical_support"], "reviewed_test_results": data["runtime_evidence"]["reviewed_test_results"]}, ensure_ascii=False, indent=2))
