# yuejuan2 并发优化后续实施方案

本方案覆盖原优化顺序中的第 3 项 Worker 和第 4 项混合负载。第 1 项发布事务和第 2 项采集上传的本地结果用作对照基线。正式容量按学校业务峰值和目标部署验收。

## 一、范围和顺序

| 阶段 | 工作 | 开始下一阶段的条件 |
| --- | --- | --- |
| 1 | 在真实 PostgreSQL 上复现并验证主观题入队连接等待，建立队列等待基线 | 单连接调度能在请求时限内完成，重复请求和取消有回归 |
| 2 | 对选中的一条队列做一个、两个 Worker 的对照 | 幂等和租约保护通过，吞吐改善达到试验门槛，其他链路未明显退化 |
| 3 | 本地混合负载冒烟，目标部署按到达率逐步加压 | 数据核对、延迟、失败率和清空时间满足预先约定的门槛 |
| 4 | 按测量结果只调整最慢阶段，再运行同样的试验 | 同一负载下有可重复改善，具备回退记录 |

固定镜像和 Git 提交、PostgreSQL 连接池大小、MinIO、模型版本和额度、资源配额与输入分布。每轮清空测试队列或使用新测试数据库，使用合成答卷、测试账号及隔离桶。对照时只改变一个变量。记录数据库和模型执行节点配置、网关实例数、扫描样本大小分布和脚本参数。

## 二、Worker 和队列

### 2.1 先验证入队能正常推进

Go 评分协调器的 scheduleAI 在持有 sql.Conn 和会话锁期间调用主观题模块。两个 store 共用一个连接池，后者仍需借连接读计划并开启入队事务。优先复用现有真实 PostgreSQL 评分协调器夹具，将池设为 1 并给单次调度设定明确截止时间。随后用生产装配的主观题入队路径补回归，避免测试替身遗漏事务内的重复借用。

复现后，优先把入队占有权收口到现有主观题事务。该事务已经锁定运行和条目、校验冻结输入、以请求键幂等创建评分运行和 Worker 任务，并写回条目。实施前确认调用者依赖的短路语义和配置失败的 blocked 写入条件。测试至少覆盖正常入队、同任务两个并发调用、取消、失效输入版本、确定性配置错误、暂时失败后恢复。验收核对同一有效输入只留下一个主观题运行、一条任务、一个正确条目关联，取消或旧输入不会产生新任务。操作截止时间只限制等待，现有错误分类和恢复语义继续适用。

源码入口：[评分协调器](../../services/api-gateway/internal/grading/scoring_coordinator.go#L222) · [共享数据库装配](../../services/api-gateway/internal/server/application_stores.go#L114) · [主观题入队事务](../../services/api-gateway/internal/subjective/image_coordinator.go#L214)。[Go 数据库连接指南](https://go.dev/doc/database/manage-connections) · [Go 事务指南](https://go.dev/doc/database/execute-transactions)。

### 2.2 标准化测量

| 指标 | 口径 |
| --- | --- |
| 队列深度 | 按队列区分已可领取、尚在重试退避、已租用和执行中 |
| 首次领取等待 | 第一次领取开始时间减任务创建时间，报告 P50/P95。延期首尝试单列 |
| 最老可领取任务 | 当前时间减最早满足可领取条件的时间，排除尚在退避的任务 |
| 执行耗时和恢复 | 现有执行 P95，另报完成、失败、重试、过期租约和受控接管数 |
| 清空耗时和吞吐 | 从释放同批任务到最后一条成功，用完成任务数除以墙钟时间 |
| 资源与下游 | 网关 P95、CPU、内存、数据库连接等待次数和累计时长、模型请求数与 HTTP 429 比例 |

首次领取时间可以从任务和尝试记录提取。领取 SQL 会清空 not_before，重试调度延迟需要在领取前记录可运行时间，或在试验期间采样。任何实际新增的 Prometheus 标签使用固定的队列和结果类型。按学校比较公平性时，在隔离测试报告中分组即可。 [现有队列指标](../../services/api-gateway/internal/workerruntime/store_postgres_metrics.go#L17) · [现有连接池指标](../../services/api-gateway/internal/observability/metrics.go#L364) · [Prometheus 队列指标](https://prometheus.io/docs/practices/instrumentation/#threadpools)。

### 2.3 对照、验收和回退

1. 本地先用真实 PostgreSQL 验证一个与两个不同 Worker ID 的领取、心跳、完成。24 条合成任务，每条使用固定时延替身。用 SQL 验证任务只成功一次。这个对照给出租约和调度基线。
2. 从采集后的页面处理、图像观察、主观题评分中，选实际排队 P95 或最老可领取任务年龄最高的队列。主观题 Worker 当前每轮只领一条，轮后固定休眠 5 秒。如果它是瓶颈，单独比较有任务就立即领取和现有行为，空队列、异常仍保留等待。此项的对照与副本数对照分开。
3. 在隔离 Compose 栈复用真实网关、Python Worker 和模型替身。基线一个实例，第二组以同镜像启动带独立容器名和 Worker ID 的 one-off 实例，三轮交替测量。固定容器名状态下直接使用 --scale 会失败。随后用授权的预生产模型配置做限量对照，验证模型 429 和退避，限流判定按供应商账号及模型的共享额度进行。
4. 试验门槛建议采用至少三轮的中位数。双实例吞吐达到单实例的 1.3 倍或清空耗时下降 25%，排队 P95 至少下降 20%。两组都要求零丢失、零重复生效、稳定负载中零意外租约过期。网关关键接口 P95 不超过基线 10%，连接池平均每次等待及 429 比例同时比较，模型 429 不超过事先约定预算。用受控停机复验过期后能接管、旧租约回执被拒绝、结果只提交一次。
5. 收益不足、发生租约错误或供应商限流恶化时，先停新增试验实例，保留单实例，等待未完成租约过期并核对最终状态。配置回退前后比较队列积压、延迟和错误，无需更改业务数据。

[主观题 Worker 循环](../../services/subjective-grading-worker/subjective_grading_worker/__main__.py#L34) · [现有 PostgreSQL 领取事务](../../services/api-gateway/internal/workerruntime/store_postgres_leases.go#L119) · [Compose 固定容器名](https://docs.docker.com/reference/compose-file/services/#container_name) · [Compose 单次实例](https://docs.docker.com/reference/cli/docker/compose/run/) · [PostgreSQL SKIP LOCKED](https://www.postgresql.org/docs/16/sql-select.html#SQL-FOR-UPDATE-SHARE) · [HTTP 429](https://www.rfc-editor.org/rfc/rfc6585.html#section-4)。

## 三、混合负载容量

### 3.1 场景与流量

| 场景 | 实际入口与核对点 |
| --- | --- |
| 集中采集 | 上传初始化、按文件依次确认每块偏移、完成上传。校验确认字节和文件哈希，数据库只登记一次。独立文件并行，分别测同一考试和不同考试 |
| 教师提交 | 使用已分派的不同任务和固定评分范围，请求携带当前修订号。同评分运行并发，核对写入一次。单独测试重复或过期提交，预期业务冲突单列 |
| 发布后查分 | 预先发布考试，用独立的学生身份查分。增加一场独立考试的发布事件，单独记录成功和耗时，校验查分支持已发布版本且始终返回一致总分和题分 |
| 后台处理 | 让已选 Worker 队列正常运行，采样最老可领取任务年龄，结束后计时至队列清空 |

先用隔离 HTTP 夹具冒烟，以上传、教师提交、查分每秒 1/1/4、2/2/8、3/3/12 各跑 3 秒，确认脚本参数、账号作用域和每条请求的业务断言。三种操作各用一个到达率场景。报告实际发出与接收到的操作数、HTTP 请求数、每个阶段的业务校验和未发出的迭代。

目标部署的初始压力示例采用 200 页在 120 秒内上传、60 名老师平均每 6 秒提交一次、1000 名学生在 30 秒内查分，向上取整为每秒 2 次上传、10 次教师提交、35 次查分。一次上传包含初始化、分块请求和完成，另统计实际 HTTP RPS 和 MiB/s。使用目标学校峰值和上传文件大小分布替换示例参数。按 25%、50%、100% 阶梯测试，每级持续 2–5 分钟。100% 至少三轮，另用 125% 寻找退化起点。用相同采集样本和条件复测。每组至少留足样本再解读 P99，低频发布以实际事件数与逐次耗时报告。 [k6 到达率](https://grafana.com/docs/k6/latest/using-k6/scenarios/executors/ramping-arrival-rate/) · [k6 未发出迭代](https://grafana.com/docs/k6/latest/using-k6/scenarios/concepts/dropped-iterations/)。

### 3.2 初始业务验收门槛

- 以 1 MiB 文件为参考的上传完整流程 P95 ≤ 2 秒，教师提交与学生查分 P95 各 ≤ 500 毫秒。更大的文件按大小分组，与目标网络带宽一并说明。记录 P99、吞吐、CPU、内存、数据库等待和队列清空时间。
- 业务 5xx 比例 ≤ 0.1%，100% 目标负载未发出的压测迭代为 0。校验失败、超时、意外 4xx 和预期幂等冲突各自计数。若未发出迭代，先确认压测机的预分配 VU、CPU 和网络是否足够。
- 发现丢失答卷、重复登记或评分、确认错学生、读到未发布或不一致成绩，立即停止加压并保留日志和数据样本。单独验证故障恢复时再注入错误，统计重试后最终结果。
- 清空时间由实际业务窗口确定。若学校尚未给出窗口，报告三轮清空时间和最老任务年龄，不签发正式容量。以最长连续满足延迟、正确性和排空预算的档位报告该部署的容量。上述示例阈值是试验起点，合同或学校验收阈值另行确定。

[现有 HTTP 延迟指标](../../services/api-gateway/internal/observability/metrics.go#L352) · [k6 阈值](https://grafana.com/docs/k6/latest/using-k6/thresholds/) · [k6 预分配 VU](https://grafana.com/docs/k6/latest/using-k6/scenarios/concepts/arrival-rate-vu-allocation/)。

### 3.3 按瓶颈决定变更

| 实测表现 | 下一步 |
| --- | --- |
| 连接池等待增长，同时 PostgreSQL 锁等待或 I/O 较低 | 先排查事务内再次借连接、外部 I/O 持有连接的路径。连接数调整需要同负载复验 |
| 同考试比跨考试慢，阻塞链指向采集集合 | 保留收卷一致性锁。先缩短已确认的锁内工作，确认必要后再评估分块存储迁移 |
| 教师提交集中阻塞在同一评分运行 | 测量锁内查询和统计，优先减少重复的整场刷新，复验取消和发布边界 |
| 发布耗时主要来自大量快照写入 | 测量每份快照 SQL 数和持锁时间；在同一事务内试验有界批量写入，核对版本和快照哈希 |
| 队列持续增长，模型 429 同步增加 | 按实际供应商额度控制在途请求或减少实例，复测退避和清空时间 |

交付每轮原始结果、环境、样本、Git 提交、关键指标及回退观察。用网关连接池等待统计和 PostgreSQL 的 wait_event 与 pg_blocking_pids 分开识别池等待、SQL 锁和 I/O。 [人工提交事务](../../services/api-gateway/internal/review/store_postgres_task_submission.go#L199) · [发布快照写入](../../services/api-gateway/internal/scorerelease/store_postgres_facts.go#L132) · [PostgreSQL 活动](https://www.postgresql.org/docs/16/monitoring-stats.html#MONITORING-PG-STAT-ACTIVITY-VIEW) · [PostgreSQL 阻塞查询](https://www.postgresql.org/docs/16/view-pg-locks.html)。

## 四、当前本地基线

- 发布门禁单连接 PostgreSQL 回归已通过，首次发布、复评发布和旧回执重放均覆盖。桌面重复上传和账号切换共 29 项测试通过。
- 对象存储在读到首字节后暂停时，修复前无关查询耗尽 3 秒时限，连接池新增 1 次等待。修复后同一查询在对象仍暂停时完成，新增等待为 0，上传恢复后核对了完整字节、最终偏移、唯一文件和幂等回执。恢复用例同时覆盖对象存储故障、过期完成租约和重试。
- 真实 PostgreSQL 与 MinIO，每轮 32 个约 256 KiB 的一页 PNG，拆成 64 KiB 分块。两种考试分布，各跑 1、2、4 路独立上传，每档三轮。修复前后各确认 576 份原件，文件哈希、确认偏移、唯一登记和暂存分块清理校验通过。下表取修复后三轮中位数。完成 P95 只统计 complete 阶段。

| 考试分布 | 并发路数 | 确认文件/秒 | 分块 P95（毫秒） | 完成 P95（毫秒） |
| --- | ---: | ---: | ---: | ---: |
| 同一考试 | 1 | 2.35 | 52.37 | 377.5 |
| 同一考试 | 2 | 3.48 | 87.17 | 564.1 |
| 同一考试 | 4 | 6.34 | 99.84 | 474.0 |
| 不同考试 | 1 | 2.08 | 134.9 | 619.8 |
| 不同考试 | 2 | 4.46 | 70.01 | 350.2 |
| 不同考试 | 4 | 9.86 | 44.07 | 293.0 |

自然负载下未采样到分块 SQL 被行锁阻塞，连接池等待为零。另有可重复用例主动持有同考试收卷行，确认同考试分块确实在等待该事务，同时另一场考试可以写入，释放锁后两场偏移都正确。吞吐测量使用共享本机，同档波动较大，试验不能归因到单次代码变更，也不足以决定更换暂存介质或调整桌面默认并发。 [批量上传基准与行锁回归](../../services/api-gateway/internal/captureupload/concurrency_benchmark_test.go#L34) · [慢对象存储回归](../../services/api-gateway/internal/server/e2e_capture_upload_slow_object_postgres_test.go#L44) · [S3 分段上传文档](https://docs.aws.amazon.com/AmazonS3/latest/userguide/mpuoverview.html)。

Worker 的 PostgreSQL 租约探针每组 24 条任务，使用 75 毫秒执行替身，一个和两个 Worker 各跑一轮。排队 P95 为 6.44 秒和 2.58 秒，清空为 6.32 秒和 2.72 秒，均只成功一次。 [Worker 探针](../../services/api-gateway/internal/server/e2e_subjective_worker_concurrency_postgres_test.go#L27)。

本地复验入口如下。定向数据库门禁以仓库根目录运行，先设置隔离容器的 EDUGRADE_E2E_DATABASE_URL。上传基准从 services/api-gateway 运行，使用 cmd/prepare-postgres-tests 创建的随机迁移库和已准备的独立 MinIO 桶。EDUGRADE_CAPTURE_BENCH_DATABASE_URL 指向随机库，EDUGRADE_CAPTURE_BENCH_MINIO_ENDPOINT 指向该 MinIO，桶和凭据由对应 EDUGRADE_CAPTURE_BENCH_MINIO_* 变量指定。记录每轮输出并按相同环境清理随机库和测试桶。

```text
node scripts/ci/run-postgres-tests.mjs ./internal/server ./internal/captureupload --tests=TestPostgresCaptureUploadSlowObjectStorageReleasesDatabaseConnection,TestPostgresCaptureUploadCollectionLockScope,TestSubjectiveWorkerConcurrencyWithPostgresTestDatabase
go test ./internal/captureupload -run '^$' -bench '^BenchmarkCaptureUploadConcurrency$' -benchtime=1x -count=3
```

## 五、后续执行验证

AI 阅卷入队的 PostgreSQL 单连接回归在修改前返回上下文超时。协调器现在在预检结束后释放连接，主观题事务负责核对当前观察和冻结输入、创建并关联任务。入队时限单独设为 15 秒，暂时失败由恢复扫描重试。永久错误只有在提交时仍是同一条目版本且输入有效时才标记阻塞。 [协调器](../../services/api-gateway/internal/grading/scoring_coordinator.go#L224) · [入队事务](../../services/api-gateway/internal/subjective/image_coordinator.go#L213)。

真实 Python Worker 在隔离 Compose 中完成三轮单、双实例对照，每组 24 条文本评分任务，共 144 条任务。试验使用本地固定镜像、当前数据库迁移、250 毫秒模型替身和 1 秒轮询。全部记录只产生一份建议，零重试、零失败和零连接池等待。下表为三轮中位数。 [对照脚本](../../infra/docker-compose/scripts/measure-subjective-worker-concurrency.ps1)

| 指标 | 单实例 | 双实例 |
| --- | ---: | ---: |
| 排队 P95 | 38.00 秒 | 19.83 秒 |
| 队列清空耗时 | 40.25 秒 | 21.87 秒 |
| 吞吐 | 0.60 条/秒 | 1.10 条/秒 |
| 单次执行 P95 | 564 毫秒 | 786 毫秒 |

双实例满足本地吞吐和排队改善门槛。另用真实 PostgreSQL 验证了租约接管后旧租约不能续期或提交，以及最终尝试过期时的原子死信结算。目标环境仍需使用实际供应商测试 429、核对单个任务耗时与整体吞吐，并以目标部署的 CPU、模型额度及队列峰值决定是否启用第二个实例。 [租约可靠性回归](../../services/api-gateway/internal/server/e2e_worker_runtime_reliability_test.go#L19)

第一次混合冒烟在同一采集批次中观察到 1 次上传初始化 HTTP 500。PostgreSQL 日志确认初始化事务通过触发器先锁考试，随后在外键检查等待批次行；同时文件登记已锁批次行并等待同一考试锁。初始化现提前取得外键所需的批次键共享锁，再插入会话。 [上传初始化事务](../../services/api-gateway/internal/captureupload/store_postgres.go#L22)

真实 PostgreSQL 锁顺序回归确认，初始化等待批次行时没有占用考试锁，批次锁释放后可正常初始化，同一命令重放会返回原会话。修复后两轮共验证 28 次上传、28 次有效教师评分提交和 119 次一致的已发布成绩读取。259 个 HTTP 请求中无失败，k6 没有未发出迭代。两轮 P95 分别为上传 690/603 毫秒、提交 493/438 毫秒、查分 187/131 毫秒。冒烟运行在本机回环，采用真实 PostgreSQL 和测试路由，使用内存对象存储。 [锁顺序回归](../../services/api-gateway/internal/server/e2e_capture_upload_lock_order_postgres_test.go#L17) · [混合冒烟](../../services/api-gateway/internal/server/e2e_mixed_capacity_test.go#L22)

后续压力场景应包含收卷与上传重叠，以及上传完成回执与批次入队重叠。源码中这两组操作持锁路径不同，本次混合冒烟没有专门制造这两种交叠。 [整场收卷](../../services/api-gateway/internal/capture/collection_store_postgres.go#L140) · [批次排队](../../services/api-gateway/internal/capture/store_postgres_batches.go#L42)

### 后续复验入口

- 容器 Worker 对照通过 `infra/docker-compose/scripts/measure-subjective-worker-concurrency.ps1` 在独立 Compose 启动。可用 `-GatewayBinary` 挂载指定提交构建的 Linux 网关二进制。默认参数为三轮、每组 24 题、1 秒轮询和 250 毫秒模型替身延迟。输出包含实际镜像 ID。 [对照脚本](../../infra/docker-compose/scripts/measure-subjective-worker-concurrency.ps1)
- 混合负载冒烟用命令 `go -C services/api-gateway test -tags mixed_capacity ./internal/server -run '^TestMixedCapacityHTTP$' -count=1 -v`。用 `EDUGRADE_MIXED_K6` 和 `EDUGRADE_E2E_DATABASE_URL` 提供 k6 和隔离 PostgreSQL，测试生成合成账号和随机数据库，只监听本地回环。 [测试夹具](../../services/api-gateway/internal/server/e2e_mixed_capacity_test.go#L22) · [k6 场景](../../scripts/ci/load/mixed-capacity.js)
