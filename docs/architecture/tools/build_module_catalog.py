"""生成Go内部包职责与静态依赖目录；保留人工证据，不改变逐文件审读状态。"""
from pathlib import Path
from collections import defaultdict
import json
import hashlib

ROOT = Path(__file__).resolve().parents[3]
EVIDENCE = ROOT / 'docs/architecture/evidence'
PREFIX = 'services/api-gateway/internal/'


def validate_responsibility_source(module, source):
    path = source['path']
    if not path.startswith(PREFIX + module + '/') or not path.endswith('.go'):
        raise ValueError(f'{module} 的职责依据必须来自本包 Go 源码。')
    data = (ROOT / path).read_bytes()
    if hashlib.sha256(data).hexdigest() != source['sha256']:
        raise ValueError(f'{path} 已变化；先复核职责和抽读范围，再更新证据 SHA。')
    line_count = len(data.decode('utf-8-sig').splitlines())
    # 职责抽读不能因目录重建自动变成全文件审读。
    if source.get('read_status') != 'partial' or source.get('full_file_review') is not False:
        raise ValueError(f'{path} 的职责抽读不能转为全文件完成。')
    for scope in source['ranges']:
        if not 1 <= scope['start'] <= scope['end'] <= line_count:
            raise ValueError(f'{path} 的抽读范围超出当前源码。')


def primary_source_is_reviewed(entry):
    primary = entry['primary_source']
    for source in entry['sources']:
        if source['path'] != primary['path']:
            continue
        if any(scope['start'] <= primary['line'] <= scope['end'] for scope in source['ranges']):
            return True
    return False


def load_responsibilities(modules):
    """源码变化时停止重建，避免继续发布已经失效的职责说明。"""
    manifest = json.loads((EVIDENCE / 'module-responsibilities.json').read_text('utf-8-sig'))
    entries = manifest['modules']
    responsibilities = {entry['module']: entry for entry in entries}
    if len(responsibilities) != len(entries) or set(responsibilities) != set(modules):
        raise ValueError('职责清单必须与模块索引逐包对应，不能缺项或重复。')
    for module, entry in responsibilities.items():
        if not entry.get('responsibility', '').strip() or not entry.get('sources'):
            raise ValueError(f'{module} 缺少职责或源码依据。')
        for source in entry['sources']:
            validate_responsibility_source(module, source)
        if not primary_source_is_reviewed(entry):
            raise ValueError(f'{module} 的主要出处必须在已抽读范围内。')
    return responsibilities


def main():
    catalog = json.loads((EVIDENCE / 'module-imports.json').read_text('utf-8-sig'))
    inventory = json.loads((EVIDENCE / 'file-inventory.json').read_text('utf-8-sig'))
    files = defaultdict(list)
    for entry in inventory:
        path = entry['path']
        if path.startswith(PREFIX) and path.endswith('.go'):
            files[path.split('/')[3]].append(path)
    edges = defaultdict(lambda: defaultdict(list))
    for edge in catalog['imports']:
        edges[edge['source']][edge['target']].append(edge)
    modules = sorted(catalog['modules'])
    responsibilities = load_responsibilities(modules)
    lines = [
        '# Go 内部模块职责与依赖', '',
        f'列出 API Gateway 的 {len(modules)} 个 Go 内部包，每包一句职责及直接依赖。', '',
        '职责依据核心函数体与调用抽读，范围和当前 SHA 保存在 '
        '[module-responsibilities.json](evidence/module-responsibilities.json)。'
        '这只证明职责说明的依据，不代表包内源码全部审读或功能完整上线。', '',
        '依据 [module-imports.json](evidence/module-imports.json) 与原始文件清单；扫描只读取非测试Go源码的import字符串。'
        '下表显示直接依赖，不把依赖包的依赖算成当前包的直接依赖；标准库、第三方依赖、其他语言和动态调用不在这个索引范围。', '',
        '**这是静态目录，不表示表中所有源码都已完成人工审读。**逐文件状态见 '
        '[file-review.tsv](evidence/file-review.tsv) 和 [audit-coverage.json](evidence/audit-coverage.json)。'
        '抽读文件仍保留原逐文件状态；未发现静态 import 环路不代表没有运行时耦合。', '',
        '| Go包与源码目录 | 职责 | Go源码文件数（含测试） | 直接内部依赖 | import证据位置数 |',
        '| --- | --- | ---: | --- | ---: |',
    ]
    for module in modules:
        dependencies = sorted(edges[module])
        directory = '../../' + PREFIX + module
        dependency_links = ', '.join(f'[{target}]({"../../" + PREFIX + target})' for target in dependencies) or '无扫描到的直接内部import'
        entry = responsibilities[module]
        primary = entry['primary_source']
        duty = entry['responsibility'].replace('|', '\\|')
        source_link = f'[依据](../../{primary["path"]})'
        lines.append(f'| [{module}]({directory}) | {duty} {source_link} | {len(files[module])} | {dependency_links} | '
                     f'{sum(len(items) for items in edges[module].values())} |')
    lines.extend(['', '## 每个直接依赖的源码依据', '',
                  '每个目标包列出全部扫描命中位置；同一源码重复import记录按路径和行号去重。'
                  '行号以生成时工作区为准，长期更新应重新生成。', ''])
    for module in modules:
        entry = responsibilities[module]
        lines.extend([f'### {module}', '', entry['responsibility'], '',
                      f'源码目录：[{PREFIX + module}](../../{PREFIX + module})。', ''])
        evidence_links = []
        for source in entry['sources']:
            ranges = '、'.join(f'{scope["start"]}–{scope["end"]}' for scope in source['ranges'])
            evidence_links.append(f'[{Path(source["path"]).name}](../../{source["path"]})（抽读 {ranges} 行）')
        lines.extend(['职责依据：' + '；'.join(evidence_links) + '。完整 SHA 见职责证据 JSON。', ''])
        if not edges[module]:
            lines.extend(['没有扫描到直接内部import；这不代表没有第三方依赖或通过接口注入的调用。', ''])
            continue
        for target, imports in sorted(edges[module].items()):
            evidence = sorted({(edge['file'], edge['line']) for edge in imports})
            links = ', '.join(f'[{Path(file).name}:{line}](../../{file})' for file, line in evidence)
            lines.append(f'- `{module} → {target}`：{links}')
        lines.append('')
    cycles = catalog.get('static_import_cycles', [])
    lines.extend(['## 静态环路记录', '',
                  '扫描发现的import环路：' + (json.dumps(cycles, ensure_ascii=False) if cycles else '0条。')])
    (ROOT / 'docs/architecture/MODULE_CATALOG.md').write_text('\n'.join(lines) + '\n', 'utf-8')
    print(json.dumps({'modules': len(modules), 'modules_with_responsibility': len(responsibilities),
                      'responsibility_source_hashes_verified': len({source['path'] for entry in responsibilities.values() for source in entry['sources']}),
                      'unique_direct_dependency_edges': sum(len(targets) for targets in edges.values()),
                      'source_import_records': len(catalog['imports'])}, ensure_ascii=False))


if __name__ == '__main__':
    main()
