"""只读核对当前全仓复扫及所有已跟踪问题；每个阶段保留独立证据。"""
from pathlib import Path
from collections import Counter
from datetime import datetime
import argparse
import hashlib
import importlib.util
import json

ROOT = Path(__file__).resolve().parents[2]
OUT = Path(__file__).resolve().parent
ORIGINAL = ROOT / 'reports/platform-audit-2026-10-10/sonar'


def read(path):
    return json.loads(path.read_text(encoding='utf-8-sig'))


def save(path, value):
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2), encoding='utf-8')


def profile_identity(path):
    return sorted((row['language'], row['key'], row['activeRuleCount'], row['rulesUpdatedAt'])
                  for row in read(path)['profiles'])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--checkpoint', required=True)
    parser.add_argument('--phase', choices=['pre-disposition', 'final'], required=True)
    args = parser.parse_args()
    # 参数只允许已存在的直接子目录，避免误读其它项目的扫描证据。
    if Path(args.checkpoint).name != args.checkpoint or not args.checkpoint.startswith('full-checkpoint-'):
        parser.error('checkpoint must be a direct full-checkpoint directory name')
    checkpoint = OUT / args.checkpoint
    target = checkpoint / (args.phase + '-audit.json')
    if target.exists():
        raise RuntimeError('Preserve previous audit evidence')
    original = read(ORIGINAL / 'issues.json')['issues']
    original_by_key = {row['key']: row for row in original}
    checkpoint02 = read(OUT / 'full-checkpoint-02/issues.json')['issues']
    new = [row for row in checkpoint02 if row['key'] not in original_by_key]
    new_by_key = {row['key']: row for row in new}
    assert len(original_by_key) == len(original) == 2223
    assert len(new_by_key) == len(new) == 47
    assert not original_by_key.keys() & new_by_key.keys()
    scope = read(checkpoint / 'source-scope.json')
    task = read(checkpoint / 'compute-engine-task.json')['task']
    execution = read(checkpoint / 'execution-status.json')
    assert task['status'] == 'SUCCESS'
    assert read(checkpoint / 'scanner-exit.json')['exit_code'] == 0
    assert execution['status'] == 'complete' and execution['current_results_valid'] is True
    assert read(checkpoint / 'export-manifest.json')['complete'] is True
    assert scope['same_original_exclusions'] is True
    assert profile_identity(ORIGINAL / 'quality-profiles.json') == profile_identity(checkpoint / 'quality-profiles-before.json') == profile_identity(checkpoint / 'quality-profiles.json')
    scanned = {row['path']: row for row in scope['files']}
    original_paths = {row['component'].split(':', 1)[1] for row in original}
    assert not original_paths - scanned.keys()
    stale = [path for path, row in scanned.items()
             if not (ROOT / path).is_file() or hashlib.sha256((ROOT / path).read_bytes()).hexdigest() != row['sha256']]
    snapshot = Path(scope['snapshot'])
    support_stale = [path for path in scope['type_resolution_support_files']
                     if not (ROOT / path).is_file() or (ROOT / path).read_bytes() != (snapshot / path).read_bytes()]
    spec = importlib.util.spec_from_file_location('current_local_sonar_audit', ORIGINAL / 'run_local_sonar.py')
    sonar = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(sonar)
    credentials = read(Path('D:/tools/sonarqube-yuejuan/private/credentials.json'))
    auth = credentials['login'] + ':' + credentials['password']

    def fetch_tracked(rows, name):
        current = []
        for start in range(0, len(rows), 50):
            keys = [row['key'] for row in rows[start:start + 50]]
            result = sonar.request('/api/issues/search', {'issues': ','.join(keys), 'ps': 100, 'additionalFields': '_all'}, auth)
            current.extend(result['issues'])
        expected = {row['key']: row for row in rows}
        assert len(current) == len({row['key'] for row in current}) == len(expected)
        assert {row['key'] for row in current} == expected.keys()
        # 行号可因修复变化；规则、项目和文件身份必须保持一致。
        assert all(row['rule'] == expected[row['key']]['rule'] and row['component'] == expected[row['key']]['component'] and row['project'] == scope['original_project'] for row in current)
        save(checkpoint / (args.phase + '-' + name + '-statuses.json'), {'requested': len(rows), 'returned': len(current), 'issues': current})
        return current

    original_current = fetch_tracked(original, 'original')
    new_current = fetch_tracked(new, 'checkpoint02-new')
    active = []
    page = 1
    while True:
        result = sonar.request('/api/issues/search', {'componentKeys': scope['original_project'], 'ps': 500, 'p': page, 'resolved': 'false', 'additionalFields': '_all'}, auth)
        active.extend(result['issues'])
        if len(active) >= result['paging']['total']:
            break
        page += 1
    assert len(active) == len({row['key'] for row in active}) == result['paging']['total']
    save(checkpoint / (args.phase + '-active-issues.json'), {'total': len(active), 'issues': active})
    gate = sonar.request('/api/qualitygates/project_status', {'analysisId': task['analysisId']}, auth)['projectStatus']
    known_keys = original_by_key.keys() | new_by_key.keys()
    untracked = [row for row in active if row['key'] not in known_keys]
    counts = dict(Counter(row['issueStatus'] for row in original_current))
    new_counts = dict(Counter(row['issueStatus'] for row in new_current))
    result = {
        'audited_at': datetime.now().astimezone().isoformat(),
        'project': scope['original_project'], 'checkpoint': args.checkpoint, 'analysis_id': task['analysisId'],
        'scanner_exit_code': 0, 'compute_engine_status': 'SUCCESS', 'export_complete': True,
        'original_count': 2223, 'checkpoint02_new_count': 47, 'tracked_count': 2270,
        'original_identity_complete': True, 'checkpoint02_new_identity_complete': True,
        'original_status_counts': counts, 'checkpoint02_new_status_counts': new_counts,
        'active_count': len(active), 'active_counts_by_rule': dict(Counter(row['rule'] for row in active)),
        'untracked_active_count': len(untracked), 'untracked_active_keys': [row['key'] for row in untracked],
        'active_issues': [{'key': row['key'], 'rule': row['rule'], 'file': row['component'].split(':', 1)[1], 'line': row.get('line'), 'message': row['message']} for row in active],
        'original_quality_profiles_and_rules_unchanged': True, 'original_exclusions_unchanged': True,
        'original_issue_files_included': len(original_paths), 'maintained_files': len(scanned),
        'changed_since_snapshot': stale, 'changed_type_support_since_snapshot': support_stale,
        'current_workspace_matches_checkpoint': not stale and not support_stale,
        'quality_gate': gate, 'coverage_imported_in_checkpoint': False,
        'all_issue_work_complete': args.phase == 'final' and not active and not stale and not support_stale,
        'limits': ['工具已扫描文件不代表人工已逐文件审读，审读状态以各分工证据为准。',
                   '扫描未导入测试覆盖率，平台覆盖率0不能解释为实际测试覆盖率0%。',
                   '源码FIXED、人工FALSE_POSITIVE及明确ACCEPTED分别统计；条件skip不计测试通过。'],
    }
    save(target, result)
    print(json.dumps({key: result[key] for key in ['original_status_counts', 'checkpoint02_new_status_counts', 'active_count', 'untracked_active_count', 'current_workspace_matches_checkpoint', 'all_issue_work_complete']}, ensure_ascii=False))


if __name__ == '__main__':
    main()
