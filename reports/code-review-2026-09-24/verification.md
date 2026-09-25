# 验证记录（2026-09-24）

对象为当前工作区（HEAD `84a33c572ab24f96f98d82426e101063cf4a1876` 加未提交修改），不是仅 HEAD。业务源码未因审查修改。以下日志均位于本目录；诊断测试仅用 Go overlay 或独立 Vitest 配置注入，不修改原测试文件。

| 验证层 | 结果 | 证据与边界 |
| --- | --- | --- |
| Go 全包测试 | 退出 0；63 个包通过，1,540 个测试/子测试通过，56 个测试跳过 | `go-test.jsonl`、`go-results-summary.json`；未配置 PG 环境的测试会跳过，不能代替真实 DB 集成。 |
| Go vet | 退出 0 | `go-vet.log`。 |
| 隔离 PostgreSQL 17 CI 选集 | 退出 0；67 个测试/子测试通过 | `go-postgres.jsonl`；独立临时数据库，非项目已有数据库。 |
| 额外 PostgreSQL 测试 | 原始运行退出 1；36 个测试/子测试通过，两项失败 | `go-postgres-supplement.jsonl`、`go-postgres-failures-recheck.jsonl`；两例不在 CI 的 PG `-run` 选择范围内。 |
| 额外 PostgreSQL 诊断 | 两项测试均通过 | `fixture-diagnostic.log`；overlay 仅改变测试夹具：认证时间从 20 分钟改为超过当前 1 小时默认 TTL；给手工建的 `answer_segment` 增现行 `crop_sha256` 列，并改用合法 64 hex SHA-256。支持“测试夹具漂移”，不证明原测试可直接通过。 |
| 前端单元 | 356 项通过 | `web-tests.log`：桌面 49、学生端 13、管理端 294。 |
| 前端 typecheck、build | 退出 0 | `typecheck.log`、`build.log`。 |
| Rust | 17 项通过 | `rust-tests.log`；不包含实际扫描硬件、打包或 Windows 凭据现场测试。 |
| Python Worker、AI、评估 | 共 335 项通过 | `python-results.csv` 与各套件日志；均为本地测试，不等于外部模型端到端。 |
| lab | 132 项通过 | `lab-tests.log`。 |
| Playwright 浏览器 | 70 通过、3 失败 | `playwright.log`；三例均在创建考试后等待旧 `/settings` URL，当前 UI 导向 `/students`。其余通过项主要使用 mock API，不等于真实数据库全链路。 |
| 合约／架构／schema／授权／工作流检查 | 均退出 0 | `gate-results.csv`；`lint` 退出 0 但有警告，详见 `lint.log`。生成 SDK 与路由覆盖只读比较均 MATCH，见 `generated-readonly.log`。 |
| 针对性缺陷诊断 | 已取消评分任务复活、考试切换竞态、跨年级班级丢选均复现 | `repro-cancelled-run.log`、`repro-preparation-race.log`。诊断测试 PASS 指“成功观察到缺陷”，不表示生产行为符合预期。 |
| Worker 跨队列动态诊断 | 退出 0，成功观察到 page-processing 身份领取学校租户主观评分队列任务 | `worker-repro.log`、`R01-worker-repro.md`；真实 router、全迁移、隔离 PG；未进行图片对象读取重放。 |

隔离 PG 使用临时目录，端口 55439。没有连接或更改现有项目数据库。未运行真实 AI 模型、对象存储生产环境、浏览器全链路非 mock API、扫描硬件、部署和备份恢复演练。
