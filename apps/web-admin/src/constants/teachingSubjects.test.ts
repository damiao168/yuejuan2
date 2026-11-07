import { describe, expect, it } from "vitest";
import { canAssignSubjectTask } from "./teachingSubjects";

const teacher = { status: "active", roles: ["teacher"], teaching_subjects: ["mathematics"], school_id: "school-a" };
describe("按本场学科选择任务人员", () => {
  it("学科别名同科，学校和原岗位仍分别约束", () => {
    expect(canAssignSubjectTask(teacher, "数学", "teacher", "school-a")).toBe(true);
    expect(canAssignSubjectTask(teacher, "english", "teacher", "school-a")).toBe(false);
    expect(canAssignSubjectTask(teacher, "math", "teacher", "school-b")).toBe(false);
    expect(canAssignSubjectTask(teacher, "math", "grader", "school-a")).toBe(false);
    expect(canAssignSubjectTask({ ...teacher, status: "disabled" }, "math", "teacher")).toBe(false);
    expect(canAssignSubjectTask({ ...teacher, teaching_subjects: [] }, "math", "teacher")).toBe(false);
    expect(canAssignSubjectTask(teacher, undefined, "teacher")).toBe(false);
  });
  it("管理员兼任教师只豁免学科，不能替代岗位、启用状态或学校", () => {
    const admin = { ...teacher, roles: ["school_admin", "teacher"], teaching_subjects: [] };
    expect(canAssignSubjectTask(admin, "english", "teacher", "school-a")).toBe(true);
    expect(canAssignSubjectTask(admin, "english", "grader", "school-a")).toBe(false);
    expect(canAssignSubjectTask(admin, "english", "teacher", "school-b")).toBe(false);
    expect(canAssignSubjectTask({ ...admin, roles: ["school_admin"] }, "math", "teacher")).toBe(false);
  });
});
