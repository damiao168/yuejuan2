# 主观题智能阅卷 API

## 产品边界

主项目通过受治理的内部 `grading-agent` 获取主观题评分建议。建议不会直接写入 `final_grade`，不会自动发布，也不会对学生直接可见。当前 `local-pilot-v1` 全部要求教师复核；作文和论述题仅允许影子记录。

浏览器 API 保持不变：

```text
POST /api/v1/answer-segments/{id}/subjective-ai-grade
```

只有具备 `grading:manage` 权限的登录用户可以调用。Go API 网关负责租户隔离、上下文读取、持久化和审计；浏览器不能直接访问内网智能体接口。

## 受治理上下文

网关从数据库读取：

- 考试学科 `exam.subject`；
- 考试绑定班级的唯一年级层级，7-9 年级映射为 `junior`，10-12 年级映射为 `senior`；
- 题目、最新已批准 Rubric 及其版本；
- 最新答案文本、答案版本和 OCR 置信度；
- Prompt 注入检测结果。

学段缺失、跨学段或不在能力矩阵内时，不调用模型，只保存失败的 AI 记录并要求人工处理。

发送给智能体的请求不包含 `tenant_id`、学生姓名、学号、学生 id、最终成绩或已发布成绩，也不发送答案图片引用。

## 模型策略

启用 `EDUGRADE_AI_SERVICE_URL` 后，模型版本、Prompt 版本、置信度阈值和超时由服务端环境变量锁定。请求体中的 `model_policy` 仅保留向后兼容，不能选择实际模型。

未配置智能体服务时，开发环境继续使用明确标记为 `mock=true` 的占位适配器，不冒充真实推理。

## 成功响应

响应为 `201 Created`。真实智能体建议包含：

```json
{
  "grade": {
    "status": "succeeded",
    "grader_type": "llm_subjective",
    "answer_version": "answer-uuid",
    "model_version": "Qwen/Qwen3-4B-GGUF:Q4_K_M",
    "prompt_version": "subjective-governed-cn-subject-routing-v6",
    "rubric_version": "rubric-v3",
    "delivery_mode": "teacher_suggestion",
    "capability_profile": "local-pilot-v1",
    "suggested_score": 4,
    "confidence": 0,
    "risk_flags": ["score_needs_review", "human_review_required", "low_model_confidence"],
    "needs_human_review": true,
    "mock": false,
    "adapter_name": "local_llama_cpp",
    "adapter_attempts": 1,
    "adapter_latency_ms": 106000,
    "adapter_repair_attempted": false
  }
}
```

分数由服务和 Go 网关分别根据 `matched_points` 重算。每个命中采分点必须引用答案原文中的证据，证据必须绑定 Rubric point。

## 失败语义

鉴权失败、能力不支持、模型不可用、超时、JSON/Schema 错误、采分点不完整、分数越界或证据不成立时，不生成有效建议。平台仍写入一条 `status=failed`、`suggested_score=0`、`needs_human_review=true` 的 `ai_grade`，但不保存未经验证的模型证据。

失败响应仍为 `201 Created`，以保持现有业务 API 的“尝试记录已落库”语义。调用方应检查 `grade.status`，不能只检查 HTTP 状态码。

## 审计

- `subjective.ai_grade_created`
- `subjective.ai_grade_failed`

普通系统日志只记录 request id、状态、尝试次数、耗时和错误码，不记录完整答案、学生身份或最终成绩。

## 三智能体 Panel 影子能力

`subjective_grading_panel`（迁移 `000155`）以答案版本和 Rubric 版本为冻结边界，将 A/B/C 三次独立 `subjective_grading_run` 关联起来；原有单智能体 HTTP 路由和最终成绩发布流程未改变。Panel 尚未接入公开路由或自动发布，仅可由受治理的服务端调用。

- A/B 并行盲评，协议中统一标为 `primary`；C 使用不同且配置强度更高的模型，协议中标为 `arbiter`。C 请求只包含冻结题目、Rubric 与学生证据，不含 A/B 分数或结论。
- A/B 各自拥有独立、持久化的角色 Run 和请求 ID。角色 Run 先进入 `queued`，执行者再原子认领为 `processing`；同一角色的并发重试不会产生第二次模型调用，而是返回执行中状态，完成后按相同请求 ID 幂等复用结果。
- 每个角色调用前必须通过现有 `aieligibility` 准入；缺少准入门禁、模型配置不一致、模型失败、输出无效或仲裁不确定时关闭自动候选路径，转人工复核。
- 模型只给出采分点支持/缺失和证据；Panel 服务丢弃模型给出的采分点数值与总分，按冻结 Rubric 确定性重算。A/B 数值相同但证据冲突仍触发仲裁，不取平均分。
- 数学 v2 Panel 复用现有证据准备、有效裁图校验与冻结数学评分规则；每个角色独立取得裁图，候选映射由服务端结算。数学证据缺失、修订冲突或角色适配器不支持 v2 时进入人工复核，不降级为 v1 文本评分。
- `000156` 保存按学段、学科、题型绑定的模型角色；`000157` 保存去标识化、不可变的离线评测观测，计算 A/B/C 与人工对照、假一致、仲裁挽救、人工升级及成本指标。
- Disagreement Engine 同时判断归一化总分差、加权采分点冲突、必得点冲突、证据冲突、置信度差和硬风险；触发原因以稳定代码随 Panel 冻结，不能只按总分差仲裁。
- 人工复核任务按 `subjective_panel_id` 唯一关联。同一 Panel 重试复用任务，不同答案版本的 Panel 不会误复用旧任务。
- 九科 Shadow 就绪门禁按“学科 + 题型”切片检查样本量、假一致率、C 在同一争议子集上的 MAE 优势、最终 MAE/QWK、覆盖率、仲裁率、人工率和成本。观测必须记录规范学段并由至少两名人工评分者仲裁；缺科、缺学段、缺 C 样本或 QWK 不可计算均不放行。
- `000158` 冻结每个“学段 + 学科 + 题型”的分歧阈值与就绪阈值。策略先处于 `shadow`，只有完成的去标识化评测 Run 经服务端重新计算并通过门禁后才能变为 `approved`；策略和评测证据可失效但不能原地改写。运行时的 `GradeWithApprovedPolicy` 只接受精确匹配的已批准策略。
- `000160` 冻结 A/B/C 按角色排序的模型与 Prompt 组合指纹。Shadow 评测 Run、策略审批及实际调用必须使用同一指纹；历史上缺指纹的策略保留可读，但不再满足运行门禁。内部 `PanelShadowRunner` 可批量接收已脱敏且至少双人仲裁的高中样本，预检后将服务端分数写入评测 Run，不自动完成评测、批准策略或发布成绩；主评失败样本不伪装成有效 A/B 观测。
- 平台管理员可在“模型配置”页的学校模型库下方，按学校、学段、学科、题型配置主评 A、主评 B 和仲裁 C。`GET/PUT /api/v1/platform/panel-model-bindings` 只允许平台模型管理权限；启用角色必须引用该校已通过完整结构化能力检测的模型，停用绑定仍可供审计查看。`000161` 允许跨学校配置的真实平台用户作为 `created_by`，不取消操作审计。三角色绑定独立于日常对话的 `is_default`，保存角色不会切换对话模型；绑定完整也不会自动开启 Panel 生产路由。

默认 15% 总分差、20% 采分点差等阈值仅供 Shadow 起步，随 Panel 保存以便审计；九科学科/题型的生产阈值与 C 的优势必须由真实高中仲裁评测集确认，不能仅凭配置的强度序号或合成单元测试认定已可上线。当前实现提供评测、审批和失效门禁，但仓库不包含可替代真实人工仲裁数据的“默认批准”策略。
