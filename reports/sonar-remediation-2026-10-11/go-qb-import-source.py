"""冻结题库导入按来源冻结、收据/溯源、去重、部分失败、发布复用、撤权阶段拆分。"""
import importlib.util
from pathlib import Path

root = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("qb", root / "go-qb-extract.py")
qb = importlib.util.module_from_spec(spec)
spec.loader.exec_module(qb)
path = Path("services/api-gateway/internal/server/e2e_question_bank_import_test.go")
test = "TestE2EPostgresQuestionBankFrozenImport"
source = path.read_text(encoding="utf-8")
begin = source.index('\tvar snapshotID string')
source = source[:begin] + '\tenv := questionBankE2EEnvironment{ctx: ctx, db: db, store: store, scope: scope, router: router, token: token}\n' + source[begin:]
source += '''
// 输入保留最初冻结快照和命令身份；后续重试、拒绝与导出不可改读实时试卷定义。
type frozenQuestionBankImportBinding struct {
    snapshotID, questionID, path, sourceBefore string
    input questionbank.ImportQuestionInput
    selection questionbank.ImportPreviewSelection
    preview *questionbank.ImportPreview
}
'''
path.write_text(source, encoding="utf-8")
env = {name: "env." + name for name in ("ctx", "db", "store", "scope", "router", "token")}
binding = {name: "binding." + name for name in ("snapshotID", "questionID", "path", "sourceBefore", "input", "selection")}
binding["p"] = "binding.preview"
qb.stage(path, test, '\tvar snapshotID string', '\tcreated := qbRequest',
    "prepareFrozenQuestionBankImportSource", "env questionBankE2EEnvironment, fixture story056AcceptanceFixture, bank questionbank.Bank",
    "binding := prepareFrozenQuestionBankImportSource(t, env, fixture, bank)", env,
    result="frozenQuestionBankImportBinding{snapshotID: snapshotID, questionID: questionID, path: path, sourceBefore: sourceBefore, input: input, selection: selection, preview: p}", returns=" frozenQuestionBankImportBinding", declare_err=True)
qb.stage(path, test, '\tcreated := qbRequest', '\t// 相同资料包',
    "assertFrozenQuestionBankImportReceiptAndProvenance", "env questionBankE2EEnvironment, fixture story056AcceptanceFixture, binding frozenQuestionBankImportBinding",
    "result := assertFrozenQuestionBankImportReceiptAndProvenance(t, env, fixture, binding)", env | binding,
    result="result", returns=" questionbank.ImportResult", declare_err=True)
qb.stage(path, test, '\t// 相同资料包', '\t// 单条失败后',
    "assertFrozenQuestionBankImportDeduplication", "env questionBankE2EEnvironment, binding frozenQuestionBankImportBinding, result questionbank.ImportResult",
    "linkedResult := assertFrozenQuestionBankImportDeduplication(t, env, binding, result)", env | binding,
    result="linkedResult", returns=" questionbank.ImportResult", declare_err=True)
qb.stage(path, test, '\t// 单条失败后', '\t// 完成映射',
    "assertFrozenQuestionBankImportPartialRecovery", "env questionBankE2EEnvironment, fixture story056AcceptanceFixture, bank questionbank.Bank, binding frozenQuestionBankImportBinding",
    "assertFrozenQuestionBankImportPartialRecovery(t, env, fixture, bank, binding)", env | binding,
    declare_err=True)
qb.stage(path, test, '\t// 完成映射', '\t// 来源作用域',
    "assertFrozenQuestionBankImportPublicationReuse", "env questionBankE2EEnvironment, fixture story056AcceptanceFixture, bank questionbank.Bank, binding frozenQuestionBankImportBinding, linkedResult questionbank.ImportResult",
    "assertFrozenQuestionBankImportPublicationReuse(t, env, fixture, bank, binding, linkedResult)", env | binding,
    declare_err=True)
qb.stage(path, test, '\t// 来源作用域', '\n}\n',
    "assertFrozenQuestionBankImportRevokedReplay", "env questionBankE2EEnvironment, fixture story056AcceptanceFixture, bank questionbank.Bank, binding frozenQuestionBankImportBinding",
    "assertFrozenQuestionBankImportRevokedReplay(t, env, fixture, bank, binding)", env | binding,
    declare_err=True)
