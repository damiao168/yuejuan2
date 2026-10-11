"""汇总人工图像/代码审查与现有日志；不运行测试或修改产品源码。"""
from datetime import datetime, timezone
import hashlib
import json
from pathlib import Path
import re
import struct

ROOT = Path(__file__).resolve().parents[2]
REPORTS = Path(__file__).parent
RESULTS = ROOT / 'output/playwright/test-results'
visual = RESULTS / 'exam-create.visual-lightwe-ab063-mposer-is-stable-at-390x844-chromium'
log_path = REPORTS / 'frontend-e2e-0.log'
log = re.sub(r'\x1b\[[0-?]*[ -/]*[@-~]', '', log_path.read_text(encoding='utf-8-sig'))
blocks = re.split(r'(?m)^\s*(\d+)\) \[chromium\] › ', log)
failures = []
assert len(blocks) == 21, '必须保存该中断日志实际存在的全部 10 个失败。'
details = [
    ('browser_context_closed', '基础设施',
     'browserContext.newPage 时浏览器/上下文已关闭；GPU CommandBuffer 创建失败。没有执行应用断言，日志不能证明具体 OOM 原因。',
     '资源空闲时串行重跑首页创建完整流程，保留真实退出码和 trace。'),
    ('assertion_blocked_by_resource_failure', '资源失败导致断言超时',
     '截图与 error-context 只有“正在加载 EduGrade，请稍候…”；trace 中 zIndex.ts、schoolAssistant.ts 均 ERR_INSUFFICIENT_RESOURCES。应用外壳尚未成功加载，不能据此判定正常页面移除了 mobile-shellbar。本次测试把 networkidle 改为 domcontentloaded，后续仅有默认 5 秒 visible 等待；健康环境需补验证冷启动等待是否足够。',
     '串行重跑移动端导航/抽屉账户场景，检查模块请求无资源错误并确保外壳已加载后验证 48px 和抽屉账户；保留等待阶段证据。'),
    ('worker_terminated', '基础设施',
     'worker process exited unexpectedly，退出码 3221226505（0xC0000409），没有业务断言失败栈；不能仅凭此退出码称为 OOM。',
     '资源空闲时串行重跑资料优先创建，确认空试卷结构和进入资料配置行为。'),
    ('browser_spawn_failed', '基础设施',
     'browserType.launch: spawn UNKNOWN，浏览器启动失败，尚未进入应用。',
     '串行重跑班级/教师成员管理入口。'),
    ('browser_context_closed', '基础设施',
     'browser.newContext 时浏览器/上下文已关闭，未验证方案抽屉业务断言。',
     '串行重跑方案抽屉带入科目结构。'),
    ('browser_context_closed', '基础设施',
     '1440x900 用例 browser.newContext 已关闭，未获得有效像素比较结果。',
     '1440x900 重新验证布局、导航、无横向溢出与原像素基线；本日志不能认定该屏通过。'),
    ('navigation_resource_failure', '基础设施',
     '1024x768 用例 page.goto 直接 net::ERR_INSUFFICIENT_RESOURCES，未达到像素断言。',
     '1024x768 重新验证布局、导航、无横向溢出与原像素基线；本日志不能认定该屏通过。'),
    ('preexisting_visual_baseline_mismatch', '真实既有截图差异',
     '390x844 两次稳定截图在 fonts loaded、animations disabled 后均差 18038 像素（日志 ratio 0.06）。expected 为旧返回考试列表外壳、页面标题和创建方式题目；actual 为汉堡导航/学校上下文，缺旧标题，首卡改为“上传完整试卷识别题目与分值，无需预填题目数量”，卡片及基本信息约上移 175px。基线 Composer 已包含这套文案和无旧标题的结构；当前仅两个未渲染的 SubjectCard 数值框 addonAfter→suffix。AppShell、外壳展示组件与 CSS 在扫描前快照和当前完全相同。因此是本次接手前已存在的截图/工作区设计差异，不能作为本轮 Sonar 修复回归，也不能当作通过。',
     '在串行健康环境先对扫描前只读快照和当前分别使用同一既有 Windows 基线复验 390x844；另验证有选科时 suffix 的尺寸/可读性。保留差异供既有设计验收，不自动更新快照。'),
    ('preexisting_ui_assertion_mismatch', '真实既有断言不一致',
     '页面完整渲染、trace 无 console/page error；测试要求“考试流程” navigation 内 4 按钮，实际源码明确 stageRail={null}，准备区域有 5 个“考试准备”任务。当前 ExamWorkspaceLayout 与 preparationSteps 和扫描前快照逐字节相同；本轮测试仅另一个学科切换用例改 waitUntil，不改此场景。它是接手前即有的 UI/断言契约差异。后面的跳转断言因首个断言失败尚未运行。',
     '同一健康浏览器基线/当前分别复验四阶段用例；确认既有导航产品意图，再在独立变更中修正 UI 或验收契约。当前报告保留失败，不能删除断言或称业务链路通过。'),
    ('assertion_blocked_by_resource_failure', '资源失败导致断言超时',
     '截图为考试外壳与“页面出现异常” ErrorBoundary；trace 中 papers.ts、assessment.ts 报 ERR_INSUFFICIENT_RESOURCES，随后 ExamPreparationPage.tsx 动态导入 fetch 失败。不是正常确认页缺失标题的证据。该断言设置 60 秒等待，但用例总超时仍 30 秒，实际先触发总超时。',
     '资源空闲时串行重跑准备确认任务，确认模块加载成功并验证标题、5 项任务、阻断项跳转和学生范围。若仍超时再针对用例总超时与步骤等待调查。'),
]
for index, (classification, category, cause, rerun) in enumerate(details, 1):
    assert int(blocks[index * 2 - 1]) == index
    block = blocks[index * 2]
    title = block.splitlines()[0].strip().rstrip('─ ').strip()
    errors = [line.strip() for line in block.splitlines() if line.strip().startswith(('Error:', 'Test timeout'))]
    failures.append({'failure_index': index, 'test_from_log': title,
                     'classification': classification, 'category': category,
                     'error_lines': errors, 'cause': cause, 'required_revalidation': rerun,
                     'status': 'failed_in_interrupted_run_revalidation_pending'})

images = []
image_paths = [visual / f'exam-create-390x844-{kind}.png' for kind in ['expected', 'actual', 'diff']]
image_paths += [RESULTS / case / 'test-failed-1.png' for case in [
    'dashboard-workbench-移动端只保留导航触发条，账户位于抽屉底部-chromium',
    'exam-workspace-考试工作区按四阶段导航，并让阻断项跳到可处理环节-chromium',
    'exam-workspace-左侧显示固定准备任务，右侧仅显示当前任务-chromium',
]]
for path in image_paths:
    data = path.read_bytes()
    assert data.startswith(b'\x89PNG\r\n\x1a\n')
    width, height = struct.unpack('>II', data[16:24])
    images.append({'path': str(path), 'sha256': hashlib.sha256(data).hexdigest(),
                   'width': width, 'height': height, 'review': '使用 view_image 实际看图，未修改图片'})
repository_expected = ROOT / 'tests/e2e/specs/exam-create.visual.spec.ts-snapshots/exam-create-390x844-chromium-win32.png'
assert images[0]['sha256'] == hashlib.sha256(repository_expected.read_bytes()).hexdigest()

scope = [
    ('apps/web-admin/src/components/AppLayout.tsx', 'full_current_source_review', '当前完整外壳和扫描前差异：仅 SubmitEvent 类型迁移。'),
    ('apps/web-admin/src/components/appLayoutPresentation.tsx', 'full_current_source_review', '三种外壳分支、移动顶部上下文与导航；扫描前字节相同。'),
    ('apps/web-admin/src/features/exams/workspace/ExamWorkspaceLayout.tsx', 'full_current_source_review', '阶段栏空值、5 项准备任务与失败边界；扫描前字节相同。'),
    ('apps/web-admin/src/features/exams/preparation/preparationSteps.ts', 'full_current_source_review', '4 个准备任务加确认完成入口；扫描前字节相同。'),
    ('apps/web-admin/src/features/exams/create/exam-create.css', 'full_current_source_review', '全部选择器和断点；无隐藏旧标题的 CSS 规则，扫描前字节相同。'),
    ('apps/web-admin/src/experience.css', 'full_current_source_review', '移动外壳上下文和布局；扫描前字节相同。'),
    ('apps/web-admin/src/features/exams/create/CreateExamComposer.tsx', 'focused_source_and_full_baseline_diff_review_remainder_pending', '创建方式数组、主渲染开头、SubjectCard、底部动作；逐行差异仅两个 suffix，未将自动比较计为完整人工审读。'),
    ('apps/web-admin/src/AppShell.tsx', 'focused_source_review_remainder_pending', 'AppLayout 调用、shellMode 决策；整个文件字节与扫描前相同仅是自动证据。'),
    ('tests/e2e/specs/exam-create.visual.spec.ts', 'full_current_source_review', '全部三屏等待、布局、字体、像素断言与旧版本差异。'),
    ('tests/e2e/specs/dashboard-workbench.spec.ts', 'full_current_source_review', '全部当前用例；重点移动启动等待和创建流程。'),
    ('tests/e2e/specs/exam-workspace.spec.ts', 'focused_source_review_remainder_pending', '完整两个失败用例和整个文件差异；不把未读其余用例计为完成。'),
]
interrupted = json.loads((REPORTS / 'frontend-e2e-interrupted.json').read_text(encoding='utf-8-sig'))
report = {
    'recorded_at': datetime.now(timezone.utc).isoformat(),
    'scope': '只读原 E2E 日志、既有图片/trace、接手基线和扫描前本地快照；仅写独立证据，不运行浏览器/Go/大型测试，不改源码或快照。',
    'test_run_status': 'interrupted_not_a_pass',
    'original_run': interrupted,
    'log_sha256': hashlib.sha256(log_path.read_bytes()).hexdigest(),
    'observed_test_start_markers': max(map(int, re.findall(r'\[(\d+)/47\]', log))),
    'counts': {'logged_failures': 10, 'preexisting_substantive_assertion_failures': 2,
               'resource_blocked_assertion_failures': 2, 'browser_process_navigation_failures': 6},
    'limitations': ['47 用例只有启动到 23 的日志，无最终 summary 或成功退出；不能推断未记录用例通过。',
                    '资源不足错误有直接 trace 证据；其他关闭/spawn/worker 故障不统一断言为 OOM。',
                    '只读审查不能替代健康环境重跑；既有 UI/测试不一致仍待产品验收决定，未更新任何快照。'],
    'baseline_evidence': 'frontend-e2e-baseline-readonly-evidence.json',
    'trace_evidence': 'frontend-e2e-trace-readonly-evidence.json',
    'images': images,
    'expected_image_matches_repository_snapshot': True,
    'file_review_status': [{'path': path, 'status': status, 'reviewed_scope': reviewed} for path, status, reviewed in scope],
    'failures': failures,
    'necessary_rerun_scope': '先串行重跑上述 10 个失败场景；随后完成原 9 spec/47 个用例的剩余验收并记录最终退出码。390 和 4 阶段断言应同时对相同本地基线与当前验证归因，不自动接受新像素。',
}
assert report['log_sha256'] == interrupted['log_sha256']
(REPORTS / 'frontend-e2e-independent-review-python-agent.json').write_text(
    json.dumps(report, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
lines = [
    '# 前端中断验收：独立只读审查', '',
    '结论：该次 E2E 中断，不能计为通过。10 个已记录失败中，2 个是接手前已有的 UI/截图契约差异，2 个断言被模块资源失败阻断，6 个是浏览器/worker/导航失败。', '',
    '390x844 的 18038 像素、约 6% 差异是真实文案和布局变化：旧返回外壳和两个标题不在 actual 中，页面主体约上移 175px。这些变化在扫描前本地源码已存在；本轮 Composer 只有两个数值输入 suffix 迁移，当前零选科场景没有渲染该组件。考试工作区的 stageRail=null 和 5 项考试准备任务也与扫描前快照完全相同，旧“四阶段/考试流程”断言与既有设计不一致。未更新快照，也未把这些失败豁免为通过。', '',
    f'本地扫描前快照：`D:/tools/sonarqube-yuejuan/snapshots/workspace-batch57-clippy-2026-10-11T07-56-17-037Z`。接手基线记录于 {interrupted["started_at"]} 的测试之前；完整文件 SHA/逐行差异见 `frontend-e2e-baseline-readonly-evidence.json`。', '',
    'mobile-shellbar 失败截图只有启动加载提示，trace 两个资源 ERR_INSUFFICIENT_RESOURCES；确认准备页失败显示 ErrorBoundary，trace 明确动态模块 fetch 失败。这两个断言未看到健康的目标页面。其余 spawn UNKNOWN、关闭和 0xC0000409 异常终止没有足够证据全部命名为 OOM。', '',
    '| 序号 | 场景/错误 | 分类与原因 | 必要复验 |',
    '| --- | --- | --- | --- |',
]
for failure in failures:
    title = failure['test_from_log'].split(' › ')[-1]
    lines.append(f"| {failure['failure_index']} | {title} | {failure['category']}：{failure['cause']} | {failure['required_revalidation']} |")
lines += ['', '原日志要求 9 spec/47 用例，仅有启动到 23 的标记；结束前 Vite 被中断，没有最终通过汇总。本审查脚本 exit 0 仅说明证据整理成功，不是 E2E exit 0。', '',
          '复验先使用资源空闲窗口、1 worker 重跑 10 个失败场景，再完成原 47 用例。既有设计/验收矛盾单独跟踪，不能通过删断言或改像素快照把本轮报成全通过。', '',
          '实际使用 view_image 看 expected/actual/diff 及 3 个失败截图；图片 SHA、尺寸和原 repo expected 一致性记录在 JSON。此范围外的图像和未读源码为 pending，自动枚举/文件 hash 不代表全文审查。', '',
          '代码审读状态：', '']
lines += [f'- `{path}`：{status}。{reviewed}' for path, status, reviewed in scope]
(REPORTS / 'frontend-e2e-independent-review-python-agent.md').write_text('\n'.join(lines) + '\n', encoding='utf-8')
print(json.dumps({'result': 'readonly_review_recorded', 'logged_failures': len(failures),
                  'substantive_preexisting_failures': 2, 'resource_blocked_assertions': 2,
                  'infrastructure_failures': 6, 'test_run': report['test_run_status']}, ensure_ascii=False))
