# R01 — Worker 跨队列认领隔离 PostgreSQL 复现

## 结论

已在隔离 PostgreSQL 17、完整当前迁移和真实 production router 上复现：平台租户的 `page_processing_worker` 服务身份，凭自身有效 token 可在通用 claim API 自报 `subjective-grading` 队列与 `subjective-grading-worker` 名称，领取 demo 学校租户的主观评分任务。服务端返回有效 lease token，数据库也把请求自报的服务名写为该任务当前租约。**诊断测试 PASS 表示成功触发这项缺陷**，不是系统正确拒绝了请求。

## 诊断方法与证据

- [`worker_claim_repro_test.go`](worker_claim_repro_test.go) 是只位于审查目录的合成测试。Go overlay 将它作为虚拟 `internal/server` 测试文件编译，未修改业务源码或现有测试文件。测试通过 `e2eApplyPostgresMigrations` 初始化独立临时数据库；在平台租户建立合成 page-processing 服务用户并赋现行角色，在 demo 租户建立合成 `subjective-grading` runtime task。
- 测试经 `/api/v1/auth/token` 获取该 page-processing 服务身份的 bearer token，再向生产 router 的 `POST /api/v1/internal/worker/tasks/claim` 提交 `queue_name=subjective-grading`、`worker_service=subjective-grading-worker`。断言响应只含该跨队列、跨租户任务，状态为 leased 且返回非空 lease token，并从 PostgreSQL 验证该自报服务名与实例名已持久化。
- [`worker-repro.log`](worker-repro.log) 记录 `TestWorkerRuntimeCrossQueueClaimRepro` PASS（隔离环境执行约 9 秒）。使用的 PostgreSQL 集群在临时目录、端口 55439；未连接项目原有数据库。服务身份与任务数据均为测试生成，不涉及真实凭据或学生信息。

## 影响边界

该动态诊断确认 **跨 Worker 队列认领及平台 Worker 对活跃学校租户任务的领取**；学生答题裁剪图读取和下游 result/execute 操作仍依据 [`S10–S12 专项报告`](S10-S12-X01-X04-X06.md) 的路由、TaskScope、payload 与文件读取静态链，未在本诊断创建对象存储文件并进行 HTTP 图片下载。前提是持有有效的 page-processing Worker 凭据，不能推断匿名用户可访问，也不能推断最终成绩可直接发布。

修复应以已认证服务身份为服务端授权依据，明确映射允许的 queue/task type，并在租约后续敏感资源／结果提交路径保持同一身份绑定。加入相同隔离 PostgreSQL 回归：page-processing 身份申请主观评分队列应被拒绝，且不得返回 payload/租约。
