"""用Go overlay运行修复前测试，再核对修复版的全部结构化业务证据。"""
from collections import Counter
from pathlib import Path
import datetime as dt
import hashlib
import json
import os
import subprocess

ROOT = Path(__file__).resolve().parents[2]
OUT = Path(__file__).resolve().parent
RELATIVE = 'services/api-gateway/tools/import-fujian-2024-math-simulation/main_batch9_test.go'
ORIGINAL = Path('D:/tools/sonar-remediation-20261011/baseline') / RELATIVE
PREFIX = 'root-go-02-'


def run(label, overlay):
    env = dict(os.environ)
    env.update({'GOTOOLCHAIN': 'local', 'GOPROXY': 'off', 'GOSUMDB': 'off', 'GOMAXPROCS': '1'})
    command = ['go', 'test', '-json', '-p', '1', '-count=1', '-timeout=5m']
    if overlay:
        command.append('-overlay=' + str(overlay))
    command.append('./tools/import-fujian-2024-math-simulation')
    started = dt.datetime.now().astimezone().isoformat()
    log_path = OUT / (PREFIX + label + '-events.jsonl')
    if log_path.exists():
        raise RuntimeError('Keep previous execution evidence; use a fresh attempt prefix')
    with log_path.open('w', encoding='utf-8') as log:
        code = subprocess.run(command, cwd=ROOT / 'services/api-gateway', env=env, stdout=log, stderr=subprocess.STDOUT).returncode
    (OUT / (PREFIX + label + '-execution.json')).write_text(json.dumps({'command': command, 'started_at': started, 'exit_code': code}), encoding='utf-8')
    events = []
    for line in log_path.read_text(encoding='utf-8').splitlines():
        try:
            events.append(json.loads(line))
        except json.JSONDecodeError:
            continue
    counts = Counter(e['Action'] for e in events if 'Test' in e and e['Action'] in {'pass', 'fail', 'skip'})
    proofs = []
    buffers = {}
    for event in events:
        output = event.get('Output', '')
        key = (event.get('Package'), event.get('Test'))
        # go test -json会拆分较长的Output行；完整行重组后才能解析业务JSON证据。
        buffers[key] = buffers.get(key, '') + output
        while '\n' in buffers[key]:
            line, _, buffers[key] = buffers[key].partition('\n')
            if 'FUJIAN_IMPORT_BATCH9_PROOF ' in line:
                proofs.append(json.loads(line.split('FUJIAN_IMPORT_BATCH9_PROOF ', 1)[1]))
    result = {'label': label, 'started_at': started, 'completed_at': dt.datetime.now().astimezone().isoformat(), 'command': command, 'exit_code': code, 'test_counts': dict(counts), 'business_proofs': proofs}
    (OUT / (PREFIX + label + '-result.json')).write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding='utf-8')
    if code != 0 or counts['fail']:
        raise RuntimeError('Go package validation failed: ' + label)
    return result


def main():
    # overlay只影响本次Go进程，不恢复或覆盖工作区文件。
    overlay = OUT / 'root-go-baseline-overlay.json'
    overlay.write_text(json.dumps({'Replace': {str(ROOT / RELATIVE): str(ORIGINAL)}}), encoding='utf-8')
    before = run('baseline', overlay)
    after = run('current', None)
    assert before['test_counts'] == after['test_counts']
    assert before['business_proofs'] == after['business_proofs']
    assert len(before['business_proofs']) > 0
    result = {'same_test_counts': True, 'same_complete_business_proofs': True, 'proof_count': len(after['business_proofs']), 'test_counts': after['test_counts'], 'baseline_overlay_only': True, 'workspace_sha256': hashlib.sha256((ROOT / RELATIVE).read_bytes()).hexdigest(), 'evidence': [PREFIX + 'baseline-result.json', PREFIX + 'current-result.json'], 'sonar_confirmation': 'pending'}
    (OUT / 'root-go-equivalence.json').write_text(json.dumps(result, ensure_ascii=False, indent=2), encoding='utf-8')
    issues = json.loads((ROOT / 'reports/platform-audit-2026-10-10/sonar/issues.json').read_text(encoding='utf-8'))['issues']
    states = [{
        'key': issue['key'], 'rule': issue['rule'], 'severity': issue['severity'], 'path': RELATIVE,
        'original_line': issue['line'], 'state': 'changed_awaiting_sonar',
        'change': '按连接/身份/数据模式、页面/区域几何、注册事务、导入名册、评分路由、裁剪跳过及更新等业务阶段拆分；保留全部原断言与中止顺序。',
        'evidence': ['root-go-equivalence.json'],
    } for issue in issues if issue['component'].split(':', 1)[1] == RELATIVE]
    assert len(states) == 6
    (OUT / 'root-go-progress.json').write_text(json.dumps({'original_count': 6, 'issue_states': states, 'files': {RELATIVE: {'file_review_status': 'annotated', 'review_scope': '整文件、相关签名适配和生产配置入口已审读；分阶段保留事务结果和断言，新增提交失败事实说明。'}}, 'validation': result}, ensure_ascii=False, indent=2), encoding='utf-8')
    print(json.dumps({'original_issues_changed': 6, 'equivalence': 'passed', 'proof_count': result['proof_count'], 'test_counts': result['test_counts']}))


if __name__ == '__main__':
    main()
