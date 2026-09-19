import { useCallback, useEffect, useMemo, useState } from "react";
import { Alert, App, Button, Form, Input, Modal, Select, Space, Tabs } from "antd";
import type { TableColumnsType } from "antd";
import { Plus, RefreshCw } from "lucide-react";
import { getUserErrorMessage } from "../../../api/client";
import { listClasses, listGrades, listSchools, type Grade, type School, type SchoolClass } from "../../../api/org";
import {
  createManagedUser,
  createCredentialRecovery,
  listAssignableRoles,
  listManagedUsers,
  reissueManagedUserActivation,
  updateManagedUserStatus,
  type AssignableRole,
  type ManagedUser
} from "../../../api/users";
import type { SessionUser } from "../../../auth/session";
import { ErrorState, LoadingState } from "../../../components/PageState";
import { ResponsiveTable } from "../../../components/ResponsiveTable";
import { StatusTag } from "../../../components/StatusTag";
import { isStepUpCancelledError, useStepUp } from "../../../auth/stepUpContext";

const roleLabels: Record<string, string> = {
  tenant_admin: "机构管理员",
  school_admin: "学校管理员",
  teacher: "学科教师",
  grader: "阅卷教师",
  arbitrator: "仲裁/复核教师"
};
const teacherRoles = ["teacher", "grader", "arbitrator"];

type MemberView = "teachers" | "administrators";
type CreateMode = "teacher" | "administrator";

export function TeacherManagementPage({ currentUser }: { currentUser: SessionUser }) {
  const { message, modal } = App.useApp();
  const { runWithStepUp } = useStepUp();
  const [users, setUsers] = useState<ManagedUser[]>([]);
  const [roles, setRoles] = useState<AssignableRole[]>([]);
  const [grades, setGrades] = useState<Grade[]>([]);
  const [classes, setClasses] = useState<SchoolClass[]>([]);
  const [schools, setSchools] = useState<School[]>([]);
  const [view, setView] = useState<MemberView>("teachers");
  const [query, setQuery] = useState("");
  const [createMode, setCreateMode] = useState<CreateMode>("teacher");
  const [open, setOpen] = useState(false);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [updatingUserID, setUpdatingUserID] = useState("");
  const [error, setError] = useState("");
  const [form] = Form.useForm();
  const selectedRole = Form.useWatch("role_code", form);

  const load = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const [userResult, roleResult, schoolResult, gradeResult, classResult] = await Promise.all([
        listManagedUsers({ limit: 200 }),
        listAssignableRoles(),
        listSchools(),
        listGrades(),
        listClasses()
      ]);
      setUsers(userResult.users.filter((user) => user.roles.some((role) => teacherRoles.includes(role) || role === "school_admin")));
      setRoles(roleResult.roles);
      setSchools(schoolResult.schools);
      setGrades(gradeResult.grades);
      setClasses(classResult.classes);
    } catch (loadError) {
      setError(getUserErrorMessage(loadError, "人员名单加载失败"));
    } finally {
      setLoading(false);
    }
  }, []);
  useEffect(() => { void load(); }, [load]);

  const teacherUsers = useMemo(() => users.filter((user) => !user.roles.includes("school_admin") && user.roles.some((role) => teacherRoles.includes(role))), [users]);
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
  const administratorNotice = `${schoolsNeedingBackup.length
    ? `${schoolsNeedingBackup.map((item) => `${item.name}当前有 ${item.count} 位`).join("；")}。建议至少保留 2 位启用的学校管理员，系统不允许停用最后一位。`
    : "当前每所学校均有至少 2 位启用的学校管理员，系统不允许停用最后一位。"}${canCreateAdministrator ? "" : " 如需新增、停用或恢复管理员，请联系机构管理员。"}`;

  function canManageUser(user: ManagedUser) {
    return user.id !== currentUser.id && user.roles.length > 0 && user.roles.every((role) => assignableRoleCodes.has(role));
  }

  const statusLabel = (status: string) => status === "active" ? "已启用" : status === "invited" ? "待激活" : "已停用";
  const renderActions = (user: ManagedUser) => {
    if (!canManageUser(user)) return "-";
    if (user.status === "invited") return <Button type="link" loading={updatingUserID === user.id} onClick={() => void reissueActivation(user)}>重新生成邀请</Button>;
    return <Space size={2}>{user.status === "active" ? <Button type="link" loading={updatingUserID === user.id} onClick={() => void createRecovery(user)}>协助恢复</Button> : null}<Button type="link" danger={user.status === "active"} loading={updatingUserID === user.id} onClick={() => confirmStatusChange(user)}>{user.status === "active" ? "停用" : "恢复"}</Button></Space>;
  };

  const recentUsage = (user: ManagedUser) => user.last_login_at ? new Date(user.last_login_at).toLocaleString("zh-CN") : user.status === "invited" ? "尚未激活" : "尚未登录";

  const teacherColumns: TableColumnsType<ManagedUser> = [
    { title: "姓名", dataIndex: "display_name", width: 160 },
    { title: "手机号", dataIndex: "phone_masked", width: 150, render: (value?: string) => value || "-" },
    { title: "教职工号", dataIndex: "employee_no", width: 140, render: (value?: string) => value || "-" },
    { title: "工作职责", dataIndex: "roles", render: (items: string[]) => items.map((item) => roleLabels[item]).filter(Boolean).join("、") || "-" },
    { title: "状态", dataIndex: "status", width: 100, render: (status: string) => <StatusTag tone={status === "active" ? "success" : status === "invited" ? "warning" : "neutral"}>{statusLabel(status)}</StatusTag> },
    { title: "最近使用", key: "last_login_at", width: 175, render: (_, user) => recentUsage(user) },
    { title: "操作", key: "actions", width: 170, render: (_, item) => renderActions(item) }
  ];
  const administratorColumns: TableColumnsType<ManagedUser> = [
    { title: "姓名", dataIndex: "display_name", width: 160 },
    { title: "手机号", dataIndex: "phone_masked", width: 150, render: (value?: string) => value || "-" },
    { title: "教职工号", dataIndex: "employee_no", width: 140, render: (value?: string) => value || "-" },
    { title: "所属学校", dataIndex: "school_id", render: (schoolID?: string) => schoolID ? schoolNames.get(schoolID) ?? "学校信息已变更" : "未标明学校" },
    { title: "状态", dataIndex: "status", width: 100, render: (status: string) => <StatusTag tone={status === "active" ? "success" : status === "invited" ? "warning" : "neutral"}>{statusLabel(status)}</StatusTag> },
    { title: "最近使用", key: "last_login_at", width: 175, render: (_, user) => recentUsage(user) },
    { title: "操作", key: "actions", width: 170, render: (_, item) => renderActions(item) }
  ];

  async function reissueActivation(user: ManagedUser) {
    setUpdatingUserID(user.id);
    try {
      const result = await reissueManagedUserActivation(user.id);
      const activationURL = `${window.location.origin}${result.activation.path}`;
      modal.success({
        title: `已为${user.display_name}重新生成邀请`,
        width: 560,
        content: <div><p>此前的邀请链接已失效。请通过可信渠道发给本人，新链接将在 {new Date(result.activation.expires_at).toLocaleString("zh-CN")} 失效。</p><Input readOnly value={activationURL} addonAfter={<Button type="link" onClick={() => void navigator.clipboard.writeText(activationURL).then(() => message.success("激活链接已复制"))}>复制</Button>} /></div>
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
        content: <div><p>旧恢复链接已失效。请核对本人身份后通过可信渠道发送，新链接将在 {new Date(result.recovery.expires_at).toLocaleString("zh-CN")} 失效。</p><Input readOnly value={recoveryURL} addonAfter={<Button type="link" onClick={() => void navigator.clipboard.writeText(recoveryURL).then(() => message.success("恢复链接已复制"))}>复制</Button>} /></div>
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
    if (nextMode === "administrator") form.setFieldValue("role_code", "school_admin");
    setOpen(true);
  }

  async function submit(values: { phone: string; employee_no?: string; display_name: string; role_code: string; school_id: string; class_ids?: string[] }) {
    setSaving(true);
    try {
      const selectedClasses = classes.filter((item) => (values.class_ids ?? []).includes(item.id));
      const schoolIds = [...new Set(selectedClasses.map((item) => item.school_id))];
      const result = await createManagedUser({
        phone: values.phone,
        employee_no: values.employee_no,
        display_name: values.display_name,
        role_code: createMode === "administrator" ? "school_admin" : values.role_code,
        school_id: schoolIds.length === 1 ? schoolIds[0] : values.school_id,
        class_ids: values.role_code === "teacher" ? values.class_ids ?? [] : []
      });
      message.success(createMode === "administrator" ? "管理员邀请已创建" : "教师邀请已创建");
      setOpen(false);
      if (result.activation) {
        const activationURL = `${window.location.origin}${result.activation.path}`;
        modal.success({
          title: "激活邀请已生成",
          width: 560,
          content: <div><p>请通过可信渠道发给本人。链接将在 {new Date(result.activation.expires_at).toLocaleString("zh-CN")} 失效，且只能使用一次。</p><Input readOnly value={activationURL} addonAfter={<Button type="link" onClick={() => void navigator.clipboard.writeText(activationURL).then(() => message.success("激活链接已复制"))}>复制</Button>} /></div>
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
  return <div className="member-management-page">
    <section className="member-management-heading">
      <div><h1>{isTeacherView ? "人员与访问管理" : "管理员账号"}</h1><p>{isTeacherView ? "创建教师邀请并管理教学、阅卷和复核访问权限。" : "每所学校建议至少配置 2 位管理员，便于工作交接和应急接替。"}</p></div>
      <div><Button icon={<RefreshCw size={15} />} loading={loading} onClick={() => void load()}>刷新</Button>{isTeacherView ? <Button type="primary" icon={<Plus size={15} />} onClick={() => openCreate("teacher")}>新增教师</Button> : canCreateAdministrator ? <Button type="primary" icon={<Plus size={15} />} onClick={() => openCreate("administrator")}>新增管理员</Button> : null}</div>
    </section>
    <section className="member-table-section">
      <Tabs activeKey={view} onChange={(key) => { setView(key as MemberView); setQuery(""); }} items={[
        { key: "teachers", label: `阅卷教师（${teacherUsers.length}）` },
        { key: "administrators", label: `管理员账号（${administratorUsers.length}）` }
      ]} />
      {!isTeacherView ? <Alert className="member-administrator-alert" showIcon type={schoolsNeedingBackup.length ? "warning" : "success"} message={schoolsNeedingBackup.length ? "管理员配置需要完善" : "管理员配置正常"} description={administratorNotice} /> : null}
      <div className="member-management-filters"><Input.Search allowClear value={query} placeholder="搜索姓名、手机号或工号" onChange={(event) => setQuery(event.target.value)} /><span>共 {visible.length} 人</span></div>
      <ResponsiveTable className="dense-data-table" rowKey="id" size="small" columns={isTeacherView ? teacherColumns : administratorColumns} dataSource={visible} pagination={{ pageSize: 20 }} scroll={{ x: 930 }} />
    </section>
    <Modal title={createMode === "administrator" ? "新增学校管理员" : "新增教师"} open={open} footer={null} destroyOnHidden onCancel={() => setOpen(false)}>
      <Form form={form} layout="vertical" onFinish={(values) => void submit(values)}>
        <div className="form-grid compact-form-grid">
          <Form.Item name="display_name" label="姓名" rules={[{ required: true, message: "请输入姓名" }]}><Input /></Form.Item>
          <Form.Item name="phone" label="手机号" rules={[{ required: true, message: "请输入教师手机号" }, { pattern: /^(?:\+?86)?1\d{10}$/, message: "请输入有效的中国大陆手机号" }]}><Input inputMode="tel" autoComplete="tel" /></Form.Item>
          <Form.Item name="employee_no" label="教职工号（可选）"><Input autoComplete="off" /></Form.Item>
          {createMode === "teacher" ? <Form.Item name="role_code" label="工作职责" rules={[{ required: true, message: "请选择工作职责" }]}><Select options={roles.filter((role) => teacherRoles.includes(role.code)).map((role) => ({ value: role.code, label: roleLabels[role.code] ?? role.name }))} /></Form.Item> : <Form.Item name="role_code" hidden><Input /></Form.Item>}
          <Form.Item name="school_id" label="所属学校" rules={[{ required: true, message: "请选择所属学校" }]}><Select options={schools.map((school) => ({ value: school.id, label: school.name }))} /></Form.Item>
        </div>
        {createMode === "teacher" && selectedRole === "teacher" ? <Form.Item name="class_ids" label="关联班级（可选）"><Select mode="multiple" optionFilterProp="label" options={classes.map((item) => ({ value: item.id, label: `${grades.find((grade) => grade.id === item.grade_id)?.name ?? "未分年级"} · ${item.name}` }))} /></Form.Item> : createMode === "teacher" && selectedRole === "grader" ? <p>阅卷范围由具体阅卷任务决定。</p> : createMode === "teacher" && selectedRole === "arbitrator" ? <p>复核范围由具体复核任务决定。</p> : null}
        <Button type="primary" htmlType="submit" loading={saving}>{createMode === "administrator" ? "创建管理员邀请" : "创建并生成邀请"}</Button>
      </Form>
    </Modal>
  </div>;
}
