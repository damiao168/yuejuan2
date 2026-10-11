from pathlib import Path
import re,json
root=Path('D:/project/yuejuan');report=root/'reports/sonar-remediation-2026-10-11'
source=(report/'go_server_stages2.py').read_text(encoding='utf-8');ns={};exec(source[:source.index("\npath='services/")],ns)
function=ns['function'];replace_function=ns['replace_function'];save=ns['save'];mask_go=ns['mask_go']
repairs=[]
def repair(file,name,transform):
 path='services/api-gateway/internal/server/'+file;s=(root/path).read_text(encoding='utf-8');_,_,body=function(s,name);updated=transform(body)
 if updated==body:return
 save(path,replace_function(s,name,updated,''));repairs.append({'file':path,'function':name})
repair('e2e_paper_import_reconciliation_test.go','e2eRacePaperHumanReviewAndSuccessor',lambda b:b.replace('ctx, store, tenantID, userID, job := recovery.Gate.Context, recovery.Gate.Store, recovery.Gate.TenantID, recovery.Gate.UserID, recovery.Gate.Job','ctx, store, tenantID, userID := recovery.Gate.Context, recovery.Gate.Store, recovery.Gate.TenantID, recovery.Gate.UserID'))
repair('e2e_reliability_command_test.go','e2eAssertActualClientCommandResources',lambda b:b.replace(', commandID :=', ' :=').replace(', scenario.CommandID',''))
repair('e2e_reliability_command_test.go','e2eFailPreparationAttempt',lambda b:b.replace('db, paperStore, claimed := scenario.DB, scenario.Paper, scenario.OldClaim','db, paperStore := scenario.DB, scenario.Paper'))
for name in ['e2eAssertPreparationExhaustionRollback','e2eAssertFinalAbandonedRuntimeRecovery']:
 repair('e2e_reliability_command_test.go',name,lambda b:b.replace('\n\tvar err error','',1))
repair('e2e_review_recovery_test.go','e2eFindRepairQualityException',lambda b:b.replace('if err = projector.RefreshExam','if err := projector.RefreshExam',1))
(report/'go-server-batch02-compile-repairs.json').write_text(json.dumps(repairs,indent=2)+'\n',encoding='utf-8')
print(json.dumps(repairs))
