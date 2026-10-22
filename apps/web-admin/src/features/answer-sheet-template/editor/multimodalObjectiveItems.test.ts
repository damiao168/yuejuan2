import { describe, expect, it } from "vitest";
import type { TemplateAnalysisCandidate, TemplateObjectiveItem } from "../../../api/answerSheetTemplateAnalysis";
import type { Question } from "../../../api/papers";
import { buildMultimodalObjectiveRegions } from "./multimodalObjectiveItems";

const question = (number: string): Question => ({ id: `q${number}`, question_no: number, question_type: "single_choice",
  options: ["第一项", "第二项", "第三项", "第四项"] } as Question);
const item = (number = "1", x = .12): TemplateObjectiveItem => ({ printed_question_label: number,
  bbox: { x, y: .22, width: .28, height: .08 }, options: ["D", "C", "B", "A"].map((label, index) => ({ label,
    bbox: { x: x + .02 + index * .05, y: .24, width: .03, height: .025 } })) });
const group = (...items: TemplateObjectiveItem[]): TemplateAnalysisCandidate => ({ candidate_id: "choices", page_no: 1,
  role: "objective_group", proposed_bbox: { x: .1, y: .2, width: .7, height: .3 }, printed_question_label: null,
  association_status: "unassociated", semantic_check: "supported", evidence_refs: [], reason_codes: [], objective_items: items });
const build = (candidate: TemplateAnalysisCandidate, questions = [question("1"), question("2")], candidates = [candidate]) =>
  buildMultimodalObjectiveRegions(candidate, questions, candidates);

describe("multimodal objective automatic correspondence", () => {
  it("uses printed labels rather than group order and converts actual option geometry exactly once", () => {
    const candidate = group(item("2"), item("1", .44));
    const regions = build(candidate)!;
    expect(regions.map(region => region.question_id)).toEqual(["q2", "q1"]);
    expect(regions[0].option_coordinate_space).toBe("question_relative");
    expect(regions[0].option_regions?.map(option => option.label)).toEqual(["D", "C", "B", "A"]);
    expect(regions[0].option_regions?.[0]).toMatchObject({ x: .07142857, y: .25, width: .10714286, height: .3125 });
  });

  it("does not partially accept a block that lost one option or includes a subjective question", () => {
    const missing = item("2", .44); missing.options.pop();
    expect(build(group(item(), missing))).toBeUndefined();
    expect(build(group(item(), item("2", .44)), [question("1"), { ...question("2"), question_type: "calculation" }])).toBeUndefined();
  });

  it("does not let repeated numbers across blocks silently overwrite another question", () => {
    const first = group(item());
    const second = group(item("1", .44));
    expect(build(first, [question("1")], [first, second])).toBeUndefined();
    expect(build(second, [question("1")], [first, second])).toBeUndefined();
  });

  it("keeps an uncertain or out-of-parent option block available for correction", () => {
    const outside = item(); outside.options[0].bbox.x = .9;
    expect(build(group(outside))).toBeUndefined();
    const duplicated = item(); duplicated.options[0].label = "A";
    expect(build(group(duplicated))).toBeUndefined();
    expect(build(group(item(), item("2", .2)))).toBeUndefined();
    expect(build(group(item("999")))).toBeUndefined();
  });
});
