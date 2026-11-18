import { describe, expect, it } from "vitest";
import type { AiGrade } from "../../../api/review";
import { manualDraftAIAdoption, restoreDraftAIAdoption, submittedDraftAIAdoption } from "./aiAdoptionDraft";
import { createInitialDraft } from "./gradingWorkbench.model";

describe("评分草稿中的采纳事实", () => {
  const grade = { id: "model", suggested_score: 9, grader_type: "llm_subjective", status: "succeeded", delivery_mode: "teacher_suggestion", mock: false } as AiGrade;
  it("区分历史未知、明确手工和可验证采纳，损坏记录保持未知", () => {
    for (const value of [undefined, null, false, "accepted", {}, { taskId: "A", aiGradeId: "model", suggestedScore: "9" }, { taskId: "A", aiGradeId: "model", suggestedScore: 9 }]) {
      expect(restoreDraftAIAdoption(value)).toBeUndefined();
    }
    for (const accepted of [true, false]) {
      const choice = { taskId: "A", aiGradeId: "model", suggestedScore: 9, accepted };
      expect(restoreDraftAIAdoption(choice)).toEqual(choice);
    }
  });
  it("历史草稿省略采纳统计，明确手工评分才记录未采纳", () => {
    const draft = { ...createInitialDraft(null), score: 9 };
    expect(submittedDraftAIAdoption(draft, "A", grade)).toBeUndefined();
    expect(submittedDraftAIAdoption({ ...draft, aiAdoption: null }, "A", grade)).toBeUndefined();
    const aiAdoption = manualDraftAIAdoption("A", grade);
    expect(submittedDraftAIAdoption({ ...draft, aiAdoption }, "A", grade)).toEqual({ ai_grade_id: "model", accepted: false });
    expect(submittedDraftAIAdoption({ ...draft, aiAdoption }, "A", { ...grade, id: "new-model" })).toBeUndefined();
    expect(submittedDraftAIAdoption({ ...draft, aiAdoption }, "B", grade)).toBeUndefined();
    expect(submittedDraftAIAdoption({ ...draft, aiAdoption }, "A", { ...grade, suggested_score: 8 })).toBeUndefined();
  });
  it.each(["shadow_only", "auto_apply"])("%s 的结果不产生手工评分采纳样本", mode => {
    expect(manualDraftAIAdoption("A", { ...grade, delivery_mode: mode } as AiGrade)).toBeUndefined();
  });
});
