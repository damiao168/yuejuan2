from pathlib import Path
import json
r=Path('.');issues=json.loads((r/'reports/platform-audit-2026-10-10/sonar/issues.json').read_text(encoding='utf-8'))['issues']
for filename in sorted({x['component'].split(':',1)[1] for x in issues if x['rule'] in ('typescript:S9332','typescript:S8783')}):
 p=r/filename;s=p.read_text(encoding='utf-8');s=s.replace('waitUntil: "networkidle"','waitUntil: "domcontentloaded"').replace('.click({ force: true })','.click()');p.write_text(s,encoding='utf-8')
for filename,old,new in [
 ('apps/web-admin/src/features/grading/workbench/reviewContext.ts','source.points.map(normalizeRubricPoint)','source.points.map((point, index) => normalizeRubricPoint(point, index))'),
 ('lab/scripts/govern-dataset.js','samples.map(questionGroupKey)','samples.map((sample) => questionGroupKey(sample))'),
 ('apps/student-portal/src/App.tsx','<p role="status">选择需要复核的题目，核对本版作答和评分后填写申请。</p>','<p><output>选择需要复核的题目，核对本版作答和评分后填写申请。</output></p>'),
 ('apps/web-admin/src/features/answer-groups/AnswerGroupSampleEvidence.tsx','<p role="status">正在读取本样本…</p>','<p><output>正在读取本样本…</output></p>')]:
 p=r/filename;s=p.read_text(encoding='utf-8');assert old in s,(filename,old);p.write_text(s.replace(old,new),encoding='utf-8')
