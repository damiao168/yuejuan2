"""逐文件 gofmt 后原子替换，避免 Windows 映射文件的 truncate 冲突。"""
from pathlib import Path
import datetime
import hashlib
import json
import os
import subprocess
import tempfile

root = Path('D:/project/yuejuan')
report = root / 'reports/sonar-remediation-2026-10-11'
backup = Path('D:/tools/sonar-remediation-20261011/server-extra-baseline')
delegated = {'e2e_question_bank_acl_ux_test.go', 'e2e_question_bank_import_test.go', 'e2e_question_bank_metadata_acl_search_test.go', 'e2e_question_bank_publication_test.go', 'e2e_question_bank_test.go'}
results = []
for saved in sorted(backup.rglob('*.go')):
    if saved.name in delegated:
        continue
    path = root / saved.relative_to(backup)
    before = path.read_bytes()
    command = ['D:/tools/go/bin/gofmt.exe', str(path)]
    result = subprocess.run(command, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if result.returncode == 0 and result.stdout != before:
        with tempfile.NamedTemporaryFile(dir=path.parent, delete=False) as out:
            out.write(result.stdout)
            staged = Path(out.name)
        try:
            os.replace(staged, path)
        finally:
            if staged.exists():
                staged.unlink()
    results.append({'path': str(path.relative_to(root)).replace('\\', '/'), 'command': command, 'exit_code': result.returncode, 'stderr': result.stderr.decode('utf-8', errors='replace'), 'before_sha256': hashlib.sha256(before).hexdigest(), 'after_sha256': hashlib.sha256(path.read_bytes()).hexdigest()})
(report / 'go-server-format-atomic-execution.json').write_text(json.dumps({'completed_at': datetime.datetime.now().astimezone().isoformat(), 'files': results, 'delegated_files_not_formatted': sorted(delegated)}, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
print(json.dumps({'files': len(results), 'failures': sum(row['exit_code'] != 0 for row in results)}))
if any(row['exit_code'] for row in results):
    raise SystemExit(1)
