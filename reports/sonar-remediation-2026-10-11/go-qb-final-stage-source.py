"""继续按独立审批发布与来源版本变更阶段提炼，保持执行顺序和断言。"""
import importlib.util
from pathlib import Path

root = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("qb", root / "go-qb-extract.py")
qb = importlib.util.module_from_spec(spec)
spec.loader.exec_module(qb)
path = Path("services/api-gateway/internal/server/e2e_question_bank_import_test.go")
bindings = {name: "env." + name for name in ("ctx", "db", "store", "scope", "router", "token")}
qb.stage(path, "assertFrozenQuestionBankImportPublicationReuse", '\t// 完成映射、独立复核、发布并复制到另一个考试。', '\texam := e2ePostJSON',
    "publishFrozenQuestionBankImport", "env questionBankE2EEnvironment, fixture story056AcceptanceFixture, bank questionbank.Bank, linkedResult questionbank.ImportResult", "v, classID := publishFrozenQuestionBankImport(t, env, fixture, bank, linkedResult)", bindings, "v, classID", " (questionbank.Version, string)", True)
source = path.read_text(encoding="utf-8")
start = source.index('func assertFrozenQuestionBankImportPublicationReuse(')
end = source.index('\nfunc ', start + 1)
part = source[start:end].replace('\tvar err error\n', '')
source = source[:start] + part + source[end:]
path.write_text(source, encoding="utf-8")
path = Path("services/api-gateway/internal/server/e2e_question_bank_publication_test.go")
bindings = {name: "state.env." + name for name in ("ctx", "db", "store", "scope", "router", "token")}
bindings.update({name: "state." + name for name in ("scoring", "template", "item", "save", "publish")})
qb.stage(path, "assertQuestionBankPublishedExamSnapshotImmutable", '\tnext, err := store.CreateVersion', '\tafter, err := paper.NewPostgresStore',
    "reviseAndRetireQuestionBankPublicationSources", "state questionBankPublicationScenario", "reviseAndRetireQuestionBankPublicationSources(t, state)", bindings)
source = path.read_text(encoding="utf-8")
start = source.index('func assertQuestionBankPublishedExamSnapshotImmutable(')
end = source.index('\nfunc ', start + 1)
part = source[start:end]
for alias in ("scoring", "template", "item", "save", "publish"):
    part = part.replace('\t' + alias + ' := state.' + alias + '\n', '')
source = source[:start] + part + source[end:]
path.write_text(source, encoding="utf-8")
