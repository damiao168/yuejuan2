from pathlib import Path
import json
root=Path('D:/project/yuejuan');report=root/'reports/sonar-remediation-2026-10-11'
source=(report/'go_server_stages2.py').read_text(encoding='utf-8');ns={};exec(source[:source.index("\npath='services/")],ns)
function=ns['function'];replace_function=ns['replace_function'];save=ns['save']
repairs=[]
def repair(file,name,transform):
 path='services/api-gateway/internal/server/'+file;s=(root/path).read_text(encoding='utf-8');_,_,body=function(s,name);updated=transform(body)
 if updated==body:raise RuntimeError(name+' repair did not match')
 save(path,replace_function(s,name,updated,''));repairs.append({'file':path,'function':name})
repair('e2e_reliability_command_test.go','e2eAssertActualClientCommandResources',lambda b:b.replace('db, tenantID, userID, commandID, middlewareInput := scenario.DB, scenario.TenantID, scenario.UserID, scenario.MiddlewareInput','db, tenantID, userID, middlewareInput := scenario.DB, scenario.TenantID, scenario.UserID, scenario.MiddlewareInput',1).replace('for i := range commandIDs','for i, commandID := range commandIDs',1))
repair('e2e_subjective_command_test.go','e2eCompleteAndRecoverSubjectiveCommand',lambda b:b.replace('for name, identity := range map[string][2]string','for name, scopedIdentity := range map[string][2]string',1).replace('ctx, identity[0], identity[1], identity.IdempotencyKey','ctx, scopedIdentity[0], scopedIdentity[1], identity.IdempotencyKey',1))
repair('e2e_worker_runtime_reliability_test.go','e2eRaceStaleRuntimeOperations',lambda b:b.replace('reoldTask.', 'reclaimed[0].'))
def visibility(b):
 old='req := reviewAuthedRequest(http.MethodGet, "/api/v1/review-tasks/"+firstID, "", graderToken)\n\trec := httptest.NewRecorder()'
 assert old in b
 return b.replace(old,'req = reviewAuthedRequest(http.MethodGet, "/api/v1/review-tasks/"+firstID, "", graderToken)\n\trec = httptest.NewRecorder()',1)
repair('review_route_test.go','reviewAssertAssignedTaskVisibility',visibility)
(report/'go-server-batch03-compile-repairs.json').write_text(json.dumps(repairs,indent=2)+'\n',encoding='utf-8')
print(json.dumps(repairs))
