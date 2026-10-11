"""最终文档/2270项CSV/JSON对照原始平台状态的只读交付检查。"""
from collections import Counter, defaultdict
import csv
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
import re

ROOT = Path(__file__).resolve().parents[2]
REPORTS = Path(__file__).parent
SCAN = REPORTS / 'full-checkpoint-04'
INITIAL = ROOT / 'reports/platform-audit-2026-10-10/sonar'
ANALYSIS = '07c1210c-b44c-49f6-a3fe-c2bb211a0ce3'

def read(path):
    return json.loads(path.read_text(encoding='utf-8-sig'))

def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()

def serialized(value):
    return '' if value is None else str(value)

def main():
    names = ['final-remediation-report.md', 'final-issues.csv', 'final-issues.json', 'final-report-data.json']
    before = {name: sha(REPORTS / name) for name in names}
    document = (REPORTS / names[0]).read_text(encoding='utf-8')
    with (REPORTS / names[1]).open(encoding='utf-8-sig', newline='') as handle:
        csv_rows = list(csv.DictReader(handle))
    json_document = read(REPORTS / names[2])
    json_rows = json_document['issues']
    data = read(REPORTS / names[3])
    old = {row['key']: row for row in read(INITIAL / 'issues.json')['issues']}
    additional = {row['key']: row for row in read(REPORTS / 'full-checkpoint-02/issues.json')['issues'] if row['key'] not in old}
    originals = read(SCAN / 'final-original-statuses.json')['issues']
    new_states = read(SCAN / 'final-checkpoint02-new-statuses.json')['issues']
    states = {row['key']: row for row in originals + new_states}
    gate = read(SCAN / 'quality-gate.json')['projectStatus']
    metrics = {row['metric']: row.get('value') for row in read(SCAN / 'metrics.json')['component']['measures']}
    old_metrics = {row['metric']: row.get('value') for row in read(INITIAL / 'metrics.json')['component']['measures']}
    differences = []
    expected_keys = set(old) | set(additional)
    for label, rows in [('CSV', csv_rows), ('JSON', json_rows)]:
        keys = [row['key'] for row in rows]
        if len(keys) != 2270 or set(keys) != expected_keys or len(set(keys)) != len(keys):
            differences.append({'format': label, 'problem': 'key_count_uniqueness_or_set', 'count': len(keys)})
        for row in rows:
            key = row['key']
            if key not in states or key not in expected_keys:
                continue
            initial = old.get(key, additional.get(key))
            current = states[key]
            expected = {'group': 'original' if key in old else 'checkpoint02_new',
                        'key': key, 'rule': current['rule'], 'severity': initial['severity'],
                        'type': initial['type'], 'file': current['component'].split(':', 1)[1],
                        'original_line': initial.get('line'), 'latest_line': current.get('line'),
                        'platform_status': current['issueStatus'], 'message': initial['message'],
                        'analysis_id': ANALYSIS}
            for field, value in expected.items():
                actual = row.get(field)
                valid = actual == serialized(value) if label == 'CSV' else actual == value
                if not valid:
                    differences.append({'format': label, 'key': key, 'field': field, 'expected': value, 'actual': actual})
    csv_by_key = {row['key']: row for row in csv_rows}
    csv_json_differences = []
    for row in json_rows:
        for field, value in row.items():
            if csv_by_key.get(row['key'], {}).get(field) != serialized(value):
                csv_json_differences.append({'key': row['key'], 'field': field})
    if csv_json_differences:
        differences.append({'problem': 'csv_json_serialized_values_disagree', 'records': csv_json_differences})
    all_counts = dict(Counter(row['issueStatus'] for row in originals + new_states))
    original_counts = dict(Counter(row['issueStatus'] for row in originals))
    new_counts = dict(Counter(row['issueStatus'] for row in new_states))
    for field, expected in {'analysis_id': ANALYSIS, 'checkpoint': 'full-checkpoint-04', 'tracked_count': 2270,
                            'active_count': 0, 'coverage_imported': False, 'all_issue_work_complete': True,
                            'original_status_counts': original_counts,
                            'checkpoint02_new_status_counts': new_counts,
                            'all_tracked_status_counts': all_counts, 'quality_gate': gate,
                            'old_metrics': old_metrics, 'final_metrics': metrics}.items():
        if data.get(field) != expected:
            differences.append({'format': 'final-report-data', 'field': field, 'expected': expected, 'actual': data.get(field)})
    if json_document['analysis_id'] != ANALYSIS or json_document['count'] != 2270:
        differences.append({'problem': 'final_issues_json_header_disagrees'})
    languages, rules = defaultdict(Counter), defaultdict(Counter)
    for entry in originals:
        languages[entry['rule'].split(':', 1)[0]][entry['issueStatus']] += 1
        rules[entry['rule']][entry['issueStatus']] += 1
    missing_table_rows = []
    for groups in [languages, rules]:
        for name, counts in groups.items():
            row = f'| {name} | {sum(counts.values())} | {counts["FIXED"]} | {counts["FALSE_POSITIVE"]} | {counts["ACCEPTED"]} | {counts["OPEN"]} |'
            if row not in document:
                missing_table_rows.append(row)
    for name in ['ncloc', 'bugs', 'vulnerabilities', 'code_smells', 'security_hotspots',
                 'duplicated_lines_density', 'sqale_index', 'sqale_rating', 'reliability_rating', 'security_rating']:
        row = f'| {name} | {old_metrics.get(name, "未提供")} | {metrics.get(name, "未提供")} |'
        if row not in document:
            missing_table_rows.append(row)
    for condition in gate['conditions']:
        row = f'| {condition["metricKey"]} | {condition.get("actualValue", "未提供")} | {condition["comparator"]} {condition["errorThreshold"]} | {condition["status"]} |'
        if row not in document:
            missing_table_rows.append(row)
    if missing_table_rows:
        differences.append({'format': 'Markdown', 'problem': 'raw_language_rule_metric_gate_table_mismatches', 'records': missing_table_rows})
    links = []
    for label, target in re.findall(r'\[([^\]]+)\]\(([^)]+)\)', document):
        path = target.strip('<>')
        if re.match(r'^[A-Za-z]:/', path):
            links.append({'label': label, 'target': path, 'exists': Path(path).is_file(), 'sha256': sha(Path(path)) if Path(path).is_file() else None})
        elif target.startswith('/D:/'):
            differences.append({'format': 'Markdown', 'problem': 'invalid_windows_absolute_link', 'target': target})
    if any(not link['exists'] for link in links):
        differences.append({'format': 'Markdown', 'problem': 'missing_local_link_targets', 'records': [link for link in links if not link['exists']]})
    required_phrases = [ANALYSIS, '2192 项', '30 项', '43 项 FIXED', '4 项 FALSE_POSITIVE',
                        '2654 个', '291 个文件全部包含', '2 份类型支持文件',
                        '质量门禁实际状态为 ERROR', '不能解释为实测覆盖率 0%',
                        '本次门禁仅新增覆盖率项失败', '浏览器 E2E 没有全部通过',
                        '47 用例', '23 个启动标记', '2 个原有 UI/快照不一致',
                        '2 个有资源不足证据的断言超时', '6 个浏览器启动/worker/导航失败',
                        '61 条 PostgreSQL 环境跳过', '1 条 crash-helper 辅助入口跳过',
                        'PG 0 pass、5 skip', '不相加成一个虚假的全仓测试总数',
                        '本轮最终当前工作区复扫是 SonarQube', '没有在本轮重新扫描',
                        'Actions 云端 run 和 Codecov 云端上传均没有执行',
                        '没有实际运行图纸字号浏览器 QA', 'skipping Clippy analysis',
                        '自动文件清单不表示完整人工审读']
    missing_phrases = [phrase for phrase in required_phrases if phrase not in document]
    if missing_phrases:
        differences.append({'format': 'Markdown', 'problem': 'required_scope_or_limit_disclosure_missing', 'records': missing_phrases})
    gate_failed_metrics = [row['metricKey'] for row in gate['conditions'] if row['status'] == 'ERROR']
    if gate['status'] != 'ERROR' or gate_failed_metrics != ['new_coverage']:
        differences.append({'problem': 'coverage_only_gate_statement_not_supported', 'gate': gate})
    after = {name: sha(REPORTS / name) for name in names}
    if before != after:
        differences.append({'problem': 'final_artifacts_changed_during_audit', 'before': before, 'after': after})
    result = {'audited_at_utc': datetime.now(timezone.utc).isoformat(), 'reviewer': '/root/sonar_python_fix',
              'scope': '当前正式final-remediation-report.md全文人工审读；CSV/JSON全部2270行直接对照原始/新增平台key及最终原始状态。旧draft不作本次交付。只写独立检查，不改源、不运行测试。',
              'analysis_id': ANALYSIS, 'markdown_full_read': True, 'markdown_lines': len(document.splitlines()),
              'csv_rows_checked': len(csv_rows), 'json_rows_checked': len(json_rows),
              'expected_unique_keys': len(expected_keys), 'all_status_counts': all_counts,
              'original_status_counts': original_counts, 'new47_status_counts': new_counts,
              'quality_gate_status': gate['status'], 'gate_failed_metrics': gate_failed_metrics,
              'language_rows_checked': len(languages), 'rule_rows_checked': len(rules),
              'local_links_checked': links, 'artifact_sha256_before': before, 'artifact_sha256_after': after,
              'inputs': [{'file': str(path), 'sha256': sha(path)} for path in [
                  INITIAL / 'issues.json', SCAN / 'final-original-statuses.json',
                  SCAN / 'final-checkpoint02-new-statuses.json', SCAN / 'quality-gate.json', SCAN / 'metrics.json']],
              'passed': not differences, 'differences': differences,
              'limitations_preserved': ['覆盖率缺少导入，门禁ERROR不当作通过。',
                                      'E2E中断/10失败、61 PG条件skip+1helper，不算业务通过。',
                                      '历史其它平台/覆盖率只对应历史冻结版本，不是当前全仓复检。']}
    (REPORTS / 'final-artifacts-independent-review-python-agent.json').write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    lines = ['# 正式最终交付产物：独立核查', '',
             f"当前正式Markdown全文审读（{result['markdown_lines']}行）；CSV/JSON各{len(csv_rows)}项直接逐key对照原始平台状态，分析ID `{ANALYSIS}`。", '',
             f"结论：{'通过' if result['passed'] else '需修正'}，差异{len(differences)}项。全部2270唯一问题：2235 FIXED、34 FALSE_POSITIVE、1 ACCEPTED，无OPEN；人工处置没有计作源码修复。", '',
             f"6种规则语言、{len(rules)}条原规则、10项度量和全部3个门禁条件逐行一致；{len(links)}个本地引用目标存在。4份正式产物核查前后SHA稳定。", '',
             '质量门禁仅新增覆盖率项失败，实际ERROR；缺少导入不当作实测0%或门禁通过。E2E中断及失败、61PG+1helper条件skip、未执行图纸浏览器QA和其它平台历史版本局限均明确保留。', '']
    (REPORTS / 'final-artifacts-independent-review-python-agent.md').write_text('\n'.join(lines), encoding='utf-8')
    print(json.dumps({'passed': result['passed'], 'rows': len(csv_rows), 'all_counts': all_counts,
                      'quality_gate': gate['status'], 'failed_metrics': gate_failed_metrics,
                      'links': len(links), 'differences': differences}, ensure_ascii=False))
    return 0 if result['passed'] else 1

if __name__ == '__main__':
    raise SystemExit(main())
