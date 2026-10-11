"""调用当前生成器与验收入口，验证拒绝 DTD/实体且不访问网络；仅写内存产物。"""
from datetime import datetime, timezone
import hashlib
import importlib.metadata
import io
import json
from pathlib import Path
import sys
from unittest.mock import patch

import python_verify_tools as verifier

REPORT = Path(__file__).resolve().parent
PAYLOADS = {
    'editable_xml': b'<mxfile><diagram><mxGraphModel><root/></mxGraphModel></diagram></mxfile>',
    'plain_dtd': b'<!DOCTYPE mxfile><mxfile/>',
    'internal_entity': b'<!DOCTYPE mxfile [<!ENTITY value "expanded">]><mxfile>&value;</mxfile>',
    'external_entity': b'<!DOCTYPE mxfile [<!ENTITY value SYSTEM "http://127.0.0.1:1/private">]><mxfile>&value;</mxfile>',
}


def run_case(name, payload):
    calls = []
    previous = sys.modules.get('build_diagrams')
    try:
        with verifier.VirtualFiles() as files:
            if name == 'verify_delivery':
                sys.modules['build_diagrams'] = verifier.load_module('build_diagrams', True)
            module = verifier.load_module(name, True)
            real_parse = module.safe_xml.parse

            def intercepted_parse(path, **options):
                # 保留真实调用传入的安全选项，只将回读文件替换为恶意/正常测试输入。
                calls.append({'path': str(path), 'options': options})
                return real_parse(io.BytesIO(payload), **options)

            try:
                with patch.object(module.safe_xml, 'parse', intercepted_parse), patch('builtins.print'):
                    if name == 'build_diagrams':
                        module.build()
                    else:
                        module.validate_diagram(module.EXPECTED[0], {}, {}, [])
                return {'error': None, 'parse_calls': calls, 'virtual_writes': len(files.writes)}
            except Exception as exc:
                return {'error': {'type': type(exc).__name__, 'message': str(exc)},
                        'parse_calls': calls, 'virtual_writes': len(files.writes)}
    finally:
        if previous is None:
            sys.modules.pop('build_diagrams', None)
        else:
            sys.modules['build_diagrams'] = previous


def main():
    network_calls = []

    def reject_network(*args, **kwargs):
        network_calls.append(repr(args))
        raise AssertionError('XML验收不得访问网络')

    records = []
    with patch('socket.create_connection', reject_network), patch('urllib.request.urlopen', reject_network):
        for tool in ('build_diagrams', 'verify_delivery'):
            for name, payload in PAYLOADS.items():
                result = run_case(tool, payload)
                expected = None if name == 'editable_xml' else 'DTDForbidden'
                actual = result['error']['type'] if result['error'] else None
                records.append({'tool': tool, 'case': name, 'expected_error': expected, **result,
                                'passed': actual == expected and bool(result['parse_calls'])
                                and all(call['options'].get('forbid_dtd') is True for call in result['parse_calls'])})
    report = {'checked_at_utc': datetime.now(timezone.utc).isoformat(), 'defusedxml_version': importlib.metadata.version('defusedxml'),
              'source_hashes': {verifier.TOOL_DIR + name + '.py': hashlib.sha256((verifier.ROOT / (verifier.TOOL_DIR + name + '.py')).read_bytes()).hexdigest()
                                for name in ('build_diagrams', 'verify_delivery')},
              'records': records, 'network_calls': network_calls, 'passed': all(item['passed'] for item in records) and not network_calls,
              'scope': '真实入口的XML回读；所有生成写入仅在内存；不宣称工作区全交付门禁已通过'}
    (REPORT / 'python-xml-security-validation.json').write_text(json.dumps(report, ensure_ascii=False, indent=2), 'utf-8')
    print(json.dumps({'passed': report['passed'], 'cases': len(records), 'network_calls': len(network_calls)}))
    return 0 if report['passed'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
