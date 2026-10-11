// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PaperImportJob } from "../../api/papers";
import { usePaperImportWorkflow } from "./usePaperImportWorkflow";

const mocks = vi.hoisted(() => ({
  save: vi.fn(), apply: vi.fn(), loadConfig: vi.fn(),
  error: vi.fn(), warning: vi.fn(), success: vi.fn(), info: vi.fn()
}));
vi.mock("antd", () => ({ App: { useApp: () => ({
  message: { error: mocks.error, warning: mocks.warning, success: mocks.success, info: mocks.info },
  modal: { confirm: vi.fn() }
}) } }));
vi.mock("../../api/papers", () => ({
  savePaperImportReview: mocks.save, applyPaperImport: mocks.apply,
  uploadFile: vi.fn(), createPaperImport: vi.fn(), addPaperImportSources: vi.fn(),
  replacePaperImportSources: vi.fn(), cancelPaperImport: vi.fn(), retryPaperImportParse: vi.fn(),
  fillPaperImportContent: vi.fn()
}));

const review = {
  id: "import-43", exam_id: "exam-43", generation: 7, source_revision: "sources-7", status: "review_required",
  updated_at: "2026-10-09T03:00:00Z", sources: [], questions: [{
    candidate_id: "candidate-43", question_no: "1", question_type: "short_answer", score: 5, stem: "待确认原题",
    source_refs: [], knowledge_points: [], confidence: 1, issues: [], human_confirmed_fields: [],
    answer_key: { standard_answer: ["原答案"], equivalent_answers: [], tolerance: {} },
    rubric: { status: "draft", max_score: 5, points: [{ id: "point-43", description: "完成推导", score: 5, required: true }], deductions: [], examples: [] }
  }]
} as unknown as PaperImportJob;

describe("第43批导入确认的首错顺序", () => {
  let root: Root;
  let container: HTMLDivElement;
  let current: ReturnType<typeof usePaperImportWorkflow>;

  function Probe() {
    current = usePaperImportWorkflow({ canManage: true, selectedExam: { id: "exam-43" } as never,
      papers: [], paperImports: [review], loadConfig: mocks.loadConfig });
    return null;
  }

  beforeEach(async () => {
    vi.clearAllMocks();
    mocks.loadConfig.mockResolvedValue(undefined);
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
    await act(async () => { root.render(<Probe />); });
  });

  afterEach(async () => {
    await act(async () => { root.unmount(); });
    container.remove();
  });

  it.each([
    ["分值", 0, 4, "评分数据无法读取", "第1题分值必须大于 0，不能确认"],
    ["细则", 5, 4, "评分数据无法读取", "第1题评分细则分值不一致，不能确认"],
    ["JSON", 5, 5, "评分数据无法读取", "高级评分数据包含无效 JSON，请先修正"],
    ["答案", 5, 5, undefined, "第1题标准答案格式不正确，不能保存"]
  ] as const)("多个问题同时存在时先报告%s错误", async (_label, score, rubricScore, jsonError, expected) => {
    // 每行都保留后面的无效项；真实确认入口必须在首个错误处停止，不能开始写入。
    act(() => {
      current.updateReviewDraft(0, "score", { score });
      current.updateReviewDraft(0, "rubric", { rubric: { ...review.questions[0].rubric!, max_score: rubricScore,
        points: [{ ...review.questions[0].rubric!.points[0], score: rubricScore }] } });
      current.updateReviewDraft(0, "answer", { answer_key: { ...review.questions[0].answer_key!, standard_answer: "数组答案{" } });
      current.setReviewJsonText("0:examples", "未完成[", jsonError);
    });
    await act(async () => { await current.saveImportReview(review, 0); });
    expect(mocks.error).toHaveBeenCalledTimes(1);
    expect(mocks.error).toHaveBeenCalledWith(expected);
    expect(mocks.warning).not.toHaveBeenCalled();
    expect(mocks.success).not.toHaveBeenCalled();
    expect(mocks.save).not.toHaveBeenCalled();
    expect(mocks.apply).not.toHaveBeenCalled();
    expect(mocks.loadConfig).not.toHaveBeenCalled();
    expect(current.savingImportReview).toBe(false);
    expect(current.reviewDirty).toBe(true);
  });
});
