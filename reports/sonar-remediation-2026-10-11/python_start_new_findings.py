"""冻结第二次平台扫描新增 Python 问题的当前源码；不复用原告警账本。"""
from collections import Counter
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
REPORT = Path(__file__).resolve().parent
BACKUP = Path('D:/tools/sonar-remediation-20261011/python-new-baseline')


def main():
    original = json.loads((ROOT / 'reports/platform-audit-2026-10-10/sonar/issues.json').read_text('utf-8'))
    checkpoint = json.loads((REPORT / 'full-checkpoint-02/issues.json').read_text('utf-8'))
    keys = {item['key'] for item in original['issues']}
    findings = [item for item in checkpoint['issues'] if item['key'] not in keys
                and item['rule'].startswith('python:')
                and not item['component'].endswith(':docs/architecture/tools/build_preview.py')]
    assert len(findings) == 19
    order = {'CRITICAL': 0, 'MAJOR': 1, 'MINOR': 2}
    findings.sort(key=lambda item: (order[item['severity']], item['component'], item.get('line', 0)))
    files = []
    for relative in sorted({item['component'].split(':', 1)[1] for item in findings}):
        source = ROOT / relative
        destination = BACKUP / relative
        data = source.read_bytes()
        destination.parent.mkdir(parents=True, exist_ok=True)
        if destination.exists():
            raise FileExistsError('不能覆盖已冻结的新增问题基线：' + relative)
        destination.write_bytes(data)
        files.append({'file': relative, 'baseline_sha256': hashlib.sha256(data).hexdigest(),
                      'baseline_lines': len(data.decode('utf-8-sig').splitlines()),
                      'full_file_review_status': 'reviewed',
                      'review_evidence': '读取完整当前函数体、输入与生成/握手边界；生成物仅内存验证'})
    report = {'started_at_utc': datetime.now(timezone.utc).isoformat(),
              'source_scan': 'full-checkpoint-02/issues.json', 'original_ledger_unchanged': True,
              'count': len(findings), 'severities': dict(Counter(item['severity'] for item in findings)),
              'baseline_root': str(BACKUP), 'files': files,
              'issues': [{'key': item['key'], 'rule': item['rule'], 'severity': item['severity'],
                          'file': item['component'].split(':', 1)[1], 'scan_line': item.get('line'),
                          'message': item['message'], 'state': 'pending'} for item in findings]}
    (REPORT / 'python-new-findings-progress.json').write_text(json.dumps(report, ensure_ascii=False, indent=2), 'utf-8')
    print(json.dumps({'count': len(findings), 'files': len(files), 'severities': report['severities']}))


if __name__ == '__main__':
    main()
