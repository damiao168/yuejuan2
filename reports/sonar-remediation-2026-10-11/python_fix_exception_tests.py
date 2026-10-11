"""按已定位的 Sonar 异常测试，把准备步骤移到异常断言之外。"""
from pathlib import Path
import ast
import copy
import hashlib
import json

ROOT = Path(__file__).resolve().parents[2]
REPORT = Path(__file__).resolve().parent
ISSUES = json.loads((REPORT / 'python-progress.json').read_text('utf-8'))['issues']
CALL_NAMES = {
    'settings': 'test_settings', 'FakeModel': 'model', 'body_size': 'request_size',
    'UnapprovedAdapter': 'adapter', 'valid_request': 'request', 'fixture': 'request',
    'approved_crop': 'crop', 'config': 'managed_config', 'candidates': 'question_candidates',
    'MathRubricDraftGenerator': 'generator', 'structured_schema': 'schema',
    'PaperParser': 'parser', 'payload': 'request', 'visual_page': 'page',
    '_document': 'document', '_output': 'output', '_ref': 'reference',
    'arguments': 'benchmark_arguments', 'FakeEngine': 'engine', 'FakeDetector': 'detector',
    'Response': 'response', 'WorkerConfig': 'worker_config', 'OCRRunner': 'runner',
    '_math_task': 'task', 'RoutedRecognitionResult': 'recognition', 'manifest': 'manifest_path',
    'benchmark_arguments': 'arguments', 'SimpleNamespace': 'detector', 'iter': 'clock_values',
    'png': 'image_bytes', 'getvalue': 'source_bytes', 'sheet': 'image_bytes', 'float': 'invalid_value',
    'OMRProfile': 'profile', '_png': 'page_bytes', 'RecordingHeartbeat': 'heartbeat',
    '_registration_task': 'task', 'sample': 'synthetic_sample',
    'NetworkCapableTransport': 'transport',
}


def call_name(call):
    function = call.func
    if isinstance(function, ast.Name):
        return function.id
    return function.attr if isinstance(function, ast.Attribute) else 'prepared_value'


class HoistPreparation(ast.NodeTransformer):
    def __init__(self, used_names, root):
        self.used_names = used_names
        self.root = root
        self.preparations = []

    def visit_Call(self, node):
        if node is self.root:
            return self.generic_visit(node)
        name = CALL_NAMES.get(call_name(node), 'prepared_value')
        candidate, number = name, 2
        while candidate in self.used_names:
            candidate = f'{name}_{number}'
            number += 1
        self.used_names.add(candidate)
        self.preparations.append(ast.Assign(targets=[ast.Name(id=candidate, ctx=ast.Store())], value=node))
        return ast.copy_location(ast.Name(id=candidate, ctx=ast.Load()), node)


def main():
    grouped = {}
    for issue in ISSUES:
        if issue['rule'] == 'python:S5778':
            grouped.setdefault(issue['file'], []).append(issue)
    records = []
    for relative, issues in grouped.items():
        path = ROOT / relative
        data = path.read_bytes()
        source = data.decode('utf-8-sig')
        tree = ast.parse(source)
        lines = source.splitlines(keepends=True)
        newline = '\r\n' if '\r\n' in source else '\n'
        changes = []
        for issue in issues:
            candidates = [node for node in ast.walk(tree) if isinstance(node, ast.With)
                          and node.lineno <= issue['original_line'] <= node.end_lineno]
            if not candidates:
                raise ValueError(f'告警位置已漂移，需要重新定位：{issue["key"]}')
            original = min(candidates, key=lambda node: node.end_lineno - node.lineno)
            if len(original.body) != 1 or not isinstance(original.body[0], ast.Expr) or not isinstance(original.body[0].value, ast.Call):
                raise ValueError(f'需手工审读不同断言结构：{issue["key"]}')
            function = min((node for node in ast.walk(tree) if isinstance(node, ast.FunctionDef)
                            and node.lineno <= original.lineno <= node.end_lineno), key=lambda node: node.end_lineno - node.lineno)
            used_names = {node.id for node in ast.walk(function) if isinstance(node, ast.Name)}
            updated = copy.deepcopy(original)
            root_call = updated.body[0].value
            hoist = HoistPreparation(used_names, root_call)
            updated.body[0] = hoist.visit(updated.body[0])
            if not hoist.preparations:
                raise ValueError(f'当前断言已只有一个调用，需保留原文核对误报：{issue["key"]}')
            ast.fix_missing_locations(updated)
            statements = hoist.preparations + [updated]
            rendered = '\n'.join(ast.unparse(ast.fix_missing_locations(node)) for node in statements)
            indent = ' ' * original.col_offset
            rendered = newline.join(indent + line if line else '' for line in rendered.splitlines()) + newline
            changes.append((original.lineno - 1, original.end_lineno, rendered))
            records.append({'key': issue['key'], 'file': relative, 'test': function.name,
                            'before': ast.get_source_segment(source, original),
                            'after': rendered.strip(), 'prepared_variables': [item.targets[0].id for item in hoist.preparations],
                            'source_sha256_before': hashlib.sha256(data).hexdigest(),
                            'assertion_body_calls_after': sum(isinstance(node, ast.Call) for node in ast.walk(updated.body[0]))})
        for start, end, rendered in sorted(changes, reverse=True):
            lines[start:end] = [rendered]
        updated_source = ''.join(lines)
        ast.parse(updated_source)
        path.write_bytes((b'\xef\xbb\xbf' if data.startswith(b'\xef\xbb\xbf') else b'') + updated_source.encode('utf-8'))
    for record in records:
        record['source_sha256_after'] = hashlib.sha256((ROOT / record['file']).read_bytes()).hexdigest()
    (REPORT / 'python-exception-test-changes.json').write_text(json.dumps({'records': records}, ensure_ascii=False, indent=2), 'utf-8')
    print(json.dumps({'issues': len(records), 'files': len(grouped), 'all_assertions_single_call': all(record['assertion_body_calls_after'] == 1 for record in records)}))


if __name__ == '__main__':
    main()
