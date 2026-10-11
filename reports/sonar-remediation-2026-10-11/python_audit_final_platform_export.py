"""只读最终本地平台导出，独立逐 key/扫描版本/源码 SHA 核对；不修改平台或代理账本。"""
from collections import Counter
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
import re
import sys

ROOT = Path(__file__).resolve().parents[2]
REPORTS = Path(__file__).parent
BASE = ROOT / 'reports/platform-audit-2026-10-10/sonar'

def read(path):
    return json.loads(path.read_text(encoding='utf-8-sig'))

def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest() if path.is_file() else None

def issue_list(document):
    return document['issues'] if isinstance(document, dict) else document

def profiles(document):
    fields = ['key', 'name', 'language', 'isBuiltIn', 'activeRuleCount', 'activeDeprecatedRuleCount', 'rulesUpdatedAt']
    return sorted([{key: profile.get(key) for key in fields} for profile in document.get('profiles', [])], key=lambda value: str(value['key']))

def properties(path):
    # 明确只读取影响范围/规则的白名单；凭据和值不会进入审计输出。
    allowed = ['sonar.projectKey', 'sonar.exclusions', 'sonar.test.exclusions',
               'sonar.issue.ignore.allfile', 'sonar.issue.ignore.multicriteria',
               'sonar.coverage.exclusions', 'sonar.cpd.exclusions']
    result = {}
    for line in path.read_text(encoding='utf-8-sig').splitlines():
        if '=' not in line or line.lstrip().startswith('#'):
            continue
        name, value = line.split('=', 1)
        if name.strip() in allowed or name.strip().startswith('sonar.issue.ignore.multicriteria.'):
            result[name.strip()] = value.strip()
    return result

def reconcile(expected, observed):
    by_key = {entry['key']: entry for entry in observed}
    identity = []
    for key in set(expected) & set(by_key):
        initial, current = expected[key], by_key[key]
        for field in ['rule', 'component', 'severity']:
            if initial[field] != current[field]:
                identity.append({'key': key, 'field': field, 'expected': initial[field], 'current': current[field]})
    return {'requested_count': len(expected), 'returned_count': len(observed),
            'unique_returned_count': len(by_key),
            'missing_keys': sorted(set(expected) - set(by_key)),
            'unexpected_keys': sorted(set(by_key) - set(expected)),
            'duplicate_keys': [key for key, count in Counter(entry['key'] for entry in observed).items() if count > 1],
            'identity_mismatches': identity,
            'issue_status_counts': dict(Counter(entry.get('issueStatus', entry.get('status')) for entry in observed)),
            'legacy_status_counts': dict(Counter(entry['status'] for entry in observed)),
            'unresolved_issues': [{key: entry.get(key) for key in ['key', 'rule', 'component', 'line', 'status', 'issueStatus', 'message']}
                                  for entry in observed if entry.get('issueStatus', entry['status']) not in ['FIXED', 'FALSE_POSITIVE', 'ACCEPTED']],
            'records': [{key: entry.get(key) for key in ['key', 'rule', 'component', 'status', 'issueStatus', 'resolution', 'updateDate']}
                        for entry in observed]}

def main():
    directory = REPORTS / (sys.argv[1] if len(sys.argv) > 1 else 'full-checkpoint-03')
    expected_analysis = sys.argv[2] if len(sys.argv) > 2 else None
    new_status_path = directory / (sys.argv[3] if len(sys.argv) > 3 else 'new-issue-current-statuses.json')
    output_stem = sys.argv[4] if len(sys.argv) > 4 else 'delivery-independent-final-platform-audit-python-agent'
    phase = sys.argv[5] if len(sys.argv) > 5 else None
    original_status_path = directory / (f'{phase}-original-statuses.json' if phase else 'original-issue-current-statuses.json')
    active_path = directory / (f'{phase}-active-issues.json' if phase else 'issues.json')
    original_list = issue_list(read(BASE / 'issues.json'))
    original = {entry['key']: entry for entry in original_list}
    checkpoint02 = issue_list(read(REPORTS / 'full-checkpoint-02/issues.json'))
    previous_new = {entry['key']: entry for entry in checkpoint02 if entry['key'] not in original}
    initial_sha = sha(BASE / 'issues.json')
    initial_sha_matches_baseline = initial_sha == read(REPORTS / 'baseline.json')['original_issues_sha256']
    original_states = issue_list(read(original_status_path))
    new_states = issue_list(read(new_status_path))
    final_active = issue_list(read(active_path))
    original_check = reconcile(original, original_states)
    new_check = reconcile(previous_new, new_states)
    scanner = read(directory / 'scanner-exit.json')
    ce = read(directory / 'compute-engine-task.json')['task']
    export = read(directory / 'export-manifest.json')
    status = read(directory / 'execution-status.json')
    scope = read(directory / 'source-scope.json')
    source_differences, frozen_differences = [], []
    frozen_root = Path(scope['snapshot'])
    for entry in scope['files']:
        relative, recorded = entry['path'], entry['sha256']
        actual, frozen = sha(ROOT / relative), sha(frozen_root / relative)
        if actual != recorded:
            source_differences.append({'file': relative, 'scan_sha256': recorded, 'current_sha256': actual})
        if frozen != recorded:
            frozen_differences.append({'file': relative, 'recorded_scan_sha256': recorded, 'actual_frozen_sha256': frozen})
    support_files = []
    for relative in scope.get('type_resolution_support_files', []):
        current, frozen = sha(ROOT / relative), sha(frozen_root / relative)
        support_files.append({'file': relative, 'current_sha256': current,
                              'actual_frozen_sha256': frozen, 'current_matches_frozen': current == frozen and frozen is not None})
    support_differences = [entry for entry in support_files if not entry['current_matches_frozen']]
    original_files = {entry['component'].split(':', 1)[1] for entry in original.values()}
    maintained_files = {entry['path'] for entry in scope['files']}
    duplicate_scope_paths = [path for path, count in Counter(entry['path'] for entry in scope['files']).items() if count > 1]
    missing_original_files = sorted(original_files - maintained_files)
    profile_match = profiles(read(BASE / 'quality-profiles.json')) == profiles(read(directory / 'quality-profiles.json'))
    initial_properties = properties(BASE / 'effective-scan.properties')
    final_properties = properties(directory / 'effective-scan.properties')
    property_match = initial_properties == final_properties
    dispositions = []
    for entry in original_states + new_states:
        if entry.get('issueStatus', entry['status']) not in ['FALSE_POSITIVE', 'ACCEPTED']:
            continue
        relative = entry['component'].split(':', 1)[1]
        current, frozen = sha(ROOT / relative), sha(frozen_root / relative)
        comments = '\n'.join(comment.get('markdown', '') for comment in entry.get('comments', []))
        recorded = re.findall(r'源码 SHA256[：:]\s*([a-f0-9]{64})', comments)
        plans = re.findall(r'复核记录[：:]\s*(reports/[^。\s]+\.json)', comments)
        dispositions.append({'key': entry['key'], 'status': entry.get('issueStatus', entry['status']),
                             'file': relative, 'current_sha256': current, 'frozen_sha256': frozen,
                             'platform_comment_source_sha256': recorded,
                             'comment_sha_matches_current_and_frozen': current == frozen and current in recorded,
                             'referenced_review_records': [{'file': path, 'exists': (ROOT / path).is_file(),
                                                           'sha256': sha(ROOT / path)} for path in plans],
                             'platform_comment_present': bool(comments),
                             'reason_recorded': bool(comments.split('源码 SHA256')[0].strip())})
    disposition_problems = [entry for entry in dispositions
                            if not entry['comment_sha_matches_current_and_frozen']
                            or not entry['platform_comment_present'] or not entry['reason_recorded']
                            or not entry['referenced_review_records']
                            or not all(record['exists'] for record in entry['referenced_review_records'])]
    quality = read(directory / 'quality-gate.json')['projectStatus']
    new_untracked = [entry for entry in final_active if entry['key'] not in set(original) | set(previous_new)]
    known_unresolved = {entry['key'] for entry in original_check['unresolved_issues'] + new_check['unresolved_issues']}
    active_keys = {entry['key'] for entry in final_active}
    active_state_mismatches = {'active_missing_from_unresolved': sorted(active_keys - known_unresolved),
                               'unresolved_missing_from_active': sorted(known_unresolved - active_keys),
                               'duplicate_active_keys': [key for key, count in Counter(entry['key'] for entry in final_active).items() if count > 1]}
    defects = []
    if len(original_list) != 2223 or len(original) != 2223 or len(previous_new) != 47 or not initial_sha_matches_baseline:
        defects.append({'problem': 'original_baseline_or_known_new_keys_not_intact',
                        'original_records': len(original_list), 'original_unique': len(original),
                        'known_new_unique': len(previous_new), 'original_sha_matches_baseline': initial_sha_matches_baseline})
    if duplicate_scope_paths or scope['maintained_files'] != len(maintained_files):
        defects.append({'problem': 'scan_scope_count_or_unique_paths_disagree',
                        'duplicate_paths': duplicate_scope_paths,
                        'declared_maintained_count': scope['maintained_files'],
                        'actual_unique_paths': len(maintained_files)})
    if any(active_state_mismatches.values()):
        defects.append({'problem': 'active_export_and_tracked_current_statuses_disagree',
                        'details': active_state_mismatches})
    for label, check in [('original', original_check), ('checkpoint02_new', new_check)]:
        for field in ['missing_keys', 'unexpected_keys', 'duplicate_keys', 'identity_mismatches', 'unresolved_issues']:
            if check[field]:
                defects.append({'scope': label, 'problem': field, 'records': check[field]})
    for label, value in [('current_source_differs_from_scanned', source_differences),
                         ('frozen_source_differs_from_recorded_sha', frozen_differences),
                         ('type_support_current_source_differs_from_scanned', support_differences),
                         ('platform_disposition_source_or_review_record_not_bound', disposition_problems),
                         ('original_issue_files_missing_from_scan_scope', missing_original_files),
                         ('new_untracked_active_issues', new_untracked)]:
        if value:
            defects.append({'problem': label, 'records': value})
    if scanner['exit_code'] != 0 or ce['status'] != 'SUCCESS' or status.get('status') != 'complete' or not export.get('complete'):
        defects.append({'problem': 'scan_or_export_not_successful', 'scanner': scanner, 'ce_status': ce['status'], 'execution': status, 'export': export})
    if expected_analysis and ce.get('analysisId') != expected_analysis:
        defects.append({'problem': 'analysis_id_mismatch', 'expected': expected_analysis, 'actual': ce.get('analysisId')})
    if not profile_match or not property_match:
        defects.append({'problem': 'quality_profile_or_scope_settings_changed', 'profile_match': profile_match,
                        'properties_match': property_match, 'original_safe_properties': initial_properties,
                        'final_safe_properties': final_properties})
    result = {
        'audited_at_utc': datetime.now(timezone.utc).isoformat(),
        'reviewer': '/root/sonar_python_fix',
        'scope': '本地最终完整导出的 2223 原 key +47 已知新增 key，以及 active 额外新增、Scanner/CE、冻结源/current SHA、原规则/范围与质量门禁独立对账。无服务状态修改，无远端访问。',
        'analysis_id': ce.get('analysisId'), 'project': export.get('project'),
        'export_phase': phase or 'scan_export',
        'scanner_exit_code': scanner['exit_code'], 'compute_engine_status': ce['status'],
        'export_complete': export['complete'],
        'tracked_total': len(original) + len(previous_new),
        'original_export_sha256': initial_sha,
        'original_export_sha256_matches_baseline': initial_sha_matches_baseline,
        'original': original_check, 'checkpoint02_new': new_check,
        'final_active_issue_count': len(final_active),
        'new_untracked_active_issues': new_untracked,
        'active_export_and_tracked_state_mismatches': active_state_mismatches,
        'maintained_source_files_checked': len(scope['files']),
        'maintained_source_unique_files_checked': len(maintained_files),
        'scan_scope_duplicate_paths': duplicate_scope_paths,
        'type_resolution_support_files_checked': support_files,
        'original_issue_files_count': len(original_files),
        'original_issue_files_missing': missing_original_files,
        'current_source_mismatches': source_differences,
        'frozen_source_mismatches': frozen_differences,
        'original_quality_profiles_match': profile_match,
        'original_scope_and_ignore_properties_match': property_match,
        'platform_disposition_evidence_checked': dispositions,
        'platform_disposition_source_or_review_record_problems': disposition_problems,
        'quality_gate': quality,
        'quality_gate_is_separate_from_issue_resolution': True,
        'quality_gate_passed': quality['status'] == 'OK',
        'quality_gate_limitations': '未导入覆盖率时平台覆盖率0只是缺少数据，不能报告真实代码测试覆盖率为0；门禁ERROR仍如实保留。原始问题修复结论与门禁/业务准确率分别报告。',
        'dynamic_limits': ['前端 E2E 中断/10失败，未更新像素快照；全47 UI验收未完成。',
                           'server 188 pass、61 PG条件skip、1辅助入口skip；skip不计实际DB验收。',
                           '静态规则修复/平台FP或ACCEPTED不证明真实学校数据和AI阅卷准确率。'],
        'verdict': 'all_tracked_issues_resolved_and_scan_matches_current_sources' if not defects else 'further_action_required',
        'defects': defects,
        'input_files': [{'file': str(path), 'sha256': sha(path)} for path in [
            active_path, original_status_path, new_status_path,
            directory / 'scanner-exit.json', directory / 'compute-engine-task.json',
            directory / 'source-scope.json', directory / 'quality-gate.json',
            directory / 'quality-profiles.json', directory / 'effective-scan.properties',
            directory / 'execution-status.json', directory / 'export-manifest.json']],
    }
    output = REPORTS / f'{output_stem}.json'
    output.write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    lines = ['# 最终本地 SonarQube：独立逐项对账', '',
             f"结论：{result['verdict']}。analysisId `{result['analysis_id']}`；Scanner exit {scanner['exit_code']}，CE {ce['status']}，导出完整 {export['complete']}。", '',
             f"2223 原问题：{original_check['issue_status_counts']}。47 已知新增：{new_check['issue_status_counts']}。总跟踪2270 key；active {len(final_active)}，额外未跟踪 active {len(new_untracked)}。源码 FIXED、人工 FALSE_POSITIVE 和 ACCEPTED 分开统计，不把处置算作源码修复。", '',
             f"核对{len(scope['files'])}份维护源码及{len(support_files)}份类型支持文件冻结/当前SHA；原告警{len(original_files)}文件均须包含。current差异{len(source_differences)}，冻结差异{len(frozen_differences)}，类型支持差异{len(support_differences)}，原规则配置匹配{profile_match}，原排除/忽略配置匹配{property_match}。", '',
             f"质量门禁仍为 {quality['status']}；它与问题关闭数独立。如缺覆盖率数据，不把平台0声称为真实测试覆盖率。UI中断/实际断言差异与PG条件skip限制保留。", '',
             f'需继续处理的证据问题：{len(defects)}。完整逐key状态、额外告警、源SHA和门禁条件见同名JSON。', '']
    (REPORTS / f'{output_stem}.md').write_text('\n'.join(lines), encoding='utf-8')
    print(json.dumps({'verdict': result['verdict'], 'analysis_id': result['analysis_id'],
                      'original_counts': original_check['issue_status_counts'],
                      'new47_counts': new_check['issue_status_counts'],
                      'active': len(final_active), 'quality_gate': quality['status'],
                      'defect_count': len(defects),
                      'defects': [{'problem': entry.get('problem'), 'scope': entry.get('scope'),
                                   'count': len(entry.get('records', []))} for entry in defects]}, ensure_ascii=False))
    return 0 if not defects else 1

if __name__ == '__main__':
    raise SystemExit(main())
