"""记录主智能体两份 JS 重构的独立只读审查，不修改其源码或调用平台。"""
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
REPORT = Path(__file__).resolve().parent
BASELINE = Path('D:/tools/sonar-remediation-20261011/root-new-findings-baseline')


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def main():
    sdk = 'scripts/lib/openapi-sdk.mjs'
    scanner = 'docs/architecture/tools/frontend-api-scan.mjs'
    source = (ROOT / scanner).read_text('utf-8-sig')
    recursion_corrected = 'pathValue(context, context.constants.get(node.text), depth + 1)' in source
    unused_context = 'function isDirectTransport(context, expression)' in source
    tests = json.loads((REPORT / 'root-new-js-tests-01.json').read_text('utf-8'))
    assert tests['exit_code'] == 0 and tests['source_sha256'][sdk] == digest(ROOT / sdk)
    report = {
        'reviewed_at_utc': datetime.now(timezone.utc).isoformat(),
        'reviewer': '/root/sonar_python_fix', 'source_writes_performed': False,
        'baseline': str(BASELINE),
        'files': [
            {'file': sdk, 'baseline_sha256': digest(BASELINE / sdk), 'current_sha256': digest(ROOT / sdk),
             'full_current_source_reviewed': True, 'baseline_scope': 'verifyArtifactPath 与完整原 writeIfChanged 段',
             'verdict': 'no_actionable_regression_found',
             'checks': [
                 'EEXIST 重试仍为 attempt=0/1/2；第三次争用直接抛 createError，不额外打开或截断文件。',
                 'O_RDWR 失败仅 EACCES/EPERM 进入 O_RDONLY；只读非普通文件抛原权限错误，内容不同在 close 后抛原错误。',
                 '只读内容相同先验证 dev/ino 身份再返回 false；stat/读取异常保留原发生顺序，描述符均由 readonly helper 的 finally 关闭。',
                 '写入成功返回的 descriptor 只交给 writeIfChanged 的 try/finally 关闭；null 表示只读 helper 已完成关闭，避免重复 close。',
                 'fstat、FD 读取、位置 0 短写补齐、ftruncate、最后路径身份校验和 close 的顺序不变。',
                 '创建空文件返回 true，已有内容相同返回 false，原创建/更新与错误语义保留。',
             ],
             'existing_dynamic_evidence': {'report': 'root-new-js-tests-01.json', 'log': 'root-new-js-tests-01.tap',
                                           'exit_code': 0, 'passed': 18, 'skipped': 0, 'current_sha_bound': True},
             'test_scope_reviewed': ['scripts/ci/generated-files-race.test.mjs', 'scripts/ci/openapi-sdk.batch20.test.mjs',
                                     'scripts/ci/architecture-audit-tools.test.mjs']},
            {'file': scanner, 'baseline_sha256': digest(BASELINE / scanner), 'current_sha256': digest(ROOT / scanner),
             'full_current_source_reviewed': True, 'full_baseline_source_reviewed': True,
             'verdict': 'one_runtime_regression_reported_and_current_correction_verified',
             'checks': [
                 '每个 sourceMap 文件迭代都新建 functions/constants Map 与 sdkNames Set，显式 context 不保留上个文件的符号。',
                 'collect 和 visit 的递归 child 回调都传同一 context；字符串/模板/加法/返回值/调用展开深度和遍历次序保留。',
                 '全局 calls/imports 仍只聚合带来源文件的输出，不承载文件局部符号。',
                 '路由文字解析、导入图、页面可达性和后端映射的剩余代码与基线相同；未扩大可达性为运行保证。',
             ],
             'dynamic_scan_verification_by_this_reviewer': False,
             'dynamic_scan_validation_owner': 'root frozen-input output equivalence',
             'reported_findings': [
                 {'priority': 1, 'line': 96, 'problem': 'Identifier 递归曾错误访问 context.constantsconstants 且漏传 context。',
                  'trigger': '某 API 调用的路径参数是 collect 已收集的常量标识符。',
                  'impact': 'TypeError 中止整次盘点；即使只改字段名，错误递归参数仍无法正常展开。',
                  'expected': 'pathValue(context, context.constants.get(node.text), depth + 1)',
                  'current_source_correction_verified': recursion_corrected, 'status': 'corrected_by_root' if recursion_corrected else 'open'},
                 {'priority': 3, 'line': 143, 'problem': 'isDirectTransport 的 context 参数未使用。',
                  'impact': '引入不必要的参数；可删除签名/调用的首参以避免新增维护或未使用参数告警。',
                  'runtime_behavior_affected': False, 'status': 'open_optional_cleanup' if unused_context else 'corrected_by_root'},
             ]},
        ],
    }
    (REPORT / 'root-new-js-independent-review-python-agent.json').write_text(json.dumps(report, ensure_ascii=False, indent=2), 'utf-8')
    text = '# root 新增 JS 重构独立审查\n\n'
    text += '全文审读当前两文件，并比较 root-new-findings-baseline。openapi-sdk 写文件阶段拆分未发现行为回归：三轮争用重试、只读错误顺序、FD 与路径身份校验及 close 职责保持。已核对 root 的 18 通过 / 0 skip / exit 0 测试证据与 SDK 当前 SHA。\n\n'
    text += 'frontend-api-scan 曾在常量标识符递归遗漏 context 并误写 constantsconstants，已及时报告；当前正确递归表达式已复核。每个文件使用新符号表，未发现跨文件污染。完整盘点输出等价验证由 root 执行，本审查不冒充其动态结果。\n\n'
    text += 'isDirectTransport 的 context 未使用，已作为无运行影响的可选清理告知 root。审查未修改 root 源码。逐文件 SHA、具体检查和发现状态见 JSON。\n'
    (REPORT / 'root-new-js-independent-review-python-agent.md').write_text(text, 'utf-8')
    print(json.dumps({'recursion_corrected_by_root': recursion_corrected, 'unused_context_remains': unused_context,
                      'sdk_tests_current_sha_bound': True, 'source_writes': False}))


if __name__ == '__main__':
    main()
