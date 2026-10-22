// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ObjectiveGridAnalysis } from "../../../api/answerSheetObjectiveGrid";
import type { AnswerSheetTemplate, TemplateLayout } from "../../../api/configuration";
import type { PaperVersion, Question } from "../../../api/papers";
import { useObjectiveGrid } from "./useObjectiveGrid";
import { templateLayoutReducer, type TemplateLayoutAction } from "./templateLayoutReducer";

const mocks = vi.hoisted(() => ({ analyze: vi.fn(), image: vi.fn(), dispatch: vi.fn(), applied: vi.fn(), confirm: vi.fn(), warning: vi.fn(), success: vi.fn() }));
vi.mock("../../../api/answerSheetObjectiveGrid", () => ({ analyzeAnswerSheetObjectiveGrid: mocks.analyze }));
vi.mock("./objectiveGridPageImage", () => ({ objectiveGridPageImage: mocks.image }));
vi.mock("antd", () => ({ App: { useApp: () => ({ message: { warning: mocks.warning, success: mocks.success }, modal: { confirm: mocks.confirm } }) } }));
const block = { x: .05, y: .1, width: .9, height: .8 };
const question = { id: "q1", question_no: "1", question_type: "single_choice", options: ["A", "B", "C", "D"] } as Question;
const paper = { id: "paper", file: { hash_sha256: "original" } } as PaperVersion;
const template = { id: "template", exam_id: "exam", status: "draft" } as AnswerSheetTemplate;
function layout(): TemplateLayout { return { pages: [{ page_no: 1, width: 1000, height: 1000, registration_marks: [], identity_regions: [], question_regions: [] }] }; }
function analysis(): ObjectiveGridAnalysis { return { template_id: "template", template_revision: 1, source_sha256: "original", analysis: {
  request_id: "request", schema_version: "answer-sheet-objective-grid-v1", status: "review_required", coordinate_space: "source_display_normalized",
  page_no: 1, source_sha256: "pixels", block_bbox: block, reason_codes: [], versions: { algorithm: "test" }, groups: [{ group_id: "group",
    bbox: { x: .1, y: .2, width: .3, height: .1 }, reason_codes: ["GEOMETRY_ONLY_REQUIRES_REVIEW"],
    options: Array.from({ length: 4 }, (_, index) => ({ bbox: { x: .12 + index * .06, y: .22, width: .04, height: .05 } })) }]
} }; }
function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>(done => { resolve = done; }); return { promise, resolve }; }
type HookInput = Parameters<typeof useObjectiveGrid>[0];

describe("objective grid review state", () => {
  let root: Root; let host: HTMLDivElement; let current: ReturnType<typeof useObjectiveGrid>; let original: TemplateLayout;
  function Probe(props: HookInput) { current = useObjectiveGrid(props); return null; }
  async function mount(patch: Partial<HookInput> = {}) {
    await act(async () => root.render(<Probe selectedTemplate={template} revision={1} selectedPaper={paper} pageNo={1} imageURL="blob:page"
      readonly={false} layout={original} questions={[question]} pdfRef={{ current: undefined }} dispatchLayout={mocks.dispatch} onApplied={mocks.applied} {...patch} />));
  }
  async function prepare(questions = [question]) {
    await mount({ questions });
    await act(async () => current.openGrid());
    await act(async () => current.changeBlock(block));
    await act(async () => current.changeQuestionIds(questions.map(item => item.id)));
    await act(async () => current.analyze());
  }
  async function review() {
    await act(async () => current.assignQuestion("group", "q1"));
    await act(async () => current.setReviewed(true));
  }
  beforeEach(() => {
    vi.resetAllMocks(); Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    original = layout(); host = document.createElement("div"); document.body.append(host); root = createRoot(host);
    mocks.image.mockResolvedValue({ canvas: {}, width: 1000, height: 1000, data_base64: "pixels", sha256: "pixels", media_type: "image/jpeg" });
    mocks.analyze.mockResolvedValue(analysis());
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); });

  it("requires explicit question mapping and review before an atomic draft apply", async () => {
    await prepare();
    expect(mocks.analyze.mock.calls[0][1]).toMatchObject({ expected_revision: 1, expected_source_sha256: "original", block_bbox: block, option_count: 4, option_orientation: "horizontal" });
    await act(async () => current.apply()); expect(mocks.dispatch).not.toHaveBeenCalled();
    await review(); await act(async () => current.apply());
    expect(mocks.dispatch).toHaveBeenCalledOnce();
    expect(mocks.dispatch.mock.calls[0][0]).toMatchObject({ type: "applyObjectiveGrid", pageNo: 1, expectedLayout: original,
      regions: [{ question_id: "q1", option_coordinate_space: "question_relative", option_regions: [{ label: "A" }, { label: "B" }, { label: "C" }, { label: "D" }] }] });
    expect(mocks.applied).not.toHaveBeenCalled();
    const next = templateLayoutReducer(original, mocks.dispatch.mock.calls[0][0] as TemplateLayoutAction);
    await mount({ layout: next });
    expect(mocks.applied).toHaveBeenCalledWith(block, 1);
    expect(current.open).toBe(false);
  });

  it.each(["page", "source", "version", "readonly"] as const)("aborts and discards old results after %s changes", async boundary => {
    const delayed = deferred<ObjectiveGridAnalysis>(); mocks.analyze.mockReturnValue(delayed.promise);
    await mount(); await act(async () => current.openGrid()); await act(async () => current.changeBlock(block));
    let pending!: Promise<void>;
    await act(async () => { pending = current.analyze(); });
    await vi.waitFor(() => expect(mocks.analyze).toHaveBeenCalledOnce());
    const signal = mocks.analyze.mock.calls[0][2] as AbortSignal;
    await mount(boundary === "page" ? { pageNo: 2 } : boundary === "source" ? { selectedPaper: { ...paper, id: "other" } }
      : boundary === "version" ? { revision: 2 } : { readonly: true });
    expect(signal.aborted).toBe(true);
    await act(async () => { delayed.resolve(analysis()); await pending; });
    expect(current.analysis).toBeUndefined(); expect(current.open).toBe(false); expect(mocks.dispatch).not.toHaveBeenCalled();
  });

  it.each(["CLIPPED_OPTION_CANDIDATES", "OPTION_GROUP_CONFLICT"])("does not apply groups after %s", async reason => {
    const partial = analysis(); partial.analysis.reason_codes = [reason];
    mocks.analyze.mockResolvedValue(partial);
    await prepare(); await review(); await act(async () => current.apply());
    expect(current.validationError).toContain("裁断"); expect(mocks.dispatch).not.toHaveBeenCalled();
  });

  it.each(["OPTION_COUNT_OR_GROUPING_UNCERTAIN", "OPTION_SPACING_OR_SIZE_UNCERTAIN"])("applies only explicitly assigned complete groups after both reviews for %s", async reason => {
    const partial = analysis(); partial.analysis.reason_codes = [reason]; mocks.analyze.mockResolvedValue(partial);
    await prepare(); expect(current.partialDetection).toBe(true);
    await review(); await act(async () => current.apply());
    expect(mocks.dispatch).not.toHaveBeenCalled();
    await act(async () => current.setReviewed(false)); await act(async () => current.setPartialReviewed(true));
    await act(async () => current.apply()); expect(mocks.dispatch).not.toHaveBeenCalled();
    await act(async () => current.setReviewed(true)); await act(async () => current.apply());
    const action = mocks.dispatch.mock.calls[0][0] as TemplateLayoutAction;
    expect(action).toMatchObject({ type: "applyObjectiveGrid", regions: [{ question_id: "q1", option_regions: [{ label: "A" }, { label: "B" }, { label: "C" }, { label: "D" }] }] });
    const next = templateLayoutReducer(original, action); await mount({ layout: next });
    expect(mocks.applied).toHaveBeenCalledExactlyOnceWith(expect.objectContaining(partial.analysis.groups[0].bbox), 1);
    expect(mocks.applied).not.toHaveBeenCalledWith(block, 1);
    expect(mocks.success).toHaveBeenCalledWith(expect.stringContaining("漏检题目请另行补框"));
  });

  it("disables bulk mapping for partial detections and invalidates both reviews after a manual reassignment", async () => {
    const partial = analysis(); partial.analysis.reason_codes = ["OPTION_COUNT_OR_GROUPING_UNCERTAIN"]; mocks.analyze.mockResolvedValue(partial);
    await prepare(); await act(async () => current.changeQuestionIds(["q1"]));
    await act(async () => current.mapSelectedQuestions());
    expect(current.assignments).toEqual({}); expect(current.error).toContain("不能按阅读顺序批量映射");
    await review(); await act(async () => current.setPartialReviewed(true));
    await act(async () => current.assignQuestion("group", "q1"));
    expect(current.reviewed).toBe(false); expect(current.partialReviewed).toBe(false);
    await act(async () => current.apply()); expect(mocks.dispatch).not.toHaveBeenCalled();
  });

  it("detects six of eight groups as partial even when the detector does not report missing groups", async () => {
    const questions = Array.from({ length: 8 }, (_, index) => ({ ...question, id: `q${index + 1}`, question_no: `${index + 1}` }));
    const missing = analysis();
    missing.analysis.groups = Array.from({ length: 6 }, (_, index) => {
      const x = .08 + Math.floor(index / 2) * .29; const y = .2 + (index % 2) * .2;
      return { group_id: `g${index}`, bbox: { x, y, width: .22, height: .12 }, reason_codes: [],
        options: Array.from({ length: 4 }, (_, option) => ({ bbox: { x: x + .01 + option * .05, y: y + .02, width: .04, height: .05 } })) };
    });
    mocks.analyze.mockResolvedValue(missing);
    await prepare(questions);
    expect(current.partialDetection).toBe(true);
    await act(async () => current.mapSelectedQuestions());
    expect(current.assignments).toEqual({}); expect(current.error).toContain("不能按阅读顺序批量映射");
    // 图上第 2、5 题漏格，剩余组只能按原图逐个指认；顺次套 1–6 会把题号整体错位。
    for (const [index, number] of [1, 3, 4, 6, 7, 8].entries()) await act(async () => current.assignQuestion(`g${index}`, `q${number}`));
    await act(async () => current.setReviewed(true)); await act(async () => current.apply());
    expect(mocks.dispatch).not.toHaveBeenCalled();
    await act(async () => current.setPartialReviewed(true)); await act(async () => current.apply());
    const action = mocks.dispatch.mock.calls[0][0] as Extract<TemplateLayoutAction, { type: "applyObjectiveGrid" }>;
    expect(action.regions.map(region => region.question_id)).toEqual(["q1", "q3", "q4", "q6", "q7", "q8"]);
  });

  it("requires the full block question scope before accepting reviewed groups", async () => {
    await prepare(); await act(async () => current.changeQuestionIds([]));
    await review(); await act(async () => current.apply());
    expect(current.validationError).toContain("本大区包含的全部题目"); expect(mocks.dispatch).not.toHaveBeenCalled();
  });

  it("rejects assignments outside the selected block and clears mappings removed from that scope", async () => {
    const other = { ...question, id: "q2", question_no: "2" };
    await prepare([question, other]);
    await act(async () => current.changeQuestionIds(["q1"]));
    await act(async () => current.assignQuestion("group", "q2"));
    await act(async () => current.setReviewed(true)); await act(async () => current.apply());
    expect(current.validationError).toContain("不在本大区所选题目中"); expect(mocks.dispatch).not.toHaveBeenCalled();
    await act(async () => current.changeQuestionIds(["q1"]));
    expect(current.assignments).toEqual({}); expect(current.reviewed).toBe(false);
  });

  it("rejects an uncertain individual group even after the teacher checks both confirmations", async () => {
    const partial = analysis(); partial.analysis.groups[0].reason_codes.push("OPTION_SPACING_OR_SIZE_UNCERTAIN"); mocks.analyze.mockResolvedValue(partial);
    await prepare(); await review(); await act(async () => current.setPartialReviewed(true)); await act(async () => current.apply());
    expect(current.validationError).toContain("不完整题组"); expect(mocks.dispatch).not.toHaveBeenCalled();
  });

  it("rejects duplicate explicit question assignments in a partial batch without inventing missing questions", async () => {
    const partial = analysis(); partial.analysis.reason_codes = ["OPTION_COUNT_OR_GROUPING_UNCERTAIN"];
    const first = partial.analysis.groups[0];
    partial.analysis.groups.push({ ...first, group_id: "other", bbox: { ...first.bbox, x: first.bbox.x + .4 },
      options: first.options.map(option => ({ bbox: { ...option.bbox, x: option.bbox.x + .4 } })) });
    mocks.analyze.mockResolvedValue(partial);
    await prepare(); await act(async () => current.assignQuestion("group", "q1")); await act(async () => current.assignQuestion("other", "q1"));
    await act(async () => current.setReviewed(true)); await act(async () => current.setPartialReviewed(true)); await act(async () => current.apply());
    expect(current.error).toContain("同一题不能关联多个题组"); expect(mocks.dispatch).not.toHaveBeenCalled();
  });

  it("confirms overwrite and rejects the confirmation after unrelated draft edits", async () => {
    original.pages[0].question_regions.push({ id: "old", question_id: "q1", label: "1", x: .1, y: .2, width: .3, height: .1 });
    await prepare(); await review(); await act(async () => current.apply());
    expect(mocks.dispatch).not.toHaveBeenCalled(); expect(mocks.confirm).toHaveBeenCalledOnce();
    const confirm = mocks.confirm.mock.calls[0][0] as { onOk: () => void };
    await mount({ layout: { ...original } });
    await act(async () => confirm.onOk());
    expect(mocks.dispatch).not.toHaveBeenCalled(); expect(mocks.warning).toHaveBeenCalledWith(expect.stringContaining("草稿或候选已变化"));
  });

  it("changing detection parameters clears candidates rather than reusing old option grouping", async () => {
    await prepare(); await review(); await act(async () => current.changeOptionCount(5));
    expect(current.analysis).toBeUndefined(); expect(current.reviewed).toBe(false); expect(current.assignments).toEqual({});
  });

  it("opens a suggested block directly and clears old analysis and review before using another block", async () => {
    await mount(); await act(async () => current.openGrid(block));
    expect(current.open).toBe(true); expect(current.block).toEqual(block);
    await act(async () => current.analyze()); await review();
    const nextBlock = { x: .1, y: .15, width: .8, height: .7 };
    await act(async () => current.openGrid(nextBlock));
    expect(current.block).toEqual(nextBlock); expect(current.analysis).toBeUndefined();
    expect(current.assignments).toEqual({}); expect(current.reviewed).toBe(false);
    expect(current.partialReviewed).toBe(false); expect(mocks.dispatch).not.toHaveBeenCalled();
  });

  it("rejects an invalid suggested block without changing a current reviewed analysis", async () => {
    await prepare(); await review();
    const currentAnalysis = current.analysis;
    await act(async () => current.openGrid({ x: .9, y: .1, width: .2, height: .8 }));
    expect(current.block).toEqual(block); expect(current.analysis).toBe(currentAnalysis); expect(current.reviewed).toBe(true);
  });

  it("aborts the previous block request and ignores its result when a new suggested block is opened", async () => {
    const delayed = deferred<ObjectiveGridAnalysis>(); mocks.analyze.mockReturnValueOnce(delayed.promise);
    await mount(); await act(async () => current.openGrid(block));
    let pending!: Promise<void>;
    await act(async () => { pending = current.analyze(); });
    await vi.waitFor(() => expect(mocks.analyze).toHaveBeenCalledOnce());
    const signal = mocks.analyze.mock.calls[0][2] as AbortSignal;
    const nextBlock = { x: .1, y: .15, width: .8, height: .7 };
    await act(async () => current.openGrid(nextBlock)); expect(signal.aborted).toBe(true);
    await act(async () => { delayed.resolve(analysis()); await pending; });
    expect(current.block).toEqual(nextBlock); expect(current.analysis).toBeUndefined(); expect(current.busy).toBe(false);
  });

  it("keeps the review open and preserves old candidates when the reducer rejects a stale batch", async () => {
    await prepare(); await review(); await act(async () => current.apply());
    expect(current.applying).toBe(true);
    // 同一事件批次里另一条编辑先落入 reducer；旧 expectedLayout 必须整体失败，不能清理候选。
    const concurrent = { ...original, pages: original.pages.map(page => ({ ...page })) };
    const rejected = templateLayoutReducer(concurrent, mocks.dispatch.mock.calls[0][0] as TemplateLayoutAction);
    expect(rejected).toBe(concurrent);
    await mount({ layout: rejected });
    expect(current.open).toBe(true); expect(current.applying).toBe(false);
    expect(current.error).toContain("本批题区未应用");
    expect(mocks.applied).not.toHaveBeenCalled(); expect(mocks.success).not.toHaveBeenCalled();
  });
});
