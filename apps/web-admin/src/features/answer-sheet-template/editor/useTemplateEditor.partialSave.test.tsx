// @vitest-environment jsdom
import { act, useRef, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import type { PDFDocumentProxy } from "pdfjs-dist";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Question } from "../../../api/papers";
import type { AnswerSheetTemplate } from "../../../api/configuration";
import { answerSource, answerTemplate } from "../testing/answerSheetFixtures";
import { useTemplateEditor } from "./useTemplateEditor";

const mocks = vi.hoisted(() => ({ save: vi.fn(), render: vi.fn(), frames: vi.fn(), analyze: vi.fn() }));
vi.mock("./saveTemplateDraft", () => ({ saveTemplateDraft: mocks.save }));
vi.mock("./answerSheetOCR", () => ({ renderAnswerSheetPage: mocks.render }));
vi.mock("./answerSheetImageRegions", () => ({ detectAnswerSheetFrames: mocks.frames }));
vi.mock("antd", () => ({ App: { useApp: () => ({
  message: { success: vi.fn(), error: vi.fn(), warning: vi.fn() }, modal: { confirm: vi.fn() }
}) } }));

const frames = [{ x: .05, y: .1, width: .4, height: .7, confidence: .95 }, { x: .55, y: .1, width: .4, height: .7, confidence: .95 }];
const questions = [{ id: "q1", question_no: "1", question_type: "calculation" }, { id: "q2", question_no: "2", question_type: "calculation" }] as Question[];
const template: AnswerSheetTemplate = { ...answerTemplate, exam_paper_id: answerSource.id, layout: { pages: [{ ...answerTemplate.layout.pages[0], question_regions: [], identity_regions: [] }] } };

describe("template partial save candidate preservation", () => {
  let root: Root; let host: HTMLDivElement; let current: ReturnType<typeof useTemplateEditor>;
  function Probe() {
    const [templates, setTemplates] = useState([template]);
    const [selectedId, setSelectedId] = useState(template.id);
    const [pageNo, setPageNo] = useState(1);
    const pdfRef = useRef<PDFDocumentProxy | undefined>(undefined);
    current = useTemplateEditor({ examId: template.exam_id, canManage: true, selectedTemplate: templates.find(item => item.id === selectedId),
      selectedPaper: answerSource, questions, setTemplates, setSelectedTemplateId: setSelectedId, pdfRef, pageNo, setPageNo, analyzePage: mocks.analyze,
      preview: { sourceId: answerSource.id, imageUrl: "blob:sheet", loading: false, pageCount: 1, width: 1000, height: 1000 } });
    return null;
  }
  beforeEach(() => {
    vi.resetAllMocks(); Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.frames.mockReturnValue(frames);
    mocks.render.mockResolvedValue({ width: 1000, height: 1000, getContext: () => ({ getImageData: vi.fn() }) });
    mocks.analyze.mockResolvedValue({ regions: [{ id: "matched-q1", question_id: "q1", ...frames[0] }], unassigned: [] });
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); });

  it("keeps the remaining ordinary frame after the real save lifecycle replaces the server snapshot and revision", async () => {
    let finishSave!: (value: unknown) => void;
    mocks.save.mockImplementation(() => new Promise(resolve => { finishSave = resolve; }));
    await act(async () => root.render(<Probe />));
    await act(async () => current.suggestQuestionRegions());
    const pending = current.unassignedRegions.get(1)!;
    expect(pending).toHaveLength(1); expect(pending[0].x).toBe(.55);
    expect(current.layout.pages[0].question_regions.map(region => region.question_id)).toEqual(["q1"]);
    let saving!: Promise<void>;
    await act(async () => { saving = current.saveDraft(); });
    expect(current.readonly).toBe(true);
    const payload = mocks.save.mock.calls[0][0].payload;
    await act(async () => {
      finishSave({ status: "saved", template: { ...template, ...payload, revision: template.revision + 1 } });
      await saving;
    });
    expect(current.revision).toBe(template.revision + 1); expect(current.readonly).toBe(false);
    expect(current.unassignedRegions.get(1)).toEqual(pending);
    expect(current.layout.pages[0].question_regions.map(region => region.question_id)).toEqual(["q1"]);
    expect(current.objectiveGroups.size).toBe(0);
    await act(async () => current.assignSuggestedRegion(pending[0].id, "q2"));
    expect(current.layout.pages[0].question_regions.map(region => region.question_id)).toEqual(["q1", "q2"]);
    expect(current.unassignedRegions.size).toBe(0);
  });
});
