// @vitest-environment jsdom
import type { ReactNode } from "react";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PaperImportJob, PaperImportSource, PaperVersion } from "../../api/papers";
import { PaperMaterialsConfirmation, registerConfirmedPaperMaterials } from "./PaperMaterialsConfirmation";

const api = vi.hoisted(() => ({ getPaperImport: vi.fn(), registerPaperFromFile: vi.fn() }));
vi.mock("../../api/papers", () => api);
vi.mock("../../api/client", () => ({ getUserErrorMessage: (error: unknown, fallback: string) => error instanceof Error ? error.message : fallback }));
vi.mock("antd", () => ({
  Alert: ({ message, description, action }: { message: ReactNode; description: ReactNode; action: ReactNode }) => <aside>{message}{description}{action}</aside>,
  Button: ({ children, disabled, loading, onClick }: { children: ReactNode; disabled?: boolean; loading?: boolean; onClick: () => void }) => <button disabled={disabled || loading} onClick={onClick}>{children}</button>,
  Checkbox: ({ children, checked, disabled, onChange }: { children: ReactNode; checked: boolean; disabled: boolean; onChange: (event: { target: { checked: boolean } }) => void }) => <label><input type="checkbox" checked={checked} disabled={disabled} onChange={onChange} />{children}</label>,
  Modal: ({ open, children, onOk, onCancel, confirmLoading, okButtonProps }: { open: boolean; children: ReactNode; onOk: () => void; onCancel: () => void; confirmLoading: boolean; okButtonProps: { disabled: boolean } }) => open ? <div role="dialog">{children}<button disabled={confirmLoading || okButtonProps.disabled} onClick={onOk}>提交确认</button><button onClick={onCancel}>取消</button></div> : null,
  Space: ({ children }: { children: ReactNode }) => <div>{children}</div>,
}));

function source(asset = "file-1", patch: Partial<PaperImportSource> = {}): PaperImportSource {
  return { id: `source-${asset}`, file_asset_id: asset, document_index: 0, role_hint: "auto", detected_role: "mixed", processing_status: "processed", role_confidence: .95, original_name: `${asset}.pdf`, ...patch };
}
function job(patch: Partial<PaperImportJob> = {}): PaperImportJob {
  return { id: "import-1", exam_id: "exam-1", generation: 2, structure_confirmed_generation: 2, status: "review_required", source_revision: "r2", sources: [source()], questions: [], ...patch } as PaperImportJob;
}
function paper(asset = "file-1", purpose: PaperVersion["purpose"] = "question_paper"): PaperVersion {
  return { id: `paper-${asset}`, tenant_id: "tenant-1", exam_id: "exam-1", file_asset_id: asset, version_no: 1, status: "uploaded", purpose, file: { original_name: `${asset}.pdf`, content_type: "application/pdf", size_bytes: 1, hash_sha256: "hash" } };
}
function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: unknown) => void;
  const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail; });
  return { promise, resolve, reject };
}

describe("exam materials confirmation", () => {
  const onConfirmed = vi.fn<() => Promise<void>>();
  beforeEach(() => {
    vi.clearAllMocks();
    onConfirmed.mockResolvedValue(undefined);
    api.getPaperImport.mockResolvedValue({ import: job() });
    api.registerPaperFromFile.mockImplementation(async (_examId: string, asset: string) => ({ paper: paper(asset), note: "" }));
  });
  afterEach(cleanup);
  function mount(activeJob = job(), papers: PaperVersion[] = [], canManage = true, disabled = false) {
    return render(<PaperMaterialsConfirmation job={activeJob} papers={papers} canManage={canManage} disabled={disabled} onConfirmed={onConfirmed} />);
  }
  function openAndCheck() {
    fireEvent.click(screen.getByRole("button", { name: "确认考试资料" }));
    fireEvent.click(screen.getByRole("checkbox", { name: "我已核对试卷资料完整，题目范围与实际试卷一致" }));
  }
  async function submit() {
    fireEvent.click(screen.getByRole("button", { name: "提交确认" }));
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  }

  it("requires manual acknowledgment and only registers distinct processed question sources", async () => {
    const current = job({ sources: [source(), source("file-1", { id: "duplicate", document_index: 1 }), source("file-2", { detected_role: "question" }), source("answers", { detected_role: "answer" }), source("solutions", { detected_role: "solution" })] });
    api.getPaperImport.mockResolvedValue({ import: current });
    mount(current, [paper("file-2")]);
    fireEvent.click(screen.getByRole("button", { name: "确认考试资料" }));
    expect(screen.getByRole<HTMLButtonElement>("button", { name: "提交确认" }).disabled).toBe(true);
    expect(screen.getByRole("dialog").textContent).toContain("file-1.pdf");
    expect(screen.getByRole("dialog").textContent).not.toContain("answers.pdf");
    expect(screen.getByRole("dialog").textContent).not.toContain("file-2.pdf");
    fireEvent.click(screen.getByRole("checkbox"));
    await submit();
    expect(api.getPaperImport).toHaveBeenCalledExactlyOnceWith("import-1");
    expect(api.registerPaperFromFile).toHaveBeenCalledExactlyOnceWith("exam-1", "file-1");
    expect(onConfirmed).toHaveBeenCalledOnce();
    expect(screen.getByText("资料已登记")).toBeDefined();
  });

  it("keeps already registered question papers complete without making another request", () => {
    mount(job({ status: "applied", structure_confirmed_generation: undefined }), [paper()]);
    expect(screen.getByText("资料已登记")).toBeDefined();
    expect(screen.queryByRole("button", { name: "确认考试资料" })).toBeNull();
    expect(api.getPaperImport).not.toHaveBeenCalled();
    expect(api.registerPaperFromFile).not.toHaveBeenCalled();
  });

  it("does not mistake an answer sheet registration for the question paper", async () => {
    mount(job(), [paper("file-1", "answer_sheet")]);
    openAndCheck();
    await submit();
    expect(api.registerPaperFromFile).toHaveBeenCalledExactlyOnceWith("exam-1", "file-1");
  });

  it("does not use a different exam's paper receipt as completion evidence", async () => {
    mount(job(), [{ ...paper(), exam_id: "exam-2" }]);
    expect(screen.queryByText("资料已登记")).toBeNull();
    openAndCheck();
    await submit();
    expect(api.registerPaperFromFile).toHaveBeenCalledExactlyOnceWith("exam-1", "file-1");
  });

  it.each([
    job({ structure_confirmed_generation: 1 }),
    job({ sources: [source("answers", { detected_role: "answer" })] }),
    job({ sources: [source("pending", { processing_status: "processing" })] }),
    job({ sources: [source("")] }),
    job({ sources: [source(), source("failed", { detected_role: "question", processing_status: "failed" })] }),
  ])("rejects an unconfirmed or incomplete question material set", (current) => {
    mount(current);
    const button = screen.getByRole<HTMLButtonElement>("button", { name: "确认考试资料" });
    expect(button.disabled).toBe(true);
    fireEvent.click(button);
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(api.getPaperImport).not.toHaveBeenCalled();
    expect(api.registerPaperFromFile).not.toHaveBeenCalled();
  });

  it.each([
    job({ generation: 3, structure_confirmed_generation: 3 }),
    job({ sources: [source("replaced")] }),
    job({ structure_confirmed_generation: 1 }),
  ])("rechecks current generation and source identity before writing", async (fresh) => {
    api.getPaperImport.mockResolvedValue({ import: fresh });
    mount();
    openAndCheck();
    await submit();
    expect(api.registerPaperFromFile).not.toHaveBeenCalled();
    expect(onConfirmed).toHaveBeenCalledOnce();
  });

  it("uses a synchronous lock for repeated confirmation clicks", async () => {
    const fresh = deferred<{ import: PaperImportJob }>();
    api.getPaperImport.mockReturnValue(fresh.promise);
    mount();
    openAndCheck();
    const button = screen.getByRole("button", { name: "提交确认" });
    fireEvent.click(button);
    fireEvent.click(button);
    expect(api.getPaperImport).toHaveBeenCalledOnce();
    await act(async () => fresh.resolve({ import: job() }));
    await waitFor(() => expect(onConfirmed).toHaveBeenCalledOnce());
    expect(api.registerPaperFromFile).toHaveBeenCalledOnce();
  });

  it("retains successful registrations and retries only the remaining source after partial failure", async () => {
    const current = job({ sources: [source(), source("file-2")] });
    api.getPaperImport.mockResolvedValue({ import: current });
    api.registerPaperFromFile.mockResolvedValueOnce({ paper: paper(), note: "" }).mockRejectedValueOnce(new Error("network error")).mockResolvedValueOnce({ paper: paper("file-2"), note: "" });
    mount(current);
    openAndCheck();
    fireEvent.click(screen.getByRole("button", { name: "提交确认" }));
    await waitFor(() => expect(screen.getAllByRole("alert")[0].textContent).toContain("已登记 1 / 2"));
    expect(onConfirmed).not.toHaveBeenCalled();
    await submit();
    expect(api.registerPaperFromFile.mock.calls).toEqual([["exam-1", "file-1"], ["exam-1", "file-2"], ["exam-1", "file-2"]]);
    expect(onConfirmed).toHaveBeenCalledOnce();
  });

  it("recovers a failed result read without submitting registrations again", async () => {
    onConfirmed.mockRejectedValueOnce(new Error("read unavailable")).mockResolvedValueOnce(undefined);
    mount();
    openAndCheck();
    await submit();
    expect(screen.getByRole("button", { name: "重新读取确认结果" })).toBeDefined();
    expect(screen.getByText("资料已登记，等待状态刷新")).toBeDefined();
    expect(screen.queryByText("资料已登记")).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "重新读取确认结果" }));
    await waitFor(() => expect(screen.queryByRole("button", { name: "重新读取确认结果" })).toBeNull());
    expect(onConfirmed).toHaveBeenCalledTimes(2);
    expect(api.getPaperImport).toHaveBeenCalledOnce();
    expect(api.registerPaperFromFile).toHaveBeenCalledOnce();
  });

  it("does not let an old A response affect a newly reopened A scope after switching exams", async () => {
    const fresh = deferred<{ import: PaperImportJob }>();
    api.getPaperImport.mockReturnValue(fresh.promise);
    const view = mount();
    openAndCheck();
    fireEvent.click(screen.getByRole("button", { name: "提交确认" }));
    view.rerender(<PaperMaterialsConfirmation job={job({ exam_id: "exam-2", id: "import-2" })} papers={[]} canManage disabled={false} onConfirmed={onConfirmed} />);
    view.rerender(<PaperMaterialsConfirmation job={job()} papers={[]} canManage disabled={false} onConfirmed={onConfirmed} />);
    await act(async () => fresh.resolve({ import: job() }));
    expect(api.registerPaperFromFile).not.toHaveBeenCalled();
    expect(onConfirmed).not.toHaveBeenCalled();
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.queryByText("资料已登记")).toBeNull();
    expect(screen.getByRole<HTMLButtonElement>("button", { name: "确认考试资料" }).disabled).toBe(false);
  });

  it.each([{ canManage: false, disabled: false }, { canManage: true, disabled: true }])("stops a pending confirmation when write access changes", async (access) => {
    const fresh = deferred<{ import: PaperImportJob }>();
    api.getPaperImport.mockReturnValue(fresh.promise);
    const view = mount();
    openAndCheck();
    fireEvent.click(screen.getByRole("button", { name: "提交确认" }));
    view.rerender(<PaperMaterialsConfirmation job={job()} papers={[]} {...access} onConfirmed={onConfirmed} />);
    await act(async () => fresh.resolve({ import: job() }));
    expect(api.registerPaperFromFile).not.toHaveBeenCalled();
    expect(onConfirmed).not.toHaveBeenCalled();
    expect(screen.queryByText("资料已登记")).toBeNull();
    expect(screen.getAllByRole("alert")[0].textContent).toContain("确认操作已暂停");
  });

  it("retains a successful write receipt when access changes before the second source", async () => {
    const current = job({ sources: [source(), source("file-2")] });
    const first = deferred<{ paper: PaperVersion; note: string }>();
    api.getPaperImport.mockResolvedValue({ import: current });
    api.registerPaperFromFile.mockReturnValueOnce(first.promise);
    const view = mount(current);
    openAndCheck();
    fireEvent.click(screen.getByRole("button", { name: "提交确认" }));
    await waitFor(() => expect(api.registerPaperFromFile).toHaveBeenCalledOnce());
    view.rerender(<PaperMaterialsConfirmation job={current} papers={[]} canManage disabled onConfirmed={onConfirmed} />);
    await act(async () => first.resolve({ paper: paper(), note: "" }));
    expect(api.registerPaperFromFile).toHaveBeenCalledOnce();
    expect(onConfirmed).not.toHaveBeenCalled();
    expect(screen.getAllByRole("alert")[0].textContent).toContain("已登记 1 / 2");
    expect(screen.queryByText("资料已登记")).toBeNull();
    view.rerender(<PaperMaterialsConfirmation job={current} papers={[]} canManage disabled={false} onConfirmed={onConfirmed} />);
    await submit();
    expect(api.registerPaperFromFile.mock.calls).toEqual([["exam-1", "file-1"], ["exam-1", "file-2"]]);
    expect(onConfirmed).toHaveBeenCalledOnce();
  });

  it("stops before another registration when the current exam changes during the first write", async () => {
    const current = job({ sources: [source(), source("file-2")] });
    const first = deferred<{ paper: PaperVersion; note: string }>();
    api.getPaperImport.mockResolvedValue({ import: current });
    api.registerPaperFromFile.mockReturnValue(first.promise);
    const view = mount(current);
    openAndCheck();
    fireEvent.click(screen.getByRole("button", { name: "提交确认" }));
    await waitFor(() => expect(api.registerPaperFromFile).toHaveBeenCalledOnce());
    view.rerender(<PaperMaterialsConfirmation job={job({ exam_id: "exam-2", id: "import-2" })} papers={[]} canManage disabled={false} onConfirmed={onConfirmed} />);
    await act(async () => first.resolve({ paper: paper(), note: "" }));
    expect(api.registerPaperFromFile).toHaveBeenCalledOnce();
    expect(onConfirmed).not.toHaveBeenCalled();
  });

  it("does not continue registering after the confirmation component is unmounted", async () => {
    const fresh = deferred<{ import: PaperImportJob }>();
    api.getPaperImport.mockReturnValue(fresh.promise);
    const view = mount();
    openAndCheck();
    fireEvent.click(screen.getByRole("button", { name: "提交确认" }));
    view.unmount();
    await act(async () => fresh.resolve({ import: job() }));
    expect(api.registerPaperFromFile).not.toHaveBeenCalled();
    expect(onConfirmed).not.toHaveBeenCalled();
  });

  it("also registers a just-adopted structure using the original preview job identity", async () => {
    await registerConfirmedPaperMaterials(job({ structure_confirmed_generation: undefined }), [], () => true);
    expect(api.registerPaperFromFile).toHaveBeenCalledExactlyOnceWith("exam-1", "file-1");
  });
});
