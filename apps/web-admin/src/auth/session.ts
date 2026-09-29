import type { AuthUser } from "../api/auth";

export interface SessionUser {
  id: string;
  username: string;
  displayName: string;
  name: string;
  role: string;
  roles: string[];
  tenant: string;
  school: string;
  currentExam: string;
  permissions: string[];
  publicComputer: boolean;
  organizationScope: {
    resolved: boolean;
    tenantWide: boolean;
    schoolIds: string[];
    gradeIds: string[];
    classIds: string[];
  };
}

export function hasEveryPermission(user: SessionUser | null, permissions: string[]): boolean {
  if (!user) {
    return false;
  }
  return permissions.every((permission) => user.permissions.includes(permission));
}

export function hasAnyPermission(user: SessionUser | null, permissions: string[]): boolean {
  return Boolean(user && permissions.some((permission) => user.permissions.includes(permission)));
}

export function productIdentityLabel(user: SessionUser): string {
  if (user.roles.includes("platform_admin")) return "平台管理";
  if (user.roles.includes("tenant_admin")) return "机构管理员";
  if (user.roles.includes("school_admin")) return "学校管理员";
  if (user.roles.includes("teacher")) return "教师工作台";
  if (user.roles.includes("grader")) return "阅卷工作台";
  if (user.roles.includes("arbitrator")) return "仲裁工作台";
  if (user.roles.includes("auditor")) return "审计工作台";
  if (user.roles.includes("student")) return "学生端";
  return "工作台";
}

export function sessionFromAuthUser(user: AuthUser): SessionUser {
  const role = user.roles[0] ?? "user";
  const displayName = normalizeDisplayName(user.display_name);
  return {
    id: user.id,
    username: user.username,
    displayName,
    name: displayNameOrUsername(user.display_name, user.username),
    role,
    roles: user.roles,
    tenant: user.tenant_code,
    school: schoolLabelFromScope(user.data_scope, user.tenant_code),
    currentExam: "未选择考试",
    permissions: user.permissions,
    publicComputer: user.current_session_type === "public_device",
    // 缺失组织范围表示尚未解析，不能把空数组解释为机构内全部可见。
    organizationScope: {
      resolved: Boolean(user.organization_scope),
      tenantWide: user.organization_scope?.tenant_wide ?? false,
      schoolIds: user.organization_scope?.school_ids ?? [],
      gradeIds: user.organization_scope?.grade_ids ?? [],
      classIds: user.organization_scope?.class_ids ?? []
    }
  };
}

export function displayNameOrUsername(displayName: unknown, username: string): string {
  return normalizeDisplayName(displayName) || username.trim() || "未命名用户";
}

function normalizeDisplayName(value: unknown): string {
  if (typeof value !== "string") {
    return "";
  }
  const trimmed = value.trim();
  return trimmed && !/^[?？]+$/.test(trimmed) && !trimmed.includes("\uFFFD") ? trimmed : "";
}

function schoolLabelFromScope(scope: Record<string, unknown>, tenantCode: string): string {
  const direct = scope["school_name"];
  if (typeof direct === "string" && direct.trim()) {
    return direct;
  }
  for (const value of Object.values(scope)) {
    if (!value || typeof value !== "object" || Array.isArray(value)) {
      continue;
    }
    const nested = value as Record<string, unknown>;
    const nestedSchool = nested["school_name"];
    if (typeof nestedSchool === "string" && nestedSchool.trim()) {
      return nestedSchool;
    }
  }
  return tenantCode || "当前机构";
}
