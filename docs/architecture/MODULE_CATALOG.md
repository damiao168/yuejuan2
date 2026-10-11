# Go 内部模块职责与依赖

列出 API Gateway 的 68 个 Go 内部包，每包一句职责及直接依赖。

职责依据核心函数体与调用抽读，范围和当前 SHA 保存在 [module-responsibilities.json](evidence/module-responsibilities.json)。这只证明职责说明的依据，不代表包内源码全部审读或功能完整上线。

依据 [module-imports.json](evidence/module-imports.json) 与原始文件清单；扫描只读取非测试Go源码的import字符串。下表显示直接依赖，不把依赖包的依赖算成当前包的直接依赖；标准库、第三方依赖、其他语言和动态调用不在这个索引范围。

**这是静态目录，不表示表中所有源码都已完成人工审读。**逐文件状态见 [file-review.tsv](evidence/file-review.tsv) 和 [audit-coverage.json](evidence/audit-coverage.json)。抽读文件仍保留原逐文件状态；未发现静态 import 环路不代表没有运行时耦合。

| Go包与源码目录 | 职责 | Go源码文件数（含测试） | 直接内部依赖 | import证据位置数 |
| --- | --- | ---: | --- | ---: |
| [aidisagreement](../../services/api-gateway/internal/aidisagreement) | 记录 AI 与人工评分差异，支持分类和后续处置。 [依据](../../services/api-gateway/internal/aidisagreement/service.go) | 8 | [auth](../../services/api-gateway/internal/auth), [gradingdisagreement](../../services/api-gateway/internal/gradingdisagreement), [httpx](../../services/api-gateway/internal/httpx) | 3 |
| [aieligibility](../../services/api-gateway/internal/aieligibility) | 根据题目风险、策略和质量证据决定 AI 准入。 [依据](../../services/api-gateway/internal/aieligibility/service.go) | 7 | [assessment](../../services/api-gateway/internal/assessment), [auth](../../services/api-gateway/internal/auth), [httpx](../../services/api-gateway/internal/httpx) | 7 |
| [answergroup](../../services/api-gateway/internal/answergroup) | 按文本相似度将同题答案分组，供抽检和复核。 [依据](../../services/api-gateway/internal/answergroup/store_postgres.go) | 11 | [auth](../../services/api-gateway/internal/auth), [binaryresourcehttp](../../services/api-gateway/internal/binaryresourcehttp), [goldpaper](../../services/api-gateway/internal/goldpaper), [httpx](../../services/api-gateway/internal/httpx), [logger](../../services/api-gateway/internal/logger), [paper](../../services/api-gateway/internal/paper), [segment](../../services/api-gateway/internal/segment), [taskmaterials](../../services/api-gateway/internal/taskmaterials) | 8 |
| [appeal](../../services/api-gateway/internal/appeal) | 接收成绩申诉，分派复核并记录处理结论。 [依据](../../services/api-gateway/internal/appeal/handlers.go) | 19 | [auth](../../services/api-gateway/internal/auth), [binaryresourcehttp](../../services/api-gateway/internal/binaryresourcehttp), [httpx](../../services/api-gateway/internal/httpx), [logger](../../services/api-gateway/internal/logger), [pagination](../../services/api-gateway/internal/pagination), [segment](../../services/api-gateway/internal/segment) | 9 |
| [assessment](../../services/api-gateway/internal/assessment) | 维护学科、题型、评分策略和考试题目快照。 [依据](../../services/api-gateway/internal/assessment/http.go) | 13 | [auth](../../services/api-gateway/internal/auth), [httpx](../../services/api-gateway/internal/httpx), [logger](../../services/api-gateway/internal/logger) | 3 |
| [auth](../../services/api-gateway/internal/auth) | 认证会话，检查权限与数据范围。 [依据](../../services/api-gateway/internal/auth/middleware.go) | 77 | [csvsafe](../../services/api-gateway/internal/csvsafe), [db](../../services/api-gateway/internal/db), [httpx](../../services/api-gateway/internal/httpx), [logger](../../services/api-gateway/internal/logger), [pagination](../../services/api-gateway/internal/pagination) | 32 |
| [backmark](../../services/api-gateway/internal/backmark) | 按规则筛选已阅任务，创建换人回评批次。 [依据](../../services/api-gateway/internal/backmark/service.go) | 11 | [auth](../../services/api-gateway/internal/auth), [binaryresourcehttp](../../services/api-gateway/internal/binaryresourcehttp), [httpx](../../services/api-gateway/internal/httpx), [logger](../../services/api-gateway/internal/logger), [pagination](../../services/api-gateway/internal/pagination), [paper](../../services/api-gateway/internal/paper), [regrade](../../services/api-gateway/internal/regrade), [review](../../services/api-gateway/internal/review), [segment](../../services/api-gateway/internal/segment) | 10 |
| [binaryresource](../../services/api-gateway/internal/binaryresource) | 定义私有二进制资源的元信息、延迟读取和审计约定。 [依据](../../services/api-gateway/internal/binaryresource/resource.go) | 1 | 无扫描到的直接内部import | 0 |
| [binaryresourcehttp](../../services/api-gateway/internal/binaryresourcehttp) | 将已授权私有资源输出为 HTTP 下载或图像响应。 [依据](../../services/api-gateway/internal/binaryresourcehttp/serve.go) | 2 | [auth](../../services/api-gateway/internal/auth), [binaryresource](../../services/api-gateway/internal/binaryresource), [httpx](../../services/api-gateway/internal/httpx), [logger](../../services/api-gateway/internal/logger) | 4 |
| [buildinfo](../../services/api-gateway/internal/buildinfo) | 提供构建版本、提交和发布标识。 [依据](../../services/api-gateway/internal/buildinfo/buildinfo.go) | 1 | 无扫描到的直接内部import | 0 |
| [calibration](../../services/api-gateway/internal/calibration) | 以已批准标准卷组织试评，记录阅卷资格。 [依据](../../services/api-gateway/internal/calibration/service.go) | 9 | [assessment](../../services/api-gateway/internal/assessment), [auth](../../services/api-gateway/internal/auth), [goldpaper](../../services/api-gateway/internal/goldpaper), [httpx](../../services/api-gateway/internal/httpx), [logger](../../services/api-gateway/internal/logger), [paper](../../services/api-gateway/internal/paper), [taskmaterials](../../services/api-gateway/internal/taskmaterials) | 8 |
| [capture](../../services/api-gateway/internal/capture) | 管理采集批次、印发身份、页面配准和人工确认。 [依据](../../services/api-gateway/internal/capture/handlers.go) | 31 | [auth](../../services/api-gateway/internal/auth), [exam](../../services/api-gateway/internal/exam), [files](../../services/api-gateway/internal/files), [httpx](../../services/api-gateway/internal/httpx), [logger](../../services/api-gateway/internal/logger), [pagination](../../services/api-gateway/internal/pagination), [paper](../../services/api-gateway/internal/paper), [workerruntime](../../services/api-gateway/internal/workerruntime) | 13 |
| [captureupload](../../services/api-gateway/internal/captureupload) | 接收答题卡分块上传，校验哈希并归并为文件资源。 [依据](../../services/api-gateway/internal/captureupload/service.go) | 8 | [auth](../../services/api-gateway/internal/auth), [capture](../../services/api-gateway/internal/capture), [config](../../services/api-gateway/internal/config), [files](../../services/api-gateway/internal/files), [httpx](../../services/api-gateway/internal/httpx), [logger](../../services/api-gateway/internal/logger) | 6 |
| [commandreceipt](../../services/api-gateway/internal/commandreceipt) | 在业务事务中保存命令回执，支持重试恢复。 [依据](../../services/api-gateway/internal/commandreceipt/receipt.go) | 2 | 无扫描到的直接内部import | 0 |
| [config](../../services/api-gateway/internal/config) | 加载环境配置并校验服务、依赖和安全参数。 [依据](../../services/api-gateway/internal/config/config.go) | 4 | 无扫描到的直接内部import | 0 |
| [csvsafe](../../services/api-gateway/internal/csvsafe) | 转义 CSV 中的危险公式前缀。 [依据](../../services/api-gateway/internal/csvsafe/csvsafe.go) | 2 | 无扫描到的直接内部import | 0 |
| [dashboard](../../services/api-gateway/internal/dashboard) | 汇总权限范围内的考试进度、待办和近期活动。 [依据](../../services/api-gateway/internal/dashboard/service.go) | 6 | [auth](../../services/api-gateway/internal/auth), [exam](../../services/api-gateway/internal/exam), [httpx](../../services/api-gateway/internal/httpx), [review](../../services/api-gateway/internal/review), [submission](../../services/api-gateway/internal/submission) | 8 |
| [db](../../services/api-gateway/internal/db) | 创建 PostgreSQL 连接池，按配置绑定租户上下文。 [依据](../../services/api-gateway/internal/db/postgres.go) | 7 | [config](../../services/api-gateway/internal/config) | 1 |
| [deps](../../services/api-gateway/internal/deps) | 检查数据库、缓存、存储和 AI 服务依赖状态。 [依据](../../services/api-gateway/internal/deps/deps.go) | 2 | [config](../../services/api-gateway/internal/config), [minioadapter](../../services/api-gateway/internal/minioadapter) | 2 |
| [evidence](../../services/api-gateway/internal/evidence) | 核对 AI 分数和评分证据，给出人工复核标记。 [依据](../../services/api-gateway/internal/evidence/engine.go) | 7 | [auth](../../services/api-gateway/internal/auth), [grading](../../services/api-gateway/internal/grading), [httpx](../../services/api-gateway/internal/httpx), [logger](../../services/api-gateway/internal/logger), [paper](../../services/api-gateway/internal/paper) | 7 |
| [exam](../../services/api-gateway/internal/exam) | 管理考试场次、配置和状态。 [依据](../../services/api-gateway/internal/exam/handlers.go) | 38 | [auth](../../services/api-gateway/internal/auth), [httpx](../../services/api-gateway/internal/httpx), [logger](../../services/api-gateway/internal/logger), [pagination](../../services/api-gateway/internal/pagination) | 26 |
| [files](../../services/api-gateway/internal/files) | 管理文件资源、对象上传下载和存储对账。 [依据](../../services/api-gateway/internal/files/handlers.go) | 15 | [auth](../../services/api-gateway/internal/auth), [binaryresource](../../services/api-gateway/internal/binaryresource), [binaryresourcehttp](../../services/api-gateway/internal/binaryresourcehttp), [config](../../services/api-gateway/internal/config), [httpx](../../services/api-gateway/internal/httpx), [logger](../../services/api-gateway/internal/logger), [minioadapter](../../services/api-gateway/internal/minioadapter) | 13 |
| [goldpaper](../../services/api-gateway/internal/goldpaper) | 管理标准卷的提名、版本、批准与停用。 [依据](../../services/api-gateway/internal/goldpaper/handlers.go) | 8 | [auth](../../services/api-gateway/internal/auth), [httpx](../../services/api-gateway/internal/httpx), [logger](../../services/api-gateway/internal/logger) | 3 |
| [graderdrift](../../services/api-gateway/internal/graderdrift) | 计算阅卷者质量滑窗，记录偏差事件并可暂停资格。 [依据](../../services/api-gateway/internal/graderdrift/service.go) | 7 | [auth](../../services/api-gateway/internal/auth), [httpx](../../services/api-gateway/internal/httpx), [logger](../../services/api-gateway/internal/logger), [seedquality](../../services/api-gateway/internal/seedquality) | 4 |
| [grading](../../services/api-gateway/internal/grading) | 按题型规则计算客观题分数。 [依据](../../services/api-gateway/internal/grading/engine.go) | 26 | [assessment](../../services/api-gateway/internal/assessment), [auth](../../services/api-gateway/internal/auth), [files](../../services/api-gateway/internal/files), [httpx](../../services/api-gateway/internal/httpx), [logger](../../services/api-gateway/internal/logger), [paper](../../services/api-gateway/internal/paper), [workerruntime](../../services/api-gateway/internal/workerruntime) | 15 |
| [gradingdisagreement](../../services/api-gateway/internal/gradingdisagreement) | 比较两份评分的分差、评分点和证据冲突。 [依据](../../services/api-gateway/internal/gradingdisagreement/engine.go) | 3 | 无扫描到的直接内部import | 0 |
| [gradingevaluation](../../services/api-gateway/internal/gradingevaluation) | 管理离线评分评测，汇总误差和模型组指标。 [依据](../../services/api-gateway/internal/gradingevaluation/service.go) | 13 | [assessment](../../services/api-gateway/internal/assessment), [auth](../../services/api-gateway/internal/auth), [httpx](../../services/api-gateway/internal/httpx) | 4 |
| [handlers](../../services/api-gateway/internal/handlers) | 提供健康、就绪及系统能力状态接口。 [依据](../../services/api-gateway/internal/handlers/handlers.go) | 2 | [auth](../../services/api-gateway/internal/auth), [buildinfo](../../services/api-gateway/internal/buildinfo), [config](../../services/api-gateway/internal/config), [deps](../../services/api-gateway/internal/deps), [httpx](../../services/api-gateway/internal/httpx), [workerruntime](../../services/api-gateway/internal/workerruntime) | 6 |
| [httpx](../../services/api-gateway/internal/httpx) | 统一 JSON 成功响应和带追踪标识的错误响应。 [依据](../../services/api-gateway/internal/httpx/response.go) | 1 | [logger](../../services/api-gateway/internal/logger) | 1 |
| [idempotency](../../services/api-gateway/internal/idempotency) | 按请求身份和指纹缓存响应，拒绝同键不同请求。 [依据](../../services/api-gateway/internal/idempotency/middleware.go) | 7 | [auth](../../services/api-gateway/internal/auth), [commandreceipt](../../services/api-gateway/internal/commandreceipt), [httpx](../../services/api-gateway/internal/httpx) | 5 |
| [imagequality](../../services/api-gateway/internal/imagequality) | 创建页面质检任务，接收结果并支持人工覆盖。 [依据](../../services/api-gateway/internal/imagequality/handlers.go) | 9 | [auth](../../services/api-gateway/internal/auth), [capture](../../services/api-gateway/internal/capture), [files](../../services/api-gateway/internal/files), [httpx](../../services/api-gateway/internal/httpx), [logger](../../services/api-gateway/internal/logger), [submission](../../services/api-gateway/internal/submission), [workerruntime](../../services/api-gateway/internal/workerruntime) | 10 |
| [logger](../../services/api-gateway/internal/logger) | 输出带请求标识的结构化日志，并处理敏感字段。 [依据](../../services/api-gateway/internal/logger/logger.go) | 2 | 无扫描到的直接内部import | 0 |
| [mathunderstanding](../../services/api-gateway/internal/mathunderstanding) | 关联数学答案结构与冻结评分点，生成待教师确认的评分建议。 [依据](../../services/api-gateway/internal/mathunderstanding/scorer.go) | 34 | [auth](../../services/api-gateway/internal/auth), [grading](../../services/api-gateway/internal/grading), [httpx](../../services/api-gateway/internal/httpx), [logger](../../services/api-gateway/internal/logger), [paper](../../services/api-gateway/internal/paper), [workerruntime](../../services/api-gateway/internal/workerruntime) | 9 |
| [middleware](../../services/api-gateway/internal/middleware) | 统一处理安全头、CORS、CSRF、请求标识和异常恢复。 [依据](../../services/api-gateway/internal/middleware/middleware.go) | 2 | [httpx](../../services/api-gateway/internal/httpx), [logger](../../services/api-gateway/internal/logger) | 2 |
| [minioadapter](../../services/api-gateway/internal/minioadapter) | 创建共享 TLS 信任配置的 MinIO 客户端。 [依据](../../services/api-gateway/internal/minioadapter/client.go) | 2 | [config](../../services/api-gateway/internal/config) | 1 |
| [modelcalibration](../../services/api-gateway/internal/modelcalibration) | 以完成的评分评测建立模型置信度校准，支持审批与失效。 [依据](../../services/api-gateway/internal/modelcalibration/service.go) | 7 | [auth](../../services/api-gateway/internal/auth), [gradingevaluation](../../services/api-gateway/internal/gradingevaluation), [httpx](../../services/api-gateway/internal/httpx) | 3 |
| [modelgovernance](../../services/api-gateway/internal/modelgovernance) | 管理供应商、模型配置、租户使用策略及评测审批。 [依据](../../services/api-gateway/internal/modelgovernance/handlers.go) | 53 | [assessment](../../services/api-gateway/internal/assessment), [auth](../../services/api-gateway/internal/auth), [httpx](../../services/api-gateway/internal/httpx), [logger](../../services/api-gateway/internal/logger), [pagination](../../services/api-gateway/internal/pagination) | 12 |
| [observability](../../services/api-gateway/internal/observability) | 采集请求、数据库与模型调用指标。 [依据](../../services/api-gateway/internal/observability/metrics.go) | 3 | 无扫描到的直接内部import | 0 |
| [ocr](../../services/api-gateway/internal/ocr) | 管理答卷 OCR 任务与识别结果。 [依据](../../services/api-gateway/internal/ocr/handlers.go) | 16 | [auth](../../services/api-gateway/internal/auth), [httpx](../../services/api-gateway/internal/httpx), [logger](../../services/api-gateway/internal/logger), [pagination](../../services/api-gateway/internal/pagination), [submission](../../services/api-gateway/internal/submission), [workerruntime](../../services/api-gateway/internal/workerruntime) | 9 |
| [onboarding](../../services/api-gateway/internal/onboarding) | 汇总平台或学校管理员的初始化检查与下一步操作。 [依据](../../services/api-gateway/internal/onboarding/service.go) | 5 | [auth](../../services/api-gateway/internal/auth), [httpx](../../services/api-gateway/internal/httpx) | 2 |
| [orchestrator](../../services/api-gateway/internal/orchestrator) | 保存智能体运行与任务，处理任务状态和依赖。 [依据](../../services/api-gateway/internal/orchestrator/handlers.go) | 6 | [auth](../../services/api-gateway/internal/auth), [httpx](../../services/api-gateway/internal/httpx), [logger](../../services/api-gateway/internal/logger) | 3 |
| [org](../../services/api-gateway/internal/org) | 管理租户、学校、年级、班级、学生及教师归属。 [依据](../../services/api-gateway/internal/org/handlers.go) | 10 | [auth](../../services/api-gateway/internal/auth), [httpx](../../services/api-gateway/internal/httpx), [logger](../../services/api-gateway/internal/logger), [pagination](../../services/api-gateway/internal/pagination) | 4 |
| [outbox](../../services/api-gateway/internal/outbox) | 领取事务外发事件，投递并记录成功或重试。 [依据](../../services/api-gateway/internal/outbox/outbox.go) | 4 | [logger](../../services/api-gateway/internal/logger) | 1 |
| [pagination](../../services/api-gateway/internal/pagination) | 编解码分页游标并校验页大小。 [依据](../../services/api-gateway/internal/pagination/cursor.go) | 2 | 无扫描到的直接内部import | 0 |
| [paper](../../services/api-gateway/internal/paper) | 管理试卷、题目和评分细则，组织考试资料解析。 [依据](../../services/api-gateway/internal/paper/handlers.go) | 99 | [assessment](../../services/api-gateway/internal/assessment), [auth](../../services/api-gateway/internal/auth), [exam](../../services/api-gateway/internal/exam), [files](../../services/api-gateway/internal/files), [httpx](../../services/api-gateway/internal/httpx), [logger](../../services/api-gateway/internal/logger), [workerruntime](../../services/api-gateway/internal/workerruntime) | 15 |
| [platformschools](../../services/api-gateway/internal/platformschools) | 为平台汇总学校成员、用量、模型健康和活动。 [依据](../../services/api-gateway/internal/platformschools/service.go) | 9 | [httpx](../../services/api-gateway/internal/httpx) | 1 |
| [processing](../../services/api-gateway/internal/processing) | 汇总流水线进度与异常，支持分派、处置和重试。 [依据](../../services/api-gateway/internal/processing/service.go) | 15 | [assessment](../../services/api-gateway/internal/assessment), [auth](../../services/api-gateway/internal/auth), [binaryresourcehttp](../../services/api-gateway/internal/binaryresourcehttp), [files](../../services/api-gateway/internal/files), [httpx](../../services/api-gateway/internal/httpx), [logger](../../services/api-gateway/internal/logger), [workerruntime](../../services/api-gateway/internal/workerruntime), [workflowactors](../../services/api-gateway/internal/workflowactors) | 16 |
| [qualitydashboard](../../services/api-gateway/internal/qualitydashboard) | 按题汇总标准卷、试评、盲投和偏差证据，形成质量门禁。 [依据](../../services/api-gateway/internal/qualitydashboard/service.go) | 7 | [answergroup](../../services/api-gateway/internal/answergroup), [auth](../../services/api-gateway/internal/auth), [backmark](../../services/api-gateway/internal/backmark), [goldpaper](../../services/api-gateway/internal/goldpaper), [graderdrift](../../services/api-gateway/internal/graderdrift), [httpx](../../services/api-gateway/internal/httpx), [review](../../services/api-gateway/internal/review), [seedquality](../../services/api-gateway/internal/seedquality) | 12 |
| [questionbank](../../services/api-gateway/internal/questionbank) | 管理题库、题目版本、复核、模板及考试引用。 [依据](../../services/api-gateway/internal/questionbank/handlers.go) | 27 | [assessment](../../services/api-gateway/internal/assessment), [auth](../../services/api-gateway/internal/auth), [commandreceipt](../../services/api-gateway/internal/commandreceipt), [files](../../services/api-gateway/internal/files), [httpx](../../services/api-gateway/internal/httpx), [paper](../../services/api-gateway/internal/paper) | 25 |
| [regrade](../../services/api-gateway/internal/regrade) | 基于已发布版本创建独立重评，形成后继发布计划。 [依据](../../services/api-gateway/internal/regrade/service.go) | 11 | [assessment](../../services/api-gateway/internal/assessment), [auth](../../services/api-gateway/internal/auth), [binaryresourcehttp](../../services/api-gateway/internal/binaryresourcehttp), [httpx](../../services/api-gateway/internal/httpx), [logger](../../services/api-gateway/internal/logger), [paper](../../services/api-gateway/internal/paper), [segment](../../services/api-gateway/internal/segment), [taskmaterials](../../services/api-gateway/internal/taskmaterials) | 10 |
| [regraderelease](../../services/api-gateway/internal/regraderelease) | 将已复核重评计划转为新的成绩发布草稿。 [依据](../../services/api-gateway/internal/regraderelease/service.go) | 3 | [auth](../../services/api-gateway/internal/auth), [httpx](../../services/api-gateway/internal/httpx), [regrade](../../services/api-gateway/internal/regrade), [scorerelease](../../services/api-gateway/internal/scorerelease) | 6 |
| [releasegate](../../services/api-gateway/internal/releasegate) | 评估成绩发布策略与质量证据，记录豁免审批。 [依据](../../services/api-gateway/internal/releasegate/service.go) | 7 | [auth](../../services/api-gateway/internal/auth), [httpx](../../services/api-gateway/internal/httpx), [logger](../../services/api-gateway/internal/logger), [scorerelease](../../services/api-gateway/internal/scorerelease) | 6 |
| [report](../../services/api-gateway/internal/report) | 生成学生、班级、题目和阅卷质量统计及导出。 [依据](../../services/api-gateway/internal/report/handlers.go) | 10 | [auth](../../services/api-gateway/internal/auth), [commandreceipt](../../services/api-gateway/internal/commandreceipt), [csvsafe](../../services/api-gateway/internal/csvsafe), [httpx](../../services/api-gateway/internal/httpx), [logger](../../services/api-gateway/internal/logger) | 10 |
| [review](../../services/api-gateway/internal/review) | 分派人工阅卷任务，处理单评、双评和仲裁结算。 [依据](../../services/api-gateway/internal/review/handlers.go) | 34 | [aidisagreement](../../services/api-gateway/internal/aidisagreement), [assessment](../../services/api-gateway/internal/assessment), [auth](../../services/api-gateway/internal/auth), [binaryresourcehttp](../../services/api-gateway/internal/binaryresourcehttp), [commandreceipt](../../services/api-gateway/internal/commandreceipt), [files](../../services/api-gateway/internal/files), [httpx](../../services/api-gateway/internal/httpx), [logger](../../services/api-gateway/internal/logger), [pagination](../../services/api-gateway/internal/pagination), [paper](../../services/api-gateway/internal/paper), [seedquality](../../services/api-gateway/internal/seedquality), [segment](../../services/api-gateway/internal/segment), [workflowactors](../../services/api-gateway/internal/workflowactors) | 26 |
| [reviewannotation](../../services/api-gateway/internal/reviewannotation) | 管理阅卷批注、评语模板及学生可见内容。 [依据](../../services/api-gateway/internal/reviewannotation/handlers.go) | 8 | [auth](../../services/api-gateway/internal/auth), [httpx](../../services/api-gateway/internal/httpx), [logger](../../services/api-gateway/internal/logger) | 3 |
| [schoolassistant](../../services/api-gateway/internal/schoolassistant) | 汇集授权的考试事实，流式返回学校业务查询结果。 [依据](../../services/api-gateway/internal/schoolassistant/service.go) | 15 | [auth](../../services/api-gateway/internal/auth), [exam](../../services/api-gateway/internal/exam), [httpx](../../services/api-gateway/internal/httpx), [modelgovernance](../../services/api-gateway/internal/modelgovernance), [pagination](../../services/api-gateway/internal/pagination), [releasegate](../../services/api-gateway/internal/releasegate), [scorerelease](../../services/api-gateway/internal/scorerelease), [workspace](../../services/api-gateway/internal/workspace) | 23 |
| [score](../../services/api-gateway/internal/score) | 汇总和确认成绩，保留旧发布与成绩查询接口。 [依据](../../services/api-gateway/internal/score/handlers.go) | 8 | [auth](../../services/api-gateway/internal/auth), [commandreceipt](../../services/api-gateway/internal/commandreceipt), [csvsafe](../../services/api-gateway/internal/csvsafe), [httpx](../../services/api-gateway/internal/httpx), [logger](../../services/api-gateway/internal/logger), [pagination](../../services/api-gateway/internal/pagination) | 12 |
| [scorerelease](../../services/api-gateway/internal/scorerelease) | 冻结成绩发布版本，管理发布、回滚和学生可见结果。 [依据](../../services/api-gateway/internal/scorerelease/service.go) | 24 | [auth](../../services/api-gateway/internal/auth), [binaryresource](../../services/api-gateway/internal/binaryresource), [binaryresourcehttp](../../services/api-gateway/internal/binaryresourcehttp), [files](../../services/api-gateway/internal/files), [httpx](../../services/api-gateway/internal/httpx), [logger](../../services/api-gateway/internal/logger), [paper](../../services/api-gateway/internal/paper), [qualitydashboard](../../services/api-gateway/internal/qualitydashboard), [segment](../../services/api-gateway/internal/segment) | 9 |
| [seedquality](../../services/api-gateway/internal/seedquality) | 向阅卷队列盲投标准卷，记录质量观察。 [依据](../../services/api-gateway/internal/seedquality/service.go) | 9 | [assessment](../../services/api-gateway/internal/assessment), [auth](../../services/api-gateway/internal/auth), [calibration](../../services/api-gateway/internal/calibration), [commandreceipt](../../services/api-gateway/internal/commandreceipt), [goldpaper](../../services/api-gateway/internal/goldpaper), [httpx](../../services/api-gateway/internal/httpx), [logger](../../services/api-gateway/internal/logger), [paper](../../services/api-gateway/internal/paper) | 12 |
| [segment](../../services/api-gateway/internal/segment) | 管理答案片段、坐标和受控图像读取。 [依据](../../services/api-gateway/internal/segment/handlers.go) | 8 | [auth](../../services/api-gateway/internal/auth), [binaryresource](../../services/api-gateway/internal/binaryresource), [binaryresourcehttp](../../services/api-gateway/internal/binaryresourcehttp), [files](../../services/api-gateway/internal/files), [httpx](../../services/api-gateway/internal/httpx), [logger](../../services/api-gateway/internal/logger), [paper](../../services/api-gateway/internal/paper), [submission](../../services/api-gateway/internal/submission) | 10 |
| [server](../../services/api-gateway/internal/server) | 组装业务模块与存储，注册路由和入口守卫。 [依据](../../services/api-gateway/internal/server/application_modules.go) | 95 | [aidisagreement](../../services/api-gateway/internal/aidisagreement), [aieligibility](../../services/api-gateway/internal/aieligibility), [answergroup](../../services/api-gateway/internal/answergroup), [appeal](../../services/api-gateway/internal/appeal), [assessment](../../services/api-gateway/internal/assessment), [auth](../../services/api-gateway/internal/auth), [backmark](../../services/api-gateway/internal/backmark), [calibration](../../services/api-gateway/internal/calibration), [capture](../../services/api-gateway/internal/capture), [captureupload](../../services/api-gateway/internal/captureupload), [config](../../services/api-gateway/internal/config), [dashboard](../../services/api-gateway/internal/dashboard), [db](../../services/api-gateway/internal/db), [deps](../../services/api-gateway/internal/deps), [evidence](../../services/api-gateway/internal/evidence), [exam](../../services/api-gateway/internal/exam), [files](../../services/api-gateway/internal/files), [goldpaper](../../services/api-gateway/internal/goldpaper), [graderdrift](../../services/api-gateway/internal/graderdrift), [grading](../../services/api-gateway/internal/grading), [gradingevaluation](../../services/api-gateway/internal/gradingevaluation), [handlers](../../services/api-gateway/internal/handlers), [httpx](../../services/api-gateway/internal/httpx), [idempotency](../../services/api-gateway/internal/idempotency), [imagequality](../../services/api-gateway/internal/imagequality), [logger](../../services/api-gateway/internal/logger), [mathunderstanding](../../services/api-gateway/internal/mathunderstanding), [middleware](../../services/api-gateway/internal/middleware), [modelcalibration](../../services/api-gateway/internal/modelcalibration), [modelgovernance](../../services/api-gateway/internal/modelgovernance), [observability](../../services/api-gateway/internal/observability), [ocr](../../services/api-gateway/internal/ocr), [onboarding](../../services/api-gateway/internal/onboarding), [orchestrator](../../services/api-gateway/internal/orchestrator), [org](../../services/api-gateway/internal/org), [outbox](../../services/api-gateway/internal/outbox), [paper](../../services/api-gateway/internal/paper), [platformschools](../../services/api-gateway/internal/platformschools), [processing](../../services/api-gateway/internal/processing), [qualitydashboard](../../services/api-gateway/internal/qualitydashboard), [questionbank](../../services/api-gateway/internal/questionbank), [regrade](../../services/api-gateway/internal/regrade), [regraderelease](../../services/api-gateway/internal/regraderelease), [releasegate](../../services/api-gateway/internal/releasegate), [report](../../services/api-gateway/internal/report), [review](../../services/api-gateway/internal/review), [reviewannotation](../../services/api-gateway/internal/reviewannotation), [schoolassistant](../../services/api-gateway/internal/schoolassistant), [score](../../services/api-gateway/internal/score), [scorerelease](../../services/api-gateway/internal/scorerelease), [seedquality](../../services/api-gateway/internal/seedquality), [segment](../../services/api-gateway/internal/segment), [studentportal](../../services/api-gateway/internal/studentportal), [subjective](../../services/api-gateway/internal/subjective), [submission](../../services/api-gateway/internal/submission), [taskmaterials](../../services/api-gateway/internal/taskmaterials), [workerruntime](../../services/api-gateway/internal/workerruntime), [workspace](../../services/api-gateway/internal/workspace) | 203 |
| [studentportal](../../services/api-gateway/internal/studentportal) | 为学生列出本人有当前成绩发布版本的考试。 [依据](../../services/api-gateway/internal/studentportal/service.go) | 6 | [auth](../../services/api-gateway/internal/auth), [httpx](../../services/api-gateway/internal/httpx) | 2 |
| [subjective](../../services/api-gateway/internal/subjective) | 组织主观题 AI 评分、确定性结算和人工复核。 [依据](../../services/api-gateway/internal/subjective/handlers_grade.go) | 63 | [aieligibility](../../services/api-gateway/internal/aieligibility), [assessment](../../services/api-gateway/internal/assessment), [auth](../../services/api-gateway/internal/auth), [files](../../services/api-gateway/internal/files), [grading](../../services/api-gateway/internal/grading), [gradingdisagreement](../../services/api-gateway/internal/gradingdisagreement), [gradingevaluation](../../services/api-gateway/internal/gradingevaluation), [httpx](../../services/api-gateway/internal/httpx), [logger](../../services/api-gateway/internal/logger), [mathunderstanding](../../services/api-gateway/internal/mathunderstanding), [modelcalibration](../../services/api-gateway/internal/modelcalibration), [modelgovernance](../../services/api-gateway/internal/modelgovernance), [paper](../../services/api-gateway/internal/paper), [segment](../../services/api-gateway/internal/segment), [workerruntime](../../services/api-gateway/internal/workerruntime) | 58 |
| [submission](../../services/api-gateway/internal/submission) | 管理考生答卷、页面和质量状态。 [依据](../../services/api-gateway/internal/submission/handlers.go) | 9 | [auth](../../services/api-gateway/internal/auth), [files](../../services/api-gateway/internal/files), [httpx](../../services/api-gateway/internal/httpx), [logger](../../services/api-gateway/internal/logger), [pagination](../../services/api-gateway/internal/pagination) | 5 |
| [taskmaterials](../../services/api-gateway/internal/taskmaterials) | 按任务快照读取题干、参考答案、解析和评分细则。 [依据](../../services/api-gateway/internal/taskmaterials/question.go) | 2 | [paper](../../services/api-gateway/internal/paper) | 1 |
| [workerruntime](../../services/api-gateway/internal/workerruntime) | 统一后台任务的领取、租约、完成、失败和重试。 [依据](../../services/api-gateway/internal/workerruntime/handlers.go) | 14 | [auth](../../services/api-gateway/internal/auth), [httpx](../../services/api-gateway/internal/httpx), [logger](../../services/api-gateway/internal/logger) | 6 |
| [workflowactors](../../services/api-gateway/internal/workflowactors) | 按考试学校与任务权限列出可分派人员。 [依据](../../services/api-gateway/internal/workflowactors/directory.go) | 1 | 无扫描到的直接内部import | 0 |
| [workspace](../../services/api-gateway/internal/workspace) | 投影单场考试的准备、处理和阅卷进度，给出下一步操作。 [依据](../../services/api-gateway/internal/workspace/service.go) | 7 | [assessment](../../services/api-gateway/internal/assessment), [auth](../../services/api-gateway/internal/auth), [exam](../../services/api-gateway/internal/exam), [httpx](../../services/api-gateway/internal/httpx), [paper](../../services/api-gateway/internal/paper), [processing](../../services/api-gateway/internal/processing), [review](../../services/api-gateway/internal/review), [submission](../../services/api-gateway/internal/submission) | 12 |

## 每个直接依赖的源码依据

每个目标包列出全部扫描命中位置；同一源码重复import记录按路径和行号去重。行号以生成时工作区为准，长期更新应重新生成。

### aidisagreement

记录 AI 与人工评分差异，支持分类和后续处置。

源码目录：[services/api-gateway/internal/aidisagreement](../../services/api-gateway/internal/aidisagreement)。

职责依据：[service.go](../../services/api-gateway/internal/aidisagreement/service.go)（抽读 1–24、31–52 行）。完整 SHA 见职责证据 JSON。

- `aidisagreement → auth`：[handlers.go:11](../../services/api-gateway/internal/aidisagreement/handlers.go)
- `aidisagreement → gradingdisagreement`：[service.go:13](../../services/api-gateway/internal/aidisagreement/service.go)
- `aidisagreement → httpx`：[handlers.go:12](../../services/api-gateway/internal/aidisagreement/handlers.go)

### aieligibility

根据题目风险、策略和质量证据决定 AI 准入。

源码目录：[services/api-gateway/internal/aieligibility](../../services/api-gateway/internal/aieligibility)。

职责依据：[service.go](../../services/api-gateway/internal/aieligibility/service.go)（抽读 1–24、40–89 行）。完整 SHA 见职责证据 JSON。

- `aieligibility → assessment`：[handlers.go:10](../../services/api-gateway/internal/aieligibility/handlers.go), [service.go:11](../../services/api-gateway/internal/aieligibility/service.go), [store_memory.go:9](../../services/api-gateway/internal/aieligibility/store_memory.go), [store_postgres.go:10](../../services/api-gateway/internal/aieligibility/store_postgres.go), [types.go:9](../../services/api-gateway/internal/aieligibility/types.go)
- `aieligibility → auth`：[handlers.go:11](../../services/api-gateway/internal/aieligibility/handlers.go)
- `aieligibility → httpx`：[handlers.go:12](../../services/api-gateway/internal/aieligibility/handlers.go)

### answergroup

按文本相似度将同题答案分组，供抽检和复核。

源码目录：[services/api-gateway/internal/answergroup](../../services/api-gateway/internal/answergroup)。

职责依据：[store_postgres.go](../../services/api-gateway/internal/answergroup/store_postgres.go)（抽读 1–24、31–80 行）。完整 SHA 见职责证据 JSON。

- `answergroup → auth`：[handlers.go:9](../../services/api-gateway/internal/answergroup/handlers.go)
- `answergroup → binaryresourcehttp`：[handlers.go:10](../../services/api-gateway/internal/answergroup/handlers.go)
- `answergroup → goldpaper`：[handlers.go:11](../../services/api-gateway/internal/answergroup/handlers.go)
- `answergroup → httpx`：[handlers.go:12](../../services/api-gateway/internal/answergroup/handlers.go)
- `answergroup → logger`：[handlers.go:13](../../services/api-gateway/internal/answergroup/handlers.go)
- `answergroup → paper`：[member_context.go:6](../../services/api-gateway/internal/answergroup/member_context.go)
- `answergroup → segment`：[handlers.go:14](../../services/api-gateway/internal/answergroup/handlers.go)
- `answergroup → taskmaterials`：[member_context.go:7](../../services/api-gateway/internal/answergroup/member_context.go)

### appeal

接收成绩申诉，分派复核并记录处理结论。

源码目录：[services/api-gateway/internal/appeal](../../services/api-gateway/internal/appeal)。

职责依据：[handlers.go](../../services/api-gateway/internal/appeal/handlers.go)（抽读 1–24、30–58 行）。完整 SHA 见职责证据 JSON。

- `appeal → auth`：[handlers.go:9](../../services/api-gateway/internal/appeal/handlers.go), [published_question_handlers.go:8](../../services/api-gateway/internal/appeal/published_question_handlers.go)
- `appeal → binaryresourcehttp`：[published_question_handlers.go:9](../../services/api-gateway/internal/appeal/published_question_handlers.go)
- `appeal → httpx`：[handlers.go:10](../../services/api-gateway/internal/appeal/handlers.go), [published_question_handlers.go:10](../../services/api-gateway/internal/appeal/published_question_handlers.go)
- `appeal → logger`：[handlers.go:11](../../services/api-gateway/internal/appeal/handlers.go), [published_question_handlers.go:11](../../services/api-gateway/internal/appeal/published_question_handlers.go)
- `appeal → pagination`：[handlers.go:12](../../services/api-gateway/internal/appeal/handlers.go)
- `appeal → segment`：[published_question_handlers.go:12](../../services/api-gateway/internal/appeal/published_question_handlers.go)

### assessment

维护学科、题型、评分策略和考试题目快照。

源码目录：[services/api-gateway/internal/assessment](../../services/api-gateway/internal/assessment)。

职责依据：[http.go](../../services/api-gateway/internal/assessment/http.go)（抽读 1–24、67–86 行）。完整 SHA 见职责证据 JSON。

- `assessment → auth`：[http.go:10](../../services/api-gateway/internal/assessment/http.go)
- `assessment → httpx`：[http.go:11](../../services/api-gateway/internal/assessment/http.go)
- `assessment → logger`：[http.go:12](../../services/api-gateway/internal/assessment/http.go)

### auth

认证会话，检查权限与数据范围。

源码目录：[services/api-gateway/internal/auth](../../services/api-gateway/internal/auth)。

职责依据：[middleware.go](../../services/api-gateway/internal/auth/middleware.go)（抽读 1–24、26–64、108–124、65–86 行）。完整 SHA 见职责证据 JSON。

- `auth → csvsafe`：[handlers_audit.go:12](../../services/api-gateway/internal/auth/handlers_audit.go)
- `auth → db`：[context.go:6](../../services/api-gateway/internal/auth/context.go)
- `auth → httpx`：[access_scope.go:11](../../services/api-gateway/internal/auth/access_scope.go), [activation.go:9](../../services/api-gateway/internal/auth/activation.go), [archive_boundary.go:7](../../services/api-gateway/internal/auth/archive_boundary.go), [handlers.go:11](../../services/api-gateway/internal/auth/handlers.go), [handlers_audit.go:13](../../services/api-gateway/internal/auth/handlers_audit.go), [handlers_session.go:10](../../services/api-gateway/internal/auth/handlers_session.go), [login_helpers.go:10](../../services/api-gateway/internal/auth/login_helpers.go), [managed_access_handler.go:8](../../services/api-gateway/internal/auth/managed_access_handler.go), [mfa_handlers.go:15](../../services/api-gateway/internal/auth/mfa_handlers.go), [middleware.go:9](../../services/api-gateway/internal/auth/middleware.go), [recovery.go:9](../../services/api-gateway/internal/auth/recovery.go), [resource_scope.go:10](../../services/api-gateway/internal/auth/resource_scope.go), [security_events.go:8](../../services/api-gateway/internal/auth/security_events.go), [step_up.go:7](../../services/api-gateway/internal/auth/step_up.go), [user_admin.go:11](../../services/api-gateway/internal/auth/user_admin.go), [wechat.go:16](../../services/api-gateway/internal/auth/wechat.go)
- `auth → logger`：[activation.go:10](../../services/api-gateway/internal/auth/activation.go), [audit_record.go:7](../../services/api-gateway/internal/auth/audit_record.go), [handlers.go:12](../../services/api-gateway/internal/auth/handlers.go), [handlers_audit.go:14](../../services/api-gateway/internal/auth/handlers_audit.go), [handlers_session.go:11](../../services/api-gateway/internal/auth/handlers_session.go), [managed_access_handler.go:9](../../services/api-gateway/internal/auth/managed_access_handler.go), [mfa_handlers.go:16](../../services/api-gateway/internal/auth/mfa_handlers.go), [mfa_security_changes.go:11](../../services/api-gateway/internal/auth/mfa_security_changes.go), [recovery.go:10](../../services/api-gateway/internal/auth/recovery.go), [store_postgres.go:13](../../services/api-gateway/internal/auth/store_postgres.go), [user_admin.go:12](../../services/api-gateway/internal/auth/user_admin.go), [wechat.go:17](../../services/api-gateway/internal/auth/wechat.go)
- `auth → pagination`：[handlers_audit.go:15](../../services/api-gateway/internal/auth/handlers_audit.go), [user_admin.go:13](../../services/api-gateway/internal/auth/user_admin.go)

### backmark

按规则筛选已阅任务，创建换人回评批次。

源码目录：[services/api-gateway/internal/backmark](../../services/api-gateway/internal/backmark)。

职责依据：[service.go](../../services/api-gateway/internal/backmark/service.go)（抽读 1–24、59–91 行）。完整 SHA 见职责证据 JSON。

- `backmark → auth`：[handlers.go:10](../../services/api-gateway/internal/backmark/handlers.go)
- `backmark → binaryresourcehttp`：[handlers.go:11](../../services/api-gateway/internal/backmark/handlers.go)
- `backmark → httpx`：[handlers.go:12](../../services/api-gateway/internal/backmark/handlers.go)
- `backmark → logger`：[handlers.go:13](../../services/api-gateway/internal/backmark/handlers.go)
- `backmark → pagination`：[handlers.go:14](../../services/api-gateway/internal/backmark/handlers.go)
- `backmark → paper`：[service.go:12](../../services/api-gateway/internal/backmark/service.go), [types.go:9](../../services/api-gateway/internal/backmark/types.go)
- `backmark → regrade`：[handlers.go:15](../../services/api-gateway/internal/backmark/handlers.go)
- `backmark → review`：[service.go:13](../../services/api-gateway/internal/backmark/service.go)
- `backmark → segment`：[handlers.go:16](../../services/api-gateway/internal/backmark/handlers.go)

### binaryresource

定义私有二进制资源的元信息、延迟读取和审计约定。

源码目录：[services/api-gateway/internal/binaryresource](../../services/api-gateway/internal/binaryresource)。

职责依据：[resource.go](../../services/api-gateway/internal/binaryresource/resource.go)（抽读 1–22 行）。完整 SHA 见职责证据 JSON。

没有扫描到直接内部import；这不代表没有第三方依赖或通过接口注入的调用。

### binaryresourcehttp

将已授权私有资源输出为 HTTP 下载或图像响应。

源码目录：[services/api-gateway/internal/binaryresourcehttp](../../services/api-gateway/internal/binaryresourcehttp)。

职责依据：[serve.go](../../services/api-gateway/internal/binaryresourcehttp/serve.go)（抽读 1–24、15–56 行）。完整 SHA 见职责证据 JSON。

- `binaryresourcehttp → auth`：[serve.go:8](../../services/api-gateway/internal/binaryresourcehttp/serve.go)
- `binaryresourcehttp → binaryresource`：[serve.go:9](../../services/api-gateway/internal/binaryresourcehttp/serve.go)
- `binaryresourcehttp → httpx`：[serve.go:10](../../services/api-gateway/internal/binaryresourcehttp/serve.go)
- `binaryresourcehttp → logger`：[serve.go:11](../../services/api-gateway/internal/binaryresourcehttp/serve.go)

### buildinfo

提供构建版本、提交和发布标识。

源码目录：[services/api-gateway/internal/buildinfo](../../services/api-gateway/internal/buildinfo)。

职责依据：[buildinfo.go](../../services/api-gateway/internal/buildinfo/buildinfo.go)（抽读 1–18、13–18 行）。完整 SHA 见职责证据 JSON。

没有扫描到直接内部import；这不代表没有第三方依赖或通过接口注入的调用。

### calibration

以已批准标准卷组织试评，记录阅卷资格。

源码目录：[services/api-gateway/internal/calibration](../../services/api-gateway/internal/calibration)。

职责依据：[service.go](../../services/api-gateway/internal/calibration/service.go)（抽读 1–24、50–73 行）。完整 SHA 见职责证据 JSON。

- `calibration → assessment`：[service.go:15](../../services/api-gateway/internal/calibration/service.go)
- `calibration → auth`：[handlers.go:10](../../services/api-gateway/internal/calibration/handlers.go)
- `calibration → goldpaper`：[service.go:16](../../services/api-gateway/internal/calibration/service.go)
- `calibration → httpx`：[handlers.go:11](../../services/api-gateway/internal/calibration/handlers.go)
- `calibration → logger`：[handlers.go:12](../../services/api-gateway/internal/calibration/handlers.go)
- `calibration → paper`：[service.go:17](../../services/api-gateway/internal/calibration/service.go), [types.go:5](../../services/api-gateway/internal/calibration/types.go)
- `calibration → taskmaterials`：[service.go:18](../../services/api-gateway/internal/calibration/service.go)

### capture

管理采集批次、印发身份、页面配准和人工确认。

源码目录：[services/api-gateway/internal/capture](../../services/api-gateway/internal/capture)。

职责依据：[handlers.go](../../services/api-gateway/internal/capture/handlers.go)（抽读 1–24、39–75、687–700、91–115、657–665、726–772 行）。完整 SHA 见职责证据 JSON。

- `capture → auth`：[handlers.go:11](../../services/api-gateway/internal/capture/handlers.go), [scan_context.go:8](../../services/api-gateway/internal/capture/scan_context.go)
- `capture → exam`：[handlers.go:12](../../services/api-gateway/internal/capture/handlers.go), [scan_context.go:9](../../services/api-gateway/internal/capture/scan_context.go)
- `capture → files`：[handlers.go:13](../../services/api-gateway/internal/capture/handlers.go)
- `capture → httpx`：[handlers.go:14](../../services/api-gateway/internal/capture/handlers.go), [scan_context.go:10](../../services/api-gateway/internal/capture/scan_context.go), [target.go:5](../../services/api-gateway/internal/capture/target.go)
- `capture → logger`：[handlers.go:15](../../services/api-gateway/internal/capture/handlers.go)
- `capture → pagination`：[handlers.go:16](../../services/api-gateway/internal/capture/handlers.go)
- `capture → paper`：[scan_context.go:11](../../services/api-gateway/internal/capture/scan_context.go)
- `capture → workerruntime`：[handlers.go:17](../../services/api-gateway/internal/capture/handlers.go), [template_match_store_postgres.go:10](../../services/api-gateway/internal/capture/template_match_store_postgres.go)

### captureupload

接收答题卡分块上传，校验哈希并归并为文件资源。

源码目录：[services/api-gateway/internal/captureupload](../../services/api-gateway/internal/captureupload)。

职责依据：[service.go](../../services/api-gateway/internal/captureupload/service.go)（抽读 1–24、91–140 行）。完整 SHA 见职责证据 JSON。

- `captureupload → auth`：[handlers.go:11](../../services/api-gateway/internal/captureupload/handlers.go)
- `captureupload → capture`：[service.go:14](../../services/api-gateway/internal/captureupload/service.go)
- `captureupload → config`：[service.go:15](../../services/api-gateway/internal/captureupload/service.go)
- `captureupload → files`：[service.go:16](../../services/api-gateway/internal/captureupload/service.go)
- `captureupload → httpx`：[handlers.go:12](../../services/api-gateway/internal/captureupload/handlers.go)
- `captureupload → logger`：[handlers.go:13](../../services/api-gateway/internal/captureupload/handlers.go)

### commandreceipt

在业务事务中保存命令回执，支持重试恢复。

源码目录：[services/api-gateway/internal/commandreceipt](../../services/api-gateway/internal/commandreceipt)。

职责依据：[receipt.go](../../services/api-gateway/internal/commandreceipt/receipt.go)（抽读 1–24、63–93 行）。完整 SHA 见职责证据 JSON。

没有扫描到直接内部import；这不代表没有第三方依赖或通过接口注入的调用。

### config

加载环境配置并校验服务、依赖和安全参数。

源码目录：[services/api-gateway/internal/config](../../services/api-gateway/internal/config)。

职责依据：[config.go](../../services/api-gateway/internal/config/config.go)（抽读 1–24、170–219 行）。完整 SHA 见职责证据 JSON。

没有扫描到直接内部import；这不代表没有第三方依赖或通过接口注入的调用。

### csvsafe

转义 CSV 中的危险公式前缀。

源码目录：[services/api-gateway/internal/csvsafe](../../services/api-gateway/internal/csvsafe)。

职责依据：[csvsafe.go](../../services/api-gateway/internal/csvsafe/csvsafe.go)（抽读 1–24、6–18 行）。完整 SHA 见职责证据 JSON。

没有扫描到直接内部import；这不代表没有第三方依赖或通过接口注入的调用。

### dashboard

汇总权限范围内的考试进度、待办和近期活动。

源码目录：[services/api-gateway/internal/dashboard](../../services/api-gateway/internal/dashboard)。

职责依据：[service.go](../../services/api-gateway/internal/dashboard/service.go)（抽读 1–24、42–91 行）。完整 SHA 见职责证据 JSON。

- `dashboard → auth`：[activity_store.go:8](../../services/api-gateway/internal/dashboard/activity_store.go), [handlers.go:7](../../services/api-gateway/internal/dashboard/handlers.go), [organization_postgres.go:9](../../services/api-gateway/internal/dashboard/organization_postgres.go), [service.go:10](../../services/api-gateway/internal/dashboard/service.go)
- `dashboard → exam`：[service.go:11](../../services/api-gateway/internal/dashboard/service.go)
- `dashboard → httpx`：[handlers.go:8](../../services/api-gateway/internal/dashboard/handlers.go)
- `dashboard → review`：[service.go:12](../../services/api-gateway/internal/dashboard/service.go)
- `dashboard → submission`：[service.go:13](../../services/api-gateway/internal/dashboard/service.go)

### db

创建 PostgreSQL 连接池，按配置绑定租户上下文。

源码目录：[services/api-gateway/internal/db](../../services/api-gateway/internal/db)。

职责依据：[tenant_context.go](../../services/api-gateway/internal/db/tenant_context.go)（抽读 1–24、28–32、74–82、104–122 行）；[postgres.go](../../services/api-gateway/internal/db/postgres.go)（抽读 1–24、26–67 行）。完整 SHA 见职责证据 JSON。

- `db → config`：[postgres.go:13](../../services/api-gateway/internal/db/postgres.go)

### deps

检查数据库、缓存、存储和 AI 服务依赖状态。

源码目录：[services/api-gateway/internal/deps](../../services/api-gateway/internal/deps)。

职责依据：[deps.go](../../services/api-gateway/internal/deps/deps.go)（抽读 1–24、51–63 行）。完整 SHA 见职责证据 JSON。

- `deps → config`：[deps.go:15](../../services/api-gateway/internal/deps/deps.go)
- `deps → minioadapter`：[deps.go:16](../../services/api-gateway/internal/deps/deps.go)

### evidence

核对 AI 分数和评分证据，给出人工复核标记。

源码目录：[services/api-gateway/internal/evidence](../../services/api-gateway/internal/evidence)。

职责依据：[engine.go](../../services/api-gateway/internal/evidence/engine.go)（抽读 1–24、20–50 行）。完整 SHA 见职责证据 JSON。

- `evidence → auth`：[handlers.go:7](../../services/api-gateway/internal/evidence/handlers.go)
- `evidence → grading`：[types.go:8](../../services/api-gateway/internal/evidence/types.go)
- `evidence → httpx`：[handlers.go:8](../../services/api-gateway/internal/evidence/handlers.go)
- `evidence → logger`：[handlers.go:9](../../services/api-gateway/internal/evidence/handlers.go)
- `evidence → paper`：[engine.go:7](../../services/api-gateway/internal/evidence/engine.go), [store_postgres.go:9](../../services/api-gateway/internal/evidence/store_postgres.go), [types.go:9](../../services/api-gateway/internal/evidence/types.go)

### exam

管理考试场次、配置和状态。

源码目录：[services/api-gateway/internal/exam](../../services/api-gateway/internal/exam)。

职责依据：[handlers.go](../../services/api-gateway/internal/exam/handlers.go)（抽读 1–24、24–46、47–91、165–214 行）。完整 SHA 见职责证据 JSON。

- `exam → auth`：[candidate_snapshot_postgres.go:8](../../services/api-gateway/internal/exam/candidate_snapshot_postgres.go), [handlers.go:9](../../services/api-gateway/internal/exam/handlers.go), [management_archive_bulk.go:8](../../services/api-gateway/internal/exam/management_archive_bulk.go), [management_archive_memory.go:8](../../services/api-gateway/internal/exam/management_archive_memory.go), [management_archive_postgres.go:14](../../services/api-gateway/internal/exam/management_archive_postgres.go), [management_archive_types.go:8](../../services/api-gateway/internal/exam/management_archive_types.go), [management_handlers.go:9](../../services/api-gateway/internal/exam/management_handlers.go), [management_memory.go:9](../../services/api-gateway/internal/exam/management_memory.go), [management_postgres.go:11](../../services/api-gateway/internal/exam/management_postgres.go), [management_progress.go:8](../../services/api-gateway/internal/exam/management_progress.go), [management_types.go:7](../../services/api-gateway/internal/exam/management_types.go), [session_memory.go:8](../../services/api-gateway/internal/exam/session_memory.go), [session_postgres.go:12](../../services/api-gateway/internal/exam/session_postgres.go), [store_memory.go:10](../../services/api-gateway/internal/exam/store_memory.go), [store_postgres.go:11](../../services/api-gateway/internal/exam/store_postgres.go), [template_memory.go:6](../../services/api-gateway/internal/exam/template_memory.go), [template_postgres.go:8](../../services/api-gateway/internal/exam/template_postgres.go), [templates.go:9](../../services/api-gateway/internal/exam/templates.go), [types.go:8](../../services/api-gateway/internal/exam/types.go)
- `exam → httpx`：[handlers.go:10](../../services/api-gateway/internal/exam/handlers.go), [management_archive_handlers.go:8](../../services/api-gateway/internal/exam/management_archive_handlers.go), [management_handlers.go:10](../../services/api-gateway/internal/exam/management_handlers.go), [templates.go:10](../../services/api-gateway/internal/exam/templates.go)
- `exam → logger`：[handlers.go:11](../../services/api-gateway/internal/exam/handlers.go)
- `exam → pagination`：[handlers.go:12](../../services/api-gateway/internal/exam/handlers.go), [management_handlers.go:11](../../services/api-gateway/internal/exam/management_handlers.go)

### files

管理文件资源、对象上传下载和存储对账。

源码目录：[services/api-gateway/internal/files](../../services/api-gateway/internal/files)。

职责依据：[handlers.go](../../services/api-gateway/internal/files/handlers.go)（抽读 1–24、52–101 行）。完整 SHA 见职责证据 JSON。

- `files → auth`：[downloads.go:10](../../services/api-gateway/internal/files/downloads.go), [handlers.go:13](../../services/api-gateway/internal/files/handlers.go), [store_memory.go:9](../../services/api-gateway/internal/files/store_memory.go), [store_postgres.go:11](../../services/api-gateway/internal/files/store_postgres.go), [types.go:9](../../services/api-gateway/internal/files/types.go)
- `files → binaryresource`：[downloads.go:11](../../services/api-gateway/internal/files/downloads.go)
- `files → binaryresourcehttp`：[handlers.go:14](../../services/api-gateway/internal/files/handlers.go)
- `files → config`：[handlers.go:15](../../services/api-gateway/internal/files/handlers.go), [storage_minio.go:9](../../services/api-gateway/internal/files/storage_minio.go)
- `files → httpx`：[downloads.go:12](../../services/api-gateway/internal/files/downloads.go), [handlers.go:16](../../services/api-gateway/internal/files/handlers.go)
- `files → logger`：[handlers.go:17](../../services/api-gateway/internal/files/handlers.go)
- `files → minioadapter`：[storage_minio.go:10](../../services/api-gateway/internal/files/storage_minio.go)

### goldpaper

管理标准卷的提名、版本、批准与停用。

源码目录：[services/api-gateway/internal/goldpaper](../../services/api-gateway/internal/goldpaper)。

职责依据：[handlers.go](../../services/api-gateway/internal/goldpaper/handlers.go)（抽读 1–24、40–58 行）。完整 SHA 见职责证据 JSON。

- `goldpaper → auth`：[handlers.go:11](../../services/api-gateway/internal/goldpaper/handlers.go)
- `goldpaper → httpx`：[handlers.go:12](../../services/api-gateway/internal/goldpaper/handlers.go)
- `goldpaper → logger`：[handlers.go:13](../../services/api-gateway/internal/goldpaper/handlers.go)

### graderdrift

计算阅卷者质量滑窗，记录偏差事件并可暂停资格。

源码目录：[services/api-gateway/internal/graderdrift](../../services/api-gateway/internal/graderdrift)。

职责依据：[service.go](../../services/api-gateway/internal/graderdrift/service.go)（抽读 1–24、47–79 行）。完整 SHA 见职责证据 JSON。

- `graderdrift → auth`：[handlers.go:11](../../services/api-gateway/internal/graderdrift/handlers.go)
- `graderdrift → httpx`：[handlers.go:12](../../services/api-gateway/internal/graderdrift/handlers.go)
- `graderdrift → logger`：[handlers.go:13](../../services/api-gateway/internal/graderdrift/handlers.go)
- `graderdrift → seedquality`：[service.go:10](../../services/api-gateway/internal/graderdrift/service.go)

### grading

按题型规则计算客观题分数。

源码目录：[services/api-gateway/internal/grading](../../services/api-gateway/internal/grading)。

职责依据：[engine.go](../../services/api-gateway/internal/grading/engine.go)（抽读 1–24、23–41、72–200 行）。完整 SHA 见职责证据 JSON。

- `grading → assessment`：[scoring_run_routing.go:6](../../services/api-gateway/internal/grading/scoring_run_routing.go), [types.go:8](../../services/api-gateway/internal/grading/types.go)
- `grading → auth`：[handlers.go:8](../../services/api-gateway/internal/grading/handlers.go)
- `grading → files`：[handlers.go:9](../../services/api-gateway/internal/grading/handlers.go)
- `grading → httpx`：[handlers.go:10](../../services/api-gateway/internal/grading/handlers.go)
- `grading → logger`：[handlers.go:11](../../services/api-gateway/internal/grading/handlers.go)
- `grading → paper`：[omr_calibration.go:14](../../services/api-gateway/internal/grading/omr_calibration.go), [scoring_run_omr.go:10](../../services/api-gateway/internal/grading/scoring_run_omr.go), [scoring_run_routing.go:7](../../services/api-gateway/internal/grading/scoring_run_routing.go), [scoring_run_start.go:11](../../services/api-gateway/internal/grading/scoring_run_start.go), [store_memory.go:11](../../services/api-gateway/internal/grading/store_memory.go), [store_postgres.go:9](../../services/api-gateway/internal/grading/store_postgres.go), [types.go:9](../../services/api-gateway/internal/grading/types.go)
- `grading → workerruntime`：[handlers.go:12](../../services/api-gateway/internal/grading/handlers.go), [scoring_run_omr.go:11](../../services/api-gateway/internal/grading/scoring_run_omr.go)

### gradingdisagreement

比较两份评分的分差、评分点和证据冲突。

源码目录：[services/api-gateway/internal/gradingdisagreement](../../services/api-gateway/internal/gradingdisagreement)。

职责依据：[engine.go](../../services/api-gateway/internal/gradingdisagreement/engine.go)（抽读 1–24、59–96 行）。完整 SHA 见职责证据 JSON。

没有扫描到直接内部import；这不代表没有第三方依赖或通过接口注入的调用。

### gradingevaluation

管理离线评分评测，汇总误差和模型组指标。

源码目录：[services/api-gateway/internal/gradingevaluation](../../services/api-gateway/internal/gradingevaluation)。

职责依据：[service.go](../../services/api-gateway/internal/gradingevaluation/service.go)（抽读 1–24、52–74 行）。完整 SHA 见职责证据 JSON。

- `gradingevaluation → assessment`：[panel_metrics.go:11](../../services/api-gateway/internal/gradingevaluation/panel_metrics.go), [service.go:13](../../services/api-gateway/internal/gradingevaluation/service.go)
- `gradingevaluation → auth`：[handlers.go:10](../../services/api-gateway/internal/gradingevaluation/handlers.go)
- `gradingevaluation → httpx`：[handlers.go:11](../../services/api-gateway/internal/gradingevaluation/handlers.go)

### handlers

提供健康、就绪及系统能力状态接口。

源码目录：[services/api-gateway/internal/handlers](../../services/api-gateway/internal/handlers)。

职责依据：[handlers.go](../../services/api-gateway/internal/handlers/handlers.go)（抽读 1–24、67–91 行）。完整 SHA 见职责证据 JSON。

- `handlers → auth`：[handlers.go:10](../../services/api-gateway/internal/handlers/handlers.go)
- `handlers → buildinfo`：[handlers.go:11](../../services/api-gateway/internal/handlers/handlers.go)
- `handlers → config`：[handlers.go:12](../../services/api-gateway/internal/handlers/handlers.go)
- `handlers → deps`：[handlers.go:13](../../services/api-gateway/internal/handlers/handlers.go)
- `handlers → httpx`：[handlers.go:14](../../services/api-gateway/internal/handlers/handlers.go)
- `handlers → workerruntime`：[handlers.go:15](../../services/api-gateway/internal/handlers/handlers.go)

### httpx

统一 JSON 成功响应和带追踪标识的错误响应。

源码目录：[services/api-gateway/internal/httpx](../../services/api-gateway/internal/httpx)。

职责依据：[response.go](../../services/api-gateway/internal/httpx/response.go)（抽读 1–24、34–44 行）。完整 SHA 见职责证据 JSON。

- `httpx → logger`：[response.go:7](../../services/api-gateway/internal/httpx/response.go)

### idempotency

按请求身份和指纹缓存响应，拒绝同键不同请求。

源码目录：[services/api-gateway/internal/idempotency](../../services/api-gateway/internal/idempotency)。

职责依据：[middleware.go](../../services/api-gateway/internal/idempotency/middleware.go)（抽读 1–24、80–96 行）。完整 SHA 见职责证据 JSON。

- `idempotency → auth`：[middleware.go:16](../../services/api-gateway/internal/idempotency/middleware.go)
- `idempotency → commandreceipt`：[command_identity.go:7](../../services/api-gateway/internal/idempotency/command_identity.go), [middleware.go:17](../../services/api-gateway/internal/idempotency/middleware.go)
- `idempotency → httpx`：[command_identity.go:8](../../services/api-gateway/internal/idempotency/command_identity.go), [middleware.go:18](../../services/api-gateway/internal/idempotency/middleware.go)

### imagequality

创建页面质检任务，接收结果并支持人工覆盖。

源码目录：[services/api-gateway/internal/imagequality](../../services/api-gateway/internal/imagequality)。

职责依据：[handlers.go](../../services/api-gateway/internal/imagequality/handlers.go)（抽读 1–24、70–77、80–129 行）。完整 SHA 见职责证据 JSON。

- `imagequality → auth`：[handlers.go:12](../../services/api-gateway/internal/imagequality/handlers.go)
- `imagequality → capture`：[coordinator_postgres.go:8](../../services/api-gateway/internal/imagequality/coordinator_postgres.go), [handlers.go:13](../../services/api-gateway/internal/imagequality/handlers.go)
- `imagequality → files`：[handlers.go:14](../../services/api-gateway/internal/imagequality/handlers.go)
- `imagequality → httpx`：[handlers.go:15](../../services/api-gateway/internal/imagequality/handlers.go)
- `imagequality → logger`：[handlers.go:16](../../services/api-gateway/internal/imagequality/handlers.go)
- `imagequality → submission`：[coordinator_postgres.go:9](../../services/api-gateway/internal/imagequality/coordinator_postgres.go), [handlers.go:17](../../services/api-gateway/internal/imagequality/handlers.go)
- `imagequality → workerruntime`：[coordinator_postgres.go:10](../../services/api-gateway/internal/imagequality/coordinator_postgres.go), [handlers.go:18](../../services/api-gateway/internal/imagequality/handlers.go)

### logger

输出带请求标识的结构化日志，并处理敏感字段。

源码目录：[services/api-gateway/internal/logger](../../services/api-gateway/internal/logger)。

职责依据：[logger.go](../../services/api-gateway/internal/logger/logger.go)（抽读 1–24、36–40、58–84 行）。完整 SHA 见职责证据 JSON。

没有扫描到直接内部import；这不代表没有第三方依赖或通过接口注入的调用。

### mathunderstanding

关联数学答案结构与冻结评分点，生成待教师确认的评分建议。

源码目录：[services/api-gateway/internal/mathunderstanding](../../services/api-gateway/internal/mathunderstanding)。

职责依据：[scorer.go](../../services/api-gateway/internal/mathunderstanding/scorer.go)（抽读 1–24、100–149 行）。完整 SHA 见职责证据 JSON。

- `mathunderstanding → auth`：[handlers.go:14](../../services/api-gateway/internal/mathunderstanding/handlers.go)
- `mathunderstanding → grading`：[scorer.go:11](../../services/api-gateway/internal/mathunderstanding/scorer.go)
- `mathunderstanding → httpx`：[handlers.go:15](../../services/api-gateway/internal/mathunderstanding/handlers.go), [scoring_handler.go:12](../../services/api-gateway/internal/mathunderstanding/scoring_handler.go)
- `mathunderstanding → logger`：[handlers.go:16](../../services/api-gateway/internal/mathunderstanding/handlers.go)
- `mathunderstanding → paper`：[rubric.go:4](../../services/api-gateway/internal/mathunderstanding/rubric.go), [scorer.go:12](../../services/api-gateway/internal/mathunderstanding/scorer.go)
- `mathunderstanding → workerruntime`：[handlers.go:17](../../services/api-gateway/internal/mathunderstanding/handlers.go), [verification_runtime.go:9](../../services/api-gateway/internal/mathunderstanding/verification_runtime.go)

### middleware

统一处理安全头、CORS、CSRF、请求标识和异常恢复。

源码目录：[services/api-gateway/internal/middleware](../../services/api-gateway/internal/middleware)。

职责依据：[middleware.go](../../services/api-gateway/internal/middleware/middleware.go)（抽读 1–24、40–53 行）。完整 SHA 见职责证据 JSON。

- `middleware → httpx`：[middleware.go:14](../../services/api-gateway/internal/middleware/middleware.go)
- `middleware → logger`：[middleware.go:15](../../services/api-gateway/internal/middleware/middleware.go)

### minioadapter

创建共享 TLS 信任配置的 MinIO 客户端。

源码目录：[services/api-gateway/internal/minioadapter](../../services/api-gateway/internal/minioadapter)。

职责依据：[client.go](../../services/api-gateway/internal/minioadapter/client.go)（抽读 1–24、16–53 行）。完整 SHA 见职责证据 JSON。

- `minioadapter → config`：[client.go:10](../../services/api-gateway/internal/minioadapter/client.go)

### modelcalibration

以完成的评分评测建立模型置信度校准，支持审批与失效。

源码目录：[services/api-gateway/internal/modelcalibration](../../services/api-gateway/internal/modelcalibration)。

职责依据：[service.go](../../services/api-gateway/internal/modelcalibration/service.go)（抽读 1–24、26–41 行）。完整 SHA 见职责证据 JSON。

- `modelcalibration → auth`：[handlers.go:10](../../services/api-gateway/internal/modelcalibration/handlers.go)
- `modelcalibration → gradingevaluation`：[gradingevaluation_adapter.go:6](../../services/api-gateway/internal/modelcalibration/gradingevaluation_adapter.go)
- `modelcalibration → httpx`：[handlers.go:11](../../services/api-gateway/internal/modelcalibration/handlers.go)

### modelgovernance

管理供应商、模型配置、租户使用策略及评测审批。

源码目录：[services/api-gateway/internal/modelgovernance](../../services/api-gateway/internal/modelgovernance)。

职责依据：[handlers.go](../../services/api-gateway/internal/modelgovernance/handlers.go)（抽读 1–24、49–61、289–326、75–121、483–517、626–660 行）；[managed_config_handlers.go](../../services/api-gateway/internal/modelgovernance/managed_config_handlers.go)（抽读 75–122 行）。完整 SHA 见职责证据 JSON。

- `modelgovernance → assessment`：[role_binding.go:9](../../services/api-gateway/internal/modelgovernance/role_binding.go)
- `modelgovernance → auth`：[handlers.go:12](../../services/api-gateway/internal/modelgovernance/handlers.go), [managed_config_handlers.go:9](../../services/api-gateway/internal/modelgovernance/managed_config_handlers.go)
- `modelgovernance → httpx`：[handlers.go:13](../../services/api-gateway/internal/modelgovernance/handlers.go), [managed_chat.go:16](../../services/api-gateway/internal/modelgovernance/managed_chat.go), [managed_config_handlers.go:10](../../services/api-gateway/internal/modelgovernance/managed_config_handlers.go), [role_binding_handlers.go:7](../../services/api-gateway/internal/modelgovernance/role_binding_handlers.go)
- `modelgovernance → logger`：[handlers.go:14](../../services/api-gateway/internal/modelgovernance/handlers.go), [managed_chat.go:17](../../services/api-gateway/internal/modelgovernance/managed_chat.go), [managed_config_handlers.go:11](../../services/api-gateway/internal/modelgovernance/managed_config_handlers.go), [usage_ledger.go:9](../../services/api-gateway/internal/modelgovernance/usage_ledger.go)
- `modelgovernance → pagination`：[handlers.go:15](../../services/api-gateway/internal/modelgovernance/handlers.go)

### observability

采集请求、数据库与模型调用指标。

源码目录：[services/api-gateway/internal/observability](../../services/api-gateway/internal/observability)。

职责依据：[metrics.go](../../services/api-gateway/internal/observability/metrics.go)（抽读 1–24、81–95 行）。完整 SHA 见职责证据 JSON。

没有扫描到直接内部import；这不代表没有第三方依赖或通过接口注入的调用。

### ocr

管理答卷 OCR 任务与识别结果。

源码目录：[services/api-gateway/internal/ocr](../../services/api-gateway/internal/ocr)。

职责依据：[handlers.go](../../services/api-gateway/internal/ocr/handlers.go)（抽读 1–24、37–86 行）。完整 SHA 见职责证据 JSON。

- `ocr → auth`：[handlers.go:10](../../services/api-gateway/internal/ocr/handlers.go)
- `ocr → httpx`：[handlers.go:11](../../services/api-gateway/internal/ocr/handlers.go)
- `ocr → logger`：[handlers.go:12](../../services/api-gateway/internal/ocr/handlers.go)
- `ocr → pagination`：[handlers.go:13](../../services/api-gateway/internal/ocr/handlers.go)
- `ocr → submission`：[handlers.go:14](../../services/api-gateway/internal/ocr/handlers.go)
- `ocr → workerruntime`：[handlers.go:15](../../services/api-gateway/internal/ocr/handlers.go), [runtime_contract.go:3](../../services/api-gateway/internal/ocr/runtime_contract.go), [runtime_coordinator.go:7](../../services/api-gateway/internal/ocr/runtime_coordinator.go), [runtime_coordinator_postgres.go:9](../../services/api-gateway/internal/ocr/runtime_coordinator_postgres.go)

### onboarding

汇总平台或学校管理员的初始化检查与下一步操作。

源码目录：[services/api-gateway/internal/onboarding](../../services/api-gateway/internal/onboarding)。

职责依据：[service.go](../../services/api-gateway/internal/onboarding/service.go)（抽读 1–24、56–65 行）。完整 SHA 见职责证据 JSON。

- `onboarding → auth`：[handler.go:8](../../services/api-gateway/internal/onboarding/handler.go)
- `onboarding → httpx`：[handler.go:9](../../services/api-gateway/internal/onboarding/handler.go)

### orchestrator

保存智能体运行与任务，处理任务状态和依赖。

源码目录：[services/api-gateway/internal/orchestrator](../../services/api-gateway/internal/orchestrator)。

职责依据：[handlers.go](../../services/api-gateway/internal/orchestrator/handlers.go)（抽读 1–24、24–38 行）。完整 SHA 见职责证据 JSON。

- `orchestrator → auth`：[handlers.go:9](../../services/api-gateway/internal/orchestrator/handlers.go)
- `orchestrator → httpx`：[handlers.go:10](../../services/api-gateway/internal/orchestrator/handlers.go)
- `orchestrator → logger`：[handlers.go:11](../../services/api-gateway/internal/orchestrator/handlers.go)

### org

管理租户、学校、年级、班级、学生及教师归属。

源码目录：[services/api-gateway/internal/org](../../services/api-gateway/internal/org)。

职责依据：[handlers.go](../../services/api-gateway/internal/org/handlers.go)（抽读 1–24、32–77 行）。完整 SHA 见职责证据 JSON。

- `org → auth`：[handlers.go:10](../../services/api-gateway/internal/org/handlers.go)
- `org → httpx`：[handlers.go:11](../../services/api-gateway/internal/org/handlers.go)
- `org → logger`：[handlers.go:12](../../services/api-gateway/internal/org/handlers.go)
- `org → pagination`：[handlers.go:13](../../services/api-gateway/internal/org/handlers.go)

### outbox

领取事务外发事件，投递并记录成功或重试。

源码目录：[services/api-gateway/internal/outbox](../../services/api-gateway/internal/outbox)。

职责依据：[outbox.go](../../services/api-gateway/internal/outbox/outbox.go)（抽读 1–24、74–95 行）。完整 SHA 见职责证据 JSON。

- `outbox → logger`：[publisher_log.go:6](../../services/api-gateway/internal/outbox/publisher_log.go)

### pagination

编解码分页游标并校验页大小。

源码目录：[services/api-gateway/internal/pagination](../../services/api-gateway/internal/pagination)。

职责依据：[cursor.go](../../services/api-gateway/internal/pagination/cursor.go)（抽读 1–24、25–44 行）。完整 SHA 见职责证据 JSON。

没有扫描到直接内部import；这不代表没有第三方依赖或通过接口注入的调用。

### paper

管理试卷、题目和评分细则，组织考试资料解析。

源码目录：[services/api-gateway/internal/paper](../../services/api-gateway/internal/paper)。

职责依据：[handlers.go](../../services/api-gateway/internal/paper/handlers.go)（抽读 1–24、48–71、414–432 行）。完整 SHA 见职责证据 JSON。

- `paper → assessment`：[paper_import_policy.go:8](../../services/api-gateway/internal/paper/paper_import_policy.go), [readiness_assessment_facts.go:9](../../services/api-gateway/internal/paper/readiness_assessment_facts.go), [readiness_scoring_policy.go:8](../../services/api-gateway/internal/paper/readiness_scoring_policy.go), [recognition_policy.go:6](../../services/api-gateway/internal/paper/recognition_policy.go)
- `paper → auth`：[handlers.go:12](../../services/api-gateway/internal/paper/handlers.go)
- `paper → exam`：[store_postgres_configuration.go:12](../../services/api-gateway/internal/paper/store_postgres_configuration.go)
- `paper → files`：[document_import.go:12](../../services/api-gateway/internal/paper/document_import.go), [document_source_loader.go:14](../../services/api-gateway/internal/paper/document_source_loader.go), [document_visual_loader.go:13](../../services/api-gateway/internal/paper/document_visual_loader.go)
- `paper → httpx`：[handlers.go:13](../../services/api-gateway/internal/paper/handlers.go), [paper_import_structure_handlers.go:6](../../services/api-gateway/internal/paper/paper_import_structure_handlers.go)
- `paper → logger`：[handlers.go:14](../../services/api-gateway/internal/paper/handlers.go)
- `paper → workerruntime`：[paper_import_parse_commit_postgres.go:9](../../services/api-gateway/internal/paper/paper_import_parse_commit_postgres.go), [paper_import_runtime_postgres.go:15](../../services/api-gateway/internal/paper/paper_import_runtime_postgres.go), [parse_task_executor.go:10](../../services/api-gateway/internal/paper/parse_task_executor.go)

### platformschools

为平台汇总学校成员、用量、模型健康和活动。

源码目录：[services/api-gateway/internal/platformschools](../../services/api-gateway/internal/platformschools)。

职责依据：[service.go](../../services/api-gateway/internal/platformschools/service.go)（抽读 1–24、24–65 行）。完整 SHA 见职责证据 JSON。

- `platformschools → httpx`：[handlers.go:10](../../services/api-gateway/internal/platformschools/handlers.go)

### processing

汇总流水线进度与异常，支持分派、处置和重试。

源码目录：[services/api-gateway/internal/processing](../../services/api-gateway/internal/processing)。

职责依据：[service.go](../../services/api-gateway/internal/processing/service.go)（抽读 1–24、21–27、28–85 行）；[store_postgres.go](../../services/api-gateway/internal/processing/store_postgres.go)（抽读 106–176 行）。完整 SHA 见职责证据 JSON。

- `processing → assessment`：[service.go:7](../../services/api-gateway/internal/processing/service.go), [store_memory.go:10](../../services/api-gateway/internal/processing/store_memory.go), [store_postgres.go:11](../../services/api-gateway/internal/processing/store_postgres.go), [types.go:10](../../services/api-gateway/internal/processing/types.go)
- `processing → auth`：[handlers.go:12](../../services/api-gateway/internal/processing/handlers.go)
- `processing → binaryresourcehttp`：[context.go:6](../../services/api-gateway/internal/processing/context.go)
- `processing → files`：[context.go:7](../../services/api-gateway/internal/processing/context.go), [handlers.go:13](../../services/api-gateway/internal/processing/handlers.go)
- `processing → httpx`：[actors.go:5](../../services/api-gateway/internal/processing/actors.go), [context.go:8](../../services/api-gateway/internal/processing/context.go), [handlers.go:14](../../services/api-gateway/internal/processing/handlers.go)
- `processing → logger`：[handlers.go:15](../../services/api-gateway/internal/processing/handlers.go)
- `processing → workerruntime`：[handlers.go:16](../../services/api-gateway/internal/processing/handlers.go), [retry_postgres.go:8](../../services/api-gateway/internal/processing/retry_postgres.go), [service.go:8](../../services/api-gateway/internal/processing/service.go)
- `processing → workflowactors`：[actors.go:6](../../services/api-gateway/internal/processing/actors.go)

### qualitydashboard

按题汇总标准卷、试评、盲投和偏差证据，形成质量门禁。

源码目录：[services/api-gateway/internal/qualitydashboard](../../services/api-gateway/internal/qualitydashboard)。

职责依据：[service.go](../../services/api-gateway/internal/qualitydashboard/service.go)（抽读 1–24、28–62 行）。完整 SHA 见职责证据 JSON。

- `qualitydashboard → answergroup`：[service.go:11](../../services/api-gateway/internal/qualitydashboard/service.go), [types.go:10](../../services/api-gateway/internal/qualitydashboard/types.go)
- `qualitydashboard → auth`：[handlers.go:7](../../services/api-gateway/internal/qualitydashboard/handlers.go)
- `qualitydashboard → backmark`：[adapters.go:6](../../services/api-gateway/internal/qualitydashboard/adapters.go)
- `qualitydashboard → goldpaper`：[service.go:12](../../services/api-gateway/internal/qualitydashboard/service.go), [types.go:11](../../services/api-gateway/internal/qualitydashboard/types.go)
- `qualitydashboard → graderdrift`：[adapters.go:7](../../services/api-gateway/internal/qualitydashboard/adapters.go)
- `qualitydashboard → httpx`：[handlers.go:8](../../services/api-gateway/internal/qualitydashboard/handlers.go)
- `qualitydashboard → review`：[service.go:13](../../services/api-gateway/internal/qualitydashboard/service.go), [types.go:12](../../services/api-gateway/internal/qualitydashboard/types.go)
- `qualitydashboard → seedquality`：[service.go:14](../../services/api-gateway/internal/qualitydashboard/service.go), [types.go:13](../../services/api-gateway/internal/qualitydashboard/types.go)

### questionbank

管理题库、题目版本、复核、模板及考试引用。

源码目录：[services/api-gateway/internal/questionbank](../../services/api-gateway/internal/questionbank)。

职责依据：[service.go](../../services/api-gateway/internal/questionbank/service.go)（抽读 1–24、20–20 行）；[handlers.go](../../services/api-gateway/internal/questionbank/handlers.go)（抽读 1–24、233–244、309–328、329–402、477–489 行）。完整 SHA 见职责证据 JSON。

- `questionbank → assessment`：[service.go:12](../../services/api-gateway/internal/questionbank/service.go)
- `questionbank → auth`：[handlers.go:11](../../services/api-gateway/internal/questionbank/handlers.go), [import_memory.go:6](../../services/api-gateway/internal/questionbank/import_memory.go), [import_postgres.go:13](../../services/api-gateway/internal/questionbank/import_postgres.go), [members.go:10](../../services/api-gateway/internal/questionbank/members.go), [metadata_acl_search_memory.go:11](../../services/api-gateway/internal/questionbank/metadata_acl_search_memory.go), [metadata_acl_search_postgres.go:11](../../services/api-gateway/internal/questionbank/metadata_acl_search_postgres.go), [publication_memory.go:5](../../services/api-gateway/internal/questionbank/publication_memory.go), [publication_postgres.go:9](../../services/api-gateway/internal/questionbank/publication_postgres.go), [service.go:13](../../services/api-gateway/internal/questionbank/service.go), [store_memory.go:12](../../services/api-gateway/internal/questionbank/store_memory.go), [store_postgres.go:9](../../services/api-gateway/internal/questionbank/store_postgres.go), [types.go:9](../../services/api-gateway/internal/questionbank/types.go)
- `questionbank → commandreceipt`：[handlers.go:12](../../services/api-gateway/internal/questionbank/handlers.go), [import_postgres.go:14](../../services/api-gateway/internal/questionbank/import_postgres.go), [store_memory.go:13](../../services/api-gateway/internal/questionbank/store_memory.go), [store_postgres.go:10](../../services/api-gateway/internal/questionbank/store_postgres.go)
- `questionbank → files`：[publication_postgres.go:10](../../services/api-gateway/internal/questionbank/publication_postgres.go)
- `questionbank → httpx`：[handlers.go:13](../../services/api-gateway/internal/questionbank/handlers.go), [members.go:11](../../services/api-gateway/internal/questionbank/members.go)
- `questionbank → paper`：[import.go:12](../../services/api-gateway/internal/questionbank/import.go), [publication_postgres.go:11](../../services/api-gateway/internal/questionbank/publication_postgres.go), [scoring.go:11](../../services/api-gateway/internal/questionbank/scoring.go), [service.go:14](../../services/api-gateway/internal/questionbank/service.go), [types.go:10](../../services/api-gateway/internal/questionbank/types.go)

### regrade

基于已发布版本创建独立重评，形成后继发布计划。

源码目录：[services/api-gateway/internal/regrade](../../services/api-gateway/internal/regrade)。

职责依据：[service.go](../../services/api-gateway/internal/regrade/service.go)（抽读 1–24、34–62 行）。完整 SHA 见职责证据 JSON。

- `regrade → assessment`：[grader_context_postgres.go:9](../../services/api-gateway/internal/regrade/grader_context_postgres.go)
- `regrade → auth`：[handlers.go:11](../../services/api-gateway/internal/regrade/handlers.go)
- `regrade → binaryresourcehttp`：[handlers.go:12](../../services/api-gateway/internal/regrade/handlers.go)
- `regrade → httpx`：[handlers.go:13](../../services/api-gateway/internal/regrade/handlers.go)
- `regrade → logger`：[handlers.go:14](../../services/api-gateway/internal/regrade/handlers.go)
- `regrade → paper`：[grader_context_postgres.go:10](../../services/api-gateway/internal/regrade/grader_context_postgres.go), [store_memory.go:11](../../services/api-gateway/internal/regrade/store_memory.go), [types.go:10](../../services/api-gateway/internal/regrade/types.go)
- `regrade → segment`：[handlers.go:15](../../services/api-gateway/internal/regrade/handlers.go)
- `regrade → taskmaterials`：[grader_context_postgres.go:11](../../services/api-gateway/internal/regrade/grader_context_postgres.go)

### regraderelease

将已复核重评计划转为新的成绩发布草稿。

源码目录：[services/api-gateway/internal/regraderelease](../../services/api-gateway/internal/regraderelease)。

职责依据：[service.go](../../services/api-gateway/internal/regraderelease/service.go)（抽读 1–24、33–57 行）。完整 SHA 见职责证据 JSON。

- `regraderelease → auth`：[handlers.go:9](../../services/api-gateway/internal/regraderelease/handlers.go)
- `regraderelease → httpx`：[handlers.go:10](../../services/api-gateway/internal/regraderelease/handlers.go)
- `regraderelease → regrade`：[handlers.go:11](../../services/api-gateway/internal/regraderelease/handlers.go), [service.go:10](../../services/api-gateway/internal/regraderelease/service.go)
- `regraderelease → scorerelease`：[handlers.go:12](../../services/api-gateway/internal/regraderelease/handlers.go), [service.go:11](../../services/api-gateway/internal/regraderelease/service.go)

### releasegate

评估成绩发布策略与质量证据，记录豁免审批。

源码目录：[services/api-gateway/internal/releasegate](../../services/api-gateway/internal/releasegate)。

职责依据：[service.go](../../services/api-gateway/internal/releasegate/service.go)（抽读 41–161 行）。完整 SHA 见职责证据 JSON。

- `releasegate → auth`：[handlers.go:9](../../services/api-gateway/internal/releasegate/handlers.go)
- `releasegate → httpx`：[handlers.go:10](../../services/api-gateway/internal/releasegate/handlers.go)
- `releasegate → logger`：[handlers.go:11](../../services/api-gateway/internal/releasegate/handlers.go)
- `releasegate → scorerelease`：[coordinator.go:8](../../services/api-gateway/internal/releasegate/coordinator.go), [service.go:13](../../services/api-gateway/internal/releasegate/service.go), [types.go:11](../../services/api-gateway/internal/releasegate/types.go)

### report

生成学生、班级、题目和阅卷质量统计及导出。

源码目录：[services/api-gateway/internal/report](../../services/api-gateway/internal/report)。

职责依据：[handlers.go](../../services/api-gateway/internal/report/handlers.go)（抽读 1–24、50–60 行）。完整 SHA 见职责证据 JSON。

- `report → auth`：[handlers.go:8](../../services/api-gateway/internal/report/handlers.go)
- `report → commandreceipt`：[command_recovery.go:5](../../services/api-gateway/internal/report/command_recovery.go), [handlers.go:4](../../services/api-gateway/internal/report/handlers.go), [store_memory.go:5](../../services/api-gateway/internal/report/store_memory.go), [store_postgres.go:6](../../services/api-gateway/internal/report/store_postgres.go), [types.go:5](../../services/api-gateway/internal/report/types.go)
- `report → csvsafe`：[stats.go:13](../../services/api-gateway/internal/report/stats.go)
- `report → httpx`：[command_recovery.go:6](../../services/api-gateway/internal/report/command_recovery.go), [handlers.go:9](../../services/api-gateway/internal/report/handlers.go)
- `report → logger`：[handlers.go:10](../../services/api-gateway/internal/report/handlers.go)

### review

分派人工阅卷任务，处理单评、双评和仲裁结算。

源码目录：[services/api-gateway/internal/review](../../services/api-gateway/internal/review)。

职责依据：[handlers.go](../../services/api-gateway/internal/review/handlers.go)（抽读 1–24、235–279、193–279、706–729、858–877 行）。完整 SHA 见职责证据 JSON。

- `review → aidisagreement`：[handlers.go:13](../../services/api-gateway/internal/review/handlers.go)
- `review → assessment`：[task_context.go:8](../../services/api-gateway/internal/review/task_context.go), [task_context_postgres.go:9](../../services/api-gateway/internal/review/task_context_postgres.go), [types.go:9](../../services/api-gateway/internal/review/types.go)
- `review → auth`：[actors.go:5](../../services/api-gateway/internal/review/actors.go), [handlers.go:14](../../services/api-gateway/internal/review/handlers.go)
- `review → binaryresourcehttp`：[handlers.go:15](../../services/api-gateway/internal/review/handlers.go)
- `review → commandreceipt`：[command_recovery.go:5](../../services/api-gateway/internal/review/command_recovery.go), [handlers.go:5](../../services/api-gateway/internal/review/handlers.go), [store_memory.go:5](../../services/api-gateway/internal/review/store_memory.go), [store_postgres_arbitration.go:6](../../services/api-gateway/internal/review/store_postgres_arbitration.go), [store_postgres_tasks.go:6](../../services/api-gateway/internal/review/store_postgres_tasks.go), [types.go:5](../../services/api-gateway/internal/review/types.go)
- `review → files`：[handlers.go:16](../../services/api-gateway/internal/review/handlers.go)
- `review → httpx`：[actors.go:6](../../services/api-gateway/internal/review/actors.go), [command_recovery.go:6](../../services/api-gateway/internal/review/command_recovery.go), [handlers.go:17](../../services/api-gateway/internal/review/handlers.go), [task_context_handler.go:7](../../services/api-gateway/internal/review/task_context_handler.go)
- `review → logger`：[handlers.go:18](../../services/api-gateway/internal/review/handlers.go)
- `review → pagination`：[handlers.go:19](../../services/api-gateway/internal/review/handlers.go)
- `review → paper`：[store_postgres_context.go:9](../../services/api-gateway/internal/review/store_postgres_context.go), [task_context.go:9](../../services/api-gateway/internal/review/task_context.go), [types.go:10](../../services/api-gateway/internal/review/types.go)
- `review → seedquality`：[handlers.go:20](../../services/api-gateway/internal/review/handlers.go)
- `review → segment`：[handlers.go:21](../../services/api-gateway/internal/review/handlers.go)
- `review → workflowactors`：[actors.go:7](../../services/api-gateway/internal/review/actors.go)

### reviewannotation

管理阅卷批注、评语模板及学生可见内容。

源码目录：[services/api-gateway/internal/reviewannotation](../../services/api-gateway/internal/reviewannotation)。

职责依据：[handlers.go](../../services/api-gateway/internal/reviewannotation/handlers.go)（抽读 1–24、23–37 行）。完整 SHA 见职责证据 JSON。

- `reviewannotation → auth`：[handlers.go:9](../../services/api-gateway/internal/reviewannotation/handlers.go)
- `reviewannotation → httpx`：[handlers.go:10](../../services/api-gateway/internal/reviewannotation/handlers.go)
- `reviewannotation → logger`：[handlers.go:11](../../services/api-gateway/internal/reviewannotation/handlers.go)

### schoolassistant

汇集授权的考试事实，流式返回学校业务查询结果。

源码目录：[services/api-gateway/internal/schoolassistant](../../services/api-gateway/internal/schoolassistant)。

职责依据：[service.go](../../services/api-gateway/internal/schoolassistant/service.go)（抽读 1–24、54–91 行）。完整 SHA 见职责证据 JSON。

- `schoolassistant → auth`：[auth.go:9](../../services/api-gateway/internal/schoolassistant/auth.go), [conversation.go:12](../../services/api-gateway/internal/schoolassistant/conversation.go), [service.go:9](../../services/api-gateway/internal/schoolassistant/service.go), [tools.go:11](../../services/api-gateway/internal/schoolassistant/tools.go), [tools_search.go:10](../../services/api-gateway/internal/schoolassistant/tools_search.go), [types.go:10](../../services/api-gateway/internal/schoolassistant/types.go)
- `schoolassistant → exam`：[conversation.go:13](../../services/api-gateway/internal/schoolassistant/conversation.go), [tools.go:12](../../services/api-gateway/internal/schoolassistant/tools.go), [tools_search.go:11](../../services/api-gateway/internal/schoolassistant/tools_search.go), [types.go:11](../../services/api-gateway/internal/schoolassistant/types.go)
- `schoolassistant → httpx`：[conversation_handlers.go:12](../../services/api-gateway/internal/schoolassistant/conversation_handlers.go), [handlers.go:12](../../services/api-gateway/internal/schoolassistant/handlers.go)
- `schoolassistant → modelgovernance`：[conversation.go:14](../../services/api-gateway/internal/schoolassistant/conversation.go), [conversation_capability.go:9](../../services/api-gateway/internal/schoolassistant/conversation_capability.go), [conversation_handlers.go:13](../../services/api-gateway/internal/schoolassistant/conversation_handlers.go), [conversation_types.go:8](../../services/api-gateway/internal/schoolassistant/conversation_types.go)
- `schoolassistant → pagination`：[tools_search.go:12](../../services/api-gateway/internal/schoolassistant/tools_search.go)
- `schoolassistant → releasegate`：[types.go:12](../../services/api-gateway/internal/schoolassistant/types.go)
- `schoolassistant → scorerelease`：[adapters.go:10](../../services/api-gateway/internal/schoolassistant/adapters.go), [service.go:10](../../services/api-gateway/internal/schoolassistant/service.go), [types.go:13](../../services/api-gateway/internal/schoolassistant/types.go)
- `schoolassistant → workspace`：[adapters.go:11](../../services/api-gateway/internal/schoolassistant/adapters.go), [types.go:14](../../services/api-gateway/internal/schoolassistant/types.go)

### score

汇总和确认成绩，保留旧发布与成绩查询接口。

源码目录：[services/api-gateway/internal/score](../../services/api-gateway/internal/score)。

职责依据：[handlers.go](../../services/api-gateway/internal/score/handlers.go)（抽读 1–24、26–36 行）。完整 SHA 见职责证据 JSON。

- `score → auth`：[handlers.go:10](../../services/api-gateway/internal/score/handlers.go)
- `score → commandreceipt`：[command_recovery.go:5](../../services/api-gateway/internal/score/command_recovery.go), [handlers.go:4](../../services/api-gateway/internal/score/handlers.go), [store_memory.go:6](../../services/api-gateway/internal/score/store_memory.go), [store_postgres.go:7](../../services/api-gateway/internal/score/store_postgres.go), [types.go:5](../../services/api-gateway/internal/score/types.go)
- `score → csvsafe`：[store_memory.go:14](../../services/api-gateway/internal/score/store_memory.go), [store_postgres.go:14](../../services/api-gateway/internal/score/store_postgres.go)
- `score → httpx`：[command_recovery.go:6](../../services/api-gateway/internal/score/command_recovery.go), [handlers.go:11](../../services/api-gateway/internal/score/handlers.go)
- `score → logger`：[handlers.go:12](../../services/api-gateway/internal/score/handlers.go)
- `score → pagination`：[handlers.go:13](../../services/api-gateway/internal/score/handlers.go)

### scorerelease

冻结成绩发布版本，管理发布、回滚和学生可见结果。

源码目录：[services/api-gateway/internal/scorerelease](../../services/api-gateway/internal/scorerelease)。

职责依据：[service.go](../../services/api-gateway/internal/scorerelease/service.go)（抽读 1–24、81–107、1–185 行）。完整 SHA 见职责证据 JSON。

- `scorerelease → auth`：[handlers.go:11](../../services/api-gateway/internal/scorerelease/handlers.go)
- `scorerelease → binaryresource`：[anonymous_paper.go:22](../../services/api-gateway/internal/scorerelease/anonymous_paper.go)
- `scorerelease → binaryresourcehttp`：[handlers.go:12](../../services/api-gateway/internal/scorerelease/handlers.go)
- `scorerelease → files`：[anonymous_paper.go:23](../../services/api-gateway/internal/scorerelease/anonymous_paper.go)
- `scorerelease → httpx`：[handlers.go:13](../../services/api-gateway/internal/scorerelease/handlers.go)
- `scorerelease → logger`：[handlers.go:14](../../services/api-gateway/internal/scorerelease/handlers.go)
- `scorerelease → paper`：[anonymous_paper.go:24](../../services/api-gateway/internal/scorerelease/anonymous_paper.go)
- `scorerelease → qualitydashboard`：[store_postgres.go:14](../../services/api-gateway/internal/scorerelease/store_postgres.go)
- `scorerelease → segment`：[handlers.go:15](../../services/api-gateway/internal/scorerelease/handlers.go)

### seedquality

向阅卷队列盲投标准卷，记录质量观察。

源码目录：[services/api-gateway/internal/seedquality](../../services/api-gateway/internal/seedquality)。

职责依据：[service.go](../../services/api-gateway/internal/seedquality/service.go)（抽读 1–24、82–122 行）。完整 SHA 见职责证据 JSON。

- `seedquality → assessment`：[context.go:15](../../services/api-gateway/internal/seedquality/context.go)
- `seedquality → auth`：[handlers.go:10](../../services/api-gateway/internal/seedquality/handlers.go)
- `seedquality → calibration`：[service.go:17](../../services/api-gateway/internal/seedquality/service.go)
- `seedquality → commandreceipt`：[command_recovery.go:5](../../services/api-gateway/internal/seedquality/command_recovery.go), [context.go:6](../../services/api-gateway/internal/seedquality/context.go), [store_memory.go:5](../../services/api-gateway/internal/seedquality/store_memory.go), [store_postgres.go:6](../../services/api-gateway/internal/seedquality/store_postgres.go), [types.go:5](../../services/api-gateway/internal/seedquality/types.go)
- `seedquality → goldpaper`：[service.go:18](../../services/api-gateway/internal/seedquality/service.go)
- `seedquality → httpx`：[handlers.go:11](../../services/api-gateway/internal/seedquality/handlers.go)
- `seedquality → logger`：[handlers.go:12](../../services/api-gateway/internal/seedquality/handlers.go)
- `seedquality → paper`：[context.go:16](../../services/api-gateway/internal/seedquality/context.go)

### segment

管理答案片段、坐标和受控图像读取。

源码目录：[services/api-gateway/internal/segment](../../services/api-gateway/internal/segment)。

职责依据：[handlers.go](../../services/api-gateway/internal/segment/handlers.go)（抽读 1–24、32–81 行）。完整 SHA 见职责证据 JSON。

- `segment → auth`：[handlers.go:10](../../services/api-gateway/internal/segment/handlers.go)
- `segment → binaryresource`：[images.go:10](../../services/api-gateway/internal/segment/images.go)
- `segment → binaryresourcehttp`：[handlers.go:11](../../services/api-gateway/internal/segment/handlers.go)
- `segment → files`：[handlers.go:12](../../services/api-gateway/internal/segment/handlers.go), [images.go:11](../../services/api-gateway/internal/segment/images.go)
- `segment → httpx`：[handlers.go:13](../../services/api-gateway/internal/segment/handlers.go)
- `segment → logger`：[handlers.go:14](../../services/api-gateway/internal/segment/handlers.go)
- `segment → paper`：[handlers.go:15](../../services/api-gateway/internal/segment/handlers.go)
- `segment → submission`：[handlers.go:16](../../services/api-gateway/internal/segment/handlers.go), [images.go:12](../../services/api-gateway/internal/segment/images.go)

### server

组装业务模块与存储，注册路由和入口守卫。

源码目录：[services/api-gateway/internal/server](../../services/api-gateway/internal/server)。

职责依据：[application_modules.go](../../services/api-gateway/internal/server/application_modules.go)（抽读 1–24、52–101 行）；[router_guards.go](../../services/api-gateway/internal/server/router_guards.go)（抽读 1–135 行）。完整 SHA 见职责证据 JSON。

- `server → aidisagreement`：[application_stores.go:6](../../services/api-gateway/internal/server/application_stores.go), [module_ai_foundation.go:4](../../services/api-gateway/internal/server/module_ai_foundation.go), [module_ai_governance.go:4](../../services/api-gateway/internal/server/module_ai_governance.go), [routes_governance.go:5](../../services/api-gateway/internal/server/routes_governance.go)
- `server → aieligibility`：[application_stores.go:7](../../services/api-gateway/internal/server/application_stores.go), [module_ai_foundation.go:5](../../services/api-gateway/internal/server/module_ai_foundation.go), [module_ai_governance.go:5](../../services/api-gateway/internal/server/module_ai_governance.go), [module_grading.go:5](../../services/api-gateway/internal/server/module_grading.go), [routes_governance.go:6](../../services/api-gateway/internal/server/routes_governance.go)
- `server → answergroup`：[application_stores.go:8](../../services/api-gateway/internal/server/application_stores.go), [module_grading.go:6](../../services/api-gateway/internal/server/module_grading.go), [routes_review.go:4](../../services/api-gateway/internal/server/routes_review.go)
- `server → appeal`：[application_stores.go:9](../../services/api-gateway/internal/server/application_stores.go), [module_release.go:6](../../services/api-gateway/internal/server/module_release.go)
- `server → assessment`：[application_stores.go:10](../../services/api-gateway/internal/server/application_stores.go), [module_exam.go:5](../../services/api-gateway/internal/server/module_exam.go), [module_grading.go:7](../../services/api-gateway/internal/server/module_grading.go)
- `server → auth`：[application_modules.go:6](../../services/api-gateway/internal/server/application_modules.go), [application_stores.go:11](../../services/api-gateway/internal/server/application_stores.go), [infrastructure.go:9](../../services/api-gateway/internal/server/infrastructure.go), [module_ai_governance.go:6](../../services/api-gateway/internal/server/module_ai_governance.go), [module_capture.go:4](../../services/api-gateway/internal/server/module_capture.go), [module_exam.go:6](../../services/api-gateway/internal/server/module_exam.go), [module_grading.go:8](../../services/api-gateway/internal/server/module_grading.go), [module_identity.go:4](../../services/api-gateway/internal/server/module_identity.go), [module_release.go:7](../../services/api-gateway/internal/server/module_release.go), [onboarding_readers.go:7](../../services/api-gateway/internal/server/onboarding_readers.go), [router_context.go:7](../../services/api-gateway/internal/server/router_context.go), [router_guards.go:7](../../services/api-gateway/internal/server/router_guards.go), [routes_auth.go:4](../../services/api-gateway/internal/server/routes_auth.go), [routes_files.go:4](../../services/api-gateway/internal/server/routes_files.go), [routes_governance.go:7](../../services/api-gateway/internal/server/routes_governance.go), [routes_paper.go:4](../../services/api-gateway/internal/server/routes_paper.go), [routes_review.go:5](../../services/api-gateway/internal/server/routes_review.go), [server.go:8](../../services/api-gateway/internal/server/server.go)
- `server → backmark`：[application_stores.go:12](../../services/api-gateway/internal/server/application_stores.go), [module_grading.go:9](../../services/api-gateway/internal/server/module_grading.go), [routes_review.go:6](../../services/api-gateway/internal/server/routes_review.go)
- `server → calibration`：[application_stores.go:13](../../services/api-gateway/internal/server/application_stores.go), [module_grading.go:10](../../services/api-gateway/internal/server/module_grading.go), [routes_review.go:7](../../services/api-gateway/internal/server/routes_review.go)
- `server → capture`：[application_stores.go:14](../../services/api-gateway/internal/server/application_stores.go), [module_capture.go:5](../../services/api-gateway/internal/server/module_capture.go)
- `server → captureupload`：[application_stores.go:15](../../services/api-gateway/internal/server/application_stores.go), [module_capture.go:6](../../services/api-gateway/internal/server/module_capture.go), [routes_capture.go:4](../../services/api-gateway/internal/server/routes_capture.go)
- `server → config`：[application_modules.go:7](../../services/api-gateway/internal/server/application_modules.go), [infrastructure.go:10](../../services/api-gateway/internal/server/infrastructure.go), [module_ai_governance.go:7](../../services/api-gateway/internal/server/module_ai_governance.go), [module_capture.go:7](../../services/api-gateway/internal/server/module_capture.go), [module_exam.go:7](../../services/api-gateway/internal/server/module_exam.go), [module_grading.go:11](../../services/api-gateway/internal/server/module_grading.go), [module_identity.go:5](../../services/api-gateway/internal/server/module_identity.go), [router_context.go:8](../../services/api-gateway/internal/server/router_context.go), [router_guards.go:8](../../services/api-gateway/internal/server/router_guards.go), [server.go:9](../../services/api-gateway/internal/server/server.go)
- `server → dashboard`：[application_stores.go:16](../../services/api-gateway/internal/server/application_stores.go), [module_exam.go:8](../../services/api-gateway/internal/server/module_exam.go), [routes_auth.go:5](../../services/api-gateway/internal/server/routes_auth.go)
- `server → db`：[infrastructure.go:11](../../services/api-gateway/internal/server/infrastructure.go)
- `server → deps`：[infrastructure.go:12](../../services/api-gateway/internal/server/infrastructure.go), [server.go:10](../../services/api-gateway/internal/server/server.go)
- `server → evidence`：[application_stores.go:17](../../services/api-gateway/internal/server/application_stores.go), [module_grading.go:12](../../services/api-gateway/internal/server/module_grading.go)
- `server → exam`：[application_stores.go:18](../../services/api-gateway/internal/server/application_stores.go), [module_capture.go:8](../../services/api-gateway/internal/server/module_capture.go), [module_exam.go:9](../../services/api-gateway/internal/server/module_exam.go), [module_release.go:8](../../services/api-gateway/internal/server/module_release.go), [onboarding_readers.go:8](../../services/api-gateway/internal/server/onboarding_readers.go), [server.go:11](../../services/api-gateway/internal/server/server.go)
- `server → files`：[application_modules.go:8](../../services/api-gateway/internal/server/application_modules.go), [application_stores.go:19](../../services/api-gateway/internal/server/application_stores.go), [infrastructure.go:13](../../services/api-gateway/internal/server/infrastructure.go), [module_capture.go:9](../../services/api-gateway/internal/server/module_capture.go), [module_exam.go:10](../../services/api-gateway/internal/server/module_exam.go), [module_grading.go:13](../../services/api-gateway/internal/server/module_grading.go), [module_release.go:9](../../services/api-gateway/internal/server/module_release.go), [server.go:12](../../services/api-gateway/internal/server/server.go)
- `server → goldpaper`：[application_stores.go:20](../../services/api-gateway/internal/server/application_stores.go), [module_grading.go:14](../../services/api-gateway/internal/server/module_grading.go), [routes_review.go:8](../../services/api-gateway/internal/server/routes_review.go)
- `server → graderdrift`：[application_stores.go:21](../../services/api-gateway/internal/server/application_stores.go), [module_grading.go:15](../../services/api-gateway/internal/server/module_grading.go), [routes_review.go:9](../../services/api-gateway/internal/server/routes_review.go)
- `server → grading`：[application_stores.go:22](../../services/api-gateway/internal/server/application_stores.go), [module_grading.go:16](../../services/api-gateway/internal/server/module_grading.go)
- `server → gradingevaluation`：[application_stores.go:23](../../services/api-gateway/internal/server/application_stores.go), [module_ai_foundation.go:6](../../services/api-gateway/internal/server/module_ai_foundation.go), [module_ai_governance.go:8](../../services/api-gateway/internal/server/module_ai_governance.go), [routes_governance.go:8](../../services/api-gateway/internal/server/routes_governance.go)
- `server → handlers`：[onboarding_readers.go:9](../../services/api-gateway/internal/server/onboarding_readers.go), [router_context.go:9](../../services/api-gateway/internal/server/router_context.go)
- `server → httpx`：[routes_governance.go:15](../../services/api-gateway/internal/server/routes_governance.go)
- `server → idempotency`：[application_modules.go:9](../../services/api-gateway/internal/server/application_modules.go), [application_stores.go:24](../../services/api-gateway/internal/server/application_stores.go), [router_guards.go:9](../../services/api-gateway/internal/server/router_guards.go), [routes_paper.go:5](../../services/api-gateway/internal/server/routes_paper.go)
- `server → imagequality`：[application_stores.go:25](../../services/api-gateway/internal/server/application_stores.go), [module_capture.go:10](../../services/api-gateway/internal/server/module_capture.go), [worker_source_lease.go:7](../../services/api-gateway/internal/server/worker_source_lease.go)
- `server → logger`：[infrastructure.go:14](../../services/api-gateway/internal/server/infrastructure.go), [server.go:13](../../services/api-gateway/internal/server/server.go)
- `server → mathunderstanding`：[application_stores.go:26](../../services/api-gateway/internal/server/application_stores.go), [module_ai_governance.go:9](../../services/api-gateway/internal/server/module_ai_governance.go), [module_grading.go:17](../../services/api-gateway/internal/server/module_grading.go), [routes_exam.go:4](../../services/api-gateway/internal/server/routes_exam.go)
- `server → middleware`：[server.go:14](../../services/api-gateway/internal/server/server.go)
- `server → modelcalibration`：[application_stores.go:27](../../services/api-gateway/internal/server/application_stores.go), [module_ai_foundation.go:7](../../services/api-gateway/internal/server/module_ai_foundation.go), [module_ai_governance.go:10](../../services/api-gateway/internal/server/module_ai_governance.go), [routes_governance.go:9](../../services/api-gateway/internal/server/routes_governance.go)
- `server → modelgovernance`：[application_modules.go:10](../../services/api-gateway/internal/server/application_modules.go), [application_stores.go:28](../../services/api-gateway/internal/server/application_stores.go), [module_ai_governance.go:11](../../services/api-gateway/internal/server/module_ai_governance.go), [onboarding_readers.go:10](../../services/api-gateway/internal/server/onboarding_readers.go), [server.go:15](../../services/api-gateway/internal/server/server.go)
- `server → observability`：[application_modules.go:11](../../services/api-gateway/internal/server/application_modules.go), [infrastructure.go:15](../../services/api-gateway/internal/server/infrastructure.go), [router_context.go:10](../../services/api-gateway/internal/server/router_context.go), [server.go:16](../../services/api-gateway/internal/server/server.go)
- `server → ocr`：[application_stores.go:29](../../services/api-gateway/internal/server/application_stores.go), [module_capture.go:11](../../services/api-gateway/internal/server/module_capture.go)
- `server → onboarding`：[onboarding_readers.go:11](../../services/api-gateway/internal/server/onboarding_readers.go), [router_context.go:11](../../services/api-gateway/internal/server/router_context.go)
- `server → orchestrator`：[application_stores.go:30](../../services/api-gateway/internal/server/application_stores.go), [module_capture.go:12](../../services/api-gateway/internal/server/module_capture.go)
- `server → org`：[application_stores.go:31](../../services/api-gateway/internal/server/application_stores.go), [module_identity.go:6](../../services/api-gateway/internal/server/module_identity.go), [onboarding_readers.go:12](../../services/api-gateway/internal/server/onboarding_readers.go), [server.go:17](../../services/api-gateway/internal/server/server.go)
- `server → outbox`：[infrastructure.go:16](../../services/api-gateway/internal/server/infrastructure.go)
- `server → paper`：[application_modules.go:12](../../services/api-gateway/internal/server/application_modules.go), [application_stores.go:32](../../services/api-gateway/internal/server/application_stores.go), [infrastructure.go:17](../../services/api-gateway/internal/server/infrastructure.go), [module_capture.go:13](../../services/api-gateway/internal/server/module_capture.go), [module_exam.go:11](../../services/api-gateway/internal/server/module_exam.go), [server.go:18](../../services/api-gateway/internal/server/server.go)
- `server → platformschools`：[application_modules.go:13](../../services/api-gateway/internal/server/application_modules.go), [application_stores.go:33](../../services/api-gateway/internal/server/application_stores.go)
- `server → processing`：[application_modules.go:14](../../services/api-gateway/internal/server/application_modules.go), [application_stores.go:34](../../services/api-gateway/internal/server/application_stores.go), [infrastructure.go:18](../../services/api-gateway/internal/server/infrastructure.go), [module_capture.go:14](../../services/api-gateway/internal/server/module_capture.go), [module_grading.go:18](../../services/api-gateway/internal/server/module_grading.go), [routes_capture.go:5](../../services/api-gateway/internal/server/routes_capture.go)
- `server → qualitydashboard`：[application_stores.go:35](../../services/api-gateway/internal/server/application_stores.go), [module_grading.go:19](../../services/api-gateway/internal/server/module_grading.go), [routes_review.go:10](../../services/api-gateway/internal/server/routes_review.go)
- `server → questionbank`：[application_stores.go:36](../../services/api-gateway/internal/server/application_stores.go), [module_exam.go:12](../../services/api-gateway/internal/server/module_exam.go)
- `server → regrade`：[application_stores.go:37](../../services/api-gateway/internal/server/application_stores.go), [module_grading.go:20](../../services/api-gateway/internal/server/module_grading.go), [module_release.go:10](../../services/api-gateway/internal/server/module_release.go), [routes_review.go:11](../../services/api-gateway/internal/server/routes_review.go), [server.go:19](../../services/api-gateway/internal/server/server.go)
- `server → regraderelease`：[module_release.go:11](../../services/api-gateway/internal/server/module_release.go), [routes_review.go:12](../../services/api-gateway/internal/server/routes_review.go)
- `server → releasegate`：[application_stores.go:38](../../services/api-gateway/internal/server/application_stores.go), [module_release.go:12](../../services/api-gateway/internal/server/module_release.go), [routes_release.go:4](../../services/api-gateway/internal/server/routes_release.go), [server.go:20](../../services/api-gateway/internal/server/server.go)
- `server → report`：[application_stores.go:39](../../services/api-gateway/internal/server/application_stores.go), [module_release.go:13](../../services/api-gateway/internal/server/module_release.go)
- `server → review`：[application_stores.go:40](../../services/api-gateway/internal/server/application_stores.go), [module_ai_governance.go:12](../../services/api-gateway/internal/server/module_ai_governance.go), [module_exam.go:13](../../services/api-gateway/internal/server/module_exam.go), [module_grading.go:21](../../services/api-gateway/internal/server/module_grading.go)
- `server → reviewannotation`：[application_stores.go:41](../../services/api-gateway/internal/server/application_stores.go), [module_grading.go:22](../../services/api-gateway/internal/server/module_grading.go)
- `server → schoolassistant`：[application_modules.go:15](../../services/api-gateway/internal/server/application_modules.go), [module_school_assistant.go:3](../../services/api-gateway/internal/server/module_school_assistant.go), [routes_school_assistant.go:6](../../services/api-gateway/internal/server/routes_school_assistant.go)
- `server → score`：[application_stores.go:42](../../services/api-gateway/internal/server/application_stores.go), [module_release.go:14](../../services/api-gateway/internal/server/module_release.go)
- `server → scorerelease`：[application_stores.go:43](../../services/api-gateway/internal/server/application_stores.go), [module_release.go:15](../../services/api-gateway/internal/server/module_release.go), [routes_release.go:5](../../services/api-gateway/internal/server/routes_release.go), [server.go:21](../../services/api-gateway/internal/server/server.go)
- `server → seedquality`：[application_stores.go:44](../../services/api-gateway/internal/server/application_stores.go), [module_grading.go:23](../../services/api-gateway/internal/server/module_grading.go), [routes_review.go:13](../../services/api-gateway/internal/server/routes_review.go)
- `server → segment`：[application_stores.go:45](../../services/api-gateway/internal/server/application_stores.go), [module_exam.go:14](../../services/api-gateway/internal/server/module_exam.go), [module_grading.go:24](../../services/api-gateway/internal/server/module_grading.go), [module_release.go:16](../../services/api-gateway/internal/server/module_release.go)
- `server → studentportal`：[application_stores.go:46](../../services/api-gateway/internal/server/application_stores.go), [module_release.go:17](../../services/api-gateway/internal/server/module_release.go), [routes_release.go:6](../../services/api-gateway/internal/server/routes_release.go)
- `server → subjective`：[application_stores.go:47](../../services/api-gateway/internal/server/application_stores.go), [module_grading.go:25](../../services/api-gateway/internal/server/module_grading.go)
- `server → submission`：[application_stores.go:48](../../services/api-gateway/internal/server/application_stores.go), [module_capture.go:15](../../services/api-gateway/internal/server/module_capture.go), [module_exam.go:15](../../services/api-gateway/internal/server/module_exam.go), [server.go:22](../../services/api-gateway/internal/server/server.go)
- `server → taskmaterials`：[module_grading.go:26](../../services/api-gateway/internal/server/module_grading.go)
- `server → workerruntime`：[application_stores.go:49](../../services/api-gateway/internal/server/application_stores.go), [module_ai_governance.go:13](../../services/api-gateway/internal/server/module_ai_governance.go), [module_capture.go:16](../../services/api-gateway/internal/server/module_capture.go), [module_grading.go:27](../../services/api-gateway/internal/server/module_grading.go), [router_guards.go:10](../../services/api-gateway/internal/server/router_guards.go), [worker_source_lease.go:8](../../services/api-gateway/internal/server/worker_source_lease.go)
- `server → workspace`：[module_exam.go:16](../../services/api-gateway/internal/server/module_exam.go), [routes_exam.go:5](../../services/api-gateway/internal/server/routes_exam.go)

### studentportal

为学生列出本人有当前成绩发布版本的考试。

源码目录：[services/api-gateway/internal/studentportal](../../services/api-gateway/internal/studentportal)。

职责依据：[service.go](../../services/api-gateway/internal/studentportal/service.go)（抽读 1–24、13–43 行）。完整 SHA 见职责证据 JSON。

- `studentportal → auth`：[handlers.go:7](../../services/api-gateway/internal/studentportal/handlers.go)
- `studentportal → httpx`：[handlers.go:8](../../services/api-gateway/internal/studentportal/handlers.go)

### subjective

组织主观题 AI 评分、确定性结算和人工复核。

源码目录：[services/api-gateway/internal/subjective](../../services/api-gateway/internal/subjective)。

职责依据：[handlers_grade.go](../../services/api-gateway/internal/subjective/handlers_grade.go)（抽读 1–24、12–61、1–205 行）；[grade_settlement_pipeline.go](../../services/api-gateway/internal/subjective/grade_settlement_pipeline.go)（抽读 1–76 行）。完整 SHA 见职责证据 JSON。

- `subjective → aieligibility`：[eligibility.go:7](../../services/api-gateway/internal/subjective/eligibility.go), [grade_settlement_pipeline.go:7](../../services/api-gateway/internal/subjective/grade_settlement_pipeline.go), [grading_agent_v2_builder.go:14](../../services/api-gateway/internal/subjective/grading_agent_v2_builder.go), [math_grading.go:10](../../services/api-gateway/internal/subjective/math_grading.go), [panel_orchestrator.go:11](../../services/api-gateway/internal/subjective/panel_orchestrator.go), [types.go:8](../../services/api-gateway/internal/subjective/types.go), [worker_execution_service.go:7](../../services/api-gateway/internal/subjective/worker_execution_service.go)
- `subjective → assessment`：[eligibility.go:8](../../services/api-gateway/internal/subjective/eligibility.go), [handlers.go:6](../../services/api-gateway/internal/subjective/handlers.go), [math_evidence.go:10](../../services/api-gateway/internal/subjective/math_evidence.go), [panel_policy.go:12](../../services/api-gateway/internal/subjective/panel_policy.go), [panel_policy_store_memory.go:7](../../services/api-gateway/internal/subjective/panel_policy_store_memory.go), [panel_policy_store_postgres.go:10](../../services/api-gateway/internal/subjective/panel_policy_store_postgres.go), [panel_shadow_runner.go:14](../../services/api-gateway/internal/subjective/panel_shadow_runner.go), [types.go:9](../../services/api-gateway/internal/subjective/types.go)
- `subjective → auth`：[handlers.go:7](../../services/api-gateway/internal/subjective/handlers.go), [handlers_support.go:9](../../services/api-gateway/internal/subjective/handlers_support.go)
- `subjective → files`：[active_crop_resolver.go:14](../../services/api-gateway/internal/subjective/active_crop_resolver.go)
- `subjective → grading`：[adapter.go:7](../../services/api-gateway/internal/subjective/adapter.go), [adapter_http.go:17](../../services/api-gateway/internal/subjective/adapter_http.go), [handlers_grade.go:7](../../services/api-gateway/internal/subjective/handlers_grade.go), [math_grading.go:11](../../services/api-gateway/internal/subjective/math_grading.go), [store_memory.go:9](../../services/api-gateway/internal/subjective/store_memory.go), [types.go:10](../../services/api-gateway/internal/subjective/types.go), [validation.go:8](../../services/api-gateway/internal/subjective/validation.go)
- `subjective → gradingdisagreement`：[panel_disagreement.go:10](../../services/api-gateway/internal/subjective/panel_disagreement.go)
- `subjective → gradingevaluation`：[handlers.go:8](../../services/api-gateway/internal/subjective/handlers.go), [panel_policy.go:13](../../services/api-gateway/internal/subjective/panel_policy.go), [panel_policy_store_memory.go:8](../../services/api-gateway/internal/subjective/panel_policy_store_memory.go), [panel_policy_store_postgres.go:11](../../services/api-gateway/internal/subjective/panel_policy_store_postgres.go), [panel_shadow_runner.go:15](../../services/api-gateway/internal/subjective/panel_shadow_runner.go)
- `subjective → httpx`：[handlers_batch.go:8](../../services/api-gateway/internal/subjective/handlers_batch.go), [handlers_grade.go:8](../../services/api-gateway/internal/subjective/handlers_grade.go), [handlers_support.go:10](../../services/api-gateway/internal/subjective/handlers_support.go), [handlers_worker.go:7](../../services/api-gateway/internal/subjective/handlers_worker.go)
- `subjective → logger`：[handlers_support.go:11](../../services/api-gateway/internal/subjective/handlers_support.go)
- `subjective → mathunderstanding`：[grading_agent_v2_builder.go:15](../../services/api-gateway/internal/subjective/grading_agent_v2_builder.go), [math_evidence.go:11](../../services/api-gateway/internal/subjective/math_evidence.go), [math_grading.go:12](../../services/api-gateway/internal/subjective/math_grading.go), [types.go:11](../../services/api-gateway/internal/subjective/types.go)
- `subjective → modelcalibration`：[eligibility.go:9](../../services/api-gateway/internal/subjective/eligibility.go), [handlers.go:9](../../services/api-gateway/internal/subjective/handlers.go)
- `subjective → modelgovernance`：[panel_agent_binding.go:7](../../services/api-gateway/internal/subjective/panel_agent_binding.go), [panel_orchestrator.go:12](../../services/api-gateway/internal/subjective/panel_orchestrator.go)
- `subjective → paper`：[grading_agent_v2_builder.go:16](../../services/api-gateway/internal/subjective/grading_agent_v2_builder.go), [grading_reference.go:10](../../services/api-gateway/internal/subjective/grading_reference.go), [panel_disagreement.go:11](../../services/api-gateway/internal/subjective/panel_disagreement.go), [store_memory.go:10](../../services/api-gateway/internal/subjective/store_memory.go), [store_postgres.go:9](../../services/api-gateway/internal/subjective/store_postgres.go), [store_postgres_batch.go:9](../../services/api-gateway/internal/subjective/store_postgres_batch.go), [types.go:12](../../services/api-gateway/internal/subjective/types.go), [validation.go:9](../../services/api-gateway/internal/subjective/validation.go)
- `subjective → segment`：[active_crop_resolver.go:15](../../services/api-gateway/internal/subjective/active_crop_resolver.go)
- `subjective → workerruntime`：[handlers.go:10](../../services/api-gateway/internal/subjective/handlers.go), [handlers_batch.go:9](../../services/api-gateway/internal/subjective/handlers_batch.go), [handlers_worker.go:8](../../services/api-gateway/internal/subjective/handlers_worker.go), [worker_completion_service.go:7](../../services/api-gateway/internal/subjective/worker_completion_service.go), [worker_execution_service.go:8](../../services/api-gateway/internal/subjective/worker_execution_service.go)

### submission

管理考生答卷、页面和质量状态。

源码目录：[services/api-gateway/internal/submission](../../services/api-gateway/internal/submission)。

职责依据：[handlers.go](../../services/api-gateway/internal/submission/handlers.go)（抽读 1–24、27–48 行）。完整 SHA 见职责证据 JSON。

- `submission → auth`：[handlers.go:10](../../services/api-gateway/internal/submission/handlers.go)
- `submission → files`：[handlers.go:11](../../services/api-gateway/internal/submission/handlers.go)
- `submission → httpx`：[handlers.go:12](../../services/api-gateway/internal/submission/handlers.go)
- `submission → logger`：[handlers.go:13](../../services/api-gateway/internal/submission/handlers.go)
- `submission → pagination`：[handlers.go:14](../../services/api-gateway/internal/submission/handlers.go)

### taskmaterials

按任务快照读取题干、参考答案、解析和评分细则。

源码目录：[services/api-gateway/internal/taskmaterials](../../services/api-gateway/internal/taskmaterials)。

职责依据：[question.go](../../services/api-gateway/internal/taskmaterials/question.go)（抽读 1–24、18–36 行）。完整 SHA 见职责证据 JSON。

- `taskmaterials → paper`：[question.go:7](../../services/api-gateway/internal/taskmaterials/question.go)

### workerruntime

统一后台任务的领取、租约、完成、失败和重试。

源码目录：[services/api-gateway/internal/workerruntime](../../services/api-gateway/internal/workerruntime)。

职责依据：[handlers.go](../../services/api-gateway/internal/workerruntime/handlers.go)（抽读 1–24、51–77、1–174 行）。完整 SHA 见职责证据 JSON。

- `workerruntime → auth`：[handlers.go:9](../../services/api-gateway/internal/workerruntime/handlers.go), [identity.go:3](../../services/api-gateway/internal/workerruntime/identity.go), [scope_middleware.go:10](../../services/api-gateway/internal/workerruntime/scope_middleware.go)
- `workerruntime → httpx`：[handlers.go:10](../../services/api-gateway/internal/workerruntime/handlers.go), [scope_middleware.go:11](../../services/api-gateway/internal/workerruntime/scope_middleware.go)
- `workerruntime → logger`：[handlers.go:11](../../services/api-gateway/internal/workerruntime/handlers.go)

### workflowactors

按考试学校与任务权限列出可分派人员。

源码目录：[services/api-gateway/internal/workflowactors](../../services/api-gateway/internal/workflowactors)。

职责依据：[directory.go](../../services/api-gateway/internal/workflowactors/directory.go)（抽读 1–24、16–37 行）。完整 SHA 见职责证据 JSON。

没有扫描到直接内部import；这不代表没有第三方依赖或通过接口注入的调用。

### workspace

投影单场考试的准备、处理和阅卷进度，给出下一步操作。

源码目录：[services/api-gateway/internal/workspace](../../services/api-gateway/internal/workspace)。

职责依据：[service.go](../../services/api-gateway/internal/workspace/service.go)（抽读 1–24、80–129 行）。完整 SHA 见职责证据 JSON。

- `workspace → assessment`：[service.go:10](../../services/api-gateway/internal/workspace/service.go)
- `workspace → auth`：[archive.go:6](../../services/api-gateway/internal/workspace/archive.go), [handlers.go:7](../../services/api-gateway/internal/workspace/handlers.go), [service.go:11](../../services/api-gateway/internal/workspace/service.go)
- `workspace → exam`：[archive.go:7](../../services/api-gateway/internal/workspace/archive.go), [handlers.go:8](../../services/api-gateway/internal/workspace/handlers.go), [service.go:12](../../services/api-gateway/internal/workspace/service.go)
- `workspace → httpx`：[handlers.go:9](../../services/api-gateway/internal/workspace/handlers.go)
- `workspace → paper`：[service.go:13](../../services/api-gateway/internal/workspace/service.go)
- `workspace → processing`：[service.go:14](../../services/api-gateway/internal/workspace/service.go)
- `workspace → review`：[service.go:15](../../services/api-gateway/internal/workspace/service.go)
- `workspace → submission`：[service.go:16](../../services/api-gateway/internal/workspace/service.go)

## 静态环路记录

扫描发现的import环路：0条。
