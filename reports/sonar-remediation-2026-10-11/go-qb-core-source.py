"""题库基础 E2E 按并发、状态、事务和权限职责拆分，保留真实收据和请求结构。"""
import importlib.util
from pathlib import Path

root = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("qb", root / "go-qb-extract.py")
qb = importlib.util.module_from_spec(spec)
spec.loader.exec_module(qb)
path = Path("services/api-gateway/internal/server/e2e_question_bank_test.go")
source = path.read_text(encoding="utf-8")
needle = '\tt.Run("concurrent version allocation and durable replay"'
begin = source.index(needle)
source = source[:begin] + '''\tstate := questionBankCoreScenario{env: questionBankE2EEnvironment{ctx: ctx, db: db, store: store, scope: scope, router: router, token: token},
        tenant: tenant, actor: actor, school: school, bankID: bankID, itemID: itemID, versionID: versionID,
        itemPath: itemPath, bankBody: bankBody, bankInput: bankInput, content: content}
''' + source[begin:]
source += '''
// 隔离数据库与真实 Store/路由共享同一身份；业务阶段输入另列，避免更换 tenant 或 command 的执行上下文。
type questionBankE2EEnvironment struct {
    ctx context.Context
    db *sql.DB
    store *questionbank.PostgresStore
    scope auth.AccessScope
    router http.Handler
    token string
}

type questionBankCoreScenario struct {
    env questionBankE2EEnvironment
    tenant, actor, school, bankID, itemID, versionID, itemPath, bankBody string
    bankInput questionbank.CreateBankInput
    content questionbank.Content
}
'''
path.write_text(source, encoding="utf-8")
bindings = {name: "state.env." + name for name in ("ctx", "db", "store", "scope", "router", "token")}
bindings.update({name: "state." + name for name in ("tenant", "actor", "school", "bankID", "itemID", "versionID", "itemPath", "bankBody", "bankInput", "content")})
for label, helper in [
    ("concurrent version allocation and durable replay", "assertQuestionBankConcurrentVersionAndReplay"),
    ("competing optimistic edits", "assertQuestionBankOptimisticEdits"),
    ("composite foreign keys and draft guard", "assertQuestionBankCompositeIdentityAndDraft"),
    ("archive locks content mutations", "assertQuestionBankArchiveMutationBoundaries"),
    ("transaction rollback includes audit outbox and receipt", "assertQuestionBankRollbackAndReceiptRecovery"),
    ("runtime RLS fails closed and follows tenant", "assertQuestionBankRuntimeTenantRLS"),
    ("new tenant provisioning inherits action permissions", "assertQuestionBankProvisionedPermissions"),
    ("read revocation also denies HTTP receipt replay", "assertQuestionBankRevokedReceiptAccess"),
]:
    qb.subtest(path, "TestE2EPostgresQuestionBank", label, helper, "state questionBankCoreScenario", "state", bindings)
qb.stage(path, "assertQuestionBankConcurrentVersionAndReplay", '\ttype outcome struct {', '\tsameCtx := commandreceipt.WithID',
    "assertQuestionBankUniqueVersionNumbers", "state questionBankCoreScenario", "assertQuestionBankUniqueVersionNumbers(t, state)", bindings)
# 第二轮并发重放独立等待，先前的组已全部结束；容量、命令身份与版本号期望保持原值。
source = path.read_text(encoding="utf-8")
function = source.index("func assertQuestionBankConcurrentVersionAndReplay(")
begin = source.index('\tsameCtx := commandreceipt.WithID', function)
source = source[:begin] + '''\ttype outcome struct { v questionbank.Version; err error }
    var wg sync.WaitGroup
''' + source[begin:]
source = source.replace('\tout = make(chan outcome, 4)', '\tout := make(chan outcome, 4)', 1)
path.write_text(source, encoding="utf-8")
