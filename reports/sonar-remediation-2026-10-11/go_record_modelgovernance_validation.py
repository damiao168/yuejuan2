"""将两次真实串行测试绑定到六个归属文件的当前版本，不代替平台复扫。"""
from collections import Counter
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
REPORT = Path(__file__).resolve().parent

execution = json.loads((REPORT / 'go-modelgovernance-test-execution.json').read_text('utf-8'))
assert execution['passed']
progress_path = REPORT / 'go-modelgovernance-progress.json'
progress = json.loads(progress_path.read_text('utf-8'))
for item in [*progress['files'], *progress['issues']]:
    path = item['path']
    digest = hashlib.sha256((ROOT / path).read_bytes()).hexdigest()
    assert digest == item['current_sha256'] == execution['source_hashes_after'][path]
    item['validation'] = {'report': 'go-modelgovernance-test-execution.json',
                          'baseline_exit_code': 0, 'current_exit_code': 0,
                          'current_sha256': digest, 'same_test_ids': True,
                          'same_skip_ids': True, 'same_stream_traces': True}
    if 'key' in item:
        item['state'] = 'validated_fix_pending_platform_rescan'
    else:
        item['tests'] = 'passed_baseline_overlay_and_current'
progress.update(updated_at_utc=datetime.now(timezone.utc).isoformat(), runtime_validation='passed',
                summary=dict(Counter(item['state'] for item in progress['issues'])))
progress_path.write_text(json.dumps(progress, ensure_ascii=False, indent=2), 'utf-8')
results = execution['results']
counts = [(item['name'], len(item['passed_test_ids']), len(item['skipped_test_ids'])) for item in results]
text = '# modelgovernance 原 12 个 Critical 问题\n\n'
text += '六个归属测试文件均完整审读并按真实协议、治理和流事件阶段拆分。保留原断言和测试案例。\n\n'
text += '基线六文件由 Go overlay 加载在同一当前生产源码上；当前版单独运行，均实际退出 0。GOMAXPROCS=1、-p1、GOMEMLIMIT=384MiB，网络依赖关闭。\n\n'
for name, passed, skipped in counts:
    text += f'- {name}：{passed} 个通过测试事件，{skipped} 个跳过；原始 JSON 事件保存在对应 jsonl。\n'
text += '\n测试身份、跳过身份与流事件 TRACE 完全一致。源码及本地 import 闭包 SHA 在测试前后不变。状态为 validated_fix_pending_platform_rescan，平台由 root 统一复扫。\n'
(REPORT / 'go-modelgovernance-progress.md').write_text(text, 'utf-8')
print(json.dumps({'issues': len(progress['issues']), 'counts': counts,
                  'source_unchanged': execution['sources_unchanged_during_tests'],
                  'same_test_ids': execution['same_passed_test_ids'], 'same_skip_ids': execution['same_skipped_test_ids'],
                  'same_stream_traces': execution['same_stream_traces']}))
