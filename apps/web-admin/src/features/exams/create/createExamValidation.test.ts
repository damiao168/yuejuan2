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
    draft.creationMode = "template";
    draft.templateId = "template-1";
    draft.subjects[0].sections = [{ id: "section-1", title: "全卷", questionType: "short_answer", questionCount: 9, scorePerQuestion: 10 }];
    expect(validateCreateExam(draft)).toContainEqual({ field: "subject.math.totalScore", message: "模板题目分值合计必须等于科目满分" });
  });

  it("ignores stale template sections after switching to materials", () => {
    const draft=validDraft();
    draft.subjects[0].sections=[{id:"old",title:"旧方案",questionType:"calculation",questionCount:21,scorePerQuestion:11}];
    expect(validateCreateExam(draft)).toEqual([]);
  });

  it("requires an actual template only in template mode", () => {
    const draft = { ...validDraft(), creationMode: "template" as const, templateId: "" };
    expect(validateCreateExam(draft)).toContainEqual({ field: "templateId", message: "请选择一个考试方案" });
  });

  it("按基础信息和科目顺序返回错误，分区不完整仍继续检查总分", () => {
    const draft = initialCreateExamDraft();
    draft.creationMode = "template";
    draft.subjects = [{
      ...createSubjectDraft("math"), totalScore: 0, durationMinutes: 0,
      candidateRule: "subject_selected_classes",
      sections: [
        { id: "first", title: " ", questionType: "short_answer", questionCount: 1, scorePerQuestion: 1 },
        { id: "second", title: "", questionType: "", questionCount: 1, scorePerQuestion: 1 }
      ]
    }, { ...createSubjectDraft("english"), durationMinutes: 0 }];
    Object.assign(draft, { gradingMode: "" });

    // 创建页会聚焦数组首项；多个坏分区只生成一条摘要，后续科目和阅卷方式仍要检查。
    expect(validateCreateExam(draft)).toEqual([
      { field: "name", message: "请填写考试名称" },
      { field: "examType", message: "请选择考试类型" },
      { field: "schoolId", message: "请选择考试所属学校" },
      { field: "gradeId", message: "请选择考试年级" },
      { field: "classIds", message: "请至少选择一个参考班级" },
      { field: "templateId", message: "请选择一个考试方案" },
      { field: "subject.math.totalScore", message: "满分必须大于 0" },
      { field: "subject.math.durationMinutes", message: "考试时间必须大于 0" },
      { field: "subject.math.classIds", message: "请为该科目选择参考班级" },
      { field: "subject.math", message: "模板中的试卷分区不完整" },
      { field: "subject.math.totalScore", message: "模板题目分值合计必须等于科目满分" },
      { field: "subject.english.durationMinutes", message: "考试时间必须大于 0" },
      { field: "gradingMode", message: "请选择阅卷方式" }
    ]);
  });

  it("模板总分允许千分之一以内的舍入差值", () => {
    const draft = validDraft();
    draft.creationMode = "template";
    draft.templateId = "template-1";
    draft.subjects[0].sections = [{ id: "decimal", title: "全卷", questionType: "short_answer", questionCount: 1, scorePerQuestion: 100.0009 }];
    expect(validateCreateExam(draft)).toEqual([]);
    draft.subjects[0].sections[0].scorePerQuestion = 100.0011;
    expect(validateCreateExam(draft)).toEqual([{ field: "subject.math.totalScore", message: "模板题目分值合计必须等于科目满分" }]);
  });
});
