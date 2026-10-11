# 交付检查与未验证项

这份记录区分图纸/文档交付、架构事实证据与业务运行验证，避免把渲染通过当作整个系统测试通过。来源是当前工作区及本次实际执行结果。

## 要求与对应产物

| 用户要求 | 当前证据 |
| --- | --- |
| 读取Git/未提交状态并保留原有内容 | workspace-baseline.json；任务写入范围为docs/architecture及截图output/playwright；末次差异见workspace-preservation.json |
| 仓库、前后端、共享工具、依赖、测试、CI/部署 | repository-scan.json、module-imports.json、root-audit.md、frontend-api-audit.md、图02/03/10 |
| 功能编号、归属和真实状态 | 图01的F01–F10及11页；partial/unknown和真实入口分别说明 |
| 当前逻辑/物理架构与调用方向 | 图02两页逻辑/计算边界，图10独立Compose物理部署页 |
| 真实直接/间接依赖和循环检查 | 图03四页、68包职责及源码MODULE_CATALOG.md、74份出处文件hash核对、737处import证据/351唯一边；无静态import环，不证明无动态耦合 |
| 完整核心数据库实体/字段/关系/约束/版本/隔离 | 图04全局+关系读法+27域，共206表；图内精选字段、FK、UQ与IDX，完整定义见database-catalog与独立validation |
| 考试至成绩真实流程、失败与人工分支 | 图05三页；旧发布与新快照明确并存，pipeline-audit逐项门禁比较 |
| 图像/模板/OpenCV/空白/人工恢复 | 图06三页及pipeline-audit；YOLO/未知版面建模板保留待确认 |
| 多模态、模型、A/B/C、成本/超时/无效响应 | 图07四页；内核与普通生产单适配器接入边界明确 |
| 页面/API/存储/模型/鉴权数据流 | 图08八页与api-catalog；精确参数与别名分口径，/health例外解释 |
| 真实任务与领域状态、重试/租约/取消/恢复 | 图09三页；状态采用源码枚举，部分成功缺口标问题 |
| 角色/tenant-school/Key/部署并脱敏 | 图10三页与root/database审计；只写字段/算法/路径，没有凭据值 |
| 10个指定原生可编辑draw.io文件 | diagram-validation.json：10文件70页面，XML为mxGraph对象，无整图图片 |
| 中文、排版、连线与可打开验证 | native-render-validation.json：实际diagrams.net逐页渲染；visual-qa.md及全部SVG/截图 |
| 逻辑清楚、文字简短 | 686节点可见文字减少26.8%；关系未变，完整说明/出处保留为details/evidence；ER保留核心IDX/条件唯一约束，AI保留超时/费用边界 |
| README、审查报告、维护机制 | README.md、ARCHITECTURE_REVIEW.md、DIAGRAM_MAINTENANCE.md；六类变更映射、Git及CI提示规则 |
| 真实证据、未读pending、不改历史生成物 | diagram-provenance.tsv、file-review.tsv、各分工哈希账本；没有把自动枚举当审读 |

## 实际执行

| 检查/命令 | 结果 | 能证明的范围 |
| --- | --- | --- |
| python tools/build_diagrams.py | 退出0；10文件70页；errors=[] | XML/出处路径行号/节点范围/重叠/边端点 |
| playwright-cli run-code tools/render_native.js | 10文件70页；dangling/overflow/obstructions均0 | 实际draw.io导入、渲染及选定几何检查 |
| 全页联系表与重点图放大查看 | 已查看70页联系表+5个重点页 | 中文与明显视觉缺陷，详见visual-qa |
| python tools/verify_database_catalog.py | passed=true；errors=[] | PG静态117动态复合FK、ER206表、SQLite内存schema与PRAGMA一致 |
| node scripts/check-architecture-boundaries.mjs | **退出1**；3个原有页面超过700行 | 当前维护债务；未修复、未放宽门禁 |
| python tools/merge_review_ledger.py | 3396行，complete需匹配hash；pending保留 | 审读声明版本有效性，不自动证明审读质量 |
| python tools/verify_delivery.py | 交付静态核验；末次结果见delivery-validation.json | 当前规格与XML标签/关系/属性/出处一致、原生渲染hash及全文审读声明的源码版本有效；不证明业务运行 |
| python tools/snapshot_workspace.py --verify | **退出1**；截至2026-10-11 03:47 UTC，HEAD不变，58原始文件hash变化 | 工作区在审计期间发生并行变化；未回滚或覆盖，需结合复核记录 |

上述工具路径以`docs/architecture/`为基准。首次与末次检查有源变化时采用末次真实结果，不能重建baseline掩盖差异。架构门禁完整输出见[architecture-gate.txt](architecture-gate.txt)。

## 范围与尚未完成

本次是全仓静态盘点、关键架构链路与可视化交付。全仓逐文件完整审读尚未完成，精确数以[audit-coverage.json](audit-coverage.json)为准；未审读/部分审读文件明确pending，文件枚举与AST抽取不是人工完成。

未执行PG迁移/部署schema核验、全套CI、生产API/浏览器业务链、任务竞争/数据库故障注入、真实供应商模型效果/阈值、扫描仪现场与TLS/HA验收。数据库/依赖静态扫描的边界均已旁置说明。风险有源码路径和具体触发条件，但未声称生产利用或事故已经发生。

截止快照的58文件哈希变化来源没有在本任务中确认；本任务所有业务代码操作为读取。原有文件保持当前状态，受影响架构事实再审及新版hash见[concurrent-workspace-review.md](concurrent-workspace-review.md)。这项变化不能写为“原始全部hash未变”，也不能据此断言任何业务代码被本任务修改。
