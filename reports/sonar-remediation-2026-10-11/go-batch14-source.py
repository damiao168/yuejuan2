"""授权契约、事务和公共电脑审计：仅提取已审读的业务阶段。"""
from pathlib import Path
from importlib.util import spec_from_file_location, module_from_spec

ROOT = Path(__file__).resolve().parent
spec = spec_from_file_location("reviewed", ROOT / "go-refactor-reviewed-tests.py")
reviewed = module_from_spec(spec)
spec.loader.exec_module(reviewed)

path = Path("services/api-gateway/internal/auth/authorization_contract_postgres_test.go")
source = path.read_text(encoding="utf-8")
begin = source.index("\n\tmigrationFiles, err := filepath.Glob(") + 1
end = source.index("\n\n\tcontractRaw, err :=", begin)
body = source[begin:end]
source = source[:begin] + "\tapplyAuthorizationContractMigrations(t, ctx, db)" + source[end:]
source += "\n// 按历史文件顺序完整执行迁移，再读取隔离数据库的最终授权矩阵。\nfunc applyAuthorizationContractMigrations(t *testing.T, ctx context.Context, db *pgx.Conn) {\n\tt.Helper()\n" + body + "\n}\n"
function = source.index("func compareAuthorizationMatrix(")
begin = source.index("\n\t\tif role.Scope != expected.Scope", function) + 1
end = source.index("\n\t}\n\tfor code := range actual", begin)
body = "\n".join(line[1:] if line.startswith("\t") else line for line in source[begin:end].splitlines())
source = source[:begin] + "\t\tdifferences = append(differences, authorizationRoleDifferences(code, expected, role)...)" + source[end:]
source += "\n// 同一角色同时核对范围、缺失权限和额外权限；调用方统一排序全部差异。\nfunc authorizationRoleDifferences(code string, expected authorizationRoleContract, role databaseRoleContract) []string {\n\tdifferences := []string{}\n" + body + "\n\treturn differences\n}\n"
path.write_text(source, encoding="utf-8")

path = Path("services/api-gateway/internal/auth/migrations_test.go")
source = path.read_text(encoding="utf-8")
function = source.index("func TestAuthorizationHardeningDefinesFinalRoleMatrix(")
begin = source.index("\n\tcontains := func", function) + 1
end = source.index("\n\tsqlText := compactMigrationSQL", begin)
body = source[begin:end]
source = source[:begin] + "\tassertAuthorizationContractPermissionBoundaries(t, contract)" + source[end:]
source += "\n// 学校管理员保留管理授权；阅卷角色和服务角色分别受人工职责边界约束。\nfunc assertAuthorizationContractPermissionBoundaries(t *testing.T, contract map[string]authorizationRoleContract) {\n\tt.Helper()\n" + body + "\n}\n"
path.write_text(source, encoding="utf-8")

path = Path("services/api-gateway/internal/auth/store_postgres_batch50_test.go")
reviewed.scenario_stage(path, "TestBatch50ManagedCreationKeepsAllBindingsInOneTransaction", "assertManagedUserBindingsTransaction", "lateFailure bool", "lateFailure")

path = Path("services/api-gateway/internal/auth/handlers_test.go")
source = path.read_text(encoding="utf-8")
function = source.index("func TestPublicComputerSessionCanReauthenticateWithoutLosingWorkspace(")
begin = source.index("\n\taudits := store.Audits()", function) + 1
end = source.index("\n}\n", begin)
body = source[begin:end]
source = source[:begin] + "\tassertPublicSessionReauthenticationAudits(t, store)" + source[end:]
source += "\n// 先验证同一会话恢复可用，再核对锁定、失败和成功三类安全审计事实。\nfunc assertPublicSessionReauthenticationAudits(t *testing.T, store *auth.MemoryStore) {\n\tt.Helper()\n" + body + "\n}\n"
path.write_text(source, encoding="utf-8")

spec = spec_from_file_location("progress", ROOT / "go-record-progress.py")
progress = module_from_spec(spec)
spec.loader.exec_module(progress)
progress.record_batch("go-batch14", {
    "services/api-gateway/internal/auth/authorization_contract_postgres_test.go": "完整审读：提取按文件顺序执行迁移及单角色双向权限比较；保留隔离数据库清理和全局差异排序。",
    "services/api-gateway/internal/auth/migrations_test.go": "完整审读：提取角色管理权限与服务角色边界断言；保留权限范围先验检查和迁移SQL契约检查顺序。",
    "services/api-gateway/internal/auth/store_postgres_batch50_test.go": "完整审读：提取用户绑定事务场景；保留所有SQL步骤、晚失败回滚及成功返回副本断言。",
    "services/api-gateway/internal/auth/handlers_test.go": "完整审读：提取公共电脑再认证三类审计事实核验；保留失败后成功请求、同一会话恢复和原断言时序。",
}, 5)
