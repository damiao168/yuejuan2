from pathlib import Path
import json
root=Path('D:/project/yuejuan');report=root/'reports/sonar-remediation-2026-10-11'
source=(report/'go_server_stages2.py').read_text(encoding='utf-8');ns={};exec(source[:source.index("\npath='services/")],ns)
function=ns['function'];replace_function=ns['replace_function'];apply=ns['apply']

path='services/api-gateway/internal/server/e2e_business_command_test.go';name='e2eInterruptedBusinessCommandRecovery'
def interrupted(s,body):
 start=body.index('\n\tresponses := make(chan');end=body.index('\n\tvar after int',start)
 phase=body[start:end]
 body=body[:start]+'\n\te2eAssertConcurrentReportTakeover(t, request)'+body[end:]
 helper='\n\n// 同一过期命令同时接管，至少一份完成；其余只允许 operation_in_progress，之后重放必须成功。\nfunc e2eAssertConcurrentReportTakeover(t *testing.T, request func() *httptest.ResponseRecorder) {\n\tt.Helper()'+phase+'\n}'
 return replace_function(s,name,body,helper)
apply(path,name,interrupted)

name='e2eBusinessCommandRecoveryStates'
def states(s,body):
 processing=body.index('\n\tprocessingID :=');terminal=body.index('\n\trejectedID :=');log=body.index('\n\tt.Log(',terminal)
 first=body[processing:terminal];second=body[terminal:log]
 body=body[:processing]+'\n\te2eAssertProcessingCommandRecovery(t, db, tenant, actor, insert)\n\te2eAssertTerminalCommandRecovery(t, db, tenant, actor, insert)'+body[log:]
 params='t *testing.T, db *sql.DB, tenant, actor string, insert func(string, string, string, int, string)'
 helpers='\n\n// 有效占位仍在处理中；过期占位带回服务器冻结载荷，别的租户或操作者不能接管。\nfunc e2eAssertProcessingCommandRecovery('+params+') {\n\tt.Helper()'+first+'\n}\n\n// 已拒绝、缺失业务回执、跨领域与身份歧义分别返回原终态，不能假定传输完成等于业务成功。\nfunc e2eAssertTerminalCommandRecovery('+params+') {\n\tt.Helper()'+second+'\n}'
 return replace_function(s,name,body,helpers)
apply(path,name,states)
(report/'go-server-stages5.json').write_text(json.dumps({'files':ns['ns']['changed'],'issue_functions':ns['functions']},ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
print(json.dumps(ns['functions']))
