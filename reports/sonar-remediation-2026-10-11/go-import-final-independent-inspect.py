import difflib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
BASE = Path(__file__).parent
PATH = 'services/api-gateway/tools/import-fujian-2024-math-simulation/main_batch9_test.go'
before = Path('D:/tools/sonar-remediation-20261011/root-go-final-02-baseline') / PATH
after = ROOT / PATH
print(''.join(difflib.unified_diff(before.read_text(encoding='utf-8').splitlines(True), after.read_text(encoding='utf-8').splitlines(True), fromfile='checkpoint03-current', tofile='final-two-refactor', n=5)))
for name in ['root-go-final-02-baseline-execution.json', 'root-go-final-02-current-execution.json', 'root-go-final-02-baseline-result.json', 'root-go-final-02-current-result.json']:
    data = json.loads((BASE / name).read_text(encoding='utf-8-sig'))
    print(name, 'keys', list(data))
    print(json.dumps({key: val for key, val in data.items() if key not in ['source_sha_before', 'source_sha_after', 'source_sha_changes', 'tests', 'proofs', 'events', 'test_results', 'all_test_results']}, ensure_ascii=True)[:6000])
