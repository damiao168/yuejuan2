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

function readFileText(file: File) {
  return new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result ?? ""));
    reader.onerror = () => reject(reader.error);
    reader.readAsText(file);
  });
}

describe("paper import material lifecycle", () => {
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


});
