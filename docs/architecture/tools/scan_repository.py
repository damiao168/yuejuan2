"""自动盘点只提供审计线索；导入扫描和文件枚举都不能代替逐文件审读。"""
from pathlib import Path
import json
import re
import subprocess

ROOT = Path(__file__).resolve().parents[3]
OUT = ROOT / "docs/architecture/evidence"

def scan_internal_imports(files):
    imports, modules = [], set()
    for path in files:
        if not path.startswith('services/api-gateway/internal/') or not path.endswith('.go') or path.endswith('_test.go'):
            continue
        target = ROOT / path
        if not target.is_file():
            continue
        module = path.split('/')[3]
        modules.add(module)
        source = target.read_text(encoding='utf-8-sig')
        for match in re.finditer(r'"edugrade-enterprise/services/api-gateway/internal/([^"/]+)(?:/[^"\n]+)?"', source):
            imports.append({'source': module, 'target': match.group(1), 'file': path, 'line': source[:match.start()].count('\n') + 1})
    return imports, modules


def static_cycles(imports, modules):
    adjacency = {module: set() for module in modules}
    for edge in imports:
        adjacency[edge['source']].add(edge['target'])
    cycles, visited, stack = [], set(), []
    # 环路只包含源码导入边，不包含接口注入造成的运行时调用回路。
    def visit(module):
        if module in stack:
            cycles.append(stack[stack.index(module):] + [module])
            return
        if module in visited:
            return
        stack.append(module)
        for target in adjacency.get(module, []):
            visit(target)
        stack.pop()
        visited.add(module)
    for module in sorted(modules):
        visit(module)
    return cycles


def inventory_entry(path):
    if path.startswith('docs/architecture/') and path.split('/')[2] in ('evidence', 'tools', 'specs', 'previews', 'diagrams'):
        return None
    suffix = Path(path).suffix.lower()
    protected = path.endswith(('package-lock.json', 'go.sum', 'Cargo.lock')) or '/migrations/' in path or '/generated/' in path or '/prompts/' in path or path.startswith('.env') or suffix in ('.jsonl', '.csv', '.pdf', '.png', '.jpg', '.jpeg', '.webp', '.woff2', '.onnx', '.gguf')
    handling = 'read_only'
    if protected:
        handling = 'protected'
    elif suffix == '.md':
        handling = 'documentation'
    return {'path': path, 'review': 'pending', 'handling': handling, 'note': '自动盘点；尚未完成逐文件审读'}


def main():
    OUT.mkdir(exist_ok=True, parents=True)
    files = sorted(set(subprocess.check_output(['git', 'ls-files', '--cached', '--others', '--exclude-standard', '-z'], cwd=ROOT).decode('utf-8').split('\0')) - {''})
    imports, modules = scan_internal_imports(files)
    cycles = static_cycles(imports, modules)
    (OUT / 'module-imports.json').write_text(json.dumps({'method': '正则提取非测试Go内部包import；人工审读见file-review.tsv', 'modules': sorted(modules), 'imports': imports, 'static_import_cycles': cycles}, ensure_ascii=False, indent=2), encoding='utf-8')
    inventory = [entry for path in files if (entry := inventory_entry(path)) is not None]
    (OUT / 'file-inventory.json').write_text(json.dumps(inventory, ensure_ascii=False, indent=2), encoding='utf-8')
    summary = {'inventory': len(inventory), 'go_internal_modules': len(modules), 'unique_import_edges': len({(edge['source'], edge['target']) for edge in imports}), 'cycles': cycles, 'tests': sum(path.endswith('_test.go') or '/tests/' in path or '.test.' in path or '.spec.' in path for path in files)}
    (OUT / 'repository-scan.json').write_text(json.dumps(summary, ensure_ascii=False, indent=2), encoding='utf-8')
    print(json.dumps(summary, ensure_ascii=False))

if __name__ == "__main__":
    main()
