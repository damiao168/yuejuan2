// @vitest-environment jsdom
import { act, useRef, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import type { PDFDocumentProxy } from "pdfjs-dist";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AnswerSheetTemplate } from "../../../api/configuration";
import type { Question } from "../../../api/papers";
import { useTemplateEditor } from "./useTemplateEditor";

const mocks = vi.hoisted(() => ({ save: vi.fn() }));
vi.mock("./saveTemplateDraft", () => ({ saveTemplateDraft: mocks.save }));
vi.mock("antd", () => ({ App: { useApp: () => ({
  message: { success: vi.fn(), error: vi.fn(), warning: vi.fn() }, modal: { confirm: vi.fn() }
}) } }));

const template: AnswerSheetTemplate = {
  id: "template", tenant_id: "tenant", exam_id: "exam", exam_paper_id: "paper", name: "原模板",
  revision: 1, version_no: 1, status: "draft", page_count: 1, content_hash: "hash", created_by: "admin",
  created_at: "2026-09-30T00:00:00Z", updated_at: "2026-09-30T00:00:00Z",
  layout: { pages: [{ page_no: 1, width: 100, height: 200, registration_marks: [], identity_regions: [],
    question_regions: [{ id: "region", question_id: "question", x: .1, y: .1, width: .5, height: .2 }] }] }
};
const questions = [{ id: "question", question_no: "1" }] as Question[];

describe("template editor save boundary", () => {
  let root: Root;
  let host: HTMLDivElement;
  let editor: ReturnType<typeof useTemplateEditor>;
  beforeEach(() => {
    vi.clearAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
  });
  afterEach(async () => {
    await act(async () => root.unmount());
    host.remove();
  });

  it("freezes name and region changes while saving, then reflects the confirmed server snapshot", async () => {
    let finishSave!: (value: unknown) => void;
    mocks.save.mockImplementation(() => new Promise((resolve) => { finishSave = resolve; }));
    function Probe() {
      const [templates, setTemplates] = useState([template]);
      const [selectedId, setSelectedId] = useState(template.id);
      const [pageNo, setPageNo] = useState(1);
      const pdfRef = useRef<PDFDocumentProxy | undefined>(undefined);
      editor = useTemplateEditor({
        examId: "exam", canManage: true, selectedTemplate: templates.find((item) => item.id === selectedId),
        questions, setTemplates, setSelectedTemplateId: setSelectedId, pdfRef,
        preview: { loading: false, pageCount: 1, width: 100, height: 200 }, pageNo, setPageNo
      });
      return null;
    }
    await act(async () => root.render(<Probe />));
    await act(async () => { editor.setName("已修改"); editor.updateRegion("region", { x: .25 }); });
    let saving!: Promise<void>;
    await act(async () => { saving = editor.saveDraft(); });
    expect(editor.readonly).toBe(true);
    await act(async () => {
      editor.setName("等待期间的新内容");
      editor.updateRegion("region", { x: .8 });
      editor.removeRegion("region");
    });
    expect(editor.name).toBe("已修改");
    expect(editor.layout.pages[0].question_regions[0].x).toBe(.25);
    const payload = mocks.save.mock.calls[0][0].payload;
    await act(async () => {
      finishSave({ status: "saved", template: { ...template, ...payload, revision: 2 } });
      await saving;
    });
    expect(editor.readonly).toBe(false);
    expect(editor.revision).toBe(2);
    expect(editor.name).toBe("已修改");
    expect(editor.layout.pages[0].question_regions[0].x).toBe(.25);
  });
});
