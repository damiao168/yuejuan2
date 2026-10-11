"""导入候选、内容补齐与解析重试按原边界分解。"""
from pathlib import Path
from importlib.util import spec_from_file_location, module_from_spec

ROOT = Path(__file__).resolve().parent
spec = spec_from_file_location("reviewed", ROOT / "go-refactor-reviewed-tests.py")
reviewed = module_from_spec(spec)
spec.loader.exec_module(reviewed)

path = Path("services/api-gateway/internal/paper/document_import_batch23_test.go")
reviewed.named_case(path, "TestBatch23SourceReadFailureKeepsStorageRouting", "test", "paperSourceReadFailureCase")
reviewed.scenario_stage(path, "TestBatch23SourceReadFailureKeepsStorageRouting", "assertPaperSourceReadStorageRouting", "test paperSourceReadFailureCase, readErr error", "test, readErr")

path = Path("services/api-gateway/internal/paper/paper_import_candidates_test.go")
reviewed.named_case(path, "TestCandidateReconciliationKeepsQuestionAnswerAndSolutionIndependent", "tests", "paperCandidateReconciliationCase", True)
reviewed.scenario_stage(path, "TestCandidateReconciliationKeepsQuestionAnswerAndSolutionIndependent", "assertPaperCandidatesStayIndependent", "testCase paperCandidateReconciliationCase", "testCase")

path = Path("services/api-gateway/internal/paper/paper_import_content_batch32_test.go")
reviewed.named_case(path, "TestPlanContentBatch32PreservesIndependentEmptyFields", "test", "paperIndependentContentCase")
reviewed.scenario_stage(path, "TestPlanContentBatch32PreservesIndependentEmptyFields", "assertPaperIndependentEmptyFields", "test paperIndependentContentCase", "test")

path = Path("services/api-gateway/internal/paper/paper_import_content_test.go")
source = path.read_text(encoding="utf-8")
function = source.index("func TestFillContentPreservesBlueprintAndTeacherContentAndIsIdempotent(")
begin = source.index("\n\tfirstAnswerID, firstSolutionID :=", function) + 1
end = source.index("\n}\n", begin)
body = source[begin:end].replace("\tresult, err = store.FillPaperImportContent", "\tresult, err := store.FillPaperImportContent", 1)
source = source[:begin] + "\tassertPaperFillIdempotencyAndStateBoundaries(t, store, ctx, job, processed, questions)" + source[end:]
source += "\n// 首次补齐已核对蓝图和教师内容，随后重放不能新增版本或绕过人工确认与冻结边界。\nfunc assertPaperFillIdempotencyAndStateBoundaries(t *testing.T, store *MemoryStore, ctx context.Context, job PaperImportJob, processed PaperImportJob, questions []Question) {\n\tt.Helper()\n" + body + "\n}\n"
path.write_text(source, encoding="utf-8")

path = Path("services/api-gateway/internal/paper/paper_import_parse_batch33_test.go")
reviewed.named_case(path, "TestBatch33ParseRetryRetainsTransactionalStateGates", "test", "paperParseRetryCase")
reviewed.scenario_stage(path, "TestBatch33ParseRetryRetainsTransactionalStateGates", "assertPaperParseRetryTransactionGates", "test paperParseRetryCase, readFailure error", "test, readFailure")
source = path.read_text(encoding="utf-8")
function = source.index("func assertPaperParseRetryTransactionGates(")
begin = source.index("\n\tsteps := []paperBatch10Step", function) + 1
end = source.index("\n\tstore, script := openPaperBatch10Store", begin)
body = source[begin:end]
source = source[:begin] + "\tsteps := paperParseRetrySQLSteps(test, readFailure)" + source[end:]
source += "\n// 先准备锁定代次和任务的SQL，再按状态决定重新排队或提交后的回执读取失败。\nfunc paperParseRetrySQLSteps(test paperParseRetryCase, readFailure error) []paperBatch10Step {\n" + body + "\n\treturn steps\n}\n"
path.write_text(source, encoding="utf-8")

spec = spec_from_file_location("progress", ROOT / "go-record-progress.py")
progress = module_from_spec(spec)
spec.loader.exec_module(progress)
progress.record_batch("go-batch21", {
    "services/api-gateway/internal/paper/document_import_batch23_test.go": "完整审读：提取来源读取失败场景，保留内存失败状态写入/持久化原读取错误分派和错误优先级。",
    "services/api-gateway/internal/paper/paper_import_candidates_test.go": "完整审读：提取候选题目/答案/解析独立核验场景，保留组合矩阵和未匹配问题判断。",
    "services/api-gateway/internal/paper/paper_import_content_batch32_test.go": "完整审读：提取空内容字段补齐场景，保留已有答案/解析、蓝图分值和不可改写人工确认断言。",
    "services/api-gateway/internal/paper/paper_import_content_test.go": "完整审读：提取首次补齐后的幂等与状态边界核验；保留原版本、跨租户/代次/冻结错误及人工确认门禁。",
    "services/api-gateway/internal/paper/paper_import_parse_batch33_test.go": "完整审读：分离解析重试SQL场景准备与实际事务核验，保留全部11状态场景、锁定次序和提交后读取失败语义。",
}, 5)
