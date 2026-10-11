"""发布 E2E 保留共享版本指针及并发 Wait/收集顺序，阶段只按真实状态边界拆分。"""
import importlib.util
from pathlib import Path

root = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("qb", root / "go-qb-extract.py")
qb = importlib.util.module_from_spec(spec)
spec.loader.exec_module(qb)
path = Path("services/api-gateway/internal/server/e2e_question_bank_publication_test.go")
test = "TestE2EPostgresQuestionBankPublication"
source = path.read_text(encoding="utf-8")
begin = source.index('\tdecode := func(value map[string]any) questionbank.Version {')
body_start = source.index("{\n", begin) + 2
end = source.index('\n\t}\n\tdecision :=', body_start)
decode_body = "\n".join(line[1:] if line.startswith("\t") else line for line in source[body_start:end].splitlines())
source = source[:body_start] + '\t\treturn decodeQuestionBankPublicationVersion(t, value)' + source[end:]
begin = source.index('\tt.Run("independent approval and return invalidation"')
source = source[:begin] + '''\tstate := questionBankPublicationScenario{env: questionBankE2EEnvironment{ctx: ctx, db: db, store: store, scope: scope, router: router, token: token},
        tenant: tenant, school: school, class: class, reviewer: reviewer, reviewerToken: reviewerToken,
        content: content, scoring: scoring, template: template, item: item, assetID: assetID, version: &v,
        save: save, decision: decision, publish: publish}
''' + source[begin:]
begin = source.index('\tt.Run("atomic failure recovery and durable receipt"')
source = source[:begin] + '\tstate.examID, state.input = examID, in\n' + source[begin:]
source += '''
// 每个子场景读写同一版本指针，失败时也保留此前已发生的状态变化；请求 helper 已绑定原认证与命令输入。
type questionBankPublicationScenario struct {
    env questionBankE2EEnvironment
    tenant, school, class, reviewer, reviewerToken, assetID, examID string
    content questionbank.Content
    scoring questionbank.Scoring
    template, item questionbank.ItemResult
    version *questionbank.Version
    input questionbank.MaterializeInput
    save func(questionbank.Version, questionbank.Scoring, string) questionbank.Version
    decision func(questionbank.Version, string, string, string, int) questionbank.Version
    publish func(questionbank.Version, string) questionbank.Version
}
'''
source += "\nfunc decodeQuestionBankPublicationVersion(t *testing.T, value map[string]any) questionbank.Version {\n\tt.Helper()\n" + decode_body + "\n}\n"
path.write_text(source, encoding="utf-8")
bindings = {name: "state.env." + name for name in ("ctx", "db", "store", "scope", "router", "token")}
bindings.update({name: "state." + name for name in ("tenant", "school", "class", "reviewer", "reviewerToken", "assetID", "examID", "content", "scoring", "template", "item", "save", "decision", "publish")})
bindings["in"] = "state.input"
for label, helper in [
    ("independent approval and return invalidation", "assertQuestionBankPublicationApprovalBoundaries"),
    ("published child facts and bindings are immutable", "assertQuestionBankPublishedChildrenImmutable"),
    ("atomic failure recovery and durable receipt", "assertQuestionBankPublicationMaterializeRecovery"),
    ("template versions and bank retirement cannot change exam facts", "assertQuestionBankPublishedExamSnapshotImmutable"),
]:
    qb.subtest(path, test, label, helper, "state questionBankPublicationScenario", "state", bindings, {"v": "(*state.version)"})
qb.stage(path, "assertQuestionBankPublicationApprovalBoundaries", '\treviewerScope := auth.AccessScope', '\t(*state.version) = decision((*state.version), "return-to-draft"',
    "assertQuestionBankConcurrentIndependentApproval", "state questionBankPublicationScenario", "assertQuestionBankConcurrentIndependentApproval(t, state)", bindings)
qb.stage(path, "assertQuestionBankPublicationApprovalBoundaries", '\tresults = make(chan transitionResult, 2)', '\n}\n',
    "assertQuestionBankConcurrentPublication", "state questionBankPublicationScenario", "assertQuestionBankConcurrentPublication(t, state)", bindings)
source = path.read_text(encoding="utf-8")
function = source.index("func assertQuestionBankConcurrentPublication(")
begin = source.index('\tresults = make(chan transitionResult, 2)', function)
source = source[:begin] + '''\ttype transitionResult struct { version questionbank.Version; err error }
    var wg sync.WaitGroup
''' + source[begin:]
tail = source[begin:]
tail = tail.replace('\tresults = make(chan transitionResult, 2)', '\tresults := make(chan transitionResult, 2)', 1)
tail = tail.replace('\tsucceeded, rejected = 0, 0', '\tsucceeded, rejected := 0, 0', 1)
source = source[:begin] + tail
path.write_text(source, encoding="utf-8")
