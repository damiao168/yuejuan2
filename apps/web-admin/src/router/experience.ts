import type { SessionUser } from "../auth/session";

export type ProductExperience = "admin" | "teacher" | "auditor" | "student";

export const ADMIN_ROLES = ["platform_admin", "tenant_admin", "school_admin"] as const;
export const TEACHER_ROLES = ["teacher", "grader", "arbitrator"] as const;
export const AUDITOR_ROLES = ["auditor"] as const;
export const STUDENT_ROLES = ["student"] as const;

const experiencePrefixes: Record<ProductExperience, string> = {
  admin: "/admin",
  teacher: "/teacher",
  auditor: "/auditor",
  student: "/student"
};

function hasRole(user: SessionUser, roles: readonly string[]) {
  return roles.some((role) => user.roles.includes(role));
}

// 产品入口按角色分配；持有某个业务权限本身不能获得管理端身份。
export function availableExperiences(user: SessionUser): ProductExperience[] {
  const experiences: ProductExperience[] = [];
  if (hasRole(user, ADMIN_ROLES)) experiences.push("admin");
  if (hasRole(user, TEACHER_ROLES)) experiences.push("teacher");
  if (hasRole(user, AUDITOR_ROLES)) experiences.push("auditor");
  if (hasRole(user, STUDENT_ROLES)) experiences.push("student");
  return experiences;
}

export function defaultExperience(user: SessionUser): ProductExperience {
  const experiences = availableExperiences(user);
  if (hasRole(user, ADMIN_ROLES)) return "admin";
  if (experiences.includes("teacher")) return "teacher";
  if (experiences.includes("auditor")) return "auditor";
  return experiences.includes("student") ? "student" : "admin";
}

export function hasExperienceAccess(user: SessionUser, experience: ProductExperience): boolean {
  return availableExperiences(user).includes(experience);
}

export function experienceFromPath(pathname: string): ProductExperience | null {
  const normalized = normalizePath(pathname);
  if (normalized === "/admin" || normalized.startsWith("/admin/")) return "admin";
  if (normalized === "/teacher" || normalized.startsWith("/teacher/")) return "teacher";
  if (normalized === "/auditor" || normalized.startsWith("/auditor/")) return "auditor";
  if (normalized === "/student" || normalized.startsWith("/student/")) return "student";
  return null;
}

export function canonicalPathFromPath(pathname: string): string {
  const normalized = normalizePath(pathname);
  const experience = experienceFromPath(normalized);
  if (!experience) return normalized;
  const canonical = normalized.slice(experiencePrefixes[experience].length);
  return normalizePath(canonical || "/dashboard");
}

export function pathForExperience(pathname: string, experience: ProductExperience): string {
  const canonical = canonicalPathFromPath(pathname);
  const queryIndex = pathname.indexOf("?");
  const query = queryIndex >= 0 ? pathname.slice(queryIndex) : "";
  return `${experiencePrefixes[experience]}${canonical}${query}`;
}

export function experienceLabel(experience: ProductExperience): string {
  if (experience === "admin") return "管理端";
  if (experience === "teacher") return "阅卷端";
  if (experience === "auditor") return "审计端";
  return "学生端";
}

function normalizePath(pathname: string): string {
  if (!pathname) return "/dashboard";
  const withoutQuery = pathname.split(/[?#]/, 1)[0];
  const normalized = withoutQuery.startsWith("/") ? withoutQuery : `/${withoutQuery}`;
  return normalized.length > 1 ? normalized.replace(/\/+$/, "") : normalized;
}
