"""最终冻结后只补 3 个现有测试文件；不覆盖旧测试证据，不连接真实服务。"""
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
REPORTS = Path(__file__).parent
OUTPUT = REPORTS / 'fresh-python-dynamic-01'
TARGETS = {
    'ocr-api-and-symbolic': [
        'services/ocr-worker/tests/test_api_batch51.py',
        'services/ocr-worker/tests/test_symbolic_runner.py',
    ],
    'subjective-api-integration': ['services/subjective-grading-worker/tests/test_api_integration.py'],
}

def manifest():
    paths = {ROOT / relative for relatives in TARGETS.values() for relative in relatives}
    for directory in ['services/ocr-worker/ocr_worker',
                      'services/subjective-grading-worker/subjective_grading_worker',
                      'packages/worker-runtime-python/edugrade_worker_runtime']:
        paths.update((ROOT / directory).rglob('*.py'))
    paths.add(ROOT / 'services/ocr-worker/tests/test_runner.py')
    return {path.relative_to(ROOT).as_posix(): hashlib.sha256(path.read_bytes()).hexdigest() for path in sorted(paths)}

def main():
    OUTPUT.mkdir(exist_ok=True)
    environment = os.environ.copy()
    removed = [name for name in environment if name.startswith(('EDUGRADE_', 'OPENAI_', 'AZURE_OPENAI_', 'ANTHROPIC_', 'OLLAMA_'))]
    for name in removed:
        environment.pop(name)
    environment.update(
        PYTHONPATH=os.pathsep.join(str(ROOT / relative) for relative in [
            'services/ocr-worker', 'services/ocr-worker/tests',
            'services/subjective-grading-worker', 'packages/worker-runtime-python']),
        PYTHONIOENCODING='utf-8', PYTHONDONTWRITEBYTECODE='1',
        PYTEST_DISABLE_PLUGIN_AUTOLOAD='1', OMP_NUM_THREADS='1',
        OPENBLAS_NUM_THREADS='1', MKL_NUM_THREADS='1',
    )
    before = manifest()
    records = []
    for name, paths in TARGETS.items():
        command = [sys.executable, '-m', 'pytest', '--import-mode=prepend',
                   '-p', 'no:cacheprovider', '-q', *paths, '--junitxml=' + str(OUTPUT / (name + '-junit.xml'))]
        started = datetime.now(timezone.utc).isoformat()
        clock = time.monotonic()
        log = OUTPUT / (name + '-pytest.log')
        with log.open('w', encoding='utf-8', newline='') as stream:
            completed = subprocess.run(command, cwd=ROOT, env=environment, stdout=stream, stderr=subprocess.STDOUT, timeout=180)
        root = ElementTree.parse(OUTPUT / (name + '-junit.xml')).getroot()
        cases = list(root.iter('testcase'))
        passed = [case.get('classname', '') + '::' + case.get('name', '') for case in cases
                  if not any(case.find(tag) is not None for tag in ['failure', 'error', 'skipped'])]
        skipped = [{'id': case.get('classname', '') + '::' + case.get('name', ''),
                    'reason': case.find('skipped').get('message', '')} for case in cases if case.find('skipped') is not None]
        failed = [case.get('classname', '') + '::' + case.get('name', '') for case in cases
                  if case.find('failure') is not None or case.find('error') is not None]
        records.append({'group': name, 'paths': paths, 'command': command,
                        'started_at_utc': started, 'finished_at_utc': datetime.now(timezone.utc).isoformat(),
                        'duration_seconds': time.monotonic() - clock, 'exit_code': completed.returncode,
                        'counts': {'passed': len(passed), 'failed': len(failed), 'skipped': len(skipped)},
                        'passed_ids': passed, 'failed_ids': failed, 'skipped': skipped,
                        'log': str(log.relative_to(REPORTS)).replace('\\', '/'),
                        'log_sha256': hashlib.sha256(log.read_bytes()).hexdigest(),
                        'junit': str((OUTPUT / (name + '-junit.xml')).relative_to(REPORTS)).replace('\\', '/')})
        print(name, records[-1]['counts'], 'actual exit', completed.returncode, flush=True)
        after = manifest()
        report = {'recorded_at_utc': datetime.now(timezone.utc).isoformat(),
                  'scope': '3 个现有测试文件：OCR HTTP 调用 patch 和符号验证假对象，subjective API 既有回环随机端口模拟服务；清理真实服务/模型凭据，不访问真实学校DB或模型。',
                  'previous_evidence_preserved': 'python-test-execution.json + original logs/JUnit unchanged',
                  'python': sys.version, 'pytest': importlib.metadata.version('pytest'),
                  'removed_environment_names': removed,
                  'source_hashes_before': before, 'source_hashes_after': after,
                  'sources_unchanged_during_tests': before == after,
                  'results': records,
                  'passed': len(records) == len(TARGETS) and all(item['exit_code'] == 0 and not item['failed_ids'] for item in records) and before == after}
        (OUTPUT / 'execution.json').write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    return 0 if report['passed'] else 1

if __name__ == '__main__':
    raise SystemExit(main())
