import { describe, expect, it } from "vitest";
import roleMatrix from "../../../../contracts/authorization/role-matrix.json";
import type { SessionUser } from "../auth/session";
import { availableExperiences, defaultExperience, pathForExperience, type ProductExperience } from "./experience";
import { hasPathAccess, legacyRedirectForPath } from "./navigation";
import { createRouteRegistry, examWorkspaceSections, routeFromPath } from "./routes";

type HumanRole = "platform_admin" | "tenant_admin" | "school_admin" | "teacher" | "grader" | "arbitrator" | "auditor" | "student";
const allExperiences: ProductExperience[] = ["admin", "teacher", "auditor", "student"];
const schoolPages = ["/question-bank", "/dashboard", "/account/sessions", "/exams", "/exams/new", "/papers", "/capture", "/grading", "/arbitration", "/scores", "/reports", "/appeals", "/members/students", "/members/classes", "/members/teachers", "/organization/setup", "/grading/subjective-batches", "/audit"];

// 逐角色的期望独立于路由注册表：路径迁移不能把“平台有业务权限”误当作学校页面访问权。
const allowedPages: Record<HumanRole, readonly string[]> = {
  platform_admin: ["/dashboard", "/account/sessions", "/platform/getting-started", "/platform/schools", "/platform/model-config", "/system/models", "/system/status", "/audit"],
  tenant_admin: schoolPages,
  school_admin: [...schoolPages, "/ai-chat"],
  teacher: ["/question-bank", "/dashboard", "/account/sessions", "/grading", "/reports", "/appeals"],
  grader: ["/dashboard", "/account/sessions", "/grading"],
  arbitrator: ["/dashboard", "/account/sessions", "/arbitration"],
  auditor: ["/dashboard", "/account/sessions", "/audit"],
  student: ["/dashboard", "/account/sessions"]
};

function session(roles: HumanRole[]): SessionUser {
  return {
    id: "clean-route-review", username: "reviewer", displayName: "路由验证", name: "路由验证", role: roles[0], roles,
    tenant: "route-review-school", school: "路由验证学校", currentExam: "", publicComputer: false,
    permissions: [...new Set(roles.flatMap(role => roleMatrix[role].permissions))],
    organizationScope: { resolved: true, tenantWide: false, schoolIds: ["school-review"], gradeIds: [], classIds: [] }
  };
}

describe("无井号地址的完整身份边界", () => {
  const production = createRouteRegistry(false);

  it.each(Object.keys(allowedPages) as HumanRole[])("%s 的全部生产页面仍按身份和权限判定", role => {
    const user = session([role]);
    const experience = defaultExperience(user);
    expect(new Set(allowedPages[role]).size).toBe(allowedPages[role].length);
    for (const canonical of allowedPages[role]) expect(production.some(route => route.path === canonical), canonical).toBe(true);
    for (const route of production) {
      for (const requestedExperience of allExperiences) {
        const path = pathForExperience(route.path, requestedExperience);
        expect(path.includes("#")).toBe(false);
        expect(routeFromPath(path, production)).toBe(route);
        expect(hasPathAccess(user, path), `${role}: ${path}`).toBe(requestedExperience === experience && allowedPages[role].includes(route.path));
      }
    }
  });

  it.each(Object.keys(allowedPages) as HumanRole[])("%s 的考试总览和13个分区不因地址迁移扩大权限", role => {
    const user = session([role]);
    const permitsExams = role === "school_admin" || role === "tenant_admin";
    const pages = [...examWorkspaceSections.admin.map(section => `/exams/exam%20review/${section}`), "/exams/overview/session/exam%20review", "/exams/overview/standalone/exam%20review"];
    for (const canonical of pages) {
      for (const experience of allExperiences) {
        expect(hasPathAccess(user, pathForExperience(canonical, experience)), `${role}: ${experience}${canonical}`).toBe(permitsExams && experience === "admin");
      }
    }
  });

  it("混合身份可以切换可用入口，管理员身份仍必须由角色授予", () => {
    const user = session(["teacher", "grader", "arbitrator", "auditor", "student"]);
    expect(availableExperiences(user)).toEqual(["teacher", "auditor", "student"]);
    for (const role of ["teacher", "grader", "arbitrator", "auditor", "student"] as HumanRole[]) {
      const experience = defaultExperience(session([role]));
      for (const canonical of allowedPages[role]) expect(hasPathAccess(user, pathForExperience(canonical, experience))).toBe(true);
    }
    expect(hasPathAccess(user, "/admin/exams")).toBe(false);
    expect(hasPathAccess(user, "/admin/platform/model-config")).toBe(false);
  });

  it("查询参数和编码ID在旧地址跳转及身份切换后保持完整", () => {
    const path = "/exams/exam%20review/grading?task=task%2F1&q=%E6%95%B0%E5%AD%A6";
    expect(legacyRedirectForPath(path, session(["school_admin"]))).toBe(`/admin${path}`);
    expect(pathForExperience(`/admin${path}`, "teacher")).toBe(`/teacher${path}`);
  });
});
