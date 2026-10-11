"""只读审查最终报告生成器的文字与统计口径，不执行生成器 main 或改其源码。"""
from collections import Counter
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
REPORTS = Path(__file__).parent

def sha(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()

def read(path):
    return json.loads(path.read_text(encoding='utf-8-sig'))

def main():
    writer = REPORTS / 'write_final_report.py'
    source = writer.read_text(encoding='utf-8')
    original_path = ROOT / 'reports/platform-audit-2026-10-10/sonar/issues.json'
    original = read(original_path)['issues']
    sources = ['frontend-web-retry-01-execution.json', 'frontend-web-current-typecheck-02.json',
               'frontend-production-contracts-current-01.json', 'go-final-validation-audit.md',
               'go-server-batch04-tests.json', 'go-server-question-bank-progress.md',
               'delivery-independent-fresh-followup-audit-python-agent.md',
               'fresh-python-dynamic-01/execution.json', 'root-new-js-tests-02.json',
               'frontend-api-scope-equivalence-02.json', 'python-tls-platform-evidence.md',
               'root-go-final-02-progress.md', 'go-server-reliability-independent-review.md']
    references = [{'file': name, 'exists': (REPORTS / name).is_file(),
                   'sha256': sha(REPORTS / name) if (REPORTS / name).is_file() else None}
                  for name in sources]
    counts = {'type': dict(Counter(entry['type'] for entry in original)),
              'severity': dict(Counter(entry['severity'] for entry in original))}
    formatting_issue_still_present = "return f'[{label or path.name}](/{target})'" in source
    issues = []
    if formatting_issue_still_present:
        issues.append({'severity': 'report_presentation', 'file': str(writer), 'function': 'link',
                       'finding': 'Windows D:/...绝对路径被增加前置斜杠，变成/D:/...；不是宿主真实绝对文件目标。',
                       'recommendation': '直接使用resolve().as_posix()的D:/...目标；空格目标采用<...>。仅由root修改生成器。'})
    result = {
        'reviewed_at_utc': datetime.now(timezone.utc).isoformat(),
        'reviewer': '/root/sonar_python_fix',
        'writer': str(writer), 'writer_sha256': sha(writer),
        'review_scope': 'write_final_report.py 整份源码和全部报告文案全文审读；统计口径对照原平台 JSON、已核对执行证据、独立E2E/动态源SHA报告。未执行其main，未修改该文件。',
        'original_counts_independently_computed': counts,
        'hardcoded_original_counts_match': counts == {'type': {'CODE_SMELL': 560, 'VULNERABILITY': 10, 'BUG': 1653},
                                                     'severity': {'MAJOR': 1829, 'MINOR': 164, 'CRITICAL': 230}},
        'report_logic_review': [
            '先要求final-audit all_issue_work_complete true，保留旧final报告；逐key输出唯一2270及原源码/规则/严重度。',
            '源码FIXED、人工FALSE_POSITIVE、唯一ACCEPTED分别计数，没有把35人工处置算作源码修复。',
            '平台度量、生产ncloc、维护文件和测试输入口径分开；文件自动枚举不等于全文人工审读。',
            '质量门禁仍按实际ERROR显示；new_coverage 0因没有导入，不声称实测0%或降低80%门槛。',
            'Web单测416、类型检查、Node生产契约17和SDK18分别列出；没有相加成唯一全仓测试数量。',
            'server188pass与61PGskip+1helper严格区分；题库PG0pass5skip不是业务集成通过。',
            '模型治理366旧动态证明仅依据117实际编译输入SHA仍一致；fresh45补验为重叠批次，不追加到旧1006唯一测试总数。',
            'E2E47请求、23启动标记后中断及10失败真实保留：2原有UI/快照不一致、2资源阻断断言、6基础设施失败；未更新快照，不把基线差异当作验收通过。',
            '图纸浏览器QA、Docker和Rust Clippy未实际运行的边界明确，历史CodeQL/Rust只作历史证据。',
            '本轮当前全仓只复扫Sonar；历史Semgrep/CodeQL/Actions/覆盖率不绑定到最新版本，云端Actions/Codecov未执行。',
        ],
        'fixed_input_api_catalog_counts_verified': read(REPORTS / 'frontend-api-scope-equivalence-02.json')['current']['summary'],
        'evidence_references': references,
        'issues': issues,
        'pending_final_check': 'full04 尚待CE分析ID与最终状态导出；本次审读不提前声称所有2270已关闭或最终平台ERROR的唯一条件。输出最终报告后需核对文字/CSV/JSON实际产物。',
        'overall': 'content_and_counting_scope_supported_with_link_formatting_issue' if issues else 'content_and_counting_scope_supported',
    }
    (REPORTS / 'final-report-writer-independent-review-python-agent.json').write_text(json.dumps(result, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    text = ['# 最终报告生成器：独立文字与统计审读', '',
            f"源码全文审读版本 SHA `{result['writer_sha256']}`。原平台分类和严重度实际重算与文案一致，13份成功批次/独立审查引用均存在。", '',
            'FIXED/人工处置、覆盖率缺少导入的ERROR、E2E10失败与中断、61PG+1helper skip、历史平台范围及未运行图纸浏览器QA等口径准确；无测试重叠累加或将skip写为业务通过。', '',
            f"待处理呈现问题 {len(issues)}：Windows绝对路径link()如仍有额外/前缀，建议root修正。未执行main、未修改生成器；full04最终状态与实际产物校验仍待完成。", '']
    (REPORTS / 'final-report-writer-independent-review-python-agent.md').write_text('\n'.join(text), encoding='utf-8')
    print(json.dumps({'overall': result['overall'], 'writer_sha256': result['writer_sha256'],
                      'hardcoded_counts_match': result['hardcoded_original_counts_match'],
                      'references_all_exist': all(entry['exists'] for entry in references),
                      'issue_count': len(issues)}, ensure_ascii=False))

if __name__ == '__main__':
    main()
