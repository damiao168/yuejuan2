from pathlib import Path
import json,re
root=Path('D:/project/yuejuan');report=root/'reports/sonar-remediation-2026-10-11'
source=(report/'go_server_stages2.py').read_text(encoding='utf-8');ns={};exec(source[:source.index("\npath='services/")],ns)
replace_function=ns['replace_function'];apply=ns['apply'];mask_go=ns['mask_go'];function=ns['function']
source26=(report/'go_server_stages26.py').read_text(encoding='utf-8');exec(source26[source26.index('fields='):source26.index('def main(s,body):')],globals())
path='services/api-gateway/internal/server/e2e_reliability_command_test.go'
def block(body,label):
 start=body.index(label);opening=body.index('{',start);masked=mask_go(body);depth=1;i=opening+1
 while depth:
  if masked[i]=='{':depth+=1
  elif masked[i]=='}':depth-=1
  i+=1
 return start,opening,i,body[opening+1:i-1]

def actual_client(s,body):
 loop,opening,end,phase=block(body,'\n\tfor i, commandID :=')
 loopphase=body[loop:end].replace('evidence.CommandIDs','commandIDs').replace('evidence.SessionIDs','sessionIDs')
 body=body[:loop]+'\n\te2eAssertActualClientCommandResources(t, scenario, evidence.CommandIDs, evidence.SessionIDs)'+body[end:]
 return replace_function(s,'e2eAssertActualClientCommandRecovery',body,helper('e2eAssertActualClientCommandResources',loopphase,'逐命令核对真实客户端生成的父子资源，两个显式命令各只有一份。',args=', commandIDs, sessionIDs []string'))
apply(path,'e2eAssertActualClientCommandRecovery',actual_client)
def preparation(s,body):
 loop,opening,end,phase=block(body,'\n\tfor attempt :=')
 attemptphase=re.sub(r'^\t','',phase,flags=re.M)
 body=body[:opening+1]+'\n\t\te2eFailPreparationAttempt(t, scenario, ctx, prepared, attempt)\n\t'+body[end-1:]
 return replace_function(s,'e2eAssertPreparationRetryExhaustion',body,helper('e2eFailPreparationAttempt',attemptphase,'每次领取后先处理首轮过期边界，再失败并核对同代次退避或第三次耗尽。',args=', ctx context.Context, prepared paper.PaperImportJob, attempt int'))
apply(path,'e2eAssertPreparationRetryExhaustion',preparation)
def preparation_attempt(s,body):
 start,opening,end,phase=block(body,'\n\tif attempt == 1')
 expiry=re.sub(r'^\t','',phase,flags=re.M)
 body=body[:opening+1]+'\n\t\te2eAssertExpiredPreparationCallbacks(t, scenario, ctx, prepared, claimed, owner)\n\t'+body[end-1:]
 return replace_function(s,'e2eFailPreparationAttempt',body,helper('e2eAssertExpiredPreparationCallbacks',expiry,'首轮先让准备租约过期，失败和调度回调都拒绝；延长同一租约后再执行原正常失败路径。',args=', ctx context.Context, prepared, claimed paper.PaperImportJob, owner string',locals=('claimed',)))
apply(path,'e2eFailPreparationAttempt',preparation_attempt)
def abandoned(s,body):
 loop,opening,end,phase=block(body,'\n\tfor attempt :=');attemptphase=re.sub(r'^\t','',phase,flags=re.M)
 attemptphase=attemptphase.replace('\n\tlastClaim, lastOwner = claimed, owner','',1)+'\n\treturn claimed, owner'
 body=body[:opening+1]+'\n\t\tlastClaim, lastOwner = e2eRecoverAbandonedPreparationAttempt(t, scenario, ctx, prepared, attempt)\n\t'+body[end-1:]
 return replace_function(s,'e2eAssertAbandonedPreparationRecovery',body,helper('e2eRecoverAbandonedPreparationAttempt',attemptphase,'模拟每次准备持有者崩溃，先核对最终回滚再恢复，并拒绝原持有者的迟到回调。',' (paper.PaperImportJob, string)',('claimed',),', ctx context.Context, prepared paper.PaperImportJob, attempt int'))
apply(path,'e2eAssertAbandonedPreparationRecovery',abandoned)
def abandoned_attempt(s,body):
 helpers=''
 for label,name,comment,args in [('\n\tif attempt == 3','e2eAssertPreparationExhaustionRollback','最终准备耗尽故障不得提前改变作业、运行、持有者或来源状态，撤下注入后才继续恢复。',', ctx context.Context, prepared paper.PaperImportJob, owner string'),('\n\tif attempt < 3','e2eAssertAbandonedPreparationBackoff','前两次恢复仍保留处理状态及原尝试数，退避事实核对后再开放下一次领取。',', prepared paper.PaperImportJob, attempt int')]:
  start,opening,end,phase=block(body,label);phase=re.sub(r'^\t','',phase,flags=re.M)
  callargs=', ctx, prepared, owner' if '== 3' in label else ', prepared, attempt'
  body=body[:opening+1]+'\n\t\t'+name+'(t, scenario'+callargs+')\n\t'+body[end-1:]
  helpers+=helper(name,phase,comment,args=args)
 return replace_function(s,'e2eRecoverAbandonedPreparationAttempt',body,helpers)
apply(path,'e2eRecoverAbandonedPreparationAttempt',abandoned_attempt)
def final_attempt(s,body):
 start=body.index('\n\tinput :=');inject=body.index('\n\tif _, err := db.Exec(`CREATE FUNCTION reject_exhaustion_test')
 phase=body[start:inject]+'\n\treturn job, taskID'
 body=body[:start]+'\n\tjob, taskID := e2eCreateExhaustedImportAttempt(t, scenario, ctx)'+body[inject:]
 return replace_function(s,'e2eAssertFinalAbandonedRuntimeRecovery',body,helper('e2eCreateExhaustedImportAttempt',phase,'先创建一份只允许一次尝试的运行，领取后过期并由运行时关闭，后续协调才处理导入终态。',' (paper.PaperImportJob, string)',('input','job'),', ctx context.Context'))
apply(path,'e2eAssertFinalAbandonedRuntimeRecovery',final_attempt)
def capture_lifecycle(s,body):
 body,phase=extract_callback(body,'capture batch durable command','e2eAssertCaptureCommandRecovery',', ctx, examID')
 phase=re.sub(r'^\t','',phase,flags=re.M)
 helpers=helper('e2eAssertCaptureCommandRecovery',phase,'采集命令并发、删除后重放和传输收据丢失均使用同一生产存储及中间件。',locals=('store','input'),args=', ctx context.Context, examID string')
 start=body.index('\n\tbefore, err :=');active=body.index('\n\tdeleted, err :=')
 setup=body[start:active]+'\n\treturn before, captureStore, cpID, spID'
 tail=body[active:]
 body=body[:start]+'\n\tbefore, captureStore, cpID, spID := e2eCreateCaptureProjectionPage(t, scenario, ctx, examID)\n\te2eDeleteAndRestoreCaptureProjection(t, scenario, ctx, examID, captureProjectionPage{Before: before, Capture: captureStore, CapturePageID: cpID, SubmissionPageID: spID})\n'
 helpers+='\n\ntype captureProjectionPage struct {\n\tBefore processing.Summary\n\tCapture *capture.PostgresStore\n\tCapturePageID, SubmissionPageID string\n}'
 helpers+=helper('e2eCreateCaptureProjectionPage',setup,'生产采集前保存有效页统计，建立原提交与采集页关联后才验证新增一页。',' (processing.Summary, *capture.PostgresStore, string, string)',args=', ctx context.Context, examID string')
 helpers+=helper('e2eDeleteAndRestoreCaptureProjection','\n\tbefore, captureStore, cpID, spID := page.Before, page.Capture, page.CapturePageID, page.SubmissionPageID'+tail,'删除减去一页，恢复必须按新的生命周期修订恢复该页并重新打开原问题。',args=', ctx context.Context, examID string, page captureProjectionPage')
 return replace_function(s,'e2eAssertCaptureProjectionLifecycle',body,helpers)
apply(path,'e2eAssertCaptureProjectionLifecycle',capture_lifecycle)
def capture_command(s,body):
 transport=body.index('\n\thandler :=')
 phase=body[transport:];phase=phase.replace('.Scan(&count)', '.Scan(&count)')
 body=body[:transport]+'\n\te2eAssertCaptureLostReceiptTakeover(t, scenario, ctx, examID, fresh)\n'
 phase='\n\tvar count int'+phase
 return replace_function(s,'e2eAssertCaptureCommandRecovery',body,helper('e2eAssertCaptureLostReceiptTakeover',phase,'业务采集批次先提交而收据写入失败，恢复及准确重试只认同一个批次，接管不重复创建。',args=', ctx context.Context, examID string, fresh *capture.PostgresStore'))
apply(path,'e2eAssertCaptureCommandRecovery',capture_command)
def manual_resolution(s,body):
 loop=body.index('\n\tfor range 2');delete=body.index('\n\tif _, err = db.Exec(`UPDATE submission SET deleted_at',loop)
 phase=body[loop:delete]
 body=body[:loop]+'\n\te2eRefreshResolvedProjectionHistory(t, scenario, resolvedPageID, resolvedExceptionID)'+body[delete:]
 return replace_function(s,'e2eAssertManualProjectionResolutionPreserved',body,helper('e2eRefreshResolvedProjectionHistory',phase,'两次更新时间戳并刷新仍保留 operator_confirmed 终态，不把人工处理覆盖成开放异常。',args=', resolvedPageID, resolvedExceptionID string'))
apply(path,'e2eAssertManualProjectionResolutionPreserved',manual_resolution)
(report/'go-server-stages27.json').write_text(json.dumps({'files':ns['ns']['changed'],'issue_functions':ns['functions'],'checkpoint_relation':'after checkpoint02; requires subsequent scan'},ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
print(json.dumps(ns['functions']))
