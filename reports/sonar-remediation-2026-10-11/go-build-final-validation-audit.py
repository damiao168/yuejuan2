"""只汇总既有证据并校验本地源码身份；不执行测试或平台扫描。"""
import hashlib
import json
import re
from collections import Counter, defaultdict
from datetime import datetime
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
BASE = Path(__file__).parent
PREFIX = 'edugrade-enterprise/services/api-gateway/'
MODULE = 'services/api-gateway/'

def read(name):
    return json.loads((BASE / name).read_text(encoding='utf-8-sig'))

def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest() if path.is_file() else None

def normalized(path):
    return path.replace('\\', '/')

def package_of(path):
    return str(Path(path).parent).replace('\\', '/').removeprefix(MODULE)

def parse_events(path):
    events, unparsed = [], []
    if path and path.exists():
        for line in path.read_text(encoding='utf-8-sig').splitlines():
            try:
                events.append(json.loads(line))
            except json.JSONDecodeError:
                unparsed.append(line)
    return events, unparsed

def evidence(name):
    data = read(name)
    logname = data.get('event_log') or name.replace('-tests.json', '-test-events.jsonl')
    logpath = BASE / logname
    events, raw = parse_events(logpath)
    package_results = data.get('package_results', [item for item in events if not item.get('Test') and item.get('Action') in {'pass', 'fail', 'skip'}])
    if not package_results and data.get('stdout'):
        for line in data['stdout'].splitlines():
            if line.startswith('ok\t'):
                package_results.append({'Action': 'pass', 'Package': line.split('\t')[1]})
    final_tests = {(item['Package'], item['Test']): item for item in events if item.get('Test') and item.get('Action') in {'pass', 'fail', 'skip'}}
    counts = dict(Counter(item['Action'] for item in final_tests.values())) if events else None
    skips = []
    for (pkg, test), result in final_tests.items():
        if result['Action'] == 'skip':
            output = [item.get('Output', '').strip() for item in events if item.get('Package') == pkg and item.get('Test') == test and item.get('Action') == 'output']
            skips.append({'package': pkg.removeprefix(PREFIX), 'test': test, 'raw_output': output})
    source = data.get('source_identity')
    source_map = {}
    if source:
        source_map = {MODULE + normalized(path): sha for path, sha in read(source['after']).items()}
    elif data.get('source_sha_file'):
        source_map = read(data['source_sha_file'])['files']
    result = {
        'report': name,
        'report_sha256': digest(BASE / name),
        'event_log': logname if logpath.exists() else None,
        'event_log_sha256': digest(logpath),
        'actual_exit_code': data['exit_code'],
        'started_at': data.get('started_at'),
        'completed_at': data.get('finished_at', data.get('completed_at', data.get('observed_completed_at'))),
        'command': data.get('command'),
        'environment': data.get('isolation', data.get('environment', {'GOMAXPROCS': data.get('GOMAXPROCS'), 'cleared_environment_names': data.get('cleared_environment_names')})),
        'reported_test_counts': data.get('test_result_counts'),
        'independently_recounted_test_counts': counts,
        'package_results': package_results,
        'conditionally_skipped_tests': skips,
        'source_identity_evidence': source or data.get('source_sha_file'),
        'source_changed_during_test': source.get('changed_paths') if source else data.get('source_changed_during_test'),
        'limitations': [] if events else ['无逐测试 JSONL；保留包级真实 exit/ok，不推断逐测试通过数、跳过数或测试时 SHA。'],
    }
    if result['actual_exit_code'] != 0:
        failure_lines = [item.get('Output', '').strip() for item in events if re.search(r'(undefined:|cannot |not enough arguments|too many|1455|paging file|pagefile|sqlmock|could not match|was not expected|FAIL|fatal error|out of memory)', item.get('Output', ''), re.I)]
        result['retained_failure_diagnostics'] = failure_lines[:35] + raw[:8]
    return result, final_tests, source_map

go = read('go-progress.json')
qb = read('go-server-question-bank-progress.json')
new = read('go-new-findings-progress.json')
reliability = read('go-server-reliability-independent-review.json')
scope = read('full-checkpoint-03/source-scope.json')
ce_task = read('full-checkpoint-03/compute-engine-task.json')['task']
assert ce_task['status'] == 'SUCCESS' and ce_task['analysisId'] == 'a741715d-5427-49a6-ac74-7dd92ad5cfa1'
original_platform = {item['key']: item for item in read('full-checkpoint-03/pre-disposition-original-statuses.json')['issues']}
new_platform = {item['key']: item for item in read('full-checkpoint-03/pre-disposition-checkpoint02-new-statuses.json')['issues']}
frozen_index = {item['path']: item['sha256'] for item in scope['files']}
frozen_root = Path(scope['snapshot'])
initial = [item for item in go['issue_states'] if item.get('batch')]
assert len(initial) == 129 and len(qb['issue_states']) == 5 and len(new['issue_states']) == 5
originals = initial + qb['issue_states']
assert len({item['key'] for item in originals}) == 134

names = sorted({path.name for path in BASE.glob('go-batch*-tests.json')})
names += ['go-taskmaterials-baseline-tests.json', 'go-server-batch02-tests.json', 'go-server-batch03-tests.json', 'go-server-batch04-tests.json']
executions, tests_by_report, sources_by_report = [], {}, {}
for name in names:
    execution, tests, sources = evidence(name)
    executions.append(execution)
    tests_by_report[name] = tests
    sources_by_report[name] = sources
assert all(execution['reported_test_counts'] is None or execution['independently_recounted_test_counts'] is None or all(execution['reported_test_counts'].get(k, 0) == execution['independently_recounted_test_counts'].get(k, 0) for k in {'pass', 'fail', 'skip'}) for execution in executions)

latest_by_package = {}
for execution in sorted(executions, key=lambda item: item['completed_at'] or ''):
    if execution['report'] in {'go-taskmaterials-baseline-tests.json', 'go-server-batch02-tests.json', 'go-server-batch03-tests.json'}:
        continue
    for result in execution['package_results']:
        if result['Action'] == 'pass':
            latest_by_package[result['Package'].removeprefix(PREFIX)] = execution

ledger_files = {item['path']: item for item in go['files'] if item['review_state'] != 'pending'}
ledger_files.update({item['path']: item for item in qb['files']})
ledger_files.update({item['path']: item for item in new['files']})
issue_paths = {item['path'] for item in originals + new['issue_states']}
assert issue_paths <= ledger_files.keys()
file_rows = []
for path, row in sorted(ledger_files.items()):
    current_sha = digest(ROOT / path)
    frozen_sha = digest(frozen_root / path)
    package = package_of(path)
    latest = latest_by_package.get(package)
    test_sha = sources_by_report[latest['report']].get(path) if latest else None
    file_rows.append({
        'path': path,
        'manual_review_state': row['review_state'],
        'full_file_review_confirmation': 'documented_full_read_and_refactored' if row['review_state'] == 'read_and_refactored' else 'documented_read_and_maintenance_notes; annotated is defined as reviewed in commenting-standard section 6',
        'manual_review_evidence': 'go-server-question-bank-progress.json' if '/server/' in path else ('go-new-findings-progress.json' if path in {item['path'] for item in new['files']} else 'go-progress.json'),
        'maintenance_points': row.get('maintenance_points'),
        'ledger_sha256': row.get('source_sha256'),
        'current_sha256': current_sha,
        'full03_scope_sha256': frozen_index.get(path),
        'full03_actual_snapshot_sha256': frozen_sha,
        'current_matches_ledger': current_sha == row.get('source_sha256'),
        'current_matches_full03_scope_and_actual_snapshot': current_sha == frozen_index.get(path) == frozen_sha,
        'latest_successful_package_test_report': latest['report'] if latest else None,
        'latest_test_actual_exit_code': latest['actual_exit_code'] if latest else None,
        'latest_test_package_action': 'pass' if latest else None,
        'latest_test_recorded_sha256': test_sha,
        'current_matches_latest_test_sha': current_sha == test_sha if test_sha else None,
        'test_sha_limitation': None if test_sha else '该成功执行未记录此文件测试时 SHA；不能仅凭包通过补记哈希身份。',
    })
assert all(row['current_matches_full03_scope_and_actual_snapshot'] for row in file_rows), [row['path'] for row in file_rows if not row['current_matches_full03_scope_and_actual_snapshot']]
assert all(row['current_matches_ledger'] for row in file_rows), [row['path'] for row in file_rows if not row['current_matches_ledger']]
assert not any(row['current_matches_latest_test_sha'] is False for row in file_rows)

issue_rows = []
qb_keys = {item['key'] for item in qb['issue_states']}
new_keys = {item['key'] for item in new['issue_states']}
by_file = {item['path']: item for item in file_rows}
for item in originals + new['issue_states']:
    file = by_file[item['path']]
    latest = latest_by_package[package_of(item['path'])]
    pg = item.get('pg_scenario_result')
    official = (new_platform if item['key'] in new_keys else original_platform)[item['key']]
    assert official['resolution'] == 'FIXED' and official['issueStatus'] == 'FIXED', (item['key'], official['status'], official.get('resolution'), official.get('issueStatus'))
    issue_rows.append({
        'key': item['key'], 'rule': item['rule'], 'severity': item['severity'], 'path': item['path'],
        'original_line': item['original_line'],
        'scope': 'checkpoint02_new' if item['key'] in new_keys else ('original_transferred_question_bank' if item['key'] in qb_keys else 'original_initial_ownership'),
        'batch': item.get('batch'), 'source_changed': True, 'manual_review_state': file['manual_review_state'],
        'package_test_report': latest['report'], 'actual_execution_exit_code': latest['actual_exit_code'],
        'actual_package_result': 'pass', 'validation_boundary': 'compiled_and_package_regression_passed; PostgreSQL scene skipped' if pg else 'associated_package_regression_passed; individual skips detailed by report',
        'postgres_scenario': pg,
        'current_sha256': file['current_sha256'], 'full03_sha_matches': file['current_matches_full03_scope_and_actual_snapshot'],
        'test_sha_matches': file['current_matches_latest_test_sha'],
        'platform_state': 'official_full03_FIXED',
        'official_full03': {'analysis_id': ce_task['analysisId'], 'status': official['status'], 'issue_status': official['issueStatus'], 'resolution': official['resolution'], 'rule': official['rule'], 'component': official['component'], 'update_date': official['updateDate'], 'evidence_report': 'full-checkpoint-03/pre-disposition-checkpoint02-new-statuses.json' if item['key'] in new_keys else 'full-checkpoint-03/pre-disposition-original-statuses.json'},
    })

classification = read('go-server-batch04-skip-classification.json')
server_execution = next(item for item in executions if item['report'] == 'go-server-batch04-tests.json')
server_skips = server_execution['conditionally_skipped_tests']
assert len(server_skips) == 62
assert classification['counts'] == {'postgres_environment_condition': 61, 'auxiliary_entry_condition': 1}
assert {item['test'] for item in server_skips} == {item['test'] for item in classification['tests']}
crash = [item for item in classification['tests'] if item['category'] == 'auxiliary_entry_condition']
assert [item['test'] for item in crash] == ['TestBusinessCommandReservationCrashHelper']
for item in qb['issue_states']:
    assert item['pg_scenario_result']['test'] in {row['test'] for row in server_skips}

affected = []
for row in reliability['affected_scopes']:
    affected.append({**row, 'current_sha256': digest(ROOT / row['path']), 'full03_sha256': frozen_index.get(row['path']), 'current_matches_full03': digest(ROOT / row['path']) == frozen_index.get(row['path'])})
primary = reliability['primary_file']
primary = {**primary, 'current_sha256_rechecked': digest(ROOT / primary['path']), 'full03_sha256': frozen_index.get(primary['path']), 'current_matches_full03': digest(ROOT / primary['path']) == frozen_index.get(primary['path'])}
other_owner = [item for item in go['issue_states'] if not item.get('batch') and item['key'] not in qb_keys]
own_packages = {package_of(row['path']) for row in file_rows}
latest_package_rows = []
for package in sorted(own_packages):
    execution = latest_by_package[package]
    results = [row for (pkg, _), row in tests_by_report[execution['report']].items() if pkg.removeprefix(PREFIX) == package]
    latest_package_rows.append({'package': package, 'report': execution['report'], 'execution_exit_code': execution['actual_exit_code'], 'package_action': 'pass', 'test_result_counts': dict(Counter(row['Action'] for row in results)) if results else None, 'note': 'whole execution exit1 retained but this package actually passed' if execution['actual_exit_code'] != 0 else None})

report = {
    'generated_at': datetime.now().astimezone().isoformat(),
    'reviewer': 'sonar_go_fix', 'read_only_reconciliation': True, 'product_source_edits': False, 'new_test_executions': False, 'remote_github_access': False,
    'scope': {'original_go_total_context': 209, 'initial_owned_originals': 129, 'transferred_question_bank_originals': 5, 'owned_originals': 134, 'checkpoint02_new_findings': 5, 'owned_combined_issue_count': 139, 'other_owner_originals_excluded': len(other_owner), 'original_severity_counts': dict(Counter(item['severity'] for item in originals)), 'new_severity_counts': dict(Counter(item['severity'] for item in new['issue_states']))},
    'summary': {'source_processed_originals': 134, 'associated_package_validation_originals': 134, 'question_bank_postgres_scenes_passed': 0, 'question_bank_postgres_scenes_conditionally_skipped': 5, 'source_processed_new_findings': 5, 'associated_package_validation_new_findings': 5, 'owned_source_file_review_records': len(file_rows), 'owned_source_files_documented_read': len(file_rows), 'owned_source_files_label_read_and_refactored': sum(row['manual_review_state'] == 'read_and_refactored' for row in file_rows), 'owned_source_files_early_label_annotated': sum(row['manual_review_state'] == 'annotated' for row in file_rows), 'current_matches_full03_files': sum(row['current_matches_full03_scope_and_actual_snapshot'] for row in file_rows), 'current_matches_ledger_files': sum(row['current_matches_ledger'] for row in file_rows), 'current_matches_recorded_latest_test_sha_files': sum(row['current_matches_latest_test_sha'] is True for row in file_rows), 'latest_test_sha_not_recorded_files': sum(row['current_matches_latest_test_sha'] is None for row in file_rows), 'test_sha_mismatch_files': 0, 'official_platform_verified_original_fixed': 134, 'official_platform_verified_checkpoint02_new_fixed': 5, 'official_platform_verified_resolved': 139, 'platform_state': 'official_full03_FIXED_for_all_owned_keys; full04 whole repository rescan pending'},
    'freeze': {'checkpoint': 'full-checkpoint-03', 'scope_report': 'full-checkpoint-03/source-scope.json', 'scope_report_sha256': digest(BASE / 'full-checkpoint-03/source-scope.json'), 'snapshot_at': scope['snapshot_at'], 'actual_snapshot_root': str(frozen_root), 'analysis_id': ce_task['analysisId'], 'compute_engine_status': ce_task['status'], 'executed_at': ce_task['executedAt'], 'official_status_report_sha256': {'original': digest(BASE / 'full-checkpoint-03/pre-disposition-original-statuses.json'), 'checkpoint02_new': digest(BASE / 'full-checkpoint-03/pre-disposition-checkpoint02-new-statuses.json')}},
    'execution_counts_note': '每次执行单列真实退出码和 Test/子 Test 结果；同一包多次执行不相加冒充独立测试数。latest_package_validations 每包仅选最近实际 pass；包通过不证明条件 skip 的 PostgreSQL/外部服务场景通过。',
    'test_executions': executions, 'latest_package_validations': latest_package_rows,
    'server_shared_package_boundary': {'report': 'go-server-batch04-tests.json', 'actual_exit_code': 0, 'tests_passed': 188, 'tests_failed': 0, 'tests_conditionally_skipped': 62, 'postgres_environment_skips': 61, 'crash_helper_entry_skip': 1, 'crash_helper_test': 'TestBusinessCommandReservationCrashHelper', 'classification_evidence': 'go-server-batch04-skip-classification.json', 'note': 'server 共用包运行支持本代理 5 题库文件编译和现有包回归；其余 57 原项归 frontend，不在本代理源码处理数量中重复计数。'},
    'question_bank_postgres_boundary': {'passed': 0, 'conditionally_skipped': 5, 'scenes': [item['pg_scenario_result'] for item in qb['issue_states']], 'note': '缺少隔离 PostgreSQL/EDUGRADE_E2E_DATABASE_URL，实际场景未运行。'},
    'manual_review_scope': {'owned_file_review_records': file_rows, 'early_annotated_status_note': '保留早期19文件annotated原标签，按commenting-standard第6节的已审读定义、各文件具体维护点及源码记录认定；不依赖本次哈希枚举推断新审读。', 'status_definition_evidence': {'path': 'docs/engineering/commenting-standard.md', 'section': '6. 逐文件实施与验收', 'annotated_definition': '已审读并补充/修订必要注释，记录具体维护点。', 'sha256': digest(ROOT / 'docs/engineering/commenting-standard.md')}, 'additional_independent_full_file_review': primary, 'additional_affected_scopes_only': affected, 'additional_scope_full_file_pending_count': 5, 'additional_independent_evidence': 'go-server-reliability-independent-review.json', 'additional_independent_findings': reliability['findings'], 'other_owner_original_issue_states_not_claimed': [{'key': row['key'], 'path': row['path'], 'status_by_this_agent': 'pending / delegated; consult owner ledger'} for row in other_owner], 'unlisted_workspace_files': 'pending by this agent; enumeration or hash checking does not imply human review'},
    'issues': issue_rows,
    'limits': ['本报告逐key采用Sonar full03官方FIXED状态；全仓另外2原问题和最终full04由root负责，本代理139项关闭不等同全仓最终交付完成。', '早期成功执行未存测试时 SHA 的文件明确为 null，当前/账本/full03 一致不追溯证明测试时哈希。', '未完整人工审读的其他源码保持 pending；本代理没有宣称全仓架构/阅卷准确率或真实 PostgreSQL E2E 已验证。'],
}
(BASE / 'go-final-validation-audit.json').write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')

lines = ['# Go 最终验证对账', '', f"生成时间：{report['generated_at']}", '', '只读汇总既有日志与本地源码哈希，本轮没有修改产品源码、运行测试或访问远端 GitHub。', '', f"本代理负责原始 129 项及移交题库 5 项，共 134 原项；另处理 checkpoint02 新增 5 项。139 项均已源码处理并有关联包实际 pass 证据。其他代理的 75 原项不重复计入。", '', f"归属/维护源码 {len(file_rows)} 文件：当前工作区、记录的源码 SHA、full03 清单及实际冻结文件全部相同。保留90个 read_and_refactored、19个 annotated原标签；标准第6节将 annotated定义为已审读并记录维护点，汇总沿用已有人工记录，不依靠本轮哈希枚举升级状态。{report['summary']['current_matches_recorded_latest_test_sha_files']} 文件同时吻合最近成功关联包测试记录的 SHA；{report['summary']['latest_test_sha_not_recorded_files']} 文件的早期成功日志未保存测试时 SHA，报告保留该证据缺口。", '', '题库真实 PostgreSQL 场景：**0 通过、5 条件跳过**。server 最后一次共享包执行 exit 0，188 pass、0 fail、62 skip；其中 **61 因 PostgreSQL 环境、1 为 crash-helper 辅助入口**。包回归通过只证明已运行测试和编译，不能计作跳过场景的业务验证通过。', '', f"Sonar full03 官方分析 `{ce_task['analysisId']}` / CE SUCCESS：134 原项 + 5 checkpoint02 新项逐 key 均为 **FIXED**。采用官方完整状态导出，不以辅助 AST 复杂度估值计关闭；全仓另2原项修复及full04最终扫描由root协调。", '', '## 实际执行证据', '', '同一包重复运行不相加作为独立测试数。早期 exit 1、修复后重跑、原测试 overlay 失败均保留。', '', '|执行报告|实际 exit|Test pass / fail / skip|说明|', '|---|---:|---|---|']
for execution in executions:
    counts = execution['independently_recounted_test_counts']
    display = f"{counts.get('pass', 0)} / {counts.get('fail', 0)} / {counts.get('skip', 0)}" if counts is not None else '未记录逐测试计数'
    note = '原失败保留，关联包见后续通过证据' if execution['actual_exit_code'] != 0 else '实际通过；条件 skip 单列于 JSON'
    if execution['report'] == 'go-taskmaterials-baseline-tests.json':
        note = '原测试 overlay 同样失败，支持严格补齐已有生产 SQL 契约的测试夹具'
    lines.append(f"|{execution['report']}|{execution['actual_exit_code']}|{display}|{note}|")
lines += ['', '## 题库场景边界', '', '|测试|结果|实际跳过原因|', '|---|---|---|']
for item in qb['issue_states']:
    result = item['pg_scenario_result']
    lines.append(f"|{result['test']}|skip|{result['reason']}|")
lines += ['', '## 人工审读范围', '', '归属源文件的逐文件审读状态、维护规则、issue key 与 SHA 详见 JSON `manual_review_scope.owned_file_review_records` / `issues`。哈希核对仅证明源码身份，不替代人工审读；19 个早期 annotated 标签沿用标准定义和实际维护记录。', '', '另独立完整对照 reliability command 测试：基线 1213 行、当前 1633 行，检查多 CommandID、跨阶段状态、lease/Tx/defer、故障注入次序及编译修正；未发现当前语义回归。其两个 PostgreSQL 主测试实际 skip，不计真实数据库通过。', '', '下列 5 文件仅检查具体受影响片段，整文件仍 pending：', '']
for item in affected:
    lines.append(f"- {item['path']}：当前行 {item['current_lines']}；完整审读 pending。")
lines += ['', '其余代理的 75 原项和未列出的全仓文件不由本报告确认完成。详细每包最新实际结果、逐执行跳过日志及原始证据 SHA 均在 JSON。', '']
(BASE / 'go-final-validation-audit.md').write_text('\n'.join(lines), encoding='utf-8')
print(json.dumps({'scope': report['scope'], 'summary': report['summary'], 'latest_packages': len(latest_package_rows), 'reports_written': ['go-final-validation-audit.json', 'go-final-validation-audit.md']}, ensure_ascii=True))
