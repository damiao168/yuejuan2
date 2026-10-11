from pathlib import Path
import json
root=Path('D:/project/yuejuan');report=root/'reports/sonar-remediation-2026-10-11'
source=(report/'go_server_stages2.py').read_text(encoding='utf-8');ns={};exec(source[:source.index("\npath='services/")],ns)
replace_function=ns['replace_function'];apply=ns['apply']
path='services/api-gateway/internal/server/review_route_test.go'

def helper(name,params,body,comment,result=''):
 body=body.replace('\n\treq =','\n\treq :=',1).replace('\n\trec =','\n\trec :=',1)
 return '\n\n// '+comment+'\nfunc '+name+'('+params+')'+result+' {\n\tt.Helper()'+body+'\n}'

def assigned(s,body):
 start=body.index('\n\treq := reviewAuthedRequest(http.MethodGet, "/api/v1/review-tasks",')
 drafts=body.index('\n\treq = reviewAuthedRequest(http.MethodGet, "/api/v1/review-tasks/"+firstID+"/draft",')
 submit=body.index('\n\treq = reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks/"+firstID+"/submit",')
 visibility=body[start:drafts];authority=body[drafts:submit]
 tail=body[submit:].replace('\n\treq =','\n\treq :=',1).replace('\n\trec =','\n\trec :=',1)
 body=body[:start]+'\n\treviewAssertAssignedTaskVisibility(t, router, firstID, secondID, graderToken, teacherToken)\n\treviewAssertDraftAndImageAuthority(t, router, firstID, graderToken, teacherToken, managerToken, evidenceOnlyToken)'+tail
 helpers=helper('reviewAssertAssignedTaskVisibility','t *testing.T, router http.Handler, firstID, secondID, graderToken, teacherToken string',visibility,'任务列表、读取和派发分别核对已分配范围；教师的管理权限仍不能绕过任务归属。')
 helpers+=helper('reviewAssertDraftAndImageAuthority','t *testing.T, router http.Handler, firstID, graderToken, teacherToken, managerToken, evidenceOnlyToken string',authority,'管理者不能读写他人的草稿；通用证据入口和带身份的原图分别按原权限断言，不把 404 当作内容存在。')
 return replace_function(s,'TestReviewWorkPermissionIsScopedToAssignedTasks',body,helpers)
apply(path,'TestReviewWorkPermissionIsScopedToAssignedTasks',assigned)

def workbench(s,body):
 prefetch=body.index('\n\tbeforePrefetch, err :=')
 claim=body.index('\n\treq = reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks/next",')
 draft=body.index('\n\tdraftBody :=')
 submit=body.index('\n\treq = reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks/"+firstID+"/submit",')
 prefetch_phase=body[prefetch:claim];draft_phase=body[draft:submit]
 claim_phase=body[claim:draft].replace('\n\treq =','\n\treq :=',1).replace('\n\trec =','\n\trec :=',1)
 body=body[:prefetch]+'\n\treviewAssertNextContextPrefetchIsReadOnly(t, reviewStore, router, graderToken, secondID)'+claim_phase+'\n\treviewAssertDraftRefreshAndVersionConflict(t, router, graderToken, firstID)'+body[submit:]
 helpers=helper('reviewAssertNextContextPrefetchIsReadOnly','t *testing.T, reviewStore *review.MemoryStore, router http.Handler, graderToken, secondID string',prefetch_phase,'预取下一任务只读上下文，读取前后的修订号和 assigned 状态必须保持一致，随后才领取当前任务。')
 helpers+=helper('reviewAssertDraftRefreshAndVersionConflict','t *testing.T, router http.Handler, graderToken, firstID string',draft_phase,'同一已领取任务依次保存草稿、刷新恢复和拒绝旧修订号，确认服务端草稿与租约版本没有混用。')
 return replace_function(s,'TestReviewWorkbenchClaimDraftRefreshSubmitAndNext',body,helpers)
apply(path,'TestReviewWorkbenchClaimDraftRefreshSubmitAndNext',workbench)

def arbitration(s,body):
 filters=body.index('\n\treq = reviewAuthedRequest(http.MethodGet, "/api/v1/arbitration-tasks?exam_id=exam-1",')
 matrix=body.index('\n\tfor name, assigneeID :=')
 unassigned=body.index('\n\treq = reviewAuthedRequest(http.MethodGet, "/api/v1/arbitration-tasks/"+arbitrationID, "", arbitratorToken)',matrix)
 assigned=body.index('\n\treq = reviewAuthedRequest(http.MethodPost, "/api/v1/arbitration-tasks/"+arbitrationID+"/assign", `{"assigned_to":"`+reviewArbitratorID+`","expected_revision":1}`, managerToken)',unassigned)
 materials=body.index('\n\treq = reviewAuthedRequest(http.MethodGet, "/api/v1/arbitration-tasks/"+arbitrationID, "", arbitratorToken)',assigned)
 filter_phase=body[filters:matrix];matrix_phase=body[matrix:unassigned];unassigned_phase=body[unassigned:assigned];material_phase=body[materials:]
 body=body[:filters]+'\n\treviewAssertArbitrationFilterAndUnassignedSubmission(t, router, managerToken, arbitrationID)\n\treviewAssertInvalidArbitrationAssignees(t, router, managerToken, arbitrationID)\n\treviewAssertUnassignedArbitrationAccess(t, router, arbitrationID, arbitratorToken, teacherToken)'+body[assigned:materials]+'\n\treviewAssertAssignedArbitrationMaterialsAndSubmission(t, router, arbitrationID, arbitratorToken, otherArbitratorToken)\n'
 helpers=helper('reviewAssertArbitrationFilterAndUnassignedSubmission','t *testing.T, router http.Handler, managerToken, arbitrationID string',filter_phase,'考试筛选分别检查包含和排除；管理者提交不能隐式把未分配的仲裁任务指派给自己。')
 helpers+=helper('reviewAssertInvalidArbitrationAssignees','t *testing.T, router http.Handler, managerToken, arbitrationID string',matrix_phase,'原有子测试分别保留角色、停用状态和租户边界检查，失败仍只中止对应子测试。')
 helpers+=helper('reviewAssertUnassignedArbitrationAccess','t *testing.T, router http.Handler, arbitrationID, arbitratorToken, teacherToken string',unassigned_phase,'正确仲裁角色在未分配时仍不能读取；教师管理权限也不能替代管理员派发。')
 helpers+=helper('reviewAssertAssignedArbitrationMaterialsAndSubmission','t *testing.T, router http.Handler, arbitrationID, arbitratorToken, otherArbitratorToken string',material_phase,'派发后只向负责人提供冻结标准；先拒绝其他仲裁者的读取和提交，再由负责人产生最终成绩。')
 return replace_function(s,'TestArbitrationWorkPermissionIsScopedToAssignedTasks',body,helpers)
apply(path,'TestArbitrationWorkPermissionIsScopedToAssignedTasks',arbitration)

def doublemark(s,body):
 blind=body.index('\n\treq = reviewAuthedRequest(http.MethodPost, "/api/v1/review-tasks/"+session.FirstReviewTaskID+"/submit",')
 assignment=body.index('\n\treq = reviewAuthedRequest(http.MethodPost, "/api/v1/arbitration-tasks/"+arbitrationID+"/assign",')
 blind_phase=body[blind:assignment].replace('session.FirstReviewTaskID','firstID').replace('session.SecondReviewTaskID','secondID')
 blind_phase=blind_phase.replace('\n\tarbitrationID := decodeArbitrationIDFromSubmit(t, rec.Body.Bytes())','\n\treturn decodeArbitrationIDFromSubmit(t, rec.Body.Bytes())')
 body=body[:blind]+'\n\tarbitrationID := reviewSubmitBlindDoubleMarks(t, router, session.FirstReviewTaskID, session.SecondReviewTaskID, firstToken, secondToken)'+body[assignment:]
 helpers=helper('reviewSubmitBlindDoubleMarks','t *testing.T, router http.Handler, firstID, secondID, firstToken, secondToken string',blind_phase,'第一轮提交和任务详情均保持盲评；第二轮产生仲裁而不是直接终结成绩，然后才允许派发仲裁。',' string')
 return replace_function(s,'TestDoubleMarkAndArbitrationRoutes',body,helpers)
apply(path,'TestDoubleMarkAndArbitrationRoutes',doublemark)
(report/'go-server-stages11.json').write_text(json.dumps({'files':ns['ns']['changed'],'issue_functions':ns['functions'],'checkpoint_relation':'after checkpoint02; requires subsequent scan'},ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
print(json.dumps(ns['functions']))
