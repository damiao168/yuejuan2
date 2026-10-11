from pathlib import Path
import json,re
root=Path('D:/project/yuejuan');report=root/'reports/sonar-remediation-2026-10-11'
source=(report/'go_server_stages2.py').read_text(encoding='utf-8');ns={};exec(source[:source.index("\npath='services/")],ns)
replace_function=ns['replace_function'];apply=ns['apply'];mask_go=ns['mask_go']
path='services/api-gateway/internal/server/e2e_story060_sheet_identity_test.go'
fields={'db':'DB','router':'Router','adminToken':'AdminToken','fixture':'Acceptance','suffix':'Suffix','store':'Capture'}

def helper(name,phase,comment,args='',result=''):
 used=[(key,value) for key,value in fields.items() if re.search(r'\b'+key+r'\b',mask_go(phase))]
 aliases='\n\t'+', '.join(key for key,_ in used)+' := '+', '.join('scenario.'+field for _,field in used) if used else ''
 return '\n\n// '+comment+'\nfunc '+name+'(t *testing.T, scenario sheetIdentityScenario'+args+')'+result+' {\n\tt.Helper()'+aliases+phase+'\n}'

def closure(body,label,name,args):
 start=body.index(label);opening=body.index('{',start);masked=mask_go(body);depth=1;i=opening+1
 while depth:
  if masked[i]=='{':depth+=1
  elif masked[i]=='}':depth-=1
  i+=1
 phase=body[opening+1:i-1].replace('\n\t\tt.Helper()','',1)
 return body[:opening+1]+'\n\t\treturn '+name+'(t, scenario, '+args+')\n\t'+body[i-1:],phase

def multipage(s,body):
 issue=body.index('\n\tissue :=')
 body=body[:issue]+'\n\tscenario := sheetIdentityScenario{DB: db, Router: router, AdminToken: adminToken, Acceptance: fixture, Suffix: suffix}'+body[issue:]
 body,issuance=closure(body,'issue := func','e2eIssueThreePageSheet','label, targetStudentID')
 body,created=closure(body,'createCapture := func','e2eCreateControlledMultiPageCapture','label, barcodes')
 body=body.replace('\n\tstore := capture.NewPostgresStoreWithBarcodeKeyring(db, e2eBarcodeKeyring())','\n\tstore := capture.NewPostgresStoreWithBarcodeKeyring(db, e2eBarcodeKeyring())\n\tscenario.Capture = store',1)
 printstart=body.index('\n\tprintContext :=');capturestart=body.index('\n\tstore :=',printstart)
 printphase=body[printstart:capturestart]
 inserted=body.index('\n\tvar insertedStudentID');rows=body.index('\n\trows, err :=',inserted)
 inserted_phase=body[inserted:rows]
 observed=body.index('\n\te2eExpectStatus(',rows)
 missing=body.index('\n\t_, missingSheetSerial',observed)
 observed_phase=body[observed:missing].replace('\n\tprintContext =','\n\tprintContext :=',1).replace('\n\tbatches =','\n\tbatches :=',1)
 missing_phase=body[missing:]
 body=body[:printstart]+'\n\te2eAssertThreePagePrintPackage(t, scenario, studentID, printBatchID)'+body[capturestart:inserted]+'\n\te2eAssertInsertedPageAndFrozenTemplate(t, scenario, studentID, insertedFileID, insertedSheetSerial)'+body[rows:observed]+'\n\te2eAssertObservedSheetPackageUnavailable(t, scenario, printBatchID)\n\te2eAssertMissingAndRepeatedSheetPages(t, scenario, studentID, insertedSheetSerial, issue, createCapture)\n'
 # 原 rows 的 defer 保留在主测试，直到最后的缺页/重复场景完成才关闭。
 helpers='\n\ntype sheetIdentityScenario struct {\n\tDB *sql.DB\n\tRouter http.Handler\n\tAdminToken, Suffix string\n\tAcceptance story056AcceptanceFixture\n\tCapture *capture.PostgresStore\n}'
 helpers+=helper('e2eIssueThreePageSheet',issuance,'三页发行仍持久化同一打印批次和卡片序列，逐页值保持原页码对应。',', label, targetStudentID string',' (string, string, map[int]string)')
 helpers+=helper('e2eCreateControlledMultiPageCapture',created,'物理页按扫描顺序注册，只有受控条码赋予逻辑页身份；插页仍保留为没有受控条码的页面。',', label string, barcodes []string',' string')
 helpers+=helper('e2eAssertThreePagePrintPackage',printphase,'发行后上下文防止重复发行，打印包可以重复下载且字节、类型及审计哈希保持一致。',', studentID, printBatchID string')
 helpers+=helper('e2eAssertInsertedPageAndFrozenTemplate',inserted_phase,'插页不能增加受控页计数；后来的锁定模板也不能替换已打印卡片的冻结模板身份。',', studentID, insertedFileID, insertedSheetSerial string')
 helpers+=helper('e2eAssertObservedSheetPackageUnavailable',observed_phase,'卡片被观察后打印包不能再次下载，上下文也必须明确显示不可下载和观察数量。',', printBatchID string')
 helpers+=helper('e2eAssertMissingAndRepeatedSheetPages',missing_phase,'缺页保留逻辑页 3 的原页码；同一学生再次提交进入冲突，三份不可变卡片台账都仍存在。',', studentID, insertedSheetSerial string, issue func(string, string) (string, string, map[int]string), createCapture func(string, []string) string')
 s=replace_function(s,'TestStory060MultiPageInsertionMissingPageAndPrintPackageE2EWithPostgresTestDatabase',body,helpers)
 return s.replace('"context"','"context"\n\t"database/sql"',1)
apply(path,'TestStory060MultiPageInsertionMissingPageAndPrintPackageE2EWithPostgresTestDatabase',multipage)

def identity(s,body):
 issue=body.index('\n\tissueBody :=')
 capture=body.index('\n\tfirstAssetID :=',issue)
 pages=body.index('\n\tpages := e2eGetJSON',capture)
 deletion=body.index('\n\tdeletedPage :=',pages)
 restore=body.index('\n\te2ePostJSON(',deletion)
 revoke=body.index('\n\trevoked :=',restore)
 reprint=body.index('\n\treprintBody :=',revoke)
 issue_phase=body[issue:capture]+'\n\treturn sheetSerial, barcodeValue'
 conflict=body[pages:deletion]+'\n\treturn secondPage'
 delete_restore=body[deletion:revoke].replace('\n\tpages =','\n\tpages :=',1).replace('\n\tfirstPage =','\n\tfirstPage :=',1).replace('\n\tsecondPage =','\n\tsecondPage =',1)
 delete_restore=delete_restore.replace('\n\tvar unassignedSubmissions int','\n\tvar unassignedSubmissions int\n\tvar sheetStatus string',1)
 revoke_phase=body[revoke:reprint].replace('\n\tpages =','\n\tpages :=',1)
 reprint_phase=body[reprint:]
 body=body[:issue]+'\n\tscenario := sheetIdentityScenario{DB: db, Router: router, AdminToken: adminToken, Acceptance: fixture, Suffix: suffix}\n\tsheetSerial, barcodeValue := e2eAssertSinglePageSheetIssuance(t, scenario, rosterStudentID, outsideStudentID)'+body[capture:pages]+'\n\tsecondPage := e2eAssertDuplicatePhysicalSheetConflict(t, scenario, batchID, sheetSerial)\n\te2eDeleteAndRestoreDuplicateSheetPage(t, scenario, batchID, sheetSerial, secondPage)\n\te2eRevokeObservedSheet(t, scenario, batchID, sheetSerial)\n\te2eAssertSheetReprintIdentityAndLineage(t, scenario, sheetSerial)\n'
 helpers=helper('e2eAssertSinglePageSheetIssuance',issue_phase,'相同发行请求重放打印批次、卡片序列和条码；改变学生集合则冲突，台账仍仅一份。',', rosterStudentID, outsideStudentID string',' (string, string)')
 helpers+=helper('e2eAssertDuplicatePhysicalSheetConflict',conflict,'两份物理页都保留可见并进入冲突；台账序列、重复链接和两个提交的冲突事实分别核对。',', batchID, sheetSerial string',' map[string]any')
 helpers+=helper('e2eDeleteAndRestoreDuplicateSheetPage',delete_restore,'明确删除重复页后恢复剩余页身份，随后原页恢复必须重新计算两侧冲突，不能沿用删除时的通过状态。',', batchID, sheetSerial string, secondPage map[string]any')
 helpers+=helper('e2eRevokeObservedSheet',revoke_phase,'撤销保留原因，并使旧序列的所有已观察页重新需要复核。',', batchID, sheetSerial string')
 helpers+=helper('e2eAssertSheetReprintIdentityAndLineage',reprint_phase,'补打只生成一个新物理序列并可原请求重放；再次替代冲突，数据库保留旧序列、操作及原因谱系。',', sheetSerial string')
 return replace_function(s,'TestStory060StudentSheetIdentityE2EWithPostgresTestDatabase',body,helpers)
apply(path,'TestStory060StudentSheetIdentityE2EWithPostgresTestDatabase',identity)
(report/'go-server-stages22.json').write_text(json.dumps({'files':ns['ns']['changed'],'issue_functions':ns['functions'],'checkpoint_relation':'after checkpoint02; requires subsequent scan'},ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
print(json.dumps(ns['functions']))
