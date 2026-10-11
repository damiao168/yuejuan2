"""只对逐文件已审读的指定测试执行具名阶段提取；不枚举或修改未审读文件。"""
from pathlib import Path


def named_case(path, test, variable, name, collection=False):
    source = path.read_text(encoding="utf-8")
    function = source.index("func " + test + "(")
    needle = variable + " := []struct {" if collection else "for _, " + variable + " := range []struct {"
    begin = source.index(needle, function)
    end = source.index("\n\t}{", begin)
    fields = source[begin + len(needle):end]
    replacement = variable + " := []" + name + "{" if collection else "for _, " + variable + " := range []" + name + "{"
    source = source[:begin] + replacement + source[end + len("\n\t}{"):]
    source += "\ntype " + name + " struct {" + fields + "\n}\n"
    path.write_text(source, encoding="utf-8")


def scenario_stage(path, test, helper, parameters, arguments, depth=2):
    source = path.read_text(encoding="utf-8")
    function = source.index("func " + test + "(")
    indent = "\t" * depth
    begin = source.index(indent + "t.Run(", function)
    body_begin = source.index("{\n", begin) + 2
    end = source.index("\n" + indent + "})", body_begin)
    body = "\n".join(line[depth:] if line.startswith(indent) else line for line in source[body_begin:end].splitlines())
    source = source[:body_begin] + indent + "\t" + helper + "(t, " + arguments + ")" + source[end:]
    source += "\nfunc " + helper + "(t *testing.T, " + parameters + ") {\n\tt.Helper()\n" + body + "\n}\n"
    path.write_text(source, encoding="utf-8")


if __name__ == "__main__":
    root = Path("services/api-gateway/internal/review")
    path = root / "review_workbench_batch38_test.go"
    named_case(path, "TestBatch38SubmitGradeKeepsSeedFailureAndFallbackOrder", "cases", "reviewSeedSubmitCase", True)
    scenario_stage(path, "TestBatch38SubmitGradeKeepsSeedFailureAndFallbackOrder", "assertReviewSeedSubmitFallbackOrder", "test reviewSeedSubmitCase, input SubmitGradeInput", "test, input")
    named_case(path, "TestBatch38TaskContextKeepsHistoryShapeAndSourceMaterial", "cases", "reviewSecondOpinionCase", True)
    scenario_stage(path, "TestBatch38TaskContextKeepsHistoryShapeAndSourceMaterial", "assertReviewTaskContextSourceAndOpinion", "test reviewSecondOpinionCase", "test")
    path = root / "store_postgres_batch34_test.go"
    named_case(path, "TestBatch34SubmitGradeKeepsParentLockAndFailurePriority", "test", "reviewParentLockCase")
    scenario_stage(path, "TestBatch34SubmitGradeKeepsParentLockAndFailurePriority", "assertReviewParentLockFailurePriority", "test reviewParentLockCase, taskLockFailure error", "test, taskLockFailure")
    named_case(path, "TestBatch34ArbitrationInsertKeepsSessionIdentityAndPolicy", "test", "reviewArbitrationInsertCase")
    scenario_stage(path, "TestBatch34ArbitrationInsertKeepsSessionIdentityAndPolicy", "assertReviewArbitrationSessionBinding", "test reviewArbitrationInsertCase, session DoubleMarkSession, writeFailure error", "test, session, writeFailure")
    path = root / "store_test.go"
    named_case(path, "TestDoubleMarkAutoFinalizesByResolutionStrategy", "tests", "reviewDoubleMarkStrategyCase", True)
    scenario_stage(path, "TestDoubleMarkAutoFinalizesByResolutionStrategy", "assertDoubleMarkResolutionStrategy", "tt reviewDoubleMarkStrategyCase", "tt")
    source = path.read_text(encoding="utf-8")
    function = source.index("func TestSubmitAndReturnAreLinearizable(")
    begin = source.index("\n\t\tstore := NewMemoryStore()", function) + 1
    end = source.index("\n\t}\n}\n", begin)
    body = "\n".join(line[1:] if line.startswith("\t") else line for line in source[begin:end].splitlines())
    source = source[:begin] + "\t\tassertConcurrentReviewSubmitAndReturn(t, iteration)" + source[end:]
    source += "\nfunc assertConcurrentReviewSubmitAndReturn(t *testing.T, iteration int) {\n\tt.Helper()\n" + body + "\n}\n"
    path.write_text(source, encoding="utf-8")
