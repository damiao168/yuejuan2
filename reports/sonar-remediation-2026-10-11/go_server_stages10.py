from pathlib import Path
import json
root=Path('D:/project/yuejuan');report=root/'reports/sonar-remediation-2026-10-11'
source=(report/'go_server_stages2.py').read_text(encoding='utf-8');ns={};exec(source[:source.index("\npath='services/")],ns)
function=ns['function'];replace_function=ns['replace_function'];apply=ns['apply'];callback=ns['callback']
path='services/api-gateway/internal/server/e2e_math_grading_binding_test.go';name='TestPostgresMathGradingBindingsRoundTripAndRejectWrongVersion'
def bindings(s,body):
 history=body.index('\n\t// 已分配任务的读取投影');batch=body.index('\n\t// 恢复已有批次运行');invalid=body.index('\n\tfor _, scenario := range')
 history_phase=body[history:batch].replace('artifact.ID','gradeInput.MathArtifactID').replace('artifact.Version','gradeInput.MathArtifactVersion')
 batch_phase=body[batch:invalid]
 body=body[:history]+'\n\tbinding := mathGradingBindingContext{DB: db, Store: store, Fixture: fixture}\n\te2eAssertMathBindingReviewHistory(t, ctx, binding, gradeInput, grade, suffix)\n\te2eAssertMathBindingBatchRecovery(t, ctx, binding, input, suffix)'+body[invalid:]
 body,invalid_helper=callback(s,body,'t.Run(scenario','e2eAssertRejectedMathBinding','t *testing.T, ctx context.Context, binding mathGradingBindingContext, input subjective.CreateRunInput, gradeInput subjective.Grade, otherSegmentID, scenario string','t, ctx, binding, input, gradeInput, otherSegmentID, scenario','错误版本、错误答案、缺失产物和不完整绑定分别核对对应数据库约束，不能只断言任意错误。')
 invalid_helper=invalid_helper.replace('\n\tt.Helper()','\n\tt.Helper()\n\tstore, fixture := binding.Store, binding.Fixture',1)
 helpers='\n\ntype mathGradingBindingContext struct {\n\tDB *sql.DB\n\tStore *subjective.PostgresStore\n\tFixture story056AcceptanceFixture\n}\n\n// 指定复核任务的历史投影仍保留完整数学身份、修订号和最新到最旧的次序。\nfunc e2eAssertMathBindingReviewHistory(t *testing.T, ctx context.Context, binding mathGradingBindingContext, gradeInput, grade subjective.Grade, suffix string) {\n\tt.Helper()\n\tdb, store, fixture := binding.DB, binding.Store, binding.Fixture\n\tsegmentID := gradeInput.AnswerSegmentID'+history_phase+'\n}'
 helpers+='\n\n// 重排批次时使用原运行冻结的绑定，不能替换为更高修订号或未来评分器。\nfunc e2eAssertMathBindingBatchRecovery(t *testing.T, ctx context.Context, binding mathGradingBindingContext, input subjective.CreateRunInput, suffix string) {\n\tt.Helper()\n\tstore, fixture := binding.Store, binding.Fixture\n\tsegmentID := input.AnswerSegmentID'+batch_phase+'\n}'+invalid_helper
 s=replace_function(s,name,body,helpers);return s.replace('"context"','"context"\n\t"database/sql"',1)
apply(path,name,bindings)

path='services/api-gateway/internal/server/e2e_story060_roster_test.go';name='TestStory060FiveHundredStudentRosterScaleE2EWithPostgresTestDatabase'
def scale(s,body):
 start=body.index('\n\tconst rosterReadBudget');missing=body.index('\n\trows, err := db.Query(',start)
 measure=body[start:missing];reconcile=body[missing:].replace('\n\troster =','\n\troster :=',1).replace('\n\tsummary =','\n\tsummary :=',1).replace('\n\tquality =','\n\tquality :=',1)
 body=body[:start]+'\n\te2eAssertStory060RosterReadBudget(t, db, router, adminToken, tenantID, examID)\n\te2eResolveStory060ScaleAbsences(t, db, router, adminToken, story060ScaleRosterScope{TenantID: tenantID, ClassID: classID, ExamID: examID, Suffix: suffix})\n'
 helpers='\n\n// 同一批 500 名考生分别走存储和 HTTP 路径，原 5 秒预算从各自调用前开始计时。\nfunc e2eAssertStory060RosterReadBudget(t *testing.T, db *sql.DB, router http.Handler, adminToken, tenantID, examID string) {\n\tt.Helper()'+measure+'\n}'
 helpers+='\n\ntype story060ScaleRosterScope struct {\n\tTenantID, ClassID, ExamID, Suffix string\n}\n\n// 原来缺失的三份答卷必须逐人明确缺考，之后才允许质量通过及发布。\nfunc e2eResolveStory060ScaleAbsences(t *testing.T, db *sql.DB, router http.Handler, adminToken string, scope story060ScaleRosterScope) {\n\tt.Helper()\n\ttenantID, classID, examID, suffix := scope.TenantID, scope.ClassID, scope.ExamID, scope.Suffix'+reconcile+'}'
 s=replace_function(s,name,body,helpers);return s.replace('"fmt"','"database/sql"\n\t"fmt"',1)
apply(path,name,scale)
(report/'go-server-stages10.json').write_text(json.dumps({'files':ns['ns']['changed'],'issue_functions':ns['functions'],'checkpoint_relation':'after checkpoint02; requires subsequent scan'},ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
print(json.dumps(ns['functions']))
