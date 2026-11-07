# CI architecture

CI is organized as independent layers so one failure does not hide unrelated failures:

- Workflow syntax: the independent `workflow-lint.yml` runs actionlint whenever workflow files change.
- Static: `web-static`, `go-static`, `python-static`.
- Unit: `web-unit`, `go-unit`, `go-race`, and `python-tests` matrix entries.
- Contracts and acceptance: `story-gates` (fail-fast disabled), `lab`.
- Database/integration: `go-postgres`, `go-boundary`, `compose-config`.
- Security: `node-security`, `python-security`, `go-security`, and `supply-chain` (SBOM plus Trivy).
- Release supply chain: `release-images.yml` builds all runtime images from repository variables pinned by digest, runs image-level Trivy and CycloneDX generation before publication, then signs the published digest and attaches an SBOM attestation with Cosign.
- Infrastructure: `compose-config` validates all profiles and isolated stacks; `compose-image-smoke` builds and loads the subjective worker configuration.
- E2E: `web-e2e-mocked` tests browser flows with API mocks. `real-system-e2e` starts PostgreSQL, the API, workers and grading agent; checks persistence; runs real Playwright; preserves failure evidence; and always tears the stack down.
- `ci-gate` runs with `always()` and fails on failure, cancellation, or unexpected skip.

Jobs only use `needs` for the final aggregate; independent checks therefore run in parallel. CI refactoring must never reduce validation coverage. Local entry points are `npm run ci:workflow-lint`, `npm run ci:web:fast`, `npm run ci:contracts`, and `npm run ci:web:full`.

`go-postgres` 统一执行 `node scripts/ci/run-postgres-tests.mjs`。入口要求显式 `EDUGRADE_E2E_DATABASE_URL`，创建随机数据库并应用全部迁移，再把历史测试变量映射到该隔离库；原 DSN 指定的数据库不被迁移或删除。它运行整个 Go 测试树并解析 JSON 事件，数据库用例或子测试跳过、枚举出的用例未执行以及测试失败都会使作业失败；清理只删除本次建立的随机库。少量外部模型、Python 合同和崩溃子进程的显式例外在 `postgres-test-gate.mjs` 中说明，不得把新的数据库用例加入例外。该作业的临时 Redis 同时验证跨实例登录限流。

`python-tests` 的基础设施矩阵覆盖部署、TLS、评估脚本和架构 XML 工具，数学矩阵覆盖整个 `lab/evals/math-recognition`。`check-python-test-coverage.mjs` 对照 Git 中的 Python 测试与工作流目标，新增测试未被任何 pytest 目标包含时失败。架构工具额外安装自己的锁定依赖；TLS 过期证书用固定过去时间签发，不依赖 OpenSSL 是否接受负 `-days`。

本地修正失败用例后可显式复验受影响包，例如 `node scripts/ci/run-postgres-tests.mjs ./internal/auth ./internal/capture`。也可传入明确的 `./tools/<路径>`，支持多级普通目录，不接受通配符或父目录跳转。需要缩小到顶层测试时可追加 `--tests=TestNameOne,TestNameTwo`，只接受实际声明的完整测试名，不接受正则表达式；包含 internal 包的范围仍须至少有一个已枚举的数据库测试。纯工具包可以没有数据库用例，但 Go 退出码必须为 0，也不能跳过测试。输出会注明 `selected packages` 和选定测试，只核对该范围的必跑用例，不能当作完整门禁结果。CI 保持无参数调用，始终执行全树。

Go 每个包的测试超时为 45 分钟，`go-postgres` 作业总时限为 90 分钟，为依赖安装、建库迁移、其他包、清理和后续检查留出时间。测试自身超时会先产生失败事件并进入随机库清理；平台取消或强制终止进程时，仍需依赖托管服务销毁收回临时数据库。

## 制品配额与保留期限

日常构建性能和浏览器诊断制品保留 7 天，CI SBOM 保留 30 天，正式发布的镜像摘要和 SBOM 制品保留 90 天；发布镜像另有签名及 SBOM attestation。上传仍是必需步骤，配额不足不能通过忽略上传失败或放宽 CI Gate 来绕过。

工作流中的 `retention-days` 只约束新上传的制品，不会缩短已有制品的到期时间。遇到 `Artifact storage quota` 时，先检查 Actions 的失败上传和现有制品的 `expires_at`；清理前核对制品归属及证据留存要求，保留需要的发布证据。恢复配额后重跑失败 job，并确认该提交的主分支 CI Gate 成功。操作与保留期限说明见 [GitHub 制品管理文档](https://docs.github.com/en/actions/how-tos/manage-workflow-runs/remove-workflow-artifacts) 和 [upload-artifact 参数](https://github.com/actions/upload-artifact#inputs)。

Initial bootstrap intentionally accepts only the `platform` tenant and `platform_admin` role. Ordinary users must be provisioned through normal user administration flows. For a new migration, create the file, run `npm run sync:schema-version`, then `npm run check:schema-version` and PostgreSQL tests.
