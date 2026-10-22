// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useAnswerSheetSourceUpload } from "./useAnswerSheetSourceUpload";
import type { PaperVersion } from "../../../api/papers";

const mocks = vi.hoisted(() => ({ merge: vi.fn(), upload: vi.fn(), register: vi.fn(), download: vi.fn(), readSourceHashes: vi.fn(), onCreated: vi.fn(), success: vi.fn(), warning: vi.fn(), info: vi.fn(), order: vi.fn() }));
vi.mock("antd", () => ({ App: { useApp: () => ({ message: { success: mocks.success, warning: mocks.warning, info: mocks.info } }) } }));
vi.mock("../../../api/files", () => ({ uploadFileWithProgress: mocks.upload, downloadFileBlob: mocks.download }));
vi.mock("../../../api/papers", () => ({ registerPaperFromFile: mocks.register }));
vi.mock("./mergeAnswerSheetSources", () => ({ mergeAnswerSheetSources: mocks.merge }));
vi.mock("./answerSheetSourceManifest", () => ({ readAnswerSheetSourceHashes: mocks.readSourceHashes }));
vi.mock("./orderAnswerSheetSourcePages", () => ({ orderAnswerSheetSourcePages: mocks.order }));
const file = new File(["synthetic"], "blank-answer-sheet.pdf", { type: "application/pdf" });
const asset = { id: "asset-sheet" };
const source = { id: "source-sheet", purpose: "answer_sheet" };
const combinedPaper = { id: "combined", exam_id: "exam-1", purpose: "answer_sheet", file_asset_id: "combined-asset", file: { content_type: "application/pdf", hash_sha256: "b".repeat(64) } } as PaperVersion;
function deferred<T = { file: typeof asset }>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => { resolve = done; });
  return { promise, resolve };
}

describe("independent answer sheet upload", () => {
  let root: Root;
  let container: HTMLDivElement;
  let current: ReturnType<typeof useAnswerSheetSourceUpload>;
  let mounted: boolean;
  function Probe({ examId = "exam-1", canManage = true, identityKey = "account-1", existingPapers = [] }: { examId?: string; canManage?: boolean; identityKey?: string; existingPapers?: PaperVersion[] }) {
    current = useAnswerSheetSourceUpload({ examId, canManage, identityKey, existingPapers, onCreated: mocks.onCreated });
    return null;
  }
  beforeEach(() => {
    vi.resetAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.merge.mockImplementation(async (files: readonly File[]) => files[0]);
    mocks.upload.mockResolvedValue({ file: asset });
    mocks.register.mockResolvedValue({ paper: source });
    mocks.download.mockResolvedValue({ blob: new Blob(["combined PDF"]) });
    mocks.readSourceHashes.mockResolvedValue(["c".repeat(64)]);
    container = document.createElement("div"); document.body.appendChild(container);
    root = createRoot(container); mounted = true;
  });
  afterEach(async () => {
    if (mounted) await act(async () => { root.unmount(); });
    container.remove();
  });
  async function render(props: { examId?: string; canManage?: boolean; identityKey?: string; existingPapers?: PaperVersion[] } = {}) { await act(async () => { root.render(<Probe {...props} />); }); }

  it("registers the uploaded file as an answer sheet for the current exam", async () => {
    await render(); await act(async () => { await current.upload(file); });
    expect(mocks.upload).toHaveBeenCalledWith(file, { owner_type: "exam", owner_id: "exam-1", exam_id: "exam-1" }, expect.any(Function));
    expect(mocks.register).toHaveBeenCalledWith("exam-1", "asset-sheet", "answer_sheet");
    expect(mocks.onCreated).toHaveBeenCalledWith(source);
  });

  it("retries registration using the uploaded asset after the second step fails", async () => {
    mocks.register.mockRejectedValueOnce(new Error("failed registration"));
    await render(); await act(async () => { await current.upload(file); });
    expect(current.canRetry).toBe(true);
    expect(mocks.onCreated).not.toHaveBeenCalled();
    await act(async () => { await current.retry(); });
    expect(mocks.upload).toHaveBeenCalledTimes(1);
    expect(mocks.register).toHaveBeenCalledTimes(2);
    expect(mocks.onCreated).toHaveBeenCalledWith(source);
    expect(current.canRetry).toBe(false);
  });

  it("blocks duplicate uploads while the first request is pending", async () => {
    const waiting = deferred(); mocks.upload.mockReturnValue(waiting.promise);
    await render(); let pending!: Promise<void>;
    await act(async () => { pending = current.upload(file); await current.upload(file); });
    expect(mocks.upload).toHaveBeenCalledTimes(1);
    await act(async () => { waiting.resolve({ file: asset }); await pending; });
  });

  for (const boundary of ["exam", "permission", "identity", "unmount"] as const) {
    it(`ignores an upload finishing after the ${boundary} changes`, async () => {
      const waiting = deferred(); mocks.upload.mockReturnValue(waiting.promise);
      await render(); let pending!: Promise<void>;
      await act(async () => { pending = current.upload(file); });
      if (boundary === "exam") await render({ examId: "exam-2" });
      else if (boundary === "permission") await render({ canManage: false });
      else if (boundary === "identity") await render({ identityKey: "account-2" });
      else await act(async () => { root.unmount(); mounted = false; });
      await act(async () => { waiting.resolve({ file: asset }); await pending; });
      expect(mocks.register).not.toHaveBeenCalled();
      expect(mocks.onCreated).not.toHaveBeenCalled();
    });
  }

  it("does not upload when the account cannot manage answer sheets", async () => {
    await render({ canManage: false }); await act(async () => { await current.upload(file); });
    expect(mocks.upload).not.toHaveBeenCalled();
    expect(mocks.merge).not.toHaveBeenCalled();
  });

  it("merges the selected batch in order before uploading and registering a single source", async () => {
    const second = new File(["second image"], "page-2.png", { type: "image/png" });
    const merged = new File(["merged pages"], "whole-sheet.pdf", { type: "application/pdf" });
    mocks.merge.mockResolvedValue(merged);
    await render(); await act(async () => { await current.uploadBatch([file, second]); });
    expect(mocks.merge).toHaveBeenCalledWith([file, second], expect.objectContaining({ isCurrent: expect.any(Function) }));
    expect(mocks.upload).toHaveBeenCalledWith(merged, expect.any(Object), expect.any(Function));
    expect(mocks.upload).toHaveBeenCalledTimes(1);
    expect(mocks.register).toHaveBeenCalledTimes(1);
    expect(current.phase).toBe("idle");
  });

  it("keeps the entire merge busy and does not start a second batch", async () => {
    const waiting = deferred<File>(); mocks.merge.mockReturnValue(waiting.promise);
    await render(); let pending!: Promise<void>;
    await act(async () => { pending = current.uploadBatch([file]); await current.uploadBatch([file]); });
    expect(current.uploading).toBe(true);
    expect(current.phase).toBe("merging");
    expect(mocks.merge).toHaveBeenCalledTimes(1);
    expect(mocks.upload).not.toHaveBeenCalled();
    await act(async () => { waiting.resolve(file); await pending; });
  });

  it("does not upload a partial batch when merging a later file fails", async () => {
    mocks.merge.mockRejectedValue(new Error("第 2 个答题卡文件无法读取。"));
    await render(); await act(async () => { await current.uploadBatch([file, file]); });
    expect(current.error).toContain("第 2 个");
    expect(current.canRetry).toBe(false);
    expect(current.uploading).toBe(false);
    expect(mocks.upload).not.toHaveBeenCalled();
    expect(mocks.register).not.toHaveBeenCalled();
  });

  it("passes existing content hashes and only permits source reuse for an explicit combination", async () => {
    const existingPapers = [{ exam_id: "exam-1", purpose: "answer_sheet", file: { hash_sha256: "a".repeat(64) } }] as PaperVersion[];
    await render({ existingPapers });
    await act(async () => { await current.uploadBatch([file]); });
    expect(mocks.merge).toHaveBeenLastCalledWith([file], expect.objectContaining({ existingHashes: ["a".repeat(64)], allowExistingSourceFiles: undefined }));
    await act(async () => { await current.uploadBatch([file], { allowExistingSourceFiles: true }); });
    expect(mocks.merge).toHaveBeenLastCalledWith([file], expect.objectContaining({ existingHashes: ["a".repeat(64)], allowExistingSourceFiles: true }));
  });

  it("reports a duplicate without uploading, registering, or retrying part of the batch", async () => {
    mocks.merge.mockRejectedValue(new Error("“front.png”已经上传过，请勿重复上传。"));
    await render();
    await act(async () => { await current.uploadBatch([file, file]); });
    expect(current.error).toContain("已经上传过");
    expect(mocks.warning).toHaveBeenCalledWith(expect.stringContaining("已经上传过"));
    expect(current.canRetry).toBe(false);
    expect(current.uploading).toBe(false);
    expect(mocks.upload).not.toHaveBeenCalled();
    expect(mocks.register).not.toHaveBeenCalled();
    await act(async () => { await current.retry(); });
    expect(mocks.merge).toHaveBeenCalledOnce();
    expect(mocks.upload).not.toHaveBeenCalled();
  });

  it("checks persisted original-image hashes only on upload and reuses the result for the same PDF", async () => {
    await render({ existingPapers: [combinedPaper] });
    expect(mocks.download).not.toHaveBeenCalled();
    mocks.merge.mockRejectedValue(new Error("“renamed-front.png”已经上传过，请勿重复上传。"));
    await act(async () => { await current.upload(file); });
    expect(mocks.download).toHaveBeenCalledWith("combined-asset");
    expect(mocks.merge).toHaveBeenLastCalledWith([file], expect.objectContaining({
      existingHashes: ["b".repeat(64)], existingSourceHashes: ["c".repeat(64)]
    }));
    expect(current.error).toContain("已经上传过");
    expect(mocks.upload).not.toHaveBeenCalled();
    expect(mocks.register).not.toHaveBeenCalled();
    await act(async () => { await current.upload(file); });
    expect(mocks.download).toHaveBeenCalledOnce();
    expect(mocks.readSourceHashes).toHaveBeenCalledOnce();
  });

  it("does not reuse original-image hashes after the same PDF asset content changes", async () => {
    await render({ existingPapers: [combinedPaper] });
    await act(async () => { await current.upload(file); });
    await render({ existingPapers: [{ ...combinedPaper, file: { ...combinedPaper.file, hash_sha256: "d".repeat(64) } }] });
    await act(async () => { await current.upload(file); });
    expect(mocks.download).toHaveBeenCalledTimes(2);
    expect(mocks.readSourceHashes).toHaveBeenCalledTimes(2);
  });

  it("allows explicit front-back combination without downloading existing source manifests", async () => {
    await render({ existingPapers: [combinedPaper] });
    await act(async () => { await current.uploadBatch([file], { allowExistingSourceFiles: true }); });
    expect(mocks.download).not.toHaveBeenCalled();
    expect(mocks.merge).toHaveBeenLastCalledWith([file], expect.objectContaining({
      existingHashes: ["b".repeat(64)], existingSourceHashes: [], allowExistingSourceFiles: true
    }));
  });

  it("does not upload when an existing PDF cannot be checked and retries its download on the next selection", async () => {
    mocks.download.mockRejectedValueOnce(new Error("下载暂不可用"));
    await render({ existingPapers: [combinedPaper] });
    await act(async () => { await current.upload(file); });
    expect(current.error).toContain("下载暂不可用");
    expect(current.canRetry).toBe(false);
    expect(mocks.merge).not.toHaveBeenCalled();
    expect(mocks.upload).not.toHaveBeenCalled();
    await act(async () => { await current.upload(file); });
    expect(mocks.download).toHaveBeenCalledTimes(2);
    expect(mocks.upload).toHaveBeenCalledOnce();
  });

  for (const boundary of ["exam", "permission", "identity", "unmount"] as const) {
    it(`ignores duplicate-check results after the ${boundary} changes`, async () => {
      const waiting = deferred<{ blob: Blob }>(); mocks.download.mockReturnValueOnce(waiting.promise);
      await render({ existingPapers: [combinedPaper] });
      let pending!: Promise<void>;
      await act(async () => { pending = current.upload(file); });
      expect(current.phase).toBe("checking");
      if (boundary === "exam") await render({ examId: "exam-2", existingPapers: [combinedPaper] });
      else if (boundary === "permission") { await render({ canManage: false }); await render({ canManage: true, existingPapers: [combinedPaper] }); }
      else if (boundary === "identity") await render({ identityKey: "account-2", existingPapers: [combinedPaper] });
      else await act(async () => { root.unmount(); mounted = false; });
      await act(async () => { waiting.resolve({ blob: new Blob(["old PDF"]) }); await pending; });
      expect(mocks.readSourceHashes).not.toHaveBeenCalled();
      expect(mocks.merge).not.toHaveBeenCalled();
      expect(mocks.upload).not.toHaveBeenCalled();
      expect(mocks.register).not.toHaveBeenCalled();
      if (boundary !== "unmount") {
        await act(async () => { await current.upload(file); });
        // 换考试后不能再读取旧考试来源；同一考试换身份则重新取本场清单。
        expect(mocks.download).toHaveBeenCalledTimes(boundary === "exam" ? 1 : 2);
      }
    });
  }

  for (const boundary of ["exam", "permission", "identity", "unmount"] as const) {
    it(`does not start a network upload when the ${boundary} changes during merging`, async () => {
      const waiting = deferred<File>(); mocks.merge.mockReturnValue(waiting.promise);
      await render(); let pending!: Promise<void>;
      await act(async () => { pending = current.uploadBatch([file, file]); });
      if (boundary === "exam") await render({ examId: "exam-2" });
      else if (boundary === "permission") { await render({ canManage: false }); await render({ canManage: true }); }
      else if (boundary === "identity") await render({ identityKey: "account-2" });
      else await act(async () => { root.unmount(); mounted = false; });
      await act(async () => { waiting.resolve(file); await pending; });
      expect(mocks.upload).not.toHaveBeenCalled();
      expect(mocks.register).not.toHaveBeenCalled();
      expect(mocks.onCreated).not.toHaveBeenCalled();
    });
  }

  it("drops the pending asset when identity changes before registration retry", async () => {
    mocks.register.mockRejectedValueOnce(new Error("failed registration"));
    await render(); await act(async () => { await current.uploadBatch([file]); });
    expect(current.canRetry).toBe(true);
    await render({ identityKey: "account-2" });
    await act(async () => { await current.retry(); });
    expect(current.canRetry).toBe(false);
    expect(mocks.register).toHaveBeenCalledTimes(1);
  });

  it("retries only registration of a merged asset and does not merge or upload again", async () => {
    mocks.register.mockRejectedValueOnce(new Error("failed registration"));
    await render(); await act(async () => { await current.uploadBatch([file, file]); });
    await act(async () => { await current.retry(); });
    expect(mocks.merge).toHaveBeenCalledTimes(1);
    expect(mocks.upload).toHaveBeenCalledTimes(1);
    expect(mocks.register).toHaveBeenCalledTimes(2);
  });

  it("exposes upload and registration phases until the corresponding network requests finish", async () => {
    const uploading = deferred();
    const registering = deferred<{ paper: typeof source }>();
    mocks.upload.mockReturnValue(uploading.promise);
    mocks.register.mockReturnValue(registering.promise);
    await render(); let pending!: Promise<void>;
    await act(async () => { pending = current.uploadBatch([file]); });
    expect(current.phase).toBe("uploading");
    await act(async () => { mocks.upload.mock.calls[0][2](65); });
    expect(current.progress).toBe(65);
    await act(async () => { uploading.resolve({ file: asset }); });
    expect(current.phase).toBe("registering");
    expect(current.uploading).toBe(true);
    await act(async () => { registering.resolve({ paper: source }); await pending; });
    expect(current.phase).toBe("idle");
    expect(current.uploading).toBe(false);
  });

  it("does not start an upload through a retained callback after the page unmounts", async () => {
    await render();
    const upload = current.uploadBatch;
    await act(async () => { root.unmount(); mounted = false; });
    await upload([file]);
    expect(mocks.merge).not.toHaveBeenCalled();
    expect(mocks.upload).not.toHaveBeenCalled();
  });

  it("requires an exam before reading or uploading the batch", async () => {
    await render({ examId: "" });
    await act(async () => { await current.uploadBatch([file]); });
    expect(mocks.merge).not.toHaveBeenCalled();
    expect(mocks.upload).not.toHaveBeenCalled();
  });

  it("limits content duplicate checks to the current exam and answer-sheet purpose", async () => {
    await render({ existingPapers: [combinedPaper, { ...combinedPaper, id: "another-exam", exam_id: "exam-2" },
      { ...combinedPaper, id: "question-source", purpose: "question_paper" }] });
    await act(async () => current.upload(file));
    expect(mocks.download).toHaveBeenCalledOnce();
    expect(mocks.merge).toHaveBeenCalledWith([file], expect.objectContaining({ existingHashes: [combinedPaper.file.hash_sha256] }));
  });

  it("allows the same name with changed content and explains that a new source was kept", async () => {
    await render({ existingPapers: [{ ...combinedPaper, file: { ...combinedPaper.file, original_name: file.name } }] });
    await act(async () => current.upload(file));
    expect(mocks.onCreated).toHaveBeenCalledWith(source);
    expect(mocks.success).toHaveBeenCalledWith(expect.stringContaining("同名文件内容不同"));
  });

  it("uses the existing source returned by registration idempotency and reports the duplicate explicitly", async () => {
    mocks.register.mockResolvedValue({ paper: { ...source, file: { original_name: "已有答题卡.pdf" } }, duplicate: true });
    await render(); await act(async () => current.upload(file));
    expect(mocks.onCreated).toHaveBeenCalledWith(expect.objectContaining({ id: source.id }));
    expect(mocks.info).toHaveBeenCalledWith(expect.stringContaining("已上传过"));
    expect(mocks.success).not.toHaveBeenCalled();
  });

  it("requests multimodal ordering for multiple files and never for a single source", async () => {
    mocks.order.mockResolvedValue([1, 0]);
    mocks.merge.mockImplementation(async (files: readonly File[], options: { resolvePageOrder?: (sources: readonly File[]) => Promise<number[]> }) => {
      if (options.resolvePageOrder) await options.resolvePageOrder(files);
      return files[0];
    });
    const back = new File(["back"], "back.png", { type: "image/png" });
    await render(); await act(async () => current.upload(file));
    expect(mocks.order).not.toHaveBeenCalled();
    await act(async () => current.uploadBatch([back, file]));
    expect(mocks.order).toHaveBeenCalledWith([back, file], expect.objectContaining({ examId: "exam-1", signal: expect.any(AbortSignal) }));
    expect(mocks.upload).toHaveBeenCalledTimes(2);
  });

  it.each(["uncertain", "identity"] as const)("does not upload after %s page-order results", async boundary => {
    const waiting = deferred<number[]>(); mocks.order.mockReturnValue(waiting.promise);
    mocks.merge.mockImplementation(async (files: readonly File[], options: { resolvePageOrder?: (sources: readonly File[]) => Promise<number[]> }) => {
      await options.resolvePageOrder?.(files); return files[0];
    });
    await render(); let pending!: Promise<void>;
    await act(async () => { pending = current.uploadBatch([file, file]); });
    await vi.waitFor(() => expect(mocks.order).toHaveBeenCalledOnce());
    const signal = mocks.order.mock.calls[0][1].signal as AbortSignal;
    expect(current.phase).toBe("ordering");
    if (boundary === "identity") {
      await render({ identityKey: "account-2" });
      expect(signal.aborted).toBe(true);
      await act(async () => { waiting.resolve([1, 0]); await pending; });
    } else {
      // 不确定结果由页序模块抛错，不能回退为用户选择顺序继续上传。
      mocks.order.mockRejectedValueOnce(new Error("多模态模型无法可靠确定整套答题卡页序"));
      waiting.resolve([1, 0]); await act(async () => pending);
      mocks.upload.mockClear(); mocks.register.mockClear();
      await act(async () => current.uploadBatch([file, file]));
      expect(current.error).toContain("无法可靠确定");
    }
    expect(mocks.upload).not.toHaveBeenCalled(); expect(mocks.register).not.toHaveBeenCalled();
  });
});
