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

function readFileText(file: File) {
  return new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result ?? ""));
    reader.onerror = () => reject(reader.error);
    reader.readAsText(file);
  });
}

describe("paper import workflow controller", () => {
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

  it("fills recognized content with the expected generation without confirming the review", async () => {
    const review = job("review_required");
    mocks.fillPaperImportContent.mockResolvedValueOnce({ filled_questions: ["1"], missing_answers: ["2"], skipped: [] });
    await mount(review);
    await act(async () => { await current.fillRecognizedContent(review); });
    expect(mocks.fillPaperImportContent).toHaveBeenCalledWith("import-1", 7);
    expect(mocks.savePaperImportReview).not.toHaveBeenCalled();
    expect(mocks.applyPaperImport).not.toHaveBeenCalled();
    expect(mocks.loadConfig).toHaveBeenCalledWith("exam-1", { silent: true });
    expect(current.contentFillResult?.missing_answers).toEqual(["2"]);
  });

  it("discards a content-fill response when the active generation changes", async () => {
    const review = job("review_required");
    let resolveFill!: (value: { filled_questions: string[]; missing_answers: string[]; skipped: [] }) => void;
    mocks.fillPaperImportContent.mockReturnValueOnce(new Promise((resolve) => { resolveFill = resolve; }));
    await mount(review);
    let fill!: Promise<void>;
    act(() => { fill = current.fillRecognizedContent(review); });
    await mount({ ...review, generation: 8 });
    await act(async () => { resolveFill({ filled_questions: ["1"], missing_answers: [], skipped: [] }); await fill; });
    expect(mocks.loadConfig).not.toHaveBeenCalled();
    expect(mocks.success).not.toHaveBeenCalled();
    expect(current.contentFillResult).toBeNull();
  });

  it("requires saving local review edits before source changes restart recognition", async () => {
    const review = { ...job("review_required"), questions: [{ question_no: "1", question_type: "fill_blank", score: 2, stem: "原题" }] } as PaperImportJob;
    await mount(review);
    act(() => current.updateReviewDraft(0, "stem", { stem: "人工修改" }));
    await act(async () => { await current.replaceImportSources(review, review.sources); });
    await act(async () => { await current.importPastedText("## 第 1 题\n\n已知 $x^2=4$，求 $x$ 的值，并写出完整步骤。", "question"); });
    expect(mocks.replacePaperImportSources).not.toHaveBeenCalled();
    expect(mocks.uploadFile).not.toHaveBeenCalled();
    expect(current.reviewDrafts[0].stem).toBe("人工修改");
    expect(mocks.warning).toHaveBeenCalledTimes(2);
  });

  it("retries only a failed parse with its current generation", async () => {
    const failed = job("failed");
    await mount(failed);
    await act(async () => { await current.retryImportParse(failed); });
    expect(mocks.retryPaperImportParse).toHaveBeenCalledWith("import-1", 7);
    expect(mocks.loadConfig).toHaveBeenCalledWith("exam-1", { silent: true });
  });

  it("cancels processing only after confirmation, preserving the generation", async () => {
    const processing = job("processing");
    await mount(processing);
    act(() => { current.stopPaperImport(processing); });
    expect(mocks.cancelPaperImport).not.toHaveBeenCalled();
    const confirmation = mocks.confirm.mock.calls[0][0] as { onOk: () => Promise<void> };
    await act(async () => { await confirmation.onOk(); });
    expect(mocks.cancelPaperImport).toHaveBeenCalledWith("import-1", 7, expect.any(String));
    expect(mocks.loadConfig).toHaveBeenCalledWith("exam-1", { silent: true });
  });

  it("sends the expected generation when replacing sources and reports a conflict", async () => {
    const review = job("review_required");
    mocks.replacePaperImportSources.mockRejectedValueOnce(new Error("stale generation"));
    await mount(review);
    await act(async () => { await current.replaceImportSources(review, review.sources); });
    expect(mocks.replacePaperImportSources).toHaveBeenCalledWith(
      "import-1", 7,
      [{ id: "source-1", document_index: 0, role_hint: "auto" }],
      expect.any(String)
    );
    expect(mocks.error).toHaveBeenCalled();
    expect(current.updatingImportSources).toBe(false);
  });

  it("does not start an import after a partial material upload failure", async () => {
    mocks.uploadFile.mockResolvedValueOnce({ file: { id: "asset-1" } })
      .mockRejectedValueOnce(new Error("upload failed"));
    await mount(job("review_required"));
    const first = Object.assign(new File(["a"], "one.pdf", { type: "application/pdf" }), { uid: "file-1" });
    const second = Object.assign(new File(["b"], "two.pdf", { type: "application/pdf" }), { uid: "file-2" });
    await act(async () => {
      current.uploadProps.beforeUpload?.(first as never, [first, second] as never);
      await vi.waitFor(() => expect(mocks.uploadFile).toHaveBeenCalledTimes(2));
      await vi.waitFor(() => expect(mocks.error).toHaveBeenCalled());
    });
    expect(current.parsing).toBe(false);
    expect(mocks.createPaperImport).not.toHaveBeenCalled();
    expect(mocks.addPaperImportSources).not.toHaveBeenCalled();
    expect(mocks.loadConfig).toHaveBeenCalledWith("exam-1", { silent: true });
    expect(mocks.error).toHaveBeenCalled();
  });

  it("uploads pasted Markdown as a text source and preserves its role hint", async () => {
    const review = job("review_required");
    mocks.uploadFile.mockResolvedValueOnce({ file: { id: "markdown-asset" } });
    mocks.addPaperImportSources.mockResolvedValueOnce({ import: { ...review, status: "processing" } });
    await mount(review);
    const markdown = "## 第 1 题\n\n已知 $x^2=4$，求 $x$ 的值，并写出完整步骤。";
    let imported = false;
    await act(async () => { imported = await current.importPastedText(markdown, "question"); });
    expect(imported).toBe(true);
    const uploaded = mocks.uploadFile.mock.calls[0][0] as File;
    expect(uploaded.name).toMatch(/^pasted-material-\d{8}-\d{6}\.md$/);
    expect(uploaded.type).toBe("text/markdown");
    expect(await readFileText(uploaded)).toBe(markdown);
    expect(mocks.addPaperImportSources).toHaveBeenCalledWith(
      "import-1",
      7,
      [{ file_asset_id: "markdown-asset", document_index: 1, role_hint: "question" }],
      expect.any(String)
    );
  });

  it("does not restart recognition when the uploaded content is already attached", async () => {
    const review = {
      ...job("review_required"),
      sources: [{ id: "source-1", file_asset_id: "markdown-asset", document_index: 0, role_hint: "auto" }]
    } as PaperImportJob;
    mocks.uploadFile.mockResolvedValueOnce({ file: { id: "markdown-asset" } });
    await mount(review);

    let imported = false;
    await act(async () => {
      imported = await current.importPastedText("## 第 1 题\n\n已知 $x^2=4$，求 $x$ 的值，并写出完整步骤。", "question");
    });

    expect(imported).toBe(true);
    expect(mocks.addPaperImportSources).not.toHaveBeenCalled();
    expect(mocks.createPaperImport).not.toHaveBeenCalled();
    expect(mocks.info).toHaveBeenCalledWith("这份资料已在当前识别任务中，无需重复添加");
  });

  it("adds only new assets when a batch contains existing and repeated files", async () => {
    const review = {
      ...job("review_required"),
      sources: [{ id: "source-1", file_asset_id: "asset-existing", document_index: 0, role_hint: "auto" }]
    } as PaperImportJob;
    mocks.uploadFile
      .mockResolvedValueOnce({ file: { id: "asset-existing" } })
      .mockResolvedValueOnce({ file: { id: "asset-new" } })
      .mockResolvedValueOnce({ file: { id: "asset-new" } });
    mocks.addPaperImportSources.mockResolvedValueOnce({ import: { ...review, status: "processing" } });
    await mount(review);
    const files = [
      Object.assign(new File(["a"], "old.pdf", { type: "application/pdf" }), { uid: "old" }),
      Object.assign(new File(["b"], "new.pdf", { type: "application/pdf" }), { uid: "new" }),
      Object.assign(new File(["b"], "new-copy.pdf", { type: "application/pdf" }), { uid: "new-copy" })
    ];

    await act(async () => {
      current.uploadProps.beforeUpload?.(files[0] as never, files as never);
      await vi.waitFor(() => expect(mocks.addPaperImportSources).toHaveBeenCalled());
    });

    expect(mocks.addPaperImportSources).toHaveBeenCalledWith(
      "import-1",
      7,
      [{ file_asset_id: "asset-new", document_index: 1, role_hint: "auto" }],
      expect.any(String)
    );
    expect(mocks.success).toHaveBeenCalledWith("已忽略 2 份重复资料，新增 1 份并开始识别");
  });

  it("rejects pasted text that is too short before uploading", async () => {
    await mount(job("review_required"));
    let imported = true;
    await act(async () => { imported = await current.importPastedText("第 1 题", "auto"); });
    expect(imported).toBe(false);
    expect(mocks.uploadFile).not.toHaveBeenCalled();
    expect(mocks.error).toHaveBeenCalledWith("请至少粘贴 20 个字符的考试资料");
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
