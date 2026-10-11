from pathlib import Path
import re,json
root=Path('D:/project/yuejuan');report=root/'reports/sonar-remediation-2026-10-11'
source=(report/'go_server_stages2.py').read_text(encoding='utf-8');ns={};exec(source[:source.index("\npath='services/")],ns)
function=ns['function'];mask_go=ns['mask_go'];replace_function=ns['replace_function'];save=ns['save']
changes=[]
for filename in ['e2e_subjective_panel_test.go','e2e_worker_runtime_reliability_test.go','e2e_paper_import_reconciliation_test.go','e2e_reliability_command_test.go']:
 path='services/api-gateway/internal/server/'+filename;s=(root/path).read_text(encoding='utf-8')
 for name in re.findall(r'^func (e2e\w+)\(',s,re.M):
  _,_,body=function(s,name);updated=body
  pattern=r'\n\t([\w]+(?:, [\w]+)*) := ((?:(?:scenario|recovery)\.[\w.]+)(?:, (?:scenario|recovery)\.[\w.]+)*)\n'
  for match in reversed(list(re.finditer(pattern,body))):
   names=match[1].split(', ');values=match[2].split(', ')
   remaining=mask_go(body[:match.start()]+body[match.end():])
   kept=[(key,value) for key,value in zip(names,values) if re.search(r'\b'+key+r'\b',remaining)]
   removed=[key for key in names if key not in [p[0] for p in kept]]
   if not removed:continue
   replacement='\n\t'+', '.join(p[0] for p in kept)+' := '+', '.join(p[1] for p in kept)+'\n' if kept else '\n'
   updated=updated[:match.start()]+replacement+updated[match.end():]
   changes.append({'file':path,'function':name,'removed_unused_aliases':removed})
  if updated!=body:s=replace_function(s,name,updated,'')
 if s!=(root/path).read_text(encoding='utf-8'):save(path,s)
(report/'go-server-unused-alias-cleanup.json').write_text(json.dumps(changes,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
print(json.dumps({'functions_updated':len(changes)}))
