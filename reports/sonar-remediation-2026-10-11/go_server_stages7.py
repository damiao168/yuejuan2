from pathlib import Path
import json
root=Path('D:/project/yuejuan');report=root/'reports/sonar-remediation-2026-10-11'
source=(report/'go_server_stages2.py').read_text(encoding='utf-8');ns={};exec(source[:source.index("\npath='services/")],ns)
function=ns['function'];replace_function=ns['replace_function'];apply=ns['apply']

path='services/api-gateway/internal/server/e2e_story056_calibration_test.go';name='TestStory056TemplateDifferenceCalibrationApprovalAndRevocationE2E'
def calibration(s,body):
 start=body.index('\n\tcreated := e2ePostJSON(');end=body.index('\n\tfirstCase :=',start)
 phase=body[start:end]
 body=body[:start]+'\n\tapproved := e2eApproveStory056Calibration(t, db, router, story056CalibrationActors{AdminToken: adminToken, ApproverToken: approverToken, ApproverID: approverID}, fixture)\n\tcalibrationID, approvedSession, approvedCases := approved.ID, approved.Session, approved.Cases\n'+body[end:]
 helpers='\n\ntype story056CalibrationActors struct {\n\tAdminToken, ApproverToken, ApproverID string\n}\n\ntype story056ApprovedCalibration struct {\n\tID string\n\tSession map[string]any\n\tCases []any\n}\n\n// 先冻结样本并完整盲标，创建者始终不能自批；只有独立审批者能冻结证据哈希。\nfunc e2eApproveStory056Calibration(t *testing.T, db *sql.DB, router http.Handler, actors story056CalibrationActors, fixture story056AcceptanceFixture) story056ApprovedCalibration {\n\tt.Helper()\n\tadminToken, approverToken, approverID := actors.AdminToken, actors.ApproverToken, actors.ApproverID'+phase+'\n\treturn story056ApprovedCalibration{ID: calibrationID, Session: approvedSession, Cases: approvedCases}\n}'
 return replace_function(s,name,body,helpers)
apply(path,name,calibration)

path='services/api-gateway/internal/server/e2e_story060_capture_recovery_test.go';name='TestStory060CaptureRecoveryE2EWithPostgresTestDatabase'
def capture(s,body):
 session=body.index('\n\tvar gradeID string');source=body.index('\n\tsourceAssetID :=');quality=body.index('\n\tvar sourceHash string');transaction=body.index('\n\tif _, err = db.Exec(`UPDATE agent_worker_task');override=body.index('\n\toverride := e2ePostJSON(')
 session_phase=body[session:source];materialize=body[quality:transaction];atomic=body[transaction:override];manual=body[override:]
 atomic=atomic.replace('leasedRun.ID','quality.RunID').replace('if _, err = db.Exec','if _, err := db.Exec').replace('if _, err = qualityStore.SubmitResultCommand','if _, err := qualityStore.SubmitResultCommand').replace('if err = db.QueryRow','if err := db.QueryRow')
 body=body[:session]+'\n\te2eAssertStory060SessionCommandIdentity(t, db, fixture, suffix)\n'+body[source:quality]+'\n\tquality := e2eMaterializeStory060RecoveredQuality(t, db, fixture, suffix, story060RecoveredCaptureSource{AssetID: sourceAssetID, FileID: reuploadedID})\n\te2eAssertStory060QualityAtomicity(t, db, fixture, quality)\n\te2eAssertStory060ManualQualityRecovery(t, db, router, adminToken, fixture, quality.PageID)\n'
 helpers='\n\n// 丢失创建响应后沿用同一命令身份，场次及其唯一学科考试都不能重复创建。\nfunc e2eAssertStory060SessionCommandIdentity(t *testing.T, db *sql.DB, fixture story056AcceptanceFixture, suffix string) {\n\tt.Helper()'+session_phase+'\n}'
 helpers+='\n\ntype story060RecoveredCaptureSource struct {\n\tAssetID, FileID string\n}\n\ntype story060QualityRecovery struct {\n\tPageID, RunID string\n\tStore *imagequality.PostgresStore\n\tTask workerruntime.Task\n\tInput imagequality.ResultInput\n}\n\n// 恢复采集页后再建立质量任务；Runtime 租约和质量源租约必须来自同一次领取。\nfunc e2eMaterializeStory060RecoveredQuality(t *testing.T, db *sql.DB, fixture story056AcceptanceFixture, suffix string, source story060RecoveredCaptureSource) story060QualityRecovery {\n\tt.Helper()\n\tsourceAssetID, reuploadedID := source.AssetID, source.FileID'+materialize+'\n\treturn story060QualityRecovery{PageID: submissionPageID, RunID: leasedRun.ID, Store: qualityStore, Task: qualityTask, Input: resultInput}\n}'
 helpers+='\n\n// 先破坏 Runtime 租约验证整笔回滚，再恢复原租约提交；页面、采集页和 Runtime 终态必须一起变化。\nfunc e2eAssertStory060QualityAtomicity(t *testing.T, db *sql.DB, fixture story056AcceptanceFixture, quality story060QualityRecovery) {\n\tt.Helper()\n\tqualityStore, qualityTask, resultInput := quality.Store, quality.Task, quality.Input'+atomic+'\n}'
 helpers+='\n\n// 人工放行保留原失败证据和操作者，并重新排队配准任务，不能只修改页面状态。\nfunc e2eAssertStory060ManualQualityRecovery(t *testing.T, db *sql.DB, router http.Handler, adminToken string, fixture story056AcceptanceFixture, submissionPageID string) {\n\tt.Helper()'+manual+'}'
 s=replace_function(s,name,body,helpers);return s.replace('"context"','"context"\n\t"database/sql"',1)
apply(path,name,capture)
(report/'go-server-stages7.json').write_text(json.dumps({'files':ns['ns']['changed'],'issue_functions':ns['functions'],'checkpoint_relation':'after checkpoint02 2026-10-11 18:01:04; requires subsequent scan'},ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
print(json.dumps(ns['functions']))
