"""核对字号更新后的原生图纸与旧版内容，验证范围不扩大到业务源码重新审计。"""
import hashlib
import json
from datetime import datetime, timezone
from pathlib import Path

from defusedxml import ElementTree as safe_xml

BASE = Path(__file__).resolve().parents[1]


def main():
    before = json.loads((BASE/'evidence/typography-before.json').read_text(encoding='utf-8'))
    native = json.loads((BASE/'evidence/native-render-validation.json').read_text(encoding='utf-8'))
    rendered = {entry['file']: entry['source_sha256'] for entry in native['inputs']}
    errors, files, content_errors = [], [], []
    for original in before:
        path = BASE/'diagrams'/original['file']
        sha = hashlib.sha256(path.read_bytes()).hexdigest()
        pages = []
        # 字号核对不需要 DTD 或自定义实体；解析拒绝时停止，不覆盖此前验收结果。
        for page in safe_xml.parse(path, forbid_dtd=True).getroot().findall('diagram'):
            cells = [{key: cell.get(key) for key in ['id','details','evidence','source','target','vertex','edge','implementationStatus']}
                     for cell in page.iter('mxCell') if cell.get('details') is not None]
            pages.append({'id': page.get('id'), 'cells': cells})
        if pages != original['pages']:
            content_errors.append('内容或关系变化：'+original['file'])
        if rendered.get(path.name) != sha:
            errors.append('原生渲染未覆盖当前XML：'+path.name)
        files.append({'file': path.name, 'sha256': sha, 'pages': len(pages)})
    errors.extend(content_errors)
    for page in native['results']:
        if page['dangling'] or page['overflow'] or page['obstructions']:
            errors.append('原生排版检查失败：'+page['file']+'/'+page['id'])
    static = json.loads((BASE/'evidence/diagram-validation.json').read_text(encoding='utf-8'))
    errors.extend(static['errors'])
    specs = [json.loads(path.read_text(encoding='utf-8-sig')) for path in (BASE/'specs').glob('*.json')]
    nodes = [n for spec in specs for page in spec['pages'] for n in page['nodes']]
    titles = [n['title_font_size'] for n in nodes if n.get('kind') not in ('entity','group')]
    report = {
        'checked_at_utc': datetime.now(timezone.utc).isoformat(), 'passed': not errors, 'errors': errors,
        'files': files, 'pages': sum(entry['pages'] for entry in files), 'content_and_relations_preserved': not content_errors,
        'font_sizes': {'node_title_range': [min(titles),max(titles)], 'entity_title': 34, 'body': 32, 'entity_fields': 26,
                       'edge_labels': 26, 'group_title': 36, 'legend': 24, 'page_title': 48},
        'source_audit': '保留原审计时点；本次仅显示文案/字号/紧框排版/预览缩放，不声明业务源码重新审计或运行验收',
    }
    (BASE/'evidence/typography-validation.json').write_text(json.dumps(report,ensure_ascii=False,indent=2),encoding='utf-8')
    print(json.dumps({'passed':not errors,'files':len(files),'pages':report['pages'],'errors':errors},ensure_ascii=False))
    return bool(errors)


if __name__ == '__main__':
    raise SystemExit(main())
