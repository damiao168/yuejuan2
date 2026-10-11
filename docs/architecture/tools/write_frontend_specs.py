"""生成已核对功能与 API 数据流规格；不修改业务源码或接口契约。"""
from pathlib import Path
import json
import re

BASE = Path(__file__).resolve().parents[1]
ROOT = BASE.parents[1]
API = json.loads((BASE / "evidence/api-catalog.json").read_text(encoding="utf-8-sig"))
ROUTER_GUARDS_SOURCE = "services/api-gateway/internal/server/router_guards.go"
FILE_HANDLERS_SOURCE = "services/api-gateway/internal/files/handlers.go"


def node(id_, label, x, y, w, h, evidence="", status="implemented", kind="box"):
    return {'id': id_, 'label': label, 'x': x, 'y': y, 'w': w, 'h': h, 'status': status, 'kind': kind, 'evidence': evidence}


def edge(source, target, label="", evidence="", **options):
    return {'source': source, 'target': target, 'label': label, 'evidence': evidence, **options}


def save(file, title, pages):
    (BASE / "specs" / file.replace(".drawio", ".json")).write_text(
        json.dumps({'file': file, 'title': title, 'pages': pages}, ensure_ascii=False, indent=2) + "\n",
        encoding="utf-8",
    )


# 编号表示业务归属；每个叶项保留独立编号。组节点的颜色只概括该域的接入边界。
DOMAINS = [
    ("F01", "身份与平台", "partial", "apps/web-admin/src/router/routes.tsx;services/api-gateway/internal/server/routes_auth.go", [
        ("登录与账户安全", "implemented", ["密码 / Token / 微信登录", "激活、恢复、改密与会话撤销", "锁定、近期认证、TOTP 与恢复码"], "services/api-gateway/internal/server/routes_auth.go"),
        ("平台控制台", "implemented", ["平台首次启用与学校管理入口", "工作台汇总、操作审计与导出", "平台管理员与学校工作区分离"], "apps/web-admin/src/router/pageRendering.tsx;services/api-gateway/internal/server/routes_platform_schools.go"),
        ("产品身份与页面访问", "partial", ["admin / teacher / auditor / student 体验", "角色、权限、工作区及深链接边界", "4 个 mock 页仅开发 / demo；生产禁用"], "apps/web-admin/src/router/experience.ts;apps/web-admin/src/router/routes.tsx"),
    ], "功能状态依据当前工作区源码；登录方案已编码不代表本次完成外部身份服务验收。"),
    ("F02", "组织与学校", "implemented", "services/api-gateway/internal/server/routes_org.go;apps/web-admin/src/router/pageRendering.tsx", [
        ("组织结构", "implemented", ["学校初始化与机构配置", "学年、年级、班级维护", "学校级成员作用域管理"], "services/api-gateway/internal/server/routes_org.go;apps/web-admin/src/pages/OrganizationSetupPage.tsx"),
        ("学生与教师", "implemented", ["学生名单维护、导入与班级归属", "教师账号、状态与访问权限", "账户激活和凭据重置入口"], "apps/web-admin/src/pages/StudentManagementPage.tsx;apps/web-admin/src/features/members/teachers/TeacherManagementPage.tsx;services/api-gateway/internal/server/routes_auth.go"),
        ("学校助手", "implemented", ["学校管理员模型聊天与 SSE", "助手查询考试、处理与成绩上下文", "工具查询重新校验业务权限与范围"], "apps/web-admin/src/api/schoolAiChat.ts;services/api-gateway/internal/server/routes_school_assistant.go;services/api-gateway/internal/schoolassistant/handlers.go"),
    ], "学校 AI 助手入口限 school_admin；聊天正文不进入通用幂等收据。"),
    ("F03", "题库与试卷", "implemented", "services/api-gateway/internal/server/routes_questionbank.go;services/api-gateway/internal/server/routes_paper.go", [
        ("题库与访问控制", "implemented", ["题库题目、内容、资产与元数据", "成员权限与 ACL 管理", "题库题目实例化到考试"], "services/api-gateway/internal/server/routes_questionbank.go;apps/web-admin/src/features/question-bank/QuestionBankPage.tsx"),
        ("试卷资料导入", "implemented", ["试卷来源上传、解析与人工确认", "内容补齐、结构预览与结构采纳", "取消、重试解析与量规草案"], "services/api-gateway/internal/server/routes_paper.go;services/api-gateway/internal/paper/paper_import_structure_handlers.go"),
        ("题目与评估策略", "implemented", ["题目、参考答案、量规与规则版本", "学科画像 / 题型原型配置及快照", "试卷准备检查与配置 hash 确认"], "services/api-gateway/internal/server/routes_exam.go;services/api-gateway/internal/paper/configuration_handlers.go;apps/web-admin/src/pages/PaperRubricPage.tsx"),
    ], "题库素材与考试题目是不同资源；实例化需要 exam:manage 与 question_bank:read。"),
    ("F04", "考试管理", "implemented", "services/api-gateway/internal/server/routes_exam.go;apps/web-admin/src/features/exams/create/CreateExamPage.tsx", [
        ("创建与管理", "implemented", ["新建单科考试或多科考试场次", "考试模板、列表与管理总览", "考试资料修改及命令回执恢复"], "services/api-gateway/internal/server/routes_exam.go;apps/web-admin/src/features/exams/management/ManagementExamOverviewPage.tsx"),
        ("准备与采集边界", "implemented", ["学生范围、名单与候选人刷新", "readiness 检查、确认、开始采集", "冻结配置变化后重新检查"], "services/api-gateway/internal/server/routes_exam.go;services/api-gateway/internal/server/routes_paper.go;apps/web-admin/src/pages/ExamPreparationPage.tsx"),
        ("考试工作区", "implemented", ["13 个 admin 分区承载各业务阶段", "管理考试归档检查、归档与恢复", "归档事实控制工作区只读入口"], "apps/web-admin/src/router/pageRendering.tsx;apps/web-admin/src/router/routes.tsx;services/api-gateway/internal/exam/management_archive_handlers.go"),
    ], "工作区动态路由不计入 24 个生产静态入口；teacher/auditor/student 无考试工作区分区授权。"),
    ("F05", "采集、图像与模板", "partial", "services/api-gateway/internal/server/routes_capture.go;docs/architecture/specs/06-image-template-pipeline.json", [
        ("模板编辑与打印", "implemented", ["模板页与 ROI 编辑、锁定和克隆", "绑定已有模板、条码与学生打印包", "OMR 校准案例标注、批准和撤销"], "services/api-gateway/internal/server/routes_paper.go;services/api-gateway/internal/server/routes_grading.go;apps/web-admin/src/pages/AnswerSheetTemplatePage.tsx"),
        ("采集与图像处理", "implemented", ["采集批次、文件解码、多页 PNG", "质量检测、条码观测与身份核对", "候选模板匹配、OpenCV 配准及 ROI 切题"], "services/api-gateway/internal/capture/registration_store_postgres.go;services/page-processing-worker/page_processing/registration.py;services/image-quality-worker/image_quality/engine.py"),
        ("人工纠正与处理中心", "implemented", ["页面 / 学生匹配确认、拆分与合并", "质量覆写、重试与四点校正预览", "应用 / 撤销校正及阻断异常定位"], "services/api-gateway/internal/server/routes_capture.go;services/api-gateway/internal/processing/handlers.go;apps/web-admin/src/features/capture/MatchingWorkspace.tsx"),
    ], "F05.04（待确认）：未发现 YOLO 备用识别与未知版面自动建模板的实现；不能接入主流程。配准完成事务边界问题见图 09。"),
    ("F06", "OCR 与答案理解", "implemented", "services/api-gateway/internal/server/routes_processing.go;services/api-gateway/internal/server/routes_exam.go", [
        ("OCR 任务", "implemented", ["答卷 OCR 创建、读取、开始与结果提交", "worker 任务来源与租约校验", "OCR 结果与 runtime 完成的事务协调"], "services/api-gateway/internal/ocr/runtime_coordinator_postgres.go;services/api-gateway/internal/server/routes_processing.go"),
        ("分段答案与证据", "implemented", ["答案分段生成、修订及图像读取", "OCR / 公式 / 数学证据关联", "当前答案版本与有效裁片追踪"], "services/api-gateway/internal/server/routes_processing.go;services/api-gateway/internal/mathunderstanding/handlers.go"),
        ("数学理解与验证", "implemented", ["数学理解任务、产物与证据核验", "符号、图形、空间与量规辅助", "worker runtime 回调绑定授权任务"], "services/api-gateway/internal/mathunderstanding/handlers.go;services/api-gateway/internal/server/routes_worker.go"),
    ], "OCR、数学产物和模型建议都是版本化证据；不把识别成功直接等同人工确认或最终成绩。"),
    ("F07", "AI 评分与人工复核", "partial", "services/api-gateway/internal/server/routes_grading.go;docs/architecture/specs/07-ai-grading.json", [
        ("规则与 AI 建议", "implemented", ["客观题规则 / OMR 评分与评分运行", "主观题单题、批次、排队和重试", "单 activeAdapter + 服务端结算与证据风险"], "services/api-gateway/internal/grading/scoring_run_routing.go;services/api-gateway/internal/subjective/handlers_worker.go;services/api-gateway/internal/subjective/grade_settlement_pipeline.go"),
        ("人工阅卷与仲裁", "implemented", ["任务领取、续租、分派、草稿及提交", "双评策略与人工分差仲裁", "批注 / 评语、退回与匿名裁片工作台"], "services/api-gateway/internal/server/routes_review.go;services/api-gateway/internal/server/routes_grading.go;apps/web-admin/src/features/grading/workbench/GradingWorkbench.tsx"),
        ("三智能体 A/B/C 内核", "partial", ["A/B 并行盲评与差异比较内核", "C 盲仲裁、准入策略与影子评估", "普通生产 Grade / worker 未自动调用 Panel"], "services/api-gateway/internal/subjective/panel_orchestrator.go;services/api-gateway/internal/subjective/panel_shadow_runner.go;services/api-gateway/internal/subjective/handlers.go"),
    ], "人工双评与 AI Panel 是两种不同机制。AI 建议不直接创建最终发布成绩；主观题仍要求人工确认。"),
    ("F08", "成绩、发布与学生", "partial", "services/api-gateway/internal/server/routes_release.go;apps/student-portal/src/App.tsx", [
        ("成绩汇总与发布", "partial", ["finalize、质量检查、缺考及名单核对", "确认成绩；旧发布与新快照/门禁并存", "成绩导出、重评结果发布"], "services/api-gateway/internal/server/routes_release.go;services/api-gateway/internal/scorerelease/handlers.go;services/api-gateway/internal/regraderelease/handlers.go"),
        ("统计与复核", "implemented", ["考试 / 班级 / 题目 / 阅卷质量报告", "成绩复核分派、教师建议与决定", "公开逐题申诉、重评与新版本关联"], "services/api-gateway/internal/server/routes_release.go;apps/web-admin/src/pages/LearningReportsPage.tsx;apps/web-admin/src/pages/QuestionAppealCenterPage.tsx"),
        ("学生端", "implemented", ["本人已发布考试、成绩与逐题反馈", "答题图片 / 公开批注按 release 读取", "圈选申诉区域、草稿与发布变化处理"], "apps/student-portal/src/App.tsx;apps/student-portal/src/api.ts;services/api-gateway/internal/studentportal/handlers.go"),
    ], "学生页面同时检查角色与读取权限。旧 /exams/{examId}/publish 仅发布grade并执行旧质量检查；新版 score_release 路径才提供快照/当前指针和对应门禁。"),
    ("F09", "模型治理与阅卷质量", "partial", "services/api-gateway/internal/server/routes_governance.go;services/api-gateway/internal/server/routes_review.go", [
        ("模型与策略治理", "implemented", ["托管 API 配置、探测与学校模型授权", "模型角色绑定、提示版本与评估", "批准 / 撤销、校准与 AI 准入"], "services/api-gateway/internal/server/routes_governance.go;apps/web-admin/src/pages/PlatformModelConfigPage.tsx"),
        ("人工阅卷质量", "implemented", ["Gold 样卷、校准、答案组与 Seed", "评分分歧、阅卷员漂移与 backmark", "重评批次与质量仪表板"], "services/api-gateway/internal/server/routes_review.go;services/api-gateway/internal/server/routes_governance.go"),
        ("治理边界与外部验证", "partial", ["旧 provider / deployment 写路由返回 410", "三智能体策略管理不表示生产自动接入", "真实模型效果、成本与样本验收待外部验证"], "services/api-gateway/internal/server/routes_governance.go;services/api-gateway/internal/subjective/panel_orchestrator.go;docs/verification-status.md"),
    ], "/system/models 由 AppShell 兼容重定向到 /platform/model-config?tab=governance；最终共用模型配置页面。"),
    ("F10", "桌面客户端与运维", "partial", "apps/desktop-client/src/App.tsx;services/api-gateway/internal/server/routes_system.go", [
        ("采集工作站", "partial", ["文件导入、批次定位与持久上传队列", "分块续传、offset 确认与失败恢复", "WIA 设备发现 / Profile / 预检；硬件采集待现场验收"], "apps/desktop-client/src/api/captureUploads.ts;apps/desktop-client/src-tauri/src/scanner.rs;apps/desktop-client/src/App.tsx"),
        ("离线阅卷", "implemented", ["下载任务、SQLite 加密草稿载荷与本机授权", "版本冲突 / 回执核对与联网同步", "系统凭据库、Spool 与账号范围隔离"], "apps/desktop-client/src/components/OfflineWorkbench.tsx;apps/desktop-client/src-tauri/src/durable_store/mod.rs;apps/desktop-client/src-tauri/src/durable_store/offline_access.rs"),
        ("运维与基础设施", "partial", ["健康检查、依赖状态、指标与文件对账", "本地日志与运行时能力诊断", "设备登记 / 吊销与自动更新仍为预留"], "services/api-gateway/internal/server/routes_system.go;apps/desktop-client/src/lib/localRuntime.ts;apps/desktop-client/src-tauri/src/lib.rs"),
    ], "F10.04（仅规划）：独立 teacher-portal 尚无业务代码。浏览器开发回退与 Windows 原生能力分开标识。"),
]


def feature_overview():
    overview = {'id': 'feature-overview', 'title': '01 系统功能总览与详细页入口', 'width': 1800, 'height': 1430,
                'note': '功能编号 F01–F10；分组状态表示当前接入范围，下一页起按业务域展示子模块和具体功能。', 'nodes': [], 'edges': []}
    overview['nodes'].append(node('system', 'yuejuan 智能阅卷系统\n当前工作区实际实现', 720, 145, 360, 130, 'apps/web-admin/src/router/pageRendering.tsx;services/api-gateway/internal/server/application_modules.go', 'infra'))
    for index, (fid, title, status, evidence, _, _) in enumerate(DOMAINS):
        x, y = (40 if index < 5 else 990), 340 + 195 * (index % 5)
        boundary = '已编码，接入边界见详页' if status == 'partial' else '主要业务入口已有代码'
        overview['nodes'].append(node(fid, f'{fid} {title}\n{boundary}\n阅读入口：第 {index + 2:02d} 页', x, y, 770, 140, evidence, status))
        trunk = 860 if index < 5 else 940
        overview['edges'].append(edge('system', fid, '', evidence, points=[[trunk, 305], [trunk, y + 70]], exitX=.25 if index < 5 else .75, exitY=1, entryX=1 if index < 5 else 0, entryY=.5))
    return overview


def feature_domain_page(index, domain):
    fid, title, status, evidence, subs, boundary_note = domain
    page = {'id': fid.lower(), 'title': f'{index + 2:02d} {fid} {title}', 'width': 1800, 'height': 1340,
            'note': '从左至右：业务域 → 子模块 → 带独立编号的具体功能。已实现表示有代码依据，不代表本次生产环境或真实设备验收。', 'nodes': [], 'edges': []}
    page['nodes'].append(node(fid, f'{fid}\n{title}', 70, 435, 260, 180, evidence, status))
    for subindex, (subtitle, substatus, functions, subevidence) in enumerate(subs, 1):
        sid, lid, y = f'{fid}.{subindex:02d}', f'{fid}.{subindex:02d}.features', 180 + (subindex - 1) * 270
        page['nodes'].append(node(sid, f'{sid} {subtitle}', 450, y, 350, 190, subevidence, substatus))
        leaflabel = '具体功能\n' + '\n'.join(f'{fid}.{subindex:02d}.{leaf:02d} {value}' for leaf, value in enumerate(functions, 1))
        page['nodes'].append(node(lid, leaflabel, 1010, y, 710, 190, subevidence, substatus))
        page['edges'].append(edge(fid, sid, '归属', subevidence, points=[[385, 525], [385, y + 95]], exitX=1, exitY=.5, entryX=0, entryY=.5))
        page['edges'].append(edge(sid, lid, '包含', subevidence, exitX=1, exitY=.5, entryX=0, entryY=.5))
    note_status = 'infra'
    if fid == 'F05':
        note_status = 'unknown'
    elif status == 'partial':
        note_status = 'partial'
    page['nodes'].append(node(f'{fid}-boundary', '阅读边界与未接入能力\n' + boundary_note, 70, 1040, 1650, 175, evidence, note_status, 'note'))
    return page


def feature_pages():
    return [feature_overview(), *[feature_domain_page(index, domain) for index, domain in enumerate(DOMAINS)]]


# API 图中的路径由目录核对，所有短路径都以 /api/v1 为前缀；生产是否启用仍取决于模块装配。
def api_row(index, frontend, frontend_ref, apilabel, endpoints, handlerlabel, handler_ref, datalabel, data_ref, permission, status="implemented"):
    for method, path in endpoints:
        if not any(r["method"] == method and r["path"] == path for r in API["backendRoutes"]):
            raise ValueError(f"未注册接口：{method} {path}")
    sources = ";".join(dict.fromkeys(r["source"] for r in API["backendRoutes"] if any(r["method"] == m and r["path"] == p for m, p in endpoints)))
    y = 190 + index*245
    ids = [f"row{index}-{part}" for part in ("page", "api", "handler", "data")]
    labels = [frontend, apilabel + "\n" + "\n".join(f"{m} {p.removeprefix('/api/v1')}" for m, p in endpoints) + f"\n权限：{permission}", handlerlabel, datalabel]
    refs = [frontend_ref, sources, handler_ref, data_ref]
    coords = [(45,310), (420,455), (950,350), (1380,375)]
    nodes = []
    for index, (id_, label, ref, (x, w)) in enumerate(zip(ids, labels, refs, coords)):
        node_status = "implemented"
        if index in (0, 2):
            node_status = status
        elif index == 3:
            node_status = "infra"
        nodes.append(node(id_, label, x, y, w, 200, ref, node_status))
    edges = [edge(ids[0], ids[1], "请求", frontend_ref), edge(ids[1],ids[2],"路由 / guard",sources), edge(ids[2],ids[3],"读 / 写",data_ref)]
    return nodes, edges


def rows_page(id_, title, rows, note="路径均以 /api/v1 为前缀；横向读取：实际页面 → API / 权限 → handler / 业务模块 → 持久化。返回数据沿请求链反向回到页面。"):
    page = {'id': id_, 'title': title, 'width': 1800, 'height': 1320, 'note': note, 'nodes': [], 'edges': []}
    for i,row in enumerate(rows):
        ns, es = api_row(i,**row)
        page["nodes"].extend(ns)
        page["edges"].extend(es)
    return page


def api_pages():
    overview = {'id': 'api-overview', 'title': '01 API 全局入口、统计口径与数据边界', 'width': 1800, 'height': 1500, 'note': '536 个 Handle 字面量注册是静态目录；另有 3 个健康 HandleFunc。目录不证明全部条件模块在每个环境启用。', 'nodes': [], 'edges': []}
    n = overview["nodes"]
    n.extend([
        node("web", "Web 管理 / 教师 / 审计体验\n24 个静态生产入口 + 4 个 mock\n动态考试工作区另计", 50, 185, 330, 170, "apps/web-admin/src/router/routes.tsx;apps/web-admin/src/router/pageRendering.tsx"),
        node("student", "独立学生门户\n本人发布成绩、逐题证据、申诉\n页面校验 student 角色与读取权限", 50, 450, 330, 170, "apps/student-portal/src/App.tsx;apps/student-portal/src/api.ts"),
        node("desktop", "Windows 桌面客户端\nToken、采集上传、匿名复核\n加密本机草稿与任务包", 50, 715, 330, 170, "apps/desktop-client/src/App.tsx;apps/desktop-client/src/api/client.ts", "partial"),
        node("transport", "共享契约 + 多种传输\nEduGradeApi 生成 SDK / 自定义 api/*.ts\nJSON、multipart、Blob、XHR、SSE\n405 个静态调用点；139 个 SDK 操作", 480, 450, 365, 230, "packages/sdk/src/generated/client.ts;apps/web-admin/src/api/client.ts;docs/architecture/evidence/api-catalog.json", "infra"),
        node("mux", "Go ServeMux 与分域路由\n536 Handle：288 精确 OpenAPI\n248 例外；OpenAPI 共 298 操作\n10 处仅参数名不同，规范化后全匹配", 950, 450, 355, 230, "services/api-gateway/internal/server/routes_exam.go;docs/architecture/evidence/api-catalog.json", "infra"),
        node("business", "业务 Handler / Service / Store\nIdentity、Exam、Capture、Grading\nRelease、AIGovernance、Assistant\n不是所有模块均经过独立 Service 层", 1410, 450, 330, 230, "services/api-gateway/internal/server/application_modules.go", "infra"),
        node("auth", "鉴权与幂等位置需分入口看\n会话 / 角色 / 权限 / 资源范围\n近期认证、worker 来源及任务租约\n通用缓存重放边界见第 08 页", 950, 185, 355, 185, ROUTER_GUARDS_SOURCE, "partial"),
        node("persistence", "数据库 + 文件对象\nPostgreSQL 领域记录与文件元数据\nMinIO 保存原件、页图与裁片\n桌面 SQLite / 加密 Spool", 1410, 770, 330, 190, "services/api-gateway/internal/files/store_postgres.go;services/api-gateway/internal/files/storage_minio.go;apps/desktop-client/src-tauri/src/durable_store/mod.rs", "infra"),
        node("worker", "worker / AI 请求另有边界\n持久任务 claim / heartbeat / 回调\nGo adapter → Python AI → 供应商\n生产主观题仍为单 activeAdapter", 950, 1020, 355, 190, "services/api-gateway/internal/server/routes_worker.go;services/api-gateway/internal/subjective/handlers_worker.go", "partial"),
        node("catalog", "完整目录与实测限制\napi-catalog.json 保存全部注册与调用位置\n376 个注册路由有静态前端调用匹配\nimport 可达性不等于每个函数都会执行", 50, 1050, 795, 190, "docs/architecture/evidence/api-catalog.json", "infra", "note"),
        node("health", "唯一静态未匹配项已有真实路由\n桌面 GET /health → mux.HandleFunc\n/health/live、/health/ready 同口径\n并非缺失 API", 1410, 1030, 330, 190, "services/api-gateway/internal/server/routes_system.go;apps/desktop-client/src/api/client.ts", "infra", "note"),
    ])
    overview["edges"] = [
        edge("web","transport","JSON / Cookie", "apps/web-admin/src/api/client.ts", points=[[425,270],[425,565]]),
        edge("student","transport","SDK / Cookie", "apps/student-portal/src/api.ts"),
        edge("desktop","transport","Token / 自定义API", "apps/desktop-client/src/api/client.ts", points=[[425,800],[425,565]]),
        edge("transport","mux","/api/v1", "docs/architecture/evidence/api-catalog.json"),
        edge("mux","business","dispatch", "services/api-gateway/internal/server/application_modules.go"),
        edge("auth","mux","包裹已注册 handler", ROUTER_GUARDS_SOURCE),
        edge("business","persistence","Store / ObjectStorage", FILE_HANDLERS_SOURCE),
        edge("worker","business","授权任务命令 / AI调用", "services/api-gateway/internal/server/routes_worker.go", points=[[1350,1115],[1350,565]]),
    ]
    pages = [overview]
    def row(frontend,frontend_ref,api,endpoints,handler,handler_ref,data,data_ref,permission,status="implemented"):
        return {'frontend': frontend, 'frontend_ref': frontend_ref, 'apilabel': api, 'endpoints': endpoints, 'handlerlabel': handler, 'handler_ref': handler_ref, 'datalabel': data, 'data_ref': data_ref, 'permission': permission, 'status': status}
    pages.append(rows_page("identity-org", "02 身份、平台学校与组织成员", [
        row("登录 / 账户安全\n登录组件、SessionManagementPage", "apps/web-admin/src/api/auth.ts;apps/web-admin/src/pages/SessionManagementPage.tsx", "会话与认证", [("POST","/api/v1/auth/login"),("GET","/api/v1/auth/me"),("GET","/api/v1/auth/sessions")], "AuthHandler\nLogin / Me / ListSessions\n密码、状态与会话边界", "services/api-gateway/internal/auth/handlers.go", "Auth Store\n用户、角色、会话与安全事件\n一次性 MFA 不走通用收据", "services/api-gateway/internal/server/router_guards.go;services/api-gateway/internal/auth/store_postgres.go", "公开登录；其余 account"),
        row("平台学校 / 首次启用\nPlatformSchoolsPage", "apps/web-admin/src/pages/PlatformSchoolsPage.tsx;apps/web-admin/src/api/platformSchools.ts", "平台学校管理", [("GET","/api/v1/platform/schools"),("GET","/api/v1/platform/schools/{tenant_id}")], "PlatformSchoolsHandler\n平台管理员目标学校汇总查询", "services/api-gateway/internal/server/routes_platform_schools.go;services/api-gateway/internal/platformschools/handlers.go", "PlatformSchools Store\n学校、学校管理员与成员事实", "services/api-gateway/internal/platformschools/store_postgres.go", "platform_admin + tenant:manage"),
        row("学校初始化 / 年级班级 / 学生\nOrganizationSetupPage / StudentManagementPage", "apps/web-admin/src/api/org.ts;apps/web-admin/src/pages/OrganizationSetupPage.tsx", "组织成员", [("GET","/api/v1/classes"),("GET","/api/v1/students")], "OrgHandler\n列表、导入、组织作用域", "services/api-gateway/internal/server/routes_org.go;services/api-gateway/internal/org/handlers.go", "Org Store\n学校、年级、班级、学生、教师\n租户 / 学校字段限制查询", "services/api-gateway/internal/org/store_postgres.go", "org:manage"),
        row("人员与权限 / 审计\nTeacherManagementPage / AuditLogPage", "apps/web-admin/src/api/users.ts;apps/web-admin/src/api/audit.ts", "账号访问与审计", [("PATCH","/api/v1/users/{id}/access"),("GET","/api/v1/audit-logs")], "AuthHandler\nUpdateManagedUserAccess / ListAudits\n访问变更撤销已有会话", "services/api-gateway/internal/server/routes_auth.go;services/api-gateway/internal/auth/handlers.go", "Auth Store\n用户访问策略与审计记录\n访问变更要求近期认证", "services/api-gateway/internal/server/router_guards.go;services/api-gateway/internal/auth/store_postgres.go", "org:manage + recent；audit:read"),
    ]))
    pages.append(rows_page("exam-paper", "03 考试、试卷、题库与模板", [
        row("考试列表 / 新建考试 / 管理总览", "apps/web-admin/src/api/exams.ts;apps/web-admin/src/features/exams/create/CreateExamPage.tsx", "考试与场次", [("POST","/api/v1/exams"),("POST","/api/v1/exam-sessions"),("GET","/api/v1/exam-management/{kind}/{id}")], "ExamHandler\nCreateExam / CreateExamSession\nGetManagementExam", "services/api-gateway/internal/exam/handlers.go;services/api-gateway/internal/exam/management_handlers.go", "Exam Store\n考试、场次、候选人与管理投影\n创建命令有回执恢复", "services/api-gateway/internal/exam/store_postgres.go;services/api-gateway/internal/exam/session_postgres.go", "exam:manage + 资源边界"),
        row("题库页面 → 试卷题目工作区", "apps/web-admin/src/api/questionBank.ts;apps/web-admin/src/features/question-bank/QuestionBankPage.tsx", "题库实例化", [("POST","/api/v1/exams/{examId}/questions/materialize-from-bank"),("GET","/api/v1/exams/{examId}/questions")], "QuestionBankHandler.Materialize\nPaperHandler.ListQuestions\n题库 ACL 与考试资源分开验证", "services/api-gateway/internal/server/routes_paper.go;services/api-gateway/internal/questionbank/handlers.go", "QuestionBank / Paper Store\n题库来源引用、考试题目及量规\n不会直接共用题库可变评分结果", "services/api-gateway/internal/paper/materialize_bank.go;services/api-gateway/internal/paper/store_postgres.go", "exam:manage + question_bank:read"),
        row("资料 / 题目编辑\nPaperRubricPage", "apps/web-admin/src/api/papers.ts;apps/web-admin/src/pages/PaperRubricPage.tsx", "导入与人工采纳", [("POST","/api/v1/exams/{examId}/paper-imports"),("PUT","/api/v1/paper-imports/{id}/review"),("POST","/api/v1/paper-imports/{id}/apply")], "PaperHandler\n导入任务、review、apply\nparser/OCR 后人工确认结构", "services/api-gateway/internal/server/routes_paper.go;services/api-gateway/internal/paper/handlers.go", "Paper Store + file_asset\n导入来源、解析、题目 / 量规版本\n异步解析结果不是直接发布", "services/api-gateway/internal/paper/paper_import_postgres.go", "exam:manage + 资源边界"),
        row("模板编辑 / 考试设置\nAnswerSheetTemplatePage / ExamReadinessPage", "apps/web-admin/src/api/configuration.ts;apps/web-admin/src/pages/AnswerSheetTemplatePage.tsx", "模板与准备确认", [("POST","/api/v1/answer-sheet-templates/{id}/lock"),("POST","/api/v1/exams/{examId}/readiness/confirm"),("POST","/api/v1/exams/{examId}/start-collection")], "PaperHandler\nLockTemplate / ConfirmReadiness\nStartCollection 核对配置 hash", "services/api-gateway/internal/paper/configuration_handlers.go", "Paper Store\n模板页 / ROI、绑定与准备确认\n有效模板 hash 用于后续配准", "services/api-gateway/internal/paper/store_postgres_template_binding.go;services/api-gateway/internal/paper/store_postgres.go", "exam:manage；上传另需 file:manage"),
    ]))
    pages.append(rows_page("capture-ocr", "04 采集、配准、OCR 与答案证据", [
        row("答卷导入 / 桌面采集工作站", "apps/web-admin/src/api/capture.ts;apps/desktop-client/src/api/captureUploads.ts", "批次与处理命令", [("POST","/api/v1/exams/{examId}/capture-batches"),("POST","/api/v1/capture-batches/{id}/files"),("POST","/api/v1/capture-batches/{id}/process")], "CaptureHandler\nCreateBatch / RegisterFile / ProcessBatch\n解码、质量、模板匹配分任务", "services/api-gateway/internal/server/routes_capture.go;services/api-gateway/internal/capture/handlers.go", "Capture Store + WorkerRuntime\n批次、采集文件、采集页、任务\n原件 / 页 PNG 存 MinIO", "services/api-gateway/internal/capture/store_postgres.go;services/api-gateway/internal/files/storage_minio.go", "capture:manage；上传 file:manage"),
        row("质量 / 匹配 / 配准纠正界面", "apps/web-admin/src/api/processing.ts;apps/web-admin/src/features/capture/MatchingWorkspace.tsx", "质量与配准", [("POST","/api/v1/submissions/{id}/run-quality-check"),("POST","/api/v1/submissions/{id}/process-pages"),("POST","/api/v1/page-registration-runs/{id}/confirm")], "ImageQuality / CaptureHandler\n质量结果与已有模板路由\n配准结果需人工确认的分支", "services/api-gateway/internal/server/routes_capture.go;services/api-gateway/internal/imagequality/handlers.go", "quality / registration run + segment\n质量通过才进入配准\n完成事务拆分问题见图 09", "services/api-gateway/internal/capture/registration_store_postgres.go;services/api-gateway/internal/imagequality/coordinator_postgres.go", "submission:manage / capture:manage", "partial"),
        row("处理 / OCR 任务界面\nSubmissionCapturePage", "apps/web-admin/src/api/submissions.ts;apps/web-admin/src/pages/SubmissionCapturePage.tsx", "OCR 与分段", [("POST","/api/v1/submissions/{id}/ocr-tasks"),("POST","/api/v1/ocr-tasks/{id}/results"),("POST","/api/v1/submissions/{id}/segment-answers")], "OCRHandler / SegmentHandler\n结果回调绑定 source 与租约\n分段读取当前有效来源", "services/api-gateway/internal/server/routes_processing.go", "OCR / Segment Store\n任务、识别产物、answer_segment\nruntime 与 OCR 结果事务协调", "services/api-gateway/internal/ocr/runtime_coordinator_postgres.go;services/api-gateway/internal/segment/store_postgres.go", "ocr:manage / segment:manage"),
        row("阅卷证据区 / 数学证据检查器", "apps/web-admin/src/api/mathUnderstanding.ts;apps/web-admin/src/features/grading/workbench/MathEvidenceInspector.tsx", "授权裁片读取", [("GET","/api/v1/answer-segments/{id}/evidence"),("GET","/api/v1/answer-segments/{id}/image"),("GET","/api/v1/review-tasks/{id}/segment-image")], "SegmentHandler / ReviewHandler\n按角色和任务权限返回图像\n教师常规复核走任务裁片入口", "services/api-gateway/internal/server/router_guards.go;services/api-gateway/internal/server/routes_review.go", "当前 segment + 证据 + 私有对象\n裁片 hash / 答案版本绑定\n原件有独立管理权限入口", "services/api-gateway/internal/review/workbench_postgres.go;services/api-gateway/internal/segment/store_postgres.go", "证据角色组合 / review:work"),
    ]))
    pages.append(rows_page("grading-release", "05 评分、复核、发布与学生数据", [
        row("阅卷管理 / 主观题批次", "apps/web-admin/src/api/subjectiveGrading.ts;apps/web-admin/src/api/scores.ts", "评分运行与 AI 建议", [("POST","/api/v1/exams/{examId}/scoring-runs"),("POST","/api/v1/answer-segments/{id}/subjective-ai-grade"),("POST","/api/v1/subjective-grading-batches")], "GradingHandler / SubjectiveHandler\n规则、OMR 与单 adapter AI\n服务端结算 → 人工确认", "services/api-gateway/internal/server/routes_grading.go;services/api-gateway/internal/subjective/grade_settlement_pipeline.go", "scoring_run / ai_grade / review_task\n答案、量规、模型与提示版本\nAI 建议不直接写发布成绩", "services/api-gateway/internal/grading/scoring_run_routing.go;services/api-gateway/internal/subjective/store_postgres.go", "grading:manage", "partial"),
        row("教师匿名工作台 / 仲裁工作台", "apps/web-admin/src/api/review.ts;apps/web-admin/src/features/grading/workbench/GradingWorkbench.tsx", "人工任务与分差仲裁", [("POST","/api/v1/review-tasks/next"),("POST","/api/v1/review-tasks/{id}/submit"),("POST","/api/v1/arbitration-tasks/{id}/submit")], "ReviewHandler\n分派 / claim / 租约 / 修订检查\n双评收齐后结算或人工仲裁", "services/api-gateway/internal/server/routes_review.go;services/api-gateway/internal/server/routes_grading.go", "review_task / human_grade\ndouble_mark_session / final_grade\n草稿和批注不等于提交成绩", "services/api-gateway/internal/review/store_postgres_resolution.go;services/api-gateway/internal/review/store_postgres_tasks.go", "review:work / arbitration:work"),
        row("成绩发布 / 成绩分析 / 复核", "apps/web-admin/src/api/scoreReleases.ts;apps/web-admin/src/api/reports.ts", "成绩确认与公开发布", [("POST","/api/v1/exams/{examId}/publish"),("POST","/api/v1/score-releases/{id}/publish"),("GET","/api/v1/exams/{examId}/reports/overview")], "Score / ScoreRelease / Report\n旧grade发布与新快照发布并存\n两个发布入口均需近期认证", "services/api-gateway/internal/server/routes_release.go;services/api-gateway/internal/score/handlers.go;services/api-gateway/internal/scorerelease/handlers.go", "旧 submission_grade 状态发布\n新版 score_release / current 快照\n旧质量检查不是新版策略门禁", "services/api-gateway/internal/score/store_postgres.go;services/api-gateway/internal/scorerelease/store_postgres.go;services/api-gateway/internal/report/store_postgres.go", "score:manage + recent；report:read", "partial"),
        row("学生成绩 / 答题图片 / 申诉", "apps/student-portal/src/api.ts;apps/student-portal/src/App.tsx", "本人发布数据与申诉", [("GET","/api/v1/student/exams"),("POST","/api/v1/student/exams/{examId}/question-appeals"),("GET","/api/v1/student/exams/{examId}/questions/{questionId}/annotations")], "StudentPortal / PublishedQuestionAppeal\nrelease_id 锚定题目与原分\n学校与本人范围重新校验", "services/api-gateway/internal/server/routes_release.go;services/api-gateway/internal/studentportal/handlers.go", "score_release 快照 / 公开批注\nsource_release 与申诉记录\n浏览器分数不会成为服务端原分", "services/api-gateway/internal/scorerelease/student_release_version.go;services/api-gateway/internal/reviewannotation/store_postgres.go", "student:grade:read / score:manage"),
    ]))
    pages.append(rows_page("governance-chat", "06 模型治理、质量工具与 AI 外部调用", [
        row("平台模型配置 / 治理页面", "apps/web-admin/src/api/modelApiConfig.ts;apps/web-admin/src/api/modelGovernance.ts", "托管 API 与角色绑定", [("GET","/api/v1/platform/model-api-configs"),("POST","/api/v1/platform/model-api-configs/{id}/probe"),("PUT","/api/v1/platform/panel-model-bindings")], "ModelGovernanceHandler\n配置 / 探测 / A/B/C 角色绑定\n旧 provider / deployment 写入 410", "services/api-gateway/internal/server/routes_governance.go", "治理 Store + SecretCipher\n模型配置、版本与学校授权\n密钥只在后端使用；图中不展示", "services/api-gateway/internal/modelgovernance/store_postgres.go;services/api-gateway/internal/modelgovernance/secrets.go", "platform_admin + 模型权限"),
        row("学校 AI 助手 / SSE 对话", "apps/web-admin/src/api/schoolAiChat.ts;apps/web-admin/src/pages/SchoolAIChatPage.tsx", "受控聊天与工具上下文", [("GET","/api/v1/ai-chat/model"),("POST","/api/v1/ai-chat/completions/stream"),("POST","/api/v1/school-assistant/conversations/stream")], "ModelGovernance / SchoolAssistant\n先解析授权上下文与查询工具\n再调用批准的学校聊天模型", "services/api-gateway/internal/server/routes_governance.go;services/api-gateway/internal/schoolassistant/handlers.go", "聊天模型配置 + 业务只读投影\n外部模型流式返回经 API 透传\n不把正文存通用幂等收据", "services/api-gateway/internal/modelgovernance/managed_agent_stream.go;services/api-gateway/internal/server/router_guards.go", "school_admin；工具另查业务权限"),
        row("AI 模型评估 / 校准 / 准入", "apps/web-admin/src/api/modelGovernance.ts;apps/web-admin/src/api/scoringAssurance.ts", "评估与批准", [("POST","/api/v1/model-evaluation-runs"),("POST","/api/v1/model-approvals"),("POST","/api/v1/model-approvals/{id}/revoke")], "治理 + Evaluation / Calibration\n批准模型、提示和策略范围\n业务准入不等于效果已验收", "services/api-gateway/internal/server/routes_governance.go", "评估、候选、批准与策略记录\n人工质量入口保留 Gold / Seed\n真实供应商效果需独立验收", "services/api-gateway/internal/modelgovernance/store_postgres.go", "model:evaluation:manage", "partial"),
        row("主观题评分命令 → worker 执行", "apps/web-admin/src/api/subjectiveGrading.ts;services/subjective-grading-worker/subjective_grading_worker/runner.py", "真实生产评分链", [("POST","/api/v1/answer-segments/{id}/subjective-ai-grade")], "SubjectiveHandler\nGo HTTPAdapter / HTTPAdapterV2\nPython grading_agent → provider", "services/api-gateway/internal/subjective/handlers_worker.go;services/api-gateway/internal/subjective/adapter_http.go;ai-services/grading_agent/app.py", "冻结答案 / 量规 / 模型 / 提示\nAPI 返回 AI 建议后服务端结算\nA/B/C 内核未自动挂接此入口", "services/api-gateway/internal/subjective/grade_settlement_pipeline.go;services/api-gateway/internal/subjective/panel_orchestrator.go", "grading:manage；worker 来源/租约", "partial"),
    ]))
    filepage = {'id': 'files-workers', 'title': '07 文件、对象存储与后台任务的数据流', 'width': 1800, 'height': 1470, 'note': '文件元数据与二进制对象分开持久化；浏览器不直接获取 MinIO 密钥。worker 必须恢复持久任务作用域再下载。', 'nodes': [], 'edges': []}
    filepage["nodes"] = [
        node("browser", "页面 / 桌面上传\nPOST /api/v1/files\nmultipart + owner/exam/submission", 50,190,350,180,"apps/web-admin/src/api/files.ts;apps/desktop-client/src/api/files.ts"),
        node("upload", "files.Handler.Upload\n检查大小 / 类型 / hash / 所有者\n资源作用域与去重键\n写 pending 资源记录",500,190,350,180,FILE_HANDLERS_SOURCE),
        node("metadata", "PostgreSQL file_asset\n私有 bucket/key、hash、业务关联\n生命周期与激活 / 对账状态",950,190,350,180,"services/api-gateway/internal/files/store_postgres.go;services/api-gateway/internal/files/reconciliation.go","infra"),
        node("objects", "MinIO ObjectStorage\n原件 / 解码 PNG / normalized\n配准 PNG / ROI / 导出 PDF\n对象写入、Stat 校验与下载",1400,190,350,180,"services/api-gateway/internal/files/storage_minio.go","infra"),
        node("intake", "领域登记与排任务\ncapture-batches/{id}/files → process\n领域记录引用 file_asset ID",50,520,350,185,"services/api-gateway/internal/server/routes_capture.go;services/api-gateway/internal/capture/store_postgres.go"),
        node("runtime", "持久 worker runtime\nagent_worker_task + attempt\nclaim → lease token → heartbeat\ncompleted/failed 回调限次数",500,520,350,185,"services/api-gateway/internal/workerruntime/store_postgres.go;services/api-gateway/internal/server/routes_worker.go"),
        node("scope", "Worker 文件读取\nGET /api/v1/files/{id}/download\nTaskScope → RequireTaskFile\n任务载荷文件 + file:manage",950,520,350,185,ROUTER_GUARDS_SOURCE),
        node("worker", "worker 处理二进制\n解码 / 质量 / 配准 / OCR\n新产物经文件 API 上传\n领域结果引用新 file ID",1400,520,350,185,"services/page-processing-worker/page_processing/runner.py;services/ocr-worker/ocr_worker/runner.py"),
        node("download", "用户下载 / 图像接口\nGET /files/{id}/download\n任务裁片 / 学生 release 图像\n权限通过才输出二进制",950,850,350,185,"services/api-gateway/internal/files/downloads.go;services/api-gateway/internal/server/routes_review.go"),
        node("preview", "浏览器 Blob / 图片预览\n同源 Cookie 或 Token\n展示 filename / watermark\n无公开对象桶地址假设",500,850,350,185,"apps/web-admin/src/api/files.ts;apps/student-portal/src/api.ts","infra"),
        node("local", "桌面本地权威状态\nSQLite 队列与加密载荷 / Spool\nReact 列表为状态投影\n在线请求与本机操作权限分开",50,850,350,185,"apps/desktop-client/src-tauri/src/durable_store/mod.rs;apps/desktop-client/src/components/OfflineWorkbench.tsx","partial"),
        node("boundary", "失败恢复边界\n对象存储和 PostgreSQL 不是单一事务，上传使用生命周期及对账补偿。\nOCR/质量/模板匹配各有协调器；配准和主观评分存在分步完成缺口，不能画成统一原子完成。",50,1170,1700,170,"services/api-gateway/internal/files/handlers.go;docs/architecture/specs/09-task-state-machine.json","partial","note"),
    ]
    filepage["edges"] = [
        edge("browser","upload","multipart", "apps/web-admin/src/api/files.ts"),
        edge("upload","metadata","pending / activate", FILE_HANDLERS_SOURCE),
        edge("upload","objects","Put + 验证", FILE_HANDLERS_SOURCE, points=[[890,430],[1350,430],[1350,280]]),
        edge("browser","intake","file ID 另行登记", "apps/web-admin/src/api/capture.ts"),
        edge("intake","runtime","排领域任务", "services/api-gateway/internal/capture/store_postgres.go"),
        edge("runtime","scope","来源 / 载荷 / 租约", ROUTER_GUARDS_SOURCE),
        edge("scope","worker","授权原件输入", "services/page-processing-worker/page_processing/runner.py"),
        edge("objects","worker","下载对象经 API", "services/api-gateway/internal/files/downloads.go"),
        edge("objects","download","私有对象流", "services/api-gateway/internal/files/downloads.go", points=[[1775,280],[1775,780],[1350,780],[1350,940]]),
        edge("download","preview","Blob / PNG / PDF", "apps/web-admin/src/api/files.ts"),
        edge("local","intake","联网队列恢复", "apps/desktop-client/src/api/captureUploads.ts"),
    ]
    pages.append(filepage)
    authpage = {'id': 'auth-order', 'title': '08 重要 API 的鉴权顺序与缓存重放', 'width': 1800, 'height': 1500, 'note': '顺序从左至右。权限检查位置因 guard 不同而不同；浏览器导航授权不能替代服务端授权。', 'nodes': [], 'edges': []}
    authpage["nodes"] = [
        node("request", "页面操作 / 深链接\nexperience + role/permissions\nworkspaces + section + readOnly",50,180,350,190,"apps/web-admin/src/router/routes.tsx;apps/web-admin/src/router/pageRendering.tsx"),
        node("session", "authenticate\nCookie 或 Bearer session\n服务端读取当前账号与 scope\n停用 / 撤销 / 过期会被拒绝",500,180,350,190,"services/api-gateway/internal/server/router_guards.go;services/api-gateway/internal/auth/middleware.go"),
        node("generic", "通用 requireAuth\nauthenticate\n→ idempotent\n→ resource boundary\n→ permission / handler",950,180,350,190,ROUTER_GUARDS_SOURCE,"partial"),
        node("cache", "同键已完成收据？\n命中可提前返回\n跳过内层资源 / 权限\n仍需合法 session 与请求键匹配",1400,180,350,190,"services/api-gateway/internal/idempotency/middleware.go","issue"),
        node("resource", "未命中：资源边界\n从路径 / 请求体解析真实资源\n租户、学校与数据 scope 约束\nwithScopedExam 本身是透传",950,520,350,190,"services/api-gateway/internal/auth/resource_scope.go;services/api-gateway/internal/server/router_guards.go"),
        node("permission", "业务权限 / 角色\nexam:manage、capture:manage 等\n任务分派、修订及状态\n还由业务 handler / Store 核对",1400,520,350,190,"services/api-gateway/internal/server/router_guards.go;services/api-gateway/internal/review/handlers.go"),
        node("strict", "requirePermission / RecentPermission\nAuth → Resource → Permission\n→ RecentAuth（关键操作）\n→ idempotent → handler",50,520,350,190,ROUTER_GUARDS_SOURCE),
        node("qbank", "题库专用 guard\nAuth → question_bank 权限\n→ CommandIdentity → handler\nACL 与命令恢复由领域维护",500,520,350,190,"services/api-gateway/internal/server/router_guards.go;services/api-gateway/internal/server/routes_questionbank.go"),
        node("mfa", "MFA / 学校助手专用入口\n一次性 MFA 不走通用收据\nschool_admin 聊天不持久化正文\n工具查询再校验业务范围",50,860,800,175,"services/api-gateway/internal/server/router_guards.go;services/api-gateway/internal/server/routes_school_assistant.go"),
        node("worker", "worker 专用约束\nTaskScope 恢复持久任务来源 / 租约\nRequireTaskSource / Payload / File\n不能信任服务身份的空浏览器 scope",950,860,800,175,"services/api-gateway/internal/server/router_guards.go;services/api-gateway/internal/server/routes_worker.go"),
        node("risk", "P1：通用收据重放绕过内层当前授权\n同一 actor 的新合法会话仍可用原键读取缓存结果，当前数据 scope 收窄后的资源 / 权限判断未重新执行。\n角色或访问变更会废止旧会话，故不能解释为未认证可访问；近期认证和平台模型专用 guard 已将授权放在收据前。",50,1170,1700,195,"services/api-gateway/internal/server/router_guards.go:103;services/api-gateway/internal/idempotency/middleware.go","issue","note"),
    ]
    authpage["edges"] = [edge("request","session","请求", "apps/web-admin/src/api/client.ts"),edge("session","generic","通用业务 guard", ROUTER_GUARDS_SOURCE),edge("generic","cache","查收据", "services/api-gateway/internal/idempotency/middleware.go"),edge("generic","resource","未命中再进入", ROUTER_GUARDS_SOURCE),edge("resource","permission","允许资源后继续", ROUTER_GUARDS_SOURCE)]
    pages.append(authpage)
    return pages


def validate_paths(pages):
    # 行号是阅读入口而不是长期定位保证；真实路径不存在时拒绝生成，避免文件名猜测进入图纸。
    missing = set()
    for page in pages:
        for item in page["nodes"] + page.get("edges", []):
            for ref in re.split(r"[;；\n]+", item.get("evidence", "")):
                ref = re.sub(r":\d+$", "", ref.strip())
                if ref and not (ROOT / ref).exists():
                    missing.add(ref)
    return sorted(missing)


if __name__ == "__main__":
    features, apis = feature_pages(), api_pages()
    missing = validate_paths(features + apis)
    if missing:
        raise SystemExit("节点证据路径不存在：\n" + "\n".join(missing))
    save("01-feature-tree.drawio", "完整系统功能树", features)
    save("08-api-data-flow.drawio", "核心 API 与数据流", apis)
    print(json.dumps({"feature_pages": len(features), "api_pages": len(apis), "paths_valid": True}, ensure_ascii=False))
