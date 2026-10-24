import { describe, expect, it } from "vitest";
import type { ReviewTask } from "../../../api/review";
import { assignmentGroupKey, assignmentGroups, buildAssignmentPlan, buildQuotaAssignmentPlan } from "./assignmentPlan";

function task(id: string, question: string, overrides: Partial<ReviewTask> = {}): ReviewTask {
  return {
    id, exam_id: "exam-a", exam_name: "期中", subject: "math", question_id: `question-${question}`,
    question_no: question, revision: 3, ...overrides
  } as ReviewTask;
}

const identities = (plan: ReturnType<typeof buildAssignmentPlan>) => plan.map(batch => [batch.graderId, batch.tasks.map(item => item.id)]);

describe("按题安排教师", () => {
  it("统一教师一次接收第14至19题的所有学生答卷，并保留任务对象和修订号", () => {
    const tasks = Array.from({ length: 6 }, (_, index) => [task(`student-a-${index + 14}`, String(index + 14)), task(`student-b-${index + 14}`, String(index + 14))]).flat();
    const plan = buildAssignmentPlan(tasks, {}, ["teacher"]);
    expect(plan).toHaveLength(1);
    expect(plan[0].graderId).toBe("teacher");
    expect(plan[0].tasks).toEqual(tasks);
    plan[0].tasks.forEach((item, index) => expect(item).toBe(tasks[index]));
    expect(plan[0].tasks.every(item => item.revision === 3)).toBe(true);
  });

  it("统一两名教师跨第14至19题轮转，每题一份时仍按3加3均分并去重教师", () => {
    const tasks = [19, 14, 17, 15, 18, 16].map(no => task(String(no), String(no)));
    expect(identities(buildAssignmentPlan(tasks, {}, ["first", "second", "first"]))).toEqual([
      ["first", ["14", "16", "18"]], ["second", ["15", "17", "19"]]
    ]);
  });

  it("多题学生数量不同时继续统一轮转，教师任务量相差最多一份", () => {
    const tasks = [task("11-a", "11"), task("11-b", "11"), task("11-c", "11"), task("12-a", "12"), task("12-b", "12")];
    expect(identities(buildAssignmentPlan(tasks, {}, ["first", "second"]))).toEqual([
      ["first", ["11-a", "11-c", "12-b"]], ["second", ["11-b", "12-a"]]
    ]);
  });

  it("单题覆盖独立均分，空数组排除本题且两者都不消耗统一轮转位置", () => {
    const tasks = [task("14", "14"), task("15-a", "15"), task("15-b", "15"), task("15-c", "15"), task("16", "16"), task("17", "17")];
    const targets = { [assignmentGroupKey(tasks[1])]: ["specialist-a", "specialist-b", "specialist-a"], [assignmentGroupKey(tasks[4])]: [] };
    expect(identities(buildAssignmentPlan(tasks, targets, ["first", "second"]))).toEqual([
      ["first", ["14"]], ["specialist-a", ["15-a", "15-c"]], ["specialist-b", ["15-b"]], ["second", ["17"]]
    ]);
  });

  it("按考试、学科及自然题号排序，并提供简短题号和上下文标签", () => {
    const tasks = [
      task("b", "1", { exam_id: "exam-b" }), task("math-15", "15"), task("english", "1", { subject: "english" }),
      task("math-10", "10"), task("math-2", "2"), task("math-14", "14")
    ];
    const groups = assignmentGroups(tasks);
    expect(groups.map(group => group.tasks[0].id)).toEqual(["english", "math-2", "math-10", "math-14", "math-15", "b"]);
    expect(groups[1]).toMatchObject({ shortLabel: "第 2 题", contextLabel: "期中 · 数学", label: "期中 · 数学 · 第 2 题" });
    expect(assignmentGroups([...tasks].reverse()).map(group => group.key)).toEqual(groups.map(group => group.key));
    expect(tasks.map(item => item.id)).toEqual(["b", "math-15", "english", "math-10", "math-2", "math-14"]);
  });

  it("旧两参数调用保留每题内多教师均分和去重，未指定题目仍跳过", () => {
    const tasks = [task("a", "14"), task("b", "14"), task("c", "14"), task("skipped", "15")];
    const key = assignmentGroupKey(tasks[0]);
    expect(identities(buildAssignmentPlan(tasks, { [key]: ["first", "second", "first"] }))).toEqual([["first", ["a", "c"]], ["second", ["b"]]]);
    expect(buildAssignmentPlan(tasks, {})).toEqual([]);
    expect(buildAssignmentPlan([], {}, ["first"])).toEqual([]);
  });
});

describe("自定义教师分配数量", () => {
  it("第12题所有学生答卷可按指定数量分给多名教师，保持对象及修订号且不重复遗漏", () => {
    const tasks = Array.from({ length: 5 }, (_, index) => task(`student-${index}`, "12", { revision: index + 1 }));
    const plan = buildQuotaAssignmentPlan(tasks, [{ graderId: "first", count: 3 }, { graderId: "second", count: 2 }]);
    expect(identities(plan)).toEqual([["first", ["student-0", "student-1", "student-2"]], ["second", ["student-3", "student-4"]]]);
    const assigned = plan.flatMap(batch => batch.tasks);
    assigned.forEach((item, index) => expect(item).toBe(tasks[index]));
    expect(new Set(assigned.map(item => item.id)).size).toBe(tasks.length);
    expect(assigned.map(item => item.revision)).toEqual([1, 2, 3, 4, 5]);
  });

  it("多题按自然题号连续取指定份数，零份教师不产生空批次且输入顺序不变", () => {
    const tasks = [task("12-b", "12"), task("2", "2"), task("12-a", "12"), task("10", "10")];
    const quotas = [{ graderId: "first", count: 1 }, { graderId: "zero", count: 0 }, { graderId: "second", count: 3 }];
    expect(identities(buildQuotaAssignmentPlan(tasks, quotas))).toEqual([["first", ["2"]], ["second", ["10", "12-b", "12-a"]]]);
    expect(tasks.map(item => item.id)).toEqual(["12-b", "2", "12-a", "10"]);
    expect(quotas.map(quota => quota.count)).toEqual([1, 0, 3]);
  });

  it.each([-1, 0.5, NaN, Infinity, Number.MAX_SAFE_INTEGER + 1])("拒绝非法份数 %s", count => {
    expect(() => buildQuotaAssignmentPlan([task("a", "12")], [{ graderId: "teacher", count }])).toThrow("0 或正整数");
  });

  it("拒绝重复教师，即使重复项为零份", () => {
    expect(() => buildQuotaAssignmentPlan([task("a", "12")], [{ graderId: "teacher", count: 1 }, { graderId: "teacher", count: 0 }])).toThrow("同一教师不能重复");
  });

  it.each([0, 2])("数量合计为%s时拒绝遗漏或超额分配", count => {
    expect(() => buildQuotaAssignmentPlan([task("a", "12")], [{ graderId: "teacher", count }])).toThrow("合计必须等于待分配任务数（1 份）");
  });

  it("拒绝重复任务及空教师，空任务和空计划返回空批次", () => {
    const item = task("a", "12");
    expect(() => buildQuotaAssignmentPlan([item, item], [{ graderId: "teacher", count: 2 }])).toThrow("待分配任务重复");
    expect(() => buildQuotaAssignmentPlan([item], [{ graderId: "", count: 1 }])).toThrow("请选择自定义分配的教师");
    expect(buildQuotaAssignmentPlan([], [])).toEqual([]);
  });
});
