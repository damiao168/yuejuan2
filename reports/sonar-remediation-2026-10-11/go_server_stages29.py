from pathlib import Path
import json
root=Path('D:/project/yuejuan');report=root/'reports/sonar-remediation-2026-10-11'
source=(report/'go_server_stages2.py').read_text(encoding='utf-8');ns={};exec(source[:source.index("\npath='services/")],ns)
replace_function=ns['replace_function'];apply=ns['apply']
path='services/api-gateway/internal/server/e2e_anonymous_paper_test.go'
def anonymous(s,body):
 source=body.index('\n\tsourceBytes, imageData :=');published=body.index('\n\tif _, err := service.Publish',source);view=body.index('\n\tstudentView, err :=',published)
 sourcephase=body[source:published]+'\n\treturn sourceAsset.ID, imageData'
 auth=body.index('\n\t// 普通页面路由');revoke=body.index('\n\tif err := service.RevokeHighScorePaper',auth)
 authphase=body[auth:revoke].replace('sourceAsset.ID','identity.SourceAssetID')
 body=body[:source]+'\n\tsourceAssetID, imageData := e2eSeedAnonymousRegisteredPage(t, ctx, db, fileStore, objects, f, top)'+body[published:auth]+'\n\te2eAssertAnonymousSourceAuthorization(t, ctx, db, router, store, anonymousSourceIdentity{Fixture: f, Top: top, Other: other, QuestionID: questionID, SourceAssetID: sourceAssetID, StudentToken: studentToken})'+body[revoke:]
 helpers='\n\n// 合成来源先完成身份区域模板和配准，只有这个已核对来源才能参与匿名页面发布。\nfunc e2eSeedAnonymousRegisteredPage(t *testing.T, ctx context.Context, db *sql.DB, fileStore *files.PostgresStore, objects *files.MemoryObjectStorage, f story056AcceptanceFixture, top repairAnswer) (string, *image.RGBA) {\n\tt.Helper()'+sourcephase+'\n}'
 helpers+='''

type anonymousSourceIdentity struct {
 Fixture story056AcceptanceFixture
 Top, Other repairAnswer
 QuestionID, SourceAssetID, StudentToken string
}

// 普通页面仍只解析本人答案；匿名分享不能借用原文件下载路由越过同学间隔离。
func e2eAssertAnonymousSourceAuthorization(t *testing.T, ctx context.Context, db *sql.DB, router http.Handler, store *scorerelease.PostgresStore, identity anonymousSourceIdentity) {
 t.Helper()
 f, top, other, questionID, studentToken := identity.Fixture, identity.Top, identity.Other, identity.QuestionID, identity.StudentToken
'''+authphase+'\n}'
 return replace_function(s,'TestHighScorePaperShareRedactsAndRevokesWithPostgresTestDatabase',body,helpers).replace('"context"','"context"\n\t"database/sql"',1)
apply(path,'TestHighScorePaperShareRedactsAndRevokesWithPostgresTestDatabase',anonymous)

path='services/api-gateway/internal/server/e2e_math_rubric_score_test.go'
def math_preview(s,body):
 live=body.index('\n\tif _, err = db.ExecContext(ctx, `UPDATE question_rubric');correct=body.index('\n\t_, err = corrections.CreateCorrection',live)
 livephase=body[live:correct].replace('if _, err =','if _, err :=')
 correctionphase=body[correct:].replace('\n\t_, err = corrections.CreateCorrection','\n\t_, err := corrections.CreateCorrection',1)
 body=body[:live]+'''
 preview := mathRubricPreviewScenario{Tenant: tenant, Segment: segment, Artifact: artifact, Actor: actor,
  Contract: contract, Lookup: lookup, Get: get, AssertScore: assertScore, First: first}
 e2eAssertLiveRubricAndScopeFenced(t, ctx, db, store, preview)
 e2eAssertPendingMathCorrectionHasNoGradeWrites(t, ctx, db, corrections, preview)
'''
 helpers='''

type mathRubricPreviewScenario struct {
 Tenant, Segment, Artifact, Actor string
 Contract mathunderstanding.CreateArtifactInput
 Lookup *mathScoringAssignment
 Get func(string) *httptest.ResponseRecorder
 AssertScore func(*httptest.ResponseRecorder) mathunderstanding.RubricScore
 First mathunderstanding.RubricScore
}

// 活动量规变化不得影响冻结得分；跨租户、未派发及错误快照身份仍分别拒绝。
func e2eAssertLiveRubricAndScopeFenced(t *testing.T, ctx context.Context, db *sql.DB, store *mathunderstanding.PostgresStore, preview mathRubricPreviewScenario) {
 t.Helper()
 tenant, segment, lookup, get, assertScore, first := preview.Tenant, preview.Segment, preview.Lookup, preview.Get, preview.AssertScore, preview.First
'''+livephase+'\n}'
 helpers+='''

// 未验证的新人工修订不能复用旧验证得分；预览整个过程中不得写入 AI 或最终成绩链。
func e2eAssertPendingMathCorrectionHasNoGradeWrites(t *testing.T, ctx context.Context, db *sql.DB, corrections *mathunderstanding.PostgresCorrectionStore, preview mathRubricPreviewScenario) {
 t.Helper()
 tenant, artifact, actor, contract, get, assertScore := preview.Tenant, preview.Artifact, preview.Actor, preview.Contract, preview.Get, preview.AssertScore
'''+correctionphase+'\n}'
 return replace_function(s,'TestPostgresMathRubricScoreUsesOnlyFrozenExamRubric',body,helpers)
apply(path,'TestPostgresMathRubricScoreUsesOnlyFrozenExamRubric',math_preview)

path='services/api-gateway/internal/server/e2e_reliability_command_test.go'
def migration(s,body):
 start=body.index('\n\tgradeID, academicYearID, cohortID :=');scope=body.index('\n\tscenario := reliabilityMigrationScenario',start)
 seedphase=body[start:scope]+'\n\treturn examCommandID, examSession, transportRequestHash'
 body=body[:start]+'\n\texamCommandID, examSession, transportRequestHash := e2eSeedMigrationReplayCommand(t, db, tenantID, userID, schoolID)'+body[scope:]
 helpers='\n\n// 全部迁移完成后才种下可恢复的新业务事实及未完成传输收据，旧历史的验证规则仍独立核对。\nfunc e2eSeedMigrationReplayCommand(t *testing.T, db *sql.DB, tenantID, userID, schoolID string) (string, exam.ExamSession, string) {\n\tt.Helper()'+seedphase+'\n}'
 return replace_function(s,'TestReliabilityMigrationFrom000122PostgresTestDatabase',body,helpers)
apply(path,'TestReliabilityMigrationFrom000122PostgresTestDatabase',migration)
(report/'go-server-stages29.json').write_text(json.dumps({'files':ns['ns']['changed'],'issue_functions':ns['functions'],'checkpoint_relation':'after checkpoint02; requires subsequent scan'},ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
print(json.dumps(ns['functions']))
