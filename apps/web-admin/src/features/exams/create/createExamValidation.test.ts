import { describe, expect, it } from "vitest";
import { createSubjectDraft, initialCreateExamDraft } from "./createExamDraft";
import { validateCreateExam } from "./createExamValidation";

function validDraft() {
  return {
    ...initialCreateExamDraft("school-1", { id: "grade-1" } as never),
    name: "高二期中考试",
    examType: "midterm_exam",
    classIds: ["class-1"],
    subjects: [createSubjectDraft("math")]
  };
}

describe("lightweight exam creation validation", () => {
  it("allows a material-first draft without paper sections", () => {
    expect(validateCreateExam(validDraft())).toEqual([]);
  });

  it("still validates template sections when they exist", () => {
    const draft = validDraft();
    draft.subjects[0].sections = [{ id: "section-1", title: "全卷", questionType: "short_answer", questionCount: 9, scorePerQuestion: 10 }];
    expect(validateCreateExam(draft)).toContainEqual({ field: "subject.math.totalScore", message: "模板题目分值合计必须等于科目满分" });
  });

  it("requires an actual template only in template mode", () => {
    const draft = { ...validDraft(), creationMode: "template" as const, templateId: "" };
    expect(validateCreateExam(draft)).toContainEqual({ field: "templateId", message: "请选择一个考试方案" });
  });
});
