"""独立交付审计：只读账本/源字节/已有测试产物，不修改其它智能体报告。"""
from collections import Counter, defaultdict, deque
from datetime import datetime, timezone
import ast
import hashlib
import json
from pathlib import Path
import re
from xml.etree import ElementTree

ROOT = Path(__file__).resolve().parents[2]
REPORTS = Path(__file__).parent
MODULE = ROOT / 'services/api-gateway'
captured_reports = {}

def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest() if path.is_file() else None

def read(path):
    data = path.read_bytes()
    captured_reports[str(path)] = hashlib.sha256(data).hexdigest()
    return json.loads(data.decode('utf-8-sig'))

def entries(document):
    return document.get('issues', document.get('issue_states', []))

def issue_key(entry):
    return entry.get('key', entry.get('issue_key'))

original_export = read(ROOT / 'reports/platform-audit-2026-10-10/sonar/issues.json')['issues']
original_by_key = {entry['key']: entry for entry in original_export}
ledger = read(REPORTS / 'issues-ledger.json')
ledger_by_key = {entry['issue_key']: entry for entry in ledger['issues']}
checkpoint = read(REPORTS / 'full-checkpoint-02/issues.json')['issues']
new_by_key = {entry['key']: entry for entry in checkpoint if entry['key'] not in original_by_key}
findings = []

def finding(code, message, details=None, priority='evidence'):
    item = {'code': code, 'priority': priority, 'message': message}
    if details is not None:
        item['details'] = details
    findings.append(item)

original_integrity = {
    'export_count': len(original_export), 'export_unique_keys': len(original_by_key),
    'ledger_count': len(ledger['issues']), 'ledger_unique_keys': len(ledger_by_key),
    'missing_keys': sorted(set(original_by_key) - set(ledger_by_key)),
    'unexpected_keys': sorted(set(ledger_by_key) - set(original_by_key)),
    'duplicates': [key for key, count in Counter(entry['issue_key'] for entry in ledger['issues']).items() if count > 1],
    'identity_mismatches': [],
}
for key in set(original_by_key) & set(ledger_by_key):
    initial, current = original_by_key[key], ledger_by_key[key]
    path = initial['component'].split(':', 1)[1]
    for field, expected in [('rule', initial['rule']), ('path', path), ('severity', initial['severity'])]:
        if current[field] != expected:
            original_integrity['identity_mismatches'].append({'key': key, 'field': field, 'expected': expected, 'actual': current[field]})

progress_names = [
    'root-progress.json', 'root-go-progress.json', 'frontend-progress.json',
    'python-progress.json', 'go-progress.json', 'go-modelgovernance-progress.json',
    'go-server-progress.json', 'go-server-question-bank-progress.json',
]
progress = {name: read(REPORTS / name) for name in progress_names}
owners = Counter(entry['owner_report'] for entry in ledger['issues'])
owner_index = {name: {issue_key(entry): entry for entry in entries(document)} for name, document in progress.items()}
ownership_gaps, status_drift = [], []
for key, item in ledger_by_key.items():
    source = owner_index.get(item['owner_report'], {}).get(key)
    if source is None:
        ownership_gaps.append({'key': key, 'owner_report': item['owner_report']})
        continue
    source_status = source.get('status', source.get('state'))
    if item['status'] != source_status:
        status_drift.append({'key': key, 'owner_report': item['owner_report'],
                             'aggregate_status': item['status'], 'owner_status': source_status})

handoff_overlaps = []
references = defaultdict(list)
for name, index in owner_index.items():
    for key in index:
        references[key].append(name)
for key, names in references.items():
    if len(names) > 1:
        handoff_overlaps.append({'key': key, 'reports': sorted(names),
                                 'authoritative_owner': ledger_by_key.get(key, {}).get('owner_report')})
partition_keys = [key for name in ['root-progress.json', 'frontend-progress.json', 'python-progress.json', 'go-progress.json'] for key in owner_index[name]]
ownership = {
    'owner_counts': dict(owners),
    'authoritative_owner_missing_keys': ownership_gaps,
    'aggregate_owner_state_drift': status_drift,
    'base_partition_unique_keys': len(set(partition_keys)),
    'base_partition_entries': len(partition_keys),
    'handoff_reference_overlaps': handoff_overlaps,
    'overlap_interpretation': 'Go 总参考账本仍含交接 key；root-go/model/server/question-bank 子报告属于追踪引用重叠。原总账每 key 仅有一个 owner，不重复计修复数。',
}
if status_drift:
    finding('AGGREGATE_STATUS_LAGS_OWNER', '汇总账本与当前负责报告的源码状态存在漂移；最终汇总前重新聚合，不能混用旧统计。', status_drift)

new_names = ['root-new-findings-progress.json', 'python-new-findings-progress.json', 'go-new-findings-progress.json']
new_progress = {name: read(REPORTS / name) for name in new_names}
new_references = defaultdict(list)
for name, document in new_progress.items():
    for entry in entries(document):
        new_references[issue_key(entry)].append(name)
new_integrity = {
    'checkpoint_new_count': len(new_by_key),
    'tracked_count': sum(len(entries(document)) for document in new_progress.values()),
    'tracked_unique_keys': len(new_references),
    'per_report': {name: len(entries(document)) for name, document in new_progress.items()},
    'missing_keys': sorted(set(new_by_key) - set(new_references)),
    'unexpected_keys': sorted(set(new_references) - set(new_by_key)),
    'duplicate_keys': [{'key': key, 'reports': reports} for key, reports in new_references.items() if len(reports) > 1],
    'identity_mismatches': [],
}
for name, document in new_progress.items():
    for entry in entries(document):
        key = issue_key(entry)
        if key not in new_by_key:
            continue
        expected = new_by_key[key]
        actual_path = entry.get('file', entry.get('path'))
        for field, actual, value in [('rule', entry['rule'], expected['rule']),
                                      ('path', actual_path, expected['component'].split(':', 1)[1]),
                                      ('severity', entry['severity'], expected['severity'])]:
            if actual != value:
                new_integrity['identity_mismatches'].append({'key': key, 'report': name, 'field': field, 'expected': value, 'actual': actual})

file_statuses, source_sha_checks = [], []
all_progress = {**progress, **new_progress}
for name, document in all_progress.items():
    files = document.get('files', [])
    items = list(files.items()) if isinstance(files, dict) else [(entry.get('path', entry.get('file')), entry) for entry in files]
    for path, item in items:
        review = {key: value for key, value in item.items() if 'review' in key or key in ['pending_scope', 'actual_review_scope']}
        file_statuses.append({'report': name, 'file': path, 'review': review})
        current = digest(ROOT / path)
        recorded = (item.get('current_sha256') or item.get('source_sha256_after') or item.get('source_sha256')
                    or item.get('current_source_sha256') or item.get('sha256'))
        source_sha_checks.append({'report': name, 'file': path, 'current_sha256': current,
                                 'recorded_latest_sha256': recorded, 'matched': current == recorded if recorded else None})
        full = item.get('full_file_review_claim') is True or item.get('full_file_review_status') in ['reviewed', 'complete']
        pending = bool(item.get('pending_scope')) or item.get('full_file_review_status') == 'pending'
        if full and pending:
            finding('FULL_REVIEW_CONTRADICTS_PENDING_SCOPE', '同一文件同时声称完整审读和存在待审范围。', {'report': name, 'file': path, 'review': review}, 'blocking_claim')
    for entry in entries(document):
        path = entry.get('path', entry.get('file'))
        recorded = entry.get('current_source_sha256', entry.get('current_sha256'))
        if recorded and path:
            source_sha_checks.append({'report': name, 'issue_key': issue_key(entry), 'file': path,
                                     'current_sha256': digest(ROOT / path), 'recorded_latest_sha256': recorded,
                                     'matched': digest(ROOT / path) == recorded})
sha_mismatches = [item for item in source_sha_checks if item['matched'] is False]
if sha_mismatches:
    finding('PROGRESS_SOURCE_SHA_DRIFT', '负责报告记录的源码版本与当前字节有差异；历史记录保持原样，最终交付需新证据绑定当前版本。', sha_mismatches)

def map_check(label, mapping, prefix=ROOT):
    result = []
    for relative, expected in mapping.items():
        if not isinstance(expected, str) or not re.fullmatch('[0-9a-f]{64}', expected):
            continue
        path = prefix / relative
        current = digest(path)
        if expected != current:
            result.append({'file': str(path.relative_to(ROOT)).replace('\\', '/'), 'tested_sha256': expected,
                           'current_sha256': current})
    return {'report': label, 'recorded_files': len(mapping), 'mismatches': result}

validation_sha = []
for name, field in [
    ('python-test-execution.json', 'source_hashes_after'),
    ('python-new-test-execution.json', 'source_hashes_after'),
    ('go-modelgovernance-test-execution.json', 'source_hashes_after'),
    ('frontend-web-retry-01-execution.json', 'source_sha_after'),
    ('python-tools-equivalence.json', 'current_source_hashes'),
    ('python-tools-equivalence-xml-security.json', 'current_source_hashes'),
    ('python-new-tools-equivalence.json', 'current_source_hashes'),
    ('root-new-js-tests-02.json', 'source_sha256'),
]:
    document = read(REPORTS / name)
    validation_sha.append(map_check(name, document[field]))
server_source = read(REPORTS / 'go-server-batch01-source-sha.json')
validation_sha.append(map_check('go-server-batch01-tests.json', server_source['files']))
go_six = read(REPORTS / 'go-batch22-26-and-new-five-source-after.json')
validation_sha.append(map_check('go-batch22-26-and-new-five-tests.json', go_six, MODULE))

def imports(path):
    text = path.read_text(encoding='utf-8-sig')
    declarations = '\n'.join(re.findall(r'^import\s*\(([\s\S]*?)^\)', text, re.M))
    declarations += '\n' + '\n'.join(re.findall(r'^import\s+(?:(?:\w+|\.)\s+)?("[^"\n]+")', text, re.M))
    return re.findall(r'"([^"\n]+)"', declarations)

def correct_go_scope(root_package):
    prefix = 'edugrade-enterprise/services/api-gateway/'
    queue = deque([(root_package, [root_package])])
    seen, sources, chains = set(), {}, {}
    while queue:
        package, chain = queue.popleft()
        if package in seen:
            continue
        seen.add(package)
        directory = MODULE / package
        for path in sorted(directory.glob('*.go')):
            # go test 只编译目标包的测试；依赖包按其生产代码导入，不能展开它们自己的测试引用。
            if package != root_package and path.name.endswith('_test.go'):
                continue
            # 当前为 Windows；其它 OS 文件不作为实际输入。标签组合未完全解析，余下范围是保守上界。
            if re.search(r'_(?:linux|darwin|freebsd|openbsd|netbsd|solaris|plan9|aix|android|ios)(?:_test)?\.go$', path.name):
                continue
            relative = path.relative_to(ROOT).as_posix()
            sources[relative] = digest(path)
            chains[relative] = chain
            for name in imports(path):
                if name.startswith(prefix):
                    dep = name[len(prefix):]
                    if dep not in seen:
                        queue.append((dep, chain + [dep]))
    return sources, chains

model_scope, model_chains = correct_go_scope('internal/modelgovernance')
model_execution = read(REPORTS / 'go-modelgovernance-test-execution.json')
model_actual_changes = []
for relative, current in model_scope.items():
    recorded = model_execution['source_hashes_after'].get(relative)
    if recorded != current:
        model_actual_changes.append({'file': relative, 'tested_sha256': recorded, 'current_sha256': current,
                                     'import_chain': model_chains[relative]})
model_graph = {'scope_files': len(model_scope), 'analysis': '仅根包含测试；依赖仅生产源码；读取静态 Go import，Windows 后缀过滤，未运行 go list 或编译。未完全求值 build tags，范围为保守上界。',
               'actual_compilation_scope_sha_changes': model_actual_changes,
               'ignored_manifest_only_changes': [item for item in next(item for item in validation_sha if item['report'] == 'go-modelgovernance-test-execution.json')['mismatches'] if item['file'] not in model_scope]}
if model_actual_changes:
    finding('MODEL_CURRENT_PRODUCTION_DEPENDENCY_CHANGED', 'model 基线/当前 366 证明仍有效于当时版本，但实际生产依赖已变；建议只补 current modelgovernance 包一次以绑定最终依赖，不重复所有包或原 overlay。', model_actual_changes, 'revalidation_needed')

test_counts = []
for name in ['python-test-execution.json', 'python-new-test-execution.json']:
    document = read(REPORTS / name)
    for result in document['results']:
        junit = ElementTree.parse(REPORTS / result['junit']).getroot()
        cases = list(junit.iter('testcase'))
        counts = Counter('failed' if case.find('failure') is not None or case.find('error') is not None
                         else 'skipped' if case.find('skipped') is not None else 'passed' for case in cases)
        test_counts.append({'report': name, 'group': result['group'], 'exit_code': result['exit_code'],
                            'junit_cases': len(cases), 'actual_counts': dict(counts), 'artifact': result['junit'],
                            'artifact_sha256': digest(REPORTS / result['junit']),
                            'note': 'JUnit case 含 subtests；不把它们重复加到 pytest 主测试数。'})

for path in sorted(REPORTS.glob('go-*tests.json')):
    document = read(path)
    if not isinstance(document, dict) or 'test_result_counts' not in document:
        continue
    counts = document['test_result_counts']
    skip_records = document.get('skipped_tests')
    if skip_records is None and 'all_test_results' in document:
        skip_records = [entry for entry in document['all_test_results'] if entry.get('Action') == 'skip' and entry.get('Test')]
    test_counts.append({'report': path.name, 'exit_code': document.get('exit_code'),
                        'recorded_counts': counts, 'skip_record_count': len(skip_records) if skip_records is not None else None,
                        'skip_records_preserved': skip_records is not None,
                        'note': document.get('note')})
    if skip_records is not None and counts.get('skip', 0) != len(skip_records):
        finding('SKIP_COUNT_MISMATCH', 'Go 记录的 skip 数与逐项 skip 记录不一致。', {'report': path.name, 'recorded': counts.get('skip', 0), 'actual': len(skip_records)}, 'blocking_claim')

for result in model_execution['results']:
    events = []
    for line in (REPORTS / result['output']).read_text(encoding='utf-8').splitlines():
        try:
            events.append(json.loads(line))
        except json.JSONDecodeError:
            pass
    counts = Counter(event['Action'] for event in events if event.get('Test') and event['Action'] in ['pass', 'fail', 'skip'])
    test_counts.append({'report': 'go-modelgovernance-test-execution.json', 'group': result['name'],
                        'exit_code': result['exit_code'], 'actual_jsonl_counts': dict(counts),
                        'recorded_pass_ids': len(result['passed_test_ids']), 'recorded_skip_ids': len(result['skipped_test_ids'])})
    if counts['pass'] != len(result['passed_test_ids']) or counts['skip'] != len(result['skipped_test_ids']):
        finding('MODEL_RESULT_COUNT_MISMATCH', 'model 实际 JSONL 事件数与报告 ID 数不一致。', result['name'], 'blocking_claim')

web = read(REPORTS / 'frontend-web-retry-01-execution.json')
web_log = (REPORTS / 'frontend-web-retry-01.log').read_text(encoding='utf-8')
test_counts.append({'report': 'frontend-web-retry-01-execution.json', 'exit_code': web['exit_code'],
                    'test_files_passed': 38, 'tests_passed': 416, 'final_counts_found_in_log': bool(re.search(r'Test Files\s+38 passed', web_log) and re.search(r'Tests\s+416 passed', web_log)),
                    'current_source_sha_valid': not next(item for item in validation_sha if item['report'] == 'frontend-web-retry-01-execution.json')['mismatches']})
frontend = progress['frontend-progress.json']
weak_validation = [entry for entry in frontend['validations'] if '未保存原始日志' in str(entry.get('evidence'))]
if weak_validation:
    finding('FRONTEND_EARLY_RESULTS_NOT_REPLAYABLE_ARTIFACTS', '部分早期通过记录只有会话输出引用，无原日志/SHA；后续 web 38/416 已有当前绑定，不应将早期记录累加成新增通过数。其它应用类型检查仍有已明确保留的资源失败待补记录。', weak_validation)

specific_python = {
    'resolved_by_later_current_evidence': [
        {'files': ['docs/architecture/tools/build_database_specs.py', 'docs/architecture/tools/build_diagrams.py', 'docs/architecture/tools/verify_delivery.py'], 'evidence': 'python-tools-equivalence-xml-security.json + python-xml-security-validation.json'},
        {'files': ['docs/architecture/tools/write_frontend_specs.py'], 'evidence': 'python-new-tools-equivalence.json original_190_issue_baseline'},
        {'files': ['ai-services/grading_agent/paper_compact/anchors.py'], 'evidence': 'reports/codeql-remediation-2026-10-11/python-remediation.json + summary.json；93 passed/2 subtests exit0，当前 after SHA一致；原 Sonar 动态总记录并未涵盖新页脚修复。'},
    ],
    'current_test_source_without_later_dynamic_sha_binding': [item for item in next(item for item in validation_sha if item['report'] == 'python-test-execution.json')['mismatches'] if item['file'] in [
        'services/ocr-worker/tests/test_api_batch51.py', 'services/ocr-worker/tests/test_symbolic_runner.py',
        'services/subjective-grading-worker/tests/test_api_integration.py']],
    'recommendation': '核对后续 AST 差异/测试命令时序；如无当前绑定记录，只补上述 3 个已有测试文件，不重跑 1006 全套。',
}
if specific_python['current_test_source_without_later_dynamic_sha_binding']:
    finding('PYTHON_CURRENT_TEST_SHA_NOT_DYNAMically_BOUND', '原总测试报告仍有 3 个测试源 SHA 漂移；静态语法/语义等价审读可保留，当前实际执行证明不能从旧 SHA直接转移。', specific_python, 'revalidation_needed')

new_root = new_progress['root-new-findings-progress.json']
preview = next(entry for entry in entries(new_root) if entry.get('file') == 'docs/architecture/tools/build_preview.py')
if '已由Python分工更新XML解析' in preview.get('reason', ''):
    finding('PREVIEW_CHANGE_PROVENANCE_INACCURATE', 'root 新告警说明把 build_preview XML 修改归于本 Python 分工，但本智能体未修改该 root 独占文件，之前双方已确认外部未知变更需保留。该归因不能替代 SHA/真实验证证据。', {'key': preview['key'], 'report': 'root-new-findings-progress.json'})

input_stability = [{
    'path': path, 'audit_input_sha256': recorded, 'current_report_sha256': digest(Path(path)),
    'unchanged_during_audit': digest(Path(path)) == recorded,
} for path, recorded in captured_reports.items()]
audit = {
    'audited_at_utc': datetime.now(timezone.utc).isoformat(),
    'scope': '原 2223 + checkpoint02 新增47 的本地问题身份/归属、当前源SHA、既有动态产物与跳过统计独立审计；未重新测试、未改代理账本、未触发平台状态动作。',
    'platform_result': 'pending_final_full_checkpoint03_scan_and_complete_export',
    'original_integrity': original_integrity,
    'ownership': ownership,
    'new_integrity': new_integrity,
    'file_review_statuses': file_statuses,
    'file_review_scope_note': '此处逐文件复制真实维护状态并检查字段矛盾，不声明审计者重新全文审读这些文件。partial/pending/protected 保留，生成物按权威源验证。',
    'source_sha_checks': source_sha_checks,
    'source_sha_mismatches': sha_mismatches,
    'validation_sha_checks': validation_sha,
    'model_actual_import_scope': model_graph,
    'python_later_validation_reconciliation': specific_python,
    'test_count_checks': test_counts,
    'findings': findings,
    'input_report_stability': input_stability,
    'skip_interpretation': '已保存 Go pass 与 skip 分栏，未发现条件 skip 被转换为 pass；package exit0 仅是该命令完成，不证明被条件跳过的真实DB/模型场景。',
    'front_end_e2e': 'frontend-e2e-independent-review-python-agent.json：仍中断/10失败，不是47通过。',
}
(REPORTS / 'delivery-independent-preflight-audit-python-agent.json').write_text(
    json.dumps(audit, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
lines = ['# 最终复扫前：独立交付证据审计', '',
         f"原导出/总账：{len(original_export)}/{len(ledger['issues'])}，原 key 缺失 {len(original_integrity['missing_keys'])}、重复 {len(original_integrity['duplicates'])}。新增 checkpoint02 {len(new_by_key)} 项，root/Python/Go 各 {new_integrity['per_report']}；缺失 {len(new_integrity['missing_keys'])}、重复 {len(new_integrity['duplicate_keys'])}。", '',
         '最终平台状态仍待 checkpoint03 完整扫描及导出，不能从源码状态推断告警归零。Go 交接引用重叠按原总账单一 owner 计算，不重复累计。所有逐文件 partial/pending/protected 状态原样保留，本审计不把哈希或枚举当全文审读。', '',
         '当前版本证据注意点：', '']
lines += [f"- **{entry['code']}**：{entry['message']}" for entry in findings]
lines += ['', f"model 实际静态导入范围（仅根包测试+依赖生产源码）{len(model_scope)} 文件；真实编译输入 SHA 漂移 {len(model_actual_changes)} 文件，其余 manifest 扩大的依赖测试变更不要求重验 model。详见 JSON 的 import_chain。", '',
          '最小复验：若 model 生产依赖确有变更，当前包一次；Python 若无后续 current 绑定，3 个指定测试文件；server 首次动态源存在后续修改，正在进行的第二批验证应绑定当前版本；前端类型/静态门禁与 E2E 的原资源失败不计通过。无需重复全部已通过测试。', '',
          '原 Go 条件 skip 独立保存；没有发现把 skip 加到 pass 的记录。嵌套测试计数需按实际测试/子测试陈述，不宣称相同数量独立业务场景或真实数据库/模型全部验收。', '',
          '完整 key、当前 SHA、旧证据差异、逐文件待审范围、JUnit/JSONL 数量与所有输入报告 SHA 在同名 JSON；动态过程中有其它智能体写报告的稳定性也逐项记录。', '']
(REPORTS / 'delivery-independent-preflight-audit-python-agent.md').write_text('\n'.join(lines), encoding='utf-8')
print(json.dumps({'original': original_integrity,
                  'new': new_integrity,
                  'owner_gaps': len(ownership_gaps), 'aggregate_state_drift': len(status_drift),
                  'progress_sha_drift': len(sha_mismatches),
                  'model_actual_scope_files': len(model_scope), 'model_actual_changes': model_actual_changes,
                  'findings': [{key: entry[key] for key in ['code', 'priority', 'message']} for entry in findings]}, ensure_ascii=False))
