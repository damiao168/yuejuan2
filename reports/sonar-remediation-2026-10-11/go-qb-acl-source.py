"""历史 ACL、实际 RBAC 职责、撤权和管理者保留动作按阶段审查与提取。"""
import importlib.util
from pathlib import Path

root = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("qb", root / "go-qb-extract.py")
qb = importlib.util.module_from_spec(spec)
spec.loader.exec_module(qb)
path = Path("services/api-gateway/internal/server/e2e_question_bank_acl_ux_test.go")
test = "TestE2EPostgresUXHistoricalACLAndActualMemberResponsibilities"
source = path.read_text(encoding="utf-8")
begin = source.index('\tgroupID := uuid.NewString()')
source = source[:begin] + '''\tenv := questionBankE2EEnvironment{ctx: ctx, db: db, store: store, scope: owner, router: router, token: token}
    actors := questionBankUXActors{reviewerID: reviewerID, groupMember: groupMember, viewerID: viewerID}
''' + source[begin:]
source += '''
type questionBankUXActors struct { reviewerID, groupMember, viewerID string }
type questionBankUXUpdate struct { groupID string; input questionbank.UpdateACLInput; updated questionbank.ACLDocument }
'''
path.write_text(source, encoding="utf-8")
env = {name: "env." + name for name in ("ctx", "db", "store", "router", "token")}
env.update(owner="env.scope", tenantID="env.scope.TenantID", ownerID="env.scope.ActorID")
actors = {name: "actors." + name for name in ("reviewerID", "groupMember", "viewerID")}
update = {name: "update." + name for name in ("groupID", "input", "updated")}
qb.stage(path, test, '\tgroupID := uuid.NewString()', '\tinput := questionbank.UpdateACLInput',
    "seedQuestionBankHistoricalACL", "env questionBankE2EEnvironment, bank questionbank.Bank, actors questionBankUXActors",
    "groupID, before := seedQuestionBankHistoricalACL(t, env, bank, actors)", env | actors,
    result="groupID, before", returns=" (string, questionbank.ACLDocument)", declare_err=True)
qb.stage(path, test, '\tinput := questionbank.UpdateACLInput', '\t// 构造独立职责',
    "assertQuestionBankHistoricalACLUpdate", "env questionBankE2EEnvironment, bank questionbank.Bank, actors questionBankUXActors, groupID string, before questionbank.ACLDocument",
    "update := assertQuestionBankHistoricalACLUpdate(t, env, bank, actors, groupID, before)", env | actors,
    result="questionBankUXUpdate{groupID: groupID, input: input, updated: updated}", returns=" questionBankUXUpdate")
qb.stage(path, "assertQuestionBankHistoricalACLUpdate", '\tfor _, old := range before.Bindings', '\treplay, err := store.UpdateACL',
    "assertQuestionBankHistoricalACLRetainedFacts", "before, updated questionbank.ACLDocument, groupID string",
    "assertQuestionBankHistoricalACLRetainedFacts(t, before, updated, groupID)", {})
qb.stage(path, test, '\t// 构造独立职责', '\treader := auth.AccessScope',
    "installQuestionBankUXReviewerRole", "env questionBankE2EEnvironment, actors questionBankUXActors",
    "roleID := installQuestionBankUXReviewerRole(t, env, actors)", env | actors,
    result="roleID", returns=" string", declare_err=True)
qb.stage(path, test, '\treader := auth.AccessScope', '\tfor _, bad := range []auth.AccessScope',
    "assertQuestionBankDirectoryIntersectsRBAC", "env questionBankE2EEnvironment, bank questionbank.Bank, actors questionBankUXActors, roleID, schoolID string",
    "assertQuestionBankDirectoryIntersectsRBAC(t, env, bank, actors, roleID, schoolID)", env | actors)
qb.stage(path, test, '\tfor _, bad := range []auth.AccessScope', '\t// 历史成员停用',
    "assertQuestionBankUXScopeBoundaries", "env questionBankE2EEnvironment, bank questionbank.Bank, update questionBankUXUpdate, schoolID, otherSchoolID string",
    "assertQuestionBankUXScopeBoundaries(t, env, bank, update, schoolID, otherSchoolID)", env | update,
    declare_err=True)
qb.stage(path, test, '\t// 历史成员停用', '\n}\n',
    "assertQuestionBankInactiveMembershipAndOperatorRights", "env questionBankE2EEnvironment, bank questionbank.Bank, actors questionBankUXActors, update questionBankUXUpdate, schoolID string",
    "assertQuestionBankInactiveMembershipAndOperatorRights(t, env, bank, actors, update, schoolID)", env | actors | update,
    declare_err=True)
