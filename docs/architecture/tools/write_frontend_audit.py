"""记录前端/API 的审读边界；静态枚举保持 pending，不冒充完整代码审计。"""
from pathlib import Path
from collections import Counter
import json
import hashlib

BASE = Path(__file__).resolve().parents[1]
ROOT = BASE.parents[1]
CATALOG = json.loads((BASE / "evidence/api-catalog.json").read_text(encoding="utf-8-sig"))
REVIEW_HASH_FILE = BASE / "evidence/frontend-review-source-hashes.json"
CURRENT_RECHECK = BASE / "evidence/api-catalog-current-recheck.json"

MOCK_MODULE_PAGE = "ModulePage；模拟路由占位"

PAGE_MAP = {
    "questionBank": "QuestionBankPage → api/questionBank.ts",
    "dashboard": "DashboardPage / TeacherDashboardPage / IdentityLandingPage → api/dashboard.ts",
    "schoolAiChat": "SchoolAIChatPage → api/schoolAiChat.ts",
    "sessions": "SessionManagementPage → api/auth.ts、api/mfa.ts",
    "platformGettingStarted": "PlatformGettingStartedPage → api/onboarding.ts",
    "platformSchools": "PlatformSchoolsPage → api/platformSchools.ts、api/org.ts",
    "platformModelConfig": "PlatformModelConfigPage → api/modelApiConfig.ts、api/modelGovernance.ts",
    "exams": "ExamManagementPage → api/exams.ts、api/workspace.ts",
    "examCreate": "CreateExamPage → api/exams.ts、api/org.ts、api/examTemplates.ts",
    "papers": "PaperRubricPage → api/papers.ts、api/questionBank.ts",
    "capture": "SubmissionCapturePage → api/submissions.ts、api/processing.ts",
    "grading": "AdminGradingOperationsPage / GradingWorkbenchPage → api/review.ts、api/scores.ts",
    "arbitration": "ArbitrationPage → api/review.ts",
    "scores": "ScoreManagementPage → api/scores.ts、api/scoreReleases.ts",
    "reports": "LearningReportsPage → api/reports.ts",
    "appeals": "QuestionAppealCenterPage → api/questionAppeals.ts、api/appeals.ts",
    "membersStudents": "StudentManagementPage → api/org.ts",
    "membersClasses": "ClassManagementPage → api/org.ts",
    "membersTeachers": "TeacherManagementPage → api/users.ts、api/org.ts",
    "organization": "OrganizationSetupPage → api/org.ts",
    "modelGovernance": "AppShell 重定向 /platform/model-config?tab=governance；同页兼容入口",
    "subjectiveGradingBatches": "SubjectiveGradingBatchPage → api/subjectiveGrading.ts",
    "systemStatus": "SystemStatusPage → api/system.ts",
    "audit": "AuditLogPage → api/audit.ts",
    "review": "ModulePage；提供 /grading 替代入口按钮",
    "quality": MOCK_MODULE_PAGE,
    "permissions": MOCK_MODULE_PAGE,
    "settings": MOCK_MODULE_PAGE,
}

# 完成记录只列出实际完整审读且核对规则的文件；其余自动清单项明确保持待审读。
COMPLETE = {
    "apps/web-admin/src/App.tsx": "QueryClientProvider、RouterProvider 与异常边界的实际装配",
    "apps/web-admin/src/AppShell.tsx": "认证写入串行、会话代次、退出清缓存、模型兼容重定向和工作区只读入口",
    "apps/web-admin/src/router/routes.tsx": "24/4 路由口径、mock 生产禁用、workspace/role/permission 与深链接规则",
    "apps/web-admin/src/router/routes.test.tsx": "检查生产注册、mock 深链接与学校聊天身份断言；本批未执行测试",
    "apps/web-admin/src/router/appRouter.tsx": "Hash Router、动态考试路径、通配兼容边界",
    "apps/web-admin/src/router/pageRendering.tsx": "lazy 页面映射、分区权限、教师匿名入口、归档只读与身份 key",
    "apps/web-admin/src/router/pageRendering.test.tsx": "核对深链接权限、阅卷权限分离、账户 key、分区只读和页面映射断言；未执行",
    "apps/web-admin/src/router/navigation.ts": "旧入口重定向、canonical 路径与考试分区导航归属",
    "apps/web-admin/src/router/experience.ts": "角色决定产品身份；业务权限不升级管理员身份",
    "apps/web-admin/src/router/experience.test.ts": "核对角色、产品身份与路径转换断言；未执行",
    "apps/web-admin/src/workspaces/registry.ts": "真实角色到工作区映射；定义中的 exam_owner 不代表自动赋予对应工作区",
    "apps/web-admin/src/auth/session.ts": "服务端会话用户投影、组织范围缺失与角色标签规则",
    "apps/web-admin/src/auth/sessionLifecycle.ts": "串行登录/退出、会话代次与命令作用域绑定顺序",
    "apps/web-admin/src/auth/sessionLifecycle.test.ts": "核对 A/B 认证、logout、锁定与迟到结果断言；未执行",
    "apps/web-admin/src/auth/capabilities.ts": "教师申诉任务入口限制；最终权限与分派仍由服务端判断",
    "apps/web-admin/src/api/client.ts": "Cookie/Token、CSRF、随机幂等键、Blob、SSE 分帧及近期认证事件",
    "apps/web-admin/src/api/client.test.ts": "核对 SSE chunk、错误上下文、近期认证与 Retry-After 断言；未执行",
    "apps/web-admin/src/api/files.ts": "multipart 上传、XHR 进度、回执确认、文件 Blob 下载",
    "apps/web-admin/src/api/scores.ts": "旧成绩API包装、名单/缺考/确认/发布与不透明命令恢复",
    "apps/web-admin/src/api/schoolAiChat.ts": "普通聊天和学校助手 SSE 分开；前端展示标签不作为模型业务输入",
    "apps/web-admin/src/api/businessCommand.ts": "命令 ID 持久化、原载荷恢复、共享 Promise、作用域代次与回执状态边界",
    "apps/web-admin/src/api/query.ts": "0/false 省略语义与数组筛选拼接，不推断为支持显式零值过滤",
    "apps/web-admin/src/api/query.test.ts": "核对编码、空值与逗号筛选行为断言；未执行",
    "apps/web-admin/src/query/client.ts": "查询有限重试、30s staleTime、写操作不自动重试",
    "apps/web-admin/src/query/client.test.ts": "核对客户端错误重试和考试工作区定向失效断言；未执行",
    "apps/web-admin/src/pages/ModulePage.tsx": "mock 页仅显示建设中，替代路径需用户操作按钮",
    "apps/web-admin/src/features/exams/create/createExamValidation.ts": "资料优先与模板流程分离、基础项和科目错误顺序、分值容差",
    "apps/web-admin/src/features/exams/create/createExamValidation.test.ts": "核对资料优先、旧模板、分区总分和0.001容差断言；未执行",
    "apps/teacher-portal/README.md": "目录明确仅为预留；教师能力来自 Web 管理后台和桌面客户端",
    "services/api-gateway/internal/server/router_guards.go": "逐类 guard 顺序；通用 requireAuth 缓存早返回、近期认证、题库和worker例外",
    "services/api-gateway/internal/server/routes_auth.go": "账户、MFA、登录、角色与管理用户端点的实际 guard",
    "services/api-gateway/internal/server/routes_exam.go": "考试、场次、管理归档、assessment与数学worker的实际注册",
    "services/api-gateway/internal/server/routes_paper.go": "试卷、模板、打印、readiness、题库实例化与导入注册",
    "services/api-gateway/internal/server/routes_capture.go": "采集、质量、模板匹配、配准纠正及条件上传模块注册",
    "services/api-gateway/internal/server/routes_processing.go": "OCR源任务/租约回调、分段证据读取与编排路由",
    "services/api-gateway/internal/server/routes_grading.go": "规则、评分运行、AI、OMR校准、人工双评和仲裁路由",
    "services/api-gateway/internal/server/routes_review.go": "Gold/Seed参考分保密、校准、复核任务、图像与命令恢复注册",
    "services/api-gateway/internal/server/routes_release.go": "成绩发布近期认证、公开申诉、报告与学生身份路由",
    "services/api-gateway/internal/server/routes_files.go": "file:manage 与 worker 持久任务文件作用域下载分离",
    "services/api-gateway/internal/server/routes_platform_schools.go": "实际只有学校汇总GET，不把平台学校路径猜成创建POST",
    "services/api-gateway/internal/server/routes_school_assistant.go": "入口仅学校管理员，正文不进入通用收据；工具另查业务权限",
    "services/api-gateway/internal/files/storage_minio.go": "私有对象读写、Stat提前检查、部署流程准备桶与有限清点",
}

# 本轮明确重新审读的版本；哈希来自独立复核记录，不能从文件枚举继承完成状态。
COMPLETE.update({
    "apps/desktop-client/src/App.tsx": "末次全文分段审读：七工作区、离线授权/会话分离、SDK卸载订阅与Tauri异步关闭、上传队列/本机草稿/诊断装配；核对App.batch33相关断言，未执行",
    "apps/desktop-client/src/features/scan-queue/useScanQueue.ts": "末次全文审读：真实批次/模板/DPI前置、逐件检查/spool失败、scope失效、替换先新后旧和重复原件保留、持久队列投影；核对对应测试断言，未执行",
    "apps/student-portal/src/App.tsx": "末次1061行分段全文审读：身份/Hash/会话代次、release锚点证据与申诉、失效回执和内存草稿保护；核对新增beforeUnload断言，未执行",
    "apps/student-portal/src/App.beforeUnload.batch52.test.tsx": "全文核对已存草稿/内存草稿/其他身份/提交回执等待/卸载恢复的合成事件断言；未执行，不代表浏览器确认框验收",
    "apps/web-admin/src/pages/LearningReportsPage.tsx": "末次全文分段审读：真实四类报告API、过期读取、空指标隐藏、班级平均分与百分比轴、导出权限与Blob水印；核对UX测试相关断言，未执行",
    "apps/web-admin/src/pages/OrganizationSetupPage.tsx": "末次全文审读：真实组织API/CSV行校验、业务人员激活回执、缺少链接与刷新失败、基于真实数据的步骤状态和首次定位；核对页面测试，未执行",
    "apps/web-admin/src/pages/OrganizationSetupPage.test.tsx": "末次全文核对邀请无初始密码、回执/缺链保留、首次步骤与CSV预览断言；未执行",
    "apps/web-admin/src/router/useUnsavedChangesGuard.ts": "末次全文核对dirty/busy导航阻断、最新ref状态、SDK卸载订阅与取消订阅；不声称浏览器必显示确认框",
    "apps/web-admin/src/router/useUnsavedChangesGuard.test.tsx": "末次全文核对导航确认、提交忙碌、属性返回值与多实例卸载断言；未执行",
    "apps/web-admin/src/styles/features/exam-preparation.css": "末次全文分段审读：模板容器布局、图像坐标映射、选项框透明与嵌套标签实色底、指针穿透；核对对比度测试断言，未执行",
    "apps/web-admin/src/styles/features/examPreparationContrast.batch52.test.ts": "全文核对实际CSS前景/背景亮度与4.5对比度、标记透明/指针属性断言；未执行",
    "packages/sdk/src/index.ts": "末次完整读取5行导出；新增browserLifecycle公共工具，不增加业务API或改变生成client导出",
    "packages/sdk/src/browserLifecycle.ts": "新增文件全文审读：每window订阅集合、旧属性链调用、只读predicate、幂等取消和外部接管属性不覆盖；核对独立事件测试",
    "packages/sdk/browserLifecycle.batch52.test.mjs": "新增文件全文核对12个EventTarget测试：订阅/属性/跨窗口/异常；未执行，不声称原生浏览器关闭提示验收",
})

PARTIAL = {
    "services/api-gateway/internal/score/handlers.go": "核对PublishGrades和旧学生grade读取；文件未全文审读",
    "services/api-gateway/internal/score/store_postgres.go": "核对PublishGrades自身quality检查、锁分/发布/回执事务；非完整Store审读",
    "services/api-gateway/internal/scorerelease/handlers.go": "核对14个注册及PublicationPublisher与Service分流；非全文审读",
    "apps/web-admin/src/api/scoreReleases.ts": "核对生成SDK快照发布与重评入口；剩余文件仍待审读",
    "apps/student-portal/src/App.tsx": "已核对首页/考试Hash、身份入口、发布/申诉版本边界；文件输出曾截断，保留pending",
    "apps/student-portal/src/api.ts": "已核对发布类型和SDK入口，非全部函数审读",
    "apps/desktop-client/src/App.tsx": "已核对采集、离线草稿、同步和诊断界面，非全部函数审读",
    "apps/desktop-client/src/api/client.ts": "已核对Token和健康调用、传输入口；非全部函数审读",
    "apps/desktop-client/src/lib/localRuntime.ts": "已核对browser fallback、系统凭据与日志脱敏；非全部函数审读",
    "apps/desktop-client/src/features/runtime/useRuntimeDiagnostics.ts": "源码全文读取；尚未结合全部调用时序与测试完成审计",
    "services/api-gateway/internal/server/routes_governance.go": "核对托管模型、学校scope、旧写410和聊天注册；其余治理模块仍待完整审读",
    "services/api-gateway/internal/server/routes_org.go": "已核对静态注册和API目录，未逐函数审读",
    "services/api-gateway/internal/server/routes_system.go": "已核对3个HandleFunc健康端点与静态目录口径；其余运维逻辑未完整审读",
    "services/api-gateway/internal/auth/resource_scope.go": "核对资源边界定位，未完整审读",
    "services/api-gateway/internal/idempotency/middleware.go": "父审计已独立确认重放顺序；本记录不把跨组结论算作本人逐文件完成",
    "services/api-gateway/internal/files/handlers.go": "读取上传入口、检查与200行内生命周期逻辑；非全文",
    "services/api-gateway/internal/files/store_postgres.go": "核对file_asset SQL、资源引用，非完整Store审读",
    "services/api-gateway/internal/scorerelease/store_postgres.go": "核对发布快照与当前指针SQL、学生查询及门禁，非完整Store审读",
    "services/api-gateway/internal/review/store_postgres_tasks.go": "核对相关SQL引用，非全文",
    "services/api-gateway/internal/subjective/store_postgres.go": "核对相关持久化引用，非全文",
}


def frontend_source(path):
    return path.is_file() and path.suffix in {'.ts', '.tsx', '.css'} and not any(part in {'node_modules', 'dist', 'target', '.git'} for part in path.parts)


def frontend_inventory():
    frontend_roots = ['apps/web-admin/src', 'apps/student-portal/src', 'apps/desktop-client/src', 'packages/sdk/src', 'packages/ui/src', 'packages/shared-types/src', 'packages/design-tokens/src']
    inventory = set()
    for directory in frontend_roots:
        inventory.update(path.relative_to(ROOT).as_posix() for path in (ROOT / directory).rglob('*') if frontend_source(path))
    inventory.update(COMPLETE)
    inventory.update(PARTIAL)
    # 图中的后端出处也只登记为待审读，不把出处存在性当成全文件完成。
    for specification in ('01-feature-tree.json', '08-api-data-flow.json'):
        spec = json.loads((BASE / 'specs' / specification).read_text(encoding='utf-8'))
        inventory.update(diagram_source_paths(spec))
    return inventory


def diagram_source_paths(spec):
    for page in spec['pages']:
        for item in page['nodes'] + page.get('edges', []):
            for ref in item.get('evidence', '').split(';'):
                path = ref.rsplit(':', 1)[0] if ref.rsplit(':', 1)[-1].isdigit() else ref
                if path.startswith(('apps/', 'packages/', 'services/', 'ai-services/')):
                    yield path


def frontend_file_status(path, complete):
    if not complete:
        return 'pending'
    if path.endswith('.md'):
        return 'documentation'
    if '.test.' in path:
        return 'self_explanatory'
    return 'existing_sufficient'


def frontend_file_handling(path):
    if path.startswith('packages/sdk/src/generated/'):
        return 'protected'
    return 'documentation' if path.endswith('.md') else 'read_only'


def frontend_record_reason(path, complete):
    if complete:
        return COMPLETE[path]
    if path in COMPLETE:
        return '源码与已审读hash不一致，需重新审读'
    return PARTIAL.get(path, '静态枚举/AST调用或证据路径清点；尚未结合全文、调用与测试完成审读')


def frontend_review_record(path, review_hashes, initial_hashes):
    current_hash = hashlib.sha256((ROOT / path).read_bytes()).hexdigest()
    # 完成声明必须绑定实际审读版本；生成器不自动续认已经变化的文件。
    complete = path in COMPLETE and review_hashes.get(path) == current_hash
    read_status = 'complete'
    if not complete:
        read_status = 'partial' if path in PARTIAL else 'not_manually_reviewed'
    return {
        'file': path, 'status': frontend_file_status(path, complete), 'read_status': read_status,
        'analysis_status': 'reviewed' if complete else 'pending', 'handling': frontend_file_handling(path),
        'source_sha256': current_hash, 'reviewed_source_sha256': review_hashes.get(path),
        'source_differs_from_initial_baseline': path in initial_hashes and initial_hashes[path] != current_hash,
        'modified': False, 'reason': frontend_record_reason(path, complete),
    }


def write_review():
    review_hashes = json.loads(REVIEW_HASH_FILE.read_text(encoding='utf-8'))
    initial_hashes = json.loads((BASE / 'evidence/workspace-baseline.json').read_text(encoding='utf-8-sig'))['files']
    files = [frontend_review_record(path, review_hashes, initial_hashes) for path in sorted(frontend_inventory())]
    report = {
        'scope': '前端与核心API边界；不声明850个清点文件或全仓审读完成', 'automatic_enumeration_is_review': False,
        'generated_sdk_handling': '只读取；不手改生成SDK，接口债务通过权威OpenAPI与正常生成流程处理',
        'summary': {'files': len(files), 'completed': sum(record['analysis_status'] == 'reviewed' for record in files),
                    'pending': sum(record['analysis_status'] == 'pending' for record in files)}, 'files': files,
    }
    (BASE / 'evidence/frontend-reviewed-files.json').write_text(json.dumps(report, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    return report['summary']


def route_table():
    rows = ["| 规范路径 | 页面及主要 API 模块 | 注册状态 / 权限 |", "| --- | --- | --- |"]
    for route in CATALOG["frontendRoutes"]:
        permissions = list(route.get("permissions", []))
        if route.get("anyPermissions"):
            permissions.append("任一：" + " / ".join(route["anyPermissions"]))
        if route.get("allowedRoles"):
            permissions.append("角色：" + " / ".join(route["allowedRoles"]))
        state = "mock（生产不注册）" if route["mock"] else "生产静态入口"
        rows.append(f"| `{route['path']}` | {PAGE_MAP[route['key']]} | {state}；{'；'.join(permissions) or '会话/身份/工作区边界'} |")
    return "\n".join(rows)


def write_audit(review_summary):
    summary = CATALOG["summary"]
    current_summary = json.loads(CURRENT_RECHECK.read_text(encoding="utf-8"))["summary"] if CURRENT_RECHECK.exists() else summary
    text = f"""# 前端、页面权限与 API 数据流审计依据

审计日期：2026-10-10。依据当前工作区（含既有未提交文件），仅生成架构文档。Git 基线见 [workspace-baseline.json](workspace-baseline.json)。功能图 [01](../diagrams/01-feature-tree.drawio) 按 F01–F10 分域；API 图 [08](../diagrams/08-api-data-flow.drawio) 分八页阅读。

## 1. 盘点口径与审读边界

静态清点覆盖 Web 管理后台、学生门户、桌面 React 前端以及 SDK/UI/shared-types/design-tokens。启动清点为 {summary['frontend_source_inventory']} 个 TS/TSX/CSS 文件、{summary['non_test_ts_source_files']} 个非测试 TypeScript 源；末次重新清点为 {current_summary['frontend_source_inventory']} / {current_summary['non_test_ts_source_files']}。末次目录见 [api-catalog-current-recheck.json](api-catalog-current-recheck.json)，变化源码复核见 [frontend-current-source-recheck.md](frontend-current-source-recheck.md)。教师独立目录只有 README；不能将预留目录记为独立应用已实现，也不能因此否认 Web 中实际存在的教师体验。

[api-catalog.json](api-catalog.json) 是 Go 字面量路由注册和 TypeScript AST 调用/导入清点，包含注册代码位置、调用位置、OpenAPI 对应关系及页面导入可达性。**AST 枚举不是完整审读，import 可达性不是函数执行证明**。目录中一个共享 API 模块可能被多页导入，模块所有操作不能都算成该页面的实际运行调用。图 08 只将已核对的核心调用链和按域关系画成主图；其余接口保留目录以便逐项追溯。

本分组审读记录共 {review_summary['files']} 项：完成全文及规则审读 {review_summary['completed']} 项，pending {review_summary['pending']} 项；详见 [frontend-reviewed-files.json](frontend-reviewed-files.json)。本批未修改业务源和生成 SDK，未运行浏览器或生产 API，不宣称全部页面、表单和 536 个 handler 已完成动态验收。现有测试只核对相关断言，不把读取测试算作本次执行通过。

| 项目 | 当前证据 |
| --- | --- |
| 生产静态入口 | 24（productionRouteDefinitions） |
| mock 入口 | 4；生产 MODE 即使显式开 flag 也不注册 |
| 动态业务入口 | 考试工作区与管理考试总览另外统计，不在24内 |
| 后端 Handle 字面量注册 | {summary['registered_backend_routes']}；可能包含条件装配的模块 |
| 与 OpenAPI 精确匹配 | {summary['registered_with_openapi']} |
| 注册例外清单 | {summary['registered_exception_routes']}；这是契约覆盖债务，不等于248个错误接口 |
| OpenAPI 操作 | {summary['openapi_operations']}；规范化参数名后全部存在对应注册 |
| 参数名等价 | {summary['openapi_parameter_name_aliases']}；保留精确覆盖统计，不把参数别名当失踪路由 |
| 静态 SDK 使用 | {summary['generated_sdk_unique_used_operations']} 个唯一 operationId |
| 静态调用点 | {summary['frontend_call_sites']}；包含SDK、自定义传输与图片URL构造 |
| 有前端匹配的注册路由 | {summary['frontend_matched_unique_registered_routes']}；其中217精确OpenAPI、159例外 |
| 另外的健康入口 | `/health`、`/health/live`、`/health/ready`：HandleFunc 不在536口径内 |

唯一未匹配的静态调用是桌面 `GET /health`。它在 [routes_system.go](../../../services/api-gateway/internal/server/routes_system.go) 实际注册，未匹配源于仓库路由统计脚本只收录 Handle。不能据此报告缺失接口，也不能把静态匹配扩张成全部536条已在生产运行。

## 2. 实际页面与身份结构

Web 使用 TanStack Hash Router；[appRouter.tsx](../../../apps/web-admin/src/router/appRouter.tsx) 匹配少量显式路径和通配兼容入口，实际内容由 [AppShell.tsx](../../../apps/web-admin/src/AppShell.tsx)、[routes.tsx](../../../apps/web-admin/src/router/routes.tsx)、[pageRendering.tsx](../../../apps/web-admin/src/router/pageRendering.tsx) 决定。生产入口的“productionReady”只是注册元数据，不能当作外部服务效果或硬件验收证明。

产品路径带 `/admin`、`/teacher`、`/auditor`、`/student` 前缀，表中给出移除体验前缀后的规范路径。角色决定允许的体验，业务权限本身不能把教师升级成管理端身份；[experience.test.ts](../../../apps/web-admin/src/router/experience.test.ts) 有对应断言。组织角色与工作区、路径授权和页面内操作权限分别存在，不能只用菜单隐藏证明授权。

{route_table()}

动态管理总览为 `/exams/overview/(session|standalone)/:id`，返回整场考试管理投影。考试工作区为 `/exams/:examId/:section`，admin 分区有 `overview/students/paper/questions/template/capture/processing/grading/quality/scores/appeals/reports/settings`（13项）；teacher/auditor/student 分区数组为空。AppShell 将旧 `overview` 分区映射到 `students`。页面动作再按相应业务权限和归档只读事实控制，见 [pageRendering.test.tsx](../../../apps/web-admin/src/router/pageRendering.test.tsx)。因此“考试负责人”工作区定义存在不意味着所有持有 exam:manage 的用户都会得到管理体验。

`/system/models` 是兼容入口：AppShell 将其重定向到 `/admin/platform/model-config?tab=governance`，页面映射也复用 PlatformModelConfigPage；不是两套独立治理实现。`paper/questions` 同样复用 PaperRubricPage，以 `initialView` 区分资料与题目。`/review` mock 页只提供前往真实 `/grading` 的按钮。旧 AppealCenterPage 文件仍存在，但真实 `/appeals` lazy 导入的是 QuestionAppealCenterPage；仅文件存在不能计为第二条生产申诉路径。

独立学生门户自己解析 `#/` 和 `#/exams/:examID`，同时校验 student 角色与 student:grade:read 后再进入成绩页面；Web 的 student 体验主要是 IdentityLandingPage 入口，不应与独立学生门户混淆。桌面客户端有登录、文件采集、匿名复核、离线草稿、同步队列、诊断和本地日志；独立 teacher-portal 的 [README](../../../apps/teacher-portal/README.md) 明确只是预留。

## 3. 组件、状态和表单边界

Web 采用 lazy 页面、features 业务模块、components 共享布局/工作台，以及 api 分域包装；不是每个页面都复制一份业务实现。AppShell 负责会话和产品身份，pageRendering 负责页面选择与权限属性，考试工作区投影负责归档只读；页面内部仍有业务状态与未保存输入。

[query/client.ts](../../../apps/web-admin/src/query/client.ts) 的查询默认30秒 staleTime、5分钟 gcTime，客户端4xx不重试，临时失败最多有限重试，写操作不自动重试。React state、ref、QueryClient、命令恢复记录职责不同：state负责渲染；ref/会话代次拒绝迟到结果；QueryClient保存可失效的服务端投影；[businessCommand.ts](../../../apps/web-admin/src/api/businessCommand.ts) 只持久化不透明命令ID，冻结输入由服务端恢复，不能用新编辑草稿覆盖结果未知的旧操作。共享Promise减少同会话重复续发，但不能据此保证跨进程所有命令只执行一次。

退出先使命令作用域失效，然后取消读请求与清Query缓存；已发出的远端写请求仍须通过原命令回执确认。认证写请求串行，防止迟到Cookie覆盖下一次登录；锁定保留身份显示但阻断命令scope。相关行为在 [sessionLifecycle.test.ts](../../../apps/web-admin/src/auth/sessionLifecycle.test.ts) 和路由渲染测试中有断言，本批未执行这些测试。

表单验证只能对已审读实例作结论。新建考试 [createExamValidation.ts](../../../apps/web-admin/src/features/exams/create/createExamValidation.ts) 检查名称、类型、学校、年级、参考班级、科目、满分、时间与阅卷方式；资料优先模式允许稍后补题目，模板模式才检查模板分区完整性与总分，容差为0.001。不能以此推断全部页面表单都使用统一 Schema 或所有输入都已充分验证。其余页面的详细校验、状态重复和异步边界仍按逐文件记录 pending。

Web、桌面和学生端各有传输包装，认证方式与本机能力不同。Web ApiClient 同源Cookie为默认，也可用Bearer；上传XHR报告请求体进度，100%仍须等待服务器回执；SSE按完整帧解析，网络chunk不是事件。默认写请求的随机Idempotency-Key只覆盖一次请求，业务重试必须显式复用原键。多份传输是维护边界，不能直接宣布是缺陷；改契约时应共同核对错误、CSRF、幂等与文件下载行为。

## 4. 核心 API 的真实数据路径

图08分域页对应路由和前端api包装，并保留代码出处。一般链为“页面 → 生成SDK或自定义包装 → Go ServeMux/guard → Handler/业务函数 → Store/协调器 → PostgreSQL或对象存储”，返回数据沿调用链反向回到页面。部分Handler直接调用Store，不绘制不存在的统一Service层。

- 组织、考试、试卷与题库：服务端从身份取得租户/资源scope，题库实例化同时要求考试管理和题库读取。资料导入解析与用户采纳分开，不能把解析产物自动当作最终题目或正式评分规则。
- 采集：上传原件后再登记领域文件并启动批次处理，解码、质量、模板匹配、配准与OCR是多个任务/命令。已有模板候选与人工绑定已实现；没有把未知版面自动建模板或YOLO备用识别连入实际主流程。
- 主观题：生产同步或worker入口调用一个 activeAdapter，Go传冻结证据给Python grading_agent，供应商响应经服务端结算，再进入人工确认。A/B/C Panel内核与影子评估存在，但普通路由没有自动调用它，详见图07与流水线审计。
- 人工复核：按分派、领取/续租、状态和修订限制处理；教师走匿名任务裁片，原件读取是独立管理权限接口。人工双评与AI Panel是不同机制。
- 成绩：最终汇总和公开发布分开。旧 /exams/{{examId}}/publish 更新grade状态并执行旧质量检查，不创建score_release快照/当前指针；新版 /score-releases/{{id}}/publish 才经相应发布协调器维护快照发布。学生端新版图像/批注/申诉用release锚定公开版本，旧学生grade接口仍注册；服务端读取原分，不信任浏览器传来的得分。
- 文件：PostgreSQL file_asset 保存归属、私有bucket/key、hash和生命周期，MinIO保存二进制。上传采用pending写入、对象Put/验证、激活和对账补偿；二者不是单一数据库事务。worker在文件授权前恢复持久任务scope并核对任务载荷文件。
- 桌面：原生SQLite/加密Spool是持久队列和草稿的权威，React队列是投影；browser-dev回退、WIA发现/预检、现场物理采集验收不能混为一类。设备登记/吊销与自动更新在能力接口中仍标为待接入。

## 5. 已确认问题与建议

### P1：通用幂等收据可提前返回，内层当前资源/权限未重查

[router_guards.go:103](../../../services/api-gateway/internal/server/router_guards.go) 的通用 requireAuth 组合为 `authenticate(idempotent(resource(permission(handler))))`。认证仍执行；命中相同actor/tenant、请求路径/正文和键的已完成收据时，[idempotency/middleware.go](../../../services/api-gateway/internal/idempotency/middleware.go) 可直接返回旧结果，内层资源scope、权限及业务handler不会再次运行。旧会话会在访问/角色变更时被废止，因此不是未认证绕过；风险是同一actor的新合法会话仍可重放原键，在当前scope收窄后读取旧响应。根审计已独立核实此边界。

requirePermission、requireRecentPermission和平台模型专用guard已经把资源/权限（及近期认证）放在缓存读取前；题库CommandIdentity、MFA、学校助手还有专用路径。建议将所有允许重放的响应入口明确分型，并保证读取缓存前验证当前资源和业务授权，补充“旧命令成功→缩小scope→新会话原键重放”的定向回归。不能只重排当前图表来声称代码已修复，本次没有改动业务实现。

### P1：旧成绩发布与新版快照发布同时保留

[score/handlers.go:172](../../../services/api-gateway/internal/score/handlers.go) 调用旧 Store.PublishGrades；[score/store_postgres.go:333](../../../services/api-gateway/internal/score/store_postgres.go) 在自身事务里检查旧 qualityReportTx，更新 submission_grade/final_grade/exam 与命令回执，没有创建新版发布快照或当前指针。新版 [scorerelease/handlers.go:214](../../../services/api-gateway/internal/scorerelease/handlers.go) 经 PublicationPublisher 或新版 Service 发布快照。两条API在 routes_release.go 都有注册，旧学生单份grade读取也仍存在。旧路径有质量检查，不能写成完全无门禁；它没有保证新版策略门禁、快照和发布锚点。建议明确旧接口退役或统一发布协调入口，回归旧API仍可调用时的门禁、锁定和公开版本一致性。

### P2：接口契约覆盖债务与多传输维护成本

536个静态Handle有248个注册例外，其中159条有前端调用匹配；关键工作流的一部分只能由自定义api包装维护。OpenAPI298个操作经参数名规范化全部有对应注册，但精确字符串覆盖数只有288。10个参数名差异是别名，不是漏注册，覆盖统计与匹配统计应同时保留。建议按业务域推进权威OpenAPI、例外收敛和SDK正常生成，覆盖JSON/Blob/multipart/SSE/图片URL等契约，不手改generated目录。

### P2：既有设计/验收说明和当前代码存在时间差

enterprise-architecture 等早期文档保留独立教师目录、设备绑定、自动更新和原生采集规划；部分生产准备文档对学生端、AI和离线能力仍用较早阶段表述。当前代码已有独立学生端、真实worker、持久桌面存储和AI建议链，仍没有普通生产三智能体自动接入与未经现场验收的完整扫描仪承诺。建议以图纸/证据为当前实现入口，将规划和现场验收边界旁置；本次保留原文，未改设计或运行输入。

未确认全仓重复状态、全部无效页面或全部表单缺陷，不给出不存在的P0结论。缓存重放、流水线事务恢复和真实设备/供应商验收需分别处理，不以静态路由匹配证明运行可靠性。

## 6. 后续维护

改路由时同步核对production/mock/dynamic三个口径、体验前缀、hasRouteAccess和renderAppRouteContent；改API时从权威OpenAPI/真实注册更新证据，再检查领域调用与状态/权限后更新图08。`/system/models`等兼容入口应记录最终重定向目的，旧文件/旧路由不能自动记为新增生产功能。

静态目录可用 `node docs/architecture/evidence/extract-frontend-api.mjs` 重建；这会读取正常依赖与仓库路由覆盖工具，只写evidence目录。图规格用 `python docs/architecture/tools/write_frontend_specs.py` 生成，再由统一 `build_diagrams.py` 生成可编辑XML。生成器检查核心端点精确注册及证据路径；这种检查不代替handler审读、动态测试或视觉检查。逐文件状态更新只能在全文、调用与必要测试证据核对完成后进行，不因脚本枚举而清掉pending。
"""
    (BASE / "evidence/frontend-api-audit.md").write_text(text,encoding="utf-8")


if __name__ == "__main__":
    result = write_review()
    write_audit(result)
    print(json.dumps(result,ensure_ascii=False))
