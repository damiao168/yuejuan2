"""主观评分单场景提取；保留 t.Run 标识、原断言与副作用顺序。"""
import importlib.util
from pathlib import Path

root = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("extract", root / "go-refactor-reviewed-tests.py")
extract = importlib.util.module_from_spec(spec)
spec.loader.exec_module(extract)
base = Path("services/api-gateway/internal/subjective")
changes = {}

def subtest(path, test, label, helper, parameters="", arguments=""):
    source = path.read_text(encoding="utf-8")
    function = source.index("func " + test + "(")
    begin = source.index('\tt.Run("' + label + '", func(t *testing.T) {\n', function)
    body_begin = source.index("{\n", begin) + 2
    end = source.index("\n\t})", body_begin)
    body = "\n".join(line[1:] if line.startswith("\t") else line for line in source[body_begin:end].splitlines())
    args = ", " + arguments if arguments else ""
    params = ", " + parameters if parameters else ""
    source = source[:body_begin] + "\t\t" + helper + "(t" + args + ")" + source[end:]
    source += "\nfunc " + helper + "(t *testing.T" + params + ") {\n\tt.Helper()\n" + body + "\n}\n"
    path.write_text(source, encoding="utf-8")

path = base / "adapter_http_test.go"
source = path.read_text(encoding="utf-8")
begin = source.index('\tfor _, forbidden := range []string{"tenant_id"', source.index("func TestHTTPAdapterSendsGovernedIdentityFreeContract("))
end = source.index("\n}\n", begin)
body = source[begin:end]
source = source[:begin] + "\tassertHTTPAdapterGovernedRequestAndEvidence(t, received, output, adapter, input)" + source[end:]
source += "\n// 供应商契约只携带评分证据，不向外暴露学生身份；评分权限、版本和完整答案去重逐项保留。\nfunc assertHTTPAdapterGovernedRequestAndEvidence(t *testing.T, received map[string]any, output AdapterOutput, adapter *HTTPAdapter, input AdapterInput) {\n\tt.Helper()\n" + body + "\n}\n"
path.write_text(source, encoding="utf-8")
changes[str(path).replace("\\", "/")] = "分离 HTTP 调用与对外契约/响应证据断言；保留鉴权、幂等、禁止身份字段、服务器评分权和遥测期望。"

path = base / "grade_settlement_pipeline_test.go"
for label, helper in [
    ("normal subjective", "assertNormalSubjectiveSettlement"),
    ("invalid output", "assertSubjectiveInvalidOutputSettlement"),
    ("prompt injection", "assertSubjectivePromptInjectionSettlement"),
    ("calibration", "assertSubjectiveCalibrationSettlement"),
    ("calibration failure remains distinct", "assertSubjectiveCalibrationErrorBoundary"),
    ("human review", "assertSubjectiveHumanReviewSettlement"),
]:
    subtest(path, "TestGradeSettlementPipelineSubjectivePolicies", label, helper, "input GradeSettlementInput", "input")
changes[str(path).replace("\\", "/")] = "按六种结算策略分离场景；保留校准回调、错误归类、注入风险和人工复核条件。"

path = base / "worker_execution_service_test.go"
for label, helper in [
    ("correct task", "assertWorkerPrepareBoundTask"),
    ("source mismatch", "assertWorkerPrepareSourceMismatch"),
    ("invalid lease", "assertWorkerPrepareInvalidLease"),
    ("inactive status", "assertWorkerPrepareInactiveTask"),
    ("math evidence unavailable", "assertWorkerPrepareMissingMathEvidence"),
    ("math revision conflict", "assertWorkerPrepareMathRevisionConflict"),
    ("eligibility abstain", "assertWorkerPrepareEligibilityAbstention"),
]:
    subtest(path, "TestWorkerExecutionServicePrepare", label, helper)
changes[str(path).replace("\\", "/")] = "按任务/租约/数学证据/准入拒绝边界分离工作准备场景；保留领域运行与队列任务终止状态检查。"

for filename, test, variable, typename, helper, parameter, argument, collection in [
    ("grading_agent_v2_builder_batch35_test.go", "TestBatch35GradingV2ProjectsAndValidatesRubricPoints", "cases", "gradingV2RubricProjectionCase", "assertGradingV2RubricPointProjection", "tc gradingV2RubricProjectionCase", "tc", True),
    ("math_evidence_batch35_test.go", "TestBatch35MathEvidencePreservesCropBindingErrors", "cases", "mathEvidenceCropBindingCase", "assertMathEvidenceCropBindingErrors", "tc mathEvidenceCropBindingCase", "tc", True),
    ("panel_agent_binding_batch48_test.go", "TestBatch48PanelRuntimePreservesScopeBuildOrderAndDependencyErrors", "scenario", "panelDependencyOrderCase", "assertPanelDependencyBuildOrder", "scenario panelDependencyOrderCase, dependencyErr error", "scenario, dependencyErr", False),
]:
    path = base / filename
    extract.named_case(path, test, variable, typename, collection)
    extract.scenario_stage(path, test, helper, parameter, argument)
    changes[str(path).replace("\\", "/")] = "提取具名场景保留原失败短路、版本/裁剪/角色治理边界与证据断言；不改变依赖创建与资源释放顺序。"
path = base / "panel_shadow_runner_test.go"
extract.scenario_stage(path, "TestPanelShadowRunnerRecordsBlindArbitrationAndHumanEscalation", "assertPanelShadowArbitrationAndEscalation", "unresolved bool", "unresolved")
changes[str(path).replace("\\", "/")] = "分离仲裁/人工升级场景；保留三角色执行、结论来源、最终分候选和存储指标顺序。"
spec = importlib.util.spec_from_file_location("progress", root / "go-record-progress.py")
progress = importlib.util.module_from_spec(spec)
spec.loader.exec_module(progress)
progress.record_batch("go-batch23", changes, 7)
