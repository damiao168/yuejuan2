from pathlib import Path
import json,subprocess,os,hashlib,datetime,sys
root=Path('D:/project/yuejuan');report=root/'reports/sonar-remediation-2026-10-11'
backup=Path('D:/tools/sonar-remediation-20261011/server-extra-baseline')
paths=sorted(str(p.relative_to(backup)).replace('\\','/') for p in backup.rglob('*.go'))
delegated={'e2e_question_bank_acl_ux_test.go','e2e_question_bank_import_test.go','e2e_question_bank_metadata_acl_search_test.go','e2e_question_bank_publication_test.go','e2e_question_bank_test.go'}
paths=[path for path in paths if Path(path).name not in delegated]
commands=[[sys.executable,str(report/'go_server_format_atomic.py')],['D:/tools/go/bin/go.exe','run',str(report/'go-structure-check.go')]]
format_only='--format-only' in sys.argv
if format_only:commands=[[sys.executable,str(report/'go_server_format_atomic.py')]]
label='format-only' if format_only else 'structure'
env=os.environ.copy();env.update({'GOMAXPROCS':'1','GOGC':'50','GOPROXY':'off'})
results=[]
for index,command in enumerate(commands):
 started=datetime.datetime.now(datetime.timezone.utc).isoformat();log=report/f'go-server-{label}-{index}.log'
 result=subprocess.run(command,cwd=root,input=json.dumps(paths) if index else None,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,encoding='utf-8',errors='replace',env=env)
 log.write_text(result.stderr,encoding='utf-8')
 if index and result.returncode==0:
  data=json.loads(result.stdout);(report/'go-server-structure-current.json').write_text(json.dumps(data,indent=2)+'\n',encoding='utf-8')
  print(json.dumps([x for x in data if x['cognitive_estimate']>15 or x['parameters']>7]),flush=True)
 results.append({'command':command,'started_at':started,'completed_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'exit_code':result.returncode,'log':log.name,'log_sha256':hashlib.sha256(log.read_bytes()).hexdigest()})
 print(json.dumps({'operation':index,'exit_code':result.returncode,'stderr':result.stderr[:1000]}),flush=True)
(report/f'go-server-{label}-execution.json').write_text(json.dumps(results,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
if any(x['exit_code'] for x in results):raise SystemExit(1)
