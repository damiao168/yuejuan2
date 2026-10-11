from pathlib import Path
import hashlib,json,re,os,tempfile
root=Path('D:/project/yuejuan'); report=root/'reports/sonar-remediation-2026-10-11'
# 复用词法定位函数而不执行上个批次的修改入口。
previous=(report/'go_server_parameters.py').read_text(encoding='utf-8')
namespace={};exec(previous[previous.index('def mask_go'):previous.index('specs=[')],namespace)
mask_go=namespace['mask_go']
changed=[]
def function(s,name):
 m=re.search(r'^func '+name+r'\(',s,re.M);a=m.start();masked=mask_go(s);opening=masked.index('{',a);depth=1;i=opening+1
 while depth:
  if masked[i]=='{':depth+=1
  elif masked[i]=='}':depth-=1
  i+=1
 return a,i,s[opening+1:i-1]
def replace_function(s,name,body,helpers):
 a,b,old=function(s,name);opening=s.index('{',a)
 return s[:opening+1]+body+'}'+helpers+s[b:]
def save(path,s):
 p=root/path;raw=p.read_bytes();backup=Path('D:/tools/sonar-remediation-20261011/server-extra-baseline')/path
 if not backup.exists():backup.parent.mkdir(parents=True,exist_ok=True);backup.write_bytes(raw)
 # Windows 的索引/实时扫描可能暂时阻止重新打开原文件；先完成临时文件再原子替换。
 with tempfile.NamedTemporaryFile(dir=p.parent,suffix='.sonar-edit',delete=False) as f:
  staged=Path(f.name);f.write(s.encode('utf-8'))
 os.replace(staged,p)
 changed.append({'file':path,'sha256':hashlib.sha256(p.read_bytes()).hexdigest(),'backup':str(backup)})

path='services/api-gateway/internal/server/appeal_route_test.go';s=(root/path).read_text(encoding='utf-8')
name='TestAppealTeacherOnlyWorksAssignedAppealsAndCannotFinalize';_,_,body=function(s,name)
start=body.index('\n\treq = appealAuthedRequest(http.MethodGet, "/api/v1/appeals", "", teacherToken)')
teacher=body.index('\n\treq = appealAuthedRequest(http.MethodGet, "/api/v1/appeals", "", teacherToken)',start+1)
original=body.index('\n\treq = appealAuthedRequest(http.MethodGet, "/api/v1/appeals", "", originalGraderToken)',teacher)
assignment=body[start:teacher].replace('\n\treq =','\n\treq :=',1).replace('\n\trec =','\n\trec :=',1)
recommendation=body[teacher:original].replace('\n\treq =','\n\treq :=',1).replace('\n\trec =','\n\trec :=',1)
helpers='\n\n// 未派发时不可读，原阅卷员不可接回申诉；拒绝请求不能推进 expected_revision。\nfunc appealAssertAssignmentBoundaries(t *testing.T, router http.Handler, appealID, teacherToken, adminToken, tenantAdminToken string) {\n\tt.Helper()'+assignment+'}\n\n// 教师只看匿名证据并提出建议；建议提交后仍不能关闭申诉或修改最终分数。\nfunc appealAssertTeacherRecommendationBoundaries(t *testing.T, router http.Handler, appealID, teacherToken string) {\n\tt.Helper()'+recommendation+'}'
body=body[:start]+'\n\tappealAssertAssignmentBoundaries(t, router, appealID, teacherToken, adminToken, tenantAdminToken)\n\tappealAssertTeacherRecommendationBoundaries(t, router, appealID, teacherToken)\n'+body[original:]
s=replace_function(s,name,body,helpers);save(path,s)

path='services/api-gateway/internal/server/e2e_business_command_test.go';s=(root/path).read_text(encoding='utf-8')
name='e2eArbitrationAndExportCommands';_,_,body=function(s,name)
export=body.index('\n\texports := report.NewPostgresStore(db)');snapshot=body.index('\n\te2eReportExportUsesSingleSnapshot(')
start=body.index('\n\tctx := context.Background()');arbitration=body[start:export]
exports=body[export:snapshot].replace('if _, err = db.Exec(', 'if _, err := db.Exec(',1)
main='\n\tt.Helper()\n\ttenant, exam, arbitrator := fixture.Tenant, fixture.Exam, fixture.Arbitrator\n\te2eArbitrationReceiptAfterDeletion(t, db, fixture)\n\te2eConcurrentExportReceipts(t, db, fixture)'+body[snapshot:]
helpers='\n\n// 双评产生仲裁后先提交，再删除任务；同一命令仍须重放原成绩并拒绝改分。\nfunc e2eArbitrationReceiptAfterDeletion(t *testing.T, db *sql.DB, fixture arbitrationCommandFixture) {\n\tt.Helper()\n\ttenant, exam, segment, firstActor, secondActor, arbitrator := fixture.Tenant, fixture.Exam, fixture.Segment, fixture.FirstActor, fixture.SecondActor, fixture.Arbitrator'+arbitration+'}\n\n// 并发共用命令键；收齐两份响应并恢复连接上限后，才验证删除后的回执重放。\nfunc e2eConcurrentExportReceipts(t *testing.T, db *sql.DB, fixture arbitrationCommandFixture) {\n\tt.Helper()\n\ttenant, exam, arbitrator := fixture.Tenant, fixture.Exam, fixture.Arbitrator\n\tctx := context.Background()'+exports+'}'
s=replace_function(s,name,main,helpers);save(path,s)

path='services/api-gateway/internal/server/e2e_story056_test.go';s=(root/path).read_text(encoding='utf-8')
name='e2eCompleteStory056HumanReviews';_,_,body=function(s,name)
start=body.index('\n\tfor index := 0; index < count; index++ {');loop=body[start:]
assert loop.endswith('\n\t}\n')
one=loop[loop.index('{')+1:loop.rfind('\n\t}')]
one=re.sub(r'^\t\t','\t',one,flags=re.M)
main=body[:start].replace('adminToken, graderToken, graderID := actors.AdminToken, actors.GraderToken, actors.GraderID','adminToken, graderID := actors.AdminToken, actors.GraderID')+'\n\tfor index := 0; index < count; index++ {\n\t\te2eCompleteStory056AssignedHumanReview(t, db, router, actors, fixture)\n\t}\n'
helpers='\n\n// 每次领取后立即提交并检查数据库；已提交任务被退回的拒绝响应不能改动成绩或进度。\nfunc e2eCompleteStory056AssignedHumanReview(t *testing.T, db *sql.DB, router http.Handler, actors story056ReviewActors, fixture story056AcceptanceFixture) {\n\tt.Helper()\n\tadminToken, graderToken := actors.AdminToken, actors.GraderToken'+one+'\n}'
s=replace_function(s,name,main,helpers);save(path,s)
(report/'go-server-stages1.json').write_text(json.dumps({'files':changed,'issue_functions':[name,'e2eArbitrationAndExportCommands','TestAppealTeacherOnlyWorksAssignedAppealsAndCannotFinalize']},ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
print(json.dumps(changed,ensure_ascii=False))
