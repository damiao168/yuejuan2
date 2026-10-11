"""题库元数据、版本迁移、搜索、附件撤权和审计分别保留原阶段顺序。"""
import importlib.util
from pathlib import Path

root = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("qb", root / "go-qb-extract.py")
qb = importlib.util.module_from_spec(spec)
spec.loader.exec_module(qb)
path = Path("services/api-gateway/internal/server/e2e_question_bank_metadata_acl_search_test.go")
test = "TestE2EPostgresQuestionBankMetadataACLSearch"
source = path.read_text(encoding="utf-8")
begin = source.index('\tmin, max, maxLength :=')
source = source[:begin] + '''\tenv := questionBankE2EEnvironment{ctx: ctx, db: db, store: store, scope: owner, router: router, token: token}
    actors := questionBankMetadataActors{manager: manager, author: author, publisher: publisher}
''' + source[begin:]
source += '''
type questionBankMetadataActors struct { manager, author, publisher auth.AccessScope }
type questionBankMetadataDefinition struct {
    fields []questionbank.MetadataFieldDefinition
    taxonomies []questionbank.TaxonomyDefinition
    first, second questionbank.ItemResult
    oldHash string
}
type questionBankMetadataAccess struct { acl questionbank.ACLDocument; derived questionbank.Version }
'''
path.write_text(source, encoding="utf-8")
env = {name: "env." + name for name in ("ctx", "db", "store", "router", "token")}
env.update(owner="env.scope", tenantID="env.scope.TenantID", ownerID="env.scope.ActorID")
actors = {name: "actors." + name for name in ("manager", "author", "publisher")}
actors.update({name + "ID": "actors." + name + ".ActorID" for name in ("manager", "author", "publisher")})
definition = {name: "definition." + name for name in ("fields", "taxonomies", "first", "second", "oldHash")}
access = {name: "access." + name for name in ("acl", "derived")}
qb.stage(path, test, '\tmin, max, maxLength :=', '\tcurrent, err := store.GetBank',
    "createQuestionBankMetadataDefinitions", "env questionBankE2EEnvironment, bank questionbank.Bank",
    "definition := createQuestionBankMetadataDefinitions(t, env, bank)", env,
    result="questionBankMetadataDefinition{fields: fields, taxonomies: taxonomies, first: first, second: second, oldHash: oldHash}", returns=" questionBankMetadataDefinition", declare_err=True)
qb.stage(path, test, '\tcurrent, err := store.GetBank', '\tpage, err := store.SearchItems',
    "assertQuestionBankMetadataVersionAndACL", "env questionBankE2EEnvironment, bank questionbank.Bank, actors questionBankMetadataActors, definition questionBankMetadataDefinition",
    "access := assertQuestionBankMetadataVersionAndACL(t, env, bank, actors, definition)", env | actors | definition,
    result="questionBankMetadataAccess{acl: acl, derived: derived}", returns=" questionBankMetadataAccess")
qb.stage(path, test, '\tpage, err := store.SearchItems', '\tacl, err = store.UpdateACL',
    "assertQuestionBankMetadataSearchAndAsset", "env questionBankE2EEnvironment, bank questionbank.Bank, actors questionBankMetadataActors, definition questionBankMetadataDefinition, schoolID string",
    "assetID := assertQuestionBankMetadataSearchAndAsset(t, env, bank, actors, definition, schoolID)", env | actors | definition,
    result="assetID", returns=" string")
qb.stage(path, test, '\tacl, err = store.UpdateACL', '\t_, err = store.UpdateACL',
    "assertQuestionBankMetadataRevocation", "env questionBankE2EEnvironment, bank questionbank.Bank, actors questionBankMetadataActors, access questionBankMetadataAccess, assetID string",
    "acl := assertQuestionBankMetadataRevocation(t, env, bank, actors, access, assetID)", env | actors | access,
    result="acl", returns=" questionbank.ACLDocument", declare_err=True)
qb.stage(path, test, '\t_, err = store.UpdateACL', '\n}\n',
    "assertQuestionBankMetadataRetirementAndHistory", "env questionBankE2EEnvironment, bank questionbank.Bank, actors questionBankMetadataActors, acl questionbank.ACLDocument, second questionbank.ItemResult",
    "assertQuestionBankMetadataRetirementAndHistory(t, env, bank, actors, acl, definition.second)", env | actors,
    declare_err=True)
