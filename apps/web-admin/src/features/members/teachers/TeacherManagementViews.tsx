import { Alert, Button, Form, Input, Select } from "antd";
import { Plus, RefreshCw } from "lucide-react";
import type { Grade, School, SchoolClass } from "../../../api/org";
import type { AssignableRole, ManagedUser } from "../../../api/users";
import { StatusTag } from "../../../components/StatusTag";

export const roleLabels: Record<string, string> = {
  tenant_admin: "机构管理员",
  school_admin: "学校管理员",
  teacher: "学科教师",
  grader: "阅卷教师",
  arbitrator: "仲裁/复核教师"
};

export const teacherRoles = new Set(["teacher", "grader", "arbitrator"]);
export type CreateMode = "teacher" | "administrator";
export function memberStatusLabel(status: string) {
  return ({ active: "正常", invited: "待激活", disabled: "已停用" } as Record<string, string>)[status] ?? "未知状态";
}

export function MemberStatus({ status }: Readonly<{ status: string }>) {
  switch (status) {
    case "active":
      return <StatusTag tone="success">已启用</StatusTag>;
    case "invited":
      return <StatusTag tone="warning">待激活</StatusTag>;
    case "disabled":
      return <StatusTag tone="neutral">已停用</StatusTag>;
    default:
      return <StatusTag tone="neutral">未知状态</StatusTag>;
  }
}

export function recentUsage(user: ManagedUser) {
  if (user.last_login_at) return new Date(user.last_login_at).toLocaleString("zh-CN");
  if (user.status === "invited") return "尚未激活";
  return "尚未登录";
}

function administratorNotice(schoolsNeedingBackup: ReadonlyArray<{ name: string; count: number }>, canCreateAdministrator: boolean) {
  let notice = "当前每所学校均有至少 2 位启用的学校管理员，系统不允许停用最后一位。";
  if (schoolsNeedingBackup.length) {
    const schoolCounts = schoolsNeedingBackup.map((item) => `${item.name}当前有 ${item.count} 位`).join("；");
    notice = `${schoolCounts}。建议至少保留 2 位启用的学校管理员，系统不允许停用最后一位。`;
  }
  if (!canCreateAdministrator) notice += " 如需新增、停用或恢复管理员，请联系机构管理员。";
  return notice;
}

export function AdministratorConfigurationNotice({ schoolsNeedingBackup, canCreateAdministrator }: Readonly<{
  schoolsNeedingBackup: ReadonlyArray<{ name: string; count: number }>;
  canCreateAdministrator: boolean;
}>) {
  const needsBackup = schoolsNeedingBackup.length > 0;
  return <Alert className="member-administrator-alert" showIcon type={needsBackup ? "warning" : "success"} message={needsBackup ? "管理员配置需要完善" : "管理员配置正常"} description={administratorNotice(schoolsNeedingBackup, canCreateAdministrator)} />;
}

export function MemberManagementHeading({ isTeacherView, canCreateAdministrator, loading, onRefresh, onCreate }: Readonly<{
  isTeacherView: boolean;
  canCreateAdministrator: boolean;
  loading: boolean;
  onRefresh: () => void;
  onCreate: (mode: CreateMode) => void;
}>) {
  let createButton = null;
  if (isTeacherView) {
    createButton = <Button type="primary" icon={<Plus size={15} />} onClick={() => onCreate("teacher")}>新增教师</Button>;
  } else if (canCreateAdministrator) {
    createButton = <Button type="primary" icon={<Plus size={15} />} onClick={() => onCreate("administrator")}>新增管理员</Button>;
  }
  return <section className="member-management-heading">
    <div><h1>{isTeacherView ? "人员与权限" : "学校管理员"}</h1><p>{isTeacherView ? "创建教师邀请并管理教学、阅卷和复核访问权限。" : "每所学校建议至少配置 2 位管理员，便于工作交接和应急接替。"}</p></div>
    <div><Button icon={<RefreshCw size={15} />} loading={loading} onClick={onRefresh}>刷新</Button>{createButton}</div>
  </section>;
}

function MemberRoleField({ mode, roles }: Readonly<{ mode: CreateMode; roles: readonly AssignableRole[] }>) {
  if (mode === "administrator") return <Form.Item name="role_code" hidden><Input /></Form.Item>;
  return <Form.Item name="role_codes" label="工作职责（可多选）" rules={[{ required: true, type: "array", min: 1, message: "请选择至少一项工作职责" }]}><Select mode="multiple" options={roles.filter((role) => teacherRoles.has(role.code)).map((role) => ({ value: role.code, label: roleLabels[role.code] ?? role.name }))} /></Form.Item>;
}

function MemberRoleScope({ mode, selectedRole, classes, grades }: Readonly<{
  mode: CreateMode;
  selectedRole: string[] | undefined;
  classes: readonly SchoolClass[];
  grades: readonly Grade[];
}>) {
  if (mode !== "teacher") return null;
  if (selectedRole?.includes("teacher")) {
      return <Form.Item name="class_ids" label="关联班级（可选）"><Select mode="multiple" optionFilterProp="label" options={classes.map((item) => ({ value: item.id, label: `${grades.find((grade) => grade.id === item.grade_id)?.name ?? "未分年级"} · ${item.name}` }))} /></Form.Item>;
  }
  return <p>阅卷和复核范围由具体分配的任务决定。</p>;
}

// 表单实例和职责选择仍由页面持有，这些顶层组件只展示字段。
export function MemberCreationFields({ mode, selectedRole, roles, schools, classes, grades }: Readonly<{
  mode: CreateMode;
  selectedRole: string[] | undefined;
  roles: readonly AssignableRole[];
  schools: readonly School[];
  classes: readonly SchoolClass[];
  grades: readonly Grade[];
}>) {
  return <>
    <div className="form-grid compact-form-grid">
      <Form.Item name="display_name" label="姓名" rules={[{ required: true, message: "请输入姓名" }]}><Input /></Form.Item>
      <Form.Item name="phone" label="手机号" rules={[{ required: true, message: "请输入教师手机号" }, { pattern: /^(?:\+?86)?1\d{10}$/, message: "请输入有效的中国大陆手机号" }]}><Input inputMode="tel" autoComplete="tel" /></Form.Item>
      <Form.Item name="employee_no" label="教职工号（可选）"><Input autoComplete="off" /></Form.Item>
      <MemberRoleField mode={mode} roles={roles} />
      <Form.Item name="school_id" label="所属学校" rules={[{ required: true, message: "请选择所属学校" }]}><Select options={schools.map((school) => ({ value: school.id, label: school.name }))} /></Form.Item>
    </div>
    <p>学科教师可查看关联班级的教学信息；阅卷教师处理分配的阅卷任务；仲裁/复核教师处理分配的复核任务。同一人可兼任，不会因此获得全校成绩访问权。模型密钥管理仍需独立的平台权限。</p>
    <MemberRoleScope mode={mode} selectedRole={selectedRole} classes={classes} grades={grades} />
  </>;
}
