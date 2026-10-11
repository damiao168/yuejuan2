from pathlib import Path
import json,hashlib,re
root=Path('.'); report=root/'reports/sonar-remediation-2026-10-11';handoff=json.loads((report/'go-server-handoff.json').read_text(encoding='utf-8'))
progress={'count':len(handoff['issues']),'issues':handoff['issues'],'files':{},'validations':[]}
for x in progress['issues']:x['state']='pending'
(report/'go-server-progress.json').write_text(json.dumps(progress,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
# 只处理词法上真实的调用；SQL、JSON、注释中的括号都不能改变参数边界。
def mask_go(s):
 out=list(s);i=0
 while i<len(s):
  if s.startswith('//',i):
   e=s.find('\n',i);e=len(s) if e<0 else e
  elif s.startswith('/*',i):
   e=s.find('*/',i+2);e=len(s) if e<0 else e+2
  elif s[i] in '\"\'`':
   q=s[i];e=i+1
   while e<len(s):
    if q!='`' and s[e]=='\\':e+=2;continue
    if s[e]==q:e+=1;break
    e+=1
  else:i+=1;continue
  for j in range(i,e):
   if out[j]!='\n':out[j]=' '
  i=e
 return ''.join(out)
def arguments(s,opening,mask):
 depth=0;parts=[];start=opening+1
 for i in range(opening,len(s)):
  c=mask[i]
  if c in '([{':depth+=1
  elif c in ')]}':
   depth-=1
   if depth==0:
    parts.append(s[start:i].strip());return parts,i
  elif c==',' and depth==1:parts.append(s[start:i].strip());start=i+1
 raise ValueError('unterminated call')
specs=[
 ('e2eArbitrationAndExportCommands','services/api-gateway/internal/server/e2e_business_command_test.go','arbitrationCommandFixture',[(2,'Tenant'),(3,'Exam'),(4,'Segment'),(5,'FirstActor'),(6,'SecondActor'),(7,'Arbitrator')],
  't *testing.T, db *sql.DB, fixture arbitrationCommandFixture', 'tenant, exam, segment, firstActor, secondActor, arbitrator := fixture.Tenant, fixture.Exam, fixture.Segment, fixture.FirstActor, fixture.SecondActor, fixture.Arbitrator', 'Tenant, Exam, Segment, FirstActor, SecondActor, Arbitrator string', '仲裁夹具把同一场双评的租户、考试、答题片段与角色身份一起传入；每个角色仍保持独立。'),
 ('createReconciliationAsset','services/api-gateway/internal/server/e2e_file_reconciliation_test.go','reconciliationAssetSpec',[(3,'TenantID'),(4,'UserID'),(5,'Name'),(6,'Body'),(7,'Lifecycle')],
  't *testing.T, ctx context.Context, store *files.PostgresStore, spec reconciliationAssetSpec','tenantID, userID, name, body, lifecycle := spec.TenantID, spec.UserID, spec.Name, spec.Body, spec.Lifecycle','TenantID, UserID, Name, Lifecycle string\n\tBody []byte','资产夹具携带完整归属和生命周期，正文仍按原始字节计算内容哈希。'),
 ('qbRequest','services/api-gateway/internal/server/e2e_question_bank_test.go','questionBankRequest',[(2,'Method'),(3,'Path'),(4,'Token'),(5,'Key'),(6,'Body'),(7,'Want')],
  't *testing.T, router http.Handler, input questionBankRequest','method, path, token, key, body, want := input.Method, input.Path, input.Token, input.Key, input.Body, input.Want','Method, Path, Token, Key, Body string\n\tWant int','题库请求把传输内容与预期状态放在同一描述中，认证令牌和幂等键仍明确区分。'),
 ('e2eCreateStory056Question','services/api-gateway/internal/server/e2e_story056_test.go','story056QuestionSpec',[(3,'ExamID'),(4,'PaperID'),(5,'QuestionNo'),(6,'QuestionType'),(7,'StandardAnswer'),(8,'SortOrder')],
  't *testing.T, router http.Handler, adminToken string, spec story056QuestionSpec','examID, paperID, questionNo, questionType, standardAnswer, sortOrder := spec.ExamID, spec.PaperID, spec.QuestionNo, spec.QuestionType, spec.StandardAnswer, spec.SortOrder','ExamID, PaperID, QuestionNo, QuestionType string\n\tStandardAnswer any\n\tSortOrder int','题目夹具把试卷归属、题型、标准答案与排序作为一份输入，避免相邻字符串参数传反。'),
 ('e2eAssertStory056Run','services/api-gateway/internal/server/e2e_story056_test.go','story056RunExpectation',[(2,'Status'),(3,'Total'),(4,'Queued'),(5,'AutoConfirmed'),(6,'HumanConfirmed'),(7,'Review'),(8,'Failed')],
  't *testing.T, run map[string]any, expected story056RunExpectation','status, total, queued, autoConfirmed, humanConfirmed, review, failed := expected.Status, expected.Total, expected.Queued, expected.AutoConfirmed, expected.HumanConfirmed, expected.Review, expected.Failed','Status string\n\tTotal, Queued, AutoConfirmed, HumanConfirmed, Review, Failed int','运行摘要按明确命名的计数核对；自动确认与人工确认不能交换或合并。'),
 ('e2eCompleteStory056HumanReviews','services/api-gateway/internal/server/e2e_story056_test.go','story056ReviewActors',[(3,'AdminToken'),(4,'GraderToken'),(5,'GraderID')],
  't *testing.T, db *sql.DB, router http.Handler, actors story056ReviewActors, fixture story056AcceptanceFixture, count int','adminToken, graderToken, graderID := actors.AdminToken, actors.GraderToken, actors.GraderID','AdminToken, GraderToken, GraderID string','管理者负责派发，阅卷员负责领取和提交；这份角色夹具不合并权限或替换请求身份。'),
 ('e2eStory056CalibrationResultPayload','services/api-gateway/internal/server/e2e_story056_calibration_test.go','story056CalibrationProfile',[(4,'Version'),(5,'ProfileVersion'),(6,'ProfileHash'),(7,'ReferenceSHA256')],
  't *testing.T, lease story056WorkerLease, item map[string]any, overlay files.FileAsset, profile story056CalibrationProfile','version, profileVersion, profileHash, referenceSHA256 := profile.Version, profile.ProfileVersion, profile.ProfileHash, profile.ReferenceSHA256','Version, ProfileVersion, ProfileHash, ReferenceSHA256 string','校准结果仍分别携带算法版本、配置版本、配置哈希和参考图哈希，不能用一个版本值替代其余证据。')]
files={str(p).replace('\\','/'):p.read_text(encoding='utf-8') for p in (root/'services/api-gateway/internal/server').glob('*.go')};original=dict(files);counts={}
for name,definition,typename,group,signature,alias,fields,comment in specs:
 count=0
 for f,s in list(files.items()):
  masked=mask_go(s);edits=[]
  for m in re.finditer(r'\b'+name+r'\s*\(',masked):
   opening=masked.find('(',m.start());args,end=arguments(s,opening,masked)
   if masked[max(0,m.start()-5):m.start()].strip()=='func':continue
   expected=8 if name=="e2eCompleteStory056HumanReviews" else max(i for i,_ in group)+1
   if len(args)!=expected:raise ValueError((name,f,len(args),args))
   indexes={i for i,_ in group};newargs=[]
   for i,arg in enumerate(args):
    if i==group[0][0]:newargs.append(typename+'{'+', '.join(field+': '+args[index] for index,field in group)+'}')
    if i not in indexes:newargs.append(arg)
   edits.append((opening+1,end,', '.join(newargs)));count+=1
  for a,b,value in reversed(edits):s=s[:a]+value+s[b:]
  files[f]=s
 s=files[definition];masked=mask_go(s);m=re.search(r'func '+name+r'\s*\(',masked);opening=masked.find('(',m.start());args,end=arguments(s,opening,masked)
 s=s[:opening+1]+signature+s[end:];body=s.index('{',opening+len(signature));helper=s.index('\n\tt.Helper()',body)
 s=s[:helper]+s[helper:].replace('\n\tt.Helper()', '\n\tt.Helper()\n\t'+alias,1)
 m=re.search(r'func '+name+r'\s*\(',s);s=s[:m.start()]+'// '+comment+'\ntype '+typename+' struct {\n\t'+fields+'\n}\n\n'+s[m.start():]
 files[definition]=s;counts[name]=count
backups=[]
for f,s in files.items():
 if s==original[f]:continue
 p=root/f;raw=p.read_bytes();backup=Path('D:/tools/sonar-remediation-20261011/server-extra-baseline')/f;backup.parent.mkdir(parents=True,exist_ok=True);backup.write_bytes(raw);backups.append({'file':f,'sha256':hashlib.sha256(raw).hexdigest(),'backup':str(backup)})
 p.write_text(s,encoding='utf-8')
(report/'go-server-parameter-refactor.json').write_text(json.dumps({'calls_updated':counts,'backups':backups},ensure_ascii=False,indent=2)+'\n',encoding='utf-8');print(counts)
