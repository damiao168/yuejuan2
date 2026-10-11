"""组织冲突、上传恢复、阅卷重试和证据规则保留场景时序。"""
from pathlib import Path
from importlib.util import spec_from_file_location, module_from_spec

ROOT = Path(__file__).resolve().parent
spec = spec_from_file_location("reviewed", ROOT / "go-refactor-reviewed-tests.py")
reviewed = module_from_spec(spec)
spec.loader.exec_module(reviewed)

path = Path("services/api-gateway/internal/org/handlers_test.go")
source = path.read_text(encoding="utf-8")
function = source.index("func TestOrganizationDuplicateCodesReturnSpecificConflicts(")
for label, helper in [("school code", "assertOrganizationSchoolCodeConflict"), ("class code", "assertOrganizationClassCodeConflict")]:
    begin = source.index('\tt.Run("' + label + '", func(t *testing.T) {', function)
    body_begin = source.index("{\n", begin) + 2
    end = source.index("\n\t})", body_begin)
    body = "\n".join(line[1:] if line.startswith("\t") else line for line in source[body_begin:end].splitlines())
    source = source[:begin] + '\tt.Run("' + label + '", ' + helper + ')' + source[end+len("\n\t})"):]
    source += "\nfunc " + helper + "(t *testing.T) {\n\tt.Helper()\n" + body + "\n}\n"
path.write_text(source, encoding="utf-8")

path = Path("services/api-gateway/internal/grading/handlers_test.go")
source = path.read_text(encoding="utf-8")
function = source.index("func TestScoringRecoveryEndpointsCoordinateWorkerRuntime(")
begin = source.index("\n\tretryTask, err :=", function) + 1
end = source.index('\n\tstore.run = grading.ScoringRun{ID: "run-3"', begin)
body = source[begin:end].replace("\treq = authedRequest", "\treq := authedRequest", 1).replace("\trec = httptest.NewRecorder()", "\trec := httptest.NewRecorder()", 1)
source = source[:begin] + "\tassertScoringFailedTaskRuntimeRetry(t, runtimeStore, store, router, token)\n" + source[end:]
source += "\n// 取消流程确认后再制造失败任务，HTTP重试必须将同一runtime任务重新入队。\nfunc assertScoringFailedTaskRuntimeRetry(t *testing.T, runtimeStore *workerruntime.MemoryStore, store *scoringRecoveryTestStore, router http.Handler, token string) {\n\tt.Helper()\n" + body + "\n}\n"
path.write_text(source, encoding="utf-8")

path = Path("services/api-gateway/internal/captureupload/service_test.go")
source = path.read_text(encoding="utf-8")
function = source.index("func TestFiveHundredPageFixtureRecoversPage173WithoutDuplicateRegistration(")
begin = source.index("\n\t// 模拟终止和重启", function) + 1
end = source.index("\n\t// 完成后再次 Init", begin)
body = source[begin:end]
source = source[:begin] + "\tresumed := resumeInterruptedCaptureChunks(t, ctx, service, input, payload, updated)\n" + source[end:]
body = body.replace("\t// 模拟终止和重启", "\tfirstChunk := payload[:3]\n\t// 模拟终止和重启", 1)
source += "\nfunc resumeInterruptedCaptureChunks(t *testing.T, ctx context.Context, service *Service, input InitInput, payload []byte, updated Session) InitResponse {\n\tt.Helper()\n" + body + "\n\treturn resumed\n}\n"
function = source.index("func TestFiveHundredPageFixtureRecoversPage173WithoutDuplicateRegistration(")
begin = source.index("\n\tasset, err := service.fileStore.Get", function) + 1
end = source.index("\n}\n", begin)
body = source[begin:end].replace("completed.FileAssetID", "fileAssetID")
source = source[:begin] + "\tassertRecoveredCaptureObjectBytes(t, ctx, service, objects, completed.FileAssetID, payload)" + source[end:]
source += "\n// 完成与重复登记已验证后，仍读取实际存储对象核对恢复后原始字节。\nfunc assertRecoveredCaptureObjectBytes(t *testing.T, ctx context.Context, service *Service, objects *files.MemoryObjectStorage, fileAssetID string, payload []byte) {\n\tt.Helper()\n" + body + "\n}\n"
path.write_text(source, encoding="utf-8")

path = Path("services/api-gateway/internal/taskmaterials/management_scope_test.go")
reviewed.scenario_stage(path, "TestGroupMemberMaterialRejectsOutsiderAndChangedRepresentation", "assertGroupMemberHistoricalMaterialScope", "scenario string", "scenario")

path = Path("services/api-gateway/internal/mathunderstanding/scorer_test.go")
reviewed.scenario_stage(path, "TestFollowThroughAndSelfCorrectionNeedExplicitReviewedPolicy", "assertMathFollowThroughNeedsReviewedPolicy", "scenario string", "scenario")

spec = spec_from_file_location("progress", ROOT / "go-record-progress.py")
progress = module_from_spec(spec)
spec.loader.exec_module(progress)
progress.record_batch("go-batch20", {
    "services/api-gateway/internal/org/handlers_test.go": "完整审读：学校与班级编码冲突分别为具名场景，保留原t.Run、两次请求顺序、状态和明确错误码断言。",
    "services/api-gateway/internal/grading/handlers_test.go": "完整审读：提取取消完成后的失败任务重试阶段，保留实际HTTP调用、同一runtime任务重入队和最终审计顺序。",
    "services/api-gateway/internal/captureupload/service_test.go": "完整审读：提取第173页检查点恢复/分块追加和对象字节核验，保留已确认偏移、过时重试、最终登记幂等全部判断。",
    "services/api-gateway/internal/taskmaterials/management_scope_test.go": "完整审读：提取分组成员材料场景，保留成员/表示哈希/冻结题目SQL范围和外部成员拒绝断言。",
    "services/api-gateway/internal/mathunderstanding/scorer_test.go": "完整审读：提取跟进及自我修正证据场景，保留前序矛盾/缺失/不确定及显式政策全部原评分断言。",
}, 5)
