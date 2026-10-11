"""顺序运行基线测试覆盖与当前测试，保留Go实际退出码/JSON事件，不写源码或数据库。"""
from datetime import datetime, timezone
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import time

ROOT = Path(__file__).resolve().parents[2]
REPORT = Path(__file__).resolve().parent
MODULE = ROOT / 'services/api-gateway'
BACKUP = Path('D:/tools/sonar-remediation-20261011/baseline')
GO = Path('D:/tools/go/bin/go.exe')


def manifest():
    prefix = 'edugrade-enterprise/services/api-gateway/'
    pending, visited, paths = [MODULE / 'internal/modelgovernance'], set(), []
    # 将本地测试依赖也纳入闭包；不把未参与该包编译的其他智能体文件变更误记为测试输入改变。
    while pending:
        directory = pending.pop()
        if directory in visited:
            continue
        visited.add(directory)
        for path in directory.glob('*.go'):
            paths.append(path)
            source = path.read_text('utf-8-sig')
            imports = '\n'.join(re.findall(r'^import\s*\(([\s\S]*?)^\)', source, re.M))
            imports += '\n' + '\n'.join(re.findall(r'^import\s+(?:\w+\s+)?("[^"\n]+")', source, re.M))
            for item in re.findall(r'"([^"\n]+)"', imports):
                if item.startswith(prefix):
                    pending.append(MODULE / item[len(prefix):])
    paths += [MODULE / 'go.mod', MODULE / 'go.sum']
    return {path.relative_to(ROOT).as_posix(): hashlib.sha256(path.read_bytes()).hexdigest() for path in sorted(paths)}


def parse_events(path):
    passed, skipped, failed, traces = [], [], [], {}
    package_pass = False
    events_count = 0
    for line in path.read_text('utf-8', errors='replace').splitlines():
        try:
            event = json.loads(line)
        except json.JSONDecodeError:
            continue
        events_count += 1
        action, test = event.get('Action'), event.get('Test')
        if action == 'pass' and test:
            passed.append(test)
        elif action == 'skip' and test:
            skipped.append(test)
        elif action == 'fail':
            failed.append(test or event.get('Package'))
        elif action == 'pass' and not test:
            package_pass = True
        match = re.search(r'TRACE (.+)', event.get('Output', '').strip())
        if match and test:
            traces[test] = match[1]
    return {'passed_test_ids': sorted(passed), 'skipped_test_ids': sorted(skipped), 'failed_ids': failed,
            'stream_trace_by_test': traces, 'package_pass_event': package_pass, 'json_events': events_count}


def main():
    progress = json.loads((REPORT / 'go-modelgovernance-progress.json').read_text('utf-8'))
    overlay = {'Replace': {str(ROOT / item['path']): str(BACKUP / item['path']) for item in progress['files']}}
    overlay_path = REPORT / 'go-modelgovernance-baseline-overlay.json'
    overlay_path.write_text(json.dumps(overlay, ensure_ascii=False, indent=2), 'utf-8')
    before = manifest()
    environment = os.environ.copy()
    removed = [name for name in environment if name.startswith('EDUGRADE_')]
    for name in removed:
        environment.pop(name)
    environment.update(GOMAXPROCS='1', GOMEMLIMIT='384MiB', CGO_ENABLED='0', GOPROXY='off',
                       GOSUMDB='off', GOTOOLCHAIN='local', GOFLAGS='')
    version = subprocess.run([str(GO), 'version'], cwd=MODULE, env=environment, capture_output=True, text=True, timeout=30)
    results = []
    for name in ('baseline-overlay', 'current'):
        command = [str(GO), 'test', '-p', '1', '-count=1', '-json']
        if name == 'baseline-overlay':
            command += ['-overlay', str(overlay_path)]
        command += ['./internal/modelgovernance']
        output_path = REPORT / ('go-modelgovernance-' + name + '-test.jsonl')
        started = datetime.now(timezone.utc).isoformat()
        before_clock = time.monotonic()
        with output_path.open('w', encoding='utf-8', newline='') as stream:
            completed = subprocess.run(command, cwd=MODULE, env=environment, stdout=stream, stderr=subprocess.STDOUT, timeout=600)
        results.append({'name': name, 'command': command, 'started_at_utc': started,
                        'finished_at_utc': datetime.now(timezone.utc).isoformat(), 'exit_code': completed.returncode,
                        'duration_seconds': time.monotonic() - before_clock, 'output': output_path.name,
                        **parse_events(output_path)})
        print(name, 'exit', completed.returncode, 'passed', len(results[-1]['passed_test_ids']), 'seconds', round(results[-1]['duration_seconds'], 2), flush=True)
        partial = {'go_version_exit_code': version.returncode, 'go_version': version.stdout.strip(),
                   'results': results, 'source_hashes_before': before,
                   'environment': {key: environment[key] for key in ('GOMAXPROCS', 'GOMEMLIMIT', 'CGO_ENABLED', 'GOPROXY', 'GOSUMDB', 'GOTOOLCHAIN', 'GOFLAGS')},
                   'removed_environment_names': removed,
                   'manifest_scope': '当前包及本地import闭包（包含测试）；外部依赖绑定go.mod/go.sum，网络取依赖关闭',
                   'scope': '同一当前生产源码下，基线六测试文件通过Go overlay加载，再运行当前六文件；不替代真实学校数据或模型验收'}
        (REPORT / 'go-modelgovernance-test-execution.json').write_text(json.dumps(partial, ensure_ascii=False, indent=2), 'utf-8')
        if completed.returncode != 0:
            break
    after = manifest()
    partial.update(source_hashes_after=after, sources_unchanged_during_tests=before == after)
    both = len(results) == 2
    partial['same_passed_test_ids'] = both and results[0]['passed_test_ids'] == results[1]['passed_test_ids']
    partial['same_skipped_test_ids'] = both and results[0]['skipped_test_ids'] == results[1]['skipped_test_ids']
    partial['same_stream_traces'] = both and results[0]['stream_trace_by_test'] == results[1]['stream_trace_by_test']
    partial['passed'] = (both and all(item['exit_code'] == 0 and item['package_pass_event'] for item in results)
                         and partial['sources_unchanged_during_tests'] and partial['same_passed_test_ids']
                         and partial['same_skipped_test_ids'] and partial['same_stream_traces'])
    (REPORT / 'go-modelgovernance-test-execution.json').write_text(json.dumps(partial, ensure_ascii=False, indent=2), 'utf-8')
    return 0 if partial['passed'] else 1


if __name__ == '__main__':
    raise SystemExit(main())
