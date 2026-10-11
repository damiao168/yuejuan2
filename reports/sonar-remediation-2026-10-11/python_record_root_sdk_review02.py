"""只读第二次 SDK 打开阶段拆分及原动态证据，保留第一轮独立审查。"""
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
REPORTS = Path(__file__).parent
source = ROOT / 'scripts/lib/openapi-sdk.mjs'
current_sha = hashlib.sha256(source.read_bytes()).hexdigest()
execution = json.loads((REPORTS / 'root-new-js-tests-02.json').read_text(encoding='utf-8-sig'))
assert current_sha == execution['source_sha256']['scripts/lib/openapi-sdk.mjs']
assert execution['exit_code'] == 0 and execution['sources_unchanged']
tap = (REPORTS / 'root-new-js-tests-02.tap').read_text(encoding='utf-8-sig')
assert all(marker in tap for marker in ['pass 18', 'fail 0', 'skipped 0'])
checks = [
    'openExistingArtifact 的 O_RDWR 成功只交出 {descriptor,created:false}；任何 fstat/read/write 异常仍由后续 writeIfChanged finally 关闭。',
    'ENOENT 唯一返回 undefined，允许当前轮尝试 wx+ 排他创建；null 唯一表示只读内容及路径 dev/ino 已核对、描述符已由 readonly helper 关闭。',
    '只读入口仍仅限 EACCES/EPERM；内容不同、非普通文件抛原权限错误，读取/stat/close 自身错误按原顺序传播。不会被外层排他创建 catch 误捕获。',
    'openArtifactForUpdate 只对 wx+ 的 EEXIST 继续，attempt 0/1/2 共三轮，第 3 次争用立即抛该 createError；不吞其他创建错误、不开第 4 轮、不用截断模式。',
    'writeIfChanged 对 null 返回 false；对已有/新建描述符仍按 fstat、FD 内容读取、0 位置短写补齐、ftruncate、路径身份验证、finally close 顺序执行，空文件 created 布尔语义不变。',
    '中文说明准确解释 undefined 与 null 的职责，未承诺路径校验后的全局排他保证。',
]
report = {
    'reviewed_at_utc': datetime.now(timezone.utc).isoformat(),
    'reviewer': '/root/sonar_python_fix',
    'source_writes_performed': False,
    'scope': '当前 openExistingArtifact/openArtifactForUpdate/readonlyArtifactUnchanged/verifyArtifactPath/writeIfChanged 完整源码及原权威基线 writeIfChanged；第一轮审查记录保留，未覆盖。',
    'previous_review': 'root-new-js-independent-review-python-agent.json',
    'file': str(source),
    'current_sha256': current_sha,
    'verdict': 'no_actionable_regression_found',
    'checks': checks,
    'existing_dynamic_evidence': {
        'report': 'root-new-js-tests-02.json', 'log': 'root-new-js-tests-02.tap',
        'exit_code': 0, 'passed': 18, 'failed': 0, 'skipped': 0,
        'current_sha_bound': True, 'tests_repeated_by_reviewer': False,
        'tap_sha256': hashlib.sha256((REPORTS / 'root-new-js-tests-02.tap').read_bytes()).hexdigest(),
    },
    'platform_verification': 'pending_root_final_scan',
}
(REPORTS / 'root-new-js-independent-review02-python-agent.json').write_text(
    json.dumps(report, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
lines = ['# SDK 第二轮打开阶段拆分：独立审查', '',
         f'未发现可执行回归。当前 SHA：`{current_sha}`。完整审读三态返回、三轮 EEXIST 重试、只读原错误传播、FD 所有权与失败关闭顺序；第一轮记录完整保留。', '']
lines += [f'- {check}' for check in checks]
lines += ['', 'root 原 18 Node 测试实际 exit 0，18 pass/0 fail/0 skip；02 报告 SHA 与当前一致。本审查只读原证据，不重复测试；平台仍待最终扫描。', '']
(REPORTS / 'root-new-js-independent-review02-python-agent.md').write_text('\n'.join(lines), encoding='utf-8')
print(json.dumps({'verdict': report['verdict'], 'sha256': current_sha, 'dynamic': '18 pass/0 skip; existing actual exit0 bound current SHA'}, ensure_ascii=False))
