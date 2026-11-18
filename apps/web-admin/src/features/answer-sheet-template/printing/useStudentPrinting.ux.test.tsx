// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiClientError } from "../../../api/client";
import type { AnswerSheetTemplate } from "../../../api/configuration";
import type { StudentPrintCandidate } from "../../../api/printing";
import { useStudentPrinting } from "./useStudentPrinting";

const mocks = vi.hoisted(() => ({ context: vi.fn(), issue: vi.fn(), reprint: vi.fn(), download: vi.fn(), confirmLayout: vi.fn(), success: vi.fn(), warning: vi.fn(), error: vi.fn() }));
vi.mock("../../../api/printing", () => ({ getStudentPrintContext: mocks.context, issueStudentPrintBatch: mocks.issue, reprintStudentSheet: mocks.reprint, downloadStudentPrintPackage: mocks.download, confirmStudentPrintLayout: mocks.confirmLayout }));
vi.mock("antd", () => ({ App: { useApp: () => ({ message: { success: mocks.success, warning: mocks.warning, error: mocks.error } }) } }));
const candidate: StudentPrintCandidate = { student_id: "student", student_name: "学生甲", student_no: "01", class_id: "class", class_name: "高一一班", attendance_status: "expected", has_active_sheet: true, active_sheet_serial: "old-card" };
const template = { id: "template-1", status: "locked" } as AnswerSheetTemplate;

describe("student printing recovery", () => {
  let host: HTMLDivElement; let root: Root; let current: ReturnType<typeof useStudentPrinting>;
  function Probe({ selected = template, canManage = true }: Readonly<{ selected?: AnswerSheetTemplate; canManage?: boolean }>) { current = useStudentPrinting(selected, canManage); return null; }
  beforeEach(async () => {
    vi.clearAllMocks(); Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    vi.stubGlobal("URL", Object.assign(URL, { createObjectURL: vi.fn(() => "blob:print"), revokeObjectURL: vi.fn() }));
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
    mocks.context.mockImplementation(async (templateId: string) => ({ print_context: { template_id: templateId, candidates: [candidate], batches: [] } }));
    mocks.reprint.mockResolvedValue({ barcodes: { print_batch_id: "new-batch", students: [{ student_id: "student", sheet_serial: "new-card" }] } });
    mocks.download.mockResolvedValue({ blob: new Blob(["synthetic print package"]), filename: "print.pdf" });
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
    await act(async () => root.render(<Probe />));
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });
  async function prepare() { await act(async () => { current.openReprint(candidate); }); await act(async () => current.setReprintReason("原卡损坏")); }
  it("retries the same replacement after an unknown response and does not rewrite its reason", async () => {
    await prepare(); mocks.reprint.mockRejectedValueOnce(new Error("response lost"));
    await act(async () => current.reprint());
    expect(current.reprintReasonFrozen).toBe(true); expect(current.reprintError).toBeTruthy();
    await act(async () => current.setReprintReason("edited after uncertainty"));
    await act(async () => current.reprint());
    expect(mocks.reprint.mock.calls[1]).toEqual(mocks.reprint.mock.calls[0]);
    expect(mocks.reprint.mock.calls[0].slice(0, 2)).toEqual(["old-card", "原卡损坏"]);
    expect(current.downloadPendingBatch).toBeUndefined();
  });
  it("retains the new batch after a download failure and downloads it without signing another card", async () => {
    await prepare(); mocks.download.mockRejectedValueOnce(new Error("download interrupted"));
    await act(async () => current.reprint());
    expect(current.downloadPendingBatch).toBe("new-batch");
    expect(mocks.warning).toHaveBeenCalledWith(expect.stringContaining("旧卡已作废、新卡已生成"));
    await act(async () => current.downloadPrintBatch(current.downloadPendingBatch!));
    expect(mocks.reprint).toHaveBeenCalledOnce(); expect(mocks.download).toHaveBeenLastCalledWith("new-batch");
    expect(current.downloadPendingBatch).toBeUndefined();
  });
  it("shows the affected page when the print layout fails before replacing the card", async () => {
    await prepare();
    const message = "答题卡第 2 页：二维码区过小，请核对版式。";
    const error = new ApiClientError(409, "student_print_layout_unavailable", "layout unavailable", { error: { message } });
    mocks.reprint.mockRejectedValueOnce(error);
    await act(async () => current.reprint());
    expect(current.reprintError).toBe(message);
    expect(current.downloadPendingBatch).toBeUndefined();
    expect(mocks.download).not.toHaveBeenCalled();
    mocks.download.mockRejectedValueOnce(error);
    await act(async () => current.downloadPrintBatch("legacy-batch"));
    expect(mocks.error).toHaveBeenCalledWith(message);
  });
  it("blocks duplicate replacement clicks while the first request is in flight", async () => {
    await prepare(); let resolve!: (value: unknown) => void;
    mocks.reprint.mockImplementationOnce(() => new Promise(done => { resolve = done; }));
    let first!: Promise<boolean>;
    await act(async () => { first = current.reprint(); void current.reprint(); });
    expect(mocks.reprint).toHaveBeenCalledOnce();
    await act(async () => { resolve({ barcodes: { print_batch_id: "new-batch", students: [] } }); await first; });
  });
  it("ignores a late replacement result after the selected template changes", async () => {
    await prepare(); let resolve!: (value: unknown) => void;
    mocks.reprint.mockImplementationOnce(() => new Promise(done => { resolve = done; }));
    let first!: Promise<boolean>; await act(async () => { first = current.reprint(); });
    await act(async () => root.render(<Probe selected={{ ...template, id: "template-2" }} />));
    await act(async () => { resolve({ barcodes: { print_batch_id: "old-scope-batch", students: [] } }); await first; });
    expect(current.printContext?.template_id).toBe("template-2"); expect(current.downloadPendingBatch).toBeUndefined();
    expect(mocks.download).not.toHaveBeenCalled(); expect(mocks.success).not.toHaveBeenCalled();
  });

  it("does not revive a delayed replacement when returning to the original template", async () => {
    await prepare(); let resolve!: (value: unknown) => void;
    mocks.reprint.mockImplementationOnce(() => new Promise(done => { resolve = done; }));
    let first!: Promise<boolean>; await act(async () => { first = current.reprint(); });
    await act(async () => root.render(<Probe selected={{ ...template, id: "template-2" }} />));
    await act(async () => root.render(<Probe />));
    await act(async () => { resolve({ barcodes: { print_batch_id: "expired-visit", students: [] } }); await first; });
    expect(current.downloadPendingBatch).toBeUndefined();
    expect(mocks.download).not.toHaveBeenCalled(); expect(mocks.success).not.toHaveBeenCalled();
  });

  it("does not download a delayed replacement after manage permission is withdrawn", async () => {
    await prepare(); let resolve!: (value: unknown) => void;
    mocks.reprint.mockImplementationOnce(() => new Promise(done => { resolve = done; }));
    let first!: Promise<boolean>; await act(async () => { first = current.reprint(); });
    await act(async () => root.render(<Probe canManage={false} />));
    await act(async () => { resolve({ barcodes: { print_batch_id: "restricted-batch", students: [] } }); await first; });
    expect(current.printContext).toBeUndefined(); expect(current.downloadPendingBatch).toBeUndefined();
    expect(mocks.download).not.toHaveBeenCalled(); expect(mocks.success).not.toHaveBeenCalled();
  });

  const layout = { source_sha256: "source-hash", pages: [{ page_no: 1, x: .5, y: .1, width: .16, height: .12 }] };
  async function requireLayout() {
    mocks.context.mockResolvedValue({ print_context: { template_id: template.id, candidates: [{ ...candidate, has_active_sheet: false }], batches: [],
      print_source: { file_asset_id: "source", content_type: "application/pdf", sha256: "source-hash" }, qr_layout_required: true, qr_layout_page_nos: [1] } });
    await act(async () => { await current.loadPrintContext(template.id); current.setSelectedPrintClassIds(["class"]); });
  }

  it("requires confirmed positions before signing a template without a QR region", async () => {
    await requireLayout();
    await act(async () => current.issuePrintPackage());
    expect(current.layoutRequest).toEqual({ intent: "issue" });
    expect(mocks.issue).not.toHaveBeenCalled();
    mocks.issue.mockResolvedValueOnce({ barcodes: { print_batch_id: "confirmed-batch", students: [candidate] } });
    await act(async () => current.confirmLayout(layout));
    expect(mocks.issue).toHaveBeenCalledWith(template.id, [candidate.student_id], expect.any(String), layout);
    expect(current.layoutRequest).toBeUndefined();
    expect(mocks.download).toHaveBeenCalledWith("confirmed-batch");
  });

  it("confirms an existing issued batch and resumes download without issuing another card", async () => {
    await requireLayout();
    mocks.download.mockRejectedValueOnce(new ApiClientError(409, "student_print_layout_confirmation_required", "confirmation required"));
    await act(async () => current.downloadPrintBatch("legacy-batch"));
    expect(current.layoutRequest).toEqual({ intent: "download", batchId: "legacy-batch" });
    mocks.confirmLayout.mockRejectedValueOnce(new Error("temporary save failure"));
    await act(async () => current.confirmLayout(layout));
    expect(current.layoutRequest?.batchId).toBe("legacy-batch");
    expect(current.layoutError).toBeTruthy();
    mocks.confirmLayout.mockResolvedValueOnce({ qr_layout: layout });
    await act(async () => current.confirmLayout(layout));
    expect(mocks.confirmLayout).toHaveBeenLastCalledWith("legacy-batch", layout);
    expect(mocks.download).toHaveBeenCalledTimes(2);
    expect(mocks.issue).not.toHaveBeenCalled();
    expect(mocks.reprint).not.toHaveBeenCalled();
    expect(current.downloadPendingBatch).toBeUndefined();
  });

  it("keeps the page selection open after rejected placement and accepts a corrected region", async () => {
    await requireLayout();
    await act(async () => current.issuePrintPackage());
    const message = "答题卡第 1 页：二维码区不足，请调整位置。";
    mocks.issue.mockRejectedValueOnce(new ApiClientError(409, "student_print_layout_unavailable", "layout unavailable", { error: { message } }));
    await act(async () => current.confirmLayout(layout));
    expect(current.layoutRequest).toEqual({ intent: "issue" });
    expect(current.layoutError).toBe(message);
    const corrected = { ...layout, pages: layout.pages.map(page => ({ ...page, x: .6 })) };
    mocks.issue.mockResolvedValueOnce({ barcodes: { print_batch_id: "corrected-batch", students: [candidate] } });
    await act(async () => current.confirmLayout(corrected));
    expect(mocks.issue).toHaveBeenLastCalledWith(template.id, [candidate.student_id], expect.any(String), corrected);
    expect(current.layoutRequest).toBeUndefined();
  });

  it("does not resume an old download after leaving the page during layout confirmation", async () => {
    await requireLayout();
    mocks.download.mockRejectedValueOnce(new ApiClientError(409, "student_print_layout_confirmation_required", "confirmation required"));
    await act(async () => current.downloadPrintBatch("legacy-batch"));
    let resolve!: (value: unknown) => void;
    mocks.confirmLayout.mockImplementationOnce(() => new Promise(done => { resolve = done; }));
    let pending!: Promise<void>; await act(async () => { pending = current.confirmLayout(layout); });
    await act(async () => root.render(null));
    await act(async () => { resolve({ qr_layout: layout }); await pending; });
    expect(mocks.download).toHaveBeenCalledOnce();
    expect(mocks.success).not.toHaveBeenCalled();
  });

  it("asks for position before replacing a card with no reusable print layout", async () => {
    await requireLayout(); await prepare();
    await act(async () => current.reprint());
    expect(current.layoutRequest).toEqual({ intent: "reprint" });
    expect(mocks.reprint).not.toHaveBeenCalled();
    await act(async () => current.confirmLayout(layout));
    expect(mocks.reprint).toHaveBeenCalledWith("old-card", "原卡损坏", expect.any(String), layout);
  });
});
