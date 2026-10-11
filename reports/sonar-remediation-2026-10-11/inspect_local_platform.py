"""读取本地 Sonar API 能力和待复核证据，不改变平台问题状态。"""
from pathlib import Path
import hashlib
import importlib.util
import json

ROOT = Path(__file__).resolve().parents[2]
OUT = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location('local_sonar_reader', ROOT / 'reports/platform-audit-2026-10-10/sonar/run_local_sonar.py')
sonar = importlib.util.module_from_spec(spec)
spec.loader.exec_module(sonar)
sonar.OUT = OUT
credentials = json.loads(Path('D:/tools/sonarqube-yuejuan/private/credentials.json').read_text(encoding='utf-8-sig'))
auth = credentials['login'] + ':' + credentials['password']
metadata = sonar.request('/api/webservices/list', auth=auth)
selected = []
for service in metadata['webServices']:
    actions = [action for action in service['actions'] if action['key'] in {'transitions', 'do_transition', 'add_comment', 'show', 'change_status'}]
    if service['path'] in {'api/issues', 'api/hotspots'}:
        selected.append({'path': service['path'], 'actions': actions})
sonar.save('local-platform-workflow-api.json', selected)
python = json.loads((OUT / 'python-nonfix-evidence.json').read_text(encoding='utf-8'))['records']
frontend = json.loads((OUT / 'frontend-platform-disposition-evidence.json').read_text(encoding='utf-8'))['issues']
records = []
for row in python + frontend:
    sha = hashlib.sha256((ROOT / row['file']).read_bytes()).hexdigest()
    expected = row.get('current_sha256') or row['source_sha256']
    issue = sonar.request('/api/issues/search', {'issues': row['key'], 'ps': 10, 'additionalFields': '_all'}, auth)
    # 当前服务已移除旧的 transitions 查询；以它自身发布的 API 定义核对合法操作。
    transition_api = next(action for service in selected if service['path'] == 'api/issues' for action in service['actions'] if action['key'] == 'do_transition')
    supported = next(param['possibleValues'] for param in transition_api['params'] if param['key'] == 'transition')
    records.append({'key': row['key'], 'file': row['file'], 'rule': row['rule'], 'actual_sha256': sha, 'evidence_sha256': expected, 'source_matches_evidence': sha == expected, 'current_issue': issue['issues'], 'api_supported_transitions': supported, 'evidence_verdict': row.get('verdict'), 'reason': row['reason']})
sonar.save('platform-disposition-readiness.json', {'actions_performed': False, 'records': records})
hotspots = sonar.request('/api/hotspots/search', {'projectKey': sonar.PROJECT, 'ps': 500}, auth)
sonar.save('local-platform-hotspots-before.json', hotspots)
print(json.dumps({'readiness_records': len(records), 'hash_mismatches': [row['key'] for row in records if not row['source_matches_evidence']], 'hotspots': hotspots['paging']['total']}))
