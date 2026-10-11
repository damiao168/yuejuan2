from pathlib import Path
import json
root=Path('D:/project/yuejuan');report=root/'reports/sonar-remediation-2026-10-11'
source=(report/'go_server_stages2.py').read_text(encoding='utf-8');ns={};exec(source[:source.index("\npath='services/")],ns)
function=ns['function'];replace_function=ns['replace_function'];apply=ns['apply']
path='services/api-gateway/internal/server/e2e_mfa_test.go';name='TestTOTPManagementE2EWithPostgres'
def management(s,body):
 enroll=body.index('\n\tstatus := e2eGetJSON(');rotation=body.index('\n\trotation := e2ePostJSON(');isolated=body.index('\n\te2eAssertMFATenantIsolation(');finish=body.index('\n\t// 无需等待时间步推进');audit=body.index('\n\t// 秘密、挑战明文');facts=body.index('\n\te2eAssertMFASecurityNotificationFacts(')
 first=body[enroll:rotation]
 rotate=body[rotation:isolated].replace('code, err = totp.GenerateCode','code, err := totp.GenerateCode',1)
 terminal=body[finish:audit].replace('record, err = store.FindTOTP','record, err := store.FindTOTP',1).replace('challenge = e2eString(t, disable','challenge := e2eString(t, disable',1)
 trace=body[audit:facts].replace('if err = rows.Scan','if err := rows.Scan').replace('if err = rows.Err()','if err := rows.Err()')
 body=body[:enroll]+'\n\tfixture := e2eEnrollTOTPManagement(t, adminDB, scopedDB, router, mfaBrowserIdentity{Token: token, OtherToken: otherToken, Session: session})\n\tnewCodes := e2eRotateRecoveryWithNotificationRollback(t, adminDB, router, fixture)\n\te2eAssertMFATenantIsolation(t, scopedDB, fixture.TenantID)\n\tchallenge := e2eFinishTOTPRecoveryLifecycle(t, adminDB, router, fixture, newCodes)\n\te2eAssertMFAAuditSecretIsolation(t, adminDB, fixture, challenge, newCodes)\n\te2eAssertMFASecurityNotificationFacts(t, adminDB, fixture.TenantID, fixture.UserID, fixture.Secret, challenge, fixture.OldCodes[0].(string), newCodes[0].(string))\n\te2eAssertMFALogPublicationKeepsNotificationPending(t, scopedDB, fixture.TenantID)\n'
 types='\n\ntype mfaBrowserIdentity struct {\n\tToken, OtherToken string\n\tSession *http.Cookie\n}\n\ntype mfaEnrollmentFixture struct {\n\tToken, OtherToken, Secret, TenantID, UserID string\n\tOldCodes []any\n\tContext context.Context\n\tStore *auth.PostgresStore\n}\n'
 helpers=types+'\n// 先证明 cookie CSRF 与会话绑定，再启用凭据并检查数据库只保存密文。\nfunc e2eEnrollTOTPManagement(t *testing.T, adminDB, scopedDB *sql.DB, router http.Handler, browser mfaBrowserIdentity) mfaEnrollmentFixture {\n\tt.Helper()\n\ttoken, otherToken, session := browser.Token, browser.OtherToken, browser.Session'+first+'\n\treturn mfaEnrollmentFixture{Token: token, OtherToken: otherToken, Secret: secret, TenantID: tenantID, UserID: userID, OldCodes: oldCodes, Context: ctx, Store: store}\n}'
 helpers+='\n\n// 临时拒绝通知意图插入，恢复码和挑战均须回滚；移除故障后同一证明只允许成功一次。\nfunc e2eRotateRecoveryWithNotificationRollback(t *testing.T, adminDB *sql.DB, router http.Handler, fixture mfaEnrollmentFixture) []any {\n\tt.Helper()\n\ttoken, otherToken, secret, tenantID, userID := fixture.Token, fixture.OtherToken, fixture.Secret, fixture.TenantID, fixture.UserID\n\tctx, store := fixture.Context, fixture.Store'+rotate+'\n\treturn newCodes\n}'
 helpers+='\n\n// 并发证明只能有一个赢家；禁用后两份会话、凭据和可信设备绑定都必须失效。\nfunc e2eFinishTOTPRecoveryLifecycle(t *testing.T, adminDB *sql.DB, router http.Handler, fixture mfaEnrollmentFixture, newCodes []any) string {\n\tt.Helper()\n\ttoken, otherToken, tenantID, userID, oldCodes := fixture.Token, fixture.OtherToken, fixture.TenantID, fixture.UserID, fixture.OldCodes\n\tctx, store := fixture.Context, fixture.Store'+terminal+'\n\treturn challenge\n}'
 helpers+='\n\n// 审计逐条核对全部四种秘密，不能用数据库已加密替代审计载荷检查。\nfunc e2eAssertMFAAuditSecretIsolation(t *testing.T, adminDB *sql.DB, fixture mfaEnrollmentFixture, challenge string, newCodes []any) {\n\tt.Helper()\n\ttenantID, userID, secret, oldCodes := fixture.TenantID, fixture.UserID, fixture.Secret, fixture.OldCodes'+trace+'\n}'
 return replace_function(s,name,body,helpers)
apply(path,name,management)
(report/'go-server-stages6.json').write_text(json.dumps({'files':ns['ns']['changed'],'issue_functions':ns['functions']},ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
print(json.dumps(ns['functions']))
