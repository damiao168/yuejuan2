import { describe, expect, it } from "vitest";
import type { ReviewTask } from "../../../api/review";
import { appendTaskPage, followingVisibleActionableTask } from "./taskPaging";

describe("review task paging", () => {
  it("combines three pages into the complete 120 task queue", () => {
    const task = (id: number) => ({ id: `task-${id}` }) as ReviewTask;
    let tasks: ReviewTask[] = [];
    tasks = appendTaskPage(tasks, Array.from({ length: 50 }, (_, index) => task(index)));
    tasks = appendTaskPage(tasks, Array.from({ length: 50 }, (_, index) => task(index + 50)));
    tasks = appendTaskPage(tasks, Array.from({ length: 20 }, (_, index) => task(index + 100)));
    expect(tasks).toHaveLength(120);
    expect(tasks[tasks.length - 1]?.id).toBe("task-119");
    expect(appendTaskPage(tasks, [task(80), task(120), task(120)])).toHaveLength(121);
  });

  it("refreshes a task that is transferred while later pages load", () => {
    const existing = { id: "task-49", assigned_to: "grader-a", status: "assigned", revision: 1 } as ReviewTask;
    const transferred = { ...existing, assigned_to: "grader-b", status: "pending", revision: 2 } as ReviewTask;
    const merged = appendTaskPage([existing], [transferred, transferred]);
    expect(merged).toEqual([transferred]);
  });

  it("finds the next actionable task after page 50 without wrapping prematurely", () => {
    const task = (id: number) => ({
      id: `task-${id}`,
      status: id === 50 || id === 119 ? "assigned" : "completed",
      exam_id: "exam-1",
      anonymous_code: `student-${id}`,
      question_no: "1",
      source: "human"
    }) as ReviewTask;
    const all = Array.from({ length: 120 }, (_, index) => task(index));
    const options = { canManageTasks: false, initialExamId: "exam-1", keyword: "", taskFilter: "active" };
    const firstPage = all.slice(0, 50);
    expect(followingVisibleActionableTask(firstPage, "task-49", options, false)).toBeUndefined();
    const twoPages = appendTaskPage(firstPage, all.slice(50, 100));
    expect(followingVisibleActionableTask(twoPages, "task-49", options, false)?.id).toBe("task-50");
    expect(followingVisibleActionableTask(all, "task-50", options, false)?.id).toBe("task-119");
    expect(followingVisibleActionableTask(all, "task-119", options, true)?.id).toBe("task-50");
    // The submitted anchor is hidden by the active filter, yet its position
    // still determines where paging resumes.
    expect(followingVisibleActionableTask(all, "task-49", options, false)?.id).toBe("task-50");
  });
});
