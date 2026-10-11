"""冻结 server 字节后运行现有测试，逐 Test 保存真实结果与条件跳过。"""
from pathlib import Path
import collections
import datetime
import hashlib
import json
import os
import subprocess
import sys
import time

root = Path('D:/project/yuejuan')
report = root / 'reports/sonar-remediation-2026-10-11'
batch = sys.argv[1]
scope = root / 'services/api-gateway/internal/server'
files = sorted(scope.glob('*.go'))
hashes = {str(path.relative_to(root)).replace('\\', '/'): hashlib.sha256(path.read_bytes()).hexdigest() for path in files}
frozen = report / (batch + '-source-sha.json')
frozen.write_text(json.dumps({'frozen_at': datetime.datetime.now().astimezone().isoformat(), 'files': hashes}, indent=2) + '\n', encoding='utf-8')
env = os.environ.copy()
cleared = []
for name in list(env):
    if name.startswith(('EDUGRADE_', 'OPENAI_', 'ANTHROPIC_', 'SEMGRAP_')) or name in {'DATABASE_URL', 'PGHOST', 'PGPORT', 'PGUSER', 'PGPASSWORD', 'PGDATABASE'}:
        cleared.append(name)
        del env[name]
env.update(GOTOOLCHAIN='local', GOPROXY='off', GOSUMDB='off', GOMAXPROCS='1', GOGC='50')
command = ['D:/tools/go/bin/go.exe', 'test', '-json', '-p', '1', '-count=1', '-timeout=5m', './internal/server']
started = datetime.datetime.now().astimezone().isoformat()
tick = time.monotonic()
log = report / (batch + '-events.jsonl')
with log.open('w', encoding='utf-8') as out:
    process = subprocess.Popen(command, cwd=root / 'services/api-gateway', env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True, encoding='utf-8', errors='replace')
    for line in process.stdout:
        out.write(line)
        out.flush()
    code = process.wait()
rows = []
for line in log.read_text(encoding='utf-8').splitlines():
    try:
        rows.append(json.loads(line))
    except json.JSONDecodeError:
        pass
counts = collections.Counter(row['Action'] for row in rows if row.get('Test') and row.get('Action') in {'pass', 'fail', 'skip'})
results = [row for row in rows if row.get('Action') in {'pass', 'fail', 'skip'}]
changed = [path for path, sha in hashes.items() if hashlib.sha256((root / path).read_bytes()).hexdigest() != sha]
summary = {'started_at': started, 'completed_at': datetime.datetime.now().astimezone().isoformat(), 'duration_seconds': time.monotonic() - tick, 'command': command, 'exit_code': code, 'GOMAXPROCS': 1, 'cleared_environment_names': sorted(cleared), 'test_result_counts': dict(counts), 'all_test_results': results, 'source_sha_file': frozen.name, 'source_changed_during_test': changed, 'event_log': log.name, 'note': '环境缺失的 PostgreSQL/模型条件 skip 不计为集成通过；原始输出保留原因。'}
(report / (batch + '-tests.json')).write_text(json.dumps(summary, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
print(json.dumps({'batch': batch, 'exit_code': code, 'test_result_counts': dict(counts), 'source_changed_during_test': changed}))
sys.exit(code)
