"""保存人工判定的原告警上下文；平台关闭与复扫由主智能体执行。"""
from collections import Counter
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
import xml.etree.ElementTree as ET

ROOT = Path(__file__).resolve().parents[2]
REPORT = Path(__file__).resolve().parent

# 以下理由来自逐段审读，源码摘录与摘要由脚本保留，不把自动枚举记为全文审读。
COMMENT_REASONS = {
    ('ai-services/grading_agent/paper_compact/anchors.py', 22): '说明考试题目分值与题干游戏得分的业务区分，紧接分值正则；没有被注释的可执行代码。',
    ('ai-services/grading_agent/paper_compact/anchors.py', 170): '说明解题步骤编号与递增题号的边界，紧接新题判定；属于自然语言规则说明。',
    ('ai-services/grading_agent/paper_compact/layout.py', 181): '解释公式裁剪边距使顶边失真，竖直中心排序更接近基线的原因。',
    ('ai-services/grading_agent/paper_validation.py', 9): '解释缺少明确考试分值时清除模型建议分值的保守条件，紧接允许的文本标记正则。',
    ('ai-services/grading_agent/paper_visual.py', 113): '解释旧保存样本回放兼容、紧凑契约展开与供应商流量边界，紧接本地表示转换。',
    ('services/image-quality-worker/image_quality/geometry.py', 197): '解释平板扫描无可见外轮廓时降低置信度的原因，没有虚构边框可靠性。',
    ('services/ocr-worker/ocr_worker/formula_validation.py', 70): '解释数学宏白名单及未知宏拒绝的安全边界，紧接允许列表。',
    ('services/ocr-worker/ocr_worker/math_layout.py', 164): '解释行内小公式裁剪扩展同时受配置和检测框高度约束的原因。',
    ('services/ocr-worker/ocr_worker/paper_formula.py', 544): '解释检测分类和边缘裁剪异常不能通过升级识别模型修复，紧接备用模型触发条件。',
    ('services/ocr-worker/ocr_worker/symbolic_runner.py', 90): '解释第二阶段重试规范化用于从第一阶段短暂失败恢复的顺序。',
    ('services/ocr-worker/ocr_worker/symbolic_runner.py', 172): '解释求解集合只保存为约束事实，不能直接证明学生结论完整并计分。',
    ('services/page-processing-worker/page_processing/config.py', 36): '解释顺序处理且只给当前任务发心跳，批量提前领任务会造成后续租约失效。',
    ('services/page-processing-worker/page_processing/omr.py', 68): '解释空白参考图配准产生窄光晕，测量新增墨迹前去除该光晕的边界。',
    ('services/page-processing-worker/page_processing/omr_acceptance.py', 84): '解释确定性扫描噪声必须放在答案内部以外，避免验收只处理相同图像副本。',
    ('services/page-processing-worker/page_processing/runner.py', 128): '解释失败报告也失败时仍维持工作循环及记录原始失败原因的恢复边界。',
}


def test_result(group, executions):
    execution = next((item for item in executions['results'] if item['group'] == group), None)
    if execution is None:
        return {'group': group, 'status': 'not_run'}
    result = {'group': group, 'exit_code': execution['exit_code'], 'log': execution['log'], 'junit': execution['junit']}
    path = REPORT / execution['junit']
    if path.exists():
        suites = ET.parse(path).getroot()
        result['counts'] = {name: sum(int(suite.get(name, '0')) for suite in suites.iter('testsuite'))
                            for name in ('tests', 'failures', 'errors', 'skipped')}
    return result


def source_excerpt(path, line, before=5, after=8):
    lines = path.read_text('utf-8-sig').splitlines()
    start, end = max(1, line - before), min(len(lines), line + after)
    return {'start_line': start, 'end_line': end, 'text': '\n'.join(lines[start - 1:end])}


def main():
    progress = json.loads((REPORT / 'python-progress.json').read_text('utf-8'))
    executions = json.loads((REPORT / 'python-test-execution.json').read_text('utf-8'))
    baseline = json.loads((REPORT / 'baseline.json').read_text('utf-8'))
    baseline_by_path = {item['path']: item for item in baseline['files']}
    records = []
    for item in progress['issues']:
        if item['status'] not in {'pending', 'reviewed_false_positive_pending_platform',
                                  'reviewed_intentional_local_http_pending_platform', 'reviewed_tls_remediation_pending_rescan'}:
            continue
        path = ROOT / item['file']
        source = path.read_text('utf-8-sig')
        digest = hashlib.sha256(path.read_bytes()).hexdigest()
        record = {name: item[name] for name in ('key', 'rule', 'severity', 'file', 'original_line', 'message')}
        record.update(current_sha256=digest, remediation_baseline_sha256=baseline_by_path[item['file']]['sha256'],
                      changed_since_remediation_baseline=digest != baseline_by_path[item['file']]['sha256'],
                      source_excerpt=source_excerpt(path, item['original_line']), platform_disposition='pending_root',
                      full_file_review_claim=False)
        if item['rule'] == 'python:S125':
            line = item['original_line']
            lines = source.splitlines()
            comment_lines = []
            while line <= len(lines) and lines[line - 1].lstrip().startswith('#'):
                comment_lines.append({'line': line, 'text': lines[line - 1]})
                line += 1
            record.update(verdict='false_positive_natural_language_comment', reason=COMMENT_REASONS[(item['file'], item['original_line'])],
                          actual_comment_lines=comment_lines, recommended_platform_disposition='false_positive')
            assert comment_lines and all(entry['text'].lstrip().startswith('#') for entry in comment_lines)
            group = ('grading-agent' if item['file'].startswith('ai-services/') else
                     'page-processing-worker' if '/page-processing-worker/' in item['file'] else
                     'ocr-worker' if '/ocr-worker/' in item['file'] else None)
            record['existing_tests'] = test_result(group, executions) if group else {'group': 'image-quality-worker', 'status': 'not_run_in_this_batch'}
            item['status'] = 'reviewed_false_positive_pending_platform'
        elif item['rule'] == 'python:S2245':
            record.update(verdict='false_positive_non_security_synthetic_randomness',
                          reason='种子驱动的仿真字体颜色、角度、选择题笔画和扫描噪声，不用于密码、令牌、权限、真实考生身份或密钥。改为密码学随机源将破坏可复现的仿真与已有种子契约。',
                          synthetic_output_contract={'synthetic': True, 'simulation_only': True,
                                                     'training_allowed': False, 'contains_real_student_identity': False},
                          synthetic_contract_excerpt=source_excerpt(path, next(n for n, text in enumerate(source.splitlines(), 1) if '"simulation_only": True' in text), 5, 5),
                          existing_tests={**test_result('simulation-generator', executions),
                                          'scope': '已有测试校验选项顺序、独立种子和笔画轨迹；不宣称覆盖所有图像噪声函数'},
                          recommended_platform_disposition='false_positive')
            item['status'] = 'reviewed_false_positive_pending_platform'
        elif item['rule'] == 'python:S3415':
            current_line = next(n for n, text in enumerate(source.splitlines(), 1)
                                if 'self.assertEqual(plan.profile_sha256, hashlib.sha256(raw).hexdigest())' in text)
            record.update(verdict='false_positive_correct_actual_expected_order',
                          reason='第一个参数 plan.profile_sha256 是被测 resolve_formula_runtime_plan 的实际输出；第二个参数 hashlib.sha256(raw).hexdigest() 是由固定输入字节独立计算的预期值，符合本文件统一的 actual-first unittest 约定。',
                          source_excerpt=source_excerpt(path, current_line, 9, 4),
                          actual_expression='plan.profile_sha256', expected_expression='hashlib.sha256(raw).hexdigest()',
                          test='FormulaProfileBatch36Tests.test_file_profile_keeps_original_byte_digest',
                          existing_tests=test_result('ocr-worker', executions), recommended_platform_disposition='false_positive')
            item['status'] = 'reviewed_false_positive_pending_platform'
        elif item['file'].endswith('/preview_server.py'):
            record.update(verdict='intentional_local_document_preview_http',
                          reason='静态架构预览与渲染结果回写监听固定 IPv4 回环 127.0.0.1:8766；不是生产阅卷接口。路径允许列表、20MiB上传上限及 resolve 后的 BASE 范围检查在同一文件。当前选择保留本机预览 HTTP，需平台按上下文记录接受，而非声称已升级TLS。',
                          source_excerpt=source_excerpt(path, 20, 19, 30),
                          recommended_platform_disposition='accepted_contextual_local_only_http',
                          existing_tests={'report': 'python-tools-equivalence.json', 'scope': 'log_message 方法和原业务行为输出等价，不是HTTP监听集成测试'})
            item['status'] = 'reviewed_intentional_local_http_pending_platform'
        else:
            group = 'grading-agent' if item['file'].startswith('ai-services/') else 'math-verifier'
            record.update(verdict='preexisting_tls_remediation_requires_actual_rescan',
                          reason='整改基线中已有TLS监听策略，创建监听器前验证证书/环境/布尔opt-in/IPv4字面回环约束，配置证书时用最低TLS1.2包装socket；本智能体未修改该业务源码。是否关闭原HTTP告警以主智能体实际复扫结果为准。',
                          contains_http_literal='http://' in source, existing_tests=test_result(group, executions),
                          tls_policy_file='ai-services/grading_agent/config.py' if group == 'grading-agent' else item['file'],
                          tls_regression_tests=['ai-services/tests/test_config.py', 'ai-services/tests/test_server_tls.py', 'ai-services/tests/test_server_tls_handshake.py'] if group == 'grading-agent' else ['services/math-verification-worker/tests/test_server_tls_batch54.py'],
                          recommended_platform_disposition='rescan_first_do_not_force_close')
            item['status'] = 'reviewed_tls_remediation_pending_rescan'
        item['evidence'] = [entry for entry in item['evidence'] if entry['kind'] != 'reviewed_platform_context']
        item['evidence'].append({'kind': 'reviewed_platform_context', 'report': 'python-nonfix-evidence.json',
                                 'key': item['key'], 'current_sha256': digest,
                                 'platform_disposition': 'pending_root'})
        records.append(record)
    evidence = {'recorded_at_utc': datetime.now(timezone.utc).isoformat(), 'count': len(records),
                'platform_actions_performed': False, 'no_source_exclusion_or_threshold_change': True, 'records': records}
    assert len(records) == 25
    (REPORT / 'python-nonfix-evidence.json').write_text(json.dumps(evidence, ensure_ascii=False, indent=2), 'utf-8')
    prose = ['# Python 原告警的上下文证据', '',
             '25 条逐项审读记录：22 条建议平台误报处置，1 条本机预览 HTTP 建议按上下文接受，2 条基线已包含 TLS 修复等待实际复扫。没有操作平台、排除源码或放宽规则。', '',
             '首次 OCR 测试仅收集失败，原因是本次 runner 选择 importlib 模式而现有测试依赖同目录 test_runner；已保留首次日志，并按正常 prepend 模式重跑，结果以逐条 existing_tests 记录为准。', '']
    for record in records:
        prose += [f"## {record['key']}", '', f"`{record['rule']}` · `{record['file']}:{record['original_line']}`", '',
                  record['reason'], '', f"当前源码 SHA256：`{record['current_sha256']}`。平台处置：pending_root。", '',
                  '```python', record['source_excerpt']['text'], '```', '']
    (REPORT / 'python-nonfix-evidence.md').write_text('\n'.join(prose), 'utf-8')
    # 通过的现有测试只绑定参与本次源码版本，不把未重跑 OCR 或生成器细项标为已验证。
    for item in progress['issues']:
        if item['status'] != 'code_modified_pending_tests':
            continue
        group = ('grading-agent' if item['file'].startswith('ai-services/') else
                 'page-processing-worker' if '/page-processing-worker/' in item['file'] else
                 'subjective-worker' if '/subjective-grading-worker/' in item['file'] else
                 'math-lab' if item['file'].startswith('lab/') else
                 'ai-evaluation' if item['file'].startswith('tests/ai-evaluation/') else 'ocr-worker')
        result = test_result(group, executions)
        digest = hashlib.sha256((ROOT / item['file']).read_bytes()).hexdigest()
        if result.get('exit_code') == 0 and executions['source_hashes_after'].get(item['file']) == digest:
            item['status'] = 'validated_fix_pending_rescan'
            item['evidence'].append({'kind': 'existing_test_suite', 'report': 'python-test-execution.json',
                                     'group': group, 'passed': True, 'current_sha256': digest})
    progress['summary'] = dict(Counter(item['status'] for item in progress['issues']))
    progress['updated_at'] = datetime.now(timezone.utc).isoformat()
    (REPORT / 'python-progress.json').write_text(json.dumps(progress, ensure_ascii=False, indent=2), 'utf-8')
    print(json.dumps(progress['summary'], ensure_ascii=False))


if __name__ == '__main__':
    main()
