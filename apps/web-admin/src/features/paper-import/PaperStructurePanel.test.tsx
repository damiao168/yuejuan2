// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PaperImportJob, PaperStructurePreview, PaperVersion } from "../../api/papers";
import { PaperStructurePanel } from "./PaperStructurePanel";

const mocks = vi.hoisted(() => ({
  preview: vi.fn(),
  adopt: vi.fn(),
  register: vi.fn(),
  success: vi.fn(),
  error: vi.fn(),
  modalSubmit: undefined as (() => void) | undefined,
}));

vi.mock("../../api/papers", () => ({
  previewPaperStructure: mocks.preview,
  adoptPaperStructure: mocks.adopt,
}));
vi.mock("../../api/client", () => ({
  getUserErrorMessage: (error: unknown, fallback: string) => error instanceof Error ? error.message : fallback,
}));
vi.mock("./PaperMaterialsConfirmation", () => ({ registerConfirmedPaperMaterials: mocks.register }));
vi.mock("antd", () => ({
  App: { useApp: () => ({ message: { success: mocks.success, error: mocks.error } }) },
  Alert: ({ message, description, action }: { message: ReactNode; description?: ReactNode; action?: ReactNode }) => <aside>{message}{description}{action}</aside>,
  Button: ({ children, disabled, onClick }: { children: ReactNode; disabled?: boolean; onClick?: () => void }) => <button disabled={disabled} onClick={onClick}>{children}</button>,
  Checkbox: ({ checked, disabled, onChange, children }: { checked: boolean; disabled?: boolean; onChange: (event: { target: { checked: boolean } }) => void; children: ReactNode }) => <label><input type="checkbox" checked={checked} disabled={disabled} onChange={onChange} />{children}</label>,
  Modal: ({ open, title, children, onOk, onCancel, okText, okButtonProps }: {
    open: boolean; title: ReactNode; children: ReactNode; onOk: () => void; onCancel: () => void; okText: string; okButtonProps: { disabled: boolean };
  }) => {
    if (!open) return null;
    mocks.modalSubmit = onOk;
    return <div role="dialog" aria-label={typeof title === "string" ? title : undefined}>{title}{children}<button disabled={okButtonProps.disabled} onClick={onOk}>{okText}</button><button onClick={onCancel}>取消</button></div>;
  },
  Space: ({ children }: { children: ReactNode }) => <div>{children}</div>,
  Table: ({ dataSource }: { dataSource: unknown[] }) => <div data-preview-changes={dataSource.length} />,
}));

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((accept, decline) => { resolve = accept; reject = decline; });
  return { promise, resolve, reject };
}

function job(overrides: Partial<PaperImportJob> = {}): PaperImportJob {
  return {
    id: "import-1", exam_id: "exam-1", exam_paper_id: "", paper_file_asset_id: "", answer_file_asset_id: "",
    status: "review_required", generation: 7, run_id: "run-7", source_revision: "revision-7", subject: "mathematics",
    created_at: "2026-10-12T00:00:00Z", updated_at: "2026-10-12T01:00:00Z",
    sources: [{ id: "source-1", file_asset_id: "file-1", document_index: 0, role_hint: "auto", detected_role: "mixed", role_confidence: .95, processing_status: "processed" }],
    questions: [], question_candidates: [], answer_candidates: [], solution_candidates: [], rubric_candidates: [], structured_issues: [], issues: [],
    ...overrides,
  };
}

function preview(overrides: Partial<PaperStructurePreview> = {}): PaperStructurePreview {
  return { import_id: "import-1", generation: 7, preview_hash: "initial-hash", current_count: 20, recognized_count: 19,
    expected_total: 150, recognized_total: 90, missing_scores: ["19"], changes: [], blocked_reasons: [], paper_detected: true, confirmed: false, ...overrides };
}

describe("paper structure and material confirmation", () => {
  let root: Root;
  let container: HTMLDivElement;
  let mounted: boolean;
  const onAdopted = vi.fn<() => Promise<void>>();
  const papers = [{ id: "paper-old", exam_id: "exam-1", file_asset_id: "other-file", purpose: "question_paper" }] as PaperVersion[];

  beforeEach(() => {
    vi.resetAllMocks();
    mocks.modalSubmit = undefined;
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.preview.mockResolvedValue(preview());
    mocks.adopt.mockResolvedValue(preview({ confirmed: true }));
    mocks.register.mockResolvedValue(undefined);
    onAdopted.mockResolvedValue(undefined);
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
    mounted = true;
  });
  afterEach(async () => { if (mounted) await act(async () => root.unmount()); container.remove(); });

  async function mount(activeJob = job(), disabled = false) {
    await act(async () => root.render(<PaperStructurePanel job={activeJob} papers={papers} disabled={disabled} onAdopted={onAdopted} />));
  }
  function button(label: string) {
    const result = Array.from(container.querySelectorAll("button")).find((item) => item.textContent === label);
    expect(result, label).toBeDefined();
    return result!;
  }
  async function click(label: string) { await act(async () => button(label).click()); }
  async function checkComplete() { await act(async () => container.querySelector<HTMLInputElement>("input[type=checkbox]")!.click()); }
  async function openConfirmedModal(activeJob = job()) {
    await mount(activeJob);
    await click("确认考试资料");
    await checkComplete();
  }

  it("requires human confirmation and submits the freshly reviewed generation and hash", async () => {
    const activeJob = job();
    mocks.preview.mockResolvedValueOnce(preview()).mockResolvedValueOnce(preview({ preview_hash: "fresh-hash" }));
    await mount(activeJob);
    await click("确认考试资料");
    expect(container.querySelector("[role=dialog]")?.getAttribute("aria-label")).toBe("确认考试资料与题目结构");
    expect(button("确认资料并采用题目结构").disabled).toBe(true);
    await act(async () => mocks.modalSubmit!());
    expect(mocks.adopt).not.toHaveBeenCalled();
    await checkComplete();
    await click("确认资料并采用题目结构");
    expect(mocks.adopt).toHaveBeenCalledWith("import-1", { expected_generation: 7, preview_hash: "fresh-hash", complete_paper_confirmed: true });
    expect(mocks.register).toHaveBeenCalledWith(activeJob, papers, expect.any(Function));
    expect(onAdopted).toHaveBeenCalledOnce();
  });

  it("adopts structure, registers materials, rereads state and only then reports success", async () => {
    const order: string[] = [];
    mocks.adopt.mockImplementation(async () => { order.push("adopt"); return preview({ confirmed: true }); });
    mocks.register.mockImplementation(async () => { order.push("register"); });
    onAdopted.mockImplementation(async () => { order.push("refresh"); });
    mocks.success.mockImplementation(() => { order.push("success"); });
    await openConfirmedModal();
    await click("确认资料并采用题目结构");
    expect(order).toEqual(["adopt", "register", "refresh", "success"]);
    expect(mocks.error).not.toHaveBeenCalled();
  });

  it("does not register materials when structure adoption is rejected", async () => {
    mocks.adopt.mockRejectedValueOnce(new Error("预览已过期"));
    await openConfirmedModal();
    await click("确认资料并采用题目结构");
    expect(mocks.register).not.toHaveBeenCalled();
    expect(onAdopted).not.toHaveBeenCalled();
    expect(mocks.success).not.toHaveBeenCalled();
    expect(mocks.error).toHaveBeenCalledWith(expect.stringContaining("预览已过期"));
  });

  it("rereads committed structure after registration fails without claiming materials completed", async () => {
    mocks.register.mockRejectedValueOnce(new Error("资料登记失败"));
    await openConfirmedModal();
    await click("确认资料并采用题目结构");
    expect(onAdopted).toHaveBeenCalledOnce();
    expect(mocks.success).not.toHaveBeenCalled();
    expect(mocks.error).toHaveBeenCalledWith(expect.stringContaining("题目结构已采用，资料登记或状态刷新未完成"));
    expect(mocks.error).toHaveBeenCalledWith(expect.stringContaining("请重试确认考试资料"));
  });

  it("does not claim completion when registration succeeds but the state reread fails", async () => {
    onAdopted.mockRejectedValueOnce(new Error("状态读取失败"));
    await openConfirmedModal();
    await click("确认资料并采用题目结构");
    expect(mocks.register).toHaveBeenCalledOnce();
    expect(mocks.success).not.toHaveBeenCalled();
    expect(mocks.error).toHaveBeenCalledWith(expect.stringContaining("状态读取失败"));
  });

  it("keeps a blocked preview from being adopted even when its modal callback is invoked", async () => {
    mocks.preview.mockResolvedValue(preview({ blocked_reasons: ["仅有答案不能作为完整试卷"] }));
    await openConfirmedModal();
    expect(button("确认资料并采用题目结构").disabled).toBe(true);
    await act(async () => mocks.modalSubmit!());
    expect(mocks.adopt).not.toHaveBeenCalled();
    expect(mocks.register).not.toHaveBeenCalled();
  });

  it("prevents a previously opened confirmation from writing after permission becomes disabled", async () => {
    const activeJob = job();
    await openConfirmedModal(activeJob);
    const staleSubmit = mocks.modalSubmit!;
    await mount(activeJob, true);
    await act(async () => staleSubmit());
    expect(mocks.adopt).not.toHaveBeenCalled();
    expect(mocks.register).not.toHaveBeenCalled();
  });

  it("prevents duplicate submissions before the saving state can rerender", async () => {
    const result = deferred<PaperStructurePreview>();
    mocks.adopt.mockReturnValueOnce(result.promise);
    await openConfirmedModal();
    const submit = mocks.modalSubmit!;
    await act(async () => { submit(); submit(); });
    expect(mocks.adopt).toHaveBeenCalledOnce();
    await act(async () => result.resolve(preview({ confirmed: true })));
    expect(mocks.register).toHaveBeenCalledOnce();
    expect(onAdopted).toHaveBeenCalledOnce();
  });

  it("does not revive an old dialog callback after switching away and back to the same import", async () => {
    const activeJob = job();
    await openConfirmedModal(activeJob);
    const oldSubmit = mocks.modalSubmit!;
    await mount(job({ id: "import-2", generation: 8 }));
    await mount(activeJob);
    await act(async () => oldSubmit());
    expect(mocks.adopt).not.toHaveBeenCalled();
    expect(mocks.register).not.toHaveBeenCalled();
    expect(onAdopted).not.toHaveBeenCalled();
  });

  it("allows retrying a failed initial preview and requires a new confirmation", async () => {
    mocks.preview.mockRejectedValueOnce(new Error("识别预览暂不可用")).mockResolvedValueOnce(preview({ preview_hash: "retry-hash" }));
    await mount();
    expect(container.textContent).toContain("识别预览暂不可用");
    await click("重试结构预览");
    expect(button("确认资料并采用题目结构").disabled).toBe(true);
    await checkComplete();
    await click("确认资料并采用题目结构");
    expect(mocks.adopt).toHaveBeenCalledWith("import-1", { expected_generation: 7, preview_hash: "retry-hash", complete_paper_confirmed: true });
  });

  it("does not continue an old adoption after switching to another import generation", async () => {
    const result = deferred<PaperStructurePreview>();
    mocks.adopt.mockReturnValueOnce(result.promise);
    await openConfirmedModal();
    await click("确认资料并采用题目结构");
    await mount(job({ generation: 8, run_id: "run-8", updated_at: "2026-10-12T02:00:00Z" }));
    await act(async () => result.resolve(preview({ confirmed: true })));
    expect(mocks.register).not.toHaveBeenCalled();
    expect(onAdopted).not.toHaveBeenCalled();
    expect(mocks.success).not.toHaveBeenCalled();
    expect(mocks.error).not.toHaveBeenCalled();
  });

  it("does not continue a completed old adoption after the panel unmounts", async () => {
    const result = deferred<PaperStructurePreview>();
    mocks.adopt.mockReturnValueOnce(result.promise);
    await openConfirmedModal();
    await click("确认资料并采用题目结构");
    await act(async () => root.unmount());
    mounted = false;
    await act(async () => result.resolve(preview({ confirmed: true })));
    expect(mocks.register).not.toHaveBeenCalled();
    expect(onAdopted).not.toHaveBeenCalled();
    expect(mocks.success).not.toHaveBeenCalled();
  });

  it("discards a late old preview instead of reopening it for the new import", async () => {
    const oldPreview = deferred<PaperStructurePreview>();
    mocks.preview.mockResolvedValueOnce(preview()).mockReturnValueOnce(oldPreview.promise).mockResolvedValueOnce(preview({ import_id: "import-2", generation: 8 }));
    await mount();
    await click("确认考试资料");
    await mount(job({ id: "import-2", generation: 8, updated_at: "2026-10-12T02:00:00Z" }));
    await act(async () => oldPreview.resolve(preview({ blocked_reasons: ["旧资料阻断原因"] })));
    expect(container.querySelector("[role=dialog]")).toBeNull();
    expect(container.textContent).not.toContain("旧资料阻断原因");
    expect(mocks.adopt).not.toHaveBeenCalled();
  });

  it("does not refresh a different import after registration returns late", async () => {
    const registration = deferred<void>();
    mocks.register.mockReturnValueOnce(registration.promise);
    await openConfirmedModal();
    await click("确认资料并采用题目结构");
    expect(mocks.register).toHaveBeenCalledOnce();
    const guard = mocks.register.mock.calls[0][2] as () => boolean;
    expect(guard()).toBe(true);
    await mount(job({ id: "import-2", generation: 8, updated_at: "2026-10-12T02:00:00Z" }));
    expect(guard()).toBe(false);
    await act(async () => registration.resolve(undefined));
    expect(onAdopted).not.toHaveBeenCalled();
    expect(mocks.success).not.toHaveBeenCalled();
  });

  it("discards an old in-flight preview response after panel unmount", async () => {
    const oldPreview = deferred<PaperStructurePreview>();
    mocks.preview.mockResolvedValueOnce(preview()).mockReturnValueOnce(oldPreview.promise);
    await mount();
    await click("确认考试资料");
    await act(async () => root.unmount());
    mounted = false;
    await act(async () => oldPreview.reject(new Error("旧预览请求失败")));
    expect(mocks.error).not.toHaveBeenCalled();
    expect(mocks.adopt).not.toHaveBeenCalled();
  });
});
