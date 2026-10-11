"""验证 RLS 边界提取兼容性与大量缺失终结符输入的有界处理，不写生成产物。"""
import ast
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
import re
import time

ROOT = Path(__file__).resolve().parents[2]
REPORT = Path(__file__).resolve().parent
SOURCE = ROOT / 'docs/architecture/tools/extract_database_catalog.py'


def main():
    source = SOURCE.read_text('utf-8-sig')
    function = next(node for node in ast.parse(source).body if isinstance(node, ast.FunctionDef) and node.name == 'rls_loops')
    namespace = {'re': re}
    exec(compile(ast.Module(body=[function], type_ignores=[]), str(SOURCE), 'exec'), namespace)
    old_pattern = re.compile(r'FOREACH\s+\w+\s+IN\s+ARRAY\s+ARRAY\s*\[([\s\S]*?)\]\s*LOOP([\s\S]*?)END\s+LOOP', re.I)
    cases = [
        ('complete', "FOREACH t IN ARRAY ARRAY['a', 'b'] LOOP ALTER TABLE a ENABLE ROW LEVEL SECURITY; END LOOP;"),
        ('multiple', "FOREACH t IN ARRAY ARRAY['a'] LOOP x END LOOP; FOREACH u IN ARRAY ARRAY['b'] LOOP y END LOOP;"),
        ('multiline', "FOREACH\nt\nIN\nARRAY\nARRAY\n[\n'a'\n]\nLOOP\nx\nEND\nLOOP"),
        ('case', "foreach t in array array['a'] loop x end loop"),
        ('missing_array_end', "FOREACH t IN ARRAY ARRAY['a' LOOP x END LOOP"),
        ('missing_body_end', "FOREACH t IN ARRAY ARRAY['a'] LOOP x"),
        ('nested_header_in_array', "FOREACH t IN ARRAY ARRAY['a', FOREACH u IN ARRAY ARRAY['b'] LOOP x END LOOP"),
        ('nested_header_in_body', "FOREACH t IN ARRAY ARRAY['a'] LOOP FOREACH u IN ARRAY ARRAY['b'] LOOP x END LOOP END LOOP"),
        ('extra_array_token_in_body', "FOREACH t IN ARRAY ARRAY['a'] LOOP ] LOOP x END LOOP"),
        ('incomplete_then_complete', "FOREACH t IN ARRAY ARRAY['a' FOREACH u IN ARRAY ARRAY['b'] LOOP x END LOOP"),
        ('unrelated_tokens', "END LOOP x ] LOOP FOREACH t IN ARRAY ARRAY['a'] LOOP END LOOP"),
        ('empty', ''),
    ]
    records = []
    for name, sql in cases:
        expected = [(match.start(), match[1], match[2]) for match in old_pattern.finditer(sql)]
        actual = list(namespace['rls_loops'](sql))
        records.append({'case': name, 'sql': sql, 'expected': expected, 'actual': actual, 'passed': expected == actual})
    malformed = "FOREACH t IN ARRAY ARRAY['a'] LOOP " * 10000
    started = time.monotonic()
    result = list(namespace['rls_loops'](malformed))
    elapsed = time.monotonic() - started
    report = {'checked_at_utc': datetime.now(timezone.utc).isoformat(), 'source_sha256': hashlib.sha256(SOURCE.read_bytes()).hexdigest(),
              'comparison_cases': records, 'passed': all(item['passed'] for item in records) and not result,
              'malformed_input': {'bytes': len(malformed.encode()), 'repeated_headers': 10000,
                                  'missing_end_loop': True, 'matches': len(result), 'elapsed_seconds': elapsed},
              'scope': '原正则匹配语义兼容；大量不完整边界只执行新扫描器，避免运行已知回溯风险模式'}
    (REPORT / 'python-rls-parser-validation.json').write_text(json.dumps(report, ensure_ascii=False, indent=2), 'utf-8')
    print(json.dumps({'passed': report['passed'], 'cases': len(records), 'malformed_seconds': elapsed}))
    return 0 if report['passed'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
