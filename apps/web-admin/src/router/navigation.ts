import type { SessionUser } from "../auth/session";
import {
  canonicalPathFromPath,
  defaultExperience,
  experienceFromPath,
  hasExperienceAccess,
  pathForExperience
} from "./experience";
import {
  examWorkspaceFromPath,
  hasExamWorkspaceSectionAccess,
  hasRouteAccess,
  notFoundRoute,
  routeFromPath
} from "./routes";

export function normalizeEntryPath(path: string): string {
  return path === "/" || path === "" ? "/dashboard" : path;
}

export function examWorkspaceNavigationPath(section: string): string {
  // 学生答卷导入归入考试列表的步骤；阅卷和成绩仍选中各自的业务模块。
  if (section === "grading") return "/grading";
  if (section === "quality") return "/arbitration";
  if (section === "scores") return "/scores";
  if (section === "reports") return "/reports";
  if (section === "appeals") return "/appeals";
  return "/exams";
}

export function legacyRedirectForPath(path: string, user: SessionUser): string | null {
  const normalized = normalizeEntryPath(path);
  if (experienceFromPath(normalized)) {
    return null;
  }
  return pathForExperience(normalized, defaultExperience(user));
}

export function hasPathAccess(user: SessionUser, path: string): boolean {
  const normalized = normalizeEntryPath(path);
  const experience = experienceFromPath(normalized) ?? defaultExperience(user);
  const canonicalPath = canonicalPathFromPath(normalized);
  const route = routeFromPath(canonicalPath);
  const workspace = examWorkspaceFromPath(canonicalPath);

  return route !== notFoundRoute
    && hasExperienceAccess(user, experience)
    && hasRouteAccess(user, route, experience)
    && (!workspace || hasExamWorkspaceSectionAccess(experience, workspace.section));
}
