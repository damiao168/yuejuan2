"""让异常断言只围住实际失败操作，保留真实 TLS 和证书回收场景。"""
from collections import Counter
from datetime import datetime, timezone
import ast
import hashlib
import json
from pathlib import Path

from python_edit_tools import replace_exact

ROOT = Path(__file__).resolve().parents[2]
REPORT = Path(__file__).resolve().parent


def main():
    replace_exact('ai-services/tests/test_model_transport_security.py',
                  '            for adapter, code, status in cases:\n                with self.subTest(adapter=type(adapter).__name__), self.assertRaises(AgentError) as caught:\n                    adapter.request(valid_request())',
                  '            for adapter, code, status in cases:\n                request_data = valid_request()\n                with self.subTest(adapter=type(adapter).__name__), self.assertRaises(AgentError) as caught:\n                    adapter.request(request_data)')
    path = 'ai-services/tests/test_server_tls_handshake.py'
    replace_exact(path, '    def _assert_closed(self, server):',
                  '    def _connect_after_close(self, address):\n        # 意外连接成功也会在退出上下文时关闭；失败断言只接受建立连接的 OSError。\n        with socket.create_connection(address, timeout=1):\n            self.fail("closed listener still accepted a connection")\n\n    def _assert_closed(self, server):')
    replace_exact(path,
                  '        with self.assertRaises(OSError):\n            with socket.create_connection(server.server_address, timeout=1):\n                self.fail("closed listener still accepted a connection")',
                  '        with self.assertRaises(OSError):\n            self._connect_after_close(server.server_address)')
    replace_exact(path,
                  '                with self.assertRaises(ssl.SSLCertVerificationError) as caught:\n                    self._trusted_context().wrap_socket(connection, server_hostname="unmatched.grading.invalid")',
                  '                context = self._trusted_context()\n                with self.assertRaises(ssl.SSLCertVerificationError) as caught:\n                    context.wrap_socket(connection, server_hostname="unmatched.grading.invalid")')
    replace_exact(path,
                  '            if response:\n                with self.assertRaises((UnicodeDecodeError, json.JSONDecodeError)):\n                    json.loads(response.decode("utf-8"))',
                  '            if response:\n                # TLS 告警可能不是 UTF-8；可解码时也必须拒绝把它识别成业务 JSON。\n                try:\n                    decoded = response.decode("utf-8")\n                except UnicodeDecodeError:\n                    pass\n                else:\n                    with self.assertRaises(json.JSONDecodeError):\n                        json.loads(decoded)')
    replace_exact(path,
                  '                with self.assertRaises(error_type):\n                    serve(replace(self.settings, tls_cert_file=certificate, tls_key_file=key))',
                  '                invalid_settings = replace(self.settings, tls_cert_file=certificate, tls_key_file=key)\n                with self.assertRaises(error_type):\n                    serve(invalid_settings)')
    path = 'services/math-verification-worker/tests/test_server_tls_batch54.py'
    replace_exact(path,
                  '        with pytest.raises(error.HTTPError) as rejected:\n            request.urlopen(req, context=_trusted_context(tls_files), timeout=10)',
                  '        context = _trusted_context(tls_files)\n        with pytest.raises(error.HTTPError) as rejected:\n            request.urlopen(req, context=context, timeout=10)')
    replace_exact(path,
                  '    with _running_verifier(monkeypatch, tls_files, "wrong-san") as port:\n        with pytest.raises(error.URLError) as rejected:\n            _health(port, _trusted_context(tls_files))',
                  '    with _running_verifier(monkeypatch, tls_files, "wrong-san") as port:\n        context = _trusted_context(tls_files)\n        with pytest.raises(error.URLError) as rejected:\n            _health(port, context)')
    replace_exact('tests/infra/test_internal_test_certificates.py',
                  '    assert spec is not None and spec.loader is not None',
                  '    assert spec is not None\n    assert spec.loader is not None')
    progress_path = REPORT / 'python-new-findings-progress.json'
    progress = json.loads(progress_path.read_text('utf-8'))
    for item in progress['issues']:
        if item['file'].endswith('/verify_typography.py'):
            item.update(state='already_resolved_current_pending_validation_and_rescan',
                        change='外部当前源码已使用字典字面量；本智能体未重复改写',
                        evidence='全文核对与 AST 无 dict(...) 调用；保留 defusedxml 安全解析')
        else:
            item.update(state='code_modified_pending_validation_and_rescan')
            if item['rule'].endswith('S3776'):
                item['change'] = '按来源版本复核、旧完成声明撤销或原生布局度量阶段拆分；保留真实处理顺序'
            elif item['rule'].endswith('S1192'):
                item['change'] = '全部相同完整路径引用已有常量，复合路径与出处文案逐字保留'
            elif item['rule'].endswith('S7498'):
                item['change'] = '仅 keyword 的 dict 构造改成字典字面量，键序与值不变'
            elif item['rule'].endswith('S5778'):
                item['change'] = '构造有效请求/可信 TLS 上下文/失败配置移到异常上下文外；保留异常和资源回收断言'
            else:
                item['change'] = '复合断言拆为顺序断言，spec 非空失败后不访问 loader'
        item['current_sha256'] = hashlib.sha256((ROOT / item['file']).read_bytes()).hexdigest()
    for item in progress['files']:
        source = (ROOT / item['file']).read_bytes()
        compile(source.decode('utf-8-sig'), item['file'], 'exec')
        item.update(current_sha256=hashlib.sha256(source).hexdigest(), current_lines=len(source.decode('utf-8-sig').splitlines()),
                    source_modified_by_this_batch=not item['file'].endswith('/verify_typography.py'),
                    syntax_validation='in_memory_compile_passed', validation='pending')
    verification = ROOT / 'docs/architecture/tools/verify_typography.py'
    assert not any(isinstance(node, ast.Call) and isinstance(node.func, ast.Name) and node.func.id == 'dict'
                   for node in ast.walk(ast.parse(verification.read_text('utf-8-sig'))))
    progress.update(updated_at_utc=datetime.now(timezone.utc).isoformat(),
                    summary=dict(Counter(item['state'] for item in progress['issues'])))
    progress_path.write_text(json.dumps(progress, ensure_ascii=False, indent=2), 'utf-8')
    print(json.dumps(progress['summary']))


if __name__ == '__main__':
    main()
