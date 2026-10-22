// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AnswerSheetTemplate } from "../../../api/configuration";
import { answerSource } from "../testing/answerSheetFixtures";
import { useTemplateLifecycle } from "./useTemplateLifecycle";

const mocks = vi.hoisted(() => ({
  saveTemplateDraft: vi.fn(), create: vi.fn(), clone: vi.fn(), setTemplates: vi.fn(),
  setSelectedTemplateId: vi.fn(), onChanged: vi.fn(), error: vi.fn(), success: vi.fn(), warning: vi.fn(), confirm: vi.fn(), lock: vi.fn(), activate: vi.fn()
}));
vi.mock("antd", () => ({
  App: { useApp: () => ({
    message: { error: mocks.error, success: mocks.success, warning: mocks.warning },
    modal: { confirm: mocks.confirm }
  }) }
}));
vi.mock("./saveTemplateDraft", () => ({ saveTemplateDraft: mocks.saveTemplateDraft }));
vi.mock("../../../api/configuration", () => ({ lockAnswerSheetTemplate: mocks.lock,
  createAnswerSheetTemplate: mocks.create, cloneAnswerSheetTemplate: mocks.clone }));

const selectedTemplate = {
  id: "template-1", exam_paper_id: "paper-1", name: "Original",
  revision: 7, status: "draft", layout: { pages: [] }
} as unknown as AnswerSheetTemplate;

describe("template lifecycle controller", () => {
  let root: Root;
  let container: HTMLDivElement;
  let current: ReturnType<typeof useTemplateLifecycle>;
  beforeEach(() => {
    vi.clearAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
  });
  afterEach(async () => {
    await act(async () => { root.unmount(); });
    container.remove();
  });
  type HookInput = Parameters<typeof useTemplateLifecycle>[0];
  function Probe({ withQuestion = false, readonly = false, examId = "exam-1", patch = {} }: {
    withQuestion?: boolean; readonly?: boolean; examId?: string; patch?: Partial<HookInput>
  }) {
      current = useTemplateLifecycle({
        examId, selectedTemplate, questions: withQuestion ? [{id:"q1"}] as never : [],
        layout: selectedTemplate.layout, coveredQuestions: new Set(withQuestion ? ["q1"] : []),
        readonly, setTemplates: mocks.setTemplates,
        setSelectedTemplateId: mocks.setSelectedTemplateId,
        onChanged: mocks.onChanged,
        onActivate: mocks.activate,
        preview: { pageCount: 1, width: 100, height: 200 } as never,
        ...patch
      });
      return null;
  }
  async function mount(withQuestion = false, readonly = false, examId = "exam-1", patch: Partial<HookInput> = {}) {
    await act(async () => { root.render(<Probe withQuestion={withQuestion} readonly={readonly} examId={examId} patch={patch} />); });
  }

  it("keeps the local edit available for manual review after a revision conflict", async () => {
    const conflict = new Error("revision conflict");
    mocks.saveTemplateDraft.mockResolvedValueOnce({ status: "revision_conflict", error: conflict });
    await mount();
    expect(current.revision).toBe(7);
    await act(async () => { await current.saveDraft(); });
    expect(mocks.saveTemplateDraft).toHaveBeenCalledWith(expect.objectContaining({
      templateId: "template-1", expectedRevision: 7
    }));
    expect(mocks.warning).toHaveBeenCalledTimes(1);
    expect(mocks.setTemplates).not.toHaveBeenCalled();
    expect(current.saving).toBe(false);
  });

  it("advances revision only after the server confirms a save", async () => {
    mocks.saveTemplateDraft.mockResolvedValueOnce({
      status: "saved", template: { ...selectedTemplate, revision: 8 }
    });
    await mount();
    await act(async () => { await current.saveDraft(); });
    expect(current.revision).toBe(8);
    expect(mocks.setTemplates).toHaveBeenCalledTimes(1);
    expect(mocks.onChanged).toHaveBeenCalledTimes(1);
  });

  it("saves the current draft before locking and activating it for the exam", async () => {
    const saved = {...selectedTemplate, revision:8};
    const locked = {...saved,status:"locked",version_no:2};
    mocks.saveTemplateDraft.mockResolvedValueOnce({status:"saved",template:saved});
    mocks.lock.mockResolvedValueOnce({template:locked}); mocks.activate.mockResolvedValueOnce({});
    await mount(true);
    await act(async () => current.setName("核对后的名称"));
    act(() => current.confirmActivate());
    await act(async () => { await mocks.confirm.mock.calls[0][0].onOk(); });
    expect(mocks.saveTemplateDraft.mock.calls[0][0].payload.name).toBe("核对后的名称");
    expect(mocks.lock).toHaveBeenCalledWith(selectedTemplate.id, saved.revision);
    expect(mocks.activate).toHaveBeenCalledWith(locked);
    expect(current.activationError).toBeUndefined();
    expect(mocks.onChanged).toHaveBeenCalledOnce();
  });

  it("locks only the revision returned by saving the latest local layout", async () => {
    const editedLayout = { ...selectedTemplate.layout, omr_profile: { mode: "manual_only" as const, version: "opencv-fill-v1" as const } };
    const saved = { ...selectedTemplate, layout: editedLayout, revision: 12 };
    mocks.saveTemplateDraft.mockResolvedValueOnce({ status: "saved", template: saved });
    mocks.lock.mockResolvedValueOnce({ template: { ...saved, status: "locked" } });
    await mount(true, false, "exam-1", { layout: editedLayout });
    act(() => current.confirmLock());
    await act(async () => mocks.confirm.mock.calls[0][0].onOk());
    expect(mocks.saveTemplateDraft).toHaveBeenCalledWith(expect.objectContaining({ expectedRevision: 7,
      payload: expect.objectContaining({ layout: editedLayout }) }));
    expect(mocks.lock).toHaveBeenCalledWith(selectedTemplate.id, 12);
    expect(mocks.saveTemplateDraft.mock.invocationCallOrder[0]).toBeLessThan(mocks.lock.mock.invocationCallOrder[0]);
  });

  it.each(["lock", "activate"] as const)("rejects a stale %s confirmation after the local draft changes", async mode => {
    await mount(true);
    act(() => mode === "lock" ? current.confirmLock() : current.confirmActivate());
    const onOk = mocks.confirm.mock.calls[0][0].onOk;
    await mount(true, false, "exam-1", { layout: { ...selectedTemplate.layout } });
    await act(async () => onOk());
    expect(mocks.saveTemplateDraft).not.toHaveBeenCalled();
    expect(mocks.lock).not.toHaveBeenCalled();
    expect(mocks.activate).not.toHaveBeenCalled();
    expect(mocks.warning).toHaveBeenCalledWith("确认期间草稿已有修改，请重新核对后确认");
  });

  it("retains a locked version and gives a binding recovery instruction when the last step fails", async () => {
    const saved = {...selectedTemplate,revision:8};
    mocks.saveTemplateDraft.mockResolvedValueOnce({status:"saved",template:saved});
    mocks.lock.mockResolvedValueOnce({template:{...saved,status:"locked"}});
    mocks.activate.mockRejectedValueOnce(new Error("offline"));
    await mount(true); act(() => current.confirmActivate());
    await act(async () => { await mocks.confirm.mock.calls[0][0].onOk(); });
    expect(current.activationError).toContain("已保存并锁定");
    expect(current.activationError).toContain("用于本场考试");
    expect(current.saving).toBe(false);
    expect(mocks.success).not.toHaveBeenCalled();
    expect(mocks.onChanged).toHaveBeenCalledOnce();
  });

  it("never locks or binds when saving reports a revision conflict", async () => {
    mocks.saveTemplateDraft.mockResolvedValueOnce({status:"revision_conflict",error:new Error("conflict")});
    await mount(true); act(() => current.confirmActivate());
    await act(async () => { await mocks.confirm.mock.calls[0][0].onOk(); });
    expect(mocks.lock).not.toHaveBeenCalled(); expect(mocks.activate).not.toHaveBeenCalled();
    expect(mocks.onChanged).not.toHaveBeenCalled();
    expect(current.activationError).toContain("尚未完成启用");
  });

  it.each(["readonly", "exam"])("旧草稿确认窗口在 %s 改变并恢复后不能写入", async boundary => {
    await mount(true); act(() => current.confirmActivate());
    const onOk = mocks.confirm.mock.calls[0][0].onOk;
    if (boundary === "readonly") { await mount(true, true); await mount(true, false); }
    else { await mount(true, false, "other-exam"); await mount(true); }
    await act(async () => onOk());
    expect(mocks.saveTemplateDraft).not.toHaveBeenCalled();
    expect(mocks.lock).not.toHaveBeenCalled();
    expect(mocks.activate).not.toHaveBeenCalled();
  });

  it("连点确认只保存一次，换考试后的旧保存响应不能继续锁定或显示完成", async () => {
    let resolveSave: (result: { status: string; template: typeof selectedTemplate }) => void = () => {};
    mocks.saveTemplateDraft.mockReturnValue(new Promise(resolve => { resolveSave = resolve; }));
    await mount(true); act(() => current.confirmActivate());
    const onOk = mocks.confirm.mock.calls[0][0].onOk;
    let saving: Promise<void> = Promise.resolve();
    await act(async () => { saving = onOk(); await onOk(); });
    expect(mocks.saveTemplateDraft).toHaveBeenCalledOnce();
    await mount(true, false, "other-exam");
    await act(async () => { resolveSave({ status: "saved", template: selectedTemplate }); await saving; });
    expect(mocks.setTemplates).not.toHaveBeenCalled();
    expect(mocks.lock).not.toHaveBeenCalled();
    expect(mocks.activate).not.toHaveBeenCalled();
    expect(mocks.onChanged).not.toHaveBeenCalled();
    expect(current.saving).toBe(false);
  });

  function sourcePatch(id = "paper-1"): Partial<HookInput> {
    return { selectedPaper: { ...answerSource, id },
      preview: { sourceId: id, loading: false, pageCount: 2, width: 600, height: 800,
        pageSizes: [{ width: 600, height: 800 }, { width: 1200, height: 800 }] } };
  }
  function deferred<T>() {
    let resolve!: (value: T) => void;
    const promise = new Promise<T>(done => { resolve = done; });
    return { promise, resolve };
  }

  it("creates one multi-page draft with each page's own dimensions despite same-event repeated calls", async () => {
    const waiting = deferred<{ template: AnswerSheetTemplate }>();
    mocks.create.mockReturnValue(waiting.promise);
    await mount(false, false, "exam-1", { ...sourcePatch(), selectedTemplate: undefined });
    let pending!: Promise<void>;
    await act(async () => { pending = current.createDraft(); void current.createDraft(); });
    expect(mocks.create).toHaveBeenCalledOnce();
    expect(mocks.create).toHaveBeenCalledWith("exam-1", expect.objectContaining({ exam_paper_id: "paper-1", page_count: 2,
      layout: expect.objectContaining({ pages: [expect.objectContaining({ page_no: 1, width: 600, height: 800 }),
        expect.objectContaining({ page_no: 2, width: 1200, height: 800 })] }) }));
    await act(async () => { waiting.resolve({ template: selectedTemplate }); await pending; });
    expect(mocks.setSelectedTemplateId).toHaveBeenCalledWith(selectedTemplate.id);
    expect(mocks.setTemplates).toHaveBeenCalledOnce();
  });

  it.each(["readonly", "old_preview", "loading", "preview_error"])("does not create a draft when %s", async boundary => {
    const patch = { ...sourcePatch(), selectedTemplate: undefined };
    if (boundary === "old_preview") patch.preview = { ...patch.preview!, sourceId: "old-source" };
    if (boundary === "loading") patch.preview = { ...patch.preview!, loading: true };
    if (boundary === "preview_error") patch.preview = { ...patch.preview!, error: "failed preview" };
    await mount(false, boundary === "readonly", "exam-1", patch);
    await act(async () => current.createDraft());
    expect(mocks.create).not.toHaveBeenCalled();
  });

  it.each(["exam", "source", "readonly", "unmount"] as const)("discards a draft creation response after %s changes", async boundary => {
    const waiting = deferred<{ template: AnswerSheetTemplate }>();
    mocks.create.mockReturnValue(waiting.promise);
    await mount(false, false, "exam-1", { ...sourcePatch(), selectedTemplate: undefined });
    let pending!: Promise<void>;
    await act(async () => { pending = current.createDraft(); });
    if (boundary === "unmount") await act(async () => root.render(null));
    else await mount(false, boundary === "readonly", boundary === "exam" ? "another-exam" : "exam-1",
      { ...sourcePatch(boundary === "source" ? "another-source" : "paper-1"), selectedTemplate: undefined });
    await act(async () => { waiting.resolve({ template: selectedTemplate }); await pending; });
    expect(mocks.setTemplates).not.toHaveBeenCalled();
    expect(mocks.setSelectedTemplateId).not.toHaveBeenCalled();
    expect(mocks.onChanged).not.toHaveBeenCalled();
    expect(mocks.success).not.toHaveBeenCalled();
    if (boundary !== "unmount") expect(current.saving).toBe(false);
  });

  it("saves once for repeated same-event calls and never moves another template's revision on a late response", async () => {
    const waiting = deferred<{ status: "saved"; template: AnswerSheetTemplate }>();
    mocks.saveTemplateDraft.mockReturnValue(waiting.promise);
    await mount(false, false, "exam-1", sourcePatch());
    let pending!: Promise<void>;
    await act(async () => { pending = current.saveDraft(); void current.saveDraft(); });
    expect(mocks.saveTemplateDraft).toHaveBeenCalledOnce();
    await mount(false, false, "exam-1", { ...sourcePatch(), selectedTemplate: { ...selectedTemplate, id: "template-2", revision: 19 } });
    await act(async () => { waiting.resolve({ status: "saved", template: { ...selectedTemplate, revision: 8 } }); await pending; });
    expect(current.revision).toBe(19);
    expect(mocks.setTemplates).not.toHaveBeenCalled();
    expect(mocks.onChanged).not.toHaveBeenCalled();
    expect(mocks.success).not.toHaveBeenCalled();
  });

  it.each(["exam", "source", "readonly"] as const)("does not lock from a confirmation opened before %s changed and restored", async boundary => {
    await mount(true, false, "exam-1", sourcePatch());
    act(() => current.confirmLock());
    const onOk = mocks.confirm.mock.calls[0][0].onOk;
    await mount(true, boundary === "readonly", boundary === "exam" ? "another-exam" : "exam-1",
      sourcePatch(boundary === "source" ? "another-source" : "paper-1"));
    await mount(true, false, "exam-1", sourcePatch());
    await act(async () => onOk());
    expect(mocks.lock).not.toHaveBeenCalled();
    expect(mocks.setTemplates).not.toHaveBeenCalled();
  });

  it("clones a locked template when permitted but blocks cloning after exam editing is frozen", async () => {
    mocks.clone.mockResolvedValue({ template: { ...selectedTemplate, id: "clone" } });
    const patch = { ...sourcePatch(), selectedTemplate: { ...selectedTemplate, status: "locked" as const }, canManage: true };
    await mount(false, true, "exam-1", patch);
    await act(async () => current.cloneTemplate());
    expect(mocks.clone).toHaveBeenCalledOnce();
    expect(mocks.setSelectedTemplateId).toHaveBeenCalledWith("clone");
    mocks.clone.mockClear();
    await mount(false, true, "exam-1", { ...patch, canManage: false });
    await act(async () => current.cloneTemplate());
    expect(mocks.clone).not.toHaveBeenCalled();
  });

  it("discards a clone response after switching sources", async () => {
    const waiting = deferred<{ template: AnswerSheetTemplate }>();
    mocks.clone.mockReturnValue(waiting.promise);
    await mount(false, false, "exam-1", sourcePatch());
    let pending!: Promise<void>;
    await act(async () => { pending = current.cloneTemplate(); });
    await mount(false, false, "exam-1", sourcePatch("another-source"));
    await act(async () => { waiting.resolve({ template: { ...selectedTemplate, id: "late-clone" } }); await pending; });
    expect(mocks.setTemplates).not.toHaveBeenCalled();
    expect(mocks.setSelectedTemplateId).not.toHaveBeenCalled();
    expect(mocks.onChanged).not.toHaveBeenCalled();
    expect(mocks.success).not.toHaveBeenCalled();
  });

  it("does not save or clone a template selected before its own source has synchronized", async () => {
    await mount(false, false, "exam-1", { ...sourcePatch(), selectedTemplate: { ...selectedTemplate, exam_paper_id: "another-source" } });
    await act(async () => { await current.saveDraft(); await current.cloneTemplate(); });
    expect(mocks.saveTemplateDraft).not.toHaveBeenCalled();
    expect(mocks.clone).not.toHaveBeenCalled();
  });

  it("discards a locked-template clone response when preview readiness changed and recovered", async () => {
    const waiting = deferred<{ template: AnswerSheetTemplate }>();
    mocks.clone.mockReturnValue(waiting.promise);
    const patch = { ...sourcePatch(), selectedTemplate: { ...selectedTemplate, status: "locked" as const }, canManage: true };
    await mount(false, true, "exam-1", patch);
    let pending!: Promise<void>;
    await act(async () => { pending = current.cloneTemplate(); });
    // 锁定模板一直只读，必须另外观察底图绑定，不能依赖 readonly 布尔值改变来失效旧操作。
    await mount(false, true, "exam-1", { ...patch, preview: { ...patch.preview!, sourceId: "other-source" } });
    await mount(false, true, "exam-1", patch);
    await act(async () => { waiting.resolve({ template: { ...selectedTemplate, id: "obsolete-clone" } }); await pending; });
    expect(mocks.setTemplates).not.toHaveBeenCalled();
    expect(mocks.setSelectedTemplateId).not.toHaveBeenCalled();
    expect(mocks.success).not.toHaveBeenCalled();
  });

  it("retains the in-flight save while only the current preview page is rendering", async () => {
    const waiting = deferred<{ status: "saved"; template: AnswerSheetTemplate }>();
    mocks.saveTemplateDraft.mockReturnValue(waiting.promise);
    await mount(false, false, "exam-1", sourcePatch());
    let pending!: Promise<void>;
    await act(async () => { pending = current.saveDraft(); });
    const patch = sourcePatch();
    await mount(false, false, "exam-1", { ...patch, preview: { ...patch.preview!, loading: true } });
    expect(current.saving).toBe(true);
    await act(async () => { waiting.resolve({ status: "saved", template: { ...selectedTemplate, revision: 8 } }); await pending; });
    expect(current.revision).toBe(8);
    expect(mocks.setTemplates).toHaveBeenCalledOnce();
    expect(mocks.onChanged).toHaveBeenCalledOnce();
    expect(mocks.success).toHaveBeenCalledOnce();
    expect(current.saving).toBe(false);
  });

  it("does not start a saved lock confirmation while the current preview page is rendering", async () => {
    await mount(true, false, "exam-1", sourcePatch());
    act(() => current.confirmLock());
    const onOk = mocks.confirm.mock.calls[0][0].onOk;
    const patch = sourcePatch();
    await mount(true, false, "exam-1", { ...patch, preview: { ...patch.preview!, loading: true } });
    await act(async () => onOk());
    expect(mocks.lock).not.toHaveBeenCalled();
  });

  it("retains the server lock but does not bind after exam editing freezes while lock confirmation is pending", async () => {
    const waiting = deferred<{ template: AnswerSheetTemplate }>();
    mocks.saveTemplateDraft.mockResolvedValue({ status: "saved", template: { ...selectedTemplate, revision: 8 } });
    mocks.lock.mockReturnValue(waiting.promise);
    await mount(true, false, "exam-1", { ...sourcePatch(), canManage: true });
    act(() => current.confirmActivate());
    let pending!: Promise<void>;
    await act(async () => { pending = mocks.confirm.mock.calls[0][0].onOk(); });
    expect(mocks.lock).toHaveBeenCalledOnce();
    await mount(true, true, "exam-1", { ...sourcePatch(), canManage: false });
    await act(async () => { waiting.resolve({ template: { ...selectedTemplate, status: "locked" } }); await pending; });
    expect(mocks.activate).not.toHaveBeenCalled();
    expect(mocks.setTemplates).toHaveBeenCalledTimes(2);
    expect(current.activationError).toContain("已保存并锁定");
    expect(mocks.onChanged).toHaveBeenCalledOnce();
    expect(mocks.success).not.toHaveBeenCalled();
  });
});
