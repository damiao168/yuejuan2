// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PaperVersion } from "../../../api/papers";
import type { AnswerSheetTemplate } from "../../../api/configuration";
import { useAnswerSheetSourceDelete } from "./useAnswerSheetSourceDelete";

const mocks = vi.hoisted(() => ({ remove: vi.fn(), confirm: vi.fn(), destroy: vi.fn(), success: vi.fn(), error: vi.fn(),
  onDeleted: vi.fn(), busy: vi.fn() }));
vi.mock("antd", () => ({ App: { useApp: () => ({ modal: { confirm: mocks.confirm }, message: { success: mocks.success, error: mocks.error } }) } }));
vi.mock("../../../api/answerSheetSources", () => ({ deleteAnswerSheetSource: mocks.remove }));
const paper = { id: "paper-1", exam_id: "exam-1", purpose: "answer_sheet", file: { original_name: "答题卡.pdf" } } as PaperVersion;
type Input = Parameters<typeof useAnswerSheetSourceDelete>[0];
type Confirmation = { onOk: () => Promise<void>; onCancel: () => void; content: string };
function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>(done => { resolve = done; }); return { promise, resolve }; }

describe("answer-sheet source deletion", () => {
  let host: HTMLDivElement, root: Root, current: ReturnType<typeof useAnswerSheetSourceDelete>, mounted: boolean;
  function Probe({ patch }: { patch: Partial<Input> }) {
    current = useAnswerSheetSourceDelete({ examId: "exam-1", identityKey: "account-1", canManage: true, selectedPaper: paper,
      onDeleted: mocks.onDeleted, onBusyChange: mocks.busy, ...patch }); return null;
  }
  const confirmation = () => mocks.confirm.mock.lastCall?.[0] as Confirmation;
  async function mount(patch: Partial<Input> = {}) { await act(async () => root.render(<Probe patch={patch} />)); }
  beforeEach(() => {
    vi.resetAllMocks(); Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.confirm.mockReturnValue({ destroy: mocks.destroy });
    mocks.remove.mockResolvedValue({ deleted: true, paper_id: paper.id, deleted_template_ids: ["draft-1"] });
    host = document.createElement("div"); document.body.append(host); root = createRoot(host); mounted = true;
  });
  afterEach(async () => { if (mounted) await act(async () => root.unmount()); host.remove(); });

  it("requires both discard and dangerous-delete confirmation before deleting and refreshing", async () => {
    let approve!: () => void;
    await mount({ confirmDiscard: action => { approve = action; }, templates: [{ id: "draft-1", exam_paper_id: paper.id, status: "draft" }] as AnswerSheetTemplate[] });
    await act(async () => current.removeCurrent());
    expect(mocks.confirm).not.toHaveBeenCalled(); expect(mocks.remove).not.toHaveBeenCalled();
    await act(async () => approve());
    expect(confirmation().content).toContain("1 个关联模板草稿");
    expect(mocks.remove).not.toHaveBeenCalled();
    await act(async () => confirmation().onOk());
    expect(mocks.remove).toHaveBeenCalledWith("exam-1", "paper-1");
    expect(mocks.onDeleted).toHaveBeenCalledWith(paper);
    expect(mocks.busy.mock.calls.map(([busy]) => busy)).toEqual([false, true, false]);
    expect(current.deleting).toBe(false); expect(current.canDelete).toBe(false);
  });

  it("preserves the source and draft on a server reference conflict and permits retry", async () => {
    mocks.remove.mockRejectedValueOnce(new Error("答题卡已被模板库引用，不能删除"));
    await mount(); await act(async () => current.removeCurrent());
    await act(async () => { await expect(confirmation().onOk()).rejects.toThrow("模板库引用"); });
    expect(current.deleteError).toContain("模板库引用");
    expect(mocks.onDeleted).not.toHaveBeenCalled(); expect(mocks.success).not.toHaveBeenCalled();
    expect(current.canDelete).toBe(true);
    await act(async () => confirmation().onOk());
    expect(mocks.onDeleted).toHaveBeenCalledOnce();
  });

  it("does not reissue deletion when the server succeeded but refreshing failed", async () => {
    mocks.onDeleted.mockRejectedValue(new Error("reload failed"));
    await mount(); await act(async () => current.removeCurrent());
    await act(async () => confirmation().onOk());
    expect(current.deleteError).toContain("已删除，但列表刷新失败");
    expect(current.canDelete).toBe(false);
    await act(async () => current.removeCurrent());
    expect(mocks.remove).toHaveBeenCalledOnce();
  });

  it.each(["permission", "legacy", "locked", "busy"])("blocks deleting a protected %s source", async boundary => {
    await mount(boundary === "permission" ? { canManage: false } : boundary === "legacy" ? { selectedPaper: { ...paper, purpose: "question_paper" } }
      : boundary === "locked" ? { templates: [{ exam_paper_id: paper.id, status: "locked" }] as AnswerSheetTemplate[] } : { busy: true });
    expect(current.canDelete).toBe(false); await act(async () => current.removeCurrent());
    expect(mocks.confirm).not.toHaveBeenCalled(); expect(mocks.remove).not.toHaveBeenCalled();
  });

  it.each(["exam", "identity", "source", "permission", "unmount"] as const)("discards a stale confirmation and response after %s changes", async boundary => {
    const waiting = deferred<{ deleted: true; paper_id: string; deleted_template_ids: string[] }>();
    mocks.remove.mockReturnValueOnce(waiting.promise);
    await mount(); await act(async () => current.removeCurrent()); const previous = confirmation();
    let pending!: Promise<void>; await act(async () => { pending = previous.onOk(); });
    expect(current.deleting).toBe(true);
    if (boundary === "unmount") { await act(async () => root.unmount()); mounted = false; }
    else await mount(boundary === "exam" ? { examId: "exam-2" } : boundary === "identity" ? { identityKey: "account-2" }
      : boundary === "source" ? { selectedPaper: { ...paper, id: "paper-2" } } : { canManage: false });
    expect(mocks.destroy).toHaveBeenCalled();
    await act(async () => { waiting.resolve({ deleted: true, paper_id: paper.id, deleted_template_ids: [] }); await pending; });
    await previous.onOk();
    expect(mocks.remove).toHaveBeenCalledOnce(); expect(mocks.onDeleted).not.toHaveBeenCalled(); expect(mocks.success).not.toHaveBeenCalled();
  });
});
