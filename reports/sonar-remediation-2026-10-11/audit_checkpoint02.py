"""审计完整复扫证据，并只读导出本地平台逐项处置后的状态。"""
from pathlib import Path
from collections import Counter
from datetime import datetime
import hashlib
import importlib.util
import json

ROOT = Path(__file__).resolve().parents[2]
OUT = Path(__file__).resolve().parent
CHECKPOINT = OUT / 'full-checkpoint-02'
ORIGINAL = ROOT / 'reports/platform-audit-2026-10-10/sonar'


def read(path):
    return json.loads(path.read_text(encoding='utf-8'))


def main():
    target = OUT / 'checkpoint02-post-disposition-audit.json'
    if target.exists():
        raise RuntimeError('Preserve previous audit evidence')
    original = read(ORIGINAL / 'issues.json')['issues']
    original_keys = {row['key'] for row in original}
    statuses = read(CHECKPOINT / 'original-issue-current-statuses.json')
    issues = read(CHECKPOINT / 'issues.json')['issues']
    scope = read(CHECKPOINT / 'source-scope.json')
    task = read(CHECKPOINT / 'compute-engine-task.json')['task']
    assert len(original_keys) == len(original) == 2223
    assert len(statuses['issues']) == 2223
    assert {row['key'] for row in statuses['issues']} == original_keys
    assert task['status'] == 'SUCCESS'
    assert read(CHECKPOINT / 'scanner-exit.json')['exit_code'] == 0
    assert read(CHECKPOINT / 'execution-status.json')['current_results_valid'] is True
    assert scope['same_original_exclusions'] is True
    scanned = {row['path']: row for row in scope['files']}
    original_paths = {row['component'].split(':', 1)[1] for row in original}
    assert not original_paths - scanned.keys()
    def profile_identity(path):
        return sorted((row['language'], row['key'], row['activeRuleCount'], row['rulesUpdatedAt']) for row in read(path)['profiles'])
    profiles_match = profile_identity(ORIGINAL / 'quality-profiles.json') == profile_identity(CHECKPOINT / 'quality-profiles-before.json') == profile_identity(CHECKPOINT / 'quality-profiles.json')
    assert profiles_match
    spec = importlib.util.spec_from_file_location('local_sonar_audit', ORIGINAL / 'run_local_sonar.py')
    sonar = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(sonar)
    credentials = read(Path('D:/tools/sonarqube-yuejuan/private/credentials.json'))
    auth = credentials['login'] + ':' + credentials['password']
    current = []
    for start in range(0, len(original), 50):
        keys = [row['key'] for row in original[start:start + 50]]
        current.extend(sonar.request('/api/issues/search', {'issues': ','.join(keys), 'ps': 100, 'additionalFields': '_all'}, auth)['issues'])
    assert len(current) == len({row['key'] for row in current}) == 2223
    assert {row['key'] for row in current} == original_keys
    (OUT / 'original-platform-statuses-after-phase02.json').write_text(json.dumps({'issues': current}, ensure_ascii=False, indent=2), encoding='utf-8')
    stale = [name for name, row in scanned.items() if not (ROOT / name).is_file() or hashlib.sha256((ROOT / name).read_bytes()).hexdigest() != row['sha256']]
    new = [row for row in issues if row['key'] not in original_keys]
    result = {
        'audited_at': datetime.now().astimezone().isoformat(),
        'project': sonar.PROJECT, 'analysis_id': task['analysisId'],
        'scanner_exit_code': 0, 'compute_engine_status': 'SUCCESS',
        'original_issue_identity_complete': True, 'original_count': 2223,
        'checkpoint_original_status_counts': dict(Counter(row['issueStatus'] for row in statuses['issues'])),
        'post_disposition_original_status_counts': dict(Counter(row['issueStatus'] for row in current)),
        'checkpoint_active_issues': len(issues), 'checkpoint_new_issues': len(new),
        'original_quality_profile_and_rules_unchanged': profiles_match,
        'original_exclusions_unchanged': True, 'original_issue_files_included': len(original_paths),
        'snapshot_maintained_files': len(scanned), 'changed_since_snapshot': stale,
        'current_workspace_matches_checkpoint': not stale,
        'final_scan_required': True,
        'quality_gate': read(CHECKPOINT / 'quality-gate.json')['projectStatus'],
        'coverage_imported_in_checkpoint': False,
        'limits': ['本记录证明冻结版本的扫描和逐项状态；后续源码需要最终复扫。', '本次质量门禁ERROR：新增告警47、平台新增覆盖率0；本次未导入覆盖率，不能将此0当作真实测试覆盖率。', '人工FALSE_POSITIVE/ACCEPTED与分析器FIXED分开统计。'],
    }
    target.write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding='utf-8')
    print(json.dumps({key: result[key] for key in ['post_disposition_original_status_counts', 'checkpoint_new_issues', 'snapshot_maintained_files', 'final_scan_required']}))


if __name__ == '__main__':
    main()
