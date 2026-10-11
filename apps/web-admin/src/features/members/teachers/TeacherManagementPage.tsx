import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Alert, App, Button, Form, Input, Modal, Select, Space, Tabs } from "antd";
import type { TableColumnsType } from "antd";
import { getUserErrorMessage } from "../../../api/client";
import { listClasses, listGrades, listSchools, type Grade, type School, type SchoolClass } from "../../../api/org";
import {
  createManagedUser,
  createCredentialRecovery,
  listAssignableRoles,
  listAllManagedUsers,
  reissueManagedUserActivation,
  updateManagedUserStatus,
  updateManagedUserAccess,
  type AssignableRole,
  type ManagedUser
} from "../../../api/users";
import type { SessionUser } from "../../../auth/session";
import { ErrorState, LoadingState } from "../../../components/PageState";
import { ResponsiveTable } from "../../../components/ResponsiveTable";
import { isStepUpCancelledError, useStepUp } from "../../../auth/stepUpContext";
import { AdministratorConfigurationNotice, MemberCreationFields, MemberManagementHeading, MemberStatus, recentUsage, roleLabels, teacherRoles, type CreateMode } from "./TeacherManagementViews";

type MemberView = "teachers" | "administrators";

function memberDialogPresentation(editingUser: ManagedUser | null, createMode: CreateMode) {
  // 编辑已有人员时，文案不能沿用上一次新增管理员时留下的模式。
  if (editingUser) return { title: `职责与班级 · ${editingUser.display_name}`, submitLabel: "验证身份并保存" };
  if (createMode === "administrator") return { title: "新增学校管理员", submitLabel: "创建管理员邀请" };
  return { title: "新增教师", submitLabel: "创建并生成邀请" };
}

export function TeacherManagementPage({ currentUser }: Readonly<{ currentUser: SessionUser }>) {
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
  const [editingUser, setEditingUser] = useState<ManagedUser | null>(null);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [updatingUserID, setUpdatingUserID] = useState("");
  const [error, setError] = useState("");
  const [form] = Form.useForm();
  const selectedRole = Form.useWatch("role_codes", form) as string[] | undefined;
  const selectedSchool = Form.useWatch("school_id", form) as string | undefined;
  const loadVersion = useRef(0);

  const load = useCallback(async () => {
    const version = ++loadVersion.current;
    setLoading(true);
    setError("");
    try {
      const [userResult, roleResult, schoolResult, gradeResult, classResult] = await Promise.all([
        listAllManagedUsers(),
        listAssignableRoles(),
        listSchools(),
        listGrades(),
        listClasses()
      ]);
      if (version !== loadVersion.current) return;
      setUsers(userResult.filter((user) => user.roles.some((role) => teacherRoles.has(role) || role === "school_admin")));
      setRoles(roleResult.roles);
      setSchools(schoolResult.schools);
      setGrades(gradeResult.grades);
      setClasses(classResult.classes);
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
    const accessButton = <Button type="link" disabled={loading || Boolean(error) || saving} onClick={() => openAccess(user)}>职责与班级</Button>;
    if (user.status === "invited") return <Space size={2}>{accessButton}<Button type="link" loading={updatingUserID === user.id} onClick={() => void reissueActivation(user)}>重新生成邀请</Button></Space>;
    return <Space size={2}>{accessButton}{user.status === "active" ? <Button type="link" loading={updatingUserID === user.id} onClick={() => void createRecovery(user)}>协助恢复</Button> : null}<Button type="link" danger={user.status === "active"} loading={updatingUserID === user.id} onClick={() => confirmStatusChange(user)}>{user.status === "active" ? "停用" : "恢复"}</Button></Space>;
  };

  const teacherColumns: TableColumnsType<ManagedUser> = [
    { title: "姓名", dataIndex: "display_name", width: 160 },
    { title: "手机号", dataIndex: "phone_masked", width: 150, render: (value?: string) => value || "-" },
    { title: "教职工号", dataIndex: "employee_no", width: 140, render: (value?: string) => value || "-" },
    { title: "工作职责", dataIndex: "roles", render: (items: string[]) => items.map((item) => roleLabels[item]).filter(Boolean).join("、") || "-" },
    { title: "状态", dataIndex: "status", width: 100, render: (status: string) => <MemberStatus status={status} /> },
    { title: "最近使用", key: "last_login_at", width: 175, render: (_, user) => recentUsage(user) },
    { title: "操作", key: "actions", width: 270, render: (_, item) => renderActions(item) }
  ];
  const administratorColumns: TableColumnsType<ManagedUser> = [
    { title: "姓名", dataIndex: "display_name", width: 160 },
    { title: "手机号", dataIndex: "phone_masked", width: 150, render: (value?: string) => value || "-" },
    { title: "教职工号", dataIndex: "employee_no", width: 140, render: (value?: string) => value || "-" },
    { title: "所属学校", dataIndex: "school_id", render: (schoolID?: string) => schoolID ? schoolNames.get(schoolID) ?? "学校信息已变更" : "未标明学校" },
    { title: "状态", dataIndex: "status", width: 100, render: (status: string) => <MemberStatus status={status} /> },
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
    setEditingUser(null);
    setCreateMode(nextMode);
    form.resetFields();
    if (nextMode === "administrator") form.setFieldValue("role_code", "school_admin");
    else form.setFieldValue("role_codes", ["teacher"]);
    if (schools.length === 1) form.setFieldValue("school_id", schools[0].id);
    setOpen(true);
  }

  function openAccess(user: ManagedUser) {
    setEditingUser(user);
    form.resetFields();
    form.setFieldValue("role_codes", [...user.roles]);
    form.setFieldValue("class_ids", [...(user.class_ids ?? [])]);
    form.setFieldValue("school_id", user.school_id);
    setOpen(true);
  }

  async function saveAccess(values: { role_codes: string[]; class_ids?: string[]; reason: string }) {
    if (!editingUser) return;
    const target = editingUser;
    setSaving(true);
    try {
      // 提交服务端返回的旧授权，不能在冲突后自动覆盖别人的修改；失败保留当前表单。
      const result = await runWithStepUp({
        reason: `修改“${target.display_name}”的工作职责与班级`,
        description: "此操作改变账号访问权限。保存成功后，该人员需要重新登录。",
        action: () => updateManagedUserAccess(target.id, {
          role_codes: values.role_codes,
          class_ids: values.role_codes.includes("teacher") ? values.class_ids ?? [] : [],
          expected_roles: target.roles,
          expected_class_ids: target.class_ids ?? [],
          reason: values.reason.trim()
        })
      });
      setUsers((current) => current.map((user) => user.id === result.user.id ? result.user : user));
      setOpen(false); setEditingUser(null);
      message.success("职责与班级已保存，请通知该人员重新登录");
    } catch (reason) {
      if (!isStepUpCancelledError(reason)) message.error(getUserErrorMessage(reason, "保存失败，当前填写内容已保留；如授权已变更，请刷新名单后重新核对。"));
    } finally { setSaving(false); }
  }

  async function submit(values: { phone: string; employee_no?: string; display_name: string; role_codes?: string[]; school_id: string; class_ids?: string[] }) {
    setSaving(true);
    try {
      const roleCodes = createMode === "administrator" ? ["school_admin"] : values.role_codes ?? [];
      const classIDs = roleCodes.includes("teacher") ? values.class_ids ?? [] : [];
      if (classIDs.some((id) => !classes.some((item) => item.id === id && item.school_id === values.school_id))) throw new Error("关联班级必须属于所选学校，请重新选择班级");
      const result = await createManagedUser({
        phone: values.phone,
        employee_no: values.employee_no,
        display_name: values.display_name,
        role_codes: roleCodes,
        school_id: values.school_id,
        class_ids: classIDs
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
  const dialog = memberDialogPresentation(editingUser, createMode);
  return <div className="member-management-page">
    <MemberManagementHeading isTeacherView={isTeacherView} canCreateAdministrator={canCreateAdministrator} loading={loading} onRefresh={() => void load()} onCreate={openCreate} />
    {error ? <Alert type="warning" showIcon message="名单刷新失败，当前显示此前读取的名单" description={error} /> : null}
    <section className="member-table-section">
      <Tabs activeKey={view} onChange={(key) => { setView(key as MemberView); setQuery(""); }} items={[
        { key: "teachers", label: `教学与阅卷人员（${teacherUsers.length}）` },
        { key: "administrators", label: `学校管理员（${administratorUsers.length}）` }
      ]} />
      {!isTeacherView ? <AdministratorConfigurationNotice schoolsNeedingBackup={schoolsNeedingBackup} canCreateAdministrator={canCreateAdministrator} /> : null}
      <div className="member-management-filters"><Input.Search allowClear value={query} placeholder="搜索姓名、手机号或工号" onChange={(event) => setQuery(event.target.value)} /><span>共 {visible.length} 人</span></div>
      <ResponsiveTable className="dense-data-table" rowKey="id" size="small" columns={isTeacherView ? teacherColumns : administratorColumns} dataSource={visible} pagination={{ pageSize: 20 }} scroll={{ x: 930 }} />
    </section>
    <Modal title={dialog.title} open={open} footer={null} destroyOnHidden maskClosable={!saving} closable={!saving} onCancel={() => { if (!saving) { setOpen(false); setEditingUser(null); } }}>
      <Form form={form} layout="vertical" disabled={saving} onFinish={(values) => editingUser ? void saveAccess(values) : void submit(values)}>
        {editingUser ? <>
          <Alert type="info" showIcon message={`所属学校：${schoolNames.get(editingUser.school_id ?? "") ?? "未标明学校"}`} description="各职责沿用原有范围。学科教师仅访问关联班级；阅卷和复核仅处理分配任务。保存后旧会话和设备凭据会失效，该人员需重新登录。" />
          <Form.Item name="role_codes" label="工作职责（可多选）" rules={[{ required: true, type: "array", min: 1, message: "至少保留一项职责" }]}><Select mode="multiple" options={roles.map((role) => ({ value: role.code, label: roleLabels[role.code] ?? role.name }))} /></Form.Item>
          {selectedRole?.includes("teacher") ? <Form.Item name="class_ids" label="关联班级"><Select mode="multiple" optionFilterProp="label" options={classes.filter((item) => item.school_id === editingUser.school_id).map((item) => ({ value: item.id, label: `${grades.find((grade) => grade.id === item.grade_id)?.name ?? "未分年级"} · ${item.name}` }))} /></Form.Item> : null}
          <Form.Item name="reason" label="变更说明" rules={[{ required: true, whitespace: true, message: "请说明职责或班级变更原因" }]}><Input /></Form.Item>
        </> : <MemberCreationFields mode={createMode} selectedRole={selectedRole} roles={roles} schools={schools} classes={classes.filter((item) => item.school_id === selectedSchool)} grades={grades} />}
        <Button type="primary" htmlType="submit" loading={saving}>{dialog.submitLabel}</Button>
      </Form>
    </Modal>
  </div>;
}
