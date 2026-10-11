"""交付验收检查：检验当前原生图纸、证据和链接，不把动态/全仓审读未执行包装成通过。"""
import ast
import csv
import hashlib
import html
import json
import re
from datetime import datetime, timezone
from pathlib import Path

from build_diagrams import label, validate_evidence
from defusedxml import ElementTree as safe_xml

BASE = Path(__file__).resolve().parents[1]
REPO = BASE.parents[1]
EXPECTED = [
    '01-feature-tree.drawio', '02-system-architecture.drawio',
    '03-module-dependencies.drawio', '04-database-er.drawio',
    '05-business-workflow.drawio', '06-image-template-pipeline.drawio',
    '07-ai-grading.drawio', '08-api-data-flow.drawio',
    '09-task-state-machine.drawio', '10-security-deployment.drawio',
]

def read(path):
    return json.loads(path.read_text(encoding='utf-8-sig'))

def validate_node(name, page, node, cells, errors):
    cell = cells.get(node['id'])
    value = html.escape(node.get('display_label', node['label'])) if node.get('kind') == 'group' else label(node)
    if cell is None or cell.get('details') != node['label'] or cell.get('value') != value or cell.get('evidence') != str(node.get('evidence', '')):
        errors.append(f'spec/XML node mismatch: {name}/{page["id"]}/{node["id"]}')
    validate_evidence(node.get('evidence', ''), f'{name}/{page["id"]}/{node["id"]}', errors)


def validate_edge(name, page, index, edge, cells, errors):
    cell = cells.get(f'__e{index}')
    value = html.escape(edge.get('display_label', edge.get('label', '')))
    if cell is None or cell.get('source') != edge['source'] or cell.get('target') != edge['target'] or cell.get('details') != edge.get('label', '') or cell.get('value') != value or cell.get('evidence') != str(edge.get('evidence', '')):
        errors.append(f'spec/XML edge mismatch: {name}/{page["id"]}/{index}')
    validate_evidence(edge.get('evidence', ''), f'{name}/{page["id"]}/edge-{index}', errors)


def validate_spec_pages(name, xml, spec_pages, errors):
    if [page.get('id') for page in xml.findall('diagram')] != [page['id'] for page in spec_pages]:
        errors.append('spec/XML page mismatch: ' + name)
    xml_pages = {page.get('id'): page for page in xml.findall('diagram')}
    for page in spec_pages:
        xml_page = xml_pages.get(page['id'])
        if xml_page is None:
            continue
        cells = {cell.get('id'): cell for cell in xml_page.iter('mxCell')}
        for node in page['nodes']:
            validate_node(name, page, node, cells, errors)
        for index, edge in enumerate(page.get('edges', [])):
            validate_edge(name, page, index, edge, cells, errors)


def validate_diagram(name, specs, rendered_hashes, errors):
    path = BASE / 'diagrams' / name
    if not path.is_file():
        errors.append('missing diagram: ' + name)
        return None
    # 只接受不含 DTD 的原生图纸；实体或外部引用被拒绝时终止验收，不能继续给出通过结果。
    xml = safe_xml.parse(path, forbid_dtd=True).getroot()
    if xml.tag != 'mxfile':
        errors.append('not native mxfile: ' + name)
    count = len(xml.findall('diagram'))
    if not count or not all(page.find('mxGraphModel/root') is not None for page in xml.findall('diagram')):
        errors.append('missing editable model: ' + name)
    if any('shape=image' in cell.get('style', '') for cell in xml.iter('mxCell')):
        errors.append('unexpected raster replacement: ' + name)
    # 旧渲染只证明旧输入版本；规格和当前 XML 必须逐页、节点和边匹配。
    validate_spec_pages(name, xml, specs.get(name, {}).get('pages', []), errors)
    sha = hashlib.sha256(path.read_bytes()).hexdigest()
    if rendered_hashes.get(name) != sha:
        errors.append('native render hash not current: ' + name)
    return {'file': name, 'pages': count, 'sha256': sha}


def validate_native_report(native, pages, errors):
    if sorted(path.name for path in (BASE / 'diagrams').glob('*.drawio')) != EXPECTED:
        errors.append('diagram filenames differ from ten required files')
    if native['files'] != 10 or native['pages'] != pages:
        errors.append('native page/file coverage mismatch')
    for page in native['results']:
        if page['dangling'] or page['overflow'] or page['obstructions']:
            errors.append('native geometry failed: ' + page['file'] + '/' + page['id'])
        if not (BASE / page['preview']).is_file():
            errors.append('missing SVG: ' + page['preview'])
    if read(BASE / 'evidence/diagram-validation.json')['errors']:
        errors.append('static diagram validation failed')
    if not read(BASE / 'evidence/database-validation.json')['passed']:
        errors.append('database documentation cross-check failed')


def validate_document_links(doc, errors):
    # 相对链接的源码行号不参与路径判断；URL 和页内锚点不检查文件存在性。
    for value in re.findall(r'\]\(([^)]+)\)', doc.read_text(encoding='utf-8-sig')):
        value = value.strip('<>').split('#')[0]
        if not value or re.match(r'[A-Za-z]+://', value):
            continue
        value = re.sub(r':\d+$', '', value)
        if not (doc.parent / value).exists():
            errors.append(f'{doc.relative_to(BASE)} missing link: {value}')


def validate_documents(errors):
    required = ['README.md', 'ARCHITECTURE_REVIEW.md', 'DIAGRAM_MAINTENANCE.md', 'MODULE_CATALOG.md']
    for name in required:
        if not (BASE / name).is_file():
            errors.append('missing document: ' + name)
    for doc in list(BASE.glob('*.md')) + list((BASE / 'evidence').glob('*.md')):
        # 历史说明不纳入本任务的新文档链接规则。
        if doc.parent == BASE and doc.name not in required:
            continue
        validate_document_links(doc, errors)
    for script in (BASE / 'tools').glob('*.py'):
        ast.parse(script.read_text(encoding='utf-8-sig'), filename=str(script))


def validate_review_versions(coverage, errors):
    if coverage['rejected_completion_claims']:
        errors.append('stale manual review claims: ' + str(len(coverage['rejected_completion_claims'])))
    # 哈希检查只核验已有完成声明，不把 pending 自动提升为已审读。
    with (BASE / 'evidence/file-review.tsv').open(encoding='utf-8-sig', newline='') as stream:
        for row in csv.DictReader(stream, delimiter='\t'):
            if row['read_status'] != 'complete':
                continue
            source = REPO / row['path']
            if not source.is_file() or hashlib.sha256(source.read_bytes()).hexdigest() != row['source_sha256']:
                errors.append('current source invalidates complete review: ' + row['path'])


def main():
    errors, files, pages = [], [], 0
    specs = {spec['file']: spec for spec in (read(path) for path in (BASE / 'specs').glob('*.json'))}
    native = read(BASE / 'evidence/native-render-validation.json')
    rendered_hashes = {entry['file']: entry['source_sha256'] for entry in native.get('inputs', [])}
    for name in EXPECTED:
        result = validate_diagram(name, specs, rendered_hashes, errors)
        if result is not None:
            files.append(result)
            pages += result['pages']
    validate_native_report(native, pages, errors)
    validate_documents(errors)
    coverage = read(BASE / 'evidence/audit-coverage.json')
    validate_review_versions(coverage, errors)
    report = {
        'checked_at_utc': datetime.now(timezone.utc).isoformat(), 'artifact_checks_passed': not errors,
        'errors': errors, 'diagrams': files, 'total_pages': pages,
        'current_spec_and_evidence_checks': 'page/node/edge labels, details, source paths and line bounds checked; does not prove business behavior',
        'full_file_review': {'reviewed': coverage['reviewed'], 'pending': coverage['pending'], 'status': 'pending'},
        'original_workspace': read(BASE / 'evidence/workspace-preservation.json'),
        'runtime_verification': 'not_executed; see acceptance-report.md for limits',
    }
    (BASE / 'evidence/delivery-validation.json').write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding='utf-8')
    print(json.dumps({'artifact_checks_passed': not errors, 'total_pages': pages, 'errors': errors}, ensure_ascii=False))
    return bool(errors)

if __name__=='__main__':
    raise SystemExit(main())
