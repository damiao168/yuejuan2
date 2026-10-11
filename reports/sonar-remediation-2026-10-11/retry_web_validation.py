"""在单worker下重验既有前端测试，保留原失败并绑定当前源码。"""
from pathlib import Path
from datetime import datetime
import hashlib
import json
import os
import subprocess
import time

ROOT = Path(__file__).resolve().parents[2]
OUT = Path(__file__).resolve().parent


def sources():
    records = {}
    for directory in ('apps/web-admin/src', 'packages/sdk/src', 'packages/ui/src', 'packages/shared-types/src', 'packages/design-tokens/src'):
        for file in (ROOT / directory).rglob('*'):
            if file.is_file() and file.suffix in {'.ts', '.tsx', '.css', '.json'}:
                records[file.relative_to(ROOT).as_posix()] = hashlib.sha256(file.read_bytes()).hexdigest()
    return records


def main():
    result = OUT / 'frontend-web-retry-01-execution.json'
    if result.exists():
        raise RuntimeError('Preserve prior validation evidence')
    old = json.loads((OUT / 'frontend-web-execution.json').read_text(encoding='utf-8'))[0]['command']
    files = old[old.index('--') + 1:]
    files = [item for item in files if not item.startswith('--')]
    before = sources()
    environment = dict(os.environ)
    environment['NODE_OPTIONS'] = '--max-old-space-size=384'
    command = ['D:/node.js/node.exe', str(ROOT / 'node_modules/vitest/vitest.mjs'), 'run', *files, '--maxWorkers=1', '--no-file-parallelism']
    started = datetime.now().astimezone().isoformat()
    clock = time.monotonic()
    with (OUT / 'frontend-web-retry-01.log').open('w', encoding='utf-8') as log:
        code = subprocess.run(command, cwd=ROOT / 'apps/web-admin', env=environment, stdout=log, stderr=subprocess.STDOUT).returncode
    after = sources()
    changed = [file for file in sorted(before.keys() | after.keys()) if before.get(file) != after.get(file)]
    report = {'command': command, 'tests_requested': len(files), 'started_at': started, 'completed_at': datetime.now().astimezone().isoformat(), 'duration_seconds': time.monotonic() - clock, 'exit_code': code, 'node_heap_mib': 384, 'worker_count': 1, 'source_sha_before': before, 'source_sha_after': after, 'changed_sources_during_tests': changed, 'prior_failure_preserved': 'frontend-web-0.log', 'result_valid_for_current_sources': code == 0 and not changed}
    result.write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding='utf-8')
    print(json.dumps({'exit_code': code, 'changed_source_count': len(changed), 'tests_requested': len(files)}))
    raise SystemExit(code)


if __name__ == '__main__':
    main()
