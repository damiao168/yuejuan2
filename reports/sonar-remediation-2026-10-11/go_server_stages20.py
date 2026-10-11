from pathlib import Path
import json,re
root=Path('D:/project/yuejuan');report=root/'reports/sonar-remediation-2026-10-11'
source=(report/'go_server_stages2.py').read_text(encoding='utf-8');ns={};exec(source[:source.index("\npath='services/")],ns)
replace_function=ns['replace_function'];apply=ns['apply'];mask_go=ns['mask_go']
path='services/api-gateway/internal/server/e2e_review_repairs_test.go'
fields={'db':'DB','svc':'Service','reader':'Reader','trace':'Trace','f':'Acceptance','answers':'Answers'}

def helper(name,phase,comment,args='',output=''):
 used=[(key,value) for key,value in fields.items() if re.search(r'\b'+key+r'\b',mask_go(phase))]
 aliases='\n\t'+', '.join(key for key,_ in used)+' := '+', '.join('fixture.'+field for _,field in used) if used else ''
 return '\n\n// '+comment+'\nfunc '+name+'(t *testing.T, ctx context.Context, fixture releaseStudentReadFixture'+args+')'+output+' {\n\tt.Helper()'+aliases+phase+'\n}'

def student(s,body):
 regrade=body.index('\n\tmakeDraft :=');conn=body.index('\n\tconn, err :=')
 unsafe=body.index('\n\t// 启用的草稿');legacy=body.index('\n\t// 旧版标记')
 unpublished=body.index('\n\tvar unpublishedQuestionID')
 question=body.index('\n\tquestionID := f.QuestionIDs',unpublished)
 totals=body.index('\n\ttotals, err :=')
 regrade_phase=body[regrade:conn]
 unsafe_phase=body[unsafe:legacy]+'\n\treturn all.Items[1].StudentID'
 unpublished_phase=body[unpublished:question]+'\n\treturn unpublishedQuestionID'
 # 原查询结果变量只用于该调用，不依赖前一学生的结果。
 totals_phase=body[totals:].replace('\n\tresult, err = reader.StudentResult','\n\tresult, err := reader.StudentResult',1)
 retained=body[legacy:unpublished].replace('\n\totherStudent := all.Items[1].StudentID','').replace('\n\tresult, err = reader.StudentResult','\n\tresult, err = reader.StudentResult',1)
 body=body[:regrade]+'\n\te2eAssertRegradeSourceAndReceipt(t, ctx, svc, f, answers, base)'+body[conn:unsafe]+'\n\tfixture := releaseStudentReadFixture{DB: db, Service: svc, Reader: reader, Trace: trace, Acceptance: f, Answers: answers}\n\totherStudent := e2ePrepareLegacyUnsafeAnalyticsRelease(t, ctx, fixture)'+retained+'\n\tunpublishedQuestionID := e2eAssertUnpublishedQuestionIsolation(t, ctx, fixture)'+body[question:totals]+'\n\te2eAssertTotalsOnlyStudentRead(t, ctx, fixture, unpublishedQuestionID)\n'
 helpers='\n\ntype releaseStudentReadFixture struct {\n\tDB *sql.DB\n\tService *scorerelease.Service\n\tReader *scorerelease.PostgresStore\n\tTrace *repairQuestionTracer\n\tAcceptance story056AcceptanceFixture\n\tAnswers []repairAnswer\n}'
 helpers+='\n\n// 同一正式版本产生两个更正草稿；第一个发布后第二个来源已过期，但首个发布重放仍成功。\nfunc e2eAssertRegradeSourceAndReceipt(t *testing.T, ctx context.Context, svc *scorerelease.Service, f story056AcceptanceFixture, answers []repairAnswer, base scorerelease.Release) {\n\tt.Helper()\n\tvar err error'+regrade_phase+'\n}'
 helpers+=helper('e2ePrepareLegacyUnsafeAnalyticsRelease',unsafe_phase,'先验证服务和存储均拒绝未准备的匿名分享，再用明确的旧版发布夹具核对后续学生投影。',output=' string')
 helpers+=helper('e2eAssertUnpublishedQuestionIsolation',unpublished_phase,'冻结后新增题目即使产生分段和终评，也不进入正式题目清单；整页图仍需发布关联才可读取。',output=' string')
 helpers+=helper('e2eAssertTotalsOnlyStudentRead',totals_phase,'仅总分版本既不能混入未发布题目，也不能查询或返回题目明细。',args=', unpublishedQuestionID string')
 return replace_function(s,'TestReviewRepairsReleaseAndStudentReadWithPostgresTestDatabase',body,helpers)
apply(path,'TestReviewRepairsReleaseAndStudentReadWithPostgresTestDatabase',student)

def feedback(s,body):
 detail=body.index('\n\tdetail, err := svc.Get')
 publish=body.index('\n\tif _, err := svc.Publish',detail)
 read=body.index('\n\tresult, err := svc.StudentResult',publish)
 snapshot=body[detail:publish]
 published=body[read:]
 reportstart=published.index('\n\t\t\tstudentReport, reportErr :=')
 returnstart=published.index('\n\t\t\treturn',reportstart)
 report_phase=published[reportstart:returnstart]
 published=published[:reportstart]+'\n\t\t\te2eAssertPublishedArbitrationReport(t, ctx, db, f, answers[0].student, questionID, question.Score)'+published[returnstart:]
 snapshot=snapshot.replace('release.ID','identity.ReleaseID').replace('questionID','identity.QuestionID').replace('arbitrationID','identity.ArbitrationID')
 body=body[:detail]+'\n\tidentity := releaseArbitrationIdentity{ReleaseID: release.ID, QuestionID: questionID, ArbitrationID: arbitrationID}\n\te2eAssertArbitrationReleaseSnapshot(t, ctx, svc, f, answers, identity)'+body[publish:read]+'\n\te2eAssertPublishedArbitrationStudentRead(t, ctx, db, svc, f, answers, questionID)\n'
 helpers='\n\ntype releaseArbitrationIdentity struct {\n\tReleaseID, QuestionID, ArbitrationID string\n}\n\n// 发布草稿只选最终仲裁的来源和反馈，不能取首轮已过时的批语。\nfunc e2eAssertArbitrationReleaseSnapshot(t *testing.T, ctx context.Context, svc *scorerelease.Service, f story056AcceptanceFixture, answers []repairAnswer, identity releaseArbitrationIdentity) {\n\tt.Helper()'+snapshot+'\n}'
 helpers+='\n\n// 发布后修改源任务批语，学生仍读取原冻结反馈；找到目标题目后才继续核对报告。\nfunc e2eAssertPublishedArbitrationStudentRead(t *testing.T, ctx context.Context, db *sql.DB, svc *scorerelease.Service, f story056AcceptanceFixture, answers []repairAnswer, questionID string) {\n\tt.Helper()'+published+'\n}'
 report_phase=report_phase.replace('answers[0].student','studentID').replace('question.Score','expectedScore')
 helpers+='\n\n// 报告题目必须与正式版本同分，且仅包含一条冻结仲裁反馈；没有目标题目时保留 Fatal。\nfunc e2eAssertPublishedArbitrationReport(t *testing.T, ctx context.Context, db *sql.DB, f story056AcceptanceFixture, studentID, questionID string, expectedScore float64) {\n\tt.Helper()'+report_phase+'\n}'
 return replace_function(s,'TestReleaseFeedbackUsesFinalArbitrationAndFreezesIt',body,helpers)
apply(path,'TestReleaseFeedbackUsesFinalArbitrationAndFreezesIt',feedback)
(report/'go-server-stages20.json').write_text(json.dumps({'files':ns['ns']['changed'],'issue_functions':ns['functions'],'checkpoint_relation':'after checkpoint02; requires subsequent scan'},ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
print(json.dumps(ns['functions']))
