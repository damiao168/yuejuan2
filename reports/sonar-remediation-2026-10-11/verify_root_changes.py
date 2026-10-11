"""核对HTML改动与工作区基线；不把源码核验记为Sonar平台关闭。"""
from collections import Counter
from pathlib import Path
import datetime as dt
import hashlib
import json
import re

ROOT = Path(__file__).resolve().parents[2]
OUT = Path(__file__).resolve().parent
BACKUP = Path('D:/tools/sonar-remediation-20261011/baseline')
HTML = [
    'docs/architecture/previews/index.html',
    'frontend-review-report/前端UI优化方案-对标成熟产品.html',
    'frontend-review-report/前端UI设计分析.html',
    'frontend-review-report/前端代码审查报告.html',
]


def contrast(foreground, background):
    def luminance(color):
        channels = [int(color[i:i + 2], 16) / 255 for i in (0, 2, 4)]
        linear = [c / 12.92 if c <= 0.04045 else ((c + 0.055) / 1.055) ** 2.4 for c in channels]
        return sum(a * b for a, b in zip(linear, (0.2126, 0.7152, 0.0722)))
    a, b = sorted((luminance(foreground), luminance(background)))
    return (b + 0.05) / (a + 0.05)


def main():
    baseline = (BACKUP / HTML[0]).read_bytes()
    current = (ROOT / HTML[0]).read_bytes()
    reverse = current.replace(b"font-family: 'Microsoft YaHei',sans-serif;", b"font-family: 'Microsoft YaHei';")
    reverse = reverse.replace('<select id="picker" aria-label="选择架构图纸">'.encode(), b'<select id="picker">')
    reverse = reverse.replace(b'show(Math.max(0,target));', b'show(target<0?0:target);')
    reverse = reverse.replace(b'Number.parseFloat(getComputedStyle(drawing).maxHeight)', b'parseFloat(getComputedStyle(drawing).maxHeight)')
    reverse = reverse.replace(b"if(updateHash){history.replaceState(null,'','#'+sheets[i].id);}", b"if(updateHash)history.replaceState(null,'','#'+sheets[i].id);")
    assert reverse == baseline, 'Generated preview differs beyond the five intended edits'
    assert current.count(b"font-family: 'Microsoft YaHei',sans-serif;") == 1645
    assert current.count(b"font-family: 'Microsoft YaHei';") == 0
    original_svgs = re.findall(rb'<svg\b.*?</svg>', baseline, re.DOTALL)
    current_svgs = re.findall(rb'<svg\b.*?</svg>', current, re.DOTALL)
    assert len(original_svgs) == len(current_svgs) == 70
    for original, svg in zip(original_svgs, current_svgs):
        assert svg.replace(b"font-family: 'Microsoft YaHei',sans-serif;", b"font-family: 'Microsoft YaHei';") == original
    ratio = contrast('e7eef9', '2d476c')
    assert ratio > 4.5
    sri = json.loads((OUT / 'echarts-sri-verification.json').read_text(encoding='utf-8'))
    for relative in HTML[1:]:
        updated = (ROOT / relative).read_bytes()
        old = (BACKUP / relative).read_bytes()
        restored = updated.replace(b'.meta-chip{background:#2d476c;', b'.meta-chip{background:rgba(255,255,255,.1);')
        if relative.endswith('前端代码审查报告.html'):
            url = sri['cdn_url']
            restored = restored.replace(f'<script src="{url}" integrity="{sri["integrity_attribute"]}" crossorigin="anonymous"></script>'.encode(), f'<script src="{url}"></script>'.encode())
            updated_fallback = re.search(rb'  // [^\r\n]+\r?\n  if\(!el\|\|typeof echarts==="undefined"\)\{.*?\r?\n  \}', restored, re.DOTALL)
            assert updated_fallback
            old_fallback = re.search(rb'  if\(!el\|\|typeof echarts==="undefined"\)\{[^\r\n]+', old).group()
            restored = restored[:updated_fallback.start()] + old_fallback + restored[updated_fallback.end():]
        assert restored == old, f'Unexpected HTML change: {relative}'
    source = json.loads((ROOT / 'reports/platform-audit-2026-10-10/sonar/issues.json').read_text(encoding='utf-8'))
    states = []
    changes = {
        'css:S4649': '修改权威生成器并正常生成，为1645个原生SVG内嵌字体栈增加sans-serif；SVG尺寸与其余字节保持一致。',
        'Web:InputWithoutLabelCheck': '生成器为图纸选择器补充明确中文可访问名称。',
        'javascript:S7766': '生成器使用Math.max表达深链接未匹配时的第0页回退。',
        'css:S7924': f'标签背景采用深色实色#2d476c；#e7eef9文字对比度{ratio:.3f}:1。',
        'Web:S5725': '固定ECharts5.5.1的CDN字节与npm发行包完全一致，补充sha384 SRI与crossorigin=anonymous。',
        'javascript:S2681': '给图表缺失分支增加显式花括号，保持容器缺失/脚本失败的原有初始化退出顺序。',
    }
    for issue in source['issues']:
        path = issue['component'].split(':', 1)[1]
        if path in HTML:
            states.append({
                'key': issue['key'], 'rule': issue['rule'], 'severity': issue['severity'],
                'path': path, 'original_line': issue.get('line'),
                'state': 'changed_awaiting_sonar', 'change': changes[issue['rule']],
                'evidence': ['root-validation.json', 'echarts-sri-verification.json'] if path == HTML[-1] else ['root-validation.json'],
            })
    assert len(states) == 1652
    validations = {
        'verified_at': dt.datetime.now().astimezone().isoformat(),
        'generated_preview_reverses_to_workspace_baseline_exactly': True,
        'font_declarations_fixed': 1645,
        'native_svg_pages_unchanged_except_embedded_font_stack': 70,
        'standalone_report_reverses_to_workspace_baseline_exactly': 3,
        'metadata_chip_contrast_ratio': ratio,
        'cdn_distribution_and_integrity_verified': True,
        'browser': {
            'cli_session': 'sonar-remediation-preview',
            'pages_and_options': 70,
            'picker_accessible_name': '选择架构图纸',
            'next_page_hash': 'p1',
            'last_page_deep_link': 'p69',
            'last_page_150_percent': {'width': 3466.5, 'height': 3031.5},
            'console': 'Only favicon.ico404 observed in preview session',
            'font_note': '浏览器当前会话跨同URL锚点导航没有重新请求HTML；字体修复按当前生成HTML与基线字节校验，浏览器重新加载验证待执行。',
            'standalone_reports': 'pending browser validation',
            'owned_browser_and_http_server_closed': True,
        },
        'platform_rescan': 'pending',
        'targeted_html_scan_02': {'status': 'SUCCESS', 'files': 4, 'original_rules_absent': 1652, 'additional_js_findings': 2, 'evidence': 'html-validation-02/issues.json', 'original_project_issue_keys': 'pending full repository rescan'},
        'files': [{'path': path, 'sha256': hashlib.sha256((ROOT / path).read_bytes()).hexdigest()} for path in HTML],
    }
    (OUT / 'root-validation.json').write_text(json.dumps(validations, ensure_ascii=False, indent=2), encoding='utf-8')
    files = {path: {
        'file_review_status': 'protected' if path == HTML[0] else 'pending',
        'review_scope': '仅按权威生成器正常生成；核对70份SVG嵌入与维护边界。' if path == HTML[0] else '已审读告警CSS/脚本及相关结构；未完成整个历史报告的维护审读。',
    } for path in HTML}
    files['docs/architecture/tools/build_preview.py'] = {'file_review_status': 'annotated', 'review_scope': '整文件和图纸维护说明已审读；字体栈补齐仅作用于内嵌副本，保留原生SVG。'}
    (OUT / 'root-progress.json').write_text(json.dumps({
        'updated_at': validations['verified_at'], 'original_count': 1652,
        'issue_states': states, 'files': files, 'validations': ['root-validation.json'],
        'summary': {'changed_awaiting_sonar': 1652, 'platform_confirmed': 0},
    }, ensure_ascii=False, indent=2), encoding='utf-8')
    (OUT / 'root-new-findings.json').write_text(json.dumps({
        'source': 'html-validation-02/issues.json',
        'scope_note': '额外两项已在权威生成器修订并正常生成；同规则全仓及HTML复验仍待确认。',
        'findings': [
            {'key': 'cb72da98-d338-447a-aa79-4399b8ed8166', 'rule': 'javascript:S7773', 'state': 'changed_awaiting_sonar', 'change': '使用Number.parseFloat读取缩放容器的maxHeight。'},
            {'key': '7896c681-434e-4ce0-b3e6-94aec494f659', 'rule': 'javascript:S2681', 'state': 'changed_awaiting_sonar', 'change': '明确updateHash花括号范围；回到顶部仍在每次show之后执行。'},
        ],
    }, ensure_ascii=False, indent=2), encoding='utf-8')
    print(json.dumps({'root_issues_changed': len(states), 'static_validation': 'passed', 'sonar_confirmed': 0, 'contrast': round(ratio, 3)}))


if __name__ == '__main__':
    main()
