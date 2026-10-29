# STORY-074：企业身份与学校 SSO

状态：Planned。优先级：P1。依赖：既有 auth、会话撤销与审计；真实学校 IdP。

2026-10-29 范围调整：身份验证器已经退役，原 TOTP、恢复码、验证器挑战和管理员 MFA 重置范围撤回。文件名保留以兼容既有链接，当前规划只覆盖学校统一身份接入。退役详情见[身份验证器退役记录](../architecture/teacher-account-security-mfa-pilot.md)。

## 问题与范围

学校需要使用已有身份提供方登录，并在本地持续管理账号状态、组织范围和业务权限。先选择真实学校与 IdP，再实施 OIDC 接入与运维验收，不提前展示不可用的 SSO 按钮。

- OIDC Authorization Code + PKCE：机构 provider 配置、固定 redirect URI、state/nonce、issuer/audience/签名/时间校验、JWKS 轮换和失败关闭。
- 以 `(tenant, issuer, subject)` 绑定本地账号；邮箱相同不能自动绑定或跨租户合并。机构管理员审批身份与角色映射，平台权限和保密题库访问不由外部普通 role claim 自动授予。
- 外部认证成功后仍检查本地账号启停、组织范围和会话安全版本。明确单点登录、本站退出、管理员撤销本站会话各自的边界，不将退出本站等同于退出全部学校应用。
- 记录实际认证方式、认证时间、provider 和本地身份绑定，保留可追溯审计；不根据登录按钮名称或任意 claim 宣称高认证等级。
- 现有本地密码登录、邀请激活、密码恢复和受控 bootstrap 入口保持可用。成绩发布等关键操作继续遵循现有近期认证边界；若未来要接受 IdP 的认证新鲜度，须另行定义和验收可信条件。

## 非范围

不做本地 TOTP、恢复码、身份验证器或其操作级挑战，不重新启用已退役接口。不在本 Story 实施 WebAuthn、SAML/LDAP、全量账号自动迁移或机构凭据共享；不把 LTI Launch 当普通 OIDC provider，不凭单个 IdP claim 跳过本地权限或关键操作确认。

## 预计修改文件

修改 `internal/auth/`、会话中间件和管理端身份设置；拟新增 provider 与 identity binding 迁移、OIDC 回调、OpenAPI/生成 SDK 和部署 Runbook。身份验证器的旧表、密钥和挑战实现不作为复用基础。

## 测试方式与验收标准

本地协议夹具验证 nonce/state/PKCE、issuer/audience、失效 token、JWKS 轮换和安全角色映射；真实 PostgreSQL 验证身份绑定唯一性、跨租户隔离与会话撤销。真实学校 IdP 联调和故障恢复演练单独留证。

首版验收要求 issuer/subject 无法跨租户错绑，外部角色不越权，停用或撤销会话立即失去本站访问，认证故障不生成登录会话，登录和关键操作审计可追溯。协议依据为 [OIDC Core](https://openid.net/specs/openid-connect-core-1_0.html)，引用标准不表示已经实现。

## 规划审阅与实施记录

2026-09-14 的验证器管理试点已在 2026-10-29 撤回，不再计入本 Story 的在建能力。学校 OIDC、身份映射、管理端 provider 设置及真实 IdP 验收仍未交付，整体 Story 尚未验收。
