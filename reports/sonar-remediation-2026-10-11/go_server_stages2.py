from pathlib import Path
import re,json
root=Path('D:/project/yuejuan');report=root/'reports/sonar-remediation-2026-10-11'
source=(report/'go_server_stages1.py').read_text(encoding='utf-8')
ns={};exec(source[:source.index("path='services/")],ns)
function=ns['function'];replace_function=ns['replace_function'];save=ns['save'];mask_go=ns['mask_go']
functions=[]
def apply(path,name,transform):
 s=(root/path).read_text(encoding='utf-8');_,_,body=function(s,name);s=transform(s,body)
 save(path,s);functions.append({'file':path,'function':name})
def callback(s,body,label,helper,params,args,comment,type_declaration=''):
 # 原 t.Run 保留；仅移动同一个子测试内部的具名业务场景。
 start=body.index('func(t *testing.T) {',body.index(label));opening=body.index('{',start);masked=mask_go(body);depth=1;i=opening+1
 while depth:
  if masked[i]=='{':depth+=1
  elif masked[i]=='}':depth-=1
  i+=1
 extracted=re.sub(r'^\t\t','',body[opening+1:i-1],flags=re.M)
 newbody=body[:opening+1]+f'\n\t\t\t{helper}({args})\n\t\t'+body[i-1:]
 helpers=f'\n\n{type_declaration}// {comment}\nfunc {helper}({params}) {{\n\tt.Helper()'+extracted+'}'
 return newbody,helpers

path='services/api-gateway/internal/server/score_route_test.go';name='TestScoreRoutesFinalizeConfirmPublishStudentLookupAndExport'
def score(s,body):
 start=body.index('\n\treq := reviewAuthedRequest(');end=body.index('\n\treq = reviewAuthedRequest(http.MethodPost, "/api/v1/exams/exam-1/confirm-grades"')
 early=body[start:end];tail=body[end:].replace('\n\treq =','\n\treq :=',1).replace('\n\trec =','\n\trec :=',1)
 body=body[:start]+'\n\tscoreAssertUnconfirmedPublicationBlocked(t, router, token)\n'+tail
 helper='\n\n// 先形成待确认成绩，再验证质量门禁与发布均拒绝；此阶段不能跳过直接确认。\nfunc scoreAssertUnconfirmedPublicationBlocked(t *testing.T, router http.Handler, token string) {\n\tt.Helper()'+early+'}'
 return replace_function(s,name,body,helper)
apply(path,name,score)

path='services/api-gateway/internal/server/readers_batch47_test.go';name='TestBatch47OnboardingOrganizationFallbackPreservesScope'
def readiness(s,body):
 start=body.index('[]struct {');end=body.index('\n\t}{',start)
 fields=body[start+len('[]struct {'):end]
 body=body[:start]+'[]batch47OrganizationScopeCase{'+body[end+len('\n\t}{'):]
 declaration='type batch47OrganizationScopeCase struct {'+fields+'\n}\n\n'
 body,helper=callback(s,body,'t.Run(test.name','assertBatch47OrganizationScopeCase','t *testing.T, test batch47OrganizationScopeCase','t, test','专用 readiness 缺席时，列表回退仍须保留租户、上下文及学校白名单。',declaration)
 return replace_function(s,name,body,helper)
apply(path,name,readiness)

path='services/api-gateway/internal/server/platform_schools_route_test.go';name='TestPlatformSchoolRoutesRequirePlatformRoleAndTenantManage'
def platform(s,body):
 start=body.index('[]struct {');end=body.index('\n\t}{',start);fields=body[start+len('[]struct {'):end]
 body=body[:start]+'[]platformSchoolRoleCase{'+body[end+len('\n\t}{'):]
 declaration='type platformSchoolRoleCase struct {'+fields+'\n}\n\n'
 body,helper=callback(s,body,'t.Run(tc.name','assertPlatformSchoolRoleCase','t *testing.T, paths []string, schoolTenantID string, tc platformSchoolRoleCase','t, paths, schoolTenantID, tc','平台角色、平台租户和 tenant:manage 必须同时满足；逐条原路由保留拒绝状态检查。',declaration)
 return replace_function(s,name,body,helper)
apply(path,name,platform)

path='services/api-gateway/internal/server/server_test.go';name='TestOCRAvailabilityUsesScopedPermissionsAndResponse'
def ocr(s,body):
 body,helper=callback(s,body,'t.Run(permission','assertOCRAvailabilityForPermission','t *testing.T, permission string','t, permission','OCR 消费权限只返回生成时间和 worker 字段，不得扩展到完整系统状态。')
 return replace_function(s,name,body,helper)
apply(path,name,ocr)

path='services/api-gateway/internal/server/e2e_math_rubric_score_test.go';name='TestPostgresMathRubricScoreUsesOnlyFrozenExamRubric'
def frozen(s,body):
 start=body.index('\n\tif first.VerifiedScore');end=body.index('\n\tif _, err = db.ExecContext',start)
 guard=body[start:end].replace('first.', 'preview.').replace('first)', 'preview)')
 body=body[:start]+'\n\tassertFrozenRubricPreviewAuthority(t, first)'+body[end:]
 helper='\n\n// 预览仍只是人工复核建议：成绩、快照哈希与版本必须全部来自冻结量规。\nfunc assertFrozenRubricPreviewAuthority(t *testing.T, preview mathunderstanding.RubricScore) {\n\tt.Helper()'+guard+'\n}'
 return replace_function(s,name,body,helper)
apply(path,name,frozen)

path='services/api-gateway/internal/server/e2e_math_effective_evidence_test.go';name='TestPostgresMathEffectiveEvidenceProjection'
def correction(s,body):
 start=body.index('\n\tresults := make(chan error');end=body.index('\n\tcontract.SolutionGraph.Steps[0].NormalizedText = "latest correction',start)
 phase=body[start:end]
 body=body[:start]+'\n\tassertConcurrentMathCorrectionRevision(t, ctx, corrections, tenantID, artifactID, actorID, input)'+body[end:]
 helper='\n\n// 两个请求共用预期修订号，等待全部写入结束后必须只有一次提交和一次版本冲突。\nfunc assertConcurrentMathCorrectionRevision(t *testing.T, ctx context.Context, corrections *mathunderstanding.PostgresCorrectionStore, tenantID, artifactID, actorID string, input mathunderstanding.CreateCorrectionInput) {\n\tt.Helper()'+phase+'\n}'
 return replace_function(s,name,body,helper)
apply(path,name,correction)

path='services/api-gateway/internal/server/e2e_idempotency_test.go';name='TestRequestIdempotencyWithPostgresTestDatabase'
def owner(s,body):
 start=body.index('\n\tstart := make(chan struct{})');end=body.index('\n\tbody := []byte(',start)
 phase=body[start:end]
 body=body[:start]+'\n\tassertConcurrentIdempotencyOwnership(t, ctx, store, input)\n'+body[end:]
 helper='\n\n// 完成回执前同时抢占同一请求，须只有一个执行者；等待 goroutine 结束后才读取结果。\nfunc assertConcurrentIdempotencyOwnership(t *testing.T, ctx context.Context, store *idempotency.PostgresStore, input idempotency.BeginInput) {\n\tt.Helper()'+phase+'\n}'
 return replace_function(s,name,body,helper)
apply(path,name,owner)

path='services/api-gateway/internal/server/e2e_scoring_command_test.go';name='TestScoringCommandRecoveryWithPostgresTestDatabase'
def scoring(s,body):
 start=body.index('\n\thandler := grading.NewHandler(');end=body.index('\n\tif _, err = db.Exec(',start)
 phase=body[start:end].replace('first.run.ID','runID')
 body=body[:start]+'\n\tassertScoringCommandTransportRecovery(t, store, fixture, command, first.run.ID)'+body[end:]
 helper='\n\n// 故意只破坏传输回执的第一次完成写入；重试必须接回已成功的业务评分运行。\nfunc assertScoringCommandTransportRecovery(t *testing.T, store *grading.PostgresStore, fixture story056AcceptanceFixture, command, runID string) {\n\tt.Helper()'+phase+'\n}'
 return replace_function(s,name,body,helper)
apply(path,name,scoring)

path='services/api-gateway/internal/server/e2e_story060_provision_test.go';name='TestStory060ProvisioningWithPostgresIsScopedAndIdempotent'
def provision(s,body):
 start=body.index('[]struct {');end=body.index('\n\t}{',start);fields=body[start+len('[]struct {'):end]
 body=body[:start]+'[]story060ProvisionCase{'+body[end+len('\n\t}{'):]
 declaration='type story060ProvisionCase struct {'+fields+'\n}\n\n'
 body,helper=callback(s,body,'t.Run(tt.roleCode','assertStory060ProvisionCase','t *testing.T, ctx context.Context, db *sql.DB, store *auth.PostgresStore, tt story060ProvisionCase','t, ctx, db, store, tt','第二次开通只更新同一用户及角色关系；服务范围和学校范围保持各自原值。',declaration)
 s=replace_function(s,name,body,helper);return s.replace('"context"','"context"\n\t"database/sql"',1)
apply(path,name,provision)
(report/'go-server-stages2.json').write_text(json.dumps({'files':ns['changed'],'issue_functions':functions},ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
print(json.dumps(functions))
