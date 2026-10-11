from pathlib import Path
import json,re
root=Path('D:/project/yuejuan');report=root/'reports/sonar-remediation-2026-10-11'
source=(report/'go_server_stages2.py').read_text(encoding='utf-8');ns={};exec(source[:source.index("\npath='services/")],ns)
replace_function=ns['replace_function'];apply=ns['apply'];mask_go=ns['mask_go'];function=ns['function']
path='services/api-gateway/internal/server/e2e_worker_runtime_reliability_test.go'
fields={'ctx':'Context','db':'DB','runtimeStore':'Runtime','tenantID':'TenantID'}
def helper(name,phase,comment,args='',returns=''):
 used=[(key,value) for key,value in fields.items() if re.search(r'\b'+key+r'\b',mask_go(phase))]
 aliases='\n\t'+', '.join(key for key,_ in used)+' := '+', '.join('scenario.'+field for _,field in used) if used else ''
 return '\n\n// '+comment+'\nfunc '+name+'(t *testing.T, scenario workerRuntimeReliabilityScenario'+args+')'+returns+' {\n\tt.Helper()'+aliases+phase+'\n}'
def main(s,body):
 body=body.replace('\n\truntimeStore := workerruntime.NewPostgresStore(db)','\n\truntimeStore := workerruntime.NewPostgresStore(db)\n\tscenario := workerRuntimeReliabilityScenario{Context: ctx, DB: db, Runtime: runtimeStore, TenantID: tenantID}',1)
 specs=[('expired final attempt','e2eAssertExhaustedRuntimeDeadLetter','最终尝试过期后十二个并发领取都不能再租用，原尝试必须原子关闭。'),('transaction primitives','e2eAssertRuntimeTransactionOwnership','可信成功与失败原语均由调用者提交，原事务回滚保护保留到整个场景结束。'),('expired lease cannot','e2eAssertRuntimeStaleLeaseRace','旧租约完成和心跳与新领取同时开始，过时写入被隔离，新持有者保留完整尝试历史。'),('ocr processing start','e2eAssertOCRStartIdempotency','并发恢复必须复用首次开始时间；失败可重试，已完成来源不能重新开始。')]
 helpers='\n\ntype workerRuntimeReliabilityScenario struct {\n\tContext context.Context\n\tDB *sql.DB\n\tRuntime *workerruntime.PostgresStore\n\tTenantID string\n}'
 for label,name,comment in specs:
  start=body.index('func(t *testing.T) {',body.index(label));opening=body.index('{',start);masked=mask_go(body);depth=1;i=opening+1
  while depth:
   if masked[i]=='{':depth+=1
   elif masked[i]=='}':depth-=1
   i+=1
  phase=re.sub(r'^\t','',body[opening+1:i-1],flags=re.M)
  body=body[:opening+1]+'\n\t\t'+name+'(t, scenario)\n\t'+body[i-1:]
  helpers+=helper(name,phase,comment)
 return replace_function(s,'TestWorkerRuntimeReliabilityWithPostgresTestDatabase',body,helpers)
apply(path,'TestWorkerRuntimeReliabilityWithPostgresTestDatabase',main)

def exhaustion(s,body):
 start=body.index('\n\tcreated, err :=');parallel=body.index('\n\tvar claimCount')
 setup=body[start:parallel]+'\n\treturn created'
 body=body[:start]+'\n\tcreated := e2eCreateExpiredFinalRuntimeAttempt(t, scenario)'+body[parallel:]
 return replace_function(s,'e2eAssertExhaustedRuntimeDeadLetter',body,helper('e2eCreateExpiredFinalRuntimeAttempt',setup,'先领取唯一允许的尝试再由数据库令租约过期，后续并发领取共享这一终态前提。',returns=' workerruntime.Task'))
apply(path,'e2eAssertExhaustedRuntimeDeadLetter',exhaustion)
def transaction(s,body):
 start=body.index('\n\tcreate :=');failed=body.index('\n\tfailedSource :=')
 trusted=body[start:failed];failure=body[failed:]
 body=body[:start]+'\n\te2ePersistTrustedRuntimeSuccess(t, scenario, tx)\n\te2eCommitRuntimeFailure(t, scenario)\n'
 helpers=helper('e2ePersistTrustedRuntimeSuccess',trusted,'不同 JSON 键顺序及耗时仍重放同一成功任务，调用者事务提交后才开始失败任务场景。',', tx *sql.Tx')
 helpers+=helper('e2eCommitRuntimeFailure',failure,'失败领取沿用其租约，失败原语出错先回滚，成功则由调用者提交终态。')
 return replace_function(s,'e2eAssertRuntimeTransactionOwnership',body,helpers)
apply(path,'e2eAssertRuntimeTransactionOwnership',transaction)
def race(s,body):
 start=body.index('\n\tcreated, err :=');parallel=body.index('\n\tstart :=');history=body.index('\n\tstored, err :=')
 setup=body[start:parallel]+'\n\treturn created, claimed[0]'
 # 原并发通道及等待全部保留，只用已领取的单一旧任务表达租约身份。
 race=body[parallel:history].replace('claimed[0].','oldTask.').replace('reclaimed, err =','reclaimed, err =')
 race='\n\tvar err error'+race
 historyphase=body[history:]
 body=body[:start]+'\n\tcreated, oldTask := e2eCreateExpiredRuntimeRace(t, scenario)\n\te2eRaceStaleRuntimeOperations(t, scenario, created, oldTask)\n\te2eAssertReclaimedRuntimeHistory(t, scenario, created)\n'
 helpers=helper('e2eCreateExpiredRuntimeRace',setup,'建立可重领的过期租约；旧任务身份在全部并发操作中保持一致。',returns=' (workerruntime.Task, workerruntime.Task)')
 helpers+=helper('e2eRaceStaleRuntimeOperations',race,'领取、旧完成及旧心跳共用开始屏障；SKIP LOCKED 空结果只在过时操作都返回后重试。',', created, oldTask workerruntime.Task')
 helpers+=helper('e2eAssertReclaimedRuntimeHistory',historyphase,'重领仍处于 leased 且第二次尝试，旧完成不能留下结果，历史保留原过期尝试。',', created workerruntime.Task')
 return replace_function(s,'e2eAssertRuntimeStaleLeaseRace',body,helpers)
apply(path,'e2eAssertRuntimeStaleLeaseRace',race)
def ocr(s,body):
 fail=body.index('\n\tif _, err := ocrStore.FailTask')
 recovery=body[fail:]
 body=body[:fail]+'\n\te2eAssertOCRFailureRetryAndTerminalFence(t, scenario, ocrStore, task)\n'
 return replace_function(s,'e2eAssertOCRStartIdempotency',body,helper('e2eAssertOCRFailureRetryAndTerminalFence',recovery,'并发开始结束后才使来源失败再重试，清除失败残留；完成的 OCR 来源则拒绝再次开始。',', ocrStore *ocrpkg.PostgresStore, task ocrpkg.Task'))
apply(path,'e2eAssertOCRStartIdempotency',ocr)
(report/'go-server-stages24.json').write_text(json.dumps({'files':ns['ns']['changed'],'issue_functions':ns['functions'],'checkpoint_relation':'after checkpoint02; requires subsequent scan'},ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
print(json.dumps(ns['functions']))
