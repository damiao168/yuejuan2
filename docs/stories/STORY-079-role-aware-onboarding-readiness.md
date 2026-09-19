# STORY-079：首次启用中心与角色化 Onboarding

## 目标

在不改写部署脚本、学校创建、模型配置和学校初始化流程的前提下，增加一层从真实资源推导的 Onboarding / Readiness 编排，让平台管理员与学校管理员登录后直接知道下一步。

## 已实现范围

- `GET /api/v1/onboarding/readiness` 根据认证用户自动返回 `platform` 或 `school` scope。
- 平台阻断项：系统核心状态、有效平台管理员、第一所学校。
- 平台建议项：AI 模式、数据处理策略；AI 不可用不阻断人工阅卷。
- 学校阻断项：学校资料、年级与班级、学生、教师与管理员。
- 首场考试是推荐项，不影响 `ready_for_use`。
- 聚合子读取失败时对应 check 返回 `unavailable`，其他 check 保持可用。
- 新增平台首次启用页、温和的一次性登录分流，以及平台/学校工作台继续设置提示。
- 建校表单由学校管理页与首次启用页共用。
- 学校初始化不再用用户数量猜测人员是否就绪，而是按业务角色判断。

## 安全与边界

- scope 只来自认证上下文，不接受客户端 `tenant_id`。
- 接口仅允许 `platform_admin`、`tenant_admin`、`school_admin`。
- 响应不返回 DSN、密码、令牌、API Key 或 credential reference。
- 不增加 `setup_progress` 表，不使用浏览器存储作为完成状态权威。
- 不提供 Web `.env` 编辑器，也不新增服务或部署单元。

## 验收

- 全新平台管理员从 Dashboard 自动进入 `/platform/getting-started`。
- 建校后 readiness 刷新，刷新浏览器仍由真实资源返回完成状态。
- 学校基础数据完整但无考试时 `ready_for_use=true`。
- readiness API 失败时工作台仍可访问，不产生跳转循环。
