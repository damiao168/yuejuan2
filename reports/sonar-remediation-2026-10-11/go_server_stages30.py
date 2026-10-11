from pathlib import Path
import json
root=Path('D:/project/yuejuan');report=root/'reports/sonar-remediation-2026-10-11'
source=(report/'go_server_stages2.py').read_text(encoding='utf-8');ns={};exec(source[:source.index("\npath='services/")],ns)
replace_function=ns['replace_function'];apply=ns['apply']
path='services/api-gateway/internal/server/e2e_anonymous_paper_test.go'
def candidates(s,body):
 start=body.index('\n\tvar highestSubmission');end=body.index('\n\tsourceAssetID, imageData :=',start)
 phase=body[start:end].replace('release.ID','releaseID')+'\n\treturn top, other'
 body=body[:start]+'\n\ttop, other := e2eSelectAnonymousShareCandidates(t, ctx, db, f, release.ID, answers)'+body[end:]
 helpers='\n\n// 先从本次冻结发布项目选最高分提交，再从原合成答案中找两名不同学生；不复用客户端传入的同学身份。\nfunc e2eSelectAnonymousShareCandidates(t *testing.T, ctx context.Context, db *sql.DB, f story056AcceptanceFixture, releaseID string, answers []repairAnswer) (repairAnswer, repairAnswer) {\n\tt.Helper()'+phase+'\n}'
 return replace_function(s,'TestHighScorePaperShareRedactsAndRevokesWithPostgresTestDatabase',body,helpers)
apply(path,'TestHighScorePaperShareRedactsAndRevokesWithPostgresTestDatabase',candidates)
(report/'go-server-stages30.json').write_text(json.dumps({'files':ns['ns']['changed'],'issue_functions':ns['functions'],'checkpoint_relation':'after checkpoint02; requires subsequent scan'},ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
print(json.dumps(ns['functions']))
