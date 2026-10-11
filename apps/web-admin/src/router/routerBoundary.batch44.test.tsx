import { describe, expect, it } from "vitest";
import type { SessionUser } from "../auth/session";
import { canonicalPathFromPath, pathForExperience } from "./experience";
import { routeGroups } from "./routes";

function user(roles: string[]): SessionUser {
  return {
    id: "user-44", username: "tester", displayName: "Tester", name: "Tester", role: roles[0], roles,
    tenant: "school-44", school: "School 44", currentExam: "", publicComputer: false, permissions: [],
    organizationScope: { resolved: false, tenantWide: false, schoolIds: [], gradeIds: [], classIds: [] }
  };
}

describe("第44批公开路由边界", () => {
  it("保持平台、学校和教师各自的菜单前缀顺序", () => {
    expect(routeGroups("admin", user(["school_admin", "platform_admin"])).slice(0, 4))
      .toEqual(["工作台", "平台管理", "系统管理", "设置"]);
    expect(routeGroups("admin", user(["school_admin"])).slice(0, 6))
      .toEqual(["工作台", "成员管理", "考试管理", "题库与组卷", "阅卷与成绩", "设置"]);
    expect(routeGroups("teacher", user(["teacher", "school_admin"])))
      .toEqual(["工作台", "阅卷工作", "教学工作"]);
    expect(routeGroups("auditor", user(["auditor"]))).toEqual(["工作台", "审计"]);
    expect(routeGroups("student", user(["student"]))).toEqual(["工作台"]);
  });

  // 根路径、多斜杠和身份入口的现有返回值不同；消除正则回溯时不能顺带合并这些结果。
  it.each([
    ["", "/dashboard"],
    ["/", "/"],
    ["///", ""],
    ["/admin///", "/dashboard"],
    ["/teacher/grading////?exam=44#detail", "/grading"],
    ["grading////", "/grading"],
    ["/reports/#detail", "/reports"],
    ["/reports///extra", "/reports///extra"]
  ])("保持路径%s的实际规范结果", (input, expected) => {
    expect(canonicalPathFromPath(input)).toBe(expected);
  });

  it("身份切换仍保留原查询段和其后的片段", () => {
    expect(pathForExperience("/teacher/grading///?exam=44#detail", "admin"))
      .toBe("/admin/grading?exam=44#detail");
  });
});
