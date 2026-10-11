// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AnswerSheetTemplate } from "../../../api/configuration";
import { useTemplateLifecycle } from "./useTemplateLifecycle";

const mocks = vi.hoisted(() => ({
  saveTemplateDraft: vi.fn(), setTemplates: vi.fn(),
  setSelectedTemplateId: vi.fn(), onChanged: vi.fn(), error: vi.fn(), success: vi.fn(), warning: vi.fn(), confirm: vi.fn(), lock: vi.fn(), activate: vi.fn()
}));
vi.mock("antd", () => ({
  App: { useApp: () => ({
    message: { error: mocks.error, success: mocks.success, warning: mocks.warning },
    modal: { confirm: mocks.confirm }
  }) }
}));
vi.mock("./saveTemplateDraft", () => ({ saveTemplateDraft: mocks.saveTemplateDraft }));
vi.mock("../../../api/configuration", () => ({ lockAnswerSheetTemplate: mocks.lock }));

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
  async function mount(withQuestion = false) {
    function Probe() {
      current = useTemplateLifecycle({
        examId: "exam-1", selectedTemplate, questions: withQuestion ? [{id:"q1"}] as never : [],
        layout: selectedTemplate.layout, coveredQuestions: new Set(withQuestion ? ["q1"] : []),
        readonly: false, setTemplates: mocks.setTemplates,
        setSelectedTemplateId: mocks.setSelectedTemplateId,
        onChanged: mocks.onChanged,
        onActivate: mocks.activate,
        preview: { pageCount: 1, width: 100, height: 200 } as never
      });
      return null;
    }
    await act(async () => { root.render(<Probe />); });
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
    expect(mocks.lock).toHaveBeenCalledWith(selectedTemplate.id);
    expect(mocks.activate).toHaveBeenCalledWith(locked);
    expect(current.activationError).toBeUndefined();
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
  });

  it("never locks or binds when saving reports a revision conflict", async () => {
    mocks.saveTemplateDraft.mockResolvedValueOnce({status:"revision_conflict",error:new Error("conflict")});
    await mount(true); act(() => current.confirmActivate());
    await act(async () => { await mocks.confirm.mock.calls[0][0].onOk(); });
    expect(mocks.lock).not.toHaveBeenCalled(); expect(mocks.activate).not.toHaveBeenCalled();
    expect(current.activationError).toContain("尚未完成启用");
  });
});
