# 交付复核

独立源码复核：2026-10-10；主任务工件与证据收口：2026-10-11。核对对象为用户完整附件、当前工作区、10份原生图纸及本次新增文档。任务写入仅限新增架构文档、工具与预览，保留既有业务代码和说明。

结论：10份可编辑图纸及配套文档交付核验通过，主要架构事实和风险有源码依据。最终版70页已原生渲染并逐页视觉复查，文字已简化。全仓逐文件审读和业务运行验收仍有未完成项，见下方边界。

## 要求核对

| 要求 | 直接检查的证据 | 本轮结论 |
| --- | --- | --- |
| Git、工作区与只读范围 | [workspace-baseline.json](workspace-baseline.json)、[workspace-preservation.json](workspace-preservation.json)、根文档的工作区说明 | 基线保留；截至2026-10-11 03:47 UTC，HEAD相同，58原文件有变化；未回滚，不归因，基线检查退出1 |
| 功能层级、编号、实际入口及状态 | 图01规格、README、前端API审计 | F01–F10及分域页存在；教师预留目录与真实Web/桌面入口分开 |
| 逻辑层和物理部署分开 | 图02、图10的规格与Compose事实说明 | 逻辑域同进程，Python独立计算；内部两条HTTPS链路已同步，实际生产启用与外层TLS保持待确认 |
| 真实依赖和模块职责 | [module-imports.json](module-imports.json)、[module-responsibilities.json](module-responsibilities.json)、[MODULE_CATALOG.md](../MODULE_CATALOG.md)、图03 | 68包职责及74份出处文件hash/行号有效；351唯一方向、737处import；静态无环不证明动态解耦 |
| 全部核心数据库、字段和关系 | 图04规格、[database-catalog.json](database-catalog.json)、[database-validation.json](database-validation.json) | 198 PG+8 SQLite表覆盖；真实FK与应用关系分开，精选字段/核心IDX/条件唯一约束可见，全量定义在目录 |
| 考试至成绩及失败/人工分支 | 图05、[pipeline-audit.md](pipeline-audit.md)、旧新发布源码 | 旧发布与新快照并存；没有把新版门禁强加到旧路径 |
| 图像、模板、OpenCV和空白 | 图06及原始/简化标签 | 已有模板匹配和配准存在；YOLO/未知版面建模板标待确认，未虚构规划或自动闭环 |
| AI、A/B/C仲裁与失败/成本 | 图07及主观题源码 | 普通生产单适配器与可调用Panel内核分开；超时不重试、无效响应修复、输出token上限和费用未知保留为短标签 |
| 页面/API/存储/模型/鉴权流向 | 图08、[api-catalog.json](api-catalog.json)、guard和文件链路 | API口径、参数别名、二进制和公开成绩来源有旁置证据；SQLite表述为载荷/Spool加密 |
| 真实任务状态及恢复顺序 | 图09、runtime/配准/主观完成源码 | 使用源码枚举；已将分步提交缺口画成问题，没有虚构失败后自动修复 |
| 角色、tenant/school、Key和部署脱敏 | 图10、README与审查报告 | tenant不等于school；部分表RLS不冒充全表/全校隔离；不包含凭据值 |
| 指定10文件、原生可编辑对象和排版 | 图纸XML、[diagram-validation.json](diagram-validation.json)、[native-render-validation.json](native-render-validation.json)、[visual-qa.md](visual-qa.md) | 10文件70页全部原生导入，当前XML逐一匹配渲染SHA；0悬空边/文本溢出/线穿无关节点，全部联系表与5重点页已查看 |
| README、报告、维护及长期更新 | 三份根文档与[acceptance-report.md](acceptance-report.md) | 图纸目录/commit/图例/限制齐全；六类变更矩阵、Git/CI提示、短标签与完整属性维护说明已同步 |
| 未审读文件明确pending | [file-review.tsv](file-review.tsv)、[audit-coverage.json](audit-coverage.json) | 原始3396文件：181完整、64部分、3151未审，3215 pending；失效完成声明为空，自动枚举/受保护不算complete |

## 风险证据复核

- **P1-01**：直接读`score/store_postgres.go`的PublishGrades、qualityReportTx及GetStudentGrade，核对`routes_release.go`与新版`scorerelease/store_postgres.go`。旧路径确有自身质量/近期认证/考试范围检查，但缺题覆盖和current快照来源独立存在；两题只切出一题的条件描述合理。未执行生产调用，不能称为已发生事故。
- **P1-02**：直接读`router_guards.go`、`idempotency/middleware.go`和Postgres Begin。通用guard的收据重放先于内层当前资源权限；收据身份包括tenant/actor/route/key/正文指纹，没有session epoch。认证本身核对当前epoch，报告正确收窄为权限收窄后重新取得合法会话的同actor旧收据读取；不会重新执行写入。
- **P1-03**：直接读CompleteRegistration和ApplyRegistrationResult，核对TaskScope的AuthorizeLease。入口已有租约校验，但领域事务先提交且未共同锁runtime；报告定位到处理期间租约变化，没有误述成所有Worker入口无校验。
- **P1-04**：直接全文读WorkerCompletionService和WorkerExecutionService。grade保存、task完成和run更新为三步；末步失败后Prepare拒绝已成功task，正常完成重放缺口成立，不等于grade丢失。
- **P1-05**：直接全文读000088迁移。明细触发器遗漏INSERT，UPDATE仅查NEW.release_id；报告注明数据库写权限前提，未推导为普通HTTP任意修改历史。
- **P2-01**：直接读Panel终态分支和routeHuman。human_review先写，创建/关联人工任务随后执行；终态重放短路。报告只将其归于Panel内核，没有扩张到普通单适配器路径。

桌面SQLite另直接核对`durable_store/connection.rs`、crypto与Cargo依赖：普通rusqlite bundled连接，草稿/队列payload和Spool使用加密；没有整库SQLCipher保证。简化图已采用准确表述。

## 最终收口结果

1. 686节点可见文字减少26.8%；关系、完整说明和源码属性保留，图04/07的必要索引与失败边界仍可见。后续字号更新已将普通标题放大到40–72px、正文32px并收紧方框；最新排版检查见[typography-validation.json](typography-validation.json)，原源码审计时点保留。
2. 68包职责采用独立JSON与hash/行号校验，生成目录会保留职责；复杂模块补核范围已记录，未因此把部分审读升级为全文完成。
3. 当前XML与原生渲染hash一致，最终8张联系表覆盖70页；另查看功能、架构、ER、Panel与部署放大图。
4. README、审查报告与验收采用同一末次工作区快照及审读数量。机器交付检查结果见[delivery-validation.json](delivery-validation.json)。

完整CI、PG迁移/生产schema、真实API业务链、租约竞争/数据库故障注入、真实供应商效果及硬件/TLS/HA未执行。上述是明确未完成范围，不标为通过；静态目录和渲染只支持其各自核验范围。
