"""重评工作流与成绩发布投影保留时序，拆分具名准备和校验阶段。"""
from pathlib import Path
from importlib.util import spec_from_file_location, module_from_spec

ROOT = Path(__file__).resolve().parent
spec = spec_from_file_location("reviewed", ROOT / "go-refactor-reviewed-tests.py")
reviewed = module_from_spec(spec)
spec.loader.exec_module(reviewed)

path = Path("services/api-gateway/internal/regrade/service_test.go")
source = path.read_text(encoding="utf-8")
function = source.index("func TestRegradeFreezesPublishedReleaseAndRequiresNewReleasePlan(")
begin = source.index("\n\tif _, err = service.Approve", function) + 1
end = source.index("\n\tif _, err = service.Finalize", begin)
body = source[begin:end].replace("if _, err = service.Approve", "if _, err := service.Approve").replace("if _, err = service.Start", "if _, err := service.Start")
source = source[:begin] + "\tcompleteRegradeCandidateReview(t, service, summary)" + source[end:]
source += "\n// 幂等创建确认后才批准和启动；每份答卷先写候选分数，再人工确认原版本。\nfunc completeRegradeCandidateReview(t *testing.T, service *Service, summary Summary) {\n\tt.Helper()\n" + body + "\n}\n"
path.write_text(source, encoding="utf-8")

path = Path("services/api-gateway/internal/regrade/store_postgres_batch48_test.go")
reviewed.named_case(path, "TestBatch48RegradeTransitionRetryAndFailuresDoNotCommit", "scenario", "regradeTransitionFailureCase")
reviewed.scenario_stage(path, "TestBatch48RegradeTransitionRetryAndFailuresDoNotCommit", "assertRegradeTransitionRetryAndFailure", "scenario regradeTransitionFailureCase", "scenario")

path = Path("services/api-gateway/internal/scorerelease/store_postgres_batch48_test.go")
reviewed.scenario_stage(path, "TestBatch48ReleaseDraftKeepsInsertArgumentsAndDatabaseResult", "assertReleaseDraftDatabaseOwnership", "source string", "source")

path = Path("services/api-gateway/internal/scorerelease/store_postgres_batch50_test.go")
reviewed.named_case(path, "TestBatch50PostgresStudentQuestionProjectionKeepsPolicyAndOwnedCopies", "tc", "studentQuestionProjectionCase")
reviewed.scenario_stage(path, "TestBatch50PostgresStudentQuestionProjectionKeepsPolicyAndOwnedCopies", "assertStudentQuestionVisibilityAndCopies", "tc studentQuestionProjectionCase, question ReleaseQuestion, presentation map[string]studentQuestionPresentation, geometry *StudentImageGeometry, ref *StudentQuestionReference", "tc, question, presentation, geometry, ref")

path = Path("services/api-gateway/internal/scorerelease/student_session_integration_test.go")
source = path.read_text(encoding="utf-8")
source = source.replace('"context"\n', '"context"\n\t"database/sql"\n', 1)
function = source.index("func TestStudentSessionProjectionWithPostgres(")
begin = source.index("\n\tfor _, statement := range []string{", function) + 1
end = source.index("\n\tresult, total, max, err :=", begin)
seed_body = source[begin:end]
read_begin = end + 1
read_end = source.index("\n}\n", read_begin)
read_body = source[read_begin:read_end]
source = source[:begin] + "\tfixture := seedStudentSessionProjection(t, ctx, db)\n\tassertStudentSessionProjection(t, ctx, db, fixture)" + source[read_end:]
table_end = seed_body.index("\n\ttenant, student, session :=")
table_body, seed_body = seed_body[:table_end], seed_body[table_end+1:]
seed_body = "\tcreateStudentSessionProjectionTables(t, ctx, db)\n" + seed_body
loop_begin = seed_body.index("\n\t\texamID, releaseID :=") + 1
loop_end = seed_body.index("\n\t}\n\t// 主科", loop_begin)
subject_body = "\n".join(line[1:] if line.startswith("\t") else line for line in seed_body[loop_begin:loop_end].splitlines())
seed_body = seed_body[:loop_begin] + "\t\tseedStudentSessionProjectionSubject(t, exec, subject, fixture, session)" + seed_body[loop_end:]
fixture_decl = "\tfixture := studentSessionProjectionFixture{tenant: tenant, student: student, foreign: foreign, exams: exams, releases: releases}\n"
seed_body = seed_body.replace("\texec := func(query string, args ...any) {", fixture_decl + "\texec := func(query string, args ...any) {", 1)
subject_body = subject_body.replace("\texams[subject], releases[subject] =", "\tfixture.exams[subject], fixture.releases[subject] =").replace("scope := tenant", "scope := fixture.tenant").replace("scope = foreign", "scope = fixture.foreign").replace("candidate := student", "candidate := fixture.student")
read_body = read_body.replace('ctx, tenant, exams["math"], student, releases["math"]', 'ctx, fixture.tenant, fixture.exams["math"], fixture.student, fixture.releases["math"]').replace('ctx, foreign, exams["math"], student, releases["math"]', 'ctx, fixture.foreign, fixture.exams["math"], fixture.student, fixture.releases["math"]')
source += "\ntype studentSessionProjectionFixture struct {\n\ttenant, student, foreign string\n\texams, releases map[string]string\n}\n"
source += "\nfunc createStudentSessionProjectionTables(t *testing.T, ctx context.Context, db *sql.DB) {\n\tt.Helper()\n" + table_body + "\n}\n"
source += "\n// 同时准备未发布、统计隐藏、小样本、其他学生和其他租户；最后切换当前发布指针。\nfunc seedStudentSessionProjection(t *testing.T, ctx context.Context, db *sql.DB) studentSessionProjectionFixture {\n\tt.Helper()\n" + seed_body + "\n\treturn fixture\n}\n"
source += "\nfunc seedStudentSessionProjectionSubject(t *testing.T, exec func(string, ...any), subject string, fixture studentSessionProjectionFixture, session string) {\n\tt.Helper()\n" + subject_body + "\n}\n"
source += "\n// 明确传入原冻结版本，零分、统计隐私及租户边界都必须来自该版本的事实。\nfunc assertStudentSessionProjection(t *testing.T, ctx context.Context, db *sql.DB, fixture studentSessionProjectionFixture) {\n\tt.Helper()\n" + read_body + "\n}\n"
# 场景辅助函数已经离开循环，跳过未发布科目应直接结束当前科目准备。
source = source.replace('if subject == "physics" {\n\t\tcontinue\n\t}', 'if subject == "physics" {\n\t\treturn\n\t}')
path.write_text(source, encoding="utf-8")

spec = spec_from_file_location("progress", ROOT / "go-record-progress.py")
progress = module_from_spec(spec)
spec.loader.exec_module(progress)
progress.record_batch("go-batch18", {
    "services/api-gateway/internal/regrade/service_test.go": "完整审读：提取批准、启动、候选与人工确认阶段；保留幂等先验、冻结来源分数、终结和后续发布计划断言。",
    "services/api-gateway/internal/regrade/store_postgres_batch48_test.go": "完整审读：提取重评状态重试和失败场景；保留行锁、写事件顺序及所有失败不提交断言。",
    "services/api-gateway/internal/scorerelease/store_postgres_batch48_test.go": "完整审读：按来源类型提取草稿写入场景，保留SQL占位、策略与数据库拥有的ID/时间/状态核验。",
    "services/api-gateway/internal/scorerelease/store_postgres_batch50_test.go": "完整审读：提取学生题目可见性和副本核验；保留共享几何引用、独立量规副本及后续答卷排序。",
    "services/api-gateway/internal/scorerelease/student_session_integration_test.go": "完整审读：拆分最小表、分科场景准备与冻结版本查询校验，保持连接清理生命周期、真实SQL和跨租户/统计隐私全部断言。",
}, 5)
