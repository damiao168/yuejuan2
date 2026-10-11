"""HTTP 游标分页、平台学校投影与文件协调按职责提取。"""
from pathlib import Path
from importlib.util import spec_from_file_location, module_from_spec

ROOT = Path(__file__).resolve().parent
spec = spec_from_file_location("reviewed", ROOT / "go-refactor-reviewed-tests.py")
reviewed = module_from_spec(spec)
spec.loader.exec_module(reviewed)

path = Path("services/api-gateway/internal/backmark/handlers_test.go")
reviewed.named_case(path, "TestBackmarkHandlersReturnBoundedCursorPages", "test", "backmarkCursorRouteCase")
reviewed.scenario_stage(path, "TestBackmarkHandlersReturnBoundedCursorPages", "assertBackmarkCursorRoute", "test backmarkCursorRouteCase, batchID string", "test, created.Summary.Batch.ID")
source = path.read_text(encoding="utf-8")
function = source.index("func assertBackmarkCursorRoute(")
source = source[:function] + source[function:].replace("created.Summary.Batch.ID", "batchID")
begin = source.index("\n\t\trequest := httptest.NewRequest", function) + 1
end = source.index("\n\t}\n}\n", begin)
body = "\n".join(line[1:] if line.startswith("\t") else line for line in source[begin:end].splitlines())
body = body.replace("\tcursor = decoded.Next", "\treturn decoded.Next")
source = source[:begin] + "\t\tcursor = assertBackmarkCursorPage(t, test, batchID, page, cursor, seen)" + source[end:]
source += "\n// 连续页共享已见ID，只在当前页内容与分页边界都正确时推进游标。\nfunc assertBackmarkCursorPage(t *testing.T, test backmarkCursorRouteCase, batchID string, page int, cursor string, seen map[string]bool) string {\n\tt.Helper()\n" + body + "\n}\n"
path.write_text(source, encoding="utf-8")

path = Path("services/api-gateway/internal/platformschools/handlers_test.go")
reviewed.named_case(path, "TestPlatformSchoolHandlersExposeSeparateReadModels", "tests", "platformSchoolReadModelCase", True)
reviewed.scenario_stage(path, "TestPlatformSchoolHandlersExposeSeparateReadModels", "assertPlatformSchoolReadModel", "test platformSchoolReadModelCase, mux *http.ServeMux", "test, mux")

path = Path("services/api-gateway/internal/files/reconciliation_batch21_test.go")
reviewed.named_case(path, "TestReconciliationInspectBranchOrder", "cases", "fileReconciliationBranchCase", True)
reviewed.scenario_stage(path, "TestReconciliationInspectBranchOrder", "assertFileReconciliationBranchOrder", "test fileReconciliationBranchCase", "test")

spec = spec_from_file_location("progress", ROOT / "go-record-progress.py")
progress = module_from_spec(spec)
spec.loader.exec_module(progress)
progress.record_batch("go-batch17", {
    "services/api-gateway/internal/backmark/handlers_test.go": "完整审读：拆分各HTTP路由与单游标页核验，保留跨页seen共享、先校验再推进游标及创建响应全部边界。",
    "services/api-gateway/internal/platformschools/handlers_test.go": "完整审读：提取学校各只读模型请求核验，保留JSON字段、敏感字段拒绝与仲裁请求量存在断言。",
    "services/api-gateway/internal/files/reconciliation_batch21_test.go": "完整审读：提取协调分支场景，保留对象/SQL顺序、失败补偿、租户身份和各finding的修复状态。",
}, 3)
