"""合并各分工原问题状态；源码处理与平台关闭始终分开统计。"""
from collections import Counter
from pathlib import Path
import datetime as dt
import json

OUT = Path(__file__).resolve().parent


def main():
    ledger_path = OUT / 'issues-ledger.json'
    ledger = json.loads(ledger_path.read_text(encoding='utf-8'))
    rows = {row['issue_key']: row for row in ledger['issues']}
    names = ['root', 'frontend', 'python', 'go', 'root-go', 'go-server', 'go-modelgovernance', 'go-server-question-bank', 'root-go-final-02']
    priorities = {'pending': 0, 'read_pending_refactor': 0, 'changed_awaiting_tests': 1, 'implemented_needs_validation': 1, 'code_modified_pending_validation': 1, 'implemented_needs_rescan': 2, 'changed_awaiting_sonar': 2, 'validated_fix_pending_rescan': 2, 'validated_fix_pending_platform_rescan': 2, 'validated_source_fix_pending_platform_rescan': 2}
    for name in names:
        path = OUT / (name + '-progress.json')
        if not path.exists():
            continue
        progress = json.loads(path.read_text(encoding='utf-8'))
        entries = progress.get('issue_states') or progress.get('issues') or []
        for entry in entries:
            key = entry['key']
            if key not in rows:
                raise RuntimeError('Unknown original issue key: ' + key)
            row = rows[key]
            state = entry.get('state') or entry.get('status')
            previous = row.get('status', 'pending')
            # 同一来源明确报告源码在验证后变化时，旧的通过结果不能覆盖待重验状态。
            needs_revalidation = state == 'external_source_change_pending_revalidation'
            if not needs_revalidation and priorities.get(state, 2) < priorities.get(previous, 2):
                continue
            row['status'] = state
            row['owner_report'] = path.name
            row['evidence'] = entry.get('evidence', [])
            row['change_or_reason'] = entry.get('change') or entry.get('reason')
            if entry.get('batch'):
                row['batch'] = entry['batch']
    # Go智能体退出前已经保存第12批真实测试结果，但状态写入晚于运行结果。
    batch_path = OUT / 'go-batch12-tests.json'
    if batch_path.exists():
        batch = json.loads(batch_path.read_text(encoding='utf-8'))
        passed = {row['Package'].split('/services/api-gateway/', 1)[1] for row in batch['package_results'] if row['Action'] == 'pass'}
        if batch['exit_code'] == 0:
            for row in rows.values():
                if row.get('batch') == 'go-batch12' and row['status'] == 'changed_awaiting_tests' and any(row['path'].startswith('services/api-gateway/' + package + '/') for package in passed):
                    row['status'] = 'changed_awaiting_sonar'
                    row['evidence'] = list(row.get('evidence', [])) + [batch_path.name]
    changed_states = {'changed_awaiting_tests', 'implemented_needs_validation', 'code_modified_pending_validation', 'implemented_needs_rescan', 'changed_awaiting_sonar', 'validated_fix_pending_rescan', 'validated_fix_pending_platform_rescan', 'validated_source_fix_pending_platform_rescan', 'external_source_change_pending_revalidation'}
    changed = sum(row['status'] in changed_states for row in rows.values())
    counts = Counter(row['status'] for row in rows.values())
    updated = dt.datetime.now().astimezone().isoformat()
    parallel_path = OUT / 'parallel-agent-state.json'
    parallel = json.loads(parallel_path.read_text(encoding='utf-8')) if parallel_path.exists() else {'status': 'unverified'}
    audit_path = OUT / 'checkpoint02-post-disposition-audit.json'
    audit = json.loads(audit_path.read_text(encoding='utf-8')) if audit_path.exists() else None
    platform_path = OUT / 'original-platform-statuses-after-phase02.json'
    if platform_path.exists():
        platform = json.loads(platform_path.read_text(encoding='utf-8'))['issues']
        if {issue['key'] for issue in platform} != set(rows):
            raise RuntimeError('Platform status identity differs from the original ledger')
        for issue in platform:
            rows[issue['key']]['checkpoint02_platform_status_after_dispositions'] = issue['issueStatus']
    latest_audit = None
    for checkpoint in sorted(OUT.glob('full-checkpoint-*'), reverse=True):
        for phase in ('final', 'pre-disposition'):
            path = checkpoint / (phase + '-audit.json')
            if path.exists():
                latest_audit = json.loads(path.read_text(encoding='utf-8'))
                statuses = json.loads((checkpoint / (phase + '-original-statuses.json')).read_text(encoding='utf-8'))['issues']
                if {issue['key'] for issue in statuses} != set(rows):
                    raise RuntimeError('Latest platform status identity differs from the original ledger')
                for issue in statuses:
                    rows[issue['key']]['latest_platform_status'] = issue['issueStatus']
                    rows[issue['key']]['latest_platform_analysis_id'] = latest_audit['analysis_id']
                break
        if latest_audit:
            break
    # 只有当前源码与冻结输入一致且全部告警已处理，才能称为最终完成。
    platform_counts = latest_audit['original_status_counts'] if latest_audit else {}
    confirmed = sum(platform_counts.get(state, 0) for state in ('FIXED', 'FALSE_POSITIVE', 'ACCEPTED'))
    summary = {'updated_at': updated, 'original_count': len(rows), 'source_changes': changed, 'source_change_percent': round(changed / len(rows) * 100, 2), 'source_count_note': '源码已处理状态，包括需测试/复扫及已有源码解决；不表示平台最终确认数。', 'status_counts': dict(counts), 'full_original_project_rescan_confirmed': platform_counts.get('FIXED', 0) if latest_audit else audit['checkpoint_original_status_counts'].get('FIXED', 0) if audit else 0, 'checkpoint02_audit': audit, 'latest_full_scan_audit': latest_audit, 'platform_confirmed_original_count': confirmed if latest_audit else None, 'platform_confirmed_original_percent': round(confirmed / len(rows) * 100, 2) if latest_audit else None, 'final_current_workspace_scan_complete': latest_audit['all_issue_work_complete'] if latest_audit else False, 'parallel_agents': parallel}
    ledger['updated_at'] = updated
    ledger['summary'] = summary
    ledger_path.write_text(json.dumps(ledger, ensure_ascii=False, indent=2), encoding='utf-8')
    (OUT / 'progress-summary.json').write_text(json.dumps(summary, ensure_ascii=False, indent=2), encoding='utf-8')
    text = [f'# SonarQube 修复进度（{updated}）', '', f'原报告 {len(rows)} 项；{changed} 项已处理源码（{summary["source_change_percent"]}%）。源码状态包含待测试和待复扫，不能当作平台关闭数。', '']
    if audit:
        text.extend(['全仓 checkpoint02 扫描与后台计算均成功；冻结版本原问题状态：' + json.dumps(audit['checkpoint_original_status_counts'], ensure_ascii=False) + '。', '', '逐项处置后原问题平台状态：' + json.dumps(audit['post_disposition_original_status_counts'], ensure_ascii=False) + '。人工误报/接受与源码FIXED分开统计。', '', f'冻结版本额外出现 {audit["checkpoint_new_issues"]} 项；后续修复仍需最终全仓复扫。', ''])
    if latest_audit:
        text.extend([f'最新完整复扫：{latest_audit["checkpoint"]}；原问题平台状态：' + json.dumps(platform_counts, ensure_ascii=False) + '。', '', f'原问题已确认处置 {confirmed}/{len(rows)}（{summary["platform_confirmed_original_percent"]}%）；当前未处置告警 {latest_audit["active_count"]} 项。', '', '新增47项状态：' + json.dumps(latest_audit['checkpoint02_new_status_counts'], ensure_ascii=False) + '；是否达到最终当前源码完成条件：' + str(summary['final_current_workspace_scan_complete']) + '。', ''])
    text.extend(['| 源码处理状态 | 数量 |', '| --- | ---: |'])
    text.extend(f'| {state} | {count} |' for state, count in sorted(counts.items()))
    text.extend(['', '并行状态见 parallel-agent-state.json；源代码、测试记录、失败日志和逐文件pending状态保留。', '', '详细原问题状态见 issues-ledger.json；原 platform-audit 报告保持不变。'])
    (OUT / 'progress-summary.md').write_text('\n'.join(text) + '\n', encoding='utf-8')
    print(json.dumps({'source_changes': changed, 'original_count': len(rows), 'source_change_percent': summary['source_change_percent'], 'full_project_confirmed': summary['full_original_project_rescan_confirmed']}))


if __name__ == '__main__':
    main()
