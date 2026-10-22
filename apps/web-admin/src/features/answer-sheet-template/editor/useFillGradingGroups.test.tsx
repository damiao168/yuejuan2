// @vitest-environment jsdom
import { act, useReducer, type Dispatch } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import type { TemplateFillGradingGroup } from "../../../api/answerSheetTemplateAnalysis";
import type { TemplateLayout } from "../../../api/configuration";
import { answerQuestion } from "../testing/answerSheetFixtures";
import { useFillGradingGroups } from "./useFillGradingGroups";
import { templateLayoutReducer, type TemplateLayoutAction } from "./templateLayoutReducer";

const questions = [12, 13, 14].map(number => ({ ...answerQuestion, id: `q${number}`, question_no: `${number}`, question_type: "fill_blank", options: [] }));
const group: TemplateFillGradingGroup = { group_id: "fill", question_labels: ["12", "13", "14"],
  bbox: { x: .1, y: .2, width: .8, height: .15 }, reason_codes: ["LOCAL_FILL_SECTION_VERIFIED"] };
const suggestions = new Map([[1, [group]]]);
const initial: TemplateLayout = { pages: [{ page_no: 1, width: 1600, height: 1132, registration_marks: [], identity_regions: [],
  question_regions: questions.map((question, index) => ({ id: question.id, question_id: question.id, label: question.question_no,
    x: .12 + index * .22, y: .22, width: .18, height: .1, grading_group_id: group.group_id, grading_bbox: group.bbox,
    origin: "local_detector", review_state: "suggested", semantic_check: "supported" })) }] };

describe("fill grading mode controller", () => {
  let root: Root, host: HTMLDivElement, current: ReturnType<typeof useFillGradingGroups>, actual: TemplateLayout;
  let dispatch: Dispatch<TemplateLayoutAction>;
  function Probe({ scope = "source-a", readonly = false, detected = true, starting = initial }: { scope?: string; readonly?: boolean; detected?: boolean; starting?: TemplateLayout }) {
    const [layout, dispatchLayout] = useReducer(templateLayoutReducer, structuredClone(starting));
    dispatch = dispatchLayout;
    actual = layout;
    current = useFillGradingGroups({ scopeKey: scope, layout, suggestions: detected ? suggestions : new Map(),
      selectedRegion: layout.pages[0].question_regions[0], selectedQuestionId: "q12", pageNo: 1, questions, readonly, dispatchLayout });
    return null;
  }
  beforeEach(() => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); });

  it("switches all three questions together and remembers the measured area for switching back", async () => {
    await act(async () => root.render(<Probe />));
    expect(current.fillGradingGroup).toMatchObject({ mode: "shared", questionLabels: ["12", "13", "14"], canShare: true });
    await act(async () => current.setFillGradingMode("individual"));
    expect(actual.pages[0].question_regions.every(region => !region.grading_group_id)).toBe(true);
    expect(current.fillGradingGroup?.mode).toBe("individual");
    await act(async () => current.setFillGradingMode("shared"));
    expect(actual).toEqual(initial);
  });

  it("does not transfer a remembered section to another source or mutate a readonly template", async () => {
    await act(async () => root.render(<Probe detected={false} />));
    await act(async () => current.setFillGradingMode("individual"));
    expect(current.fillGradingGroup?.canShare).toBe(true);
    await act(async () => root.render(<Probe scope="source-b" detected={false} />));
    expect(current.fillGradingGroup?.canShare).toBe(false);
    await act(async () => root.render(<Probe readonly />));
    const before = actual;
    await act(async () => current.setFillGradingMode("shared"));
    expect(actual).toBe(before);
  });

  it("allows an explicit manual shared area and separates the omitted former member", async () => {
    await act(async () => root.render(<Probe detected={false} />));
    const invalid = current.setFillGradingRegion(["q12"], group.bbox);
    expect(invalid).toBe(false);
    const box = { x: .1, y: .2, width: .44, height: .15 };
    await act(async () => { expect(current.setFillGradingRegion(["q12", "q13"], box)).toBe(true); });
    expect(actual.pages[0].grading_groups).toEqual([{ group_id: "fill", question_ids: ["q12", "q13"], bbox: box }]);
    expect(actual.pages[0].question_regions.every(region => !region.grading_group_id)).toBe(true);
    expect(actual.pages[0].question_regions.map(({ grading_group_id: _groupId, grading_bbox: _box, ...region }) => region))
      .toEqual(initial.pages[0].question_regions.map(({ grading_group_id: _groupId, grading_bbox: _box, ...region }) => region));
  });

  it("configures all formal fill questions from one verified area before any single-question ROI exists", async () => {
    const starting: TemplateLayout = { pages: [{ ...initial.pages[0], question_regions: [] }],
      editor_state: { version: 1, source_sha256: "source", candidates: [
        { id: "parent", page_no: 1, role: "uncertain", region: { id: "parent", ...group.bbox, review_state: "suggested" } },
        { id: "child12", page_no: 1, role: "answer", question_no: "12", region: { id: "child12", x: .12, y: .22, width: .1, height: .05 } }
      ] } };
    await act(async () => root.render(<Probe starting={starting} detected={false} />));
    expect(current.fillGradingGroup?.options.map(option => option.value)).toEqual(["q12", "q13", "q14"]);
    await act(async () => { expect(current.setFillGradingRegion(["q12", "q13", "q14"], group.bbox)).toBe(true); });
    expect(actual.pages[0].grading_groups?.[0]).toMatchObject({ question_ids: ["q12", "q13", "q14"], bbox: group.bbox });
    expect(actual.pages[0].question_regions).toEqual([]);
    expect(actual.editor_state?.candidates.every(candidate => candidate.region.review_state === "discarded")).toBe(true);
    expect(current.fillGradingGroup).toMatchObject({ mode: "shared", missingQuestionLabels: ["12", "13", "14"] });
    await act(async () => current.setFillGradingMode("individual"));
    expect(actual.pages[0].grading_groups).toEqual([]);
    expect(actual.pages[0].question_regions).toEqual([]);
    await act(async () => current.setFillGradingMode("shared"));
    expect(actual.pages[0].grading_groups?.[0].question_ids).toEqual(["q12", "q13", "q14"]);
  });

  it.each(["identity", "objective_group"] as const)("does not swallow a pending %s into the manual fill section", async role => {
    const starting: TemplateLayout = { pages: [{ ...initial.pages[0], question_regions: [] }],
      editor_state: { version: 1, source_sha256: "source", candidates: [
        { id: "conflict", page_no: 1, role, region: { id: "conflict", ...group.bbox } }
      ] } };
    await act(async () => root.render(<Probe starting={starting} detected={false} />));
    await act(async () => { expect(current.setFillGradingRegion(["q12", "q13", "q14"], group.bbox)).toBe(false); });
    expect(actual).toEqual(starting);
  });

  it("keeps an existing pair of real ROIs and records only this group's answer candidates as discarded when 14 is missing", async () => {
    const starting: TemplateLayout = { pages: [{ ...initial.pages[0], question_regions: initial.pages[0].question_regions.slice(0, 2) }],
      editor_state: { version: 1, source_sha256: "source", candidates: [
        { id: "parent", page_no: 1, role: "uncertain", region: { id: "parent", ...group.bbox, analysis_run_id: "old-analysis" } },
        { id: "child14", page_no: 1, role: "answer", question_no: "14", region: { id: "child14", x: .57, y: .22, width: .1, height: .05 } },
        { id: "other-question", page_no: 1, role: "uncertain", question_no: "15", region: { id: "other-question", x: .7, y: .22, width: .08, height: .05 } },
        { id: "choice-evidence", page_no: 1, role: "uncertain", region: { id: "choice-evidence", x: .8, y: .22, width: .08, height: .05,
          reason_codes: ["OBJECTIVE_QUESTION_REQUIRES_GROUP"] } }
      ] } };
    await act(async () => root.render(<Probe starting={starting} detected={false} />));
    await act(async () => { expect(current.setFillGradingRegion(["q12", "q13", "q14"], group.bbox)).toBe(true); });
    expect(actual.pages[0].question_regions).toHaveLength(2);
    expect(actual.pages[0].question_regions.map(({ grading_group_id: _id, grading_bbox: _box, ...roi }) => roi))
      .toEqual(starting.pages[0].question_regions.map(({ grading_group_id: _id, grading_bbox: _box, ...roi }) => roi));
    expect(actual.editor_state?.candidates.map(candidate => [candidate.id, candidate.region.review_state]))
      .toEqual([["parent", "discarded"], ["child14", "discarded"], ["other-question", undefined], ["choice-evidence", undefined]]);
    expect(actual.editor_state?.candidates[0].region.analysis_run_id).toBe("old-analysis");
    expect(current.fillGradingGroup?.missingQuestionLabels).toEqual(["14"]);
  });

  it("does not restore a cleared manual area from session memory", async () => {
    const starting: TemplateLayout = { pages: [{ ...initial.pages[0], question_regions: [],
      grading_groups: [{ group_id: "manual", question_ids: ["q12", "q13", "q14"], bbox: group.bbox }] }] };
    await act(async () => root.render(<Probe starting={starting} detected={false} />));
    await act(async () => {
      current.resetFillGradingGroups();
      dispatch({ type: "set", value: { pages: [{ ...starting.pages[0], grading_groups: [] }] } });
    });
    expect(current.fillGradingGroup?.canShare).toBe(false);
    await act(async () => current.setFillGradingMode("shared"));
    expect(actual.pages[0].grading_groups).toEqual([]);
  });
});
