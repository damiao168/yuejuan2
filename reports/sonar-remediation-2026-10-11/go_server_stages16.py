from pathlib import Path
import json
root=Path('D:/project/yuejuan');report=root/'reports/sonar-remediation-2026-10-11'
source=(report/'go_server_stages2.py').read_text(encoding='utf-8');ns={};exec(source[:source.index("\npath='services/")],ns)
replace_function=ns['replace_function'];apply=ns['apply']
path='services/api-gateway/internal/server/e2e_scoring_double_mark_test.go';name='TestScoringDoubleMarkLifecycleWithPostgresTestDatabase'

def doublemark(s,body):
 collect=body.index('\n\trows, err := db.Query')
 reviews=body.index('\n\treviews := review.NewPostgresStore')
 submit=body.index('\n\tfor i, item := range tasks',reviews)
 resolve=body.index('\n\tdetail, err :=')
 cancel=body.index('\n\t// 后续运行可能')
 cancelled=body.index('\n\tif err := db.QueryRow(`SELECT status FROM arbitration_task',cancel)
 collection=body[collect:reviews]
 type_start=collection.index('\n\ttype task struct');type_end=collection.index('\n\tvar tasks []task',type_start)
 task_type=collection[type_start+len('\n\ttype task'):type_end].replace('struct','type scoringDoubleMarkTask struct',1)
 collection=collection[:type_start]+collection[type_end:]
 collection=collection.replace('[]task','[]scoringDoubleMarkTask').replace('var item task','var item scoringDoubleMarkTask').replace('run.ID','runID')+'\n\treturn tasks'
 submitted=body[submit:resolve]
 resolved=body[resolve:cancel].replace('run.ID','runID')
 cancellation=body[cancel:cancelled].replace('\n\trows, err =','\n\trows, err :=',1).replace('\n\ttasks = nil','\n\tvar tasks []scoringDoubleMarkTask',1).replace('var item task','var item scoringDoubleMarkTask')+'\n\te2eAssertCancelledDoubleMarkFacts(t, db, f, secondRun.ID, tasks[3].segment)'
 cancelled_facts=body[cancelled:].replace('secondRun.ID','runID').replace('tasks[3].segment','segmentID')
 body=body[:collect]+'\n\ttasks := e2eCollectBlindDoubleMarkTasks(t, db, f, run.ID)'+body[reviews:submit]+'\n\te2eSubmitBlindDoubleMarks(t, reviews, f, tasks)\n\te2eResolveAndAssertDoubleMarkArbitration(t, db, reviews, f, run.ID, tasks)\n\te2eCancelMixedDoubleMarkRun(t, db, router, token, f, reviews, suffix)\n'
 helpers='\n\n'+task_type+'\n\n// 原始四个任务按答案和轮次读取，核对双人分离与轮转分配后才允许评分。\nfunc e2eCollectBlindDoubleMarkTasks(t *testing.T, db *sql.DB, f story056AcceptanceFixture, runID string) []scoringDoubleMarkTask {\n\tt.Helper()'+collection+'\n}'
 helpers+='\n\n// 同分组先最终确认，分歧组再产生仲裁；每个原结果分支都保留在对应提交之后。\nfunc e2eSubmitBlindDoubleMarks(t *testing.T, reviews *review.PostgresStore, f story056AcceptanceFixture, tasks []scoringDoubleMarkTask) {\n\tt.Helper()'+submitted+'\n}'
 helpers+='\n\n// 先拒绝未分配仲裁，再由指定负责人提交；运行汇总与两个正式成绩同时核对。\nfunc e2eResolveAndAssertDoubleMarkArbitration(t *testing.T, db *sql.DB, reviews *review.PostgresStore, f story056AcceptanceFixture, runID string, tasks []scoringDoubleMarkTask) {\n\tt.Helper()'+resolved+'\n}'
 helpers+='\n\n// 新运行重复原四次提交后才取消，保留已确认组和待仲裁组同时存在的触发条件。\nfunc e2eCancelMixedDoubleMarkRun(t *testing.T, db *sql.DB, router http.Handler, token string, f story056AcceptanceFixture, reviews *review.PostgresStore, suffix string) {\n\tt.Helper()'+cancellation+'\n}'
 helpers+='\n\n// 取消必须同时取消仲裁、撤销当前题目成绩并移除可发布的最终成绩。\nfunc e2eAssertCancelledDoubleMarkFacts(t *testing.T, db *sql.DB, f story056AcceptanceFixture, runID, segmentID string) {\n\tt.Helper()\n\tvar status string\n\tvar grades int'+cancelled_facts+'\n}'
 s=replace_function(s,name,body,helpers)
 return s.replace('"context"','"context"\n\t"database/sql"',1)
apply(path,name,doublemark)
(report/'go-server-stages16.json').write_text(json.dumps({'files':ns['ns']['changed'],'issue_functions':ns['functions'],'checkpoint_relation':'after checkpoint02; requires subsequent scan'},ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
print(json.dumps(ns['functions']))
