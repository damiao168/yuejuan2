import { describe, expect, it } from "vitest";
import { defaultExamWorkspaceSection } from "./defaultExamWorkspaceRoute";

describe("defaultExamWorkspaceSection", () => {
  it.each([["draft", "settings"], ["configured", "settings"], ["ready", "settings"], ["collecting", "capture"], ["grading", "grading"], ["reviewing", "grading"], ["published", "overview"]])("routes %s exams to %s", (status, expected) => {
    expect(defaultExamWorkspaceSection(status)).toBe(expected);
  });
});
