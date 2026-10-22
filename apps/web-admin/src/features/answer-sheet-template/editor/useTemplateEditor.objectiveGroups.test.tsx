// @vitest-environment jsdom
import { act, useMemo } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AnalysisBox } from "../../../api/answerSheetTemplateAnalysis";
import type { LayoutRegion } from "../../../api/configuration";
import type { Question } from "../../../api/papers";
import { answerSource, answerTemplate } from "../testing/answerSheetFixtures";
import { useTemplateEditor } from "./useTemplateEditor";

const mocks = vi.hoisted(() => ({ save: vi.fn(), lock: vi.fn(), activate: vi.fn(), warning: vi.fn(), render: vi.fn(), frames: vi.fn(), analyze: vi.fn(),
  applied: undefined as ((block: AnalysisBox, pageNo: number) => void) | undefined }));
vi.mock("antd", () => ({ App: { useApp: () => ({ message: { warning: mocks.warning, success: vi.fn(), error: vi.fn() } }) } }));
vi.mock("./useTemplateLifecycle", () => ({ useTemplateLifecycle: (input: { selectedTemplate?: { revision: number } }) => ({
  saving: false, revision: input.selectedTemplate?.revision ?? 0, saveDraft: mocks.save, confirmLock: mocks.lock, confirmActivate: mocks.activate
}) }));
vi.mock("./useObjectiveGrid", () => ({ useObjectiveGrid: (input: { onApplied?: typeof mocks.applied }) => { mocks.applied = input.onApplied; return { openGrid: vi.fn() }; } }));
vi.mock("./answerSheetOCR", () => ({ renderAnswerSheetPage: mocks.render }));
vi.mock("./answerSheetImageRegions", () => ({ detectAnswerSheetFrames: mocks.frames }));

const group: LayoutRegion = { id: "choice-block", label: "选择题区域", x: .1, y: .1, width: .8, height: .4 };
const questions = [{ id: "q1", question_no: "1", question_type: "single_choice" }] as Question[];
const setPageNo = vi.fn();

describe("choice block draft lifecycle", () => {
  let root: Root; let host: HTMLDivElement; let current: ReturnType<typeof useTemplateEditor>;
  function Probe({ revision = answerTemplate.revision } = {}) {
    const selectedTemplate = useMemo(() => ({ ...answerTemplate, revision,
      layout: { pages: answerTemplate.layout.pages.map(page => ({ ...page, question_regions: [] })) } }), [revision]);
    current = useTemplateEditor({ examId: "exam", canManage: true,
      selectedTemplate, selectedPaper: answerSource, questions,
      setTemplates: vi.fn(), setSelectedTemplateId: vi.fn(), pdfRef: { current: undefined }, pageNo: 1, setPageNo, analyzePage: mocks.analyze,
      preview: { sourceId: answerSource.id, imageUrl: "blob:sheet", loading: false, width: 1000, height: 1000, pageCount: 1 } });
    return null;
  }
  beforeEach(async () => {
    vi.resetAllMocks(); Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.applied = undefined;
    mocks.frames.mockReturnValue([{ ...group, confidence: .95 }]);
    mocks.render.mockResolvedValue({ width: 1000, height: 1000, getContext: () => ({ getImageData: vi.fn() }) });
    mocks.analyze.mockResolvedValue({ regions: [], unassigned: [], objectiveGroups: [group] });
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
    await act(async () => root.render(<Probe />));
    await act(async () => current.suggestQuestionRegions());
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); });

  it("saves pending choice blocks as draft but blocks locking and activation until reviewed or discarded", async () => {
    expect(current.objectiveGroups.get(1)).toHaveLength(1);
    expect(current.layout.pages.flatMap(page => page.question_regions).some(region => region.id === group.id)).toBe(false);
    await act(async () => { await current.saveDraft(); current.confirmLock(); current.confirmActivate(); });
    expect(mocks.save).toHaveBeenCalledOnce(); expect(mocks.lock).not.toHaveBeenCalled(); expect(mocks.activate).not.toHaveBeenCalled();
    expect(mocks.warning).toHaveBeenCalledWith(expect.stringContaining("区域需要核对"));
    expect(current.layout.editor_state?.candidates).toMatchObject([{ role: "objective_group", region: { id: group.id } }]);
    await act(async () => current.discardObjectiveGroup(group.id));
    await act(async () => { await current.saveDraft(); current.confirmLock(); current.confirmActivate(); });
    expect(mocks.save).toHaveBeenCalledTimes(2); expect(mocks.lock).toHaveBeenCalledOnce(); expect(mocks.activate).toHaveBeenCalledOnce();
  });

  it("retains the full block after partial grid application and clears it only after complete application", async () => {
    // 网格 hook 只在 reducer 确认落入草稿后回调；本测试核对编辑器如何清理这张收据的范围。
    await act(async () => mocks.applied?.({ x: .1, y: .1, width: .2, height: .1 }, 1));
    expect(current.objectiveGroups.get(1)).toHaveLength(1);
    await act(async () => mocks.applied?.(group, 2));
    expect(current.objectiveGroups.get(1)).toHaveLength(1);
    await act(async () => mocks.applied?.(group, 1));
    expect(current.objectiveGroups.size).toBe(0);
  });

  it("invalidates unsaved choice suggestions after an external template revision changes", async () => {
    await act(async () => root.render(<Probe revision={answerTemplate.revision + 1} />));
    expect(current.objectiveGroups.size).toBe(0);
  });
});
