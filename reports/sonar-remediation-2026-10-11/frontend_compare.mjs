import fs from 'node:fs';
import vm from 'node:vm';
import path from 'node:path';
import { createRequire } from 'node:module';
const root=process.cwd(), report=path.join(root,'reports/sonar-remediation-2026-10-11');
fs.mkdirSync(path.join(report,'frontend-comparison'),{recursive:true});
function execute(file,label){
 let code=fs.readFileSync(file,'utf8');
 code=code.replace(/^import fs from .*;$/m,'').replace(/^import path from .*;$/m,'').replace(/^import ts from .*;$/m,'').replace(/^import \{ canonicalApiPath \} from .*;$/m,'').replace(/^import \{ buildRouteCoverage \} from .*;$/m,'');
 code=code.replaceAll('import.meta.dirname',JSON.stringify(path.join(root,'docs/architecture/tools')));
 code=code.replace("const output = path.join(root, 'docs/architecture/evidence');",`const output = ${JSON.stringify(path.join(report,'frontend-comparison',label))};`);
 fs.mkdirSync(path.join(report,'frontend-comparison',label),{recursive:true});
 return import('../../scripts/update-api-route-coverage.mjs').then(async({buildRouteCoverage})=>{
  const {canonicalApiPath}=await import('../../docs/architecture/tools/frontend-api-paths.mjs');
  const require=createRequire(import.meta.url);
  vm.runInNewContext(code,{fs,path,ts:require('typescript'),buildRouteCoverage,canonicalApiPath,console:{log(){}}},{filename:file});
  return JSON.parse(fs.readFileSync(path.join(report,'frontend-comparison',label,'api-catalog.json'),'utf8'));
 });
}
const old=await execute('D:/tools/sonar-remediation-20261011/baseline/docs/architecture/evidence/extract-frontend-api.mjs','baseline');
const current=await execute('docs/architecture/tools/frontend-api-scan.mjs','current');
const identical=JSON.stringify(old)===JSON.stringify(current);
fs.writeFileSync(path.join(report,'frontend-comparison.json'),JSON.stringify({kind:'same-current-inputs, isolated output',identical,baseline_summary:old.summary,current_summary:current.summary},null,2)+'\n');
if(!identical) throw new Error('AST audit output differs');
console.log('AST audit output identical:',current.frontendCalls.length,'call sites');
