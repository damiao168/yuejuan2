from pathlib import Path
import re,json
root=Path('D:/project/yuejuan');report=root/'reports/sonar-remediation-2026-10-11'
source=(report/'go_server_stages2.py').read_text(encoding='utf-8');ns={};exec(source[:source.index("\npath='services/")],ns)
function=ns['function'];replace_function=ns['replace_function'];save=ns['save'];callback=ns['callback'];apply=ns['apply']

path='services/api-gateway/internal/server/migration_upgrade_test.go';name='TestPostgresMigrationUpgradeFrom000154'
def upgrade(s,body):
 data=body.index('\n\tvar schoolName, studentName');models=body.index('\n\tfor index, configID := range fixture.modelIDs');binding=body.index('\n\t// 000156 添加绑定外键')
 phases=[('assertUpgradePreservedGradingGraph',body[data:models],'升级前的学校、学生、答卷、评分运行和成绩必须仍以原关系连接。'),
         ('assertUpgradePreservedModelCredentials',body[models:binding],'逐份检查历史密文、版本与成功测试时间，且仍能用原租户和配置身份解密。'),
         ('assertUpgradeModelBindingsAndDeletion',body[binding:],'先绑定历史 A/B/C 配置，再删除第四份未绑定配置；跨租户平台操作者的归属和历史密文必须保留。')]
 body=body[:data]+''.join('\n\t'+helper+'(t, ctx, db, fixture)' for helper,_,_ in phases)+'\n'
 helpers=''.join('\n\n// '+comment+'\nfunc '+helper+'(t *testing.T, ctx context.Context, db *sql.DB, fixture pre155UpgradeFixture) {\n\tt.Helper()'+phase+'\n}' for helper,phase,comment in phases)
 return replace_function(s,name,body,helpers)
apply(path,name,upgrade)

path='services/api-gateway/internal/server/managed_config_postgres_test.go';name='TestPostgresManagedConfigProbeTimestamps'
def managed(s,body):
 start=body.index('[]struct {');end=body.index('\n\t}{',start);fields=body[start+len('[]struct {'):end]
 body=body[:start]+'[]managedConfigProbeCase{'+body[end+len('\n\t}{'):]
 label='\n\tfor _, tc := range'
 loc=body.index(label);body=body[:loc]+'\n\tfixture := managedConfigProbeFixture{TenantID: tenantID, ActorID: actorID, Success: success, Failure: failure}\n'+body[loc:]
 declaration='type managedConfigProbeCase struct {'+fields+'\n}\n\ntype managedConfigProbeFixture struct {\n\tTenantID, ActorID string\n\tSuccess, Failure modelgovernance.ManagedAPIProbeResult\n}\n\n'
 body,helper=callback(s,body,'t.Run(tc.name','assertManagedConfigProbeLifecycle','t *testing.T, ctx context.Context, store *modelgovernance.PostgresStore, fixture managedConfigProbeFixture, tc managedConfigProbeCase','t, ctx, store, fixture, tc','连接失败保留最后成功时间；真正修改连接参数时才清除此前探测事实。',declaration)
 helper=helper.replace('\n\tt.Helper()','\n\tt.Helper()\n\ttenantID, actorID := fixture.TenantID, fixture.ActorID\n\tsuccess, failure := fixture.Success, fixture.Failure',1)
 return replace_function(s,name,body,helper)
apply(path,name,managed)

path='services/api-gateway/internal/server/e2e_mfa_test.go';name='e2eAssertMFATenantIsolation'
def isolation(s,body):
 start=body.index('\n\tfor _, table := range');end=body.index('\n\tfor _, scope := range',start)
 phase=body[start:end];inner=phase[phase.index('\n\t\tfor _, scope'):phase.rfind('\n\t}')]
 inner=re.sub(r'^\t\t','\t',inner,flags=re.M)
 body=body[:start]+'\n\tfor _, table := range []string{"auth_totp", "auth_mfa_recovery_code", "auth_mfa_challenge", "auth_security_notification_intent"} {\n\t\te2eAssertMFACredentialTableScope(t, db, tenantID, table)\n\t}\n'+body[end:]
 helper='\n\n// 仅当前租户可见这四种 MFA 凭据或通知事实；没有租户与错误租户都必须读到空集。\nfunc e2eAssertMFACredentialTableScope(t *testing.T, db *sql.DB, tenantID, table string) {\n\tt.Helper()'+inner+'\n}'
 return replace_function(s,name,body,helper)
apply(path,name,isolation)
(report/'go-server-stages3.json').write_text(json.dumps({'files':ns['ns']['changed'],'issue_functions':ns['functions']},ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
print(json.dumps(ns['functions']))
