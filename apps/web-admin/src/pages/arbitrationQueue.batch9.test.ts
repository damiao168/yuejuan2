import { describe, expect, it } from "vitest";
import type { ArbitrationTask } from "../api/review";
import { arbitrationTaskAssignee, chooseArbitrationTaskId, filterArbitrationTasks } from "./arbitrationQueue.model";

function task(id: string, status: ArbitrationTask["status"] = "assigned", exam = "exam"): ArbitrationTask {
  return { id, status, exam_id: exam, anonymous_code: `CODE-${id}`, question_no: `Q-${id}` } as ArbitrationTask;
}

describe("batch9 arbitration queue pure decisions", () => {
  it("keeps personal scope ahead of administrative scope and preserves undefined for all", () => {
    expect(arbitrationTaskAssignee(true, "current", "all", "actor")).toBe("current");
    expect(arbitrationTaskAssignee(false, "current", "mine", "actor")).toBe("actor");
    expect(arbitrationTaskAssignee(false, "current", "all", "actor")).toBeUndefined();
  });
  it("chooses deep link, existing selection, first item, then empty without mutating the queue", () => {
    const tasks = [task("A"), task("B")];
    expect(chooseArbitrationTaskId(tasks, "deep", "B")).toBe("deep");
    expect(chooseArbitrationTaskId(tasks, "", "B")).toBe("B");
    expect(chooseArbitrationTaskId(tasks, "", "missing")).toBe("A");
    expect(chooseArbitrationTaskId([], "", "B")).toBe("");
    expect(tasks.map((entry) => entry.id)).toEqual(["A", "B"]);
  });
  it("combines exam, active status and case-insensitive name/code/question search", () => {
    const tasks = [task("A", "pending"), task("B", "assigned"), task("C", "submitted"), task("D", "assigned", "other")];
    expect(filterArbitrationTasks(tasks, { exam: "Exam Name" }, "exam", "active", " name ").map((entry) => entry.id)).toEqual(["A", "B"]);
    expect(filterArbitrationTasks(tasks, {}, "exam", "active", "code-b").map((entry) => entry.id)).toEqual(["B"]);
    expect(filterArbitrationTasks(tasks, {}, "", "submitted", "q-c").map((entry) => entry.id)).toEqual(["C"]);
    expect(filterArbitrationTasks(tasks, {}, "", "assigned", "other").map((entry) => entry.id)).toEqual(["D"]);
  });
});
