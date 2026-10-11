"""看板、种子质量、漂移与图像结果：拆分准备和业务核验阶段。"""
from pathlib import Path
from importlib.util import spec_from_file_location, module_from_spec

ROOT = Path(__file__).resolve().parent
spec = spec_from_file_location("reviewed", ROOT / "go-refactor-reviewed-tests.py")
reviewed = module_from_spec(spec)
spec.loader.exec_module(reviewed)

path = Path("services/api-gateway/internal/dashboard/service_test.go")
source = path.read_text(encoding="utf-8")
function = source.index("func TestSummaryUsesEveryTenantExamAndKeepsUnitsSeparate(")
begin = source.index("\n\tfor index := 0; index < 12;", function) + 1
end = source.index("\n\n\torganizationStatistics :=", begin)
body = source[begin:end].replace("submissions.Create(context.Background(), tenantID,", "submissions.Create(context.Background(), tenantScope.TenantID,")
source = source[:begin] + "\tseedDashboardTenantExamSubmissions(t, exams, submissions, tenantScope)" + source[end:]
source += "\n// 先写满同租户十二场考试，再放入另一租户的干扰数据，避免聚合只读取首批记录。\nfunc seedDashboardTenantExamSubmissions(t *testing.T, exams *exam.MemoryStore, submissions *submission.MemoryStore, tenantScope auth.AccessScope) {\n\tt.Helper()\n" + body + "\n}\n"
path.write_text(source, encoding="utf-8")

path = Path("services/api-gateway/internal/seedquality/service_test.go")
source = path.read_text(encoding="utf-8")
function = source.index("func TestSeedPolicySamplingAndHiddenTraitObservation(")
begin = source.index("\n\treceipt, handled, err := service.TrySubmit", function) + 1
end = source.index("\n}\n", begin)
body = source[begin:end]
source = source[:begin] + "\tassertHiddenSeedTraitSubmission(t, service, task)" + source[end:]
source += "\n// 阅卷者提交只得到普通回执；管理侧随后读取独立的质量观察事实。\nfunc assertHiddenSeedTraitSubmission(t *testing.T, service *Service, task Task) {\n\tt.Helper()\n" + body + "\n}\n"
path.write_text(source, encoding="utf-8")

path = Path("services/api-gateway/internal/graderdrift/service_batch18_test.go")
source = path.read_text(encoding="utf-8")
function = source.index("func TestRecomputeBatch18KeepsSortClockAndFailedPauseScope(")
begin = source.index("\n\titems := []seedquality.Observation{}", function) + 1
end = source.index("\n\tevents := []string{}", begin)
body = source[begin:end].replace("\tobserved := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)\n", "")
source = source[:begin] + "\tobserved := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)\n\titems := makeBatch18SortedWindowObservations(observed)\n" + source[end:]
source += "\n// 两位阅卷者的观测时间相同、ID逆序；首个异常分数用来区分相邻窗口。\nfunc makeBatch18SortedWindowObservations(observed time.Time) []seedquality.Observation {\n" + body + "\n\treturn items\n}\n"
path.write_text(source, encoding="utf-8")

path = Path("services/api-gateway/internal/imagequality/result_order_batch30_test.go")
reviewed.named_case(path, "TestBatch30ResultKeepsLegacyProjectionAndReceiptOrder", "tc", "imageQualityResultOrderCase")
reviewed.scenario_stage(path, "TestBatch30ResultKeepsLegacyProjectionAndReceiptOrder", "assertImageQualityResultProjectionOrder", "tc imageQualityResultOrderCase", "tc")
source = path.read_text(encoding="utf-8")
function = source.index("func assertImageQualityResultProjectionOrder(")
begin = source.index("\n\tif tc.processing == ProcessingCompleted", function) + 1
end = source.index("\n}\n", begin)
body = source[begin:end].replace("w.Code", "status")
source = source[:begin] + "\tassertImageQualityRuntimeReceipt(t, trace, tc, w.Code)" + source[end:]
source += "\n// 副作用顺序核验完成后，分别检查成功和可重试/终止失败的 worker 回执。\nfunc assertImageQualityRuntimeReceipt(t *testing.T, trace *resultOrderBatch30, tc imageQualityResultOrderCase, status int) {\n\tt.Helper()\n" + body + "\n}\n"
path.write_text(source, encoding="utf-8")

spec = spec_from_file_location("progress", ROOT / "go-record-progress.py")
progress = module_from_spec(spec)
spec.loader.exec_module(progress)
progress.record_batch("go-batch16", {
    "services/api-gateway/internal/dashboard/service_test.go": "完整审读：提取十二场考试与跨租户干扰数据准备，保留统计单位、全量聚合及作用域时间断言。",
    "services/api-gateway/internal/seedquality/service_test.go": "完整审读：提取隐藏种子题提交及质量观察核验阶段，保留抽样、阅卷者保密和管理观察各原断言。",
    "services/api-gateway/internal/graderdrift/service_batch18_test.go": "完整审读：提取相同时间逆序ID的双阅卷者观测准备，保留窗口排序、时钟调用、暂停失败及再次计算断言。",
    "services/api-gateway/internal/imagequality/result_order_batch30_test.go": "完整审读：提取结果投影场景与worker回执核验；保留HTTP副作用顺序、失败短路及成功/失败回执条件。",
}, 4)
