from pathlib import Path
import json
root=Path('D:/project/yuejuan');report=root/'reports/sonar-remediation-2026-10-11'
source=(report/'go_server_stages2.py').read_text(encoding='utf-8');ns={};exec(source[:source.index("\npath='services/")],ns)
function=ns['function'];replace_function=ns['replace_function'];apply=ns['apply']

path='services/api-gateway/internal/server/e2e_anonymous_paper_test.go';name='TestHighScorePaperShareRedactsAndRevokesWithPostgresTestDatabase'
def anonymous(s,body):
 pixels=body.index('\n\t// 配准图像已与模板对齐');hashing=body.index('\n\tsourceHash :=');response=body.index('\n\tif response.Code !=');own=body.index('\n\t// 普通页面路由仍解析请求学生')
 source_phase=body[pixels:hashing];image_phase=body[response:own]
 body=body[:pixels]+'\n\tsourceBytes, imageData := e2eAnonymousIdentityImage(t)'+body[hashing:response]+'\n\te2eAssertAnonymousIdentityMask(t, response, imageData)'+body[own:]
 helpers='\n\n// 把彩色身份笔画、棋盘码和 PNG 尾随秘密都放进合成来源，供后续脱敏验证。\nfunc e2eAnonymousIdentityImage(t *testing.T) ([]byte, *image.RGBA) {\n\tt.Helper()'+source_phase+'\n\treturn sourceBytes, imageData\n}'
 helpers+='\n\n// 检查身份区域三个像素与尾随秘密被去除，同时保留区域外的一处答题像素。\nfunc e2eAssertAnonymousIdentityMask(t *testing.T, response *httptest.ResponseRecorder, imageData *image.RGBA) {\n\tt.Helper()'+image_phase+'\n}'
 return replace_function(s,name,body,helpers)
apply(path,name,anonymous)

path='services/api-gateway/internal/server/e2e_core_workflow_test.go';name='TestCoreWorkflowE2EWithSyntheticMemoryStores'
def workflow(s,body):
 prepare=body.index('\n\tschool := e2ePostJSON(');collect=body.index('\n\tanswerFileID :=');grading=body.index('\n\tquestion := paper.Question{');publish=body.index('\n\tfixture.scoreStore.AddSegment(')
 prepared=body[prepare:collect];collected=body[collect:grading];graded=body[grading:publish];published=body[publish:]
 body=body[:prepare]+'\n\tactors := memoryWorkflowActors{Admin: adminToken, Teacher: teacherToken, Student: studentToken, OtherStudent: otherStudentToken}\n\texam := e2ePrepareMemoryWorkflowExam(t, fixture, adminToken)\n\tanswer := e2eCollectMemoryWorkflowAnswer(t, fixture, adminToken, exam.ExamID)\n\taiGradeID := e2eReviewMemoryWorkflowEvidence(t, fixture, actors, exam, answer)\n\te2ePublishAndAppealMemoryWorkflow(t, fixture, actors, exam, answer, aiGradeID)\n'
 types='\n\ntype memoryWorkflowActors struct {\n\tAdmin, Teacher, Student, OtherStudent string\n}\n\ntype memoryWorkflowExam struct {\n\tExamID, PaperID, QuestionID, RubricID string\n}\n\ntype memoryWorkflowAnswer struct {\n\tSubmissionID, PageID, AnswerFileID, AnswerText, SegmentID string\n}\n'
 helpers=types+'\n// 内存夹具需要显式设置试卷总分，再保存题目与量规；纸卷配置校验仍走原 API。\nfunc e2ePrepareMemoryWorkflowExam(t *testing.T, fixture *e2eMemoryFixture, adminToken string) memoryWorkflowExam {\n\tt.Helper()'+prepared+'\n\treturn memoryWorkflowExam{ExamID: examID, PaperID: paperID, QuestionID: questionID, RubricID: rubricID}\n}'
 helpers+='\n\n// 采集质量先通过，才领取 OCR Runtime 租约、回传低置信结果并生成答题片段。\nfunc e2eCollectMemoryWorkflowAnswer(t *testing.T, fixture *e2eMemoryFixture, adminToken, examID string) memoryWorkflowAnswer {\n\tt.Helper()'+collected+'\n\treturn memoryWorkflowAnswer{SubmissionID: submissionID, PageID: pageID, AnswerFileID: answerFileID, AnswerText: answerText, SegmentID: segmentID}\n}'
 helpers+='\n\n// 模拟模型与低置信证据不能自动发布；先保留未完成复核门禁，再由指定教师提交合法分数。\nfunc e2eReviewMemoryWorkflowEvidence(t *testing.T, fixture *e2eMemoryFixture, actors memoryWorkflowActors, exam memoryWorkflowExam, answer memoryWorkflowAnswer) string {\n\tt.Helper()\n\tadminToken, teacherToken := actors.Admin, actors.Teacher\n\texamID, paperID, questionID, rubricID := exam.ExamID, exam.PaperID, exam.QuestionID, exam.RubricID\n\tsubmissionID, pageID, answerText, segmentID := answer.SubmissionID, answer.PageID, answer.AnswerText, answer.SegmentID'+graded+'\n\treturn aiGradeID\n}'
 helpers+='\n\n// 人工成绩确认后才发布；随后验证学生自有成绩、同伴隔离、申诉调整及整条审计链。\nfunc e2ePublishAndAppealMemoryWorkflow(t *testing.T, fixture *e2eMemoryFixture, actors memoryWorkflowActors, exam memoryWorkflowExam, answer memoryWorkflowAnswer, aiGradeID string) {\n\tt.Helper()\n\tadminToken, teacherToken, studentToken, otherStudentToken := actors.Admin, actors.Teacher, actors.Student, actors.OtherStudent\n\texamID, questionID := exam.ExamID, exam.QuestionID\n\tsubmissionID, answerText, segmentID := answer.SubmissionID, answer.AnswerText, answer.SegmentID'+published+'}'
 return replace_function(s,name,body,helpers)
apply(path,name,workflow)
(report/'go-server-stages8.json').write_text(json.dumps({'files':ns['ns']['changed'],'issue_functions':ns['functions'],'checkpoint_relation':'after checkpoint02; requires subsequent scan'},ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
print(json.dumps(ns['functions']))
