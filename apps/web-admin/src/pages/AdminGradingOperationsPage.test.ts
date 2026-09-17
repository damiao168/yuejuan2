import { describe, expect, it } from "vitest";
import { isAdminGradingExam } from "./AdminGradingOperationsPage";

describe("admin grading monitoring scope", () => {
  it.each(["grading", "reviewing"])("includes %s exams", (status) => {
    expect(isAdminGradingExam({ status })).toBe(true);
  });

  it.each(["draft", "configured", "ready", "collecting", "finalized", "published", "archived"])("excludes %s exams", (status) => {
    expect(isAdminGradingExam({ status })).toBe(false);
  });
});
