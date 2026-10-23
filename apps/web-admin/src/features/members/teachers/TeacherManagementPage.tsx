import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Alert, App, Button, Form, Input, Modal, Space, Tabs } from "antd";
import type { TableColumnsType } from "antd";
import { getUserErrorMessage } from "../../../api/client";
import { listSchools, type School } from "../../../api/org";
import {
  createManagedUser,
  createCredentialRecovery,
  listAssignableRoles,
  listAllManagedUsers,
  reissueManagedUserActivation,
  updateManagedUserStatus,
  type AssignableRole,
  type ManagedUser
} from "../../../api/users";
import { displayNameOrUsername, type SessionUser } from "../../../auth/session";
import { ErrorState, LoadingState } from "../../../components/PageState";
import { ResponsiveTable } from "../../../components/ResponsiveTable";
import { isStepUpCancelledError, useStepUp } from "../../../auth/stepUpContext";
import { AdministratorConfigurationNotice, MemberCreationFields, MemberManagementHeading, MemberStatus, recentUsage, teacherRoles, taskStaffRoleCodes, type CreateMode } from "./TeacherManagementViews";
import { MemberProfileDialog } from "./MemberProfileDialog";

type MemberView = "teachers" | "administrators";

// 操作直接展开并允许换行，其余列共享剩余宽度，避免窄屏被动作按钮撑出横向滚动。
const memberColumnWidths = {
  phone: "14%",
  recent: "16%",
  status: 80,
  actions: 264
};

function memberDialogPresentation(createMode: CreateMode) {
  if (createMode === "administrator") return { title: "新增学校管理员", submitLabel: "创建管理员邀请" };
  return { title: "新增教师", submitLabel: "创建并生成邀请" };
}

export function TeacherManagementPage({ currentUser }: Readonly<{ currentUser: SessionUser }>) {
  const { message, modal } = App.useApp();
  const { runWithStepUp } = useStepUp();
  const [users, setUsers] = useState<ManagedUser[]>([]);
  const [roles, setRoles] = useState<AssignableRole[]>([]);
  const [schools, setSchools] = useState<School[]>([]);
  const [view, setView] = useState<MemberView>("teachers");
  const [query, setQuery] = useState("");
  const [createMode, setCreateMode] = useState<CreateMode>("teacher");
  const [open, setOpen] = useState(false);
  const [profileUser, setProfileUser] = useState<ManagedUser | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [updatingUserID, setUpdatingUserID] = useState("");
  const [error, setError] = useState("");
  const [form] = Form.useForm();
  const loadVersion = useRef(0);

  const load = useCallback(async () => {
    const version = ++loadVersion.current;
    setLoading(true);
    setError("");
    try {
      const [userResult, roleResult, schoolResult] = await Promise.all([
        listAllManagedUsers(),
        listAssignableRoles(),
        listSchools()
      ]);
      if (version !== loadVersion.current) return;
      setUsers(userResult.filter((user) => user.roles.some((role) => teacherRoles.has(role) || role === "school_admin")));
      setRoles(roleResult.roles);
      setSchools(schoolResult.schools);
    } catch (loadError) {
      if (version === loadVersion.current) setError(getUserErrorMessage(loadError, "人员名单加载失败"));
    } finally {
      if (version === loadVersion.current) setLoading(false);
    }
  }, []);
  useEffect(() => { void load(); return () => { loadVersion.current++; }; }, [load]);

  const teacherUsers = useMemo(() => users.filter((user) => !user.roles.includes("school_admin") && user.roles.some((role) => teacherRoles.has(role))), [users]);
  const administratorUsers = useMemo(() => users.filter((user) => user.roles.includes("school_admin")), [users]);
  const activeUsers = view === "teachers" ? teacherUsers : administratorUsers;
  const visible = useMemo(() => activeUsers.filter((user) => `${user.display_name} ${user.phone_masked ?? ""} ${user.employee_no ?? ""} ${user.username}`.toLowerCase().includes(query.trim().toLowerCase())), [activeUsers, query]);
  const assignableRoleCodes = useMemo(() => new Set(roles.map((role) => role.code)), [roles]);
  const canCreateAdministrator = assignableRoleCodes.has("school_admin");
  const schoolNames = useMemo(() => new Map(schools.map((school) => [school.id, school.name])), [schools]);
  const schoolsNeedingBackup = useMemo(() => schools.map((school) => ({
    name: school.name,
    count: administratorUsers.filter((user) => user.school_id === school.id && user.status === "active").length
  })).filter((item) => item.count < 2), [administratorUsers, schools]);

  // 多角色账号必须所有角色都在可分配范围内，不能只因匹配其中一个低权限角色就允许管理。
  function canManageUser(user: ManagedUser) {
    return user.id !== currentUser.id && user.roles.length > 0 && user.roles.every((role) => assignableRoleCodes.has(role));
  }

  const renderActions = (user: ManagedUser) => {
    if (!canManageUser(user)) return "-";
    const busy = loading || Boolean(error) || saving || Boolean(updatingUserID);
    return <Space className="member-account-actions" size={2} wrap>
      <Button type="link" disabled={busy} onClick={() => { if (canManageUser(user)) setProfileUser(user); }}>编辑资料</Button>
      {user.status === "invited" ? <Button type="link" disabled={busy} loading={updatingUserID === user.id} onClick={() => void reissueActivation(user)}>重新生成邀请</Button> : <>
        {user.status === "active" ? <Button type="link" disabled={busy} loading={updatingUserID === user.id} onClick={() => void createRecovery(user)}>协助恢复</Button> : null}
        <Button type="link" danger={user.status === "active"} disabled={busy} onClick={() => confirmStatusChange(user)}>{user.status === "active" ? "停用" : "恢复"}</Button>
      </>}
    </Space>;
  };

  // 表格与侧栏使用同一登录账号；历史乱码姓名不能让当前账号看起来像第三个人。
  const identityColumns: TableColumnsType<ManagedUser> = [
    { title: "用户", key: "identity", render: (_, user) => {
      const name = displayNameOrUsername(user.display_name, user.username);
      return <span className="member-login-identity"><span>{user.username}</span>{name !== user.username ? <small>{name}</small> : null}{user.id === currentUser.id ? <small>当前登录</small> : null}</span>;
    } },
    { title: "手机号", dataIndex: "phone_masked", width: memberColumnWidths.phone, render: (value?: string) => value || "未填写" }
  ];
  const teacherColumns: TableColumnsType<ManagedUser> = [
    ...identityColumns,
    { title: "状态", dataIndex: "status", width: memberColumnWidths.status, render: (status: string) => <MemberStatus status={status} /> },
    { title: "最近使用", key: "last_login_at", width: memberColumnWidths.recent, render: (_, user) => recentUsage(user) },
    { title: "操作", key: "actions", width: memberColumnWidths.actions, render: (_, item) => renderActions(item) }
  ];
  const administratorColumns: TableColumnsType<ManagedUser> = [
    ...identityColumns,
    { title: "所属学校", dataIndex: "school_id", render: (schoolID?: string) => schoolID ? schoolNames.get(schoolID) ?? "学校信息已变更" : "未标明学校" },
    { title: "状态", dataIndex: "status", width: memberColumnWidths.status, render: (status: string) => <MemberStatus status={status} /> },
    { title: "最近使用", key: "last_login_at", width: memberColumnWidths.recent, render: (_, user) => recentUsage(user) },
    { title: "操作", key: "actions", width: memberColumnWidths.actions, render: (_, item) => renderActions(item) }
  ];

  async function reissueActivation(user: ManagedUser) {
    setUpdatingUserID(user.id);
    try {
      const result = await reissueManagedUserActivation(user.id);
      const activationURL = `${window.location.origin}${result.activation.path}`;
      modal.success({
        title: `已为${user.display_name}重新生成邀请`,
        width: 560,
        content: <div><p>此前的邀请链接已失效。请通过可信渠道发给本人，新链接将在 {new Date(result.activation.expires_at).toLocaleString("zh-CN")} 失效。</p><Space.Compact block><Input readOnly value={activationURL} /><Button type="link" onClick={() => void navigator.clipboard.writeText(activationURL).then(() => message.success("激活链接已复制"))}>复制</Button></Space.Compact></div>
      });
    } catch (reason) {
      message.error(getUserErrorMessage(reason, "暂时无法重新生成邀请"));
    } finally {
      setUpdatingUserID("");
    }
  }

  async function createRecovery(user: ManagedUser) {
    setUpdatingUserID(user.id);
    try {
      const result = await runWithStepUp({
        reason: `为“${user.display_name}”生成账号恢复链接`,
        description: "此操作可能改变该账号的登录凭据，因此需要验证当前管理员身份。验证后将自动继续。",
        action: () => createCredentialRecovery(user.id)
      });
      const recoveryURL = `${window.location.origin}${result.recovery.path}`;
      modal.success({
        title: `已为${user.display_name}生成恢复链接`,
        width: 560,
        content: <div><p>旧恢复链接已失效。请核对本人身份后通过可信渠道发送，新链接将在 {new Date(result.recovery.expires_at).toLocaleString("zh-CN")} 失效。</p><Space.Compact block><Input readOnly value={recoveryURL} /><Button type="link" onClick={() => void navigator.clipboard.writeText(recoveryURL).then(() => message.success("恢复链接已复制"))}>复制</Button></Space.Compact></div>
      });
    } catch (reason) {
      if (!isStepUpCancelledError(reason)) message.error(getUserErrorMessage(reason, "暂时无法生成恢复链接"));
    } finally {
      setUpdatingUserID("");
    }
  }

  function openCreate(nextMode: CreateMode) {
    setCreateMode(nextMode);
    form.resetFields();
    if (schools.length === 1) form.setFieldValue("school_id", schools[0].id);
    setOpen(true);
  }

  async function submit(values: { username: string; phone?: string; employee_no?: string; display_name: string; school_id: string }) {
    setSaving(true);
    try {
      // 教师只取得分配任务的阅卷、复核资格；学校管理权限仍通过独立管理员入口创建。
      const roleCodes = createMode === "administrator" ? ["school_admin"] : taskStaffRoleCodes;
      const result = await createManagedUser({
        username: values.username.trim(),
        phone: values.phone?.trim() || undefined,
        employee_no: values.employee_no?.trim() || undefined,
        display_name: values.display_name.trim(),
        role_codes: roleCodes,
        school_id: values.school_id,
        class_ids: []
      });
      message.success(createMode === "administrator" ? "管理员邀请已创建" : "教师邀请已创建");
      setOpen(false);
      if (result.activation) {
        const activationURL = `${window.location.origin}${result.activation.path}`;
        modal.success({
          title: "激活邀请已生成",
          width: 560,
          content: <div><p>请通过可信渠道发给本人。链接将在 {new Date(result.activation.expires_at).toLocaleString("zh-CN")} 失效，且只能使用一次。</p><Space.Compact block><Input readOnly value={activationURL} /><Button type="link" onClick={() => void navigator.clipboard.writeText(activationURL).then(() => message.success("激活链接已复制"))}>复制</Button></Space.Compact></div>
        });
      }
      await load();
    } catch (saveError) {
      message.error(getUserErrorMessage(saveError, createMode === "administrator" ? "添加学校管理员失败" : "创建教师账号失败"));
    } finally {
      setSaving(false);
    }
  }

  function confirmStatusChange(user: ManagedUser) {
    const disabling = user.status === "active";
    modal.confirm({
      title: disabling ? `停用${user.display_name || user.username}？` : `恢复${user.display_name || user.username}？`,
      content: disabling ? "停用后，该账号会立即退出全部设备；以后恢复也不能复用旧会话。" : "恢复后，该账号需要重新登录。",
      okText: disabling ? "确认停用" : "确认恢复",
      okButtonProps: { danger: disabling },
      cancelText: "取消",
      onOk: async () => {
        setUpdatingUserID(user.id);
        try {
          const result = await updateManagedUserStatus(user.id, disabling ? "disabled" : "active");
          setUsers((current) => current.map((item) => item.id === result.user.id ? result.user : item));
          message.success(disabling ? "账号已停用" : "账号已恢复");
        } catch (updateError) {
          message.error(getUserErrorMessage(updateError, disabling ? "账号暂时无法停用" : "账号暂时无法恢复"));
          throw updateError;
        } finally {
          setUpdatingUserID("");
        }
      }
    });
  }

  if (loading && !users.length) return <LoadingState label="正在加载人员名单" />;
  if (error && !users.length) return <ErrorState message={error} onRetry={() => void load()} />;
  const isTeacherView = view === "teachers";
  const dialog = memberDialogPresentation(createMode);
  return <div className="member-management-page">
    <MemberManagementHeading isTeacherView={isTeacherView} canCreateAdministrator={canCreateAdministrator} loading={loading} onRefresh={() => void load()} onCreate={openCreate} />
    {error ? <Alert type="warning" showIcon message="名单刷新失败，当前显示此前读取的名单" description={error} /> : null}
    <section className="member-table-section">
      <Tabs activeKey={view} onChange={(key) => { setView(key as MemberView); setQuery(""); }} items={[
        { key: "teachers", label: `教学与阅卷人员（${teacherUsers.length}）` },
        { key: "administrators", label: `学校管理员（${administratorUsers.length}）` }
      ]} />
      {!isTeacherView ? <AdministratorConfigurationNotice schoolsNeedingBackup={schoolsNeedingBackup} canCreateAdministrator={canCreateAdministrator} /> : null}
      <div className="member-management-filters"><Input.Search allowClear value={query} placeholder="搜索姓名、用户名、手机号或工号" onChange={(event) => setQuery(event.target.value)} /><span>共 {visible.length} 人</span></div>
      <ResponsiveTable className="dense-data-table member-management-table" rowKey="id" size="small" columns={isTeacherView ? teacherColumns : administratorColumns} dataSource={visible} pagination={{ pageSize: 20 }} />
    </section>
    {profileUser ? <MemberProfileDialog key={profileUser.id} user={profileUser} onCancel={() => setProfileUser(null)} onSaved={(user) => {
      setUsers((current) => current.map((item) => item.id === user.id ? user : item));
      setProfileUser(null);
    }} /> : null}
    <Modal title={dialog.title} open={open} footer={null} destroyOnHidden maskClosable={!saving} closable={!saving} onCancel={() => { if (!saving) setOpen(false); }}>
      <Form form={form} layout="vertical" disabled={saving} onFinish={(values) => void submit(values)}>
        <MemberCreationFields schools={schools} />
        <Button type="primary" htmlType="submit" loading={saving}>{dialog.submitLabel}</Button>
      </Form>
    </Modal>
  </div>;
}
