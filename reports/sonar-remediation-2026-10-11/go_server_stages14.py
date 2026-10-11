from pathlib import Path
import json
root=Path('D:/project/yuejuan');report=root/'reports/sonar-remediation-2026-10-11'
source=(report/'go_server_stages2.py').read_text(encoding='utf-8');ns={};exec(source[:source.index("\npath='services/")],ns)
replace_function=ns['replace_function'];apply=ns['apply']
path='services/api-gateway/internal/server/e2e_subjective_command_test.go';name='TestSubjectiveCommandRecoveryWithPostgresTestDatabase'

def command(s,body):
 concurrency=body.index('\n\ttype outcome struct')
 plan=body.index('\n\tplan := subjective.BatchEnqueuePlan')
 complete=body.index('\n\tif err = store.CompleteEnqueuePlan')
 count=body.index('\n\tvar count int',complete)
 concurrent=body[concurrency:plan]+'\n\treturn a.batch'
 receipt=body[complete:count].replace('a.batch.ID','identity.BatchID').replace('input.IdempotencyKey','identity.IdempotencyKey').replace('\n\tif err = store.CompleteEnqueuePlan','\n\tif err := store.CompleteEnqueuePlan',1).replace('\n\tif _, err = db.Exec','\n\tif _, err = db.Exec',1)
 body=body[:concurrency]+'\n\tbatch := e2eCreateConcurrentSubjectiveBatch(t, ctx, store, fixture, input)'+body[plan:complete].replace('a.batch.ID','batch.ID')+'\n\te2eCompleteAndRecoverSubjectiveCommand(t, ctx, db, store, restarted, fixture, subjectiveCommandIdentity{BatchID: batch.ID, IdempotencyKey: input.IdempotencyKey})'+body[count:].replace('a.batch.ID','batch.ID')
 helpers='\n\n// 两个并发创建请求先同时放行，再等待两个结果；相同请求共享批次，改变答案集合必须冲突。\nfunc e2eCreateConcurrentSubjectiveBatch(t *testing.T, ctx context.Context, store *subjective.PostgresStore, fixture story056AcceptanceFixture, input subjective.CreateBatchInput) subjective.GradingBatch {\n\tt.Helper()'+concurrent+'\n}'
 helpers+='\n\ntype subjectiveCommandIdentity struct {\n\tBatchID, IdempotencyKey string\n}\n\n// 完成排队后重建存储读取持久回执；删除业务批次仍可恢复原回执，但其他租户或操作者不能取得它。\nfunc e2eCompleteAndRecoverSubjectiveCommand(t *testing.T, ctx context.Context, db *sql.DB, store, restarted *subjective.PostgresStore, fixture story056AcceptanceFixture, identity subjectiveCommandIdentity) {\n\tt.Helper()'+receipt+'\n}'
 s=replace_function(s,name,body,helpers)
 return s.replace('"context"','"context"\n\t"database/sql"',1)
apply(path,name,command)
(report/'go-server-stages14.json').write_text(json.dumps({'files':ns['ns']['changed'],'issue_functions':ns['functions'],'checkpoint_relation':'after checkpoint02; requires subsequent scan'},ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
print(json.dumps(ns['functions']))
