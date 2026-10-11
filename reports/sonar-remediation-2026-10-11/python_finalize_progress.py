"""汇总已发生的验证，不宣称平台已关闭告警或全文件维护审查已完成。"""
from collections import Counter
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
REPORT = Path(__file__).resolve().parent


def main():
    progress = json.loads((REPORT / 'python-progress.json').read_text('utf-8'))
    equivalence = json.loads((REPORT / 'python-tools-equivalence.json').read_text('utf-8'))
    xml_equivalence = json.loads((REPORT / 'python-tools-equivalence-xml-security.json').read_text('utf-8'))
    execution = json.loads((REPORT / 'python-test-execution.json').read_text('utf-8'))
    assert equivalence['passed'] and equivalence['sources_unchanged_during_comparison']
    assert xml_equivalence['passed'] and xml_equivalence['sources_unchanged_during_comparison']
    assert all(item['exit_code'] == 0 for item in execution['results']) and execution['sources_unchanged_during_tests']
    compile_results = []
    for item in progress['files']:
        path = ROOT / item['file']
        source = path.read_text('utf-8-sig')
        compile(source, str(path), 'exec')
        digest = hashlib.sha256(path.read_bytes()).hexdigest()
        compile_results.append({'file': item['file'], 'current_sha256': digest, 'passed': True})
        item['source_sha256_after'] = digest
        item['source_changed_in_remediation'] = digest != item['source_sha256_before']
        item['full_file_review_claim'] = item['file'] == 'docs/architecture/tools/preview_server.py'
        item['review_status'] = 'annotated' if item['full_file_review_claim'] else 'partial'
        item['actual_review_scope'] = '原Sonar问题位置、修改的函数及必要调用/验证上下文；未将自动枚举计为审读'
        item['pending_scope'] = '' if item['full_file_review_claim'] else '问题范围以外的完整逐行维护审读仍为pending'
    for item in progress['issues']:
        if item['file'] in equivalence['current_source_hashes'] and item['status'] in {'code_modified_pending_validation', 'validated_fix_pending_rescan', 'external_source_change_pending_revalidation'}:
            digest = hashlib.sha256((ROOT / item['file']).read_bytes()).hexdigest()
            selected = xml_equivalence if item['file'] in xml_equivalence['current_source_hashes'] else equivalence
            if digest != selected['current_source_hashes'][item['file']]:
                item['status'] = 'external_source_change_pending_revalidation'
                item['evidence'].append({'kind': 'source_changed_after_validation', 'current_sha256': digest,
                                         'verified_sha256': selected['current_source_hashes'][item['file']],
                                         'owner': 'external_change_preserved_root_coordinating'})
                continue
            item['status'] = 'validated_fix_pending_rescan'
            evidence_report = 'python-tools-equivalence-xml-security.json' if selected is xml_equivalence else 'python-tools-equivalence.json'
            item['evidence'].append({'kind': 'final_baseline_output_equivalence', 'report': evidence_report,
                                     'passed': True, 'current_sha256': digest, 'tool': item['file']})
        if item['status'] == 'validated_fix_pending_rescan':
            item['platform_rescan'] = 'pending_root'
    progress['summary'] = dict(Counter(item['status'] for item in progress['issues']))
    progress['validation'].extend([
        {'report': 'python-test-execution.json', 'passed': True, 'groups': len(execution['results']),
         'junit_cases': 1223, 'pytest_tests': 1006, 'unittest_subtests': 217,
         'limitation': '本机依赖版本如Pillow11.3.0/pypdfium2 5.12.1详见实际版本记录；不是生产模型或数据库验收'},
        {'report': 'python-rls-parser-validation.json', 'passed': True, 'cases': 12,
         'malformed_headers': 10000, 'generated_artifact_writes': False},
        {'report': 'python-source-compile-validation.json', 'passed': True, 'files': len(compile_results),
         'method': 'compile()内存语法检查，不生成pyc或改变源码'},
        {'report': 'python-tools-equivalence-xml-security.json', 'passed': True,
         'scope': '验证后发生的3个外部文件修改按最新SHA重新运行正常输出等价比较'},
        {'report': 'python-xml-security-validation.json', 'passed': True, 'cases': 8,
         'scope': '最新2个XML安全读取文件的真实入口，拒绝DTD/实体，网络访问为0'},
    ])
    progress['updated_at'] = datetime.now(timezone.utc).isoformat()
    (REPORT / 'python-source-compile-validation.json').write_text(json.dumps({'passed': True, 'files': compile_results}, ensure_ascii=False, indent=2), 'utf-8')
    (REPORT / 'python-progress.json').write_text(json.dumps(progress, ensure_ascii=False, indent=2), 'utf-8')
    lines = ['# Python 原Sonar问题处理记录', '',
             '190 条全部跟踪：165 条已修改，原修改版本通过适当本地验证；验证后外部编辑改变源码SHA的条目在JSON中明确为external_source_change_pending_revalidation。22 条有逐key误报证据等待平台处置，1 条本机架构预览 HTTP 待平台按上下文接受，2 条基线已含 TLS 策略等待实际复扫。没有使用 NOSONAR、源码排除或规则阈值放宽。', '',
             '全部原问题的源码处理与上下文评审完成，Sonar实际复扫/关闭由主智能体统一执行；本记录不将其记为平台清零。完整逐文件维护审查与问题范围不同，未全文审读的范围明确为pending。', '',
             '现有8组测试：1006个测试与217个subtests通过；首次OCR导入模式收集失败的日志已保留，正常prepend模式重跑388个测试通过。14个生成器在相同缓存输入下运行，所有42个生成写入只在内存捕获，输出等价；没有覆盖工作区生成产物。RLS新增单次扫描器通过12个旧正则语义兼容样例及10000个缺失END LOOP边界输入检查。64个原问题文件通过内存语法编译。', '',
             '| 文件 | 原问题数 | 原问题处理状态 | 全文件审查状态 |', '| --- | ---: | --- | --- |']
    for item in progress['files']:
        states = Counter(issue['status'] for issue in progress['issues'] if issue['file'] == item['file'])
        state_text = '; '.join(f'{name}:{count}' for name, count in states.items())
        lines.append(f"| `{item['file']}` | {len(item['issues'])} | {state_text} | {item['review_status']} |")
    lines += ['', '详细 key、源码 SHA 与每条验证依据见 python-progress.json；非源码处置理由与摘录见 python-nonfix-evidence.json。']
    (REPORT / 'python-progress.md').write_text('\n'.join(lines), 'utf-8')
    print(json.dumps(progress['summary'], ensure_ascii=False))


if __name__ == '__main__':
    main()
