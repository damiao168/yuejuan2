"""将审读后的图纸规格生成可编辑 mxGraph XML；只写 architecture 下的文档产物。"""
import csv
import html
import json
import re
import xml.etree.ElementTree as ET
from pathlib import Path

from defusedxml import ElementTree as safe_xml

BASE = Path(__file__).resolve().parents[1]
REPO = BASE.parents[1]
COLORS = {
    "implemented": ("#E9F6EE", "#38835B", "已实现／有代码依据"),
    "partial": ("#FFF4D9", "#AD7D24", "部分实现／接入有限"),
    "planned": ("#F1F2F4", "#969DA7", "仅规划"),
    "issue": ("#FDECEF", "#B64857", "已确认问题"),
    "infra": ("#EAF2FF", "#5278B0", "基础设施／外部服务"),
    "unknown": ("#F5F5F6", "#8C94A0", "待确认"),
}
FONT = "Microsoft YaHei"
CLOSE_DIV = "</div>"
DASHED_STYLE = "dashed=1;"
# 原生图纸使用较大字号；预览缩放由阅读器控制，不能靠缩小正文来塞进节点。
NODE_TITLE_SIZE = 40
ENTITY_TITLE_SIZE = 34
BODY_SIZE = 32
ENTITY_FIELD_SIZE = 26
EDGE_SIZE = 26

def display_ref(ref):
    # 图内显示文件名，完整仓库路径保留在XML属性和出处目录中，避免长路径挤压正文。
    chunks = re.split(r"[;；\n]+", str(ref))
    return " · ".join(c.strip().replace("\\", "/").rsplit("/", 1)[-1] for c in chunks if c.strip())

def label(node):
    lines = node.get("display_label", node["label"]).split("\n")
    title_size = node.get("title_font_size", ENTITY_TITLE_SIZE if node.get("kind") == "entity" else NODE_TITLE_SIZE)
    title_wrap = "white-space:nowrap" if node.get("title_single_line") else "word-break:break-all"
    text = f'<div style="font-size:{title_size}px;font-weight:bold;line-height:1.2;{title_wrap}">' + html.escape(lines[0]) + CLOSE_DIV
    if len(lines) > 1:
        size = node.get("body_font_size", ENTITY_FIELD_SIZE if node.get("kind") == "entity" else BODY_SIZE)
        text += f'<div style="font-size:{size}px;line-height:1.26;margin-top:6px;word-break:break-all">' + '<br>'.join(html.escape(s) for s in lines[1:]) + CLOSE_DIV
    if node.get("evidence") and node.get("show_evidence", True):
        text += '<div style="font-size:14px;color:#526070;line-height:1.25;margin-top:8px">' + html.escape(display_ref(node["evidence"])) + CLOSE_DIV
    return text

def add_vertex(root, id_, value, x, y, w, h, style, **attrs):
    cell = ET.SubElement(root, "mxCell", id=id_, value=value, style=style, vertex="1", parent="1", **attrs)
    ET.SubElement(cell, "mxGeometry", x=str(x), y=str(y), width=str(w), height=str(h), attrib={"as": "geometry"})
    return cell

def overlap(a, b):
    return min(a["x"] + a["w"], b["x"] + b["w"]) - max(a["x"], b["x"]) > 1 and min(a["y"] + a["h"], b["y"] + b["h"]) - max(a["y"], b["y"]) > 1

def validate_evidence(ref, location, errors):
    for raw in re.split(r"[;；\n]+", str(ref)):
        raw = raw.strip()
        if not raw:
            continue
        match = re.fullmatch(r"([^:]+)(?::(\d+)(?:[–-]\d+)?)?", raw)
        if not match:
            errors.append(f"{location}: unparseable evidence {raw}")
            continue
        path, line = match.groups()
        target = REPO / path
        if not target.exists():
            errors.append(f"{location}: missing evidence {raw}")
        elif line and target.is_file():
            count = len(target.read_text(encoding="utf-8-sig", errors="replace").splitlines())
            if int(line) > count or int(line) < 1:
                errors.append(f"{location}: evidence line outside file {raw}")

def node_style(node):
    status, kind = node.get('status', 'implemented'), node.get('kind', 'box')
    fill, stroke, _ = COLORS[status]
    align = node.get('text_align', 'left')
    style = f"rounded=1;arcSize=12;html=1;whiteSpace=wrap;align={align};verticalAlign=middle;spacing=12;fontFamily={FONT};fontSize={BODY_SIZE};fontColor=#25364B;fillColor={fill};strokeColor={stroke};strokeWidth=1.5;"
    if kind == 'decision':
        style += 'shape=rhombus;align=center;spacing=5;rounded=0;'
    elif kind == 'group':
        style = f"rounded=1;html=1;whiteSpace=wrap;fillColor=#F7F9FC;strokeColor=#CAD4E2;{DASHED_STYLE}align=left;verticalAlign=top;spacing=12;fontFamily={FONT};fontSize=36;fontStyle=1;fontColor=#55677E;"
    elif kind == 'note':
        style += DASHED_STYLE
    if status in ('planned', 'unknown'):
        style += DASHED_STYLE
    return style


def draw_nodes(root, spec, page, width, height, errors, provenance):
    ids = set()
    location = f'{spec["file"]}/{page["id"]}'
    for node in page['nodes']:
        if node['id'] in ids:
            errors.append(f'{location}: duplicate {node["id"]}')
        ids.add(node['id'])
        validate_evidence(node.get('evidence', ''), f'{location}/{node["id"]}', errors)
        if node['x'] < 0 or node['y'] < 130 or node['x'] + node['w'] > width or node['y'] + node['h'] > height - 80:
            errors.append(f'{location}: outside content area {node["id"]}')
        status = node.get('status', 'implemented')
        value = html.escape(node.get('display_label', node['label'])) if node.get('kind') == 'group' else label(node)
        add_vertex(root, node['id'], value, node['x'], node['y'], node['w'], node['h'], node_style(node), evidence=str(node.get('evidence', '')), details=node['label'], implementationStatus=status)
        provenance.append([spec['file'], page['title'], node['id'], node['label'].split('\n')[0], status, node.get('evidence', '')])
    return ids


def validate_overlaps(spec, page, errors):
    nodes = [node for node in page['nodes'] if node.get('kind') != 'group']
    for index, first in enumerate(nodes):
        for second in nodes[index + 1:]:
            if overlap(first, second):
                errors.append(f'{spec["file"]}/{page["id"]}: overlap {first["id"]}/{second["id"]}')


def edge_style(edge):
    stroke = COLORS[edge.get('status', 'infra')][1]
    style = f"edgeStyle=orthogonalEdgeStyle;rounded=0;orthogonalLoop=1;jettySize=auto;html=1;endArrow=block;endFill=1;strokeColor={stroke};strokeWidth=1.3;fontFamily={FONT};fontSize={EDGE_SIZE};fontColor=#435671;labelBackgroundColor=#FFFFFF;"
    if edge.get('dashed'):
        style += DASHED_STYLE
    for key in ('exitX', 'exitY', 'entryX', 'entryY'):
        if key in edge:
            style += f'{key}={edge[key]};'
    return style


def draw_edges(root, spec, page, ids, errors, provenance):
    for index, edge in enumerate(page.get('edges', [])):
        validate_evidence(edge.get('evidence', ''), f'{spec["file"]}/{page["id"]}/edge-{index}', errors)
        if edge['source'] not in ids or edge['target'] not in ids:
            errors.append(f'{spec["file"]}/{page["id"]}: dangling edge {edge}')
        cell = ET.SubElement(root, 'mxCell', id=f'__e{index}', value=html.escape(edge.get('display_label', edge.get('label', ''))), style=edge_style(edge), edge='1', parent='1', source=edge['source'], target=edge['target'], evidence=str(edge.get('evidence', '')), details=edge.get('label', ''))
        geometry = ET.SubElement(cell, 'mxGeometry', relative='1', attrib={'as': 'geometry'})
        if edge.get('points'):
            points = ET.SubElement(geometry, 'Array', attrib={'as': 'points'})
            for x, y in edge['points']:
                ET.SubElement(points, 'mxPoint', x=str(x), y=str(y))
        if edge.get('evidence'):
            provenance.append([spec['file'], page['title'], f'edge-{index}', edge.get('label', ''), edge.get('status', 'infra'), edge['evidence']])


def draw_legend(root, compact, width, height):
    for index, (status, (fill, stroke, caption)) in enumerate(COLORS.items()):
        if compact:
            caption = {'implemented':'已实现','partial':'部分接入','planned':'规划','issue':'问题','infra':'基础设施','unknown':'待确认'}[status]
        x = 40 + index * ((width - 80) / 6)
        add_vertex(root, f'__legend{index}', html.escape(caption), x, height - 62, (width - 100) / 6, 38, f'rounded=1;html=1;fillColor={fill};strokeColor={stroke};fontFamily={FONT};fontSize=24;fontColor=#405065;')


def draw_page(doc, spec, page, index, errors, info, provenance):
    width, height = page.get('width', 1800), page.get('height', 1250)
    diagram = ET.SubElement(doc, 'diagram', id=page['id'], name=page['title'])
    model = ET.SubElement(diagram, 'mxGraphModel', dx=str(width), dy=str(height), grid='1', gridSize='10', guides='1', tooltips='1', connect='1', arrows='1', fold='1', page='1', pageScale='1', pageWidth=str(width), pageHeight=str(height), math='0', shadow='0', background='#FFFFFF')
    root = ET.SubElement(model, 'root')
    ET.SubElement(root, 'mxCell', id='0')
    ET.SubElement(root, 'mxCell', id='1', parent='0')
    plain = f'text;html=1;align=left;verticalAlign=middle;whiteSpace=wrap;rounded=0;fontFamily={FONT};fontColor=#25364B;'
    page_title = re.sub(r'^\d{2}\s+', '', page['title'])
    add_vertex(root, '__title', html.escape(f'{spec["file"][:2]} / {index:02d}  {page_title}'), 40, 24, width - 80, 64, plain + 'fontSize=48;fontStyle=1;')
    note = page.get('display_note', page.get('note', '当前工作区静态审计 · 2026-10-10 · 代码路径见节点属性及 evidence/diagram-provenance.tsv'))
    add_vertex(root, '__note', html.escape(note), 40, 96, width - 80, 48, plain + 'fontSize=26;fontColor=#617084;')
    ids = draw_nodes(root, spec, page, width, height, errors, provenance)
    validate_overlaps(spec, page, errors)
    draw_edges(root, spec, page, ids, errors, provenance)
    draw_legend(root, spec.get('compact'), width, height)
    info.append({'file': spec['file'], 'page': page['title'], 'nodes': len(page['nodes']), 'edges': len(page.get('edges', [])), 'width': width, 'height': height})


def build():
    (BASE / 'diagrams').mkdir(exist_ok=True)
    (BASE / 'evidence').mkdir(exist_ok=True)
    errors, info, provenance = [], [], []
    for specfile in sorted((BASE / 'specs').glob('*.json')):
        spec = json.loads(specfile.read_text(encoding='utf-8-sig'))
        doc = ET.Element('mxfile', host='app.diagrams.net', agent='yuejuan architecture audit', version='24.7.17', type='device', compressed='false')
        for index, page in enumerate(spec['pages'], 1):
            draw_page(doc, spec, page, index, errors, info, provenance)
        ET.indent(doc, space='  ')
        ET.ElementTree(doc).write(BASE / 'diagrams' / spec['file'], encoding='utf-8', xml_declaration=True)
        # 图纸无需 DTD 或自定义实体；回读时拒绝这些声明，避免被替换的文件触发实体展开。
        # 磁盘回读只证明原生 XML 完整，不代表业务或视觉验收。
        safe_xml.parse(BASE / 'diagrams' / spec['file'], forbid_dtd=True)
    with (BASE / 'evidence/diagram-provenance.tsv').open('w', encoding='utf-8-sig', newline='') as stream:
        writer = csv.writer(stream, delimiter='\t')
        writer.writerow(['diagram', 'page', 'object', 'label', 'status', 'evidence'])
        writer.writerows(provenance)
    report = {'files': len({page['file'] for page in info}), 'pages': len(info), 'errors': errors, 'details': info}
    (BASE / 'evidence/diagram-validation.json').write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding='utf-8')
    print(json.dumps({'files': report['files'], 'pages': report['pages'], 'errors': errors}, ensure_ascii=False))
    return bool(errors)

if __name__ == "__main__":
    raise SystemExit(build())
