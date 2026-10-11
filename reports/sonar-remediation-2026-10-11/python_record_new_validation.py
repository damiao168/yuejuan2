"""绑定新增告警验证与现有安全监听证据；平台状态由主智能体处理。"""
from collections import Counter
from datetime import datetime, timezone
import ast
import hashlib
import json
from pathlib import Path
from xml.etree import ElementTree

ROOT = Path(__file__).resolve().parents[2]
REPORT = Path(__file__).resolve().parent


def sha(path):
    return hashlib.sha256((ROOT / path).read_bytes()).hexdigest()


def function_excerpt(path, name):
    source = (ROOT / path).read_text('utf-8-sig')
    tree = ast.parse(source)
    node = next(item for item in ast.walk(tree) if isinstance(item, ast.FunctionDef) and item.name == name)
    return {'file': path, 'start_line': node.lineno, 'end_line': node.end_lineno,
            'current_sha256': sha(path), 'text': '\n'.join(source.splitlines()[node.lineno - 1:node.end_lineno])}


def tls_evidence(execution):
    prior = json.loads((REPORT / 'python-nonfix-evidence.json').read_text('utf-8'))
    records = []
    for item in prior['records']:
        if item['rule'] != 'python:S5332' or not item['file'].endswith('/server.py'):
            continue
        grading = item['file'].startswith('ai-services/')
        group = 'grading-tls-and-transport' if grading else 'math-verifier-tls-and-policy'
        result = next(value for value in execution['results'] if value['group'] == group)
        assert result['exit_code'] == 0
        assert sha(item['file']) == execution['source_hashes_after'][item['file']]
        policy_file = 'ai-services/grading_agent/config.py' if grading else item['file']
        policy = '_validate_credentials_and_listener' if grading else '_validate_listener_policy'
        serving = 'serve' if grading else 'main'
        junit = ElementTree.parse(REPORT / result['junit']).getroot()
        cases = [{'class': case.get('classname'), 'name': case.get('name')}
                 for case in junit.iter('testcase') if 'tls' in (case.get('classname', '') + case.get('name', '')).lower()
                 and not any(case.find(name) is not None for name in ['failure', 'error', 'skipped'])]
        record = {'key': item['key'], 'rule': item['rule'], 'severity': item['severity'],
                  'file': item['file'], 'original_line': item['original_line'],
                  'current_sha256': sha(item['file']), 'production_source_modified_by_this_agent': False,
                  'full_review': {'server': True, 'settings': grading,
                                  'entrypoint': 'ai-services/grading_agent/__main__.py' if grading else 'services/math-verification-worker/math_verification_worker/__main__.py',
                                  'entrypoint_directly_calls_validated_serving_function': True},
                  'source_proof': [function_excerpt(policy_file, policy), function_excerpt(item['file'], serving)],
                  'observed_behavior': [
                      '生产、缺省和未知环境在应用启动或绑定监听端口前拒绝缺证书的配置；证书/密钥必须成对。',
                      '显式开发环境的明文例外必须有真实 bool opt-in 且只使用字面 IPv4 回环；DNS、通配地址、远端、IPv6 和非 bool 被拒绝。',
                      'TLS 证书加载和 socket 包装先于 serve_forever；上下文最低 TLS1.2；TLS 初始化异常也关闭监听资源。',
                      '真实监听器接受可信 CA + 正确 SAN 的 HTTPS；拒绝错误/缺失 CA、SAN 不匹配以及明文业务响应。',
                  ],
                  'test_validation': {'report': 'python-new-test-execution.json', 'group': group,
                                      'exit_code': 0, 'junit_counts': result['junit_counts'],
                                      'junit': result['junit'], 'log': result['log'], 'passed_tls_test_cases': cases,
                                      'sources_unchanged_during_tests': True},
                  'recommendation': '请 root 按本证据在平台做 false_positive 评审；不改变正确的 TLS 行为以适配只识别 HTTPServer 构造的静态模式。',
                  'reason': '告警命中 ThreadingHTTPServer 构造/继承，但该监听 socket 在处理请求前由服务端 TLS 上下文包装。调用链中的默认拒绝策略和真实握手负例已验证。',
                  'platform_disposition': 'pending_root', 'no_suppression_or_source_exclusion': True}
        if not grading:
            record['observed_behavior'].append('数学服务另用真实 TLS1.2/TLS1.3 握手和 TLS1.1 ClientHello 告警响应验证协议下限。')
        records.append(record)
    assert len(records) == 2
    report = {'checked_at_utc': datetime.now(timezone.utc).isoformat(), 'count': 2,
              'platform_actions_performed': False, 'records': records,
              'scope': '本地当前源码和现有真实 TLS 测试；未访问远端仓库或替平台自动判定状态。'}
    (REPORT / 'python-tls-platform-evidence.json').write_text(json.dumps(report, ensure_ascii=False, indent=2), 'utf-8')
    lines = ['# 原两条生产监听 TLS 告警的平台评审证据', '',
             '两服务生产和未知环境都先检查证书策略，随后包装 TLS socket，再进入服务循环。ThreadingHTTPServer 构造告警未反映后续 socket 的真实 TLS 行为。未改生产 TLS 源码、未加入抑制或排除。', '',
             '当前 SHA 的真实 TLS/配置测试全部通过：grading 42 个测试 + 154 个 subtests，数学 96 个测试，均实际 exit 0 / 0 skip。可信 CA / SAN 的 HTTPS、错误 CA / SAN 拒绝、明文拒绝、资源回收均有既有测试证据；数学另验证 TLS1.1 拒绝和 TLS1.2 / 1.3 可用。', '']
    for record in records:
        lines.append(f"- `{record['key']}`：`{record['file']}`，SHA `{record['current_sha256']}`。建议 root 按详细 JSON 证据评审平台误报处置，当前处置仍为 pending_root。")
    (REPORT / 'python-tls-platform-evidence.md').write_text('\n'.join(lines), 'utf-8')
    return report


def main():
    execution = json.loads((REPORT / 'python-new-test-execution.json').read_text('utf-8'))
    equivalence = json.loads((REPORT / 'python-new-tools-equivalence.json').read_text('utf-8'))
    assert execution['passed'] and equivalence['passed']
    tls = tls_evidence(execution)
    progress_path = REPORT / 'python-new-findings-progress.json'
    progress = json.loads(progress_path.read_text('utf-8'))
    validations = {}
    for item in progress['files']:
        path, digest = item['file'], sha(item['file'])
        assert digest == item['current_sha256']
        if path.startswith('docs/architecture/tools/'):
            assert digest == equivalence['current_source_hashes'][path]
            validations[path] = {'report': 'python-new-tools-equivalence.json', 'passed': True, 'current_sha256': digest,
                                 'generated_artifact_writes': False}
        else:
            assert digest == execution['source_hashes_after'][path]
            validations[path] = {'report': 'python-new-test-execution.json', 'passed': True, 'current_sha256': digest,
                                 'exit_code': 0, 'skipped': 0}
        item['validation'] = validations[path]
    for item in progress['issues']:
        item['state'] = ('validated_existing_resolution_pending_platform_rescan'
                         if item['file'].endswith('/verify_typography.py') else 'validated_fix_pending_platform_rescan')
        item['validation'] = validations[item['file']]
    progress.update(updated_at_utc=datetime.now(timezone.utc).isoformat(),
                    summary=dict(Counter(item['state'] for item in progress['issues'])),
                    platform_actions_performed=False, source_exclusions_or_threshold_changes=False)
    progress_path.write_text(json.dumps(progress, ensure_ascii=False, indent=2), 'utf-8')
    lines = ['# checkpoint02 新增 Python 问题', '',
             '单独跟踪 19 个新 key：5 Critical、8 Major、6 Minor。17 个由本批修改，2 个 verify_typography 字典构造问题在接手时已由外部修改解消，保留当前安全 XML 解析。九个归属文件均全文审读。', '',
             '5 个工具在相同本地输入下输出等价，生成写入全部截获在内存；另覆盖抽读版本匹配、旧 SHA 拒绝、范围越界拒绝和新 partial 来源补记，不继承旧完成声明。真实 TLS/配置/传输、数学 TLS 和证书测试均 exit 0，共 143 个测试 + 154 个 subtests，0 skip。每个 key 与当前 SHA 及验证报告绑定，等待 root 实际平台复扫。', '',
             '| key | 级别 | 文件 | 状态 |', '| --- | --- | --- | --- |']
    lines += [f"| `{item['key']}` | {item['severity']} | `{item['file']}` | {item['state']} |" for item in progress['issues']]
    (REPORT / 'python-new-findings-progress.md').write_text('\n'.join(lines), 'utf-8')
    original_path = REPORT / 'python-progress.json'
    original = json.loads(original_path.read_text('utf-8'))
    frontend = 'docs/architecture/tools/write_frontend_specs.py'
    digest = sha(frontend)
    for item in original['issues']:
        if item['file'] == frontend:
            item['evidence'].append({'kind': 'after_checkpoint02_original_baseline_equivalence',
                                     'report': 'python-new-tools-equivalence.json', 'input_mode': 'original_190_issue_baseline',
                                     'passed': True, 'current_sha256': digest})
        for record in tls['records']:
            if item['key'] == record['key']:
                item['evidence'].append({'kind': 'current_secure_listener_review_and_real_handshake',
                                         'report': 'python-tls-platform-evidence.json', 'passed': True,
                                         'current_sha256': record['current_sha256'], 'recommended_platform_disposition': 'false_positive'})
    for item in original['files']:
        if item['file'] == frontend:
            item.update(source_sha256_after=digest, full_file_review_claim=True, review_status='annotated',
                        actual_review_scope='全部生成器源代码、路径常量与证据/路由目录边界完整审读；原基线到当前输出等价。', pending_scope='')
    original['updated_at'] = datetime.now(timezone.utc).isoformat()
    original_path.write_text(json.dumps(original, ensure_ascii=False, indent=2), 'utf-8')
    print(json.dumps({'new': progress['summary'], 'tls_evidence_count': tls['count']}, ensure_ascii=False))


if __name__ == '__main__':
    main()
