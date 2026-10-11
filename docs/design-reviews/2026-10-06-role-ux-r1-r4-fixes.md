# 四角色产品复审 R1–R4 修复与验收

本轮承接[上一轮补充修复](2026-10-06-role-ux-follow-up-fixes.md)后的复审，按 R1、R2、R3、R4 的优先级拆分，三个智能体与主智能体并行实施、交叉审查。比较以本轮开始时的工作区备份为基线，保留原有及其他并行任务的未提交修改。以下结论仅覆盖本轮四项问题。

## 用户行为变化

| 编号 | 优先级、身份 | 原问题 | 修复后的行为 |
| --- | --- | --- | --- |
| R1 | P2，学校管理员 | 题目未保存可识别的评分策略时，冻结窗口提示“未配置”，仍可确认 | 每题都必须具有已保存、可识别的评分策略，才允许从准备页冻结；缺失或未知时阻止确认，并提供“前往题目配置”。其他读取失败停止核对、显示错误，重试读取成功后才能确认。确认函数同步再次拦截，关闭重开或切换考试的旧请求、旧确认失效 |
| R2 | P2，学校管理员及具有任务管理权限的教师 | 输入新关键词后立即选择全部匹配，可能同时使用旧服务端关键词和新本地关键词，选空或选错 | 筛选改变即使旧选择失效，当前筛选列表读取成功前禁用全选与分配预览。全选的服务端与本地筛选使用同一个关键词；切换范围后旧结果失效，完整分页成功后才提交选择，中途失败保留原列表和选择 |
| R3 | P2，平台管理员 | 两名管理员编辑同一角色绑定时，后保存的旧页面覆盖另一人的模型或提示词更新 | 每个角色绑定带独立 UUID 修订，保存必须提交读取时的修订；并发首次创建、旧修订更新返回 409。页面保留三个角色草稿并暂停保存，明确确认放弃草稿、成功刷新后才能重新核对保存 |
| R4 | P3，学生 | 整页批注吞掉版本冲突、答卷坏图没有恢复入口，成绩重新发布后仍停留在失效页面 | 整页批注一旦确认版本变化就立即提示，隐藏旧答卷证据并提供“查看当前成绩并重新核对”。本人及高分答卷坏图会核对发布版本；同版坏图可单独重试，普通批注失败可单独重试批注。提交复核期间禁止会卸载表单的重新核对操作，旧版草稿不自动复制到新版 |

R1 改变准备页的确认前提，后端原有默认评分策略物化规则仍保留。旧客户端或直接调用冻结接口不因此获得相同的前端逐题核实要求。R2 的全选入口只面向有任务管理权限的身份。R3 的原子保护单位是一个角色绑定，三个角色不是一笔整体事务。

R4 的图片错误事件不提供 HTTP 状态，所以通过成绩发布 ID 判断是否换版；版本复核读取的新版结果不会直接混入旧页面。页面、答卷视图或发布版本切换后，旧异步结果不能影响新页面；StrictMode 首帧重挂载也不会让图片重试永久锁住。同页某题返回版本冲突时，不等待其他尚未结束的批注请求。

## 验证结果

- 管理端统一专项：7 个测试文件，74 项通过，覆盖冻结确认、筛选全选、角色绑定、父级页面与会话边界。智能体另完成冻结及相关回归 24 项、任务队列及相关回归 57 项；这些集合与统一专项有重叠，不相加为总数。
- 学生端全量：10 个测试文件，117 项通过。其中新增整页答卷恢复 11 项，覆盖版本冲突、本人及高分坏图、普通网络失败、批注重试、切页迟到结果、提交期间重新核对、StrictMode 首帧错误及同页请求悬挂。旧提交测试增加等待已有申请核对完成，保留 POST 路径、credentials 与完整请求体断言（API 夹具）。
- Chromium：4 个场景通过，使用 API 夹具；覆盖模拟另一管理员更新后的冲突恢复、学校切换、模型治理及完整三智能体绑定流程。冲突恢复检验保留草稿、取消放弃、确认刷新及使用新修订再次保存，不宣称两个真实管理员页面连生产数据库验收。
- Go：modelgovernance、paper、assessment、review、scorerelease、reviewannotation、appeal、subjective、server、apicontract 共 10 个包通过。新增角色绑定并发测试覆盖首次创建竞争、同修订竞争、旧修订、重复提交、ABA、缺失前提及 HTTP 错误语义。
- 真实 PostgreSQL：在隔离的 PostgreSQL 17 数据库中，使用真实 `NewPostgresStore`，先应用原 156 和新 184 迁移；5 个子场景通过，包含存量回填、旧编辑及 ABA、首次创建竞争 20 轮、同修订更新竞争 20 轮、缺失前提不写入。最小夹具未覆盖完整生产权限、RLS 或全部迁移链；未接触业务数据库，临时数据库已停止。
- 管理端 TypeScript、生产构建、生产路由和构建体积预算通过；学生端 TypeScript 与最终生产构建通过。用户可见文案及本轮适用源码的 Biome 错误检查通过；e2e 目录为现有 Biome 排除项，不计入该检查。构建保留既有大包提示，学生既有 NaN 展示夹具仍输出 React 警告，未调整门槛。
- SDK 正式生成且与权威契约一致；注册路由覆盖、破坏性变更、授权契约及 schema 版本检查通过。注册路由仍为 524 条：276 条 OpenAPI、248 条已审核遗留缺口。契约、schema、生成器相关 Node 回归 14 项通过。

## 部署与兼容边界

新增迁移为 [000184_model_role_binding_revision.sql](../../services/api-gateway/migrations/000184_model_role_binding_revision.sql)，为存量行独立回填 UUID 修订。没有修改历史 156 迁移。Docker Compose 的 schema 版本通过正式同步流程更新为 000184。

部署应先应用 184，再协调发布 API 和管理页面。角色绑定保存的 `expected_revision` 现在必填：首次创建传 `absent`，更新传读取结果中的 `revision`。缺失或非法前提返回 400，竞争或旧修订返回 409 `panel_model_binding_changed`；旧客户端必须同步更新。破坏性变更脚本通过并不表示旧客户端兼容此新增前提。

本轮没有提交、推送或部署，没有在业务数据库执行迁移。完整生产数据库端到端验收未配置，不能计为通过。

## 本轮文件维护核对

下表记录实际修改文件附近的维护说明核对，不代表全仓注释整理完成。权威 JSON 和生成文件通过正式流程维护，不插入代码注释；未涉及的文件不计入本轮完成量。

| 文件 | 状态 | 维护点 |
| --- | --- | --- |
| apps/web-admin/src/pages/ExamPreparationPage.tsx | annotated | 未核实策略的确认前提、考试和弹窗代次、同步重复操作保护 |
| apps/web-admin/src/pages/ExamReadinessFreeze.test.tsx | annotated | 强制回调、关闭重开、切换考试及重复操作时序 |
| apps/web-admin/src/features/grading/workbench/hooks/useGradingQueue.ts | annotated | 同步筛选失效、请求作用域、完整分页选择与失败保留 |
| apps/web-admin/src/features/grading/workbench/hooks/useGradingQueue.productUx.test.tsx | annotated | 防抖窗口、迟到页、角色与范围变化的回归时序 |
| apps/web-admin/src/features/grading/workbench/components/TaskRail.tsx | existing_sufficient | 读取当前筛选期间禁用全选和分配预览 |
| services/api-gateway/internal/modelgovernance/role_binding.go | annotated | 首次创建前提、修订与冲突责任 |
| services/api-gateway/internal/modelgovernance/role_binding_store_memory.go | annotated | 同一互斥锁内比较与写入 |
| services/api-gateway/internal/modelgovernance/role_binding_store_postgres.go | annotated | 模型锁顺序、原子比较写入与竞争回滚 |
| services/api-gateway/internal/modelgovernance/role_binding_handlers.go | existing_sufficient | 400、409 的错误映射及既有审计边界 |
| services/api-gateway/internal/modelgovernance/role_binding_concurrency_test.go | annotated | 两位管理员先读后写、并发新建和更新的故障时序 |
| services/api-gateway/internal/modelgovernance/role_binding_test.go | self_explanatory | 初次写入显式 absent |
| services/api-gateway/internal/modelgovernance/role_binding_handlers_test.go | existing_sufficient | 更新夹具先取得修订，保留能力及停用测试 |
| services/api-gateway/internal/modelgovernance/managed_config_auto_test.go | existing_sufficient | 删除门槛夹具按当前修订更新 |
| services/api-gateway/internal/modelgovernance/managed_production_test.go | self_explanatory | 准入夹具显式初次创建 |
| services/api-gateway/internal/server/e2e_subjective_panel_test.go | existing_sufficient | 初始角色绑定夹具同步必填前提 |
| services/api-gateway/migrations/000184_model_role_binding_revision.sql | annotated | 存量回填、修订更换与应用写入前提 |
| apps/web-admin/src/pages/PanelModelBindingsSection.tsx | annotated | 冲突保留全部草稿、明确刷新解除与旧服务缺修订禁止保存 |
| apps/web-admin/src/pages/PanelModelBindingsSection.test.tsx | self_explanatory | 首次创建、草稿保护、刷新恢复及旧 API 边界 |
| apps/web-admin/src/api/modelApiConfig.ts | self_explanatory | 修订读写类型 |
| apps/web-admin/src/api/modelApiConfig.test.ts | self_explanatory | 必填写入前提的序列化 |
| services/api-gateway/openapi/edugrade-api.openapi.json | documentation | 权威字段、必填前提和错误语义 |
| packages/sdk/src/generated/types.ts | protected | 仅由正式 SDK 生成流程更新 |
| infra/docker-compose/.env.example | existing_sufficient | 正式同步 schema 版本 |
| infra/docker-compose/docker-compose.yml | existing_sufficient | 正式同步 schema 默认版本 |
| apps/student-portal/src/features/questions/PaperViewer.tsx | annotated | 图片版本复核、即时批注冲突、生命周期代次与 StrictMode 恢复 |
| apps/student-portal/src/App.tsx | annotated | 新整页证据组件、提交期间重载保护，保留其他并行任务的证据校验改动 |
| apps/student-portal/src/studentSession.tsx | annotated | 同步提交状态查询保护重绘前排队动作 |
| apps/student-portal/src/App.paperRecovery.test.tsx | annotated | 整页证据恢复及非直观请求时序 |
| apps/student-portal/src/App.test.tsx | annotated | 等待已有申请核对完成后提交，原断言保持 |
| tests/e2e/specs/platform-bindings-concurrency.spec.ts | annotated | API 夹具模拟另一管理员写入，明确刷新后才保存 |
| tests/e2e/specs/platform-model-management.spec.ts | existing_sufficient | 首次写入及成功响应夹具同步修订，保留其他并行测试改动 |
| docs/design-reviews/2026-10-06-role-ux-r1-r4-fixes.md | documentation | 实际修复、验证与部署边界 |

其他并行任务修改的 SDK 生成器局部命名、平台父级刷新回调排版和学生证据校验抽取予以保留，不归为本轮功能修复。
