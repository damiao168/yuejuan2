import { describe, expect, it } from "vitest";
import type { ReadinessCheck } from "../../../api/configuration";
import { preparationTaskGroups } from "./preparationModel";

describe("preparationTaskGroups", () => {
  it("keeps backend readiness checks grouped without inventing extra blockers", () => {
    const checks: ReadinessCheck[] = [
      { code: "students", label: "学生", passed: true, severity: "blocker", message: "ok", section: "students" },
      { code: "paper", label: "试卷", passed: false, severity: "blocker", message: "missing", section: "paper" },
      { code: "template", label: "答题卡", passed: true, severity: "blocker", message: "ok", section: "template" }
    ];
    const groups = preparationTaskGroups(checks, "人工复核");

    expect(groups.find((group) => group.key === "students")).toMatchObject({ passed: 1, total: 1, complete: true });
    expect(groups.find((group) => group.key === "materials")).toMatchObject({ passed: 1, total: 2, complete: false });
    expect(groups.find((group) => group.key === "checks")).toMatchObject({ passed: 2, total: 3, complete: false });
    expect(groups.find((group) => group.key === "grading")?.checks).toEqual([]);
  });
});
