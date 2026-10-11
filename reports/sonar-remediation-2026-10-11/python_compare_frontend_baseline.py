"""对比既有只读本地基线；只写独立审查证据，不更新 UI 或像素快照。"""
from datetime import datetime, timezone
import difflib
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
REPORTS = Path(__file__).parent
BASELINE = Path('D:/tools/sonar-remediation-20261011/baseline')
SNAPSHOT = Path('D:/tools/sonarqube-yuejuan/snapshots/workspace-batch57-clippy-2026-10-11T07-56-17-037Z')
FILES = [
    'apps/web-admin/src/features/exams/create/CreateExamComposer.tsx',
    'apps/web-admin/src/features/exams/create/exam-create.css',
    'apps/web-admin/src/features/exams/workspace/ExamWorkspaceLayout.tsx',
    'apps/web-admin/src/features/exams/preparation/preparationSteps.ts',
    'apps/web-admin/src/components/AppLayout.tsx',
    'apps/web-admin/src/components/appLayoutPresentation.tsx',
    'apps/web-admin/src/AppShell.tsx',
    'apps/web-admin/src/experience.css',
    'tests/e2e/specs/exam-create.visual.spec.ts',
    'tests/e2e/specs/exam-workspace.spec.ts',
    'tests/e2e/specs/dashboard-workbench.spec.ts',
    'tests/e2e/specs/exam-create.visual.spec.ts-snapshots/exam-create-390x844-chromium-win32.png',
]

def digest(data):
    return hashlib.sha256(data).hexdigest()

entries = []
for relative in FILES:
    current = (ROOT / relative).read_bytes()
    entry = {'path': relative, 'current_sha256': digest(current), 'comparisons': []}
    for label, root in [('remediation_baseline', BASELINE), ('pre_remediation_local_snapshot', SNAPSHOT)]:
        path = root / relative
        if not path.is_file():
            entry['comparisons'].append({'kind': label, 'exists': False, 'path': str(path)})
            continue
        saved = path.read_bytes()
        comparison = {'kind': label, 'exists': True, 'path': str(path),
                      'sha256': digest(saved), 'byte_equal': saved == current}
        if relative.endswith(('.ts', '.tsx', '.css')):
            saved_text = saved.decode('utf-8-sig').splitlines()
            current_text = current.decode('utf-8-sig').splitlines()
            comparison['line_normalized_equal'] = saved_text == current_text
            comparison['unified_diff'] = '\n'.join(difflib.unified_diff(
                saved_text, current_text, fromfile=str(path), tofile=str(ROOT / relative), n=3))
        entry['comparisons'].append(comparison)
    entries.append(entry)

report = {'recorded_at': datetime.now(timezone.utc).isoformat(),
          'snapshot_directory_timestamp': '2026-10-11T07:56:17.037Z',
          'remediation_baseline_recorded_at': json.loads((REPORTS / 'baseline.json').read_text(encoding='utf-8-sig'))['recorded_at'],
          'method': '逐个比较指定文件真实字节和 UTF-8 按行差异；此自动比较不代表全文人工审读。',
          'files': entries}
(REPORTS / 'frontend-e2e-baseline-readonly-evidence.json').write_text(
    json.dumps(report, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
for entry in entries:
    print(json.dumps({'path': entry['path'], 'current_sha256': entry['current_sha256'],
                      'comparisons': [{key: value for key, value in comparison.items() if key != 'unified_diff'}
                                      for comparison in entry['comparisons']]}, ensure_ascii=False))
composer = entries[0]
print(composer['comparisons'][0]['unified_diff'])
