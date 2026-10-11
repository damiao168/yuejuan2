import { describe, expect, it } from "vitest";
import type { AiGrade, ReviewTask } from "../../../api/review";
import { aiPointAdoption } from "./aiAdoption";
import { assignmentGroupKey, buildAssignmentPlan } from "./assignmentPlan";
import { followingVisibleActionableTask, visibleTasks } from "./taskPaging";

describe("teacher product decisions", () => {
  const points = [{ id: "p1", description: "步骤", score: 3, required: false }, { id: "p2", description: "结论", score: 2, required: false }];
  const grade = { rubric_version: "v1", suggested_score: 3, matched_points: [{ code: "p1", score: 3 }], missing_points: [{ code: "p2", score: 2 }] } as AiGrade;
  it("adopts complete point IDs only when the frozen version and sum agree", () => {
    expect(aiPointAdoption(grade, points, "v1")).toEqual({ p1: 3, p2: 0 });
    expect(aiPointAdoption(grade, points, "v2")).toBeNull();
    expect(aiPointAdoption({ ...grade, suggested_score: 4 }, points, "v1")).toBeNull();
    expect(aiPointAdoption({ ...grade, missing_points: [] }, points, "v1")).toBeNull();
    expect(aiPointAdoption({ ...grade, matched_points: [{ code: "unknown", label: "未绑定点", score: 3 }] }, points, "v1")).toBeNull();
    expect(aiPointAdoption({ ...grade, missing_points: [{ code: "p1", label: "重复点", score: 2 }] }, points, "v1")).toBeNull();
  });
  const task = (id: string, exam: string, question: string, status = "assigned") => ({ id, exam_id: exam, exam_name: exam === "exam-a" ? "期中" : "期末", subject: "数学", question_id: question, question_no: question, anonymous_code: id, source: "human", status, revision: 3 }) as ReviewTask;
  it("continues in the same exam and question and filters exam names", () => {
    const tasks = [task("a1", "exam-a", "1"), task("b1", "exam-b", "1"), task("a2", "exam-a", "1"), task("a3", "exam-a", "2")];
    const options = { canManageTasks: false, initialExamId: "", keyword: "", taskFilter: "active" };
    expect(visibleTasks(tasks, options).map((item) => item.id)).toEqual(["a1", "a2", "a3", "b1"]);
    expect(followingVisibleActionableTask(tasks, "a1", options, false)?.id).toBe("a2");
    expect(visibleTasks(tasks, { ...options, keyword: "期中", questionId: "2" }).map((item) => item.id)).toEqual(["a3"]);
  });
  it("balances explicit per-question graders and keeps skipped tasks and revisions", () => {
    const tasks = [task("1", "exam-a", "1"), task("2", "exam-a", "1"), task("3", "exam-a", "1"), task("4", "exam-a", "2"), task("5", "exam-b", "1")];
    const plan = buildAssignmentPlan(tasks, { [assignmentGroupKey(tasks[0])]: ["g1", "g2", "g1"], [assignmentGroupKey(tasks[3])]: ["g2"] });
    expect(plan.map((batch) => [batch.graderId, batch.tasks.map((item) => item.id)])).toEqual([["g1", ["1", "3"]], ["g2", ["2", "4"]]]);
    expect(plan.flatMap((batch) => batch.tasks).every((item) => item.revision === 3)).toBe(true);
    expect(plan.flatMap((batch) => batch.tasks).some((item) => item.id === "5")).toBe(false);
  });
});
