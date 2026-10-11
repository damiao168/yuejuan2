from pathlib import Path
import json,subprocess,time,datetime,sys,hashlib
root=Path('D:/project/yuejuan');report=root/'reports/sonar-remediation-2026-10-11';mode=sys.argv[1]
if mode=='e2e':
 specs=['account-security','dashboard-workbench','exam-create-flow','exam-create.visual','exam-workspace','exam-workspace.visual','platform-model-config','platform-model-management','grading-workbench-keyboard']
 commands=[['npm.cmd','run','test:e2e','--',*[f'tests/e2e/specs/{s}.spec.ts' for s in specs],'--workers=2','--reporter=line']]
elif mode=='web':
 names=set(Path(x['file']).stem for x in json.loads((report/'frontend-progress.json').read_text(encoding='utf-8'))['issues'] if x['file'].startswith('apps/web-admin/src/'))
 tests=sorted(str(p.relative_to(root/'apps/web-admin')).replace('\\','/') for p in (root/'apps/web-admin/src').rglob('*test*') if p.suffix in ['.ts','.tsx'] and any(p.stem.startswith(name+'.') for name in names))
 tests+=['src/features/grading/workbench/components/ExamScoringPanel.batch8.test.tsx','src/components/model-governance/ScoringAssuranceWorkspace.batch11.test.tsx']
 commands=[['npm.cmd','--workspace','apps/web-admin','run','test','--',*sorted(set(tests)),'--maxWorkers=2']]
elif mode=='desktop':commands=[['npm.cmd','--workspace','apps/desktop-client','run','test','--','src/components/OfflineWorkbench.test.tsx','src/components/OfflineWorkbench.batch40.test.tsx','src/features/capture/useExamCaptureContext.test.tsx','src/features/upload/useUploadSync.test.tsx','--maxWorkers=2']]
elif mode=='types':commands=[['npm.cmd','--workspace',app,'run','typecheck'] for app in ['apps/desktop-client','apps/student-portal','apps/web-admin']]
elif mode=='scripts':commands=[['node','--test','scripts/ci/schema-version.test.mjs','scripts/ci/architecture-audit-tools.test.mjs'],['npm.cmd','--workspace','apps/web-admin','run','test:production-contracts'],['npm.cmd','run','check:story049'],['npm.cmd','run','check:story052'],['npm.cmd','--prefix','lab','test']]
elif mode=='lint':
 files=sorted(set(x['file'] for x in json.loads((report/'frontend-progress.json').read_text(encoding='utf-8'))['issues']))
 files+=['docs/architecture/tools/frontend-api-scan.mjs','docs/architecture/tools/frontend-api-paths.mjs','scripts/ci/architecture-audit-tools.test.mjs','apps/web-admin/src/utils/displayValue.ts']
 commands=[['node',str(root/'node_modules/@biomejs/biome/bin/biome'),'lint',*files]]
else:raise ValueError(mode)
results=[]
for i,command in enumerate(commands):
 started=datetime.datetime.now(datetime.timezone.utc).isoformat();t=time.monotonic();log=report/f'frontend-{mode}-{i}.log'
 with log.open('w',encoding='utf-8') as f:cp=subprocess.run(command,cwd=root,stdout=f,stderr=subprocess.STDOUT,encoding='utf-8',errors='replace')
 row={'command':command,'started_at':started,'completed_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'duration_seconds':time.monotonic()-t,'exit_code':cp.returncode,'log':str(log.relative_to(report)),'log_sha256':hashlib.sha256(log.read_bytes()).hexdigest()};results.append(row);print(json.dumps({'mode':mode,'exit_code':cp.returncode,'log':row['log']}),flush=True)
(report/f'frontend-{mode}-execution.json').write_text(json.dumps(results,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
if any(x['exit_code'] for x in results):sys.exit(1)
