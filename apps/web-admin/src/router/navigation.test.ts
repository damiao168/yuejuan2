import { describe, expect, it } from "vitest";
import type { SessionUser } from "../auth/session";
import { examWorkspaceNavigationPath, hasPathAccess, legacyRedirectForPath } from "./navigation";

function user(roles: string[], permissions: string[]): SessionUser {
  return {
    id: "user-1",
    username: "tester",
    displayName: "Tester",
    name: "Tester",
    role: roles[0] ?? "user",
    roles,
    tenant: "school-1",
    school: "School 1",
    currentExam: "",
    publicComputer: false,
    permissions,
    organizationScope: { resolved: false, tenantWide: false, schoolIds: [], gradeIds: [], classIds: [] }
  };
}

describe("router compatibility and access", () => {
  const administrator = user(["school_admin"], ["exam:manage"]);

  it("redirects a legacy hash deep link without losing params or query", () => {
    expect(legacyRedirectForPath("/exams/exam%201/grading?task=task-1", administrator))
      .toBe("/admin/exams/exam%201/grading?task=task-1");
    expect(legacyRedirectForPath("/", administrator)).toBe("/admin/dashboard");
  });

  it("does not redirect an already scoped deep link", () => {
    expect(legacyRedirectForPath("/admin/exams/exam-1/overview", administrator)).toBeNull();
  });

  it("keeps permission and experience guards on deep links", () => {
    const grader = user(["grader"], ["review:work"]);

    expect(hasPathAccess(administrator, "/admin/exams/exam-1/overview")).toBe(true);
    expect(hasPathAccess(grader, "/teacher/grading")).toBe(true);
    expect(hasPathAccess(grader, "/admin/exams/exam-1/overview")).toBe(false);
    expect(hasPathAccess(administrator, "/admin/exams/exam-1/not-a-section")).toBe(false);
  });
});

describe("exam module navigation ownership", () => {
  it.each([
    ["students", "/exams"], ["paper", "/exams"], ["questions", "/exams"],
    ["template", "/exams"], ["settings", "/exams"],
    ["capture", "/grading"], ["processing", "/grading"], ["grading", "/grading"],
    ["quality", "/arbitration"], ["scores", "/scores"], ["reports", "/reports"], ["appeals", "/appeals"]
  ])("highlights the owning module for %s", (section, expected) => {
    expect(examWorkspaceNavigationPath(section)).toBe(expected);
  });
});
