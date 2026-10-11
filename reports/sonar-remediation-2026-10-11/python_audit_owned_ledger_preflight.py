"""只读核对本智能体的问题账本/当前 SHA，为 root 最终平台导出提供独立检查。"""
from collections import Counter
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
REPORTS = Path(__file__).parent

def read(path):
    return json.loads(path.read_text(encoding='utf-8-sig'))

def issues(document):
    return document['issues'] if isinstance(document, dict) else document

original = issues(read(ROOT / 'reports/platform-audit-2026-10-10/sonar/issues.json'))
checkpoint = issues(read(REPORTS / 'full-checkpoint-02/issues.json'))
original_keys = {issue['key'] for issue in original}
checkpoint_keys = {issue['key'] for issue in checkpoint}
ledgers = {
    'python_original': read(REPORTS / 'python-progress.json'),
    'python_new': read(REPORTS / 'python-new-findings-progress.json'),
    'go_modelgovernance': read(REPORTS / 'go-modelgovernance-progress.json'),
}
expected_counts = {'python_original': 190, 'python_new': 19, 'go_modelgovernance': 12}
results = []
seen_keys = set()
for label, ledger in ledgers.items():
    entries = ledger['issues']
    keys = {entry['key'] for entry in entries}
    assert len(entries) == len(keys) == expected_counts[label]
    assert not keys.intersection(seen_keys)
    seen_keys.update(keys)
    assert keys <= (checkpoint_keys - original_keys if label == 'python_new' else original_keys)
    file_checks = []
    for entry in ledger['files']:
        relative = entry.get('file', entry.get('path'))
        current = hashlib.sha256((ROOT / relative).read_bytes()).hexdigest()
        recorded = (entry.get('current_sha256') if label != 'python_original'
                    else entry.get('source_sha256_after', entry.get('source_sha256_current', entry.get('source_sha256_before'))))
        file_checks.append({'file': relative, 'current_sha256': current,
                            'latest_ledger_sha256': recorded,
                            'matched': current == recorded,
                            'full_file_review_claim': entry.get('full_file_review_claim', entry.get('full_file_review_status') == 'reviewed')})
    results.append({'ledger': label, 'issue_count': len(entries), 'unique_keys': True,
                    'keys_in_expected_source_export': True,
                    'source_status_counts': dict(Counter(entry.get('status', entry.get('state')) for entry in entries)),
                    'file_sha_checks': file_checks,
                    'source_sha_mismatches': [entry for entry in file_checks if not entry['matched']]})

report = {'recorded_at_utc': datetime.now(timezone.utc).isoformat(),
          'scope': '本智能体三份账本 190+19+12 条；核对原 key/新增 key 集合、无重叠、账本最新记录与当前文件真实字节。只读、不重跑测试、不修改平台状态。',
          'result': 'passed' if all(not result['source_sha_mismatches'] for result in results) else 'source_sha_mismatch_needs_reconciliation',
          'platform_resolution_claimed': False,
          'platform_limitation': 'checkpoint02 不是最终复扫；源码已处理不代表平台已关闭。最终平台 key 状态由 root 的最终完整导出审计。',
          'results': results}
(REPORTS / 'python-owned-ledger-preflight-audit.json').write_text(
    json.dumps(report, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
print(json.dumps({'result': report['result'], 'ledgers': [{
    'ledger': result['ledger'], 'issues': result['issue_count'],
    'file_count': len(result['file_sha_checks']), 'sha_mismatches': result['source_sha_mismatches'],
    'states': result['source_status_counts']} for result in results]}, ensure_ascii=False))
