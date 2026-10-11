"""官方 checkpoint02 新增5项，保持独立账本，不能算作原209项关闭。"""
import datetime
import hashlib
import importlib.util
import json
from pathlib import Path

root = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("extract", root / "go-refactor-reviewed-tests.py")
extract = importlib.util.module_from_spec(spec)
spec.loader.exec_module(extract)
changes = {}

path = Path("services/api-gateway/internal/mathunderstanding/handlers.go")
source = path.read_text(encoding="utf-8")
function = source.index("func retainSyntaxVerifications(")
begin = source.index("\tretained := make([]MathVerification", function)
end = source.index("\n\treturn retained, knownIDs, criticalConfidence, requiresReview, nil", begin)
body = source[begin:end]
source = source[:begin] + "\tretained, knownIDs, criticalConfidence, requiresReview := collectRetainedSyntaxEvidence(contract, syntaxCount+incomingCount)" + source[end:]
body = body.replace("syntaxCount+incomingCount", "capacity")
source += "\n// 容量由调用方在分配前检查；收集保留的语法证据同时累积识别疑点，不能由后续符号验证抹掉。\nfunc collectRetainedSyntaxEvidence(contract CreateArtifactInput, capacity int) ([]MathVerification, map[string]bool, float64, bool) {\n" + body + "\n\treturn retained, knownIDs, criticalConfidence, requiresReview\n}\n"
path.write_text(source, encoding="utf-8")
changes[str(path).replace("\\", "/")] = "容量/溢出预算检查与语法证据收集分离；仍先计 syntax 数、减法检查、再分配，不改保持顺序、ID集合、contradicted归零和人工复核累积。生产接口/权限/事务调用保持原样。"

path = Path("services/api-gateway/internal/releasegate/service_batch54_test.go")
source = path.read_text(encoding="utf-8")
function = source.index("func TestPublishRechecksNewRegradeBlockersAndPreservesPreviewEvidence(")
begin = source.index("\tsavedPreview, err := service.GetEvidence", function)
end = source.index("\n}\n", begin)
body = source[begin:end]
source = source[:begin] + "\tassertRegradePublicationEvidenceIsImmutable(t, ctx, service, preview, publish)" + source[end:]
source += "\nfunc assertRegradePublicationEvidenceIsImmutable(t *testing.T, ctx context.Context, service *Service, preview, publish Evidence) {\n\tt.Helper()\n" + body + "\n}\n"
path.write_text(source, encoding="utf-8")
changes[str(path).replace("\\", "/")] = "分离发布事实、历史预览不可变和返回计数副本保护断言；保留范围复读、阻断、未发布及所有原期望。"

path = Path("services/api-gateway/internal/releasegate/service_coverage_remediation_test.go")
extract.named_case(path, "TestRemediationPublicationResultPreservesPersistedGateEvidence", "test", "releaseGatePublicationResultCase")
extract.scenario_stage(path, "TestRemediationPublicationResultPreservesPersistedGateEvidence", "assertReleaseGatePersistedPublicationResult", "test releaseGatePublicationResultCase", "test")
extract.scenario_stage(path, "TestRemediationPublicationStopsWhenGateDependencyFails", "assertReleaseGateDependencyStopsPublication", "stage string, dependencyFailure error", "stage, dependencyFailure")
source = path.read_text(encoding="utf-8")
function = source.index("func assertReleaseGateDependencyStopsPublication(")
begin = source.index('\tif stage == "base_gate"', function)
end = source.index("\n\tsaved, readErr := service.GetEvidence", begin)
body = source[begin:end]
source = source[:begin] + "\tassertReleaseGateStopsLaterDependencies(t, stage, store, policyCalls, waiverCalls, appendCalls)" + source[end:]
source += "\n// 原阶段读取计数是在成功预览后取得；失败发布不得绕过缺失依赖继续读取或保存通过证据。\nfunc assertReleaseGateStopsLaterDependencies(t *testing.T, stage string, store *remediationServiceStore, policyCalls, waiverCalls, appendCalls int) {\n\tt.Helper()\n" + body + "\n}\n"
path.write_text(source, encoding="utf-8")
changes[str(path).replace("\\", "/")] = "按发布成功/事务失败与门禁依赖失败场景拆分，独立检查失败后的依赖调用计数；保留门禁先落库、原错误、历史预览及范围不变。"

path = Path("services/api-gateway/internal/review/review_workbench_batch38_test.go")
source = path.read_text(encoding="utf-8")
function = source.index("func assertReviewSecondOpinionHistory(")
begin = source.index("\tif opinion.History == nil", function)
end = source.index("\n\tif containsScoreKey(opinion.Metadata)", begin)
body = source[begin:end].replace("opinion.History", "history").replace("test.historyIDs", "historyIDs")
source = source[:begin] + "\thistoryHasScore := assertReviewOpinionHistoryShapeAndOrder(t, opinion.History, test.historyIDs)" + source[end:]
source += "\nfunc assertReviewOpinionHistoryShapeAndOrder(t *testing.T, history []map[string]any, historyIDs []string) bool {\n\tt.Helper()\n" + body + "\n\treturn historyHasScore\n}\n"
path.write_text(source, encoding="utf-8")
changes[str(path).replace("\\", "/")] = "二次意见事实和历史列表形状/顺序职责拆分；原短路的分数存在性累积、风险级别可见性及空数组 JSON 仍逐项验证。"

original = json.loads((root.parent / "platform-audit-2026-10-10/sonar/issues.json").read_text(encoding="utf-8"))
current = json.loads((root / "full-checkpoint-02/issues.json").read_text(encoding="utf-8"))
old_keys = {issue["key"] for issue in original["issues"]}
issues = [issue for issue in current["issues"] if issue["key"] not in old_keys and issue["rule"].startswith("go:")]
assert len(issues) == 5
data = dict(updated_at=datetime.datetime.now().astimezone().isoformat(), source_report="full-checkpoint-02/issues.json", count=5, source_changed=5, dynamically_validated=0, platform_verified_resolved=0, issue_states=[], files=[])
for issue in issues:
    path = issue["component"].split(":", 1)[1]
    assert path in changes
    data["issue_states"].append(dict(key=issue["key"], rule=issue["rule"], severity=issue["severity"], path=path, original_line=issue["line"], state="changed_awaiting_tests", change=changes[path], sonar_rescan="pending"))
for path, change in changes.items():
    data["files"].append(dict(path=path, review_state="read_and_refactored", source_sha256=hashlib.sha256(Path(path).read_bytes()).hexdigest(), maintenance_points=change))
(root / "go-new-findings-progress.json").write_text(json.dumps(data, ensure_ascii=False, indent=2), encoding="utf-8")
print(json.dumps(dict(count=5, source_changed=5, dynamically_validated=0)))
