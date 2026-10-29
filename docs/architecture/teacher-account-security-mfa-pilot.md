# 身份验证器退役记录

2026-10-29：按产品决定彻底移除身份验证器。本文保留原路径供既有链接访问，取代原 TOTP 管理试点的部署和启用说明；身份验证器不再属于产品能力或后续路线。

## 已移除的范围

- 账户安全页的验证器注册、确认、动态码、恢复码、关闭及重新生成流程。
- 后端验证器处理器、配置开关、独立主密钥配置、凭据存储和操作级挑战实现。
- 下列七个接口及其 OpenAPI、生成 SDK 定义。新后端不注册这些路由，访问返回 404。

| 已退役接口 |
| --- |
| `GET /api/v1/auth/mfa` |
| `POST /api/v1/auth/mfa/totp/enroll` |
| `POST /api/v1/auth/mfa/totp/confirm` |
| `POST /api/v1/auth/step-up/start` |
| `POST /api/v1/auth/step-up/verify` |
| `POST /api/v1/auth/mfa/recovery-codes/rotate` |
| `POST /api/v1/auth/mfa/totp/disable` |

## 数据和升级顺序

历史迁移 `000149_auth_totp_scoped_challenges.sql`、`000150_auth_mfa_security_notification_intents.sql` 等保持原样，继续受迁移校验保护。退役通过新迁移 [`000220_retire_authenticator.sql`](../../services/api-gateway/migrations/000220_retire_authenticator.sql) 完成：

1. 移除学校汇总对验证器凭据表的触发器和函数依赖，并删除验证器管理员计数字段。
2. 删除四张专用表：`auth_totp`、`auth_mfa_recovery_code`、`auth_mfa_challenge`、`auth_security_notification_intent`。已绑定密钥、恢复码摘要、挑战和该功能专用的未发送通知意图随表删除。
3. 保留 `audit_log` 和 `event_outbox` 中已写入的历史事实，不将退役解释为抹除审计，也不将历史 outbox 的发布标记解释为通知已送达。

部署前按正常流程备份并验证完整迁移链。**先升级为不再依赖验证器表的新后端并停止全部旧后端节点，再执行 `000220`。** 旧节点仍可能查询凭据表或汇总字段，不能在旧节点运行时删表；迁移后也不能直接回退到依赖这些表的旧二进制。新前端与新后端配套更新。本文记录代码和迁移意图，不代表已经执行现有开发库或生产升级。

## 保留的账号能力

普通账号密码登录、邀请激活、管理员协助恢复密码、会话撤销、公共电脑锁定与解锁、风险影子观测继续保留。成绩发布等关键操作仍由服务端检查近期密码认证；移除验证器不放宽业务权限或组织范围。

原试点没有强制教师在普通登录时输入动态码。退役后不再读取验证器绑定，也不再向原已绑定账号发出验证器挑战。未来企业身份只保留独立的 [学校 SSO 规划](../stories/STORY-074-enterprise-identity-mfa.md)，不以该规划重新引入验证器。

旧研究见 [历史研究记录](teacher-account-security-mfa-ux-research.md)。历史报告、截图、生成架构证据和旧测试记录描述当时状态，不能作为当前入口或启用指南。
