"""顺序执行本次 Python 修改涉及的现有测试；保存原始输出和实际退出状态。"""
from pathlib import Path
from datetime import datetime, timezone
import hashlib
import importlib.metadata
import json
import os
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parents[2]
REPORT = Path(__file__).resolve().parent
GROUPS = {
    'grading-agent': 'ai-services/tests',
    'ocr-worker': 'services/ocr-worker/tests',
    'page-processing-worker': 'services/page-processing-worker/tests',
    'subjective-worker': 'services/subjective-grading-worker/tests',
    'math-verifier': 'services/math-verification-worker/tests',
    'math-lab': 'lab/evals/math-recognition',
    'ai-evaluation': 'tests/ai-evaluation',
    'simulation-generator': 'scripts/test_fujian_math_batch29.py',
}
IMPORT_ROOTS = [
    'ai-services', 'ai-services/tests', 'packages/worker-runtime-python',
    'services/ocr-worker', 'services/page-processing-worker', 'services/image-quality-worker',
    'services/subjective-grading-worker', 'services/math-verification-worker',
]


def main():
    environment = os.environ.copy()
    removed = [name for name in environment if name.startswith('EDUGRADE_') and name != 'EDUGRADE_TEST_OPENSSL']
    for name in removed:
        environment.pop(name)
    environment.update(PYTHONPATH=os.pathsep.join(str(ROOT / path) for path in IMPORT_ROOTS),
                       PYTHONIOENCODING='utf-8', PYTEST_DISABLE_PLUGIN_AUTOLOAD='1',
                       OMP_NUM_THREADS='1', OPENBLAS_NUM_THREADS='1', MKL_NUM_THREADS='1')
    for executable in [Path('C:/Program Files/Git/usr/bin/openssl.exe'), Path('D:/tools/bin/openssl.exe')]:
        if executable.is_file():
            environment['EDUGRADE_TEST_OPENSSL'] = str(executable)
            break
    requested = list(sys.argv[1:] or GROUPS)
    previous_path = REPORT / 'python-test-execution.json'
    previous = json.loads(previous_path.read_text('utf-8')) if sys.argv[1:] and previous_path.exists() else {}
    results = [item for item in previous.get('results', []) if item['group'] not in requested]
    versions = {}
    for package in ('pytest', 'jsonschema', 'numpy', 'pillow', 'opencv-python-headless', 'pypdfium2', 'zxing-cpp', 'matplotlib', 'sympy'):
        try:
            versions[package] = importlib.metadata.version(package)
        except importlib.metadata.PackageNotFoundError:
            versions[package] = None
    progress = json.loads((REPORT / 'python-progress.json').read_text('utf-8'))
    hashes = {item['file']: hashlib.sha256((ROOT / item['file']).read_bytes()).hexdigest() for item in progress['files']}
    for name in requested:
        relative = GROUPS[name]
        # OCR 现有测试从同目录 test_runner 导入共享假对象，遵循其正常 prepend 导入方式。
        import_mode = 'prepend' if name == 'ocr-worker' else 'importlib'
        command = [sys.executable, '-m', 'pytest', '--import-mode=' + import_mode, '-p', 'no:cacheprovider', '-q',
                   relative, '--junitxml=' + str(REPORT / f'python-{name}-junit.xml')]
        started = datetime.now(timezone.utc).isoformat()
        before = time.monotonic()
        with (REPORT / f'python-{name}-pytest.log').open('w', encoding='utf-8', newline='') as stream:
            completed = subprocess.run(command, cwd=ROOT, env=environment, stdout=stream, stderr=subprocess.STDOUT, timeout=600)
        results.append({'group': name, 'path': relative, 'command': command, 'started_at_utc': started,
                        'finished_at_utc': datetime.now(timezone.utc).isoformat(), 'duration_seconds': time.monotonic() - before,
                        'exit_code': completed.returncode, 'log': f'python-{name}-pytest.log', 'junit': f'python-{name}-junit.xml'})
        report = {'python': sys.version, 'versions': versions, 'removed_environment_names': removed,
                  'runtime_scope': '本地现有测试；真实模型/学校数据库生产验收不在本测试范围',
                  'source_hashes_before': hashes, 'results': results,
                  'previous_attempt_report': 'python-test-attempt1-execution.json' if previous else None}
        (REPORT / 'python-test-execution.json').write_text(json.dumps(report, ensure_ascii=False, indent=2), 'utf-8')
        print(name, 'exit', completed.returncode, 'seconds', round(results[-1]['duration_seconds'], 2), flush=True)
    report['source_hashes_after'] = {path: hashlib.sha256((ROOT / path).read_bytes()).hexdigest() for path in hashes}
    report['sources_unchanged_during_tests'] = report['source_hashes_before'] == report['source_hashes_after']
    (REPORT / 'python-test-execution.json').write_text(json.dumps(report, ensure_ascii=False, indent=2), 'utf-8')
    return 0 if all(result['exit_code'] == 0 for result in results) else 1


if __name__ == '__main__':
    raise SystemExit(main())
