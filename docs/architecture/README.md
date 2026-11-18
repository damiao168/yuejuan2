# yuejuan 当前实现架构图

这套图纸以当前工作区源码为依据，提供 **10 个原生可编辑 draw.io 文件、73 个分层页面**。主图用短节点和箭头突出逻辑；详细表、接口和分支放在分域页。

首次审计于 2026-10-10，当前整改复核于 2026-11-13。分支为 `main`，基线提交为 `5b797440e52e0dd356e41901adfca3564c385391`，包含当前工作区的整改。历史盘点仍保留原版本与证据。

## 图纸目录

| 原生图纸 | 页数 | 用途与主要模块 |
| --- | ---: | --- |
| [01-feature-tree.drawio](diagrams/01-feature-tree.drawio) | 11 | F01–F10 功能编号；身份、组织、题库、考试、采集、OCR、评分、成绩、治理、桌面与运维 |
| [02-system-architecture.drawio](diagrams/02-system-architecture.drawio) | 2 | React/Tauri、SDK、Go 模块化单体、Python Worker、grading-agent、PG/MinIO/Redis |
| [03-module-dependencies.drawio](diagrams/03-module-dependencies.drawio) | 4 | 组合根及真实 import；采集、主观评分、发布与学校助手的直接/间接依赖 |
| [04-database-er.drawio](diagrams/04-database-er.drawio) | 32 | 全局、基数读法、30 个业务域，含 219 张 PostgreSQL 表与 8 张桌面 SQLite 表 |
| [05-business-workflow.drawio](diagrams/05-business-workflow.drawio) | 3 | 考试准备→答卷处理→评分→人工终评→发布；专页说明兼容与正式发布如何共用门禁和快照 |
| [06-image-template-pipeline.drawio](diagrams/06-image-template-pipeline.drawio) | 3 | 解码/质量、已知模板路由、OpenCV 配准/ROI、人工恢复、空白与 OMR/OCR 边界 |
| [07-ai-grading.drawio](diagrams/07-ai-grading.drawio) | 4 | 文本与图像评分计划、A/B/C 仲裁和人工交接、失败/成本、数学多模态与确定性结算 |
| [08-api-data-flow.drawio](diagrams/08-api-data-flow.drawio) | 8 | 核心页面→API→Handler/Store；文件、Worker、模型、公开成绩与授权顺序 |
| [09-task-state-machine.drawio](diagrams/09-task-state-machine.drawio) | 3 | runtime 与领域状态、租约/重试/取消、原子提交和人工交接恢复 |
| [10-security-deployment.drawio](diagrams/10-security-deployment.drawio) | 3 | 实际角色、认证/资源权限、tenant/school 边界、凭据加密与 Compose 物理部署 |

[离线预览](previews/index.html) 包含全部 73 页原生 SVG，默认按 100% 原生尺寸阅读，支持适应页面、125%、150%及切页；大图可在图内滚动。直接用浏览器打开本地 HTML 即可，编辑请使用 `.drawio`。

字号更新：普通框标题 **40–72px**、正文 **32px**，ER 表名 **34px**、字段 **26px**；普通框标题单行居中，方框按字宽收紧，表字段按行显示。完整说明和关系仍保留。当前图纸的逐页原生渲染见 [native-render-validation.json](evidence/native-render-validation.json)。

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

考试图像评分计划可执行单角色或 A/B 主评与条件 C 仲裁。文本 Panel 内核可恢复人工任务交接。兼容和正式成绩发布共用发布门禁及当前快照。YOLO 备用识别和未知版面自动建模板仍待确认。教师使用 Web 管理应用及桌面工作站。

## 依据、覆盖与限制

| 证据 | 用途 |
| --- | --- |
| [ARCHITECTURE_REVIEW.md](ARCHITECTURE_REVIEW.md) | 实际架构、风险、优先级、触发条件与验证限制 |
| [DIAGRAM_MAINTENANCE.md](DIAGRAM_MAINTENANCE.md) | 变更影响矩阵、更新与验证方法 |
| [MODULE_CATALOG.md](MODULE_CATALOG.md) | 首次审计的 Go 模块职责与导入证据 |
| [file-review.tsv](evidence/file-review.tsv) / [audit-coverage.json](evidence/audit-coverage.json) | 原始 3396 文件逐项审读状态，绑定源码 SHA-256 |
| [当前路由覆盖](../../services/api-gateway/openapi/route-coverage.json) / [初始 API 目录](evidence/api-catalog.json) | 当前注册和 OpenAPI 对照，以及首次审计的前端调用位置 |
| [database-catalog.json](evidence/database-catalog.json) | 全量字段、PK/FK/UQ/CHECK/显式索引、RLS、迁移来源 |
| [前端/API](evidence/frontend-api-audit.md)、[数据库](evidence/database-audit.md)、[流水线](evidence/pipeline-audit.md)、[工程/权限](evidence/root-audit.md) | 分域事实和风险依据 |
| [diagram-validation.json](evidence/diagram-validation.json) / [native-render-validation.json](evidence/native-render-validation.json) | XML、路径/行号、几何与实际 draw.io 逐页导入/渲染结果 |
| [visual-qa.md](evidence/visual-qa.md) / [workspace-preservation.json](evidence/workspace-preservation.json) | 视觉复查与原始内容保留检查 |

逐文件账本保留原始 3396 个文件的审读范围。只有源文件哈希仍匹配的完成记录才计入已审读，已失效记录退回待复核。本轮新增或拆出的文件按整改范围核对，旧账本统计见 [audit-coverage.json](evidence/audit-coverage.json)。

数据库目录从迁移和桌面 Schema 重建，SQLite 在内存执行。当前 PostgreSQL 回归使用隔离测试库。生产配置、真实模型效果和硬件仍需各自验收。修改业务行为后按 [维护说明](DIAGRAM_MAINTENANCE.md) 核对受影响的图纸、出处及预览。
