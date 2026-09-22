# 高中主观题 Panel P1–P8 对照核查（2026-09-22）

对照原始“三智能体双评仲裁制”文档核查当前 `main` 代码。这里区分“软件能力已实现”和“真实九科验证已完成”；合成测试不能替代后者。

| 阶段 | 当前结论 | 代码与验证依据 |
| --- | --- | --- |
| P1 Panel | 服务端能力已实现 | `000155_subjective_multi_agent_panel.sql`、`subjective/panel_store_{memory,postgres}.go`；冻结答案/Rubric、每角色单独 Run、Panel 状态及人工任务约束有单元和 PostgreSQL E2E 覆盖。 |
| P2 A/B 双评 | 影子服务能力已实现，未接生产路由 | `subjective/panel_orchestrator.go` 并行启动两个独立角色 Run；每个 Adapter 输入深拷贝隔离嵌套证据，且不含同伴结果；终态重试回读已保存的角色评分而不重复调用模型。现有单智能体 HTTP/Worker 路由未切换。 |
| P3 分歧引擎 | 已实现 | `gradingdisagreement/engine.go` 与 `subjective/panel_disagreement.go` 覆盖总分、采分点、必得点、证据、置信度及硬风险；相同总分但证据不同也会仲裁。 |
| P4 C 盲仲裁 | 影子服务能力已实现 | C 使用独立角色/模型且强度序号必须高于 A/B；请求不包含 A/B 分数，结果仍由冻结 Rubric 在服务端结算。`000156` 支持学校/学段/学科/题型角色绑定；平台“模型配置”页面及 `000161` 支持跨学校管理员配置，且与日常对话默认模型分开。强度序号不是实证质量证明。 |
| P5 人工兜底 | 已实现 | 主评失败、能力准入失败、仲裁不确定等路径转 `ai_panel_disagreement` 复核任务；任务按 Panel 幂等关联。 |
| P6 评测体系 | 离线能力已实现 | `000157_subjective_panel_evaluation.sql`、`gradingevaluation/panel_metrics.go` 计算 A/B/C 对人工、假一致、仲裁挽救/错误、人工率与成本；要求至少两名人工评分者的仲裁标签。 |
| P7 高中九科真实 Shadow | **内部批量逻辑已补，真实运行链路与验证未完成** | `subjective/panel_shadow_runner.go` 预检已脱敏、双人仲裁的高中样本，按角色模型指纹绑定评测 Run，写入匿名观测并支持故障后续跑；主评缺失时不伪造分数。但 `ResolvePanelAgents` 和 Runner 尚未接入部署运行入口，`PanelAdapterFactory` 没有生产实现，现有 Python grading-agent 每个进程只配置单一模型版本。仓库仍没有可核验的九科真实数据或实际模型 Shadow 结果。 |
| P8 按学科/题型校准 | **模型指纹与策略机制已补，实证校准未完成** | `000158` 冻结阈值、审批和失效；`000160` 把策略审批和实际调用绑定到同一 A/B/C 模型与 Prompt 指纹。当前无基于真实九科评测批准的阈值，且 Panel 未接自动成绩发布。 |

后续完成 P7/P8 先要为 A/B/C 配置隔离的真实评分服务和受治理的连接/凭据，在受控运行入口中调用角色绑定解析、Panel Orchestrator 与 Shadow Runner；不能把第三方模型 URL 当作 grading-agent URL，也不能向现有单模型进程透传不匹配的模型版本。现有 A14 准入在证据不足时会拒绝外部调用，真实 Shadow 运行不能绕过该治理约束，需要先具备相应的模型评测/校准准入证据。随后需要各科、各题型的真实脱敏答案及冻结 Rubric、OCR/公式/图表证据、至少两位人工评分者和仲裁结论。按学科/题型分别记录样本量、假一致率、C 相对 A/B 的争议子集优势、QWK、人工升级率和成本，再审批阈值。缺任一条件时保持影子模式，不自动影响最终成绩。

本次代码验证：`go test ./...`、相关 Python 契约/Prompt 测试、前端 Vitest、TypeScript 检查及 `npm run ci:contracts` 通过；隔离测试库里的 PostgreSQL Panel/Evaluation E2E 通过，Linux 容器中 Panel 相关 Go 包的 `go test -race` 通过。真实模型和九科数据的 Shadow 尚未运行，这些合成/契约/迁移测试不能替代实证验收。
