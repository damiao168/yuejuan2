from pathlib import Path
import json
root=Path('D:/project/yuejuan');report=root/'reports/sonar-remediation-2026-10-11'
source=(report/'go_server_stages2.py').read_text(encoding='utf-8');ns={};exec(source[:source.index("\npath='services/")],ns)
replace_function=ns['replace_function'];apply=ns['apply']
path='services/api-gateway/internal/server/e2e_story056_lease_test.go'

def reference(s,body):
 start=body.index('\n\tvar profileVersion, profileHash, referenceAssetID')
 overlay=body.index('\n\toverlay :=',start)
 snapshot=body[start:overlay]
 body=body[:start]+'\n\tsnapshot := e2eAssertTemplateDifferenceSnapshot(t, db, fixture, omrRunID)\n\tprofileVersion, profileHash, referenceSHA256 := snapshot.ProfileVersion, snapshot.ProfileHash, snapshot.ReferenceSHA256'+body[overlay:]
 body=body.replace('\n\terr = db.QueryRow','\n\terr := db.QueryRow',1)
 helpers='\n\ntype templateDifferenceSnapshot struct {\n\tProfileVersion, ProfileHash, ReferenceSHA256 string\n}\n\n// 先核对运行、模板文件和 worker 载荷里的同一冻结身份，再允许验证回调的参考资源。\nfunc e2eAssertTemplateDifferenceSnapshot(t *testing.T, db *sql.DB, fixture story056AcceptanceFixture, omrRunID string) templateDifferenceSnapshot {\n\tt.Helper()'+snapshot+'\n\treturn templateDifferenceSnapshot{ProfileVersion: profileVersion, ProfileHash: profileHash, ReferenceSHA256: referenceSHA256}\n}'
 return replace_function(s,'TestStory056TemplateDifferenceReferenceSnapshotE2E',body,helpers)
apply(path,'TestStory056TemplateDifferenceReferenceSnapshotE2E',reference)
path='services/api-gateway/internal/server/e2e_exam_management_test.go'

def phase(name,body,aliases,comment,output=''):
 return '\n\n// '+comment+'\nfunc '+name+'(t *testing.T, db *sql.DB, router http.Handler, token string, scope wholeExamManagementScope'+(', item map[string]any' if name in {'e2eEditWholeExamSubjects','e2eArchiveDraftWholeExam','e2eRestoreWholeExamAndCheckPublication','e2eArchivePublishedWholeExam'} else '')+')'+output+' {\n\tt.Helper()'+aliases+body+'\n}'

def whole(s,body):
 page=body.index('\n\t// 另校分页夹具')
 edit=body.index('\n\tupdate := examManagementMutationInput')
 draftarchive=body.index('\n\tcheck := e2eGetJSON',edit)
 restore=body.index('\n\titem = e2eExamManagementJSON(t, router, http.MethodPost, path+"/restore",')
 publication=body.index('\n\t// 发布事实来自正式发布记录')
 jobs=body.index('\n\t// 归档资格不能只看发布数量')
 legacy=body.index('\n\tlegacy := e2ePostJSON',jobs)
 pagination=body[page:edit]
 editing=body[edit:draftarchive]+'\n\treturn item'
 archive=body[draftarchive:restore]+'\n\treturn item'
 restored=body[restore:publication].replace('\n\tcheck =','\n\tcheck :=',1)+'\n\treturn item'
 published=body[publication:jobs].replace('\n\titem =','\n\titem :=',1)+'\n\treturn item, tenantID, actorID'
 background=body[jobs:legacy]+'\n\treturn item'
 standalone=body[legacy:]
 body=body[:page]+'''\n\tscope := wholeExamManagementScope{SchoolID: schoolID, SessionID: sessionID, Path: path, MathID: mathID, EnglishID: englishID, Suffix: suffix, Name: name, ClassIDs: classIDs}
\te2eAssertWholeExamCursorPages(t, db, router, token, scope)
\titem = e2eEditWholeExamSubjects(t, db, router, token, scope, item)
\titem = e2eArchiveDraftWholeExam(t, db, router, token, scope, item)
\titem = e2eRestoreWholeExamAndCheckPublication(t, db, router, token, scope, item)
\titem, scope.TenantID, scope.ActorID = e2eSeedWholeExamPublishedFacts(t, db, router, token, scope)
\titem = e2eArchivePublishedWholeExam(t, db, router, token, scope, item)
\te2eAssertStandaloneAppealArchiveBoundary(t, db, router, token, scope)
'''
 helpers='\n\ntype wholeExamManagementScope struct {\n\tSchoolID, SessionID, Path, MathID, EnglishID, Suffix, Name, TenantID, ActorID string\n\tClassIDs []string\n}'
 helpers+=phase('e2eAssertWholeExamCursorPages',pagination,'\n\tschoolID, mathID, sessionID, suffix := scope.SchoolID, scope.MathID, scope.SessionID, scope.Suffix','游标分页始终以整场为单位：保留两科、拒绝重复组并核对两页合计，另校夹具不混入后续归档范围。')
 helpers+=phase('e2eEditWholeExamSubjects',editing,'\n\tname, classIDs, mathID, sessionID, path := scope.Name, scope.ClassIDs, scope.MathID, scope.SessionID, scope.Path','整场编辑仅同步继承配置，独立选班和满分保持原值；同一旧修订号再次提交必须整体冲突。',' map[string]any')
 helpers+=phase('e2eArchiveDraftWholeExam',archive,'\n\tpath, mathID, englishID, schoolID := scope.Path, scope.MathID, scope.EnglishID, scope.SchoolID','草稿归档保留历史学科状态，同时阻断 HTTP 和直接数据库写入；只读工作区和列表归档事实必须一致。',' map[string]any')
 helpers+=phase('e2eRestoreWholeExamAndCheckPublication',restored,'\n\tpath, mathID := scope.Path, scope.MathID','恢复后才重新允许写入；仅修改 published 状态不能伪造正式发布记录或通过混合状态归档门禁。',' map[string]any')
 helpers+=phase('e2eSeedWholeExamPublishedFacts',published,'\n\tpath, mathID, englishID := scope.Path, scope.MathID, scope.EnglishID','先建立两科不可变发布事实和当前版本指针，再核对管理投影的正式发布数量。',' (map[string]any, string, string)')
 helpers+=phase('e2eArchivePublishedWholeExam',background,'\n\tpath, schoolID, mathID, tenantID, actorID := scope.Path, scope.SchoolID, scope.MathID, scope.TenantID, scope.ActorID','后台任务未结束时两种归档查询都拒绝；取消任务再归档，前后正式成绩读取与学科历史状态保持一致。',' map[string]any')
 helpers+=phase('e2eAssertStandaloneAppealArchiveBoundary',standalone,'\n\tschoolID, suffix, classIDs, tenantID, actorID := scope.SchoolID, scope.Suffix, scope.ClassIDs, scope.TenantID, scope.ActorID','独立考试保持原身份；开放申诉窗口用新发布夹具，列表和单场检查都应拒绝归档。')
 s=replace_function(s,'TestWholeExamManagementE2EWithPostgresTestDatabase',body,helpers)
 return s.replace('"encoding/json"','"database/sql"\n\t"encoding/json"',1)
apply(path,'TestWholeExamManagementE2EWithPostgresTestDatabase',whole)
(report/'go-server-stages12.json').write_text(json.dumps({'files':ns['ns']['changed'],'issue_functions':ns['functions'],'checkpoint_relation':'after checkpoint02; requires subsequent scan'},ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
print(json.dumps(ns['functions']))
