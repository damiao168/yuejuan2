from pathlib import Path
import json,re
root=Path('D:/project/yuejuan');report=root/'reports/sonar-remediation-2026-10-11'
source=(report/'go_server_stages2.py').read_text(encoding='utf-8');ns={};exec(source[:source.index("\npath='services/")],ns)
replace_function=ns['replace_function'];apply=ns['apply'];mask_go=ns['mask_go'];function=ns['function']
path='services/api-gateway/internal/server/e2e_paper_import_reconciliation_test.go'
fields={'ctx':'Context','db':'DB','store':'Store','runtimeStore':'Runtime','tenantID':'TenantID','userID':'UserID','examID':'ExamID','paperFileID':'PaperFileID','answerFileID':'AnswerFileID','questionID':'QuestionID','job':'Job','parseDocuments':'Documents','claimedParse':'ClaimedParse','binding':'Binding'}
def helper(name,phase,comment,args='',returns='',locals=()):
 used=[(key,value) for key,value in fields.items() if key not in locals and re.search(r'\b'+key+r'\b',mask_go(phase))]
 aliases='\n\t'+', '.join(key for key,_ in used)+' := '+', '.join('scenario.'+field for _,field in used) if used else ''
 return '\n\n// '+comment+'\nfunc '+name+'(t *testing.T, scenario paperApplyGateScenario'+args+')'+returns+' {\n\tt.Helper()'+aliases+phase+'\n}'
def extract_callback(body,label,name,args):
 start=body.index('func(t *testing.T) {',body.index(label));opening=body.index('{',start);masked=mask_go(body);depth=1;i=opening+1
 while depth:
  if masked[i]=='{':depth+=1
  elif masked[i]=='}':depth-=1
  i+=1
 phase=re.sub(r'^\t','',body[opening+1:i-1],flags=re.M)
 return body[:opening+1]+'\n\t\t'+name+'(t, scenario'+args+')\n\t'+body[i-1:],phase
def main(s,body):
 closure=body.index('\n\tassertPersistedPages :=');opening=body.index('{',closure);masked=mask_go(body);depth=1;i=opening+1
 while depth:
  if masked[i]=='{':depth+=1
  elif masked[i]=='}':depth-=1
  i+=1
 persisted=re.sub(r'^\t','',body[opening+1:i-1],flags=re.M).replace('\n\tt.Helper()','',1)
 body=body[:closure]+'''
 scenario := paperApplyGateScenario{Context: ctx, DB: db, Store: store, Runtime: runtimeStore,
  TenantID: tenantID, UserID: userID, ExamID: examID, PaperFileID: paperFileID,
  AnswerFileID: answerFileID, QuestionID: questionID, Job: job, Documents: parseDocuments}
 assertPersistedPages := func(t *testing.T, where string, expected []paper.PaperImportDecodedPage) string {
  return e2eAssertPersistedPaperPages(t, scenario, where, expected)
 }
'''+body[i:]
 body=body.replace('\n\tinvalidScore := 10.0','\n\tscenario.ClaimedParse, scenario.Binding = claimedParse, binding',1)
 helpers='''

type paperApplyGateScenario struct {
 Context context.Context
 DB *sql.DB
 Store *paper.PostgresStore
 Runtime *workerruntime.PostgresStore
 TenantID, UserID, ExamID, PaperFileID, AnswerFileID, QuestionID string
 Job paper.PaperImportJob
 Documents []paper.PaperImportParseDocument
 ClaimedParse []workerruntime.Task
 Binding paper.PaperImportRunBinding
}'''
 helpers+=helper('e2eAssertPersistedPaperPages',persisted,'空页数组及已解码页均核对数据库 JSON、内容顺序和存储重新读取，不能只检查序列化输入。',', where string, expected []paper.PaperImportDecodedPage',' string')
 for label,name,comment,args in [
 ('MIG immutable','e2eAssertPaperStageSnapshotImmutable','解析输入和已接受来源快照不可变，重新读取仍保持原哈希及文档集合。',''),
 ('IMP missing active','e2eAssertPaperMissingAttemptRollback','缺少活动尝试时完成操作必须整体回滚，再恢复原尝试供后续边界测试。',''),
 ('IMP binding rejects','e2eAssertPaperStageAndInputFence','逐个错误阶段和输入子测试保留原恢复步骤，拒绝结果不能改任务、作业或候选。',''),
 ('OCR completion atomically','e2eAssertPaperOCRParseRecovery','OCR 回调原子交接持久解析任务，新执行器模拟进程重启，提交失败后按原租约恢复。','')]:
  body,phase=extract_callback(body,label,name,args);helpers+=helper(name,phase,comment)
 invalid=body.index('\n\tif _, err = store.CompletePaperImportParseTask');valid=body.index('\n\tvalid :=');facts=body.index('\n\tvar previousRuleStatus');reorder=body.index('\n\treorderJob, err :=');ocr=body.index('\n\tt.Run("OCR completion')
 invalidphase='\n\tinvalidScore := 10.0'+body[invalid:valid].replace('if _, err = store.CompletePaperImportParseTask','if _, err := store.CompletePaperImportParseTask',1)
 validphase=body[valid:facts]+'\n\treturn previousRuleID'
 factphase='\n\tvar stem string'+body[facts:reorder]
 reorderphase=body[reorder:ocr]
 body=body[:invalid]+'\n\te2eAssertInvalidPaperImportGate(t, scenario)\n\tpreviousRuleID := e2eReviewAndApplyPaperImport(t, scenario)\n\te2eAssertAppliedPaperCanonicalFacts(t, scenario, previousRuleID)\n\te2eAssertPaperSourceReorder(t, scenario)'+body[ocr:]
 helpers+=helper('e2eAssertInvalidPaperImportGate',invalidphase,'无效候选允许成为复核建议但应用门禁必须拒绝，正式题干保持原值。')
 helpers+=helper('e2eReviewAndApplyPaperImport',validphase,'人工确认只推进同代次数据库版本，先记录原已发布规则再应用，保证替换顺序。',returns=' string')
 helpers+=helper('e2eAssertAppliedPaperCanonicalFacts',factphase,'应用后旧规则退役、所属运行完成，正式题目及解答保留本次导入来源。',', previousRuleID string')
 helpers+=helper('e2eAssertPaperSourceReorder',reorderphase,'版本化失败后重排两份来源，部分唯一索引不改变用户指定顺序并清除旧错误码。')
 return replace_function(s,'TestPaperImportApplyGateE2EWithPostgresTestDatabase',body,helpers).replace('"context"','"context"\n\t"database/sql"',1)
apply(path,'TestPaperImportApplyGateE2EWithPostgresTestDatabase',main)

def stage_mutation(s,body):
 body,phase=extract_callback(body,'t.Run(mutation.name','e2eAssertPaperBindingMutation',', mutation')
 phase=re.sub(r'^\t','',phase,flags=re.M)
 body=body.replace('[]struct{ name, update string }','[]paperBindingMutation')
 helpers='\n\ntype paperBindingMutation struct { name, update string }'
 helpers+=helper('e2eAssertPaperBindingMutation',phase,'每条错误绑定先修改原任务、确认冲突且事实未变，再恢复原输入和阶段供下一场景使用。',', mutation paperBindingMutation')
 return replace_function(s,'e2eAssertPaperStageAndInputFence',body,helpers)
apply(path,'e2eAssertPaperStageAndInputFence',stage_mutation)

recovery_fields={'fileStore':'Files','objectStore':'Objects','parseJob':'Job','claimed':'ClaimedOCR','executor':'Executor','parseTaskID':'TaskID','recovered':'Recovered','replayLeaseToken':'ReplayLeaseToken','binding':'Binding','parseResult':'Result','changedResult':'ChangedResult'}
def recovery_helper(name,phase,comment,returns='',locals=()):
 used=[(key,value) for key,value in fields.items() if key not in locals and re.search(r'\b'+key+r'\b',mask_go(phase))]
 used=[(key,value) for key,value in used if key not in recovery_fields]
 aliases='\n\t'+', '.join(key for key,_ in used)+' := '+', '.join('recovery.Gate.'+field for _,field in used) if used else ''
 extra=[(key,value) for key,value in recovery_fields.items() if key not in locals and re.search(r'\b'+key+r'\b',mask_go(phase))]
 if extra:aliases+='\n\t'+', '.join(key for key,_ in extra)+' := '+', '.join('recovery.'+field for _,field in extra)
 return '\n\n// '+comment+'\nfunc '+name+'(t *testing.T, recovery paperParseRecovery)'+returns+' {\n\tt.Helper()'+aliases+phase+'\n}'
def recovery(s,body):
 start=body.index('\n\tpageContent :=');handoff=body.index('\n\tblock :=');parser=body.index('\n\tparser :=');restart=body.index('\n\tif _, updateErr :=');inject=body.index('\n\tif _, triggerErr :=');receipt=body.index('\n\tvar replayLeaseToken');race=body.index('\n\t// 人工保存');historical=body.index('\n\tif currentAfterReplay,')
 setup=body[start:handoff]+'\n\treturn fileStore, objectStore, parseJob, claimed'
 handoffphase=body[handoff:parser]
 restartphase=body[restart:inject]+'\n\treturn executor, parseTaskID'
 injectphase=body[inject:receipt]+'\n\treturn recovered'
 receiptphase=body[receipt:race].replace('\n\tworked, runErr = executor.RunOnce(ctx)','\n\tworked, runErr := executor.RunOnce(ctx)',1)+'\n\treturn replayLeaseToken, binding, parseResult, changedResult'
 racephase=body[race:historical]+'\n\treturn newRun'
 historicalphase=body[historical:]
 declarations='''

type paperParseRecovery struct {
 Gate paperApplyGateScenario
 Files *files.PostgresStore
 Objects *files.MemoryObjectStorage
 Job paper.PaperImportJob
 ClaimedOCR []workerruntime.Task
 Executor *paper.ParseTaskExecutor
 TaskID string
 Recovered paper.PaperImportJob
 ReplayLeaseToken string
 Binding paper.PaperImportRunBinding
 Result, ChangedResult paper.PaperImportParseResult
}'''
 helpers=declarations
 helpers+=recovery_helper('e2eCreatePaperOCRRecovery',setup,'建立独立合成页与运行，原任务绑定被冻结的运行及代次后才领取 OCR 租约。',' (*files.PostgresStore, *files.MemoryObjectStorage, paper.PaperImportJob, []workerruntime.Task)',('fileStore','objectStore','parseJob','claimed'))
 helpers+=recovery_helper('e2eCompleteOCRAndAssertParseHandoff',handoffphase,'OCR 成功与一个排队解析必须同次提交，后续重启只使用持久化输入。')
 helpers+=recovery_helper('e2eCreateRestartedPaperParseExecutor',restartphase,'构造新的服务及执行器，沿用已提交的 OCR 输入；测试解析服务仍由外层保持开启。',' (*paper.ParseTaskExecutor, string)',('executor','parseTaskID'))
 # 新执行器 URL 是外层服务器的生命周期资源，作为具名参数传递。
 helpers=helpers.replace('func e2eCreateRestartedPaperParseExecutor(t *testing.T, recovery paperParseRecovery)', 'func e2eCreateRestartedPaperParseExecutor(t *testing.T, recovery paperParseRecovery, parserURL string)').replace('parser.URL','parserURL')
 helpers+=recovery_helper('e2eRollbackAndRecoverPaperParseCommit',injectphase,'触发器失败必须回滚任务及候选发布；删除注入后令原租约过期，由新执行器重领恢复。',' paper.PaperImportJob',('recovered',))
 helpers+=recovery_helper('e2eAssertPaperParseExactReceipt',receiptphase,'相同回调重放原收据，变更候选冲突，已完成任务不能再被执行器领取。',' (string, paper.PaperImportRunBinding, paper.PaperImportParseResult, paper.PaperImportParseResult)',('replayLeaseToken','binding','parseResult','changedResult'))
 helpers+=recovery_helper('e2eRacePaperHumanReviewAndSuccessor',racephase,'人工确认和新代次使用原开始屏障；先接受的人工字段保留，过时保存只允许明确冲突。',' paper.PaperImportJob')
 helpers+=recovery_helper('e2eAssertHistoricalPaperReceiptFenced',historicalphase,'旧代次准确收据可被确认但不能改写新运行，结果或输入哈希改变仍拒绝。')
 helpers=helpers.replace('func e2eAssertHistoricalPaperReceiptFenced(t *testing.T, recovery paperParseRecovery)', 'func e2eAssertHistoricalPaperReceiptFenced(t *testing.T, recovery paperParseRecovery, newRun paper.PaperImportJob)')
 body=body[:start]+'''
 recovery := paperParseRecovery{Gate: scenario}
 recovery.Files, recovery.Objects, recovery.Job, recovery.ClaimedOCR = e2eCreatePaperOCRRecovery(t, recovery)
 e2eCompleteOCRAndAssertParseHandoff(t, recovery)
'''+body[parser:restart]+'''
 recovery.Executor, recovery.TaskID = e2eCreateRestartedPaperParseExecutor(t, recovery, parser.URL)
 recovery.Recovered = e2eRollbackAndRecoverPaperParseCommit(t, recovery)
 recovery.ReplayLeaseToken, recovery.Binding, recovery.Result, recovery.ChangedResult = e2eAssertPaperParseExactReceipt(t, recovery)
 newRun := e2eRacePaperHumanReviewAndSuccessor(t, recovery)
 e2eAssertHistoricalPaperReceiptFenced(t, recovery, newRun)
'''
 return replace_function(s,'e2eAssertPaperOCRParseRecovery',body,helpers)
apply(path,'e2eAssertPaperOCRParseRecovery',recovery)
(report/'go-server-stages25.json').write_text(json.dumps({'files':ns['ns']['changed'],'issue_functions':ns['functions'],'checkpoint_relation':'after checkpoint02; requires subsequent scan'},ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
print(json.dumps(ns['functions']))
