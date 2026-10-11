from pathlib import Path
import collections,hashlib,json,re
root=Path('D:/project/yuejuan');report=root/'reports/sonar-remediation-2026-10-11';backup=Path('D:/tools/sonar-remediation-20261011/server-extra-baseline')
source=(report/'go_server_stages2.py').read_text(encoding='utf-8');ns={};exec(source[:source.index("\npath='services/")],ns)
function=ns['function'];mask_go=ns['mask_go']
delegated={'e2e_question_bank_acl_ux_test.go','e2e_question_bank_import_test.go','e2e_question_bank_metadata_acl_search_test.go','e2e_question_bank_publication_test.go','e2e_question_bank_test.go'}
def literals(text):
 tokens=[];i=0
 while i<len(text):
  if text.startswith('//',i):
   end=text.find('\n',i);i=len(text) if end<0 else end+1;continue
  if text.startswith('/*',i):
   end=text.find('*/',i+2);i=len(text) if end<0 else end+2;continue
  if text[i] in {'"',"'",'`'}:
   delimiter=text[i];start=i;i+=1
   while i<len(text):
    if delimiter!='`' and text[i]=='\\':i+=2;continue
    if text[i]==delimiter:i+=1;break
    i+=1
   tokens.append(text[start:i]);continue
  i+=1
 return collections.Counter(tokens)
rows=[]
for saved in sorted(backup.rglob('*.go')):
 if saved.name in delegated:continue
 path=saved.relative_to(backup);current=root/path;before=saved.read_text(encoding='utf-8');after=current.read_text(encoding='utf-8')
 old,new=literals(before),literals(after)
 rows.append({'path':path.as_posix(),'before_sha256':hashlib.sha256(saved.read_bytes()).hexdigest(),'after_sha256':hashlib.sha256(current.read_bytes()).hexdigest(),
  'literal_removed':list((old-new).elements()),'literal_added':list((new-old).elements()),
  'original_t_run_count':len(re.findall(r'\bt\.Run\(',mask_go(before))),'current_t_run_count':len(re.findall(r'\bt\.Run\(',mask_go(after)))})
 for row in rows[-1:]:
  removed=list(row['literal_removed']);added=list(row['literal_added']);notes=[]
  if '"database/sql"' in added:
   added.remove('"database/sql"');notes.append('new database/sql import for concrete fixture DB types')
  if row['path'].endswith('e2e_paper_import_reconciliation_test.go') and len(removed)==1 and len(added)==1 and re.sub(r'\s+','',removed[0])==re.sub(r'\s+','',added[0]):
   removed=[];added=[];notes.append('one OCR/parse handoff SQL literal changed indentation only; text and placeholders retained')
  if row['path'].endswith('e2e_postgres_test.go') and removed==['""','""'] and not added:
   removed=[];notes.append('SQL splitter uses zero-value dollar tag and a single shared finishStatement guard, replacing two repeated empty-string literals')
  row.update(literal_difference_classification=notes,unexplained_literals_removed=removed,unexplained_literals_added=added)
estimates=json.loads((report/'go-server-structure-current.json').read_text(encoding='utf-8'));existing=[]
for item in estimates:
 if item['cognitive_estimate']<=15:continue
 saved=backup/item['path'];now=root/item['path']
 try: _,_,old=function(saved.read_text(encoding='utf-8'),item['name']);_,_,new=function(now.read_text(encoding='utf-8'),item['name'])
 except (AttributeError,ValueError):continue
 old_tokens=re.sub(r'\s+','',mask_go(old));new_tokens=re.sub(r'\s+','',mask_go(new))
 control=r'\b(?:if|else|for|range|switch|case|select|go|return|break|continue)\b|&&|\|\|'
 existing.append({**item,'same_preexisting_code_tokens':old_tokens==new_tokens,'preexisting_control_tokens_equal':re.findall(control,mask_go(old))==re.findall(control,mask_go(new)),'preexisting_literal_counts_equal':literals(old)==literals(new),'limit':'本地估算可能与安装插件不同；真实平台结果决定处理。'})
(report/'go-server-static-invariants.json').write_text(json.dumps({'files':rows,'estimate_over_threshold':existing,'limit':'字面量及子测试计数用于辅助检查，不能证明业务行为等价。'},ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
print(json.dumps({'files':len(rows),'literal_difference_files':[row['path'] for row in rows if row['literal_added'] or row['literal_removed']],'t_run_difference_files':[row['path'] for row in rows if row['original_t_run_count']!=row['current_t_run_count']],'estimate_over_threshold':existing},ensure_ascii=False))
