// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AnswerSheetTemplate } from "../../../api/configuration";
import type { StudentPrintCandidate } from "../../../api/printing";
import { useStudentPrinting } from "./useStudentPrinting";

const mocks = vi.hoisted(() => ({ context: vi.fn(), issue: vi.fn(), reprint: vi.fn(), download: vi.fn(), success: vi.fn(), warning: vi.fn(), error: vi.fn() }));
vi.mock("../../../api/printing", () => ({ getStudentPrintContext: mocks.context, issueStudentPrintBatch: mocks.issue, reprintStudentSheet: mocks.reprint, downloadStudentPrintPackage: mocks.download }));
vi.mock("antd", () => ({ App: { useApp: () => ({ message: { success: mocks.success, warning: mocks.warning, error: mocks.error } }) } }));
const candidate: StudentPrintCandidate = { student_id: "student", student_name: "学生甲", student_no: "01", class_id: "class", class_name: "高一一班", attendance_status: "expected", has_active_sheet: true, active_sheet_serial: "old-card" };
const template = { id: "template-1", status: "locked" } as AnswerSheetTemplate;

describe("student printing recovery", () => {
  let host: HTMLDivElement; let root: Root; let current: ReturnType<typeof useStudentPrinting>;
  function Probe({ selected = template }: Readonly<{ selected?: AnswerSheetTemplate }>) { current = useStudentPrinting(selected, true); return null; }
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
  it("blocks duplicate replacement clicks while the first request is in flight", async () => {
    await prepare(); let resolve!: (value: unknown) => void;
    mocks.reprint.mockImplementationOnce(() => new Promise(done => { resolve = done; }));
    let first!: Promise<void>;
    await act(async () => { first = current.reprint(); void current.reprint(); });
    expect(mocks.reprint).toHaveBeenCalledOnce();
    await act(async () => { resolve({ barcodes: { print_batch_id: "new-batch", students: [] } }); await first; });
  });
  it("ignores a late replacement result after the selected template changes", async () => {
    await prepare(); let resolve!: (value: unknown) => void;
    mocks.reprint.mockImplementationOnce(() => new Promise(done => { resolve = done; }));
    let first!: Promise<void>; await act(async () => { first = current.reprint(); });
    await act(async () => root.render(<Probe selected={{ ...template, id: "template-2" }} />));
    await act(async () => { resolve({ barcodes: { print_batch_id: "old-scope-batch", students: [] } }); await first; });
    expect(current.printContext?.template_id).toBe("template-2"); expect(current.downloadPendingBatch).toBeUndefined();
    expect(mocks.download).not.toHaveBeenCalled(); expect(mocks.success).not.toHaveBeenCalled();
  });
});
