from pathlib import Path
import json
root=Path('D:/project/yuejuan');report=root/'reports/sonar-remediation-2026-10-11'
source=(report/'go_server_stages2.py').read_text(encoding='utf-8');ns={};exec(source[:source.index("\npath='services/")],ns)
replace_function=ns['replace_function'];apply=ns['apply'];callback=ns['callback']
path='services/api-gateway/internal/server/e2e_auth_security_test.go';name='TestTeacherAccountSecurityE2EWithPostgres'

def helper(name,params,body,comment,result='',aliases=''):
 return '\n\n// '+comment+'\nfunc '+name+'('+params+')'+result+' {\n\tt.Helper()'+aliases+body+'\n}'

def security(s,body):
 migration=body.index('\n\te2eApplyPostgresMigrationsThrough')
 activate=body.index('\n\te2eActivatePostgresDemoUsers')
 invite=body.index('\n\tcreated := e2ePostJSON')
 storage=body.index('\n\tstore := auth.NewPostgresStore')
 risk=body.index('\n\tif _, err := store.LoadLoginRiskContext')
 device=body.index('\n\tvar storedDeviceHash')
 public=body.index('\n\tpublicToken :=')
 recovery=body.index('\n\tif _, err := db.Exec(`UPDATE auth_session SET reauthenticated_at')
 events=body.index('\n\tfreshToken :=')
 migration_phase=body[migration:activate]
 login_phase=body[invite:storage]+'\n\treturn teacherAccountSecurityLogin{User: user, UserID: userID, PhoneSession: phoneSession, EmployeeSession: employeeSession, DeviceCookie: deviceCookie, RecognizedSession: recognizedSession}'
 risk_phase=body[risk:device];device_phase=body[device:public];public_phase=body[public:recovery];recovery_phase=body[recovery:events]
 public_phase,lockrace=callback(s,public_phase,'t.Run("in-flight verification','e2eAssertPublicLockWinsInFlightVerification','t *testing.T, ctx context.Context, fixture teacherAccountSecurityFixture, publicToken string','t, ctx, fixture, publicToken','同一原子测试保留三次时间顺序：锁定、旧验证不能解锁、锁后新验证才解锁。')
 lockrace=lockrace.replace('\n\tt.Helper()','\n\tt.Helper()\n\tstore, oldUser, userID := fixture.Store, fixture.OldUser, fixture.Login.UserID',1)
 tail=body[events:]
 tail,legacy=callback(s,tail,'t.Run("legacy bcrypt rehash"','e2eAssertLegacyTeacherPasswordUpgrade','t *testing.T, ctx context.Context, fixture teacherAccountSecurityFixture','t, ctx, fixture','原有子测试保留旧 bcrypt 登录前后顺序，成功登录才要求升级为 Argon2id。')
 legacy=legacy.replace('\n\tt.Helper()','\n\tt.Helper()\n\tdb, router, store := fixture.DB, fixture.Router, fixture.Store',1)
 tail,concurrent=callback(s,tail,'t.Run("concurrent one-time','e2eAssertConcurrentTeacherCredentialConsumption','t *testing.T, ctx context.Context, fixture teacherAccountSecurityFixture','t, ctx, fixture','激活和恢复各保留八路并发消费及唯一成功者；之后独立核对改密和停用使旧恢复授权失效。')
 concurrent=concurrent.replace('\n\tt.Helper()','\n\tt.Helper()\n\tstore, schoolID := fixture.Store, fixture.SchoolID',1)
 password=concurrent.rfind('\n',0,concurrent.index('pending := auth.HashToken("pending-before-password-change")'))
 disabled=concurrent.rfind('\n',0,concurrent.index('pending = auth.HashToken("pending-before-disable")'))
 ending=concurrent.rfind('}')
 password_phase=concurrent[password:disabled]
 disabled_phase=concurrent[disabled:ending].replace('pending =','pending :=',1)
 concurrent=concurrent[:password]+'\n\te2eAssertPasswordChangeInvalidatesPendingRecovery(t, ctx, store, actor, scope, invite.ID, now)\n\te2eAssertDisableInvalidatesPendingRecovery(t, ctx, store, actor, scope, invite.ID, now)\n}'
 aliases='\n\tdb, router, store, oldUser, userID := fixture.DB, fixture.Router, fixture.Store, fixture.OldUser, fixture.Login.UserID'
 public_helper=helper('e2eAssertPublicComputerLockAndReauthentication','t *testing.T, ctx context.Context, fixture teacherAccountSecurityFixture',public_phase,'公共设备会话先持久化类型和锁，再重新验证并清锁；原有竞态子测试在同一位置执行。',aliases=aliases)
 recovery_helper=helper('e2eRecoverTeacherCredentialAndRevokeOldIdentities','t *testing.T, ctx context.Context, fixture teacherAccountSecurityFixture',recovery_phase,'先要求管理员近期认证，再消费一次性恢复令牌；恢复后旧会话、浏览器绑定、旧 epoch 与已验证旧密码均应失效。',aliases=aliases+'\n\tschoolAdminToken, phoneSession, employeeSession := fixture.SchoolAdminToken, fixture.Login.PhoneSession, fixture.Login.EmployeeSession')
 body=body[:migration]+'\n\te2eAssertLegacyPhoneMigration(t, db)'+body[activate:invite]+'\n\tlogin := e2eActivateInvitedTeacherAndLogin(t, router, schoolAdminToken)\n\tuser, userID := login.User, login.UserID'+body[storage:risk]+'''\n\tfixture := teacherAccountSecurityFixture{DB: db, Router: router, Store: store, OldUser: oldUser, Login: login, SchoolID: schoolID, SchoolAdminToken: schoolAdminToken}
\te2eAssertTeacherLoginRiskFacts(t, ctx, fixture)
\te2eAssertPasswordObservedDeviceFacts(t, fixture)
\te2eAssertPublicComputerLockAndReauthentication(t, ctx, fixture)
\te2eRecoverTeacherCredentialAndRevokeOldIdentities(t, ctx, fixture)
'''+tail
 helpers='\n\ntype teacherAccountSecurityLogin struct {\n\tUser map[string]any\n\tUserID, PhoneSession, EmployeeSession string\n\tDeviceCookie, RecognizedSession *http.Cookie\n}\n\ntype teacherAccountSecurityFixture struct {\n\tDB *sql.DB\n\tRouter http.Handler\n\tStore *auth.PostgresStore\n\tOldUser auth.UserWithPassword\n\tLogin teacherAccountSecurityLogin\n\tSchoolID, SchoolAdminToken string\n}'
 helpers+=helper('e2eAssertLegacyPhoneMigration','t *testing.T, db *sql.DB',migration_phase,'先写入有歧义和唯一的旧手机号再完整升级，只有唯一号码允许绑定归一化登录身份。')
 helpers+=helper('e2eActivateInvitedTeacherAndLogin','t *testing.T, router http.Handler, schoolAdminToken string',login_phase,'邀请状态不能由状态编辑激活；重发令牌撤销旧令牌，完成激活后分别使用手机号、工号和浏览器登录。',' teacherAccountSecurityLogin',aliases='\n\tschoolID := ""')
 # 邀请请求必须使用原学校，不能用空值或从登录结果推导。
 helpers=helpers.replace('router http.Handler, schoolAdminToken string) teacherAccountSecurityLogin','router http.Handler, schoolAdminToken, schoolID string) teacherAccountSecurityLogin').replace('\n\tschoolID := ""','')
 body=body.replace('e2eActivateInvitedTeacherAndLogin(t, router, schoolAdminToken)','e2eActivateInvitedTeacherAndLogin(t, router, schoolAdminToken, schoolID)')
 helpers+=helper('e2eAssertTeacherLoginRiskFacts','t *testing.T, ctx context.Context, fixture teacherAccountSecurityFixture',risk_phase,'同一次手机号登录必须保存认证等级、安全 epoch、版本化风险元数据和唯一风险事件。',aliases='\n\tdb, store, oldUser, phoneSession := fixture.DB, fixture.Store, fixture.OldUser, fixture.Login.PhoneSession')
 helpers+=helper('e2eAssertPasswordObservedDeviceFacts','t *testing.T, fixture teacherAccountSecurityFixture',device_phase,'浏览器绑定仅持久化不透明哈希；二次登录可识别设备，但密码观察所得设备不能变成 MFA 信任。',aliases='\n\tdb, oldUser, userID, deviceCookie, recognizedSession := fixture.DB, fixture.OldUser, fixture.Login.UserID, fixture.Login.DeviceCookie, fixture.Login.RecognizedSession')
 helpers+=public_helper+lockrace+recovery_helper+legacy+concurrent
 params='t *testing.T, ctx context.Context, store *auth.PostgresStore, actor auth.UserWithPassword, scope auth.AccessScope, inviteID string, now time.Time'
 helpers+=helper('e2eAssertPasswordChangeInvalidatesPendingRecovery',params,password_phase.replace('invite.ID','inviteID'),'改密前建立的恢复授权必须同时失去查询和消费能力，不能替换新凭据。')
 helpers+=helper('e2eAssertDisableInvalidatesPendingRecovery',params,disabled_phase.replace('invite.ID','inviteID'),'停用再启用用户后，停用前签发的恢复授权仍不能查询或消费，保留原状态更新顺序。')
 s=replace_function(s,name,body,helpers)
 return s.replace('"context"','"context"\n\t"database/sql"',1)
apply(path,name,security)
(report/'go-server-stages13.json').write_text(json.dumps({'files':ns['ns']['changed'],'issue_functions':ns['functions'],'checkpoint_relation':'after checkpoint02; requires subsequent scan'},ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
print(json.dumps(ns['functions']))
