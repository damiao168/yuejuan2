"""只读现有 Playwright trace，摘取浏览器异常；不运行浏览器或变更快照。"""
import json
import hashlib
from datetime import datetime, timezone
from pathlib import Path
import zipfile

ROOT = Path(__file__).resolve().parents[2]
TARGETS = [
    'dashboard-workbench-移动端只保留导航触发条，账户位于抽屉底部-chromium',
    'exam-workspace-考试工作区按四阶段导航，并让阻断项跳到可处理环节-chromium',
    'exam-workspace-左侧显示固定准备任务，右侧仅显示当前任务-chromium',
    'exam-create.visual-lightwe-ab063-mposer-is-stable-at-390x844-chromium',
]

entries = []
for target in TARGETS:
    path = ROOT / 'output/playwright/test-results' / target / 'trace.zip'
    result = []
    with zipfile.ZipFile(path) as archive:
        for name in archive.namelist():
            if not name.endswith(('.trace', '.network')):
                continue
            for line in archive.read(name).decode('utf-8').splitlines():
                event = json.loads(line)
                if event.get('type') == 'console' and event.get('messageType') == 'error':
                    result.append(event)
                elif event.get('type') == 'event' and event.get('method') in {'pageError', 'crash'}:
                    result.append(event)
                elif event.get('type') == 'resource-snapshot' and event.get('snapshot', {}).get('response', {}).get('status', 0) >= 400:
                    resource = event['snapshot']
                    result.append({'type': 'http_error', 'url': resource['request']['url'], 'status': resource['response']['status']})
    entries.append({
        'test': target,
        'trace_path': str(path),
        'trace_sha256': hashlib.sha256(path.read_bytes()).hexdigest(),
        'events': result,
        'compact_events': [{
            'type': event.get('type'),
            'text': event.get('text', '')[:550],
            'location': event.get('location'),
            'url': event.get('url'),
            'status': event.get('status'),
        } for event in result],
    })

report = {'recorded_at': datetime.now(timezone.utc).isoformat(),
          'method': '只读现有 trace.zip，未运行浏览器、未修改源码或快照。',
          'traces': entries}
output = Path(__file__).with_name('frontend-e2e-trace-readonly-evidence.json')
output.write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
for entry in entries:
    print(json.dumps({'test': entry['test'], 'events': entry['compact_events']}, ensure_ascii=False))
