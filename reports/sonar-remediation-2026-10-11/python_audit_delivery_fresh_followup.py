"""不重测：核对fresh45、server04、model真实生产导入和全部TRACE，保留preflight。"""
from collections import Counter, defaultdict
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
import re
from xml.etree import ElementTree

ROOT = Path(__file__).resolve().parents[2]
REPORTS = Path(__file__).parent

def read(path):
    return json.loads(path.read_text(encoding='utf-8-sig'))

def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()

def current_mapping(mapping):
    return [{'file': file, 'recorded_sha256': recorded, 'current_sha256': sha(ROOT / file)}
            for file, recorded in mapping.items() if sha(ROOT / file) != recorded]

fresh = read(REPORTS / 'fresh-python-dynamic-01/execution.json')
assert fresh['passed'] and fresh['sources_unchanged_during_tests']
assert not current_mapping(fresh['source_hashes_after'])
python_counts = Counter()
for result in fresh['results']:
    assert result['exit_code'] == 0
    assert sha(REPORTS / result['log']) == result['log_sha256']
    cases = list(ElementTree.parse(REPORTS / result['junit']).getroot().iter('testcase'))
    observed = Counter('failed' if any(case.find(tag) is not None for tag in ['failure', 'error'])
                       else 'skipped' if case.find('skipped') is not None else 'passed' for case in cases)
    assert all(observed[name] == result['counts'][name] for name in ['passed', 'failed', 'skipped'])
    python_counts.update(observed)

server = read(REPORTS / 'go-server-batch04-tests.json')
server_source = read(REPORTS / 'go-server-batch04-source-sha.json')
server_sources = server_source['files']
assert server['exit_code'] == 0
assert not current_mapping(server_sources)
outputs = defaultdict(list)
server_counts = Counter()
server_skips = []
for line in (REPORTS / 'go-server-batch04-events.jsonl').read_text(encoding='utf-8').splitlines():
    try:
        event = json.loads(line)
    except json.JSONDecodeError:
        continue
    test = event.get('Test')
    if test and event['Action'] == 'output':
        outputs[test].append(event.get('Output', ''))
    if test and event['Action'] in ['pass', 'fail', 'skip']:
        server_counts[event['Action']] += 1
    if test and event['Action'] == 'skip':
        messages = ''.join(outputs[test])
        reasons = [line.strip() for line in messages.splitlines() if not line.startswith(('===', '---'))]
        category = ('postgres_environment_missing' if any(word in messages.lower() for word in ['postgres', 'test_database', 'database_url', 'database dsn'])
                    else 'intentional_helper_entrypoint' if 'child-process crash helper' in messages else 'other_conditional_skip')
        server_skips.append({'test': test, 'category': category, 'reasons': reasons})
assert all(server_counts[action] == server['test_result_counts'].get(action, 0) for action in ['pass', 'fail', 'skip'])

model = read(REPORTS / 'go-modelgovernance-test-execution.json')
model_runs = []
for result in model['results']:
    traces = defaultdict(list)
    passed_ids, skipped_ids = [], []
    for line in (REPORTS / result['output']).read_text(encoding='utf-8').splitlines():
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        test = event.get('Test')
        if test and event['Action'] == 'pass':
            passed_ids.append(test)
        if test and event['Action'] == 'skip':
            skipped_ids.append(test)
        match = re.search(r'TRACE (.+)', event.get('Output', '').strip())
        if test and match:
            traces[test].append(match[1])
    assert sorted(passed_ids) == result['passed_test_ids']
    assert sorted(skipped_ids) == result['skipped_test_ids']
    model_runs.append({'name': result['name'], 'passed_ids': sorted(passed_ids),
                       'skipped_ids': sorted(skipped_ids), 'complete_trace_by_test': dict(traces),
                       'trace_line_count': sum(len(values) for values in traces.values()),
                       'raw_jsonl_sha256': sha(REPORTS / result['output'])})
assert model_runs[0]['passed_ids'] == model_runs[1]['passed_ids']
assert model_runs[0]['skipped_ids'] == model_runs[1]['skipped_ids']
assert model_runs[0]['complete_trace_by_test'] == model_runs[1]['complete_trace_by_test']
preflight = read(REPORTS / 'delivery-independent-preflight-audit-python-agent.json')
scope = preflight['model_actual_import_scope']
assert not scope['actual_compilation_scope_sha_changes']
report = {
    'audited_at_utc': datetime.now(timezone.utc).isoformat(),
    'scope': '只读最新现有测试产物与当前 SHA；未重测、未修改任何代理账本，保留第一次preflight。',
    'python_fresh': {'report': 'fresh-python-dynamic-01/execution.json',
                     'execution_sha256': sha(REPORTS / 'fresh-python-dynamic-01/execution.json'),
                     'counts': dict(python_counts), 'exit_codes': [result['exit_code'] for result in fresh['results']],
                     'current_source_sha_valid': True, 'bound_source_files': len(fresh['source_hashes_after']),
                     'resolved_preflight_code': 'PYTHON_CURRENT_TEST_SHA_NOT_DYNAMically_BOUND',
                     'overlap_note': '45 是旧1006中指定3文件的补证，不能相加宣称新增45个独立用例。'},
    'server_current': {'report': 'go-server-batch04-tests.json', 'exit_code': 0,
                       'actual_counts': dict(server_counts), 'current_source_sha_valid': True,
                       'bound_source_files': len(server_sources),
                       'skip_categories': dict(Counter(item['category'] for item in server_skips)),
                       'skip_records': server_skips,
                       'limitations': '61项真实PostgreSQL环境条件跳过，1项辅助子进程入口跳过；均不转换为通过。source manifest仅server目录，后续最终扫描/Go6包记录补充其它生产源证据。'},
    'model_existing_evidence': {'report': 'go-modelgovernance-test-execution.json',
                                'true_static_compilation_scope_files': scope['scope_files'],
                                'production_dependency_sha_changes': [],
                                'no_current_rerun_required_confirmed_by_root': True,
                                'manifest_limitation': '旧1002清单过度展开依赖包自身*_test.go；实际根包测试+依赖生产源码117文件均保持，mathunderstanding不被真实导入。',
                                'same_pass_and_skip_ids': True, 'same_complete_trace_sequences': True,
                                'runs': model_runs},
    'previous_preflight': 'delivery-independent-preflight-audit-python-agent.json',
    'platform_result': 'pending_final_full_checkpoint03_scan_and_complete_export',
}
(REPORTS / 'delivery-independent-fresh-followup-audit-python-agent.json').write_text(
    json.dumps(report, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
text = '\n'.join([
    '# 最小补验后的独立交付审计', '',
    '3 Python测试文件fresh执行43+2=45 pass/0 fail/0 skip，两命令实际exit0；JUnit/日志/当前27源文件SHA核对通过，旧1006记录保持原样。它们是补证，不与旧计数相加。', '',
    'server batch04：188 pass、61 PostgreSQL条件skip、1辅助子进程入口skip，实际exit0，当前server源清单SHA一致。原失败与原版通过记录保留。', '',
    f"model原baseline/current各366 pass/0skip的所有测试身份及完整TRACE序列相同（各{model_runs[0]['trace_line_count']}行，不仅比对每测试最后一条）。真实根包测试+依赖生产源码117文件没有变更，mathunderstanding不在真实导入范围，root已确认无需重复重验。旧1002清单的扩大范围局限单独保留。", '',
    '原2223/new47完整且无缺漏、无重复的preflight结论保留；前端E2E仍是中断/10失败，类型和早期弱日志证明的限制保留。最后平台关闭数、误报/接受数和质量门禁待第三轮完整复扫，不从源码处理状态推断。', '',
])
(REPORTS / 'delivery-independent-fresh-followup-audit-python-agent.md').write_text(text, encoding='utf-8')
print(json.dumps({'python': dict(python_counts), 'server': dict(server_counts),
                  'skip_categories': report['server_current']['skip_categories'],
                  'model_same_complete_trace': True, 'model_trace_lines_each': model_runs[0]['trace_line_count']}, ensure_ascii=False))
