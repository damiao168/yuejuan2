// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PaperImportJob } from "../../api/papers";
import { usePaperImportWorkflow } from "./usePaperImportWorkflow";

const mocks = vi.hoisted(() => ({
  cancelPaperImport: vi.fn(), retryPaperImportParse: vi.fn(),
  replacePaperImportSources: vi.fn(), loadConfig: vi.fn(),
  uploadFile: vi.fn(), createPaperImport: vi.fn(), addPaperImportSources: vi.fn(),
  savePaperImportReview: vi.fn(), applyPaperImport: vi.fn(), fillPaperImportContent: vi.fn(),
  getPaperImport: vi.fn(),
  error: vi.fn(), success: vi.fn(), warning: vi.fn(), info: vi.fn(), confirm: vi.fn()
}));
vi.mock("antd", () => ({
  App: { useApp: () => ({
    message: { error: mocks.error, success: mocks.success, warning: mocks.warning, info: mocks.info },
    modal: { confirm: mocks.confirm }
  }) }
}));
vi.mock("../../api/papers", () => ({
  cancelPaperImport: mocks.cancelPaperImport,
  retryPaperImportParse: mocks.retryPaperImportParse,
  replacePaperImportSources: mocks.replacePaperImportSources,
  uploadFile: mocks.uploadFile, createPaperImport: mocks.createPaperImport,
  addPaperImportSources: mocks.addPaperImportSources,
  savePaperImportReview: mocks.savePaperImportReview,
  getPaperImport: mocks.getPaperImport,
  applyPaperImport: mocks.applyPaperImport,
  fillPaperImportContent: mocks.fillPaperImportContent
}));

function job(status: PaperImportJob["status"]): PaperImportJob {
  return {
    id: "import-1", exam_id: "exam-1", generation: 7, status,
    updated_at: "2026-10-08T14:00:00Z",
    error_code: status === "failed" ? "ai_parse_failed" : "",
    sources: [{ id: "source-1", role_hint: "auto" }],
    questions: []
  } as unknown as PaperImportJob;
}

function twoQuestionReview(): PaperImportJob {
  return { ...job("review_required"), source_revision: "sources-7", updated_at: "2026-10-08T14:00:00Z", questions: [1, 2].map(index => ({
    candidate_id: `candidate-${index}`, question_no: String(index), question_type: "short_answer", score: 5, stem: `第${index}题权威原题`,
    source_refs: [], knowledge_points: [], confidence: 1, issues: [], human_confirmed_fields: [],
    answer_key: { standard_answer: [`第${index}题答案`], equivalent_answers: [], tolerance: {} },
    rubric: { status: "draft", max_score: 5, points: [{ id: `point-${index}`, description: "完成推导", score: 5, required: true }], deductions: [], examples: [] }
  })) } as PaperImportJob;
}

describe("paper import review confirmation", () => {
  let root: Root;
  let container: HTMLDivElement;
  let current: ReturnType<typeof usePaperImportWorkflow>;
  let activeImport: PaperImportJob;

  function Probe() {
    current = usePaperImportWorkflow({
      canManage: true, selectedExam: { id: "exam-1" } as never,
      papers: [], paperImports: [activeImport], loadConfig: mocks.loadConfig
    });
    return null;
  }

  beforeEach(() => {
    vi.clearAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.cancelPaperImport.mockResolvedValue({});
    mocks.retryPaperImportParse.mockResolvedValue({});
    mocks.replacePaperImportSources.mockResolvedValue({});
    mocks.savePaperImportReview.mockImplementation(async (_id: string, _generation: number, questions: PaperImportJob["questions"]) => ({ import: { ...activeImport, questions } }));
    mocks.applyPaperImport.mockResolvedValue({});
    mocks.loadConfig.mockResolvedValue(undefined);
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
  });
  afterEach(async () => {
    await act(async () => { root.unmount(); });
    container.remove();
  });
  async function mount(importJob: PaperImportJob) {
    activeImport = importJob;
    await act(async () => { root.render(<Probe />); });
  }
  async function compareLatest(importJob: PaperImportJob) {
    mocks.getPaperImport.mockResolvedValue({ import: importJob });
    await act(async () => { await mocks.confirm.mock.calls[mocks.confirm.mock.calls.length - 1]![0].onOk(); });
    await act(async () => { await mocks.confirm.mock.calls[mocks.confirm.mock.calls.length - 1]![0].onOk(); });
  }

  it("preserves local edits during polling but replaces them for a new generation", async () => {
    const review = { ...job("review_required"), questions: [{ question_no: "1", question_type: "fill_blank", score: 2, stem: "原题" }] } as PaperImportJob;
    await mount(review);
    act(() => current.updateReviewDraft(0, "stem", { stem: "人工校对" }));
    await mount({ ...review });
    expect(current.reviewDrafts[0].stem).toBe("人工校对");
    expect(current.reviewDirty).toBe(true);

    await mount({ ...review, generation: 8, questions: [{ ...review.questions[0], stem: "重新识别" }] });
    expect(current.reviewDrafts[0].stem).toBe("重新识别");
    expect(current.reviewDirty).toBe(false);
    await act(async () => { await current.confirmPaperImport(review); });
    expect(mocks.savePaperImportReview).not.toHaveBeenCalled();
    expect(mocks.warning).toHaveBeenCalledWith("资料已更新，请核对当前识别结果后再确认");
  });

  it("重复采分点标识不能保存核对稿或确认为正式题目", async () => {
    const review = twoQuestionReview();
    review.questions[0].rubric!.points = [
      { id: "duplicate", description: "步骤一", score: 2, required: true },
      { id: "duplicate", description: "步骤二", score: 3, required: true }
    ];
    await mount(review);
    await act(async () => { await current.saveImportReview(review); });
    expect(mocks.savePaperImportReview).not.toHaveBeenCalled();
    expect(mocks.error).toHaveBeenCalledWith(expect.stringContaining("采分点标识重复"));
    await act(async () => { await current.confirmPaperImport(review); });
    expect(mocks.applyPaperImport).not.toHaveBeenCalled();
  });

  it("does not apply a saved review after the active generation changes", async () => {
    const review = { ...job("review_required"), questions: [{ question_no: "1", question_type: "fill_blank", score: 2, stem: "原题", human_confirmed_fields: ["question_no", "question_type", "score", "stem"] }] } as PaperImportJob;
    let resolveSave!: () => void;
    mocks.savePaperImportReview.mockReturnValueOnce(new Promise<void>((resolve) => { resolveSave = resolve; }));
    await mount(review);
    let confirmation!: Promise<void>;
    act(() => { confirmation = current.confirmPaperImport(review); });
    await mount({ ...review, generation: 8 });
    await act(async () => { resolveSave(); await confirmation; });
    expect(mocks.applyPaperImport).not.toHaveBeenCalled();
    expect(mocks.warning).toHaveBeenCalledWith("资料已更新，本轮核对结果未导入，请核对最新结果");
  });

  it("rejects a review whose rubric total disagrees with the question score", async () => {
    const review = { ...job("review_required"), questions: [{
      question_no: "1", score: 5,
      rubric: { max_score: 4, points: [{ score: 4 }] }
    }] } as PaperImportJob;
    await mount(review);
    expect(current.invalidReviewRubric).toBe(true);
    await act(async () => { await current.confirmPaperImport(review); });
    expect(mocks.savePaperImportReview).not.toHaveBeenCalled();
    expect(mocks.applyPaperImport).not.toHaveBeenCalled();
    expect(mocks.error).toHaveBeenCalled();
  });

  it("confirms a fill-in-the-blank question by answer and removes legacy rubric input", async () => {
    const review = { ...job("review_required"), questions: [{
      question_no: "8", question_type: "fill_blank", score: 2, stem: "填空",
      answer_key: { standard_answer: "1" },
      rubric_candidate_id: "legacy-rubric",
      rubric: { max_score: 1, points: [{ score: 1 }] },
      human_confirmed_fields: ["rubric", "question_no", "question_type", "score", "stem", "answer"]
    }] } as PaperImportJob;
    await mount(review);

    expect(current.invalidReviewRubric).toBe(false);
    await act(async () => { await current.confirmPaperImport(review); });

    const confirmedQuestions = mocks.savePaperImportReview.mock.calls[0][2] as PaperImportJob["questions"];
    expect(confirmedQuestions[0].rubric).toBeUndefined();
    expect(confirmedQuestions[0].rubric_candidate_id).toBeUndefined();
    expect(confirmedQuestions[0].human_confirmed_fields).toContain("answer");
    expect(confirmedQuestions[0].human_confirmed_fields).not.toContain("rubric");
    expect(mocks.applyPaperImport).toHaveBeenCalledWith("import-1");
  });

  it("does not confirm unseen questions when saving or importing a review", async () => {
    const review = { ...job("review_required"), questions: [{
      question_no: "1", question_type: "single_choice", score: 5, stem: "选择正确结论",
      options: ["A. 1", "B. 2"], answer_key: { standard_answer: "A" }
    }, { question_no: "2", question_type: "fill_blank", score: 2, stem: "未查看的题", answer_key: { standard_answer: "2" } }] } as PaperImportJob;
    await mount(review);
    await act(async () => { await current.saveImportReview(review); });
    const savedDrafts = mocks.savePaperImportReview.mock.calls[0][2] as PaperImportJob["questions"];
    expect(savedDrafts.every((draft) => !draft.human_confirmed_fields?.length)).toBe(true);
    await act(async () => { await current.saveImportReview(review, 0); });
    const confirmedQuestions = mocks.savePaperImportReview.mock.calls[1][2] as PaperImportJob["questions"];
    expect(confirmedQuestions[0].human_confirmed_fields).toContain("options");
    expect(confirmedQuestions[0].options).toEqual(["A. 1", "B. 2"]);
    expect(confirmedQuestions[1]).toEqual(savedDrafts[1]);
    await act(async () => { await current.confirmPaperImport(review); });
    expect(mocks.applyPaperImport).not.toHaveBeenCalled();
    expect(mocks.warning).toHaveBeenCalledWith(expect.stringContaining("未确认"));
  });

  it.each(["rubric", "answer", "json"])("confirms only the current question while the other local %s draft is invalid", async (invalidField) => {
    const review = twoQuestionReview(); await mount(review);
    act(() => {
      current.updateReviewDraft(0, "stem", { stem: "本题人工核对" });
      if (invalidField === "rubric") current.updateReviewDraft(1, "rubric", { rubric: { ...review.questions[1].rubric!, max_score: 4 } });
      if (invalidField === "answer") current.updateReviewDraft(1, "answer", { answer_key: { ...review.questions[1].answer_key!, standard_answer: "数组答案尚未填写完成{" } });
      if (invalidField === "json") current.setReviewJsonText("1:examples", "未填完的评分示例[", "评分示例无法读取");
    });
    // 另一题在保存前收到较新的服务器事实，过期按钮参数和本地草稿都不能覆盖它。
    const latest = { ...review, updated_at: "2026-10-08T14:00:01Z", questions: [review.questions[0], { ...review.questions[1], stem: "第二题最新权威原值", human_confirmed_fields: ["score"] }] };
    await mount(latest);
    const otherLocal = structuredClone(current.reviewDrafts[1]);
    await act(async () => { await current.saveImportReview(review, 0); });
    // 轮询已返回他人内容；先比较重基，再保存本题，不能自动使用新时间覆盖旧草稿。
    expect(mocks.savePaperImportReview).not.toHaveBeenCalled();
    await compareLatest(latest);
    await act(async () => { await current.saveImportReview(review, 0); });
    expect(mocks.error).not.toHaveBeenCalled(); expect(mocks.savePaperImportReview).toHaveBeenCalledTimes(1);
    const payload = mocks.savePaperImportReview.mock.calls[0][2] as PaperImportJob["questions"];
    expect(payload).toHaveLength(2); expect(payload[0].stem).toBe("本题人工核对");
    expect(payload[0].human_confirmed_fields).toEqual(expect.arrayContaining(["question_no", "question_type", "score", "stem", "answer", "rubric"]));
    expect(payload[1]).toEqual(latest.questions[1]);
    expect(current.reviewDrafts[1]).toEqual(otherLocal); expect(current.reviewDirty).toBe(true); expect(current.confirmedReviewCount).toBe(1);
    if (invalidField === "json") { expect(current.invalidReviewJson).toBe(true); expect(current.reviewJsonTexts["1:examples"]).toBe("未填完的评分示例["); }
    await mount({ ...latest, updated_at: "2026-10-08T14:00:02Z", questions: payload.map(draft => ({ ...draft, completeness_status: "needs_review" as const })) });
    // 未解析文本保存在独立草稿中；未改动的题目对象仍可接收服务器的诊断状态。
    expect(current.reviewDrafts[1]).toEqual(invalidField === "json" ? { ...otherLocal, completeness_status: "needs_review" } : otherLocal);
    expect(current.reviewDirty).toBe(true); expect(current.confirmedReviewCount).toBe(1);
    expect(mocks.loadConfig).toHaveBeenCalledWith("exam-1", { silent: true, propagateError: true });
  });

  it.each(["rubric", "answer", "json"])("keeps full-review validation for another invalid %s draft", async invalidField => {
    const review = twoQuestionReview(); await mount(review);
    act(() => {
      if (invalidField === "rubric") current.updateReviewDraft(1, "rubric", { rubric: { ...review.questions[1].rubric!, max_score: 4 } });
      if (invalidField === "answer") current.updateReviewDraft(1, "answer", { answer_key: { ...review.questions[1].answer_key!, standard_answer: "未完成数组答案{" } });
      if (invalidField === "json") current.setReviewJsonText("1:examples", "无效列表[", "评分示例无法读取");
    });
    await act(async () => { await current.saveImportReview(review); await current.confirmPaperImport(review); });
    expect(mocks.savePaperImportReview).not.toHaveBeenCalled(); expect(mocks.applyPaperImport).not.toHaveBeenCalled();
    expect(mocks.error).toHaveBeenCalledTimes(2); expect(current.reviewDirty).toBe(true);
  });

  it("rejects invalid current score and current answer before writing confirmation", async () => {
    const review = twoQuestionReview(); await mount(review);
    act(() => current.updateReviewDraft(0, "score", { score: 0 }));
    await act(async () => { await current.saveImportReview(review, 0); });
    expect(mocks.error).toHaveBeenLastCalledWith("第1题分值必须大于 0，不能确认");
    act(() => { current.updateReviewDraft(0, "score", { score: 5 }); current.updateReviewDraft(0, "answer", { answer_key: { ...review.questions[0].answer_key!, standard_answer: "非法数组{" } }); });
    await act(async () => { await current.saveImportReview(review, 0); });
    expect(mocks.error).toHaveBeenLastCalledWith("第1题标准答案格式不正确，不能保存");
    expect(mocks.savePaperImportReview).not.toHaveBeenCalled(); expect(current.confirmedReviewCount).toBe(0);
  });

  it("retains the first saved confirmation when a second question is saved before page props refresh", async () => {
    const review = twoQuestionReview(); await mount(review);
    act(() => current.updateReviewDraft(0, "stem", { stem: "第一题已核对" }));
    await act(async () => { await current.saveImportReview(review, 0); });
    const firstSaved = mocks.savePaperImportReview.mock.calls[0][2][0];
    act(() => current.updateReviewDraft(1, "stem", { stem: "第二题已核对" }));
    await act(async () => { await current.saveImportReview(review, 1); });
    expect(mocks.savePaperImportReview.mock.calls[1][2][0]).toEqual(firstSaved);
    expect(mocks.savePaperImportReview.mock.calls[1][2][1].stem).toBe("第二题已核对");
    expect(current.confirmedReviewCount).toBe(2); expect(current.reviewDirty).toBe(false);
  });

  it("locks synchronous repeated saves and import, and refuses a late discard while writing", async () => {
    const review = twoQuestionReview(); await mount(review);
    act(() => current.updateReviewDraft(0, "stem", { stem: "当前提交内容" }));
    let finish!: (result: { import: PaperImportJob }) => void;
    mocks.savePaperImportReview.mockReturnValueOnce(new Promise(resolve => { finish = resolve; }));
    let saving!: Promise<void>;
    act(() => { saving = current.saveImportReview(review, 0); void current.saveImportReview(review, 0); void current.confirmPaperImport(review); current.discardImportReviewDraft(); });
    expect(mocks.savePaperImportReview).toHaveBeenCalledTimes(1); expect(mocks.applyPaperImport).not.toHaveBeenCalled();
    expect(current.reviewDrafts[0].stem).toBe("当前提交内容"); expect(current.savingImportReview).toBe(true);
    await act(async () => { finish({ import: { ...review, questions: mocks.savePaperImportReview.mock.calls[0][2] } }); await saving; });
    expect(current.savingImportReview).toBe(false); expect(current.reviewDirty).toBe(false);
  });


});
