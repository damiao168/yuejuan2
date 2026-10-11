from pathlib import Path
import json,re
root=Path('D:/project/yuejuan');report=root/'reports/sonar-remediation-2026-10-11'
source=(report/'go_server_stages2.py').read_text(encoding='utf-8');ns={};exec(source[:source.index("\npath='services/")],ns)
replace_function=ns['replace_function'];apply=ns['apply'];mask_go=ns['mask_go']
path='services/api-gateway/internal/server/e2e_postgres_test.go';name='TestCoreWorkflowE2EWithPostgresTestDatabase'
fields={'db':'DB','router':'Router','demoTenantID':'TenantID','schoolID':'SchoolID','gradeID':'GradeID','classID':'ClassID','studentID':'StudentID','otherStudentID':'OtherStudentID','suffix':'Suffix','adminToken':'AdminToken','teacherToken':'TeacherToken','graderToken':'GraderToken','studentToken':'StudentToken','otherStudentToken':'OtherStudentToken','schoolAdminToken':'SchoolAdminToken','teacherID':'TeacherID','graderID':'GraderID','appealReviewerID':'AppealReviewerID','appealReviewerToken':'AppealReviewerToken'}

def helper(helpername,params,phase,comment,output=''):
 used=[(key,value) for key,value in fields.items() if re.search(r'\b'+key+r'\b',mask_go(phase))]
 aliases='\n\t'+', '.join(key for key,_ in used)+' := '+', '.join('scope.'+field for _,field in used) if used else ''
 return '\n\n// '+comment+'\nfunc '+helpername+'('+params+')'+output+' {\n\tt.Helper()'+aliases+phase+'\n}'

def postgres(s,body):
 exam=body.index('\n\texamResp :=')
 roster=body.index('\n\tvar candidateCount int',exam)
 paper=body.index('\n\tpaperFileID :=',roster)
 capture=body.index('\n\tanswerFileID :=',paper)
 projection=body.index('\n\tprocessingStore :=',capture)
 segmentation=body.index('\n\tsegmentResult :=',projection)
 review=body.index('\n\treviewTask :=',segmentation)
 publish=body.index('\n\tfinalized :=',review)
 roster_phase=body[roster:paper]
 exam_phase=body[exam:roster]+'\n\te2eAssertPostgresExamRosterSnapshot(t, scope, examID)'+body[paper:capture]+'\n\treturn examID'
 projection_phase=body[projection:segmentation]
 capture_phase=body[capture:projection]+'\n\te2eAssertPostgresProcessingProjectionIsReadOnly(t, scope, examID, ocrTaskID)'+body[segmentation:review]+'\n\treturn segmentID'
 review_phase=body[review:publish]
 published=body[publish:]
 assignment=', '.join(field+': '+key for key,field in fields.items())
 body=body[:exam]+'\n\tscope := postgresWorkflowScope{'+assignment+'}\n\texamID := e2ePreparePostgresExamAndFrozenPaper(t, scope)\n\tsegmentID := e2eCollectPostgresAnswerAndAbstainFromAI(t, scope, examID)\n\te2eCompletePostgresReviewWithDraftIsolation(t, scope, examID, segmentID)\n\te2ePublishPostgresGradeAndReviewAppeal(t, scope, examID, segmentID)\n'
 helpers='\n\ntype postgresWorkflowScope struct {\n\tDB *sql.DB\n\tRouter http.Handler\n\tTenantID, SchoolID, GradeID, ClassID, StudentID, OtherStudentID, Suffix string\n\tAdminToken, TeacherToken, GraderToken, StudentToken, OtherStudentToken, SchoolAdminToken string\n\tTeacherID, GraderID, AppealReviewerID, AppealReviewerToken string\n}'
 params='t *testing.T, scope postgresWorkflowScope'
 helpers+=helper('e2ePreparePostgresExamAndFrozenPaper',params,exam_phase,'先冻结考试名单和评估规则再接收答卷；跨学科策略拒绝与正确学科快照分别保留。',' string')
 helpers+=helper('e2eAssertPostgresExamRosterSnapshot',params+', examID string',roster_phase,'考试创建后改名和转班只更新当前学籍，名单仍使用考试时的姓名和班级，历史学籍顺序也必须保留。')
 helpers+=helper('e2eCollectPostgresAnswerAndAbstainFromAI',params+', examID string',capture_phase,'按原顺序完成图像质量、OCR 租约和切题；生产组合没有 AI 资格时必须弃权并要求人工复核。',' string')
 helpers+=helper('e2eAssertPostgresProcessingProjectionIsReadOnly',params+', examID, ocrTaskID string',projection_phase,'只读 HTTP 查询不能移动投影游标；随后重新投影也不能覆盖阅卷员已认领的异常归属。')
 helpers+=helper('e2eCompletePostgresReviewWithDraftIsolation',params+', examID, segmentID string',review_phase,'管理员派发后才可人工评分；临时改派立即阻断原阅卷员读写草稿，再恢复原派发并核对超分拒绝与命令重放。')
 helpers+=helper('e2ePublishPostgresGradeAndReviewAppeal',params+', examID, segmentID string',published,'待确认成绩先经过确认和缺考登记再发布；学生隔离、独立申诉建议、最终调整和审计依原顺序执行。')
 return replace_function(s,name,body,helpers)
apply(path,name,postgres)
(report/'go-server-stages18.json').write_text(json.dumps({'files':ns['ns']['changed'],'issue_functions':ns['functions'],'checkpoint_relation':'after checkpoint02; requires subsequent scan'},ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
print(json.dumps(ns['functions']))
