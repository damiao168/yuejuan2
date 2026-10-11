import json
from pathlib import Path

BASE = Path(__file__).parent

def load(name):
    return json.loads((BASE / name).read_text(encoding='utf-8-sig'))

for name in ['go-progress.json', 'go-server-reliability-independent-review.json', 'go-server-batch04-skip-classification.json']:
    obj = load(name)
    print(name, 'keys:', list(obj))
    for key, value in obj.items():
        if key in ['issue_states', 'files', 'batches', 'reviews', 'reviewed_files', 'file_reviews']:
            print(' ', key, 'type', type(value).__name__, 'len', len(value))
            print('  samples', json.dumps(value[:2] if isinstance(value, list) else list(value.items())[:2], ensure_ascii=True))
        elif name != 'go-progress.json':
            print(' ', key, json.dumps(value, ensure_ascii=True)[:3500])

for path in sorted(BASE.glob('go-*-tests.json')):
    obj = load(path.name)
    print(path.name, 'keys:', list(obj), 'exit', obj.get('exit_code'), 'counts', obj.get('test_result_counts', obj.get('summary')))

for name in ['go-batch01-tests.json', 'go-batch13-21-tests.json', 'go-batch22-26-and-new-five-tests.json']:
    obj = load(name)
    print(name, json.dumps({key: val for key, val in obj.items() if key not in ['test_results', 'all_test_results']}, ensure_ascii=True)[:7000])

for name in ['go-batch13-21-source-before.json', 'go-batch22-26-and-new-five-source-before.json', 'go-server-batch04-source-sha.json']:
    path = BASE / name
    if path.exists():
        obj = load(name)
        print(name, 'type', type(obj).__name__, 'samples', json.dumps(list(obj.items())[:2], ensure_ascii=True)[:1500])
