import { describe, expect, it } from "vitest";
import { createInitialDraft, fallbackSnapshot, latestGrade, resultState } from "./gradingWorkbench.model";
import type { AiGrade, ScoringRunItem } from "../../../api/review";
import type { WorkbenchContext } from "./gradingWorkbench.types";

function contextWith(points: Array<{ id: string; description: string; score: number }>, ocrText: string) {
  return {
    ocrText,
    ocrResults: [],
    question: { rubric: { points } }
  } as unknown as WorkbenchContext;
}

describe("grading workbench draft model", () => {
  it("回看已提交答卷只使用本任务本人正式分，保留明确的零分", () => {
    const context = { ...contextWith([], "作答"), task: { id: "task", status: "submitted", assigned_to: "teacher" },
      reviewContext: { human_grade: { review_task_id: "task", reviewer_id: "teacher", score: 0, comments: "教师记录", rubric_selections: [] } } } as unknown as WorkbenchContext;
    expect(createInitialDraft(context)).toMatchObject({ score: 0, comments: "教师记录" });
    expect(createInitialDraft({ ...context, task: { ...context.task, id: "other" } }).score).toBeNull();
  });
  it("creates a fresh draft for each task without leaking rubric selections", () => {
    const first = createInitialDraft(contextWith([
      { id: "step-1", description: "步骤一", score: 2 },
      { id: "step-2", description: "步骤二", score: 3 }
    ], "第一题答案"));

    first.score = 5;
    first.comments = "已评分";
    first.rubricSelections["step-1"] = 2;

    const second = createInitialDraft(contextWith([
      { id: "conclusion", description: "结论", score: 4 }
    ], "第二题答案"));

    expect(second).toEqual({
      score: null,
      comments: "",
      privateNote: "",
      studentFeedback: "",
      reason: "",
      disputeReason: "",
      rubricSelections: { conclusion: 0 },
      answerText: "第二题答案"
    });
    expect(second.rubricSelections).not.toBe(first.rubricSelections);
    expect(second.rubricSelections).not.toHaveProperty("step-1");
  });

  it("restores only valid fallback values and keeps task defaults for invalid data", () => {
    const initial = createInitialDraft(contextWith([
      { id: "criterion", description: "采分点", score: 2 }
    ], "识别文本"));

    const restored = fallbackSnapshot({
      draft: {
        score: Number.NaN,
        comments: "离线草稿",
        rubricSelections: { criterion: 2, invalid: "two" }
      },
      viewer: {
        mode: "unsupported",
        scale: 99,
        rotation: Number.NaN,
        offset: { x: 12, y: "bad" },
        fit: false
      }
    }, initial);

    expect(restored).toMatchObject({
      draft: {
        score: null,
        comments: "离线草稿",
        answerText: "识别文本",
        rubricSelections: { criterion: 2 }
      },
      viewer: {
        mode: "segment",
        scale: 4,
        rotation: 0,
        offset: { x: 12, y: 0 },
        fit: false
      }
    });
  });
});

describe("grading workbench production safeguards", () => {
  it("never exposes a shadow-only grade as a usable suggestion", () => {
    const shadowGrade = {
      id: "shadow-grade",
      delivery_mode: "shadow_only",
      created_at: "2026-08-30T00:00:00Z"
    } as AiGrade;

    expect(latestGrade([shadowGrade])).toBeUndefined();
  });

  it("labels only rule-confirmed results as automatically confirmed", () => {
    expect(resultState({ state: "confirmed", grade_source: "rule_confirmed" } as ScoringRunItem)).toEqual({
      label: "自动确认",
      tone: "success"
    });
    expect(resultState({ state: "confirmed", grade_source: "human_review" } as ScoringRunItem)).toEqual({
      label: "人工完成",
      tone: "success"
    });
  });
});
