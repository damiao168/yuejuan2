"""串行执行新增 Python 修复和 TLS 平台证据涉及的现有测试，记录真实退出码。"""
from datetime import datetime, timezone
import hashlib
import importlib.metadata
import json
import os
from pathlib import Path
import subprocess
import sys
import time
from xml.etree import ElementTree

ROOT = Path(__file__).resolve().parents[2]
REPORT = Path(__file__).resolve().parent
GROUPS = {
    'grading-tls-and-transport': ['ai-services/tests/test_model_transport_security.py',
                                'ai-services/tests/test_server_tls_handshake.py',
                                'ai-services/tests/test_server_tls.py', 'ai-services/tests/test_config.py'],
    'math-verifier-tls-and-policy': ['services/math-verification-worker/tests/test_server_tls_batch54.py',
                                    'services/math-verification-worker/tests/test_server.py'],
    'internal-test-certificates': ['tests/infra/test_internal_test_certificates.py'],
}


def main():
    environment = os.environ.copy()
    removed = [name for name in environment if name.startswith('EDUGRADE_') and name != 'EDUGRADE_TEST_OPENSSL']
    for name in removed:
        environment.pop(name)
    environment.update(PYTHONPATH=os.pathsep.join(str(ROOT / path) for path in
                      ['ai-services', 'ai-services/tests', 'packages/worker-runtime-python', 'services/math-verification-worker']),
                       PYTHONIOENCODING='utf-8', PYTEST_DISABLE_PLUGIN_AUTOLOAD='1',
                       OMP_NUM_THREADS='1', OPENBLAS_NUM_THREADS='1', MKL_NUM_THREADS='1')
    openssl = Path('C:/Program Files/Git/usr/bin/openssl.exe')
    assert openssl.is_file()
    environment['EDUGRADE_TEST_OPENSSL'] = str(openssl)
    new = json.loads((REPORT / 'python-new-findings-progress.json').read_text('utf-8'))
    paths = {item['file'] for item in new['files']}
    paths.update(path for group in GROUPS.values() for path in group)
    paths.update(['ai-services/grading_agent/server.py', 'ai-services/grading_agent/config.py',
                  'services/math-verification-worker/math_verification_worker/server.py',
                  'infra/docker-compose/scripts/generate-internal-test-certificates.py'])
    hashes = {path: hashlib.sha256((ROOT / path).read_bytes()).hexdigest() for path in sorted(paths)}
    results = []
    versions = {name: importlib.metadata.version(name) for name in ['pytest', 'jsonschema', 'sympy', 'pillow', 'defusedxml']}
    report = {'python': sys.version, 'versions': versions, 'openssl_path': str(openssl),
              'environment_names_removed': removed, 'source_hashes_before': hashes, 'results': results,
              'scope': '本地真实 TLS 监听器/握手和短期合成证书，模型依赖由既有夹具替换；不宣称生产学校或真实模型验收。'}
    for name, paths in GROUPS.items():
        junit = REPORT / ('python-new-' + name + '-junit.xml')
        log = REPORT / ('python-new-' + name + '-pytest.log')
        command = [sys.executable, '-m', 'pytest', '--import-mode=importlib', '-p', 'no:cacheprovider', '-q',
                   *paths, '--junitxml=' + str(junit)]
        started = datetime.now(timezone.utc).isoformat()
        before = time.monotonic()
        with log.open('w', encoding='utf-8', newline='') as stream:
            completed = subprocess.run(command, cwd=ROOT, env=environment, stdout=stream, stderr=subprocess.STDOUT, timeout=600)
        suites = list(ElementTree.parse(junit).getroot().iter('testsuite')) if junit.exists() else []
        counts = {field: sum(int(suite.get(field, '0')) for suite in suites) for field in ['tests', 'errors', 'failures', 'skipped']}
        results.append({'group': name, 'paths': paths, 'command': command, 'started_at_utc': started,
                        'finished_at_utc': datetime.now(timezone.utc).isoformat(), 'duration_seconds': time.monotonic() - before,
                        'exit_code': completed.returncode, 'junit_counts': counts, 'log': log.name, 'junit': junit.name})
        (REPORT / 'python-new-test-execution.json').write_text(json.dumps(report, ensure_ascii=False, indent=2), 'utf-8')
        print(name, 'exit', completed.returncode, 'counts', counts, 'seconds', round(results[-1]['duration_seconds'], 2), flush=True)
    report.update(source_hashes_after={path: hashlib.sha256((ROOT / path).read_bytes()).hexdigest() for path in hashes})
    report['sources_unchanged_during_tests'] = report['source_hashes_before'] == report['source_hashes_after']
    report['passed'] = all(item['exit_code'] == 0 and item['junit_counts']['errors'] == 0 and item['junit_counts']['failures'] == 0
                           for item in results) and report['sources_unchanged_during_tests']
    (REPORT / 'python-new-test-execution.json').write_text(json.dumps(report, ensure_ascii=False, indent=2), 'utf-8')
    return 0 if report['passed'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
