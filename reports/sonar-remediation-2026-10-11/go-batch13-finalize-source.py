from pathlib import Path
import sys
sys.path.insert(0, str(Path(__file__).resolve().parent))
from importlib.util import spec_from_file_location, module_from_spec

path = Path("services/api-gateway/internal/review/review_workbench_batch38_test.go")
source = path.read_text(encoding="utf-8")
function = source.index("func assertReviewTaskContextSourceAndOpinion(")
begin = source.index("\n\topinion := result.AISecondOpinion", function) + 1
end = source.index("\n}\n", begin)
checks = source[begin:end].removeprefix("\topinion := result.AISecondOpinion\n")
source = source[:begin] + "\tassertReviewSecondOpinionHistory(t, test, result.AISecondOpinion, prefillAllowed)" + source[end:]
source += "\n// 风险等级控制分数可见性，历史意见仍保留顺序和空数组形状；源建议已在调用前核对未被改写。\nfunc assertReviewSecondOpinionHistory(t *testing.T, test reviewSecondOpinionCase, opinion *AISecondOpinion, prefillAllowed bool) {\n\tt.Helper()\n" + checks + "\n}\n"
path.write_text(source, encoding="utf-8")

spec = spec_from_file_location("progress", Path(__file__).with_name("go-record-progress.py"))
progress = module_from_spec(spec)
spec.loader.exec_module(progress)
changes = {"services/api-gateway/internal/review/" + name: "按种子提交回退、意见历史与分数可见性、父运行锁、仲裁身份、并发提交退回及双评策略分离测试阶段；保留原断言、失败优先级和并发启动次序，新断言函数t.Helper()，附近中文维护注释已核对。" for name in ["review_workbench_batch38_test.go", "store_postgres_batch34_test.go", "store_test.go"]}
progress.record_batch("go-batch13", changes, 6)
