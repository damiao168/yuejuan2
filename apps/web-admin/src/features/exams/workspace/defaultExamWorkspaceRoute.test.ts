import { describe, expect, it } from "vitest";
import { defaultExamWorkspaceSection } from "./defaultExamWorkspaceRoute";

describe("defaultExamWorkspaceSection", () => {
  it.each([
    ["draft", "students"], ["configured", "students"],
    ["ready", "capture"], ["preparation_confirmed", "capture"], ["collecting", "capture"], ["processing", "capture"],
    ["grading", "grading"], ["reviewing", "grading"],
    ["finalized", "scores"], ["published", "scores"], ["archived", "scores"],
    ["future_status", "students"]
  ])("opens the next working section for %s exams", (status, section) => {
    expect(defaultExamWorkspaceSection(status)).toBe(section);
  });
});
