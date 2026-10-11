# yuejuan 当前实现架构图

这套图纸以当前工作区源码为依据，提供 **10 个原生可编辑 draw.io 文件、70 个分层页面**。主图用短节点和箭头突出逻辑；详细表、接口和分支放在分域页。

源码审计：2026-10-10；交付复核：2026-10-11（Asia/Shanghai）。分支：`main`。Git HEAD：`d002c7b1f928e938b841e1203976c763b03e8485`。开始时有大量既有未提交修改，因此本次依据是 **HEAD 加当前工作区**，不能只检出该提交就声称还原本次事实。原始文件哈希与 Git 状态保存在 [workspace-baseline.json](evidence/workspace-baseline.json)。本次只新增文档与文档生成工具，保留原有代码和说明。

## 图纸目录

| 原生图纸 | 页数 | 用途与主要模块 |
| --- | ---: | --- |
| [01-feature-tree.drawio](diagrams/01-feature-tree.drawio) | 11 | F01–F10 功能编号；身份、组织、题库、考试、采集、OCR、评分、成绩、治理、桌面与运维 |
| [02-system-architecture.drawio](diagrams/02-system-architecture.drawio) | 2 | React/Tauri、SDK、Go 模块化单体、Python Worker、grading-agent、PG/MinIO/Redis |
| [03-module-dependencies.drawio](diagrams/03-module-dependencies.drawio) | 4 | 组合根及真实 import；采集、主观评分、发布与学校助手的直接/间接依赖 |
| [04-database-er.drawio](diagrams/04-database-er.drawio) | 29 | 全局、基数读法、27 个业务域；198 张 PG 表与 8 张桌面 SQLite 表 |
| [05-business-workflow.drawio](diagrams/05-business-workflow.drawio) | 3 | 考试准备→答卷处理→评分→人工终评→发布；专页说明旧、新发布并存 |
| [06-image-template-pipeline.drawio](diagrams/06-image-template-pipeline.drawio) | 3 | 解码/质量、已知模板路由、OpenCV 配准/ROI、人工恢复、空白与 OMR/OCR 边界 |
| [07-ai-grading.drawio](diagrams/07-ai-grading.drawio) | 4 | 生产单适配器、A/B/C 仲裁内核、失败/成本、数学多模态与确定性结算 |
| [08-api-data-flow.drawio](diagrams/08-api-data-flow.drawio) | 8 | 核心页面→API→Handler/Store；文件、Worker、模型、公开成绩与授权顺序 |
| [09-task-state-machine.drawio](diagrams/09-task-state-machine.drawio) | 3 | runtime 与领域状态、租约/重试/取消、并发与部分成功恢复缺口 |
| [10-security-deployment.drawio](diagrams/10-security-deployment.drawio) | 3 | 实际角色、认证/资源权限、tenant/school 边界、凭据加密与 Compose 物理部署 |

[离线预览](previews/index.html) 包含全部 70 页原生 SVG，默认按 100% 原生尺寸阅读，支持适应页面、125%、150%及切页；大图可在图内滚动。直接用浏览器打开本地 HTML 即可，编辑请使用 `.drawio`。

字号更新：普通框标题 **40–72px**、正文 **32px**，ER 表名 **34px**、字段 **26px**；普通框标题单行居中，方框按字宽收紧，表字段按行显示。完整说明和关系仍保留。最新排版验证见 [typography-validation.json](evidence/typography-validation.json)，源码审计仍采用下文记录的历史时点。

## 打开和阅读

1. 在 draw.io 桌面版，或浏览器 diagrams.net，选择“文件 → 从设备打开”，选择任意 `.drawio`。
2. 每份文件第一页为该主题的阅读入口；通过底部页面标签进入细节。
3. 图面只保留名称、关系与必要边界。完整说明在对象的 `details` 属性，源码路径在 `evidence` 属性及 [diagram-provenance.tsv](evidence/diagram-provenance.tsv)；编辑器“编辑数据”可查看。
4. ER 的父表→子表实线表示实际 FK，箭头方向用于关系读法，不表示 SQL 查询方向。业务与 API 图箭头表示调用或数据流。虚线的含义以连线文字及页内说明为准。

节点、菱形、文字与箭头都是原生 mxGraph 对象，未使用整页图片冒充可编辑图纸。新增页面继续保持“主题总览 → 业务域 → 详细流程”，避免把全部表或 API 挤进一页。

## 图例与事实口径

| 颜色/状态 | 含义 |
| --- | --- |
| 绿色：已实现/有代码依据 | 已核对该节点描述的实现；不承诺生产已启用、端到端可靠或模型效果合格 |
| 黄色：部分实现/接入有限 | 内核存在但入口未接通，或仍依赖人工/条件配置/现场验收 |
| 灰色：仅规划 | 只有可追溯设计依据时才使用，不能从目标需求推断实现 |
| 灰色虚线：待确认 | 缺少实现证据，或 DDL 自动抽取但该文件尚未完整审读；和规划状态分开 |
| 红色：已确认问题 | 静态调用或约束可定位的缺口；触发前提、影响范围及未验证内容见报告 |
| 蓝色：基础设施/外部服务 | 已确认的代码或部署配置边界；不代表服务当前运行 |

三智能体评分内核已实现 A/B 并行与 C 盲仲裁，但普通生产评分/Worker 仍组装单 `activeAdapter`。旧成绩发布 API 仍注册，与新版不可变快照及门禁并存。YOLO 备用识别和未知版面自动建模板未找到可信实现依据，图纸没有将其画成现有主流程。独立 `teacher-portal` 是预留目录，教师实际入口在 Web 管理应用和桌面工作站。

## 依据、覆盖与限制

| 证据 | 用途 |
| --- | --- |
| [ARCHITECTURE_REVIEW.md](ARCHITECTURE_REVIEW.md) | 实际架构、风险、优先级、触发条件与验证限制 |
| [DIAGRAM_MAINTENANCE.md](DIAGRAM_MAINTENANCE.md) | 变更影响矩阵、更新与验证方法 |
| [MODULE_CATALOG.md](MODULE_CATALOG.md) | 全部 68 个 Go 内部包的职责、源码依据及直接 import |
| [file-review.tsv](evidence/file-review.tsv) / [audit-coverage.json](evidence/audit-coverage.json) | 原始 3396 文件逐项审读状态，绑定源码 SHA-256 |
| [api-catalog.json](evidence/api-catalog.json) | 536 个字面量 Handle 注册、前端调用位置、OpenAPI/SDK 对照 |
| [database-catalog.json](evidence/database-catalog.json) | 全量字段、PK/FK/UQ/CHECK/显式索引、RLS、迁移来源 |
| [前端/API](evidence/frontend-api-audit.md)、[数据库](evidence/database-audit.md)、[流水线](evidence/pipeline-audit.md)、[工程/权限](evidence/root-audit.md) | 分域事实和风险依据 |
| [diagram-validation.json](evidence/diagram-validation.json) / [native-render-validation.json](evidence/native-render-validation.json) | XML、路径/行号、几何与实际 draw.io 逐页导入/渲染结果 |
| [visual-qa.md](evidence/visual-qa.md) / [workspace-preservation.json](evidence/workspace-preservation.json) | 视觉复查与原始内容保留检查 |

本次完成全仓静态盘点及关键架构链路审读，**不是 3396 文件全部逐行审计完成**。末次合并记录为 181 文件完整审读、64 部分审读、3151 未完成人工审读；后两类合计 3215 `pending`。自动枚举、源码提取和受保护文件状态都不算人工审读完成。

截至2026-10-11 11:47（Asia/Shanghai），58个已有文件的hash较启动变化，基线检查因此退出1；来源未在本任务确认，均保留当前内容。相关架构事实已复核，未重新审读的变化文件保持pending。见[工作区变化复核](evidence/concurrent-workspace-review.md)。前端目录专项复核时为852文件/484非测试TS，路由/API统计未变。本次没有回滚变化，也没有重写原始baseline。

数据库是源码声明结构重建：PG 迁移未在实际数据库执行，SQLite 仅在内存中执行现有 schema 验证。完整 FK 保留在目录，图中只画精选关系；未画线不表示无约束。路由数排除 3 个 HandleFunc 健康接口，OpenAPI 的 10 个路径参数别名不算漏注册。部署图描述仓库 Compose 配置，未验证生产 profile、TLS、硬件、云凭据或模型效果。本次没有运行全套 CI、生产 API、故障注入或扫描仪现场验收。

维护时应以变化后的源码重新审读，更新出处与状态，再重建图纸；本批历史文档保留原样，不能把早期规划说明混入当前实现。
