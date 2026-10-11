"""数学工作流按准备、敌意结果变更、完成和持久化断言拆分，保留锁与推理时序。"""
import importlib.util
from pathlib import Path

root = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("extract", root / "go-refactor-reviewed-tests.py")
extract = importlib.util.module_from_spec(spec)
spec.loader.exec_module(extract)
path = Path("services/api-gateway/internal/subjective/math_grading_handler_test.go")
extract.scenario_stage(path, "TestMathGradeDirectUnresolvedAndMissingCropNeverCreateGrade", "assertMathDirectReviewCannotCreateGrade", "scenario string", "scenario")
extract.scenario_stage(path, "TestMathWorkerCompletionRescoresAndTerminatesUnsafeResults", "assertMathWorkerResultSettlement", "scenario string", "scenario")
extract.scenario_stage(path, "TestMathCropDriftPreventsCachedReplayAndInferenceSettlement", "assertMathCropDriftRejectsReplayAndSettlement", "scenario string", "scenario")
source = path.read_text(encoding="utf-8")
function = source.index("func assertMathWorkerResultSettlement(")
begin = source.index("\thandler, store, adapter, value := newMathHandlerFixture", function)
end = source.index('\n\tif scenario == "forged_score" {', begin)
fixture_body = source[begin:end]
source = source[:begin] + '''\tfixture := prepareMathWorkerCompletionFixture(t, scenario)
\thandler, store, adapter, value := fixture.handler, fixture.store, fixture.adapter, fixture.value
\truntime, run, task, claimed, output := fixture.runtime, fixture.run, fixture.task, fixture.claimed, fixture.output''' + source[end:]
function = source.index("func assertMathWorkerResultSettlement(")
begin = source.index('\n\tif scenario == "forged_score" {', function)
body_begin = source.index("{\n", begin) + 2
end = source.index('\n\t}\n\toutput.RawOutput', body_begin)
execute_body = "\n".join(line[1:] if line.startswith("\t") else line for line in source[body_begin:end].splitlines()).replace("claimed[0].LeaseToken", "claimed.LeaseToken")
source = source[:body_begin] + "\t\tassertMathWorkerExecutionUsesServerScore(t, handler, adapter, task, claimed[0], run)" + source[end:]
begin = source.index('\tstatus, runStatus, taskStatus := http.StatusOK', function)
end = source.index('\n\tw := httptest.NewRecorder()', begin)
mutation_body = source[begin:end]
source = source[:begin] + "\tstatus, runStatus, taskStatus := mutateMathWorkerResultScenario(scenario, handler, store, run, &output)" + source[end:]
begin = source.index('\tgrades := store.grades[key("tenant-a", value.SegmentID)]', function)
end = source.index("\n}\n", begin)
grade_body = source[begin:end]
source = source[:begin] + "\tassertMathWorkerPersistedGradeBoundary(t, scenario, store, value, run, updated)" + source[end:]

function = source.index("func assertMathCropDriftRejectsReplayAndSettlement(")
begin = source.index('\tchangeCrop := func() {', function)
end = source.index('\n\tinput := GradeRequest', begin)
source = source[:begin] + "\tchangeCrop := func() { changeMathHandlerActiveCropHash(handler) }" + source[end:]
begin = source.index('\tgrades := store.grades[key("tenant-a", "segment-1")]', function)
end = source.index("\n}\n", begin)
drift_body = source[begin:end]
source = source[:begin] + "\tassertMathCropDriftStoredOutcome(t, scenario, store)" + source[end:]

source += '''
type mathWorkerCompletionFixture struct {
    handler *Handler
    store *MemoryStore
    adapter *mathHandlerAdapter
    value Context
    runtime *workerruntime.MemoryStore
    run GradingRun
    task workerruntime.Task
    claimed []workerruntime.Task
    output AdapterOutput
}

// 先准备数学证据并领取任务，再取得待提交输出；主动执行场景仍在这之后调用执行入口。
func prepareMathWorkerCompletionFixture(t *testing.T, scenario string) mathWorkerCompletionFixture {
    t.Helper()
''' + fixture_body + '''
    return mathWorkerCompletionFixture{handler: handler, store: store, adapter: adapter, value: value, runtime: runtime, run: run, task: task, claimed: claimed, output: output}
}
'''
source += "\nfunc assertMathWorkerExecutionUsesServerScore(t *testing.T, handler *Handler, adapter *mathHandlerAdapter, task, claimed workerruntime.Task, run GradingRun) {\n\tt.Helper()\n" + execute_body + "\n}\n"
source += "\n// 版本漂移在原互斥锁内修改运行；输出和裁剪故障注入均发生在完成请求之前。\nfunc mutateMathWorkerResultScenario(scenario string, handler *Handler, store *MemoryStore, run GradingRun, output *AdapterOutput) (int, string, string) {\n" + mutation_body + "\n\treturn status, runStatus, taskStatus\n}\n"
source += "\nfunc assertMathWorkerPersistedGradeBoundary(t *testing.T, scenario string, store *MemoryStore, value Context, run, updated GradingRun) {\n\tt.Helper()\n" + grade_body + "\n}\n"
source += "\nfunc changeMathHandlerActiveCropHash(handler *Handler) {\n\tcrops := handler.mathEvidence.Crops.(*activeCropEvidenceFake)\n\tfor i := range crops.values {\n\t\tcrops.values[i].CropSHA256 = strings.Repeat(\"a\", 64)\n\t}\n}\n"
source += "\nfunc assertMathCropDriftStoredOutcome(t *testing.T, scenario string, store *MemoryStore) {\n\tt.Helper()\n" + drift_body + "\n}\n"
path.write_text(source, encoding="utf-8")
spec = importlib.util.spec_from_file_location("progress", root / "go-record-progress.py")
progress = importlib.util.module_from_spec(spec)
spec.loader.exec_module(progress)
progress.record_batch("go-batch25", {str(path).replace("\\", "/"): "数学直接/队列评分按场景和阶段拆分；保留证据先于推理、租约、版本修改锁、服务器重算、失败终态、原短路与缓存裁剪漂移断言。"}, 3)
