from pathlib import Path
import json,re
root=Path('D:/project/yuejuan');report=root/'reports/sonar-remediation-2026-10-11'
source=(report/'go_server_stages2.py').read_text(encoding='utf-8');ns={};exec(source[:source.index("\npath='services/")],ns)
replace_function=ns['replace_function'];apply=ns['apply'];mask_go=ns['mask_go']
path='services/api-gateway/internal/server/e2e_reliability_command_test.go'
fields={'db':'DB','tenantID':'TenantID','userID':'UserID','examID':'ExamID','reviewImportID':'ReviewImportID','processingImportID':'ProcessingImportID','draft':'Draft','examCommandID':'CommandID','examSession':'Session','transportRequestHash':'RequestHash','runRepair':'RunRepair'}
def helper(name,phase,comment,returns='',args=''):
 used=[(key,value) for key,value in fields.items() if re.search(r'\b'+key+r'\b',mask_go(phase))]
 aliases='\n\t'+', '.join(key for key,_ in used)+' := '+', '.join('scenario.'+field for _,field in used) if used else ''
 return '\n\n// '+comment+'\nfunc '+name+'(t *testing.T, scenario reliabilityMigrationScenario'+args+')'+returns+' {\n\tt.Helper()'+aliases+phase+'\n}'
def migration(s,body):
 history=body.index('\n\tvar draftPreserved');tool=body.index('\n\tvar databaseName');orphan=body.index('\n\tvar repairFileID');dry=body.index('\n\tdryRunOutput :=');second=body.index('\n\tsecondApplyOutput :=')
 historyphase=body[history:tool]
 orphanphase=body[orphan:dry]
 firstphase=body[dry:second]+'\n\treturn firstRepairStatus, firstRepairUpdated'
 secondphase=body[second:]
 declarations='''

type reliabilityMigrationScenario struct {
 DB *sql.DB
 TenantID, UserID, ExamID, ReviewImportID, ProcessingImportID, CommandID, RequestHash string
 Draft []byte
 Session exam.ExamSession
 RunRepair func(...string) string
}'''
 helpers=declarations+helper('e2eAssertUnverifiedMigrationHistory',historyphase,'升级保留人工确认草稿，未知历史来源保持 NULL；无法验证的处理中记录明确停止并保留协议原因。')
 helpers+=helper('e2eRepairOrphanedImportGeneration',orphanphase,'修复工具先按导入范围预演，再建立独立恢复代次；重复执行不生成新代次且保留旧取消任务。')
 helpers+=helper('e2eRepairLegacyCommandAndReview',firstphase,'全租户预演包含可恢复业务事实，应用只修复原请求哈希的传输收据并标记旧来源待复核。',' (string, time.Time)')
 helpers+=helper('e2eAssertReliabilityRepairRepeatNoOp',secondphase,'第二次正式修复报告零变更，旧复核运行状态和更新时间必须保持不动。',args=', firstRepairStatus string, firstRepairUpdated time.Time')
 body=body[:history]+'''
 scenario := reliabilityMigrationScenario{DB: db, TenantID: tenantID, UserID: userID, ExamID: examID,
  ReviewImportID: reviewImportID, ProcessingImportID: processingImportID, Draft: draft,
  CommandID: examCommandID, Session: examSession, RequestHash: transportRequestHash}
 e2eAssertUnverifiedMigrationHistory(t, scenario)
'''+body[tool:orphan]+'''
 scenario.RunRepair = runRepair
 e2eRepairOrphanedImportGeneration(t, scenario)
 firstRepairStatus, firstRepairUpdated := e2eRepairLegacyCommandAndReview(t, scenario)
 e2eAssertReliabilityRepairRepeatNoOp(t, scenario, firstRepairStatus, firstRepairUpdated)
'''
 return replace_function(s,'TestReliabilityMigrationFrom000122PostgresTestDatabase',body,helpers)
apply(path,'TestReliabilityMigrationFrom000122PostgresTestDatabase',migration)
(report/'go-server-stages28.json').write_text(json.dumps({'files':ns['ns']['changed'],'issue_functions':ns['functions'],'checkpoint_relation':'after checkpoint02; requires subsequent scan'},ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
print(json.dumps(ns['functions']))
