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

describe("paper import receipt and local draft reconciliation", () => {
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

  it("preserves edits made after the request snapshot in both the current and another question", async () => {
    const review = twoQuestionReview(); await mount(review);
    act(() => current.updateReviewDraft(0, "stem", { stem: "本轮提交内容" }));
    let finish!: (result: { import: PaperImportJob }) => void;
    mocks.savePaperImportReview.mockReturnValueOnce(new Promise(resolve => { finish = resolve; }));
    let saving!: Promise<void>; act(() => { saving = current.saveImportReview(review, 0); });
    act(() => {
      current.updateReviewDraft(0, "stem", { stem: "请求期间本题的新修改" });
      current.updateReviewDraft(1, "stem", { stem: "请求期间另一题的新修改" });
      current.setReviewJsonText("1:examples", "另一题未完成列表[", "无法读取");
    });
    const payload = mocks.savePaperImportReview.mock.calls[0][2];
    expect(payload[0].stem).toBe("本轮提交内容"); expect(payload[1]).toEqual(review.questions[1]);
    await act(async () => { finish({ import: { ...review, questions: payload } }); await saving; });
    expect(current.reviewDrafts.map(draft => draft.stem)).toEqual(["请求期间本题的新修改", "请求期间另一题的新修改"]);
    expect(current.reviewJsonTexts["1:examples"]).toBe("另一题未完成列表["); expect(current.invalidReviewJson).toBe(true); expect(current.reviewDirty).toBe(true);
  });

  it("keeps saved confirmation and other drafts when refresh fails, and discards only to the latest receipt", async () => {
    const review = twoQuestionReview(); await mount(review);
    act(() => { current.updateReviewDraft(0, "stem", { stem: "确已保存的第一题" }); current.updateReviewDraft(1, "stem", { stem: "未保存的第二题" }); });
    mocks.loadConfig.mockRejectedValueOnce(new Error("read failed"));
    await act(async () => { await current.saveImportReview(review, 0); });
    expect(mocks.success).not.toHaveBeenCalled(); expect(mocks.warning).toHaveBeenCalledWith(expect.stringContaining("本题已确认并保存"));
    expect(current.reviewDrafts[1].stem).toBe("未保存的第二题"); expect(current.reviewDirty).toBe(true); expect(current.confirmedReviewCount).toBe(1);
    act(() => current.discardImportReviewDraft());
    expect(current.reviewDrafts[0].stem).toBe("确已保存的第一题"); expect(current.reviewDrafts[1]).toEqual(review.questions[1]);
    expect(current.reviewDirty).toBe(false); expect(current.confirmedReviewCount).toBe(1);
  });

  it("keeps both local drafts unconfirmed when the write request fails", async () => {
    const review = twoQuestionReview(); await mount(review);
    act(() => { current.updateReviewDraft(0, "stem", { stem: "本题待保存" }); current.updateReviewDraft(1, "stem", { stem: "另一题待保存" }); });
    mocks.savePaperImportReview.mockRejectedValueOnce(new Error("write failed"));
    await act(async () => { await current.saveImportReview(review, 0); });
    expect(mocks.error).toHaveBeenCalledTimes(1); expect(mocks.success).not.toHaveBeenCalled(); expect(mocks.warning).not.toHaveBeenCalled();
    expect(mocks.loadConfig).not.toHaveBeenCalled(); expect(current.confirmedReviewCount).toBe(0); expect(current.reviewDirty).toBe(true);
    expect(current.reviewDrafts.map(draft => draft.stem)).toEqual(["本题待保存", "另一题待保存"]); expect(current.savingImportReview).toBe(false);
  });

  it.each(["generation", "source_revision", "exam", "status"])("does not merge a late saved receipt after %s changes", async boundary => {
    const review = twoQuestionReview(); await mount(review);
    let finish!: (result: { import: PaperImportJob }) => void;
    mocks.savePaperImportReview.mockReturnValueOnce(new Promise(resolve => { finish = resolve; }));
    let saving!: Promise<void>; act(() => { saving = current.saveImportReview(review, 0); });
    const changed = { ...review, ...(boundary === "generation" ? { generation: 8 } : boundary === "source_revision" ? { source_revision: "sources-next" } : boundary === "exam" ? { exam_id: "exam-other" } : { status: "applied" as const }) };
    await mount(changed); act(() => current.updateReviewDraft(1, "stem", { stem: "切换后本地草稿" }));
    await act(async () => { finish({ import: { ...review, questions: mocks.savePaperImportReview.mock.calls[0][2] } }); await saving; });
    expect(current.reviewDrafts[1].stem).toBe("切换后本地草稿"); expect(current.reviewDirty).toBe(true);
    expect(mocks.loadConfig).not.toHaveBeenCalled(); expect(mocks.success).not.toHaveBeenCalled(); expect(current.savingImportReview).toBe(false);
  });

  it.each([
    ["2026-10-08T14:00:02Z", "2026-10-08T14:00:01Z"],
    ["2026-10-08T14:00:02.123456Z", "2026-10-08T14:00:02.123123Z"]
  ])("ignores an older same-generation poll after saved timestamp %s", async (savedTime, pollTime) => {
    const review = twoQuestionReview(); await mount(review);
    mocks.savePaperImportReview.mockImplementationOnce(async (_id, _generation, questions) => ({ import: { ...review, questions, updated_at: savedTime } }));
    await act(async () => { await current.saveImportReview(review, 0); });
    expect(current.confirmedReviewCount).toBe(1);
    await mount({ ...review, questions: structuredClone(review.questions), updated_at: pollTime });
    expect(current.confirmedReviewCount).toBe(1); expect(current.reviewDirty).toBe(false);
  });

  it("tracks dirty candidate and JSON fields across authoritative reordering", async () => {
    const review = twoQuestionReview(); await mount(review);
    act(() => { current.updateReviewDraft(1, "stem", { stem: "第二候选本地草稿" }); current.setReviewJsonText("1:examples", "未完成[", "列表格式错误"); });
    const reordered = { ...review, updated_at: "2026-10-08T14:00:01Z", questions: [...review.questions].reverse() };
    await mount(reordered);
    expect(current.reviewDrafts[0].candidate_id).toBe("candidate-2"); expect(current.reviewDrafts[0].stem).toBe("第二候选本地草稿");
    expect(current.reviewJsonTexts["0:examples"]).toBe("未完成[");
    await act(async () => { await current.saveImportReview(review, 1); });
    expect(mocks.savePaperImportReview).not.toHaveBeenCalled();
    await compareLatest(reordered);
    await act(async () => { await current.saveImportReview(review, 1); });
    const payload = mocks.savePaperImportReview.mock.calls[0][2];
    expect(payload[0]).toEqual(reordered.questions[0]); expect(payload[1].candidate_id).toBe("candidate-1");
    expect(current.reviewDrafts[0].stem).toBe("第二候选本地草稿"); expect(current.invalidReviewJson).toBe(true); expect(current.reviewDirty).toBe(true);
  });

  it("preserves local drafts when a save receipt has missing candidates", async () => {
    const review = twoQuestionReview(); await mount(review);
    act(() => current.updateReviewDraft(0, "stem", { stem: "不能丢失的本题修改" }));
    mocks.savePaperImportReview.mockResolvedValueOnce({ import: { ...review, questions: [] } });
    await act(async () => { await current.saveImportReview(review, 0); });
    expect(mocks.error).toHaveBeenCalledWith(expect.stringContaining("候选题目不完整")); expect(mocks.success).not.toHaveBeenCalled();
    expect(current.reviewDrafts[0].stem).toBe("不能丢失的本题修改"); expect(current.reviewDirty).toBe(true);
  });

  it.each([0, 1])("does not roll back a newer poll when an old receipt arrives, refresh fails and question %i is next saved", async nextIndex => {
    const review = twoQuestionReview(); await mount(review);
    act(() => { current.updateReviewDraft(0, "stem", { stem: "t1 本轮提交内容" }); current.updateReviewDraft(1, "stem", { stem: "另一题未存草稿" }); });
    let finish!: (result: { import: PaperImportJob }) => void;
    mocks.savePaperImportReview.mockReturnValueOnce(new Promise(resolve => { finish = resolve; }));
    let saving!: Promise<void>; act(() => { saving = current.saveImportReview(review, 0); });
    const t1 = { ...review, updated_at: "2026-10-08T14:00:01Z", questions: mocks.savePaperImportReview.mock.calls[0][2] };
    const t2 = { ...review, updated_at: "2026-10-08T14:00:02Z", questions: [
      { ...t1.questions[0], stem: "t2 第一题最新服务器事实" },
      { ...review.questions[1], stem: "t2 第二题最新服务器事实", human_confirmed_fields: ["score"] }
    ] };
    await mount(t2);
    mocks.loadConfig.mockRejectedValueOnce(new Error("refresh failed"));
    await act(async () => { finish({ import: t1 }); await saving; });
    expect(current.reviewDrafts[0]).toEqual(t2.questions[0]); expect(current.reviewDrafts[1].stem).toBe("另一题未存草稿");
    expect(current.confirmedReviewCount).toBe(1); expect(current.reviewDirty).toBe(true);
    expect(mocks.warning).toHaveBeenCalledWith(expect.stringContaining("本题已确认并保存"));
    await act(async () => { await current.saveImportReview(review, nextIndex); });
    expect(mocks.savePaperImportReview).toHaveBeenCalledTimes(1);
    await compareLatest(t2);
    await act(async () => { await current.saveImportReview(review, nextIndex); });
    const nextPayload = mocks.savePaperImportReview.mock.calls[1][2] as PaperImportJob["questions"];
    expect(nextPayload[1 - nextIndex]).toEqual(t2.questions[1 - nextIndex]);
    expect(nextPayload[nextIndex].stem).toBe(nextIndex === 0 ? t2.questions[0].stem : "另一题未存草稿");
  });

  it("clears only the submitted candidate JSON when a poll reorders equal raw texts during saving", async () => {
    const review = twoQuestionReview(); await mount(review);
    act(() => { current.setReviewJsonText("0:examples", "[]"); current.setReviewJsonText("1:examples", "[]"); });
    let finish!: (result: { import: PaperImportJob }) => void;
    mocks.savePaperImportReview.mockReturnValueOnce(new Promise(resolve => { finish = resolve; }));
    let saving!: Promise<void>; act(() => { saving = current.saveImportReview(review, 0); });
    await mount({ ...review, updated_at: "2026-10-08T14:00:01Z", questions: [...review.questions].reverse() });
    expect(current.reviewDrafts[0].candidate_id).toBe("candidate-2");
    const receipt = { ...review, updated_at: "2026-10-08T14:00:02Z", questions: mocks.savePaperImportReview.mock.calls[0][2] };
    await act(async () => { finish({ import: receipt }); await saving; });
    const savedIndex = current.reviewDrafts.findIndex(draft => draft.candidate_id === "candidate-1");
    const otherIndex = current.reviewDrafts.findIndex(draft => draft.candidate_id === "candidate-2");
    expect(current.reviewJsonTexts[`${savedIndex}:examples`]).toBeUndefined(); expect(current.reviewJsonTexts[`${otherIndex}:examples`]).toBe("[]");
    expect(current.reviewDirty).toBe(true); expect(current.confirmedReviewCount).toBe(1);
  });
});
