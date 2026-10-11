"""冻结当前本地源码后完整复扫；保留原项目、规则与排除策略。"""
from pathlib import Path
import argparse
import csv
import datetime as dt
import fnmatch
import hashlib
import importlib.util
import json
import os
import secrets
import shutil
import subprocess
import time

ROOT = Path(__file__).resolve().parents[2]
OUT = Path(__file__).resolve().parent / 'full-checkpoint-01'
RUNTIME = Path('D:/tools/sonar-remediation-20261011/full-checkpoint-01')
SNAPSHOT = RUNTIME / 'workspace'
PROJECT = 'yuejuan-local-workspace-20261010'


def main():
    global OUT, RUNTIME, SNAPSHOT
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--attempt', type=int, default=1)
    parser.add_argument('--java-heap-mib', type=int, default=512)
    args = parser.parse_args()
    if not 1 <= args.attempt <= 99 or not 512 <= args.java_heap_mib <= 4096:
        parser.error('attempt must be 1..99; Java heap must be 512..4096 MiB')
    # 每次重试使用独立目录，保留失败日志及其冻结源码，不覆盖旧的扫描证据。
    OUT = Path(__file__).resolve().parent / f'full-checkpoint-{args.attempt:02d}'
    RUNTIME = Path('D:/tools/sonar-remediation-20261011') / OUT.name
    SNAPSHOT = RUNTIME / 'workspace'
    if OUT.exists() or SNAPSHOT.exists():
        raise RuntimeError('Keep previous checkpoint evidence; use a fresh checkpoint directory')
    OUT.mkdir()
    SNAPSHOT.mkdir(parents=True)
    original = ROOT / 'reports/platform-audit-2026-10-10/sonar'
    base = (original / 'local-scan.properties').read_text(encoding='utf-8')
    properties = dict(line.split('=', 1) for line in base.splitlines() if '=' in line and not line.lstrip().startswith('#'))
    excludes = properties['sonar.exclusions'].split(',')
    with (original.parent / 'source-manifest-resume-2026-10-11.csv').open(encoding='utf-8-sig', newline='') as handle:
        previous = list(csv.DictReader(handle))
    previous_paths = {row['path'] for row in previous}
    extensions = {Path(path).suffix for path in previous_paths}
    extensionless_names = {Path(path).name for path in previous_paths if not Path(path).suffix}
    git = subprocess.run(['git', 'ls-files', '--cached', '--others', '--exclude-standard', '-z'], cwd=ROOT, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=True)
    candidates = {path.decode('utf-8').replace('\\', '/') for path in git.stdout.split(b'\0') if path}
    candidates.update(previous_paths)
    maintained = []
    missing_previous = []
    for path in sorted(candidates):
        local = ROOT / path
        if path in previous_paths and not local.is_file():
            missing_previous.append(path)
            continue
        if not local.is_file() or any(fnmatch.fnmatchcase(path, pattern) or fnmatch.fnmatchcase('/' + path, pattern) for pattern in excludes):
            continue
        if path not in previous_paths and Path(path).suffix not in extensions and Path(path).name not in extensionless_names:
            continue
        maintained.append(path)
    original_issues = json.loads((original / 'issues.json').read_text(encoding='utf-8'))['issues']
    issue_paths = {issue['component'].split(':', 1)[1] for issue in original_issues}
    if issue_paths - set(maintained):
        raise RuntimeError('Original issue files missing from maintained scope: ' + repr(sorted(issue_paths - set(maintained))))
    records = []
    for path in maintained:
        raw = (ROOT / path).read_bytes()
        target = SNAPSHOT / path
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(raw)
        records.append({'path': path, 'bytes': len(raw), 'sha256': hashlib.sha256(raw).hexdigest(), 'manual_review_status': 'pending unless separately documented'})
    # 类型分析需要配置和生成的声明；它们仍受原扫描排除策略约束。
    support = []
    for path in sorted(candidates):
        if path in maintained:
            continue
        needed = Path(path).name in {'package.json', 'tsconfig.json', 'tsconfig.node.json'} or path.startswith('packages/sdk/src/generated/') and Path(path).suffix in {'.ts', '.tsx'}
        if needed and (ROOT / path).is_file():
            target = SNAPSHOT / path
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(ROOT / path, target)
            support.append(path)
    if (ROOT / 'node_modules').is_dir():
        # junction只供分析器读取现有依赖，不复制或修改第三方包。
        linked = subprocess.run(['cmd.exe', '/d', '/c', 'mklink', '/J', str(SNAPSHOT / 'node_modules'), str(ROOT / 'node_modules')], stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        if linked.returncode != 0:
            raise RuntimeError('Could not link existing dependency runtime for snapshot analysis')
    changed_during_copy = [row['path'] for row in records if hashlib.sha256((ROOT / row['path']).read_bytes()).hexdigest() != row['sha256']]
    if changed_during_copy:
        raise RuntimeError('Workspace changed while source was frozen: ' + repr(changed_during_copy))
    tests = [path for path in maintained if path.endswith(('_test.go', '.test.ts', '.test.tsx', '.spec.ts', '.spec.tsx', '.test.mjs', '.test.cjs')) or '/tests/' in '/' + path or Path(path).name.startswith('test_') and path.endswith('.py')]
    properties.update({
        'sonar.projectKey': PROJECT,
        'sonar.projectVersion': '2026-10-11-remediation-checkpoint',
        'sonar.inclusions': ','.join(maintained),
        'sonar.test.inclusions': ','.join(tests),
        'sonar.working.directory': (RUNTIME / 'scanner-work').as_posix(),
        'sonar.nodejs.executable': 'D:/node.js/node.exe',
        'sonar.javascript.node.maxspace': '1536',
        'sonar.python.analysis.parallel': 'false',
        'sonar.typescript.tsconfigPaths': 'apps/desktop-client/tsconfig.json,apps/student-portal/tsconfig.json,apps/web-admin/tsconfig.json,packages/sdk/tsconfig.json',
    })
    config = OUT / 'effective-scan.properties'
    config.write_text('\n'.join(f'{k}={v}' for k, v in properties.items()) + '\n', encoding='utf-8')
    scope = {'snapshot_at': dt.datetime.now().astimezone().isoformat(), 'snapshot': str(SNAPSHOT), 'original_project': PROJECT, 'same_original_exclusions': properties['sonar.exclusions'] == dict(line.split('=', 1) for line in base.splitlines() if line.startswith('sonar.exclusions='))['sonar.exclusions'], 'original_issue_files_included': len(issue_paths), 'files': records, 'maintained_files': len(maintained), 'new_maintained_paths': sorted(set(maintained) - previous_paths), 'missing_previous_paths': missing_previous, 'type_resolution_support_files': support, 'test_paths': tests, 'scope_note': 'Full current maintained local workspace frozen for a checkpoint. Later workspace changes require a subsequent final scan.'}
    (OUT / 'source-scope.json').write_text(json.dumps(scope, ensure_ascii=False, indent=2), encoding='utf-8')
    spec = importlib.util.spec_from_file_location('original_local_sonar_export', original / 'run_local_sonar.py')
    sonar = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(sonar)
    sonar.OUT, sonar.PROJECT = OUT, PROJECT
    credentials = json.loads(Path('D:/tools/sonarqube-yuejuan/private/credentials.json').read_text(encoding='utf-8-sig'))
    auth = credentials['login'] + ':' + credentials['password']
    sonar.save('execution-status.json', {'status': 'running', 'current_results_valid': False, 'started_at': scope['snapshot_at']})
    sonar.save('quality-profiles-before.json', sonar.request('/api/qualityprofiles/search', {'project': PROJECT}, auth))
    token_name = 'full-remediation-checkpoint-' + secrets.token_hex(6)
    token = sonar.request('/api/user_tokens/generate', {'name': token_name, 'type': 'USER_TOKEN'}, auth, True)['token']
    try:
        env = dict(os.environ)
        env.update({'SONAR_TOKEN': token, 'SONAR_HOST_URL': sonar.URL, 'JAVA_HOME': 'D:/xic/tps/win64/jre21.0.1_12', 'SONAR_USER_HOME': 'D:/tools/sonarqube-yuejuan/20261010/scanner-cache'})
        env['SONAR_SCANNER_OPTS'] = '-Xmx48m -Xms16m -XX:+UseSerialGC -XX:ReservedCodeCacheSize=24m -XX:ActiveProcessorCount=1'
        env['SONAR_SCANNER_JAVA_OPTS'] = f'-Xmx{args.java_heap_mib}m -Xms32m -Xss512k -XX:+UseSerialGC -XX:MinHeapFreeRatio=10 -XX:MaxHeapFreeRatio=20 -XX:-ShrinkHeapInSteps -XX:TieredStopAtLevel=1 -XX:ReservedCodeCacheSize=48m -XX:ActiveProcessorCount=1'
        command = ['cmd.exe', '/d', '/c', 'D:/tools/sonarqube-yuejuan/20261010/sonar-scanner-8.1.0.6389/bin/sonar-scanner.bat', '-Dproject.settings=' + str(config), '-Dsonar.projectBaseDir=' + str(SNAPSHOT), '-Dsonar.scanner.metadataFilePath=' + str(OUT / 'report-task.txt')]
        sonar.save('scanner-command.json', {'command': command, 'network': '127.0.0.1 only for Sonar project; existing cached tool/runtime', 'source': 'Frozen full local workspace including all original issue files and new maintained source files', 'short_lived_token': 'environment only; revoked in finally', 'java_heap_mib': args.java_heap_mib, 'node_heap_mib': 1536})
        with (OUT / 'scanner.log').open('w', encoding='utf-8') as log:
            code = subprocess.run(command, cwd=SNAPSHOT, env=env, stdout=log, stderr=subprocess.STDOUT).returncode
        sonar.save('scanner-exit.json', {'exit_code': code})
        receipt = OUT / 'report-task.txt'
        if code != 0 or not receipt.exists():
            raise RuntimeError('Full checkpoint scanner failed or has no fresh receipt')
        task_id = dict(line.split('=', 1) for line in receipt.read_text().splitlines() if '=' in line)['ceTaskId']
        deadline = time.monotonic() + 900
        state = 'PENDING'
        while time.monotonic() < deadline:
            task = sonar.request('/api/ce/task', {'id': task_id}, auth)
            sonar.save('compute-engine-task.json', task)
            state = task['task']['status']
            if state in {'SUCCESS', 'FAILED', 'CANCELED'}:
                break
            time.sleep(5)
        if state != 'SUCCESS':
            raise RuntimeError('Full checkpoint compute engine did not succeed: ' + state)
        sonar.export(auth)
        statuses = []
        for start in range(0, len(original_issues), 50):
            keys = [issue['key'] for issue in original_issues[start:start + 50]]
            result = sonar.request('/api/issues/search', {'issues': ','.join(keys), 'ps': 100, 'additionalFields': '_all'}, auth)
            statuses.extend(result['issues'])
        sonar.save('original-issue-current-statuses.json', {'requested': len(original_issues), 'returned': len(statuses), 'issues': statuses})
        current_delta = [row['path'] for row in records if not (ROOT / row['path']).is_file() or hashlib.sha256((ROOT / row['path']).read_bytes()).hexdigest() != row['sha256']]
        sonar.save('workspace-after-snapshot.json', {'changed_paths': current_delta, 'frozen_snapshot_still_valid': True, 'current_workspace_matches_snapshot': not current_delta, 'final_scan_required_after_remaining_repairs': True})
        sonar.save('execution-status.json', {'status': 'complete', 'current_results_valid': True, 'compute_engine_status': state, 'completed_at': dt.datetime.now().astimezone().isoformat(), 'scope': 'full maintained local frozen checkpoint; completion of all repairs still requires audit and any later final scan'})
    except Exception as error:
        sonar.save('execution-status.json', {'status': 'failed', 'current_results_valid': False, 'error': str(error), 'failed_at': dt.datetime.now().astimezone().isoformat()})
        raise
    finally:
        sonar.request('/api/user_tokens/revoke', {'name': token_name}, auth, True)
        sonar.save('token-lifecycle.json', {'short_lived_scan_token_revoked': True, 'secret_value_persisted_in_reports': False})


if __name__ == '__main__':
    main()
