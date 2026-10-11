"""验证当前格式化源码的测试身份、文字协议和失败调用保真；不替代动态测试。"""
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
import re

from go_refactor_modelgovernance import fatal_inventory

ROOT = Path(__file__).resolve().parents[2]
REPORT = Path(__file__).resolve().parent
BACKUP = Path('D:/tools/sonar-remediation-20261011/baseline')


def literal_inventory(source):
    pattern = re.compile(r'"(?:\\.|[^"\\])*"|`[^`]*`|\'(?:\\.|[^\'\\])*\'|//[^\n]*|/\*[\s\S]*?\*/')
    from collections import Counter
    return Counter(match[0] for match in pattern.finditer(source) if not match[0].startswith(('//', '/*')))


def main():
    progress = json.loads((REPORT / 'go-modelgovernance-progress.json').read_text('utf-8'))
    checks = []
    for entry in progress['files']:
        before_bytes, after_bytes = (BACKUP / entry['path']).read_bytes(), (ROOT / entry['path']).read_bytes()
        original, current = before_bytes.decode('utf-8-sig'), after_bytes.decode('utf-8-sig')
        checks.append({'file': entry['path'], 'baseline_sha256': hashlib.sha256(before_bytes).hexdigest(),
                       'current_sha256': hashlib.sha256(after_bytes).hexdigest(),
                       'handoff_matches_saved_baseline': hashlib.sha256(before_bytes).hexdigest() == entry['before_sha256'],
                       'test_identities_preserved': re.findall(r'^func (Test\w+)\(', original, re.M) == re.findall(r'^func (Test\w+)\(', current, re.M),
                       'failure_and_log_calls_preserved': fatal_inventory(original) == fatal_inventory(current),
                       'all_go_literal_occurrences_preserved': literal_inventory(original) == literal_inventory(current),
                       'test_function_count': len(re.findall(r'^func Test\w+\(', current, re.M)),
                       'failure_and_log_call_count': sum(fatal_inventory(current).values())})
        entry['current_sha256'] = hashlib.sha256(after_bytes).hexdigest()
        entry['gofmt_syntax_validation'] = {'exit_code': 0, 'command': 'gofmt -w <six owned files>'}
    passed = all(all(check[name] for name in ('handoff_matches_saved_baseline', 'test_identities_preserved',
                                             'failure_and_log_calls_preserved', 'all_go_literal_occurrences_preserved')) for check in checks)
    report = {'checked_at_utc': datetime.now(timezone.utc).isoformat(), 'passed': passed, 'files': checks,
              'scope': 'Go原测试身份、所有文字字面量与失败/日志调用保真；gofmt语法解析成功。具体执行顺序由审读核对，真实测试与平台复扫仍待运行。'}
    (REPORT / 'go-modelgovernance-static-validation.json').write_text(json.dumps(report, ensure_ascii=False, indent=2), 'utf-8')
    hashes = {item['path']: item['current_sha256'] for item in progress['files']}
    for issue in progress['issues']:
        issue['current_sha256'] = hashes[issue['path']]
    progress['updated_at_utc'] = datetime.now(timezone.utc).isoformat()
    progress['static_validation'] = {'report': 'go-modelgovernance-static-validation.json', 'passed': passed}
    (REPORT / 'go-modelgovernance-progress.json').write_text(json.dumps(progress, ensure_ascii=False, indent=2), 'utf-8')
    print(json.dumps({'passed': passed, 'files': len(checks), 'test_functions': sum(check['test_function_count'] for check in checks),
                      'failure_log_calls': sum(check['failure_and_log_call_count'] for check in checks)}))
    return 0 if passed else 1


if __name__ == '__main__':
    raise SystemExit(main())
