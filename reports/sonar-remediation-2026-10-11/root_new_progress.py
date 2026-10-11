"""记录root归属的新增告警；人工处置和源码验证保留独立证据。"""
from pathlib import Path
from datetime import datetime
from collections import Counter
import hashlib
import json
import subprocess

ROOT = Path(__file__).resolve().parents[2]
OUT = Path(__file__).resolve().parent


def read(name):
    return json.loads((OUT / name).read_text(encoding='utf-8'))


def main():
    owned = {
        'docs/architecture/tools/check_preview_typography.js',
        'docs/architecture/tools/frontend-api-scan.mjs',
        'docs/architecture/tools/render_native.js',
        'docs/architecture/tools/build_preview.py',
        'scripts/check-story052-production-deployment.mjs',
        'scripts/ci/check-generated-contracts.mjs',
        'scripts/lib/openapi-sdk.mjs',
        'services/api-gateway/tools/story056-real-worker-e2e/Dockerfile',
    }
    issues = read('full-checkpoint-02/issues.json')['issues']
    original = json.loads((ROOT / 'reports/platform-audit-2026-10-10/sonar/issues.json').read_text(encoding='utf-8'))['issues']
    old_keys = {row['key'] for row in original}
    current = [row for row in issues if row['key'] not in old_keys and row['component'].split(':', 1)[1] in owned]
    # build_preview的其它Python告警由Python智能体处理，root只负责字体回退说明的S125。
    current = [row for row in current if row['component'].split(':', 1)[1] != 'docs/architecture/tools/build_preview.py' or row['rule'] == 'python:S125']
    assert len(current) == 23
    js_name = 'root-new-js-tests-02.json' if (OUT / 'root-new-js-tests-02.json').exists() else 'root-new-js-tests-01.json'
    js = read(js_name)
    for file, sha in js['source_sha256'].items():
        assert hashlib.sha256((ROOT / file).read_bytes()).hexdigest() == sha
    assert js['exit_code'] == 0 and js['sources_unchanged']
    api = read('frontend-api-scope-equivalence-02.json')
    assert api['byte_identical'] and api['baseline_exit_code'] == api['current_exit_code'] == 0
    syntax = []
    for file in sorted(owned):
        if not file.endswith(('.mjs', '.js')):
            continue
        result = subprocess.run(['D:/node.js/node.exe', '--check', str(ROOT / file)], capture_output=True, text=True)
        syntax.append({'file': file, 'exit_code': result.returncode, 'source_sha256': hashlib.sha256((ROOT / file).read_bytes()).hexdigest(), 'output': result.stdout + result.stderr})
        assert result.returncode == 0
    dispositions = {row['key']: row for row in read('root-platform-disposition-phase02-results.json')['records'] if row.get('status') == 'platform_disposition_verified'}
    entries = []
    for issue in current:
        file = issue['component'].split(':', 1)[1]
        evidence = ['root-new-findings-baseline.json']
        state = 'modified_syntax_validated_pending_platform_rescan'
        reason = '保持当前行为，按告警修正；具体差异与验证见证据。'
        if issue['key'] in dispositions:
            state = 'platform_false_positive_verified'
            reason = dispositions[issue['key']]['reason']
            evidence.append('root-platform-disposition-phase02-results.json')
        elif file.endswith('build_preview.py'):
            state = 'reviewed_false_positive_awaiting_current_source_scan'
            reason = '两行中文解释离线机器字体回退及仅修改HTML内嵌SVG副本的边界，非注释掉的代码；该文件已有另外的XML解析改动，本次保留，误报处置必须以当前SHA复扫为准。'
        elif issue['rule'] == 'javascript:S905':
            state = 'reviewed_false_positive_awaiting_current_source_scan'
            reason = '整段async函数是playwright-cli run-code的函数输入，由runner注入page调用；不是无用的模块表达式。6处Error显式构造已更新，待当前SHA复扫后逐项处置。'
        elif file.endswith('frontend-api-scan.mjs'):
            state = 'validated_fix_pending_platform_rescan'
            reason = '15个每文件helper移至模块作用域并显式传入每文件context，保留符号隔离和递归深度边界；完整盘点报告1,176,650字节等价。首次常量递归漏参已修正，失败日志保留。'
            evidence.extend(['frontend-api-scope-equivalence-02.json', 'root-new-js-independent-review-python-agent.json'])
        elif file in js['source_sha256']:
            state = 'validated_fix_pending_platform_rescan'
            evidence.extend([js_name, js_name.replace('.json', '.tap'), 'root-new-js-independent-review-python-agent.json'])
            if (OUT / 'root-new-js-independent-review02-python-agent.json').exists():
                evidence.append('root-new-js-independent-review02-python-agent.json')
        elif file.endswith('check-story052-production-deployment.mjs'):
            state = 'validated_fix_pending_platform_rescan'
            reason = '迁移编号仅接收正则产生的字符串或undefined，显式string谓词保留原真值过滤与排序结果；只读部署检查exit0。'
            evidence.append('root-production-deployment-check-01.log')
        elif file.endswith('check_preview_typography.js'):
            reason = '6处Error(...)改为new Error(...)，错误文本和触发条件保持；Node语法检查通过，本批未重跑真实浏览器图纸预览。'
            evidence.append('root-new-js-syntax.json')
        elif file.endswith('Dockerfile'):
            reason = '合并连续apk安装与addgroup/adduser为一个RUN，以&&保持顺序和失败短路；USER/COPY/CMD不变。本批未拉镜像或Docker构建，待平台规则复验。'
        entries.append({'key': issue['key'], 'rule': issue['rule'], 'severity': issue['severity'], 'file': file, 'state': state, 'reason': reason, 'current_source_sha256': hashlib.sha256((ROOT / file).read_bytes()).hexdigest(), 'evidence': evidence})
    report = {'updated_at': datetime.now().astimezone().isoformat(), 'scope': 'checkpoint02新增47项中root独占23项；不混入原2223账本', 'issue_states': entries, 'state_counts': dict(Counter(row['state'] for row in entries)), 'final_platform_rescan_required': True}
    (OUT / 'root-new-findings-progress.json').write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding='utf-8')
    (OUT / 'root-new-js-syntax.json').write_text(json.dumps({'records': syntax}, ensure_ascii=False, indent=2), encoding='utf-8')
    print(json.dumps(report['state_counts']))


if __name__ == '__main__':
    main()
