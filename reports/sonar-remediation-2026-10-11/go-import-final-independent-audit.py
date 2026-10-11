"""只读复核最后两个导入工具测试拆分和其他代理已执行的原始验证证据。"""
import hashlib
import json
import re
from collections import Counter
from datetime import datetime
from pathlib import Path

BASE = Path(__file__).parent
ROOT = BASE.parents[1]
REL = 'services/api-gateway/tools/import-fujian-2024-math-simulation/main_batch9_test.go'
ORIGINAL = Path('D:/tools/sonar-remediation-20261011/baseline') / REL
BEFORE = Path('D:/tools/sonar-remediation-20261011/root-go-final-02-baseline') / REL
CURRENT = ROOT / REL

def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()

def load(name):
    return json.loads((BASE / name).read_text(encoding='utf-8-sig'))

def tokens(source):
    pattern = re.compile(r'(?P<literal>"(?:\\.|[^"\\])*"|\x27(?:\\.|[^\x27\\])*\x27|`[^`]*`)|(?P<comment>//[^\n]*|/\*.*?\*/)|(?P<word>[A-Za-z_][A-Za-z_0-9]*|[0-9]+(?:\.[0-9]+)?)|(?P<space>\s+)|(?P<other>.)', re.S)
    return [(item.lastgroup, item.group()) for item in pattern.finditer(source) if item.lastgroup not in {'comment', 'space'}]

def helper(source, name):
    match = re.search(r'\nfunc ' + name + r'\([^\n]*\{\n.*?\n\}\n', source, re.S)
    assert match
    body = match.group().split('{\n', 1)[1].rsplit('\n}', 1)[0]
    body = '\n'.join(line for line in body.splitlines() if line.strip() != 't.Helper()' and not line.strip().startswith('//'))
    return match.group(), body

before = BEFORE.read_text(encoding='utf-8')
original = ORIGINAL.read_text(encoding='utf-8')
current = CURRENT.read_text(encoding='utf-8')
upload_func, upload_body = helper(current, 'assertBatch9UploadRequest')
dataset_func, dataset_body = helper(current, 'prepareBatch9ImportFailureDataset')
upload_body = re.sub(r'\bsourceFilename\b', 'test.filename', upload_body)
dataset_body = re.sub(r'\bname\b', 'test.name', dataset_body)
dataset_body = '\n'.join(line for line in dataset_body.splitlines() if line.strip() != 'return cfg, data')
reconstructed = current.replace(upload_func, '\n').replace(dataset_func, '\n')
reconstructed = reconstructed.replace('assertBatch9UploadRequest(t, request, test.filename)', upload_body)
reconstructed = reconstructed.replace('cfg, data := prepareBatch9ImportFailureDataset(t, test.name)', dataset_body)
assert tokens(reconstructed) == tokens(before), 'reverse-inline token identity mismatch'
before_literals = Counter(value for kind, value in tokens(before) if kind == 'literal')
after_literals = Counter(value for kind, value in tokens(current) if kind == 'literal')
assert before_literals == after_literals

execution_rows = []
results = {}
for label in ['baseline', 'current']:
    execution_name = f'root-go-final-02-{label}-execution.json'
    result_name = f'root-go-final-02-{label}-result.json'
    execution = load(execution_name)
    result = load(result_name)
    log = BASE / result['event_log']
    events = [json.loads(line) for line in log.read_text(encoding='utf-8-sig').splitlines()]
    terminal = [item for item in events if item.get('Test') and item.get('Action') in {'pass', 'fail', 'skip'}]
    counts = dict(Counter(item['Action'] for item in terminal))
    identities = sorted((item['Package'], item['Test'], item['Action']) for item in terminal)
    # go test -json 会把超长单条输出拆成多块；先按原 Test 拼回完整行，再核对业务证明。
    output_by_test = {}
    for item in events:
        if item.get('Action') == 'output':
            key = (item.get('Package'), item.get('Test'))
            output_by_test.setdefault(key, []).append(item.get('Output', ''))
    proofs = []
    for blocks in output_by_test.values():
        for line in ''.join(blocks).splitlines():
            if 'FUJIAN_IMPORT_BATCH9_PROOF ' in line:
                proofs.append(json.loads(line.split('FUJIAN_IMPORT_BATCH9_PROOF ', 1)[1]))
    assert execution['exit_code'] == result['exit_code'] == 0
    assert counts == result['test_counts'] == {'pass': 58}
    assert execution['source_sha_before'] == execution['source_sha_after'] and execution['changed_sources'] == []
    assert execution['source_sha_after'][REL] == sha(CURRENT)
    results[label] = {'identities': identities, 'proofs': sorted(proofs, key=lambda item: item['case'])}
    execution_rows.append({'execution_report': execution_name, 'result_report': result_name, 'event_log': result['event_log'], 'actual_exit_code': execution['exit_code'], 'started_at': execution['started_at'], 'completed_at': execution['completed_at'], 'counts_recounted_from_raw_jsonl': counts, 'proof_count_recounted_from_raw_jsonl': len(proofs), 'source_identity_count': len(execution['source_sha_before']), 'source_identity_stable': True, 'current_target_sha_matches': True, 'baseline_overlay': execution['baseline_overlay'], 'baseline_overlay_sha256': execution['baseline_overlay_sha256'], 'database_environment_cleared': execution['database_environment_cleared'], 'report_sha256': sha(BASE / execution_name), 'event_log_sha256': sha(log)})
assert results['baseline'] == results['current']
assert len(results['current']['proofs']) == 46

checks = [
    {'topic': '上传协议与首错顺序', 'original_lines': '168–209', 'current_lines': '198–245', 'conclusion': '原 callback 的 calls++ 仍先执行，随后同一 request 与 test.filename 传入 helper；Method/Path/Accept/Authorization 先检查，失败 Fatal 同一子测试，再消费 multipart。filename/content/字段映射断言和响应返回均保持原先后次序，空文件名的0调用断言未变。'},
    {'topic': '故障输入与引用状态', 'original_lines': '623–667', 'current_lines': '665–714', 'conclusion': 'prepare helper 用同一个 *testing.T 创建数据集，按原两独立 if 删除Q1/交换0与16题后返回 cfg/data。map与slice引用保持；sequence、HTTP fixture、SQL script 均在 helper 返回后创建，questions=data.Questions仍使用改序后的数组。'},
    {'topic': 'SQL与事务事实', 'original_lines': '242–367、623–714', 'current_lines': '278–404、665–782', 'conclusion': 'SQL脚本、Query/Exec计数、begin/commit/rollback记录、ocr_task注入错误与24 exec/8 query/末尾rollback期望未变；只读解析的46份完整业务proof含实际 SQL 字符串、参数、HTTP/SQL sequence，与checkpoint03 overlay逐份完全相同。'},
    {'topic': '失败诊断与断言', 'original_lines': '168–209、623–667', 'current_lines': '198–245、665–714', 'conclusion': '新增2 helper内仍用t.Helper，保留所有原Fatal条件/消息及短路。相对checkpoint03所有string/rune/raw literal多重集无差异；将两helper仅用于审查地反向内联后整文件Go token序列完全等于checkpoint03版本，辅助核对调用外部未变。'},
    {'topic': '文件描述符与资源归属', 'original_lines': '211–240、362–367、481–508', 'current_lines': '247–276、398–404、523–549、703–714', 'conclusion': '本次2 helper未新增Open/Close/defer/事务，batch9DatasetFixture的同一子测试TempDir与WriteFile调用不变；openBatch9DB仍由原子测试t.Cleanup关闭，multipart读取同一原request.Body，响应仍原NopCloser。没有将cleanup绑定到新的子测试或提前关闭数据库/HTTP资源。'},
    {'topic': '其他原始修复保留', 'original_lines': '1–783', 'current_lines': '1–856', 'conclusion': '完整阅读原始与当前文件，配置恢复defer、模板题序/几何、注册回滚/绑定、答题候选/客观评分、导入状态顺序及裁剪更新仍保留。正式核验仅聚焦最后2原项，不替root对其余6项作平台关闭结算。'},
    {'topic': '中文维护注释', 'current_lines': '237、706', 'conclusion': '注释明确限定上传先认证后读表单、故障输入先于HTTP/SQL记录准备；与当前原判断和调用顺序一致，无生产事务/真实PostgreSQL保证的夸大表述。'},
]
report = {'generated_at': datetime.now().astimezone().isoformat(), 'reviewer': 'sonar_go_fix independent read-only', 'product_source_edits': False, 'new_test_executions': False, 'file': REL, 'review_state': 'original_and_current_fully_read; checkpoint03 final-two delta independently inspected', 'scope': {'original_lines_read': '1–783', 'current_lines_read': '1–856', 'checkpoint03_source': str(BEFORE), 'checkpoint03_delta_fully_read': True, 'last_two_keys': ['6eb1de9a-c6f8-4662-9a79-01e20787127a', '32ec19c6-7711-4694-b5dd-6577c7ecf0e9'], 'other_go_files_not_read_for_this_review': 'pending; no whole-workspace review claim'}, 'source_identity': {'original_sha256': sha(ORIGINAL), 'checkpoint03_before_final_two_sha256': sha(BEFORE), 'current_sha256': sha(CURRENT), 'current_matches_frontend_successful_test_sha256': True}, 'checks': checks, 'mechanical_support': {'reverse_inline_full_file_token_identity_with_checkpoint03': True, 'checkpoint03_vs_current_string_rune_raw_literal_multisets_identical': True, 'note': '仅支持独立人工顺序/资源/状态审查，不以token或复杂度估值代替平台确认。'}, 'test_evidence': {'executions': execution_rows, 'same_test_identities_and_results': True, 'same_complete_business_proofs': True, 'business_proof_count': 46, 'current_counts': {'pass': 58, 'fail': 0, 'skip': 0}, 'baseline_overlay_boundary': '覆盖的是最后两项提取前的checkpoint03文件，不是最初783行文件；其余工作区依赖为当前版本。', 'integration_boundary': '本工具包使用HTTP Transport/SQL driver测试夹具；58 pass不证明真实PostgreSQL或远端服务集成，未对外请求。'}, 'findings': [], 'sonar_state': 'last two official states pending root full04 rescan; no local platform claim'}
(BASE / 'go-import-final-independent-review.json').write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
lines = ['# 最后两项 Go 导入测试的独立审查', '', f"生成时间：{report['generated_at']}", '', '已完整审读原始 main_batch9_test.go（783行）与当前版本（856行），并逐段独立核查checkpoint03到最终版仅2个helper提取。没有修改源码或另跑测试，当前未发现语义回归。', '', f"当前 SHA256：`{sha(CURRENT)}`；与两次成功执行所冻结的目标源一致。", '', '|检查|结论|', '|---|---|']
lines += [f"|{item['topic']}|{item['conclusion']}|" for item in checks]
lines += ['', '只读重算其他代理原始JSONL：checkpoint03 overlay与当前版各exit0、58 pass/0fail/0skip；逐Test身份和终态一致，46份完整业务proof逐份相同。每次1034份源码身份清单前后未变。', '', 'overlay基线覆盖的是最后两项拆分前的checkpoint03文件。HTTP/SQL为测试夹具；未将这些结果称为真实PostgreSQL或远端服务验证。最后2原项官方关闭等待root full04。其他Go文件未在本审查中完整重读，保持pending。', '']
(BASE / 'go-import-final-independent-review.md').write_text('\n'.join(lines), encoding='utf-8')
print(json.dumps({'findings': report['findings'], 'mechanical_support': report['mechanical_support'], 'source_identity': report['source_identity'], 'executions': execution_rows, 'proof_count': 46}, ensure_ascii=True))
