import { useCallback, useEffect, useMemo, useState } from "react";
import { App, Button, Form, Input, List, Popconfirm, Tag } from "antd";
import { ChevronDown, ChevronUp, KeyRound, LogOut, RefreshCw, ShieldCheck, Smartphone } from "lucide-react";
import { getUserErrorMessage } from "../api/client";
import { changePassword, listSecurityEvents, listSessions, logoutAll, revokeSession, type DeviceSession, type SecurityEvent } from "../api/auth";
import { ErrorState, LoadingState } from "../components/PageState";
import { validateNewPassword } from "../auth/loginSecurity";
import { TOTPManagement } from "../components/TOTPManagement";

interface PasswordFormValues {
  currentPassword: string;
  newPassword: string;
  confirmPassword: string;
}

function sessionTypeLabel(type: DeviceSession["session_type"]) {
  if (type === "remembered_device") return "常用电脑";
  if (type === "public_device") return "学校公共电脑";
  if (type === "desktop_device") return "桌面采集客户端";
  if (type === "service") return "后台服务";
  return "浏览器登录";
}

function securityEventLabel(type: string) {
  const labels: Record<string, string> = {
    "auth.login_succeeded": "登录成功",
    "auth.login_failed": "登录失败",
    "auth.login_rate_limited": "登录尝试受到限制",
    "auth.logout": "退出登录",
    "auth.password_changed": "密码已修改",
    "auth.session_revoked": "一台设备已退出",
    "auth.sessions_revoked_all": "全部设备已退出",
    "auth.account_activated": "账号已激活",
    "auth.credential_recovery_completed": "账号恢复已完成",
    "auth.mfa_enabled": "身份验证器已启用",
    "auth.mfa_disabled": "身份验证器已关闭",
    "auth.mfa_recovery_used": "身份验证器恢复码已使用",
    "auth.mfa_recovery_rotated": "身份验证器恢复码已重新生成"
  };
  return labels[type] ?? "账户安全状态已更新";
}

export function SessionManagementPage({ onLoggedOut, accountLabel }: { onLoggedOut: () => void; accountLabel: string }) {
  const { message } = App.useApp();
  const [form] = Form.useForm<PasswordFormValues>();
  const [sessions, setSessions] = useState<DeviceSession[]>([]);
  const [events, setEvents] = useState<SecurityEvent[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();
  const [actioning, setActioning] = useState<string>();
  const [refreshKey, setRefreshKey] = useState(0);
  const [activityExpanded, setActivityExpanded] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    setError(undefined);
    try {
      const [sessionResponse, eventResponse] = await Promise.all([listSessions(), listSecurityEvents()]);
      setSessions(sessionResponse.sessions);
      setEvents(eventResponse.events);
      setRefreshKey((current) => current + 1);
    } catch (loadError) {
      setError(getUserErrorMessage(loadError, "登录设备加载失败"));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void load(); }, [load]);

  const securityActivity = useMemo(() => [
    ...sessions.map((session) => ({ id: `session:${session.id}`, kind: "session" as const, occurredAt: session.last_seen_at, session })),
    ...events.map((event) => ({ id: `event:${event.id}`, kind: "event" as const, occurredAt: event.occurred_at, event }))
  ].sort((left, right) => new Date(right.occurredAt).getTime() - new Date(left.occurredAt).getTime()), [events, sessions]);

  const revoke = async (session: DeviceSession) => {
    setActioning(session.id);
    try {
      await revokeSession(session.id);
      if (session.current) {
        onLoggedOut();
        return;
      }
      setSessions((current) => current.filter((item) => item.id !== session.id));
      message.success("该设备已退出");
    } catch (actionError) {
      message.error(getUserErrorMessage(actionError, "退出设备失败"));
    } finally {
      setActioning(undefined);
    }
  };

  const revokeAll = async () => {
    setActioning("all");
    try {
      await logoutAll();
      onLoggedOut();
    } catch (actionError) {
      message.error(getUserErrorMessage(actionError, "退出全部设备失败"));
    } finally {
      setActioning(undefined);
    }
  };

  const submitPassword = async (values: PasswordFormValues) => {
    setActioning("password");
    try {
      await changePassword({ current_password: values.currentPassword, new_password: values.newPassword });
      form.resetFields();
      message.success("密码已更新，请重新登录");
      onLoggedOut();
    } catch (actionError) {
      message.error(getUserErrorMessage(actionError, "修改密码失败"));
    } finally {
      setActioning(undefined);
    }
  };

  if (loading && sessions.length === 0) return <LoadingState label="正在读取账户安全信息" />;
  if (error && sessions.length === 0) return <ErrorState message={error} onRetry={() => void load()} />;

  return (
    <div className="page-stack session-management-page">
      <section className="page-heading">
        <div>
          <h1>账户安全</h1>
          <p>管理登录方式、设备和最近安全活动。浏览器只保存安全会话，不保存你的密码。</p>
        </div>
        <Button icon={<RefreshCw size={16} />} loading={loading} onClick={() => void load()}>刷新</Button>
      </section>

      <TOTPManagement
        personalSession={sessions.some((session) => session.current && session.session_type !== "public_device" && session.session_type !== "service")}
        accountLabel={accountLabel}
        refreshKey={refreshKey}
        onChanged={() => void load()}
        onLoggedOut={onLoggedOut}
      />

      <section className="workspace-section">
        <div className="section-heading compact">
          <div>
            <h2>登录与恢复方式</h2>
            <p>密码由你本人维护；学校管理员只能生成一次性恢复链接，无法查看或设置你的最终密码。</p>
          </div>
        </div>
        <div className="account-security-methods">
          <div><KeyRound size={20} /><span><strong>密码</strong><small>已启用 · 支持中文长密码短语</small></span><Tag color="green">可用</Tag></div>
          <div><ShieldCheck size={20} /><span><strong>账户恢复</strong><small>联系学校管理员核对身份</small></span><Tag>管理员协助</Tag></div>
        </div>
      </section>

      <section className="workspace-section">
        <div className="section-heading compact">
          <div>
            <h2>安全活动</h2>
            <p>登录中的设备与最近安全记录集中展示；不显示完整 IP 或浏览器指纹。</p>
          </div>
          <Button
            aria-expanded={activityExpanded}
            aria-controls="account-security-activity-details"
            icon={activityExpanded ? <ChevronUp size={16} /> : <ChevronDown size={16} />}
            onClick={() => setActivityExpanded((current) => !current)}
          >{activityExpanded ? "收起安全活动" : "展开安全活动"}</Button>
        </div>
        <div id="account-security-activity-details" hidden={!activityExpanded}>
        <List
          dataSource={securityActivity}
          rowKey="id"
          locale={{ emptyText: "暂时没有安全活动" }}
          renderItem={(item) => item.kind === "session" ? (
            <List.Item actions={[<Popconfirm key="revoke" title={item.session.current ? "退出当前设备？" : "让该设备退出？"} description="被撤销的会话需要重新输入密码登录。" onConfirm={() => void revoke(item.session)}><Button danger loading={actioning === item.session.id}>退出</Button></Popconfirm>]}>
              <List.Item.Meta
                avatar={<Smartphone size={22} />}
                title={<span>{item.session.device_name || "未命名设备"} {item.session.current ? <Tag color="blue">当前设备</Tag> : <Tag>登录中</Tag>}</span>}
                description={`${sessionTypeLabel(item.session.session_type)} · 最近使用 ${new Date(item.session.last_seen_at).toLocaleString("zh-CN")} · 有效至 ${new Date(item.session.expires_at).toLocaleString("zh-CN")}`}
              />
            </List.Item>
          ) : (
            <List.Item>
              <List.Item.Meta
                avatar={<ShieldCheck size={20} />}
                title={<span>{securityEventLabel(item.event.event_type)} {item.event.risk_level !== "low" ? <Tag color={item.event.risk_level === "high" ? "red" : "orange"}>需要关注</Tag> : null}</span>}
                description={`${item.event.device_summary} · ${new Date(item.event.occurred_at).toLocaleString("zh-CN")}`}
              />
            </List.Item>
          )}
        />
        <Popconfirm
          title="退出全部设备？"
          description="包括当前设备在内的全部登录会话都会失效。"
          onConfirm={() => void revokeAll()}
        >
          <Button danger icon={<LogOut size={16} />} loading={actioning === "all"}>退出全部设备</Button>
        </Popconfirm>
        </div>
      </section>

      <section className="workspace-section">
        <div className="section-heading compact">
          <div>
            <h2>修改密码</h2>
            <p>修改后会自动退出所有设备。</p>
          </div>
        </div>
        <Form<PasswordFormValues> form={form} layout="vertical" className="account-password-form" onFinish={(values) => void submitPassword(values)}>
          <Form.Item name="currentPassword" label="当前密码" rules={[{ required: true, message: "请输入当前密码" }]}>
            <Input.Password autoComplete="current-password" />
          </Form.Item>
          <Form.Item
            name="newPassword"
            label="新密码"
            extra="建议使用一句只有你知道的长短语，至少 15 个字符。"
            rules={[{ required: true, message: "请输入新密码" }, { validator: validateNewPassword }]}
          >
            <Input.Password autoComplete="new-password" />
          </Form.Item>
          <Form.Item
            name="confirmPassword"
            label="确认新密码"
            dependencies={["newPassword"]}
            rules={[
              { required: true, message: "请再次输入新密码" },
              ({ getFieldValue }) => ({ validator: (_, value) => !value || getFieldValue("newPassword") === value ? Promise.resolve() : Promise.reject(new Error("两次输入的密码不一致")) })
            ]}
          >
            <Input.Password autoComplete="new-password" />
          </Form.Item>
          <Button type="primary" htmlType="submit" icon={<KeyRound size={16} />} loading={actioning === "password"}>更新密码</Button>
        </Form>
      </section>
    </div>
  );
}
