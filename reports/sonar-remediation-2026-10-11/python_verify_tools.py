"""用相同本地输入比较基线与修复版；写入仅落在内存，不重建当前架构产物。"""
from pathlib import Path
import ast
import builtins
import contextlib
import hashlib
import io
import json
import subprocess
import sys
import types
from unittest.mock import patch
from datetime import datetime, timezone

ROOT = Path(__file__).resolve().parents[2]
REPORT = Path(__file__).resolve().parent
BACKUP = Path('D:/tools/sonar-remediation-20261011/baseline')
TOOL_DIR = 'docs/architecture/tools/'
REAL_OPEN = builtins.open
REAL_CHECK_OUTPUT = subprocess.check_output
READ_CACHE = {}
COMMAND_CACHE = {}


class CapturedText(io.StringIO):
    def __init__(self, callback):
        super().__init__()
        self.callback = callback

    def close(self):
        if not self.closed:
            self.callback(self.getvalue().encode('utf-8'))
        super().close()


class CapturedBytes(io.BytesIO):
    def __init__(self, callback):
        super().__init__()
        self.callback = callback

    def close(self):
        if not self.closed:
            self.callback(self.getvalue())
        super().close()


class VirtualFiles:
    def __init__(self):
        self.writes = {}

    def open(self, file, mode='r', buffering=-1, encoding=None, errors=None, newline=None, closefd=True, opener=None):
        if isinstance(file, int):
            return REAL_OPEN(file, mode, buffering, encoding, errors, newline, closefd, opener)
        path = str(Path(file).resolve())
        if any(flag in mode for flag in ('w', 'a', 'x', '+')):
            callback = lambda value: self.writes.__setitem__(path, value)
            return CapturedBytes(callback) if 'b' in mode else CapturedText(callback)
        if path in self.writes:
            data = self.writes[path]
        else:
            if path not in READ_CACHE:
                with REAL_OPEN(path, 'rb') as stream:
                    READ_CACHE[path] = stream.read()
            data = READ_CACHE[path]
        if 'b' in mode:
            return io.BytesIO(data)
        return io.StringIO(data.decode(encoding or 'utf-8', errors or 'strict'), newline=newline)

    def __enter__(self):
        self.stack = contextlib.ExitStack()
        self.stack.enter_context(patch('builtins.open', self.open))
        self.stack.enter_context(patch('io.open', self.open))
        self.stack.enter_context(patch.object(Path, 'mkdir', lambda *args, **kwargs: None))
        self.stack.enter_context(patch('subprocess.check_output', cached_command))
        return self

    def __exit__(self, *args):
        return self.stack.__exit__(*args)


def cached_command(args, **kwargs):
    if args[0] != 'git':
        raise AssertionError('验证只允许已有脚本的只读本地 git 命令')
    key = repr((args, kwargs))
    if key not in COMMAND_CACHE:
        COMMAND_CACHE[key] = REAL_CHECK_OUTPUT(args, **kwargs)
    return COMMAND_CACHE[key]


def module_source(name, current):
    path = (ROOT if current else BACKUP) / (TOOL_DIR + name + '.py')
    return path.read_text('utf-8-sig')


def load_module(name, current):
    module = types.ModuleType(name)
    module.__file__ = str(ROOT / (TOOL_DIR + name + '.py'))
    # 原脚本的路径计算仍指向同一真实输入；只有写入被截获，不改审读版本规则。
    exec(compile(module_source(name, current), module.__file__, 'exec'), module.__dict__)
    return module


def run_tool(name, current):
    output = io.StringIO()
    with VirtualFiles() as files, contextlib.redirect_stdout(output):
        previous_diagrams = sys.modules.get('build_diagrams')
        if name == 'verify_delivery':
            sys.modules['build_diagrams'] = load_module('build_diagrams', current)
        try:
            module = load_module(name, current)
            result = None
            if name == 'build_diagrams':
                result = module.build()
            elif name == 'write_frontend_specs':
                features, apis = module.feature_pages(), module.api_pages()
                result = {'missing': module.validate_paths(features + apis)}
                module.save('01-feature-tree.drawio', '完整系统功能树', features)
                module.save('08-api-data-flow.drawio', '核心 API 与数据流', apis)
            elif name == 'write_root_specs':
                module.architecture()
                module.dependencies()
                module.security()
            elif name == 'write_frontend_audit':
                result = module.write_review()
                module.write_audit(result)
            elif name not in {'extract_database_catalog', 'build_database_specs', 'preview_server'}:
                result = module.main()
            elif name == 'preview_server':
                result = module.Handler.log_message(None, 'synthetic log')
            error = None
        except Exception as exc:
            result, error = None, {'type': type(exc).__name__, 'message': str(exc)}
        finally:
            if previous_diagrams is None:
                sys.modules.pop('build_diagrams', None)
            else:
                sys.modules['build_diagrams'] = previous_diagrams
    writes = {}
    for path, data in files.writes.items():
        # 交付验证时间戳是实时时钟，比较其余全部真实检查结果。
        if path.endswith('delivery-validation.json'):
            value = json.loads(data)
            value.pop('checked_at_utc')
            data = json.dumps(value, ensure_ascii=False, sort_keys=True).encode()
        writes[path] = {'sha256': hashlib.sha256(data).hexdigest(), 'bytes': len(data)}
    return {'result': result, 'error': error, 'writes': writes, 'stdout': output.getvalue()}


def main():
    names = [
        'build_database_specs', 'build_diagrams', 'build_module_catalog', 'extract_database_catalog',
        'merge_review_ledger', 'preview_server', 'route_obstructions', 'scan_repository',
        'simplify_diagrams', 'verify_delivery', 'write_frontend_audit', 'write_frontend_specs',
        'write_root_review', 'write_root_specs',
    ]
    selected = sys.argv[1:]
    if selected:
        assert all(name in names for name in selected)
        names = selected
    records = []
    source_hashes = {TOOL_DIR + name + '.py': hashlib.sha256((ROOT / (TOOL_DIR + name + '.py')).read_bytes()).hexdigest()
                     for name in names}
    for name in names:
        for current in (False, True):
            ast.parse(module_source(name, current))
        baseline = run_tool(name, False)
        current = run_tool(name, True)
        records.append({'tool': TOOL_DIR + name + '.py', 'equivalent': baseline == current,
                        'baseline': baseline, 'current': current})
        print(name, 'PASS' if baseline == current else 'FAIL', 'writes', len(current['writes']), 'error', current['error'], flush=True)
    report = {
        'checked_at_utc': datetime.now(timezone.utc).isoformat(), 'passed': all(item['equivalent'] for item in records),
        'comparison': '同一缓存本地输入；所有生成写入仅在内存；交付实时时间戳不比较；文本输出统一LF，XML二进制逐字节比较',
        'quality_gate_claim': False, 'records': records,
        'current_source_hashes': source_hashes,
        'sources_unchanged_during_comparison': all(hashlib.sha256((ROOT / path).read_bytes()).hexdigest() == digest
                                                 for path, digest in source_hashes.items()),
        'input_files': [{'file': path, 'bytes': len(data), 'sha256': hashlib.sha256(data).hexdigest()}
                        for path, data in sorted(READ_CACHE.items())],
    }
    output_name = 'python-tools-equivalence-xml-security.json' if selected else 'python-tools-equivalence.json'
    (REPORT / output_name).write_text(json.dumps(report, ensure_ascii=False, indent=2), 'utf-8')
    return 0 if report['passed'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
