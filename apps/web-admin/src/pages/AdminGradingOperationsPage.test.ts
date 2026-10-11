import { describe, expect, it } from "vitest";
import { isAdminGradingExam, isAdminGradingOperationExam } from "./AdminGradingOperationsPage";

describe("admin grading monitoring scope", () => {
  it.each(["grading", "reviewing"])("includes %s exams", (status) => {
    expect(isAdminGradingExam({ status })).toBe(true);
  });

  it.each(["draft", "configured", "ready", "collecting", "finalized", "published", "archived"])("excludes %s exams", (status) => {
    expect(isAdminGradingExam({ status })).toBe(false);
  });
});

describe("admin grading import entry scope", () => {
  it.each(["ready", "collecting", "grading", "reviewing"])("keeps %s exams reachable from grading management", (status) => {
    expect(isAdminGradingOperationExam({ status })).toBe(true);
  });

  it.each(["draft", "configured", "finalized", "published", "archived"])("keeps %s exams in preparation or results modules", (status) => {
    expect(isAdminGradingOperationExam({ status })).toBe(false);
  });
});
