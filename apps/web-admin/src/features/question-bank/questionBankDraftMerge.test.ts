import { describe, expect, it } from "vitest";
import type { QuestionBankVersion } from "@edugrade/sdk";
import { mergeQuestionBankDraft } from "./questionBankDraftMerge";

const original = { id: "version", item_id: "item", revision: 2, workflow_status: "draft", stem: "原题干", default_score: 5,
  scoring: { answer: { standard_answer: "10", equivalent_answers: [], tolerance: {} }, solution: null,
    rubric: { points: [{ id: "p-original", score: 5, evidence_requirements: [] }], deductions: [], examples: [] }, assets: [], use_policy: "practice_only" }
} as unknown as QuestionBankVersion;
const latest = { ...original, revision: 3, stem: "他人题干", default_score: 8, scoring: { ...original.scoring,
  answer: { ...original.scoring.answer!, standard_answer: "20" }, rubric: { ...original.scoring.rubric!, points: [{ ...original.scoring.rubric!.points[0], id: "p-server", score: 8 }] }
} };
describe("题库冲突生成待保存草稿", () => {
  it("逐组选取本地题干和整份评分点，其他内容采用最新；不按下标混合评分点身份", () => {
    const points = [{ id: "p-local", score: 0, description: "本地评分点", evidence: "[]" }];
    const merged = mergeQuestionBankDraft(original, latest, { stem: "本地题干", default_score: 0 },
      { fields: { points, rubricEnabled: true } }, new Set(["stem", "rubric"]));
    expect(merged.content.stem).toBe("本地题干"); expect(merged.content.default_score).toBe(8);
    expect(merged.scoring.fields.points).toEqual(points); expect(merged.scoring.fields.answer).toBe("20");
    expect(latest.scoring.rubric!.points[0].id).toBe("p-server"); expect(latest.revision).toBe(3);
  });
  it("保留显式清空和零值，以及看似 JSON 的原文本答案类型", () => {
    const merged = mergeQuestionBankDraft(original, latest, { stem: "", default_score: 0 },
      { fields: { answer: "10", absolute: 0, relative: undefined, solution: "", steps: "[]" } }, new Set(["stem", "score", "answer", "solution"]));
    expect(merged.content.stem).toBe(""); expect(merged.content.default_score).toBe(0);
    expect(merged.scoring.fields.absolute).toBe(0); expect(merged.scoring.answerBaseline).toBe("10");
    expect(merged.scoring.fields.solution).toBe("");
  });
  it.each([{ ...latest, id: "other" }, { ...latest, item_id: "other" }, { ...latest, workflow_status: "published" }])("拒绝跨题、跨版本或非草稿合并", candidate => {
    expect(() => mergeQuestionBankDraft(original, candidate as QuestionBankVersion, {}, null, new Set())).toThrow("最新草稿");
  });
});
