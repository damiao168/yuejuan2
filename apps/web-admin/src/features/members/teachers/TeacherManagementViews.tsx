import { Alert, Button, Form, Input, Select } from "antd";
import { Plus, RefreshCw } from "lucide-react";
import type { School } from "../../../api/org";
import type { ManagedUser } from "../../../api/users";
import { StatusTag } from "../../../components/StatusTag";

export const teacherRoles = new Set(["teacher", "grader", "arbitrator"]);
// 两种资格都按具体任务授权；新增人员不再配置教学班级或全校成绩范围。
export const taskStaffRoleCodes = ["grader", "arbitrator"];
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
    <div><h1>{isTeacherView ? "人员与权限" : "学校管理员"}</h1><p>{isTeacherView ? "管理教师登录账号，阅卷与复核任务在阅卷管理中分配。" : "每所学校建议至少配置 2 位管理员，便于工作交接和应急接替。"}</p></div>
    <div><Button icon={<RefreshCw size={15} />} loading={loading} onClick={onRefresh}>刷新</Button>{createButton}</div>
  </section>;
}

export function MemberCreationFields({ schools }: Readonly<{
  schools: readonly School[];
}>) {
  return <>
    <div className="form-grid compact-form-grid">
      <Form.Item name="display_name" label="姓名" rules={[{ required: true, message: "请输入姓名" }]}><Input /></Form.Item>
      <Form.Item name="username" label="用户名" rules={[{ required: true, whitespace: true, message: "请输入登录用户名" }, { max: 64, message: "用户名最多 64 个字符" }]}><Input autoComplete="off" maxLength={64} placeholder="用于用户名和密码登录" /></Form.Item>
      <Form.Item name="phone" label="手机号（可选）" rules={[{ pattern: /^(?:\+?86)?1\d{10}$/, message: "请输入有效的中国大陆手机号" }]}><Input inputMode="tel" autoComplete="tel" /></Form.Item>
      <Form.Item name="employee_no" label="教职工号（可选）"><Input autoComplete="off" /></Form.Item>
      <Form.Item name="school_id" label="所属学校" rules={[{ required: true, message: "请选择所属学校" }]}><Select options={schools.map((school) => ({ value: school.id, label: school.name }))} /></Form.Item>
    </div>
  </>;
}
