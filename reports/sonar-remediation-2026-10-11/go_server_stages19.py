from pathlib import Path
import json,re
root=Path('D:/project/yuejuan');report=root/'reports/sonar-remediation-2026-10-11'
source=(report/'go_server_stages2.py').read_text(encoding='utf-8');ns={};exec(source[:source.index("\npath='services/")],ns)
function=ns['function'];replace_function=ns['replace_function'];apply=ns['apply'];callback=ns['callback'];mask_go=ns['mask_go']
path='services/api-gateway/internal/server/e2e_review_recovery_test.go';name='TestReviewRepairsRecoveryWithPostgresTestDatabase'
fields={'db':'DB','router':'Router','adminToken':'AdminToken','f':'Acceptance','answers':'Answers'}

def helper(helpername,params,phase,comment,result='',extra=''):
 used=[(key,value) for key,value in fields.items() if re.search(r'\b'+key+r'\b',mask_go(phase))]
 aliases='\n\t'+', '.join(key for key,_ in used)+' := '+', '.join('fixture.'+field for _,field in used) if used else ''
 return '\n\n// '+comment+'\nfunc '+helpername+'('+params+')'+result+' {\n\tt.Helper()'+aliases+extra+phase+'\n}'

def recovery(s,body):
 body=body.replace('\n\tctx := context.Background()','\n\tctx := context.Background()\n\tfixture := reviewRepairRecoveryFixture{DB: db, Router: router, AdminToken: adminToken, Acceptance: f, Answers: answers}',1)
 helpers='\n\ntype reviewRepairRecoveryFixture struct {\n\tDB *sql.DB\n\tRouter http.Handler\n\tAdminToken string\n\tAcceptance story056AcceptanceFixture\n\tAnswers []repairAnswer\n}'
 labels=[('seed follows normal','e2eAssertHiddenSeedAuthenticatedWorkflow','种子题仍走正常登录、领取、上下文和提交路径；不能泄漏到普通任务表。'),('upload retry','e2eAssertCaptureUploadFailureAndLeaseRecovery','同一批次先复现存储失败再复现过期租约，恢复后核对两份独立注册。'),('terminal quality retry','e2eAssertTerminalQualityRetryAtomicity','来源和 worker 重试必须原子恢复，故障回滚后才领取新租约和提交成功结果。')]
 extracted={}
 for label,helpername,comment in labels:
  body,raw=callback(s,body,'t.Run("'+label,helpername,'t *testing.T, ctx context.Context, fixture reviewRepairRecoveryFixture','t, ctx, fixture',comment)
  rawbody=function(raw,helpername)[2].removeprefix('\n\tt.Helper()')
  extracted[helpername]=rawbody
 seed=extracted[labels[0][1]]
 helpers+=helper(labels[0][1],'t *testing.T, ctx context.Context, fixture reviewRepairRecoveryFixture',seed,labels[0][2])
 upload=extracted[labels[1][1]]
 loop=upload.index('\nfor i :=');registered=upload.index('\nregistered, err :=',loop)
 loop_body=upload[loop:registered];opening=loop_body.index('{');closing=loop_body.rfind('}')
 item=loop_body[opening+1:closing]
 # 单页阶段原位调用，不新增子测试，也不改变 first failure 与 lease takeover 的顺序。
 outage=item.index('\n\tif i == 0 {');leasebranch=item.index('} else {',outage);completed=item.index('\n\tcompleted, err :=',leasebranch)
 branch_end=item.rfind('}',leasebranch,completed)
 outage_body=item[outage+len('\n\tif i == 0 {'):leasebranch]
 lease_body=item[leasebranch+len('} else {'):branch_end]
 item=item[:outage]+'\n\tif i == 0 {\n\t\tfailedAssetID = e2eAssertFailedUploadAssetReuse(t, ctx, fixture, upload, id, hash)\n\t} else {\n\t\te2eAssertUploadLeaseTakeover(t, ctx, fixture, upload, id)\n\t}'+item[completed:]
 outage_body=outage_body.replace('\n\t\tfailedAssetID = asset.ID','\n\t\treturn asset.ID')
 # helper 内首次完成错误来自局部声明，后面的查询仍保留同一错误检查。
 outage_body=outage_body.replace('if _, err = svc.Complete','if _, err := svc.Complete',1)
 item=item.replace('svc.Init','upload.Service.Init').replace('svc.AppendChunk','upload.Service.AppendChunk').replace('svc.Complete','upload.Service.Complete').replace('batch.ID','upload.Batch.ID')
 upload=upload[:loop]+'\nupload := repairUploadRecovery{Store: store, Files: fileStore, Service: svc, Batch: batch}\nfor i := 0; i < 2; i++ {\n\te2eRecoverOneCaptureUpload(t, ctx, fixture, upload, i)\n}'+upload[registered:]
 helpers+='\n\ntype repairUploadRecovery struct {\n\tStore *captureupload.PostgresStore\n\tFiles *files.PostgresStore\n\tService *captureupload.Service\n\tBatch capture.Batch\n}'
 common='t *testing.T, ctx context.Context, fixture reviewRepairRecoveryFixture, upload repairUploadRecovery'
 helpers+=helper(labels[1][1],'t *testing.T, ctx context.Context, fixture reviewRepairRecoveryFixture',upload,labels[1][2])
 helpers+=helper('e2eRecoverOneCaptureUpload',common+', i int',item,'每个原循环项先完成分块，再按对应故障恢复；存储失败项必须复用原文件资产，最终完成状态仍逐项检查。')
 helpers+=helper('e2eAssertFailedUploadAssetReuse',common+', id, hash string',outage_body,'存储故障先留下可查到的资产，返回该身份给恢复结果核对，不能悄悄创建替代资产。',' string',extra='\n\tsvc, fileStore, batch := upload.Service, upload.Files, upload.Batch')
 helpers+=helper('e2eAssertUploadLeaseTakeover',common+', id string',lease_body,'实时租约不能接管，过期后必须换 token；旧负责人不能重置或完成，第二次过期才交回服务恢复。',extra='\n\tstore := upload.Store')
 quality=extracted[labels[2][1]]
 claimstart=quality.index('\nclaim := func() workerruntime.Task {');first=quality.index('\nfirst := claim()',claimstart)
 claim=quality[claimstart+len('\nclaim := func() workerruntime.Task {'):first]
 claim=claim[:claim.rfind('}')]
 quality=quality[:claimstart]+'\nretry := repairQualityRetry{Quality: quality, Runtime: runtime, Run: runs[0]}'+quality[first:]
 quality=quality.replace('first := claim()','first := e2eClaimRepairQualityTask(t, ctx, fixture, retry)').replace('second := claim()','second := e2eClaimRepairQualityTask(t, ctx, fixture, retry)')
 projector=quality.index('\nprojector :=');fault=quality.index('\n// 运行时重新入队');retried=quality.index('\nretried, err :=',fault)
 projection=quality[projector:fault]+'\nreturn exceptionID'
 rollback=quality[fault:retried].replace('\n_, err = db.Exec','\n_, err := db.Exec',1)
 quality=quality[:projector]+'\nprojector, exceptionID := e2eFindRepairQualityException(t, ctx, fixture, retry)\ne2eAssertRepairQualityRetryRollback(t, ctx, fixture, retry, projector, first)'+quality[retried:]
 projection=projection.replace('\nreturn exceptionID','\nreturn projector, exceptionID')
 helpers+='\n\ntype repairQualityRetry struct {\n\tQuality *imagequality.PostgresStore\n\tRuntime *workerruntime.PostgresStore\n\tRun imagequality.Run\n}'
 retryparams='t *testing.T, ctx context.Context, fixture reviewRepairRecoveryFixture, retry repairQualityRetry'
 # 原 runs 切片只有一个元素；具名运行仍引用同一值，保留所有原 ID/租约参数。
 claim=claim.replace('runs[0]','retry.Run')
 projection=projection.replace('runs[0]','retry.Run')
 rollback=rollback.replace('runs[0]','retry.Run')
 helpers+=helper(labels[2][1],'t *testing.T, ctx context.Context, fixture reviewRepairRecoveryFixture',quality,labels[2][2])
 helpers+=helper('e2eClaimRepairQualityTask',retryparams,claim,'先领取运行时任务，再给来源运行记录同一租约和 attempt；首次失败与再次成功各调用一次。',' workerruntime.Task',extra='\n\truntime, quality := retry.Runtime, retry.Quality')
 helpers+=helper('e2eFindRepairQualityException',retryparams,projection,'终态失败投影后查找匹配来源运行的异常，保留原顺序和未找到时的 Fatal。',' (*processing.PostgresStore, string)')
 helpers+=helper('e2eAssertRepairQualityRetryRollback',retryparams+', projector *processing.PostgresStore, first workerruntime.Task',rollback,'故障触发器使来源重排失败时，运行时和来源两项事实都必须回滚；移除触发器后才允许真实重试。',extra='\n\truntime := retry.Runtime')
 s=replace_function(s,name,body,helpers)
 return s.replace('"context"','"context"\n\t"database/sql"',1)
apply(path,name,recovery)
(report/'go-server-stages19.json').write_text(json.dumps({'files':ns['ns']['changed'],'issue_functions':ns['functions'],'checkpoint_relation':'after checkpoint02; requires subsequent scan'},ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
print(json.dumps(ns['functions']))
