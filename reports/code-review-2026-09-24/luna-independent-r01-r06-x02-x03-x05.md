# 独立交叉复核：R01–R06、X02／X03／X05

审查对象为当前工作区，未修改业务源码，也未启动服务、连接数据库或运行测试。以下把此前保存的诊断日志当作已有佐证，不将其表述为本次复跑结果。审查按 sol-scope.md 的发现门槛去重：一个 Worker 队列边界根因只登记一次，并同时标注 R01、R03、X03；取消后阅卷任务复活只登记一次，并同时标注 R04、X05。

## 新确认：跨队列认领使服务任务隔离失效（R01、R03、X03）

**优先级：P1。** 持有 page_processing_worker 服务凭据的进程可以在通用 Worker claim 接口请求其他队列，并以请求中任意填写的 worker_service 和 instance 值取得这些任务的租约。平台租户中的该服务账号会跨活跃租户领取。拿到 subjective-grading 任务后，同一凭据能用任务 payload 里的 answer_segment_id 访问学生答案裁剪图。问题的前提是 Worker 凭据或进程已被控制；它扩大了受影响范围，跨过了不同 Worker 服务之间和学校租户之间的任务边界。

- services/api-gateway/internal/server/routes_worker.go:12 将共享 POST /api/v1/internal/worker/tasks/claim 接到 WorkerRuntimeHandler.Claim；router_guards.go:284-285 只要求 ocr:manage 或 orchestrator:manage。迁移 000028_story055_page_processing_role.sql:6-12 给 page_processing_worker 的权限正是 file:manage、ocr:manage；000075_subjective_grading_worker_role.sql:10-15 给另一独立角色 subjective_grading_worker 授 orchestrator:manage。两者分别用于不同 Worker 服务；没有可见的角色到允许队列映射。
- workerruntime/handlers.go:47-59 从请求解析 claim 参数，再按 auth.IsPlatformWorker 选择租户内或跨租户领取。auth/scope.go:3-8 将平台租户中的 page_processing_worker 识别为平台 Worker。平台 Worker 的配置示例使用平台租户身份，见 infra/docker-compose/.env.example:245-260。
- PostgreSQL claim 查询 workerruntime/store_postgres.go:214-230 只按队列名和租户范围筛选任务，没有按认证用户角色或服务名约束队列；:268-276 将输入中的 worker_service、worker_instance_id 原样写入租约。workerruntime/validation.go:47-64 仅要求这几个字符串非空。真实 Worker 客户端分别固定使用 page-processing 和 subjective-grading 队列，见 services/page-processing-worker/page_processing/api.py:47、services/subjective-grading-worker/subjective_grading_worker/api.py:41；服务端却接受任意队列名。
- 后续 TaskScope 在 workerruntime/scope_middleware.go:111-127 仅调用 AuthorizeLease 验证请求头中的 task ID、租约 token、service 名和 instance；PostgreSQL 实现 store_postgres.go:564-578 也只核这些租约字段及有效期，没有把 authenticated User.Roles 与 task queue/worker service 绑定。IsServiceUser 只判断角色是否为 Worker，见 auth/middleware.go:87-93。跨租户租约还会在 TaskScope 中把请求用户租户改为任务租户，见 scope_middleware.go:129-140。
- 主观评分任务的 payload 包含 answer_segment_id，见 subjective/handlers_batch.go:155。routes_worker.go:31 的答案图像端点只把 worker execute 权限与 RequireTaskPayloadValue("id","answer_segment_id")、TaskScope 组合；后者确认被请求的 segment 存在于刚领取任务的 payload，见 scope_middleware.go:53-69。segment/handlers.go:153-160 随后以 TaskScope 设置的 tenant 读取裁剪图。因此不同服务身份可以读取彼此任务 payload 已授权的答案图。带 file:manage 的 page worker 还可通过 routes_files.go:11 的任务范围下载路由读取任务 payload 指定的文件。

**最小静态复现：** 在平台 Worker 凭据有效、某学校有未领取的主观阅卷任务且其答案裁剪图为 active 的条件下，以 page_processing_worker token 调用共享 claim，body 指定 queue_name=subjective-grading、worker_service=subjective-grading-worker 和自选 instance。Claim 路径会跨租户扫描该队列，并返回任务 payload 和租约。随后将响应中的 task/token/service/instance 放入 Worker task headers，请求 /api/v1/internal/answer-segments/{payload.answer_segment_id}/image。根据当前中间件，任务 payload 校验和租约校验会通过，且没有服务角色/队列不匹配检查；预期应在 claim 或资源读取前拒绝该服务身份。**本次未动态运行该请求**，结论来自生产路由、认证权限、Postgres SQL 和文件读取链的静态追踪。

## 已有候选的独立核验

**S05-F01 / R04、X05：确认。** 取消事务在 grading/scoring_run_lifecycle.go:94-101 将活动 review task 和 scoring run 标为 cancelled。但 review/store_postgres_tasks.go:196-199,232-236 的单项与批量分派 SQL 只排除 submitted、completed，没有排除 cancelled；handlers.go:170-188 的 HTTP handler 只做管理者与阅卷人权限校验。分派后 SubmitGrade 可通过 canSubmit 和 revision 校验（store_postgres_tasks.go:267-283），写入 confirmed current question grade（:328-335），并以没有 run 状态条件的 SQL 将关联 run 改成 completed（:351-355）。后续成绩 finalization 将 submitted/completed 的 single review 写为 final_grade，无 scoring run 状态过滤，见 score/store_postgres.go:455-480。因此这是终止状态被复活的根因，数据可进入后续成绩及发布门禁链。存档的 repro-cancelled-run.log 显示此前真实 production router/隔离 Postgres 诊断曾观察到取消后 assign 200、submit 201、run 由 cancelled 变 completed、产生一条 current confirmed grade；我没有重跑该诊断。

**S07-F01 / R05、X02：确认。** AppShell.tsx:250-258 按考试路由参数传入 ExamStudentScopePage 和 ExamReadinessPage，没有使用 key={examId} 重挂载。学生范围页 ExamPreparationPage.tsx:49-59 在并行请求完成后无条件写 exam/classes/grades/selected；save() 用状态中的 exam.id 写入（:66-74）。准备页也在异步返回后无条件写 readiness（:115-123），而确认/开考操作则捕获当前路由 examId（:129-149），因此显示的旧考试检查可与实际动作目标不一致。已有诊断日志 repro-preparation-race.log 报告：B 页面接受延迟 A 响应后，保存目标成为 A。未由本次复跑。

**S07-F03 / R05：确认。** ExamStudentScopePage 每个年级各渲染一个 Checkbox.Group，但组内变化均直接用本组 values 替换跨年级共享的 selected，见 ExamPreparationPage.tsx:95-100。从一个年级再勾另一个年级时，后者的组选值不含前一组班级，因此前组选择被静默丢弃，保存会缩小考试班级范围。已存诊断用例及日志 repro-preparation-race.test.tsx、repro-preparation-race.log 覆盖“选第二年级后第一年级取消勾选、保存仅含第二组”的行为；这是与 S07-F01 不同的前端状态根因。

**S07-F02 / R05：静态确认是测试契约不一致，不是创建失败。** 当前创建完成路由 CreateExamPage.tsx:19-21 将非资料模式送往 /students；dashboard-workbench.spec.ts:81 仍断言 /settings，另两个恢复路径断言也保留旧目标。已存 playwright.log 记录 70 通过、3 个 URL 断言失败，实际 URL 为 /students。本次没有重跑浏览器套件；可保留为 CI 测试门禁问题，不能升级成考试创建事务失败。

**S08-F01 / R01、R05：确认。** 桌面 useDesktopSession.ts:146-157 登出时清本地 token/session state 并清理 durable session；apps/desktop-client/src/api/auth.ts 仅提供 login 与 getCurrentUser，未提供 logout 调用。服务端已注册 POST /api/v1/auth/logout（routes_auth.go:19），且 auth/handlers_session.go:14-36 会按 bearer token 删除 session。故点击桌面退出不会吊销服务端 bearer session；本机 UI 无 token 后，复制过的 token 仍能继续认证至过期/撤销。此候选由静态调用图确认，未在本次使用 bearer token 动态复现。

## 其余复核项

- **R02 / X02：当前新增 readiness advisory 与评分路由相符，未确认新的快照错位缺陷。** paper/scoring_coverage.go 将 formula、coding 识别为无自动评分路径；buildReadinessAdvisories 明确提醒全人工，不将其作为 blocker；grading/scoring_run_routing.go 对此类建立人工任务，StartScoringRun 将其计为 review，纯人工 run 为 needs_review。题目类型和 answer area 属于 readiness configuration snapshot；ready 时快照和禁止修改的数据库触发器仍是评分读取依据。此结论只覆盖新增路径和快照连接点，不表示全量 paper/assessment 审查。
- **R03 / X03：Worker 租约有效期和 token 绑定本身仍存在。** 发现是领取阶段身份—队列绑定缺失，而不是任意 task ID 可直接使用：后续 AuthorizeLease 仍要求活动任务和完整匹配的租约 token。该保护无法修补领取端已过宽的服务权限。
- **R04 / X05：除 S05-F01 外未在此次抽样中形成新的独立候选。** release current facts 基于当前 submission_grade/final_grade 快照（scorerelease/store_postgres.go:716-756）；这使 S05 的 current human grade 复活路径有下游意义，但发布仍受自身 gate 约束。
- **R06：本次未独立重审完整模型治理、Python adapter、Compose、迁移和 CI 专项；仅把 Worker role/Compose 身份配置用于 R01/R03 链的取证。此项不构成 R06 全面通过结论。**

## 去重与验证边界

本报告仅新增一个安全根因：通用 claim 不按已认证服务身份限制队列，导致跨队列冒领和任务隔离失效；R01/R03/X03 是同一问题的不同复核视角。取消任务复活归于 S05-F01（R04/X05）。考试路由迟到响应、年级 checkbox 覆盖和旧 E2E 路由断言是三个不同问题，分别归于 S07-F01、S07-F03、S07-F02。S08-F01 是独立的服务端登出吊销缺失。

本次仅文件阅读与静态调用链追踪。未操作数据库、测试服务、真实 worker 凭据或现场文件；已有报告日志只作为先前验证的辅助证据。新确认的 Worker 权限缺陷尚无本次动态复现，建议后续在隔离数据库和 production router 上验证 page_processing_worker 对 subjective-grading claim 与图片读取应被拒绝。
