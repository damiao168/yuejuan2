"""保留 worker 生命周期与 SQL 原步骤，只拆出具名测试阶段。"""
from pathlib import Path
from importlib.util import spec_from_file_location, module_from_spec

ROOT = Path(__file__).resolve().parent
spec = spec_from_file_location("reviewed", ROOT / "go-refactor-reviewed-tests.py")
reviewed = module_from_spec(spec)
spec.loader.exec_module(reviewed)

path = Path("services/api-gateway/internal/workerruntime/handlers_test.go")
reviewed.named_case(path, "TestHandlerRequiresSourceAdapterForManagedCompletion", "source", "managedWorkerSourceCase")
reviewed.scenario_stage(path, "TestHandlerRequiresSourceAdapterForManagedCompletion", "assertManagedWorkerRequiresSourceCompletion", "source managedWorkerSourceCase", "source")

path = Path("services/api-gateway/internal/workerruntime/store_postgres_batch48_test.go")
reviewed.named_case(path, "TestBatch48RuntimeClaimKeepsPollingIdentityAndDatabaseClock", "scenario", "workerPollingIdentityCase")
reviewed.scenario_stage(path, "TestBatch48RuntimeClaimKeepsPollingIdentityAndDatabaseClock", "assertWorkerPollingDatabaseClock", "scenario workerPollingIdentityCase, clock time.Time, dbNow time.Time", "scenario, clock, dbNow")
reviewed.scenario_stage(path, "TestBatch48RuntimeHeartbeatKeepsTaskQueueProgressAndAtomicFailure", "assertWorkerHeartbeatAtomicFailure", "heartbeatErr error, dbNow time.Time", "heartbeatErr, dbNow")

path = Path("services/api-gateway/internal/workerruntime/store_memory_test.go")
source = path.read_text(encoding="utf-8")
function = source.index("func TestClaimHeartbeatAndComplete(")
begin = source.index("\n\tcompleted, err := store.Complete(", function) + 1
end = source.index("\n}\n", begin)
body = source[begin:end]
source = source[:begin] + "\tassertWorkerCompletionAndDuplicateLease(t, store, task, claimed[0], running)" + source[end:]
body = body.replace("claimed[0].LeaseToken", "claimed.LeaseToken")
source += "\n// 完成后原租约可幂等重放；过期令牌必须失败且不得推进版本。\nfunc assertWorkerCompletionAndDuplicateLease(t *testing.T, store *workerruntime.MemoryStore, task workerruntime.Task, claimed workerruntime.Task, running workerruntime.Task) {\n\tt.Helper()\n" + body + "\n}\n"
path.write_text(source, encoding="utf-8")

path = Path("services/api-gateway/tools/story056-real-worker-e2e/main_batch50_test.go")
reviewed.scenario_stage(path, "TestBatch50RegistrationCompletionPreservesAssetsAndGuard", "assertRegistrationCompletionAffectedRows", "changed int64", "changed")

spec = spec_from_file_location("progress", ROOT / "go-record-progress.py")
progress = module_from_spec(spec)
spec.loader.exec_module(progress)
progress.record_batch("go-batch15", {
    "services/api-gateway/internal/workerruntime/handlers_test.go": "完整审读：按来源类型拆出托管任务完成场景，保留409拒绝、激活错误码和租约未完成断言。",
    "services/api-gateway/internal/workerruntime/store_postgres_batch48_test.go": "完整审读：分别提取轮询身份数据库时钟和心跳原子失败场景，保留原SQL参数、步骤顺序及回滚/副本断言。",
    "services/api-gateway/internal/workerruntime/store_memory_test.go": "完整审读：提取完成及重复租约检查阶段，先保留领取/心跳状态版本进度检查，再验证幂等完成和过期令牌拒绝。",
    "services/api-gateway/tools/story056-real-worker-e2e/main_batch50_test.go": "完整审读：提取注册完成受影响行数场景，保留资产绑定SQL原次序和仅一行成功的控制边界。",
}, 5)
