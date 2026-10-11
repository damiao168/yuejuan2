"""按逐项复核计划处置本地平台误报/已接受上下文，保留前后状态和依据。"""
from pathlib import Path
from datetime import datetime
import argparse
import hashlib
import importlib.util
import json

ROOT = Path(__file__).resolve().parents[2]
OUT = Path(__file__).resolve().parent
CHECKPOINT = OUT / 'full-checkpoint-02'
PROJECT = 'yuejuan-local-workspace-20261010'


def main():
    global CHECKPOINT
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--plan', default='root-platform-disposition-plan.json')
    parser.add_argument('--results', default='root-platform-disposition-results.json')
    parser.add_argument('--checkpoint', default='full-checkpoint-02')
    args = parser.parse_args()
    if any(Path(name).name != name or not name.endswith('.json') for name in (args.plan, args.results)):
        parser.error('Use JSON basenames inside the report directory')
    if not args.checkpoint.startswith('full-checkpoint-') or not args.checkpoint.removeprefix('full-checkpoint-').isdigit() or Path(args.checkpoint).name != args.checkpoint:
        parser.error('Use a full-checkpoint-NN basename')
    CHECKPOINT = OUT / args.checkpoint
    result_path = OUT / args.results
    if result_path.exists():
        raise RuntimeError('Preserve previous transition evidence; inspect it before another action')
    scan = json.loads((CHECKPOINT / 'execution-status.json').read_text(encoding='utf-8'))
    task = json.loads((CHECKPOINT / 'compute-engine-task.json').read_text(encoding='utf-8'))['task']
    if scan.get('current_results_valid') is not True or task['status'] != 'SUCCESS':
        raise RuntimeError('A complete full local checkpoint is required before issue disposition')
    plan = json.loads((OUT / args.plan).read_text(encoding='utf-8'))
    snapshot = json.loads((CHECKPOINT / 'source-scope.json').read_text(encoding='utf-8'))
    scanned = {row['path']: row['sha256'] for row in snapshot['files']}
    spec = importlib.util.spec_from_file_location('local_sonar_transition', ROOT / 'reports/platform-audit-2026-10-10/sonar/run_local_sonar.py')
    sonar = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(sonar)
    credentials = json.loads(Path('D:/tools/sonarqube-yuejuan/private/credentials.json').read_text(encoding='utf-8-sig'))
    auth = credentials['login'] + ':' + credentials['password']
    results = []
    report = {'started_at': datetime.now().astimezone().isoformat(), 'project': PROJECT, 'checkpoint_analysis_id': task['analysisId'], 'no_rule_or_scope_change': True, 'records': results}
    for row in plan['records']:
        current_hash = hashlib.sha256((ROOT / row['file']).read_bytes()).hexdigest()
        if row['source_sha256'] != current_hash or scanned[row['file']] != current_hash:
            results.append({'key': row['key'], 'status': 'skipped_stale_source_evidence', 'actual_sha256': current_hash})
            continue
        before = sonar.request('/api/issues/search', {'issues': row['key'], 'ps': 10, 'additionalFields': '_all'}, auth)['issues']
        if len(before) != 1 or before[0]['project'] != PROJECT or before[0]['rule'] != row['rule']:
            raise RuntimeError('Issue identity mismatch: ' + row['key'])
        issue = before[0]
        record = {'key': row['key'], 'file': row['file'], 'source_sha256': current_hash, 'before': issue, 'transition': row['transition'], 'reason': row['reason']}
        results.append(record)
        # 已经由分析器关闭的原问题只记录真实状态，不用人工 resolve 代替源码复扫。
        if issue.get('issueStatus') in {'FIXED', 'FALSE_POSITIVE', 'ACCEPTED'} or issue.get('status') == 'CLOSED':
            record['status'] = 'already_disposed_by_platform'
        else:
            comment = '本地逐项复核：' + row['reason'] + '\n源码 SHA256：' + current_hash + '\n扫描分析：' + task['analysisId'] + '\n复核记录：reports/sonar-remediation-2026-10-11/' + args.plan + '。' + row.get('evidence_note', '')
            record['comment_result'] = sonar.request('/api/issues/add_comment', {'issue': row['key'], 'text': comment}, auth, True)
            record['transition_result'] = sonar.request('/api/issues/do_transition', {'issue': row['key'], 'transition': row['transition']}, auth, True)
            after = sonar.request('/api/issues/search', {'issues': row['key'], 'ps': 10, 'additionalFields': '_all'}, auth)['issues']
            expected = 'FALSE_POSITIVE' if row['transition'] == 'falsepositive' else 'ACCEPTED'
            record['after'] = after
            if len(after) != 1 or after[0].get('issueStatus') != expected:
                record['status'] = 'transition_not_verified'
                result_path.write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding='utf-8')
                raise RuntimeError('Unexpected transition result: ' + row['key'])
            record['status'] = 'platform_disposition_verified'
        result_path.write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding='utf-8')
    report['completed_at'] = datetime.now().astimezone().isoformat()
    result_path.write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding='utf-8')
    print(json.dumps({'records': len(results), 'verified': sum(row['status'] == 'platform_disposition_verified' for row in results), 'skipped': sum(row['status'] == 'skipped_stale_source_evidence' for row in results)}))


if __name__ == '__main__':
    main()
