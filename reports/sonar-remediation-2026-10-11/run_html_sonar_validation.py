"""在独立本地项目核验HTML修复，不改变原全仓项目的扫描范围。"""
from pathlib import Path
import datetime as dt
import hashlib
import importlib.util
import json
import os
import secrets
import subprocess
import sys
import time

ROOT = Path(__file__).resolve().parents[2]
ATTEMPT = sys.argv[1] if len(sys.argv) > 1 else '01'
if ATTEMPT not in {'01', '02', '03', '04'}:
    raise ValueError('Unsupported validation attempt')
OUT = Path(__file__).resolve().parent / ('html-validation-' + ATTEMPT)
RUNTIME = Path('D:/tools/sonar-remediation-20261011') / ('html-validation-' + ATTEMPT)
PROJECT = 'yuejuan-remediation-html-20261011'
SOURCES = [
    'docs/architecture/previews/index.html',
    'frontend-review-report/前端UI优化方案-对标成熟产品.html',
    'frontend-review-report/前端UI设计分析.html',
    'frontend-review-report/前端代码审查报告.html',
]


def main():
    if (OUT / 'execution-status.json').exists():
        raise RuntimeError('Keep previous validation evidence; use a fresh attempt')
    OUT.mkdir(exist_ok=True)
    RUNTIME.mkdir(parents=True, exist_ok=True)
    spec = importlib.util.spec_from_file_location('prior_local_sonar_transport', ROOT / 'reports/platform-audit-2026-10-10/sonar/run_local_sonar.py')
    sonar = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(sonar)
    # 仅复用HTTP导出方法；原审计目录和原项目保持不变。
    sonar.OUT, sonar.PROJECT = OUT, PROJECT
    credentials = json.loads(Path('D:/tools/sonarqube-yuejuan/private/credentials.json').read_text(encoding='utf-8-sig'))
    auth = credentials['login'] + ':' + credentials['password']
    status = sonar.request('/api/system/status')
    if status.get('status') != 'UP':
        raise RuntimeError('Existing local SonarQube is not UP')
    sonar.save('server-status.json', status)
    projects = sonar.request('/api/projects/search', {'projects': PROJECT}, auth)
    if not projects.get('components'):
        sonar.request('/api/projects/create', {'project': PROJECT, 'name': 'yuejuan remediation HTML verification 2026-10-11', 'visibility': 'private'}, auth, True)
    settings = {
        'sonar.projectKey': PROJECT,
        'sonar.projectName': 'yuejuan remediation HTML verification 2026-10-11',
        'sonar.projectVersion': '2026-10-11-local-remediation',
        'sonar.sources': ','.join(SOURCES),
        'sonar.sourceEncoding': 'UTF-8',
        'sonar.scm.disabled': 'true',
        'sonar.scm.exclusions.disabled': 'true',
        'sonar.scanner.skipJreProvisioning': 'true',
        'sonar.nodejs.executable': 'D:/node.js/node.exe',
        'sonar.javascript.node.maxspace': '1536',
        'sonar.typescript.tsconfigPaths': 'apps/desktop-client/tsconfig.json,apps/student-portal/tsconfig.json,apps/web-admin/tsconfig.json,packages/sdk/tsconfig.json',
        'sonar.working.directory': RUNTIME.as_posix() + '/scanner-work',
    }
    properties = OUT / 'scan.properties'
    properties.write_text('\n'.join(f'{k}={v}' for k, v in settings.items()) + '\n', encoding='utf-8')
    manifest = [{'path': path, 'sha256': hashlib.sha256((ROOT / path).read_bytes()).hexdigest()} for path in SOURCES]
    sonar.save('source-scope.json', {'scope': '4 HTML files only in separate validation project; not the final full repository rescan', 'project': PROJECT, 'files': manifest, 'original_project_changed': False})
    sonar.save('execution-status.json', {'status': 'running', 'current_results_valid': False, 'started_at': dt.datetime.now().astimezone().isoformat()})
    token_name = 'html-remediation-' + secrets.token_hex(6)
    token = sonar.request('/api/user_tokens/generate', {'name': token_name, 'type': 'USER_TOKEN'}, auth, True)['token']
    try:
        env = dict(os.environ)
        env['SONAR_TOKEN'] = token
        env['SONAR_HOST_URL'] = sonar.URL
        env['JAVA_HOME'] = 'D:/xic/tps/win64/jre21.0.1_12'
        env['SONAR_USER_HOME'] = 'D:/tools/sonarqube-yuejuan/20261010/scanner-cache'
        env['SONAR_SCANNER_OPTS'] = '-Xmx48m -Xms16m -XX:+UseSerialGC -XX:ReservedCodeCacheSize=24m -XX:ActiveProcessorCount=1'
        env['SONAR_SCANNER_JAVA_OPTS'] = '-Xmx384m -Xms32m -Xss512k -XX:+UseSerialGC -XX:TieredStopAtLevel=1 -XX:ReservedCodeCacheSize=32m -XX:ActiveProcessorCount=1'
        command = [
            'cmd.exe', '/d', '/c', 'D:/tools/sonarqube-yuejuan/20261010/sonar-scanner-8.1.0.6389/bin/sonar-scanner.bat',
            '-Dproject.settings=' + str(properties),
            '-Dsonar.projectBaseDir=' + str(ROOT),
            '-Dsonar.scanner.metadataFilePath=' + str(OUT / 'report-task.txt'),
        ]
        sonar.save('scanner-command.json', {'command': command, 'scope': 'independent HTML validation', 'secret_delivery': 'ephemeral SONAR_TOKEN environment variable, revoked in finally', 'analysis_java_heap_mib': 384, 'node_heap_mib': 1536, 'network': 'localhost server and existing tool cache; no remote private repository access'})
        receipt = OUT / 'report-task.txt'
        receipt.unlink(missing_ok=True)
        with (OUT / 'scanner.log').open('w', encoding='utf-8') as log:
            code = subprocess.run(command, cwd=ROOT, env=env, stdout=log, stderr=subprocess.STDOUT).returncode
        sonar.save('scanner-exit.json', {'exit_code': code})
        if code != 0 or not receipt.exists():
            raise RuntimeError('HTML scanner did not return a successful fresh receipt')
        task_id = dict(line.split('=', 1) for line in receipt.read_text().splitlines() if '=' in line)['ceTaskId']
        deadline = time.monotonic() + 600
        state = 'PENDING'
        while time.monotonic() < deadline:
            task = sonar.request('/api/ce/task', {'id': task_id}, auth)
            sonar.save('compute-engine-task.json', task)
            state = task['task']['status']
            if state in {'SUCCESS', 'FAILED', 'CANCELED'}:
                break
            time.sleep(5)
        if state != 'SUCCESS':
            raise RuntimeError('HTML analysis did not complete: ' + state)
        for record in manifest:
            if hashlib.sha256((ROOT / record['path']).read_bytes()).hexdigest() != record['sha256']:
                raise RuntimeError('Input changed during HTML validation')
        sonar.export(auth)
        sonar.save('execution-status.json', {'status': 'complete', 'current_results_valid': True, 'compute_engine_status': state, 'completed_at': dt.datetime.now().astimezone().isoformat(), 'scope': 'independent 4-file HTML verification; full repository rescan still pending'})
    except Exception as error:
        sonar.save('execution-status.json', {'status': 'failed', 'current_results_valid': False, 'error': str(error), 'failed_at': dt.datetime.now().astimezone().isoformat()})
        raise
    finally:
        sonar.request('/api/user_tokens/revoke', {'name': token_name}, auth, True)
        sonar.save('token-lifecycle.json', {'short_lived_scan_token_revoked': True, 'secret_value_persisted_in_reports': False})


if __name__ == '__main__':
    main()
