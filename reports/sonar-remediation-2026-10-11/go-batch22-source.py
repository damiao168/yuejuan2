"""已逐文件审读的试卷结构/回执测试，按独立场景与正式题目保护边界提取。"""
import importlib.util
from pathlib import Path

root = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("extract", root / "go-refactor-reviewed-tests.py")
extract = importlib.util.module_from_spec(spec)
spec.loader.exec_module(extract)
base = Path("services/api-gateway/internal/paper")
path = base / "paper_import_runtime_batch11_test.go"
extract.scenario_stage(path, "TestBatch11LoadParseRunKeepsAddedSourceOrderAndHashGate", "assertPaperParseRunSourceOrderAndHashGate", "scenario string", "scenario")
path = base / "paper_import_structure_batch37_test.go"
for test, typename, helper in [
    ("TestBatch37MemoryStructureGuardPrecedence", "paperMemoryStructureGuardCase", "assertPaperMemoryStructureGuardPrecedence"),
    ("TestBatch37PostgresStructureReceiptAndJSONPrecedence", "paperStructureReceiptCase", "assertPaperStructureReceiptFailureOrder"),
    ("TestBatch37PostgresStructureAdoptionGuardsDoNotWrite", "paperStructureAdoptionGuardCase", "assertPaperStructureAdoptionWithoutWrite"),
]:
    extract.named_case(path, test, "test", typename)
    extract.scenario_stage(path, test, helper, "test " + typename, "test")
source = path.read_text(encoding="utf-8")
function = source.index("func TestBatch37MemoryStructureKeepsTeacherAndProtectedContent(")
begin = source.index("\tteacher := s.questions[\"question\"]", function)
end = source.index("\n\tif _, err = s.AdoptPaperStructure", begin)
body = source[begin:end]
source = source[:begin] + "\tassertPaperAdoptionPreservesProtectedQuestions(t, s, job, bank, locked, foreign)" + source[end:]
source += "\n// 采用结构只能填充允许变化的正式题目；教师内容、题库题、锁定题和其他租户资料逐项核对。\nfunc assertPaperAdoptionPreservesProtectedQuestions(t *testing.T, s *MemoryStore, job PaperImportJob, bank, locked, foreign Question) {\n\tt.Helper()\n" + body + "\n}\n"
path.write_text(source, encoding="utf-8")
spec = importlib.util.spec_from_file_location("progress", root / "go-record-progress.py")
progress = importlib.util.module_from_spec(spec)
spec.loader.exec_module(progress)
progress.record_batch("go-batch22", {
    str(base / "paper_import_runtime_batch11_test.go").replace("\\", "/"): "按输入加载场景核对源顺序与哈希失败优先级；原脚本事件与错误短路完整保留。",
    str(path).replace("\\", "/"): "分离内存/事务 guard 场景、回执身份校验和正式题目保护边界；保留 SQL 顺序、回滚、教师内容及跨租户隔离断言。",
}, 5)
