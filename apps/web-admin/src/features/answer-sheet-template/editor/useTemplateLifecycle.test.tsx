// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AnswerSheetTemplate } from "../../../api/configuration";
import { useTemplateLifecycle } from "./useTemplateLifecycle";

const mocks = vi.hoisted(() => ({
  saveTemplateDraft: vi.fn(), loadData: vi.fn(), setTemplates: vi.fn(),
  setSelectedTemplateId: vi.fn(), onChanged: vi.fn(), error: vi.fn(), success: vi.fn()
}));
vi.mock("antd", () => ({
  App: { useApp: () => ({
    message: { error: mocks.error, success: mocks.success },
    modal: { confirm: vi.fn() }
  }) }
}));
vi.mock("./saveTemplateDraft", () => ({ saveTemplateDraft: mocks.saveTemplateDraft }));

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
    mocks.loadData.mockResolvedValue(undefined);
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
  });
  afterEach(async () => {
    await act(async () => { root.unmount(); });
    container.remove();
  });
  async function mount() {
    function Probe() {
      current = useTemplateLifecycle({
        examId: "exam-1", selectedTemplate, questions: [],
        layout: selectedTemplate.layout, coveredQuestions: new Set(),
        readonly: false, setTemplates: mocks.setTemplates,
        setSelectedTemplateId: mocks.setSelectedTemplateId,
        loadData: mocks.loadData, onChanged: mocks.onChanged,
        preview: { pageCount: 1, width: 100, height: 200 } as never
      });
      return null;
    }
    await act(async () => { root.render(<Probe />); });
  }

  it("reloads authoritative state after a revision conflict", async () => {
    const conflict = new Error("revision conflict");
    mocks.saveTemplateDraft.mockResolvedValueOnce({ status: "revision_conflict", error: conflict });
    await mount();
    expect(current.revision).toBe(7);
    await act(async () => { await current.saveDraft(); });
    expect(mocks.saveTemplateDraft).toHaveBeenCalledWith(expect.objectContaining({
      templateId: "template-1", expectedRevision: 7
    }));
    expect(mocks.loadData).toHaveBeenCalledTimes(1);
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
});
