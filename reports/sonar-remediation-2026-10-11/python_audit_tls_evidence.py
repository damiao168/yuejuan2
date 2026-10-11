"""重新核对 TLS 证据的原告警身份、冻结快照和真实握手记录，不改变平台状态。"""
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
from xml.etree import ElementTree

ROOT = Path(__file__).resolve().parents[2]
REPORT = Path(__file__).resolve().parent


def main():
    evidence = json.loads((REPORT / 'python-tls-platform-evidence.json').read_text('utf-8'))
    original = {item['key']: item for item in json.loads((ROOT / 'reports/platform-audit-2026-10-10/sonar/issues.json').read_text('utf-8'))['issues']}
    checkpoint = {item['key']: item for item in json.loads((REPORT / 'full-checkpoint-02/issues.json').read_text('utf-8'))['issues']}
    scope = json.loads((REPORT / 'full-checkpoint-02/source-scope.json').read_text('utf-8'))
    frozen = {item['path']: item for item in scope['files']}
    execution = json.loads((REPORT / 'python-new-test-execution.json').read_text('utf-8'))
    rules = json.loads((REPORT / 'full-checkpoint-02/rules.json').read_text('utf-8'))['python:S5332']['rule']
    records = []
    for item in evidence['records']:
        key, path = item['key'], item['file']
        current = hashlib.sha256((ROOT / path).read_bytes()).hexdigest()
        physical = hashlib.sha256((Path(scope['snapshot']) / path).read_bytes()).hexdigest()
        group = next(value for value in execution['results'] if value['group'] == item['test_validation']['group'])
        junit = ElementTree.parse(REPORT / group['junit']).getroot()
        cases = list(junit.iter('testcase'))
        names = [(case.get('classname', '') + '/' + case.get('name', '')) for case in cases
                 if not any(case.find(tag) is not None for tag in ['error', 'failure', 'skipped'])]
        grading = path.startswith('ai-services/')
        required = (['test_production_https_preserves_health_readiness_and_service_authentication',
                     'test_wrong_or_missing_ca_rejects_the_same_real_listener', 'test_san_mismatch_rejects_a_trusted_certificate',
                     'test_plaintext_does_not_receive_an_http_or_json_response',
                     'test_missing_invalid_or_mismatched_certificate_closes_real_resources'] if grading else
                    ['test_actual_main_serves_health_and_bounded_normalization_over_tls',
                     'test_actual_tls_listener_preserves_internal_token_rejection',
                     'test_actual_tls_listener_rejects_untrusted_certificate',
                     'test_actual_tls_listener_rejects_wrong_certificate_san',
                     'test_actual_tls_listener_rejects_plaintext_http',
                     'test_actual_tls_listener_negotiates_supported_protocols',
                     'test_actual_tls_listener_rejects_a_tls11_client_hello',
                     'test_actual_certificate_load_failure_releases_listener'])
        checks = {
            'key_in_original': key in original,
            'original_rule_and_file_match': original[key]['rule'] == item['rule'] and original[key]['component'].split(':', 1)[1] == path,
            'key_in_checkpoint02': key in checkpoint,
            'checkpoint_rule_and_file_match': checkpoint[key]['rule'] == item['rule'] and checkpoint[key]['component'].split(':', 1)[1] == path,
            'source_sha_matches_record_current_and_frozen_bytes': item['current_sha256'] == current == frozen[path]['sha256'] == physical,
            'source_sha_matches_before_after_real_tests': current == execution['source_hashes_before'][path] == execution['source_hashes_after'][path],
            'junit_no_failures_errors_or_skips': all(case.find(tag) is None for case in cases for tag in ['error', 'failure', 'skipped']),
            'actual_exit_zero': group['exit_code'] == 0,
            'every_required_real_handshake_case_present_and_passed': all(any(name in actual for actual in names) for name in required),
        }
        excerpts = []
        for snippet in item['source_proof']:
            source_path = snippet['file']
            data = (ROOT / source_path).read_bytes()
            source_sha = hashlib.sha256(data).hexdigest()
            current_excerpt = '\n'.join(data.decode('utf-8-sig').splitlines()[snippet['start_line'] - 1:snippet['end_line']])
            match = (source_sha == snippet['current_sha256'] == frozen[source_path]['sha256']
                     == execution['source_hashes_before'][source_path] == execution['source_hashes_after'][source_path]
                     and snippet['text'] == current_excerpt)
            excerpts.append({'file': source_path, 'same_sha_frozen_current_before_after_tests_and_excerpt': match,
                             'current_sha256': source_sha})
        checks['all_policy_and_serving_excerpts_sha_match_checkpoint02'] = all(value['same_sha_frozen_current_before_after_tests_and_excerpt'] for value in excerpts)
        records.append({'key': key, 'file': path, 'current_sha256': current,
                        'checkpoint02_status_observed': checkpoint[key]['status'],
                        'checks': checks, 'policy_and_serving_excerpts': excerpts,
                        'required_real_handshake_test_names': required, 'passed': all(checks.values()),
                        'context_assessment': '满足按当前实际运行路径人工评审生产明文告警误报的证据要求；开发明文例外须在平台说明中明确保留，不可声称所有执行路径都用 HTTPS。',
                        'bounded_exception': '只有 local/development/dev/test、真实 bool opt-in、字面 IPv4 loopback 可明文；生产、未知和缺省环境必须 TLS。',
                        'platform_action': 'none; root owns disposition'})
    report = {'checked_at_utc': datetime.now(timezone.utc).isoformat(), 'passed': all(item['passed'] for item in records),
              'count': len(records), 'checkpoint02_snapshot_at': scope['snapshot_at'],
              'rule_export': {'file': 'full-checkpoint-02/rules.json', 'name': rules['name'], 'type': rules['type']},
              'rule_context_note': '规则导出的 URL 例外包含 loopback，但不能仅凭这个文档条目跳过服务端构造告警；本评审另外核对 TLS 包装先于服务、生产拒绝明文与真实握手。',
              'test_snapshot_binding_note': '生产 server/config SHA 与 checkpoint02 完全一致；本次部分测试文件为随后 S5778 修复版，测试自己的当前 SHA 绑定在 python-new-test-execution.json，不冒充冻结测试源码。',
              'platform_state_modified': False, 'records': records}
    (REPORT / 'python-tls-evidence-independent-audit.json').write_text(json.dumps(report, ensure_ascii=False, indent=2), 'utf-8')
    print(json.dumps({'passed': report['passed'], 'records': [{key: item[key] for key in ['key', 'file', 'current_sha256', 'passed']} for item in records]}))
    return 0 if report['passed'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
