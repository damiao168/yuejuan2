# 外部变化的最新两文件增量验收

此前全仓和46文件扫描后，另一个任务更新了两份E2E源码。此次只把最新两文件原字节复制到新的临时固定副本，审读差异和原SQL/Cookie调用；没有修改业务源码或此前扫描证据，全文件审读状态仍为pending。

Semgrep 1.179.0 / OSS：735条配置，94条实际运行，12.984秒；两显式目标全部出现在paths.scanned，退出码0、10条命中、0条诊断。

10条均为原隔离E2E数据库动态SQL误报，rule/path/命中原字节SHA-256多重集合与此前46文件扫描中的这两文件完全相同，没有新增或减少命中。Cookie相关调用仍是httptest/隔离路由测试输入；没有新增生产Cookie响应配置。原处置依据继续成立。

e2e_mfa变化仅字段对齐。e2e_postgres变化为抽取三个RLS断言helper及拆分SQL迁移拆句状态机；静态审读确认原动态SQL标识符来源、参数绑定及原状态分支对应关系保留，不把该审读描述成PostgreSQL集成测试通过。

| 文件 | 此前固定副本 SHA-256 | 最新固定副本 SHA-256 | 当前是否仍匹配最新 |
| --- | --- | --- | --- |
| `services/api-gateway/internal/server/e2e_mfa_test.go` | `baef7b00789aa9b30bc285357fdc645175dd34bdf446cc33b64aef51a4455f60` | `d96120d9299f453d22441a852eb7c476c34feba291727c07c79a794012f8e9f0` | True |
| `services/api-gateway/internal/server/e2e_postgres_test.go` | `9e64cc5888db987c913d01baf93e784fdf1f465aab406b9542950732f5a2b916` | `63661ddcd204726ad063beec0b53c009ba11021abf0822af5f7a3af166695af4` | True |

两文件的matches_prior_rescan_snapshot均为false，matches_latest_verified_scan在本次核对时均为true。新source-inventory.json提供两套哈希；旧快照和覆盖账本任务保持原样。
