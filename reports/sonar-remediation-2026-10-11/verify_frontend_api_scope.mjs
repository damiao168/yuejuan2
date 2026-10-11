// 基线与当前版使用同一个冻结输入；截获全部文件写入，不修改冻结源码或架构产物。
import fs from 'node:fs';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { createHash } from 'node:crypto';

const mode = process.argv[2];
if (!['baseline', 'current'].includes(mode)) throw new Error('Use baseline or current');
const attempt = process.argv[3] ?? '01';
if (!/^\d{2}$/.test(attempt)) throw new Error('Use a two-digit attempt');
const root = process.cwd();
const relative = 'docs/architecture/tools/frontend-api-scan.mjs';
const sourcePath = mode === 'baseline'
  ? path.join('D:/tools/sonar-remediation-20261011/root-new-findings-baseline', relative)
  : path.join(root, relative);
const frozen = 'D:/tools/sonar-remediation-20261011/full-checkpoint-02/workspace';
const source = fs.readFileSync(sourcePath, 'utf8');
const output = path.join(root, 'reports/sonar-remediation-2026-10-11');
const prefix = `frontend-api-scope-${mode}-${attempt}`;
const modulePath = path.join(output, `${prefix}-execution.mjs`);
if (fs.existsSync(modulePath)) throw new Error('Preserve existing attempt evidence');
const dependencies = [];
let program = source.replace("const root = path.resolve(import.meta.dirname, '../../..');", `const root = ${JSON.stringify(frozen)};`);
for (const dependency of ['./frontend-api-paths.mjs', '../../../scripts/update-api-route-coverage.mjs']) {
  const dependencyPath = path.resolve(frozen, path.dirname(relative), dependency);
  dependencies.push({path: dependencyPath, sha256: createHash('sha256').update(fs.readFileSync(dependencyPath)).digest('hex')});
  program = program.replace(`from '${dependency}'`, `from ${JSON.stringify(pathToFileURL(dependencyPath).href)}`);
}
if (program === source || !program.includes(`const root = ${JSON.stringify(frozen)};`)) throw new Error('Frozen-input binding failed');
fs.writeFileSync(modulePath, program);
const writes = [];
const originalWrite = fs.writeFileSync;
fs.writeFileSync = (file, content) => { writes.push({file: String(file), content: Buffer.from(content)}); };
try { await import(pathToFileURL(modulePath)); }
finally { fs.writeFileSync = originalWrite; }
if (writes.length !== 1 || !writes[0].file.endsWith('api-catalog.json')) throw new Error('Unexpected scanner writes');
const content = writes[0].content;
const artifact = path.join(output, `${prefix}-catalog.json`);
fs.writeFileSync(artifact, content);
const sourceHash = createHash('sha256').update(source).digest('hex');
if (createHash('sha256').update(fs.readFileSync(sourcePath)).digest('hex') !== sourceHash) throw new Error('Source changed during validation');
const result = {mode, attempt, source: sourcePath, source_sha256: sourceHash, source_stable_during_validation: true, frozen_input: frozen, dependencies,
  only_write_captured_in_memory: writes[0].file, bytes: content.length, output_sha256: createHash('sha256').update(content).digest('hex'),
  summary: JSON.parse(content).summary};
fs.writeFileSync(path.join(output, `${prefix}-validation.json`), JSON.stringify(result, null, 2));
