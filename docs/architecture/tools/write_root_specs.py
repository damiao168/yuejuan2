"""维护图02、03、10的文档规格；实际导入边必须能在静态目录找到源码依据。"""
from pathlib import Path
import json

BASE = Path(__file__).resolve().parents[1]
PREFIX = "services/api-gateway/internal/"

def node(id, label, x, y, w=350, h=145, status="implemented", kind="box", evidence=""):
    return {'id': id, 'label': label, 'x': x, 'y': y, 'w': w, 'h': h, 'status': status, 'kind': kind, 'evidence': evidence}

def edge(source,target,label="",**kwargs):
    return {'source': source, 'target': target, 'label': label, **kwargs}

def page(id,title,nodes,edges,width=1800,height=1300,note="当前工作区实际实现 · 2026-10-10 · 箭头表示调用／依赖方向；完整出处见节点属性与provenance目录"):
    return {'id': id, 'title': title, 'nodes': nodes, 'edges': edges, 'width': width, 'height': height, 'note': note}

def save(number,name,title,pages):
    (BASE/"specs").mkdir(exist_ok=True)
    (BASE/"specs"/f"{number}-{name}.json").write_text(json.dumps({'file': f'{number}-{name}.drawio', 'title': title, 'pages': pages},ensure_ascii=False,indent=2),encoding="utf-8")

def architecture():
    ns=[
        node("web","管理端 / 教师体验\nReact + Vite + TypeScript\n路由、页面权限、考试工作区",60,160,490,135,evidence="apps/web-admin/src/router/routes.tsx:62"),
        node("student","学生门户\nReact + Vite\n登录、已发布成绩、试卷与申诉",650,160,490,135,evidence="apps/student-portal/src/main.tsx;apps/student-portal/src/api.ts:1"),
        node("desktop","桌面工作站\nReact + Tauri / Rust\n扫描、上传队列、离线草稿与同步",1240,160,490,135,evidence="apps/desktop-client/src-tauri/src/lib.rs"),
        node("sdk","前端应用 / SDK\n共享类型、生成API客户端、自定义transport\n页面状态由hooks/context管理",60,380,1670,110,evidence="packages/sdk/src/index.ts;packages/sdk/src/runtime.ts:1"),
        node("api","Go HTTP API 统一入口\nServeMux + RequestID / Recover / metrics / CORS / CSRF / body limit\nAuthMiddleware + 路由guard + 幂等收据；不同guard顺序存在差异",60,570,1670,120,evidence=PREFIX+"server/server.go:154;"+PREFIX+"server/router_guards.go:93"),
        node("identity","Identity / 组织与权限\nauth、org、平台学校\n会话、角色、数据范围",60,790,350,150,evidence=PREFIX+"server/module_identity.go"),
        node("prepare","ExamPreparation\nexam、paper、assessment\n题库、文件、答卷、工作区",500,790,350,150,evidence=PREFIX+"server/module_exam.go:62"),
        node("capture","CaptureProcessing\ncapture、upload、OCR\n图像质量、编排、worker runtime",940,790,350,150,evidence=PREFIX+"server/module_capture.go:83"),
        node("grading","GradingQuality / Release\n评分、复核、质量、重评\n发布gate、学生、申诉、报告",1380,790,350,150,evidence=PREFIX+"server/module_grading.go:104;"+PREFIX+"server/module_release.go:61"),
        node("stores","数据访问 / 持久化\n领域Store接口 + 手写PostgreSQL SQL\nMemoryStore用于测试；文件元数据与对象分开",60,1030,790,140,evidence=PREFIX+"server/application_stores.go;"+PREFIX+"server/application_modules.go:45"),
        node("compute","图像与AI计算边界 → 下一页\n独立Python worker通过HTTP领取任务\nGo→grading-agent→本地或外部模型",940,1030,790,140,evidence=PREFIX+"server/module_grading.go:198;services/page-processing-worker/page_processing/runner.py"),
        node("db","PostgreSQL\n业务、任务、版本、审计、outbox",60,1280,490,110,status="infra",evidence=PREFIX+"server/infrastructure.go:53"),
        node("object","MinIO / S3\n原图、裁图、试卷、报告附件",650,1280,490,110,status="infra",evidence=PREFIX+"server/infrastructure.go:111"),
        node("redis","Redis\n认证限流；持久任务真相在PG\n不能等同为Redis消息队列",1240,1280,490,110,status="infra",evidence=PREFIX+"server/infrastructure.go:77")]
    es=[edge(a,"sdk","API transport",exitX=.5,exitY=1,entryX=(i+.5)/3,entryY=0) for i,a in enumerate(["web","student","desktop"])]
    es += [edge("sdk","api","HTTP / cookie 或 Bearer"),*[edge("api",a,"同进程调用",exitX=(i+.5)/4,exitY=1,entryX=.5,entryY=0) for i,a in enumerate(["identity","prepare","capture","grading"])],edge("prepare","stores","读写"),edge("identity","stores","身份查询"),edge("capture","compute","任务/图像"),edge("grading","compute","模型请求"),edge("stores","db","SQL事务"),edge("stores","object","受控对象读写"),edge("api","redis","限流",points=[[1770,630],[1770,1240],[1485,1240]],exitX=1,exitY=.5,entryX=.5,entryY=0)]
    p1=page("logical","逻辑分层：前端、模块化单体与持久化",ns,es,height=1510)
    ns=[
        node("go","Go API模块化单体\nHTTP handlers / Service / Store\n装配与后台协程同一进程",60,160,410,180,evidence=PREFIX+"server/application_modules.go:49"),
        node("runtime","workerruntime / PostgreSQL\nclaim、lease、heartbeat、generation\n结果提交校验；不是进程内任务真相",650,160,460,180,evidence=PREFIX+"workerruntime/store_postgres.go"),
        node("workers","独立Python Worker集合\npage-processing / image-quality / OCR\nmath-recognition、paper-formula\nsubjective-grading / math-verification",1300,160,430,220,status="infra",evidence="infra/docker-compose/docker-compose.yml:477"),
        node("image","页面/图像计算\nOpenCV配准、质量测量、ROI裁切\n条码识别与OMR；人工恢复入口",1300,480,430,170,evidence="services/page-processing-worker/page_processing/registration.py;services/image-quality-worker/image_quality/engine.py"),
        node("goai","Go subjective / paper\n本地HTTP Adapter / 托管模型解析\n资格门禁、rubric、证据、版本",60,480,410,180,evidence=PREFIX+"server/module_grading.go:108;"+PREFIX+"server/application_modules.go:126"),
        node("agent","Python grading-agent\n结构化解析、guardrail、provider adapter\n请求鉴权与超时、输入输出协议",650,480,460,180,status="infra",evidence="ai-services/grading_agent/server.py;ai-services/grading_agent/provider_adapter.py"),
        node("models","模型运行时 / 外部AI服务\n本地llama.cpp或受控provider\n是否运行、评分质量未由静态审计确认",650,800,460,165,status="infra",evidence="infra/docker-compose/docker-compose.yml:443;ai-services/grading_agent/model.py"),
        node("govern","AI治理与助手\nmodelgovernance / AIFoundation\n资格、评估、校准、分歧 / schoolassistant",60,800,410,165,evidence=PREFIX+"server/module_ai_governance.go:51;"+PREFIX+"server/module_ai_foundation.go;"+PREFIX+"server/module_school_assistant.go"),
        node("panel","三智能体内核\nA/B并行、C盲仲裁已存在\n普通生产worker未接自动评分链",1300,800,430,165,status="partial",evidence=PREFIX+"subjective/panel_orchestrator.go;"+PREFIX+"server/module_grading.go:108"),
        node("maintenance","Go后台任务\n试卷parse executor、processing projector\n文件巡检、outbox dispatcher",60,1080,740,160,evidence=PREFIX+"server/infrastructure.go:118;"+PREFIX+"server/server.go:86"),
        node("partialinfra","部署配置中接入有限的组件\nQdrant仅查健康，未确认向量检索链\noutbox当前Publisher为结构化日志",950,1080,780,160,status="partial",evidence=PREFIX+"deps/deps.go:182;"+PREFIX+"outbox/publisher_log.go:14")]
    es=[edge("workers","runtime","HTTP claim / complete / heartbeat",exitX=0,exitY=.4,entryX=1,entryY=.4),edge("go","runtime","写入任务事实"),edge("workers","image","本地计算"),edge("goai","agent","HTTP请求"),edge("agent","models","供应商协议"),edge("govern","goai","受控配置/资格",exitX=.5,exitY=0,entryX=.5,entryY=1),edge("go","goai","用户/任务触发"),edge("runtime","goai","subjective worker请求执行",points=[[880,400],[265,400]]),edge("maintenance","partialinfra","当前投递端；不是消息总线")]
    save("02","system-architecture","总体架构",[p1,page("runtime","计算、治理与异步任务的实际边界",ns,es,height=1380)])


def direct_dependency_edges(consumers, targets, pairs):
    edges = []
    for consumer in consumers:
        for target in targets:
            found = pairs.get((consumer, target))
            if found:
                evidence = found[0]
                edges.append(edge(consumer, target, 'import', evidence=f'{evidence["file"]}:{evidence["line"]}', exitX=1, exitY=.5, entryX=0, entryY=.5))
    return edges


def consumer_dependency_edges(consumers, pairs):
    edges = []
    for consumer in consumers:
        for target in consumers:
            found = pairs.get((consumer, target))
            if found:
                evidence = found[0]
                edges.append(edge(consumer, target, 'import', evidence=f'{evidence["file"]}:{evidence["line"]}', dashed=False, exitX=0, exitY=.5, entryX=0, entryY=.5, points=[[35, 255 + consumers.index(consumer) * 210], [35, 255 + consumers.index(target) * 210]]))
    return edges


def dependency_page(id_, title, consumers, targets, pairs):
    targets = [target for target in targets if target not in consumers]
    nodes = [node(consumer, consumer + '\n源包；右侧列出关键直接import', 60, 180 + index * 210, 380, 150, evidence=PREFIX + consumer + '/') for index, consumer in enumerate(consumers)]
    nodes.extend(node(target, target + '\n被直接依赖的包', 1090, 160 + index * 175, 520, 135, evidence=PREFIX + target + '/') for index, target in enumerate(targets))
    edges = direct_dependency_edges(consumers, targets, pairs) + consumer_dependency_edges(consumers, pairs)
    note_y = max(930, 180 + len(consumers) * 210)
    nodes.append(node('scope', '如何阅读\n实线：源包直接import目标包\n间接依赖沿箭头两跳以上阅读\n完整351条包边见module-imports.json\n静态import无环；不等于运行时无回路', 60, note_y, 800, 200, kind='note', status='infra', evidence='docs/architecture/evidence/module-imports.json'))
    return page(id_, title, nodes, edges, height=max(1300, note_y + 350), note='只画真实import，不把业务先后关系当依赖；代码出处在每条边的evidence属性，公共auth/httpx/logger见概览页')

def dependencies():
    imports=json.loads((BASE/"evidence/module-imports.json").read_text(encoding="utf-8"))
    pairs={}
    for e in imports["imports"]:
        pairs.setdefault((e["source"],e["target"]),[]).append(e)
    pages=[]
    groups=[
        ("prepare","考试资料与采集的直接包依赖",["capture","ocr","paper","processing"],["exam","submission","files","workerruntime"]),
        ("ai","主观评分的直接包依赖",["subjective","modelgovernance","review"],["modelgovernance","mathunderstanding","grading","paper","segment","assessment"]),
        ("release","发布与学校助手的直接包依赖",["schoolassistant","releasegate","scorerelease"],["exam","workspace","releasegate","scorerelease","qualitydashboard","files"]),
    ]
    pages.extend(dependency_page(id_, title, consumers, targets, pairs) for id_, title, consumers, targets in groups)
    ns=[node("server","server 组合根\n集中装配各Store/Service/Handler\n通过Dependencies传受限能力",60,160,430,170,evidence=PREFIX+"server/application_modules.go:49"),
        node("auth","auth / httpx / logger\n多数模块的公共直接依赖\nauth广接口也承载审计写入",650,160,490,170,evidence=PREFIX+"auth/store_contracts.go;"+PREFIX+"server/module_exam.go:55"),
        node("db","database/sql + pgx\n领域PostgresStore直接执行SQL\n租户context包装驱动；非ORM层",1260,160,470,170,evidence=PREFIX+"db/tenant_context.go:90;"+PREFIX+"server/application_stores.go"),
        node("web","Web / Student / Desktop\n共享SDK及自定义transport\n桌面有Rust本地SQLite边界",60,500,430,180,evidence="apps/student-portal/src/api.ts:1;apps/desktop-client/src/api/captureUploads.ts:3"),
        node("sdk","packages/sdk\n生成契约 + 自定义业务客户端\n所有API调用并非都由OpenAPI覆盖",650,500,490,180,evidence="packages/sdk/src/index.ts;docs/architecture/evidence/api-catalog.json"),
        node("workers","Python worker\n公共lease/http包\n使用HTTP能力，不直接写业务DB",1260,500,470,180,evidence="packages/worker-runtime-python/edugrade_worker_runtime/lease.py;packages/worker-runtime-python/edugrade_worker_runtime/http.py"),
        node("debt","P2：维护边界债务\ncapture.Store 49方法\nreview.Store 21 / governance.Store 21\n题库Store 26；前端3页超700行门禁",60,870,780,210,status="issue",evidence=PREFIX+"capture/types.go;"+PREFIX+"review/types.go;contracts/architecture/frontend-debt.json"),
        node("constraints","边界结论与限制\n68内部包、351静态import边无循环\n装配接口存在多方向业务耦合\n未确认非法跨层调用，不能把依赖数当漏洞\n模型配置旧/新入口共用页面，属于兼容别名",950,870,780,210,status="infra",kind="note",evidence="scripts/check-architecture-boundaries.mjs;docs/architecture/evidence/frontend-api-audit.md")]
    es=[edge("server","auth","构造注入",evidence=PREFIX+"server/application_modules.go:50"),edge("server","db","Postgres组装",points=[[520,380],[1500,380]],exitX=.5,exitY=1,entryX=.5,entryY=1,evidence=PREFIX+"server/application_modules.go:113"),edge("web","sdk","直接import",evidence="apps/student-portal/src/api.ts:1"),edge("workers","server","HTTP；运行调用",dashed=True,points=[[1780,590],[1780,420],[270,420]],exitX=1,exitY=.5,entryX=.5,entryY=1)]
    pages.insert(0,page("overview","组合根、公共依赖与维护债务",ns,es,height=1230,note="实线表示源码直接依赖；虚线HTTP是运行调用。间接依赖由详细页面实线连成的路径表达，未发现Go静态导入环"))
    save("03","module-dependencies","模块依赖",pages)

def security():
    ns=[node("human","实际人类角色\nplatform_admin / tenant_admin / school_admin\nteacher / grader / arbitrator / auditor / student\n数据范围：platform/tenant/school/class/exam_task/self",60,160,760,200,evidence=PREFIX+"auth/role_policy.go:19"),
        node("service","实际服务身份\npage_processing_worker / subjective_grading_worker\n*_worker按服务角色策略识别\n服务账户仍须绑定任务与租约",980,160,750,200,evidence=PREFIX+"auth/role_policy.go:28;"+PREFIX+"server/router_guards.go:295"),
        node("auth","AuthMiddleware\n会话hash查询、expiry/revoke/epoch/locked校验\n解析当前角色、permissions、AccessScope\ncookie / Bearer；空scope只限特定自账户入口",60,460,450,210,evidence=PREFIX+"auth/middleware.go:36;"+PREFIX+"auth/store_postgres.go:180"),
        node("idem","requireAuth中的幂等收据\n缓存key含tenant+actor+route+指纹\n命中时直接返回旧响应\nP1：内层权限/资源检查未执行",680,460,450,210,status="issue",evidence=PREFIX+"server/router_guards.go:98;"+PREFIX+"idempotency/middleware.go:144"),
        node("resource","资源边界 + Permission\n学校、班级、考试、学生、分派任务\n平台角色仍受保留tenant校验\n新命令继续经过业务Store scope",1280,460,450,210,evidence=PREFIX+"auth/resource_scope.go:37;"+PREFIX+"server/router_guards.go:130"),
        node("recent","受保护关键guard顺序\nauth → resource → permission → recentAuth\n→ idempotency → handler\n导出/关键命令会在缓存返回前再次检查",60,800,760,190,evidence=PREFIX+"server/router_guards.go:116;"+PREFIX+"server/recent_auth_test.go:67"),
        node("task","Worker作用域与能力\nTaskScope + source/payload/file校验\nowner/generation/lease限制结果提交\n服务声明不能自行扩大全租户范围",980,800,750,190,evidence=PREFIX+"server/router_guards.go:182;"+PREFIX+"server/router_guards.go:307"),
        node("rls","DB第二道防线\n每次query设置runtime role与tenant_id\n非superuser / 不可BYPASSRLS\n仅部分表FORCE RLS；学校细分仍靠应用\nmaintenance是显式后台跨租户上下文",60,1100,760,200,evidence=PREFIX+"db/tenant_context.go:55;services/api-gateway/migrations/000139_high_risk_tenant_rls.sql"),
        node("boundary","审计边界\n身份仍始终校验；权限变更会废止旧会话\n同actor重新登录、scope仍有效后才可能旧键重放\n不表示重放会再次执行写操作\nDB复合租户FK与快照触发器缺口见审查报告",980,1100,750,200,status="partial",kind="note",evidence=PREFIX+"auth/managed_access_store.go:154;docs/architecture/evidence/database-audit.md")]
    es=[edge("human","auth","请求 / 当前会话"),edge("service","task","Bearer + task headers",points=[[1770,260],[1770,750],[1355,750]],exitX=1,exitY=.5,entryX=.5,entryY=0),edge("auth","idem","requireAuth路线"),edge("idem","resource","只有新执行时"),edge("auth","recent","专用guard路线"),edge("resource","rls","Store SQL",points=[[1750,575],[1750,1040],[440,1040]],exitX=1,exitY=.5,entryX=.5,entryY=0),edge("task","rls","任务租户context",points=[[880,895],[880,1200]],exitX=0,exitY=.5,entryX=1,entryY=.5)]
    p1=page("authorization","身份、权限、幂等与学校数据边界",ns,es,height=1450)
    ns=[node("admin","平台模型配置操作\nAPI Key仅输入/掩码显示\n角色+模型配置权限+资源检查",60,160,430,170,evidence=PREFIX+"server/router_guards.go:310;"+PREFIX+"modelgovernance/managed_config_store_postgres.go:34"),
        node("cipher","CredentialCipher\nSHA-256(masterKey) → AES-256-GCM\n随机nonce；AAD绑定tenantID+configID\n解密失败返回配置不可用",650,160,480,190,evidence=PREFIX+"modelgovernance/managed_config.go:208"),
        node("store","PostgreSQL托管配置\n只保存ciphertext + nonce\n状态/能力探测/更新版本\n主密钥来自部署环境；图中无真实值",1290,160,440,190,status="infra",evidence=PREFIX+"modelgovernance/managed_config_store_postgres.go:44"),
        node("resolve","运行时解析 / 解密\nResolveDefaultManagedAPI / role binding\n同租户、active、当前能力探测版本\n显式默认不可用时不静默换供应商",650,500,480,210,evidence=PREFIX+"modelgovernance/managed_config.go:183;"+PREFIX+"modelgovernance/managed_config_store_postgres.go:211"),
        node("go","Go业务处理器 / 托管Agent\n试卷导入、评分Agent、学校助手\n将短期明文凭据用于本次HTTP调用",60,500,430,210,evidence=PREFIX+"server/application_modules.go:126;"+PREFIX+"modelgovernance/managed_agent_http.go"),
        node("external","本地或外部模型\n受控BaseURL + provider adapter\n认证header；timeouts / retries\n具体部署与密钥轮换未运行确认",1290,500,440,210,status="infra",evidence="ai-services/grading_agent/provider_adapter.py;ai-services/grading_agent/model.py"),
        node("session","用户会话与密码\n密码Argon2id；兼容bcrypt并升级\n随机token，DB保存SHA-256 hash\n授权变更提升epoch并撤销会话",60,920,780,210,evidence=PREFIX+"auth/password.go:36;"+PREFIX+"auth/token.go:11;"+PREFIX+"auth/managed_access_store.go:154"),
        node("protect","HTTP与审计控制\nCSRF、CORS、body limit、安全头\nRedis登录失败限流、MFA/step-up\n日志、指标、显式审计写入\n真实.env、口令、API Key未写入文档",950,920,780,210,evidence=PREFIX+"server/server.go:183;"+PREFIX+"server/infrastructure.go:84;"+PREFIX+"server/routes_auth.go")]
    es=[edge("admin","cipher","写入凭据"),edge("cipher","store","密文存储"),edge("store","resolve","受限配置读取",points=[[1510,420],[890,420]]),edge("resolve","go","解密后的运行配置"),edge("go","external","受控模型HTTP调用",points=[[270,800],[1510,800]],exitX=.5,exitY=1,entryX=.5,entryY=1)]
    p2=page("credential","凭据加密、使用与会话安全",ns,es,height=1290)
    ns=[node("client","浏览器 / 桌面宿主机\nWeb Admin；Student可独立构建\n桌面Tauri在客户端，不是Compose服务",60,160,440,170,status="infra",evidence="apps/student-portal/package.json;apps/desktop-client/src-tauri/tauri.conf.json"),
        node("nginx","nginx :80 → 宿主8088\n/api/* → api-gateway:8080\n/ → web-admin:80\n样例HTTP；外部TLS终止待环境确认",680,160,450,190,status="infra",evidence="infra/docker-compose/nginx/nginx.conf:45;infra/docker-compose/docker-compose.yml:753"),
        node("web","web-admin Nginx容器\nVite静态构建；默认主页管理端\nCompose没有独立student服务",1290,160,440,190,status="infra",evidence="infra/docker-compose/docker-compose.yml:408;apps/web-admin/Dockerfile"),
        node("api","api-gateway Go容器\nPostgres应用身份 / 内部TLS可配置\nAPI可绑定宿主8080\nhealth/live 与 health/ready",680,490,450,200,status="infra",evidence="infra/docker-compose/docker-compose.yml:264;services/api-gateway/cmd/api-gateway/main.go:62"),
        node("data","持久数据服务容器\nPostgreSQL16、Redis7、MinIO\nQdrant；独立Docker volumes\n数据服务默认宿主127.0.0.1绑定",60,850,440,210,status="infra",evidence="infra/docker-compose/docker-compose.yml:10;infra/docker-compose/docker-compose.yml:818"),
        node("workers","按profile启用Python Worker\nocr / processing / quality / subjective-grading\nmath-verification (math或ocr)\n运行需要匹配服务账户与模型依赖",1290,490,440,200,status="infra",evidence="infra/docker-compose/docker-compose.yml:477"),
        node("agent","grading-agent容器 :8100\nGo通过服务URL访问\nprovider adapter向宿主/外部模型发送请求",680,850,450,210,status="infra",evidence="infra/docker-compose/docker-compose.yml:430"),
        node("model","宿主llama.cpp / 外部模型\n本地启动脚本支持Qwen运行时\nCompose external provider由环境决定",1290,850,440,210,status="infra",evidence="scripts/launch-local.ps1;infra/docker-compose/docker-compose.yml:443"),
        node("ops","部署工具与维护\ntools: db-migrate / minio-init\nSQL checksum + advisory lock + 每份迁移事务\n备份、校验、restore drill、文件巡检",60,1210,790,200,status="infra",evidence="infra/docker-compose/docker-compose.yml:122;infra/docker-compose/scripts/restore-drill.ps1"),
        node("observe","observability可选profile\nPrometheus抓取API metrics → Grafana\nCI含静态/单测/race/PG/Worker/E2E门禁\n当前部署运行、告警投递与测试结果未全确认",950,1210,780,200,status="infra",evidence="infra/docker-compose/docker-compose.yml:773;.github/workflows/ci.yml")]
    es=[edge("client","nginx","HTTP入口"),edge("nginx","web","静态页面"),edge("nginx","api","/api/*"),edge("workers","api","服务账户HTTP / claim / complete"),edge("api","data","SQL/对象/限流",points=[[610,730],[280,730]],exitX=0,exitY=.5,entryX=.5,entryY=0),edge("api","agent","模型调用"),edge("agent","model","provider HTTP"),edge("observe","api","metrics scrape",dashed=True,points=[[1770,1320],[1770,790],[905,790]],exitX=1,exitY=.5,entryX=.5,entryY=1)]
    p3=page("deployment","物理部署：Compose定义与客户端宿主机",ns,es,height=1560,note="这是仓库可确认的部署定义，不是已运行拓扑；profile、地址、TLS与服务账号均取决于实际环境。密钥全部省略")
    save("10","security-deployment","权限安全部署",[p1,p2,p3])

if __name__ == "__main__":
    architecture()
    dependencies()
    security()
    print("Wrote specs 02/03/10")
