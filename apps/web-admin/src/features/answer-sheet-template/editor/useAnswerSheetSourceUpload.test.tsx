// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useAnswerSheetSourceUpload } from "./useAnswerSheetSourceUpload";

const mocks = vi.hoisted(() => ({ upload: vi.fn(), register: vi.fn(), onCreated: vi.fn(), success: vi.fn() }));
vi.mock("antd", () => ({ App: { useApp: () => ({ message: { success: mocks.success } }) } }));
vi.mock("../../../api/files", () => ({ uploadFileWithProgress: mocks.upload }));
vi.mock("../../../api/papers", () => ({ registerPaperFromFile: mocks.register }));
const file = new File(["synthetic"], "blank-answer-sheet.pdf", { type: "application/pdf" });
const asset = { id: "asset-sheet" };
const source = { id: "source-sheet", purpose: "answer_sheet" };
function deferred() {
  let resolve!: (value: { file: typeof asset }) => void;
  const promise = new Promise<{ file: typeof asset }>(done => { resolve = done; });
  return { promise, resolve };
}

describe("independent answer sheet upload", () => {
  let root: Root;
  let container: HTMLDivElement;
  let current: ReturnType<typeof useAnswerSheetSourceUpload>;
  let mounted: boolean;
  function Probe({ examId = "exam-1", canManage = true }: { examId?: string; canManage?: boolean }) {
    current = useAnswerSheetSourceUpload({ examId, canManage, onCreated: mocks.onCreated });
    return null;
  }
  beforeEach(() => {
    vi.resetAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.upload.mockResolvedValue({ file: asset });
    mocks.register.mockResolvedValue({ paper: source });
    container = document.createElement("div"); document.body.appendChild(container);
    root = createRoot(container); mounted = true;
  });
  afterEach(async () => {
    if (mounted) await act(async () => { root.unmount(); });
    container.remove();
  });
  async function render(props: { examId?: string; canManage?: boolean } = {}) { await act(async () => { root.render(<Probe {...props} />); }); }

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

  for (const boundary of ["exam", "permission", "unmount"] as const) {
    it(`ignores an upload finishing after the ${boundary} changes`, async () => {
      const waiting = deferred(); mocks.upload.mockReturnValue(waiting.promise);
      await render(); let pending!: Promise<void>;
      await act(async () => { pending = current.upload(file); });
      if (boundary === "exam") await render({ examId: "exam-2" });
      else if (boundary === "permission") await render({ canManage: false });
      else await act(async () => { root.unmount(); mounted = false; });
      await act(async () => { waiting.resolve({ file: asset }); await pending; });
      expect(mocks.register).not.toHaveBeenCalled();
      expect(mocks.onCreated).not.toHaveBeenCalled();
    });
  }

  it("does not upload when the account cannot manage answer sheets", async () => {
    await render({ canManage: false }); await act(async () => { await current.upload(file); });
    expect(mocks.upload).not.toHaveBeenCalled();
  });
});
