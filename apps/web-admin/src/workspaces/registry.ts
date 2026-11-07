import type { SessionUser } from "../auth/session";
import type { ProductExperience } from "../router/experience";

export type Workspace =
  | "platform"
  | "tenant_admin"
  | "school_admin"
  | "exam_owner"
  | "teacher"
  | "grader"
  | "arbitrator"
  | "auditor"
  | "student";

export interface WorkspaceDefinition {
  key: Workspace;
  label: string;
  purpose: string;
}

export const workspaceRegistry: Record<Workspace, WorkspaceDefinition> = {
  platform: { key: "platform", label: "平台控制台", purpose: "管理学校、平台服务、模型与审计" },
  tenant_admin: { key: "tenant_admin", label: "机构管理台", purpose: "管理租户内学校和学校级管理员" },
  school_admin: { key: "school_admin", label: "学校管理台", purpose: "组织学校考试并处理阻断事项" },
  exam_owner: { key: "exam_owner", label: "考试负责人", purpose: "配置和推进本人获授权的考试" },
  teacher: { key: "teacher", label: "教师工作台", purpose: "处理分配给本人的阅卷和反馈任务" },
  grader: { key: "grader", label: "阅卷工作台", purpose: "处理分配给本人的匿名阅卷任务" },
  arbitrator: { key: "arbitrator", label: "仲裁工作台", purpose: "处理分配给本人的评分差异" },
  auditor: { key: "auditor", label: "审计工作台", purpose: "查看授权范围内的操作审计" },
  student: { key: "student", label: "学生端", purpose: "查看本人授权的成绩与学习反馈" }
};

export function workspacesForExperience(user: SessionUser, experience: ProductExperience): Workspace[] {
  if (experience === "admin") {
    if (user.roles.includes("platform_admin")) return ["platform"];
    if (user.roles.includes("tenant_admin")) return ["tenant_admin"];
    if (user.roles.includes("school_admin")) return ["school_admin"];
    return [];
  }
  const workspaces: Workspace[] = [];
  if (user.roles.includes("teacher")) workspaces.push("teacher");
  if (user.roles.includes("grader")) workspaces.push("grader");
  if (user.roles.includes("arbitrator")) workspaces.push("arbitrator");
  if (experience === "auditor" && user.roles.includes("auditor")) workspaces.push("auditor");
  if (experience === "student" && user.roles.includes("student")) workspaces.push("student");
  return [...new Set(workspaces)];
}

export function workspaceForExperience(user: SessionUser, experience: ProductExperience): Workspace {
  const workspace = workspacesForExperience(user, experience)[0];
  if (!workspace) throw new Error("No workspace is assigned to the current identity");
  return workspace;
}

export function workspaceLabel(user: SessionUser, experience: ProductExperience) {
  return workspaceRegistry[workspaceForExperience(user, experience)].label;
}
