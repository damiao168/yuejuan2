"""本地 Python 修复辅助：只替换已审读函数，保留文件其余当前内容。"""
from pathlib import Path
import ast

ROOT = Path(__file__).resolve().parents[2]


def replace_function(relative, name, replacement):
    path = ROOT / relative
    data = path.read_bytes()
    bom = data.startswith(b'\xef\xbb\xbf')
    source = data.decode('utf-8-sig')
    tree = ast.parse(source)
    node = next(n for n in tree.body if isinstance(n, (ast.FunctionDef, ast.AsyncFunctionDef)) and n.name == name)
    lines = source.splitlines(keepends=True)
    newline = '\r\n' if '\r\n' in source else '\n'
    text = replacement.strip('\n').replace('\r\n', '\n').replace('\n', newline) + newline
    updated = ''.join(lines[:node.lineno - 1]) + text + ''.join(lines[node.end_lineno:])
    ast.parse(updated)
    path.write_bytes((b'\xef\xbb\xbf' if bom else b'') + updated.encode('utf-8'))


def replace_exact(relative, old, new):
    path = ROOT / relative
    data = path.read_bytes()
    original = data.decode('utf-8-sig')
    newline = '\r\n' if '\r\n' in original else '\n'
    source = original.replace('\r\n', '\n')
    old = old.replace('\r\n', '\n')
    new = new.replace('\r\n', '\n')
    if old not in source:
        raise ValueError(f'当前文本不匹配：{relative}: {old[:80]!r}')
    updated = source.replace(old, new)
    ast.parse(updated)
    path.write_bytes((b'\xef\xbb\xbf' if data.startswith(b'\xef\xbb\xbf') else b'') + updated.replace('\n', newline).encode('utf-8'))
