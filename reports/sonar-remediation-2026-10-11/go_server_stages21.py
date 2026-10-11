from pathlib import Path
import json,re
root=Path('D:/project/yuejuan');report=root/'reports/sonar-remediation-2026-10-11'
source=(report/'go_server_stages2.py').read_text(encoding='utf-8');ns={};exec(source[:source.index("\npath='services/")],ns)
replace_function=ns['replace_function'];apply=ns['apply'];mask_go=ns['mask_go']
path='services/api-gateway/internal/server/e2e_exam_archive_commands_test.go';name='TestExamArchiveCommandsE2EWithPostgresTestDatabase'
fields={'db':'DB','store':'Store','scope':'AccessScope','actor':'ActorID','tenantID':'TenantID','schoolID':'SchoolID','suffix':'Suffix','item':'Original','inputFor':'InputFor'}

def helper(name,params,phase,comment,result='',extra=''):
 used=[(key,value) for key,value in fields.items() if re.search(r'\b'+key+r'\b',mask_go(phase))]
 aliases='\n\t'+', '.join(key for key,_ in used)+' := '+', '.join('fixture.'+field for _,field in used) if used else ''
 return '\n\n// '+comment+'\nfunc '+name+'('+params+')'+result+' {\n\tt.Helper()'+aliases+extra+phase+'\n}'

def archive(s,body):
 receipt=body.index('\n\tarchiveInput :=')
 revisions=body.index('\n\tmissing :=')
 projection=body.index('\n\t// 先生成一条')
 lock=body.index('\n\t// 在归档事务')
 lockchildren=body.index('\n\tfor _, child := range current.Exams',lock)
 writer=body.index('\n\twriterStarted, writerDone :=')
 wait=body.index('\n\twaitDeadline :=')
 commit=body.index('\n\tif err := archiveTx.Commit()')
 ack=body.index('\n\tif err := projectionStore.RefreshExam',commit)
 receipt_phase=body[receipt:revisions]+'\n\treturn restored'
 revision_phase=body[revisions:projection].replace('\n\tcurrent, err =','\n\tcurrent, err :=',1)+'\n\treturn current'
 projection_phase=body[projection:lock]+'\n\treturn archiveProjectionFixture{ExamID: projectionExamID, Store: projectionStore, Snapshot: projectionSnapshot, Before: beforeProjection, Owner: projectionOwner, Refresh: refresh}'
 locking=body[lockchildren:writer]
 wait_phase=body[wait:commit]
 acknowledgement=body[ack:]
 assignments=', '.join(field+': '+key for key,field in fields.items())
 body=body[:receipt]+'\n\tfixture := archiveCommandFixture{'+assignments+'}\n\trestored := e2eAssertArchiveCommandReceipts(t, ctx, fixture)\n\tcurrent := e2eAssertArchiveChildRevisionSet(t, ctx, fixture, restored)\n\tprojection := e2ePrepareArchiveProjectionClaim(t, ctx, fixture, current)'+body[lock:lockchildren]+'\n\te2eHoldArchiveBusinessLocks(t, ctx, archiveTx, fixture, current)'+body[writer:wait]+'\n\te2eAwaitArchiveWriterLock(t, ctx, db, writerPID, writerDone)'+body[commit:ack]+'\n\te2eAssertArchivedProjectionAcknowledgement(t, ctx, fixture, projection)\n'
 helpers='''

type archiveCommandFixture struct {
\tDB *sql.DB
\tStore *exam.PostgresStore
\tAccessScope auth.AccessScope
\tActorID, TenantID, SchoolID, Suffix string
\tOriginal exam.ManagementExam
\tInputFor func(exam.ManagementExam, string) exam.ManagementArchiveInput
}

type archiveProjectionFixture struct {
\tExamID string
\tStore *processing.PostgresStore
\tSnapshot func() string
\tBefore, Owner string
\tRefresh processing.ProjectionRefresh
}
'''
 params='t *testing.T, ctx context.Context, fixture archiveCommandFixture'
 helpers+=helper('e2eAssertArchiveCommandReceipts',params,receipt_phase,'归档、恢复和迟到重放按原顺序执行；回执不改变当前恢复状态，部分考试范围和其他操作者不能取到整场回执。',' exam.ManagementExam')
 helpers+=helper('e2eAssertArchiveChildRevisionSet',params+', restored exam.ManagementExam',revision_phase,'子科修订集合必须完整；新增科目不改变父级版本，也必须让用户确认过的旧子集归档请求冲突。',' exam.ManagementExam')
 helpers+=helper('e2ePrepareArchiveProjectionClaim',params+', current exam.ManagementExam',projection_phase,'先生成有开放异常的投影并领取原请求版本；冻结快照在归档和确认领取之后再次比较。',' archiveProjectionFixture')
 helpers+=helper('e2eHoldArchiveBusinessLocks','t *testing.T, ctx context.Context, archiveTx *sql.Tx, fixture archiveCommandFixture, current exam.ManagementExam',locking,'父场归档前先持有所有子科共享业务锁；事务创建、提交和 defer 回滚仍由主测试持有，不能提前释放。')
 helpers+='\n\n// 原写入连接仍由主测试持有；先观察数据库确实等待锁，才允许主测试提交归档事务。\nfunc e2eAwaitArchiveWriterLock(t *testing.T, ctx context.Context, db *sql.DB, writerPID int, writerDone <-chan error) {\n\tt.Helper()'+wait_phase+'\n}'
 aliases='\n\tprojectionStore, projectionExamID, projectionOwner, refresh, projectionSnapshot, beforeProjection := projection.Store, projection.ExamID, projection.Owner, projection.Refresh, projection.Snapshot, projection.Before'
 helpers+=helper('e2eAssertArchivedProjectionAcknowledgement',params+', projection archiveProjectionFixture',acknowledgement,'归档后的投影确认只释放本次领取并保留旧异常快照，后来请求的版本仍不能被吞掉。',extra=aliases)
 s=replace_function(s,name,body,helpers)
 return s.replace('"context"','"context"\n\t"database/sql"',1)
apply(path,name,archive)
(report/'go-server-stages21.json').write_text(json.dumps({'files':ns['ns']['changed'],'issue_functions':ns['functions'],'checkpoint_relation':'after checkpoint02; requires subsequent scan'},ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
print(json.dumps(ns['functions']))
