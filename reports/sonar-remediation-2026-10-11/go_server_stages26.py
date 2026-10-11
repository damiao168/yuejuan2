from pathlib import Path
import json,re
root=Path('D:/project/yuejuan');report=root/'reports/sonar-remediation-2026-10-11'
source=(report/'go_server_stages2.py').read_text(encoding='utf-8');ns={};exec(source[:source.index("\npath='services/")],ns)
replace_function=ns['replace_function'];apply=ns['apply'];mask_go=ns['mask_go'];function=ns['function']
path='services/api-gateway/internal/server/e2e_reliability_command_test.go'
fields={'db':'DB','tenantID':'TenantID','userID':'UserID','schoolID':'SchoolID','gradeID':'GradeID','classID':'ClassID','store':'Exams','scope':'Scope','commandID':'CommandID','input':'SessionInput','first':'First','separate':'Separate','examHandler':'ExamHandler','middlewareBody':'MiddlewareBody','middlewareInput':'MiddlewareInput','fileID':'FileID','paperStore':'Paper','createImport':'ImportInput','job':'Job','claimedDispatch':'Dispatch','runtimeStore':'Runtime','claimed':'OldClaim','current':'Current','failureTask':'FailureTask','failureClaim':'FailureClaim','failedJob':'FailedJob','retried':'Retried','projectionStore':'Projection','pageID':'PageID','exceptionID':'ExceptionID','movedPageID':'MovedPageID'}
def helper(name,phase,comment,returns='',locals=(),args=''):
 used=[(key,value) for key,value in fields.items() if key not in locals and re.search(r'\b'+key+r'\b',mask_go(phase))]
 aliases='\n\t'+', '.join(key for key,_ in used)+' := '+', '.join('scenario.'+field for _,field in used) if used else ''
 # 原 err 的复用只跨语句阶段，不是业务事实；独立阶段按原操作次序复用局部 error。
 if re.search(r'\berr\s*=',mask_go(phase)):aliases+='\n\tvar err error'
 return '\n\n// '+comment+'\nfunc '+name+'(t *testing.T, scenario reliabilityCommandScenario'+args+')'+returns+' {\n\tt.Helper()'+aliases+phase+'\n}'
def extract_callback(body,label,name,args=''):
 start=body.index('func(t *testing.T) {',body.index(label));opening=body.index('{',start);masked=mask_go(body);depth=1;i=opening+1
 while depth:
  if masked[i]=='{':depth+=1
  elif masked[i]=='}':depth-=1
  i+=1
 phase=re.sub(r'^\t','',body[opening+1:i-1],flags=re.M)
 return body[:opening+1]+'\n\t\t'+name+'(t, scenario'+args+')\n\t'+body[i-1:],phase
def main(s,body):
 helpers='''

type reliabilityCommandScenario struct {
 DB *sql.DB
 TenantID, UserID, SchoolID, GradeID, ClassID, CommandID, FileID string
 Exams *exam.PostgresStore
 Scope auth.AccessScope
 SessionInput exam.CreateSessionInput
 First, Separate exam.ExamSession
 ExamHandler *exam.Handler
 MiddlewareBody []byte
 MiddlewareInput exam.CreateSessionInput
 Paper *paper.PostgresStore
 ImportInput paper.CreatePaperImportInput
 Job, Dispatch, Current, FailedJob, Retried paper.PaperImportJob
 Runtime *workerruntime.PostgresStore
 OldClaim, FailureClaim []workerruntime.Task
 FailureTask workerruntime.Task
 Projection *processing.PostgresStore
 PageID, ExceptionID, MovedPageID string
}'''
 specs=[('CMD-02 actual','e2eAssertActualClientCommandRecovery','真实客户端仍通过同一故障 handler 调用 PostgreSQL；资源与一分钟截止保留在该子测试。',()),('PROJ expired','e2eAssertProjectionLeaseGenerationFence','相同 owner 重领也不能复用旧租约，过期发布和失败回调均须拒绝。',()),('IMP durable preparation','e2eAssertPreparationRetryExhaustion','准备失败按原三次尝试退避，首轮过期回调拒绝，最终耗尽后不再领取。',('input',)),('IMP abandoned preparation','e2eAssertAbandonedPreparationRecovery','崩溃后的准备租约逐次恢复；最终故障注入必须回滚再由新存储完成耗尽。',('input',)),('IMP final abandoned','e2eAssertFinalAbandonedRuntimeRecovery','最后活动尝试耗尽必须同时关闭运行；注入失败后新存储恢复且再次恢复无变化。',('input','job')),('PROJ production capture','e2eAssertCaptureProjectionLifecycle','生产采集页删除和恢复按有效页集合更新投影，原嵌套命令恢复子测试先执行。',()),('IMP frozen exam','e2eAssertFrozenImportCommands','冻结考试仍恢复已接受命令，但新导入、添加来源和重新运行都拒绝且不改当前代次。',('current',))]
 for label,name,comment,locals in specs:
  body,phase=extract_callback(body,label,name);helpers+=helper(name,phase,comment,locals=locals)
 marks=['\n\tfirst, err :=','\n\tprocessingCommand :=','\n\tnewCommandInput :=','\n\t// 同时测试生产','\n\tt.Run("CMD-02','\n\tfileID :=','\n\tvar runCount,','\n\truntimeStore :=','\n\tif _, err = db.Exec(`CREATE FUNCTION fail_test_paper_run_insert','\n\trestarted, err :=','\n\tlegacyTask, err :=','\n\tfailureTask, err :=','\n\tif _, err = db.Exec(`CREATE FUNCTION fail_test_paper_runtime_failure','\n\tretryCommand :=','\n\traceTask, err :=','\n\tsubmissionID, pageID :=','\n\tif _, err = db.Exec(`UPDATE submission_page SET deleted_at','\n\tresolvedSubmissionID,','\n\tmovedSubmissionID,','\n\tvar oldOpen,','\n\tregroupedSubmissionID :=','\n\tdeletedSubmissionID,','\n\t// 已领取的投影器','\n\tt.Run("PROJ expired']
 positions=[body.index(m) for m in marks]
 phases=[body[positions[i]:positions[i+1]] for i in range(len(positions)-1)]
 mainprefix=body[:positions[0]]+'''
 scenario := reliabilityCommandScenario{DB: db, TenantID: tenantID, UserID: userID, SchoolID: schoolID,
  GradeID: gradeID, ClassID: classID, Exams: store, Scope: scope, CommandID: commandID, SessionInput: input}
 scenario.First = e2eAssertInitialSessionCommandReplay(t, scenario)
 scenario.SessionInput = e2eAssertSessionCommandRecoveryStates(t, scenario)
 scenario.Separate = e2eAssertSeparateAndConcurrentSessionCommands(t, scenario)
 scenario.ExamHandler, scenario.MiddlewareBody, scenario.MiddlewareInput = e2eAssertLostSessionReceiptRecovery(t, scenario)
'''
 middle=phases[4] # 原真实客户端 t.Run 留在主测试并保持同一时点。
 mainbody=mainprefix+middle+'''
 scenario.FileID, scenario.Paper, scenario.ImportInput, scenario.Job = e2eCreateReliableImportCommand(t, scenario)
 scenario.Dispatch = e2eAssertImportDurableDispatchIntent(t, scenario)
 scenario.Runtime = workerruntime.NewPostgresStore(db)
 scenario.OldClaim = e2eCreateAndCancelImportRuntime(t, scenario)
 e2eAssertSuccessorRunRollback(t, scenario)
 scenario.Current = e2eAssertSuccessorImportAndStaleCallback(t, scenario)
 e2eAssertLegacyImportCallbacksRejected(t, scenario)
 scenario.FailureTask, scenario.FailureClaim = e2eCreateImportFailureRuntime(t, scenario)
 scenario.FailedJob = e2eAssertImportFailureAtomicity(t, scenario)
 scenario.Retried = e2eAssertFailedImportRetryIdentity(t, scenario)
 e2eRaceImportCancellationAndCompletion(t, scenario)
 scenario.Projection, scenario.PageID, scenario.ExceptionID = e2eCreateAssignedProjectionSource(t, scenario)
 e2eDeleteAssignedProjectionSource(t, scenario)
 e2eAssertManualProjectionResolutionPreserved(t, scenario)
 scenario.MovedPageID = e2eMoveProjectionSourceToDestination(t, scenario)
 e2eAssertDestinationFirstProjectionFacts(t, scenario)
 e2eRegroupProjectionSource(t, scenario)
 e2eDeleteProjectionSubmission(t, scenario)
 e2eAssertProjectionAcknowledgesClaimedVersion(t, scenario)
'''+body[positions[-1]:]
 specs=[
 (0,'e2eAssertInitialSessionCommandReplay','创建与准确重放得到同一父子资源，恢复和未接受命令状态都核对原数据库事实。',' exam.ExamSession',('first',),'\n\treturn first'),
 (1,'e2eAssertSessionCommandRecoveryStates','处理中与已拒绝收据返回不同恢复状态；改变原命令负载必须冲突。',' exam.CreateSessionInput',(),'\n\treturn input'),
 (2,'e2eAssertSeparateAndConcurrentSessionCommands','显式新操作不能合并旧资源，两份并发相同命令仍返回同一资源。',' exam.ExamSession',('separate',),'\n\treturn separate'),
 (3,'e2eAssertLostSessionReceiptRecovery','生产事务提交后故障仅丢失传输收据，原请求重试接管并准确重放且父资源只有一份。',' (*exam.Handler, []byte, exam.CreateSessionInput)',('examHandler','middlewareBody','middlewareInput'),'\n\treturn examHandler, middlewareBody, middlewareInput'),
 (5,'e2eCreateReliableImportCommand','并发初始导入只创建一个运行，相同命令重放而来源身份改变拒绝。',' (string, *paper.PostgresStore, paper.CreatePaperImportInput, paper.PaperImportJob)',('fileID','paperStore','createImport','job'),'\n\treturn fileID, paperStore, createImport, job'),
 (6,'e2eAssertImportDurableDispatchIntent','唯一运行持有可恢复准备意图，领取加载该运行冻结来源及代次。',' paper.PaperImportJob',('claimedDispatch',),'\n\treturn claimedDispatch'),
 (7,'e2eCreateAndCancelImportRuntime','原任务绑定已接受运行并领取后才取消，后续旧回调保留这一租约身份。',' []workerruntime.Task',('runtimeStore','claimed'),'\n\treturn claimed'),
 (8,'e2eAssertSuccessorRunRollback','新运行插入失败整体回滚原作业，移除注入后才允许正常重试。','',(),''),
 (9,'e2eAssertSuccessorImportAndStaleCallback','新代次接受后旧准备失败及旧解码回调都拒绝，当前作业不能被旧结果污染。',' paper.PaperImportJob',('current',),'\n\treturn current'),
 (10,'e2eAssertLegacyImportCallbacksRejected','协议一及未绑定代次的结果均拒绝，当前代次、状态和候选保持原值。','',(),''),
 (11,'e2eCreateImportFailureRuntime','失败任务绑定当前运行并领取，终态提交使用该任务的原租约。',' (workerruntime.Task, []workerruntime.Task)',('failureTask','failureClaim'),'\n\treturn failureTask, failureClaim'),
 (12,'e2eAssertImportFailureAtomicity','终态故障注入同时回滚任务和作业，删除注入后按同一失败输入提交。',' paper.PaperImportJob',('failedJob',),'\n\treturn failedJob'),
 (13,'e2eAssertFailedImportRetryIdentity','终态重试创建第三代，准确命令重放同一新运行而不会复用失败运行。',' paper.PaperImportJob',('retried',),'\n\treturn retried'),
 (14,'e2eRaceImportCancellationAndCompletion','解码完成与取消共享屏障，允许明确冲突但最终取消必须清除活动任务及已发布结果。','',(),''),
 (15,'e2eCreateAssignedProjectionSource','先形成投影并派发人工异常，重复相同投影不能覆盖人工派发。',' (*processing.PostgresStore, string, string)',('projectionStore','pageID','exceptionID'),'\n\treturn projectionStore, pageID, exceptionID'),
 (16,'e2eDeleteAssignedProjectionSource','页删除使有效页数为零，清理当前状态并将已派发异常以来源删除原因关闭。','',(),''),
 (17,'e2eAssertManualProjectionResolutionPreserved','重复时间戳变化仍保留明确人工处理，删除提交之后再更新投影。','',(),''),
 (18,'e2eMoveProjectionSourceToDestination','来源转移后先刷新目标考试再刷新原考试，不能把旧异常藏在新归属之后。',' string',('movedPageID',),'\n\treturn movedPageID'),
 (19,'e2eAssertDestinationFirstProjectionFacts','转移保留原异常关闭历史，目标新异常及两侧有效页统计逐项核对。','',(),''),
 (20,'e2eRegroupProjectionSource','同考试内移动页面的提交归属只保留一个投影页。','',(),''),
 (21,'e2eDeleteProjectionSubmission','整份提交删除后不能保留当前页面状态，另一有效页统计保持一份。','',(),''),
 (22,'e2eAssertProjectionAcknowledgesClaimedVersion','投影器只确认所领取版本，新到达请求保留待处理，其他持有者不能发布。','',(),''),
 ]
 for index,name,comment,returns,locals,tail in specs:
  phase=phases[index]
  if index==7:phase=phase.replace('\n\truntimeStore := workerruntime.NewPostgresStore(db)','',1);locals=('claimed',)
  if index==16:phase=phase.replace('summary, err =','summary, err :=',1)
  helpers+=helper(name,phase+tail,comment,returns,locals)
 return replace_function(s,'TestReliabilityCommandRecoveryWithPostgresTestDatabase',mainbody,helpers).replace('"context"','"context"\n\t"database/sql"',1)
apply(path,'TestReliabilityCommandRecoveryWithPostgresTestDatabase',main)
(report/'go-server-stages26.json').write_text(json.dumps({'files':ns['ns']['changed'],'issue_functions':ns['functions'],'checkpoint_relation':'after checkpoint02; requires subsequent scan'},ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
print(json.dumps(ns['functions']))
