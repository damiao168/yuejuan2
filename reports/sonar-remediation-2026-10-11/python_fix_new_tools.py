"""按已审读业务阶段修复新增架构工具告警；受保护生成物不在此写入。"""
import ast
from pathlib import Path

from python_edit_tools import replace_function

ROOT = Path(__file__).resolve().parents[2]
PREFIX = 'docs/architecture/tools/'


def main():
    replace_function(PREFIX + 'finalize_delivery_records.py', 'main', '''
def append_supplemental_source(modules, suffix, ranges):
    module = suffix.split('/')[0]
    path = 'services/api-gateway/internal/' + suffix
    data = (ROOT / path).read_bytes()
    sha = hashlib.sha256(data).hexdigest()
    count = len(data.decode('utf-8-sig').splitlines())
    if any(end > count for _, end in ranges):
        raise ValueError('抽读范围越界：' + path)
    sources = modules[module]['sources']
    source = next((item for item in sources if item['path'] == path), None)
    # 抽读证据只对应当时的版本；源码变化必须重新审读，不能替换 hash 后沿用旧证据。
    if source and source['sha256'] != sha:
        raise ValueError('已经抽读的源码发生变化：' + path)
    if source is None:
        source = {'path': path, 'sha256': sha, 'read_status': 'partial',
                  'full_file_review': False, 'ranges': [], 'symbols': []}
        sources.append(source)
    for start, end in ranges:
        scope = {'start': start, 'end': end}
        if scope not in source['ranges']:
            source['ranges'].append(scope)


def recheck_worker_sources(pipeline):
    # 两个变化后的 Worker 文件仅核对相关架构边界；依赖及运行行为仍未完整验证。
    worker_sources = {
        'services/ocr-worker/ocr_worker/config.py': '配置加载与校验分组；顺序OCR仅允许batch_size=1，数学服务默认HTTPS；未执行运行验收。',
        'services/math-verification-worker/math_verification_worker/server.py': '符号验证用spawn子进程和预算回收；TLS与显式开发回环例外；未执行服务或数学效果验收。',
    }
    for path, reason in worker_sources.items():
        entry = next((item for item in pipeline['files'] if item['file'] == path), None)
        if entry is None:
            entry = {'file': path}
            pipeline['files'].append(entry)
        entry.update(source_sha256=hashlib.sha256((ROOT / path).read_bytes()).hexdigest(),
                     read_status='partial', analysis_status='pending', status='pending', modified=False, reason=reason)
    pipeline['final_worker_change_recheck_at_utc'] = datetime.now(timezone.utc).isoformat()
    pipeline['counts']['partial'] = sum(item.get('read_status') == 'partial' for item in pipeline['files'])
    pipeline['counts']['reviewed'] = sum(item.get('read_status') == 'complete' for item in pipeline['files'])
    pipeline['counts']['pending'] = len(pipeline['files']) - pipeline['counts']['partial'] - pipeline['counts']['reviewed']


def main():
    manifest = read_json('module-responsibilities.json')
    modules = {entry['module']: entry for entry in manifest['modules']}
    for suffix, ranges in READ_RANGES.items():
        append_supplemental_source(modules, suffix, ranges)
    manifest['supplemental_review'] = '14复杂包补核核心函数体；独立复核与主任务实际读取，仍为partial。'
    manifest['supplemental_checked_at_utc'] = datetime.now(timezone.utc).isoformat()
    (EVIDENCE / 'module-responsibilities.json').write_text(json.dumps(manifest, ensure_ascii=False, indent=2), encoding='utf-8')
    pipeline = read_json('pipeline-reviewed-files.json')
    recheck_worker_sources(pipeline)
    (EVIDENCE / 'pipeline-reviewed-files.json').write_text(json.dumps(pipeline, ensure_ascii=False, indent=2), encoding='utf-8')
    print(json.dumps({'module_source_files': len({s['path'] for m in manifest['modules'] for s in m['sources']}),
                      'worker_changed_files_rechecked': 2}))
''')
    replace_function(PREFIX + 'retire_stale_reviews.py', 'main', '''
def retire_completed_review(entry, manifest_path):
    if entry.get('read_status') != 'complete':
        return None
    source = ROOT / entry.get('file', entry.get('path', ''))
    actual = hashlib.sha256(source.read_bytes()).hexdigest() if source.is_file() else None
    if actual == entry.get('source_sha256'):
        return None
    # 原 hash 和读取范围仍指向旧版本；只撤销完成资格，不能用新 hash 续认旧审读。
    entry['superseded_completed_review'] = {
        'source_sha256': entry.get('source_sha256'),
        'read_status': 'complete',
        'analysis_status': entry.get('analysis_status'),
        'reason': entry.get('reason', entry.get('scope', '')),
        'read_ranges': entry.get('read_ranges'),
    }
    entry.update(read_status='not_manually_reviewed', analysis_status='pending', status='pending',
                 reason='原全文完成版本的源码已变化；当前版本必须重新审读，不继承旧完成声明。',
                 invalidated_at_utc=datetime.now(timezone.utc).isoformat())
    return {'manifest': manifest_path.name, 'file': entry.get('file', entry.get('path'))}


def main():
    retired = []
    for path in sorted(EVIDENCE.glob('*-reviewed-files.json')):
        manifest = json.loads(path.read_text(encoding='utf-8-sig'))
        entries = manifest.get('files', []) if isinstance(manifest, dict) else manifest
        changed = False
        for entry in entries:
            record = retire_completed_review(entry, path)
            if record is not None:
                retired.append(record)
                changed = True
        if changed:
            path.write_text(json.dumps(manifest, ensure_ascii=False, indent=2), encoding='utf-8')
    print(json.dumps({'retired_completion_claims': retired}, ensure_ascii=False))
''')
    replace_function(PREFIX + 'fit_box_typography.py', 'main', '''
def scale_original_layout(page, scale):
    # 重跑时从首次排版前的位置开始；不累乘缩放，不改标签、状态或出处。
    if "typography_layout_base" not in page:
        page["typography_layout_base"] = {
            "width": page.get("width", 1800), "height": page.get("height", 1250),
            "nodes": {n["id"]: {k: n[k] for k in ("x", "y", "w", "h")} for n in page["nodes"]},
            "points": {str(i): edge["points"] for i, edge in enumerate(page.get("edges", [])) if edge.get("points")},
        }
    base = page["typography_layout_base"]
    page["width"], page["height"] = round(base["width"] * scale), round(base["height"] * scale)
    for i, edge in enumerate(page.get("edges", [])):
        if str(i) in base["points"]:
            edge["points"] = [[round(x * scale), round(y * scale)] for x, y in base["points"][str(i)]]
    return base


def fitted_text_width(lines, width, entity, inner_ratio):
    available = (width - 2 * PADDING) * inner_ratio
    if entity:
        title_size, body_size = ENTITY_TITLE, ENTITY_FIELDS
        fitted_width = max(width, text_width(lines[0], title_size, True) + 2 * PADDING)
    else:
        unit = max(1, text_width(lines[0], TITLE_MIN, True))
        title_size = max(TITLE_MIN, min(TITLE_MAX, math.floor(TITLE_MIN * available / unit)))
        body_size = BODY_SIZE
        title_width = text_width(lines[0], title_size, True)
        body_width = min(available, max((text_width(s, body_size) for s in lines[1:]), default=0))
        fitted_width = max(title_width, body_width) / inner_ratio + 2 * PADDING
    return title_size, body_size, fitted_width


def fit_node(node, old, scale):
    x, y, width, height = [old[k] * scale for k in ("x", "y", "w", "h")]
    lines = node.get("display_label", node["label"]).split("\\n")
    kind = node.get("kind", "box")
    if kind == "group":
        node.update(x=round(x), y=round(y), w=round(width), h=round(height), title_font_size=36)
        return None
    entity = kind == "entity"
    inner_ratio = 0.66 if kind == "decision" else 1
    title_size, body_size, fitted_width = fitted_text_width(lines, width, entity, inner_ratio)
    inner_width = (fitted_width - 2 * PADDING) * inner_ratio
    rows = wrapped_rows(lines[1:], body_size, inner_width)
    text_height = title_size * 1.2 + (6 + rows * body_size * 1.26 if rows else 0)
    fitted_height = math.ceil(text_height / inner_ratio + 2 * PADDING)
    # 保持节点中心与分层顺序，使原来的关联方向仍易读；正文需要多行时按行给空间。
    node.update(x=round(x + (width - fitted_width) / 2), y=round(y + (height - fitted_height) / 2),
                w=math.ceil(fitted_width), h=fitted_height, title_font_size=title_size,
                body_font_size=body_size, title_single_line=True, text_align="left" if entity else "center")
    return {"node": node["id"], "title_size": title_size, "body_size": body_size,
            "width": node["w"], "height": node["h"]}


def fit_page(spec, page):
    entity_page = any(n.get("kind") == "entity" for n in page["nodes"])
    scale = 1.5 if entity_page else 1.3
    base = scale_original_layout(page, scale)
    changes = []
    for node in page["nodes"]:
        change = fit_node(node, base["nodes"][node["id"]], scale)
        if change is not None:
            changes.append({"file": spec["file"], "page": page["id"], **change})
    return changes


def main():
    changes = []
    for path in sorted((BASE / "specs").glob("*.json")):
        spec = json.loads(path.read_text(encoding="utf-8-sig"))
        for page in spec["pages"]:
            changes.extend(fit_page(spec, page))
        path.write_text(json.dumps(spec, ensure_ascii=False, indent=2) + "\\n", encoding="utf-8")
    report = {"node_title_range": [TITLE_MIN, TITLE_MAX], "body": BODY_SIZE,
              "entity_title": ENTITY_TITLE, "entity_fields": ENTITY_FIELDS, "nodes": changes}
    (BASE / "evidence/box-typography-layout.json").write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
    print(json.dumps({"nodes": len(changes), "node_title_range": [TITLE_MIN, TITLE_MAX], "body": BODY_SIZE,
                      "entity_title": ENTITY_TITLE, "entity_fields": ENTITY_FIELDS}, ensure_ascii=False))
''')
    path = ROOT / (PREFIX + 'write_frontend_specs.py')
    data = path.read_bytes()
    source = data.decode('utf-8-sig')
    tree = ast.parse(source)
    lines = source.splitlines(keepends=True)
    changes = []
    values = {'services/api-gateway/internal/server/router_guards.go': 'ROUTER_GUARDS_SOURCE',
              'services/api-gateway/internal/files/handlers.go': 'FILE_HANDLERS_SOURCE'}
    for node in ast.walk(tree):
        if isinstance(node, ast.Constant) and node.value in values and node.lineno > 10:
            changes.append((node.lineno - 1, node.col_offset, node.end_col_offset, values[node.value]))
    # Python 的 AST 列偏移按 UTF-8 字节计算；中文行必须按字节替换，不能当字符索引。
    for line, start, end, replacement in sorted(changes, reverse=True):
        encoded = lines[line].encode('utf-8')
        lines[line] = (encoded[:start] + replacement.encode('ascii') + encoded[end:]).decode('utf-8')
    updated = ''.join(lines)
    ast.parse(updated)
    path.write_bytes((b'\xef\xbb\xbf' if data.startswith(b'\xef\xbb\xbf') else b'') + updated.encode('utf-8'))
    print('four owned generators updated; standalone path replacements', len(changes))


if __name__ == '__main__':
    main()
