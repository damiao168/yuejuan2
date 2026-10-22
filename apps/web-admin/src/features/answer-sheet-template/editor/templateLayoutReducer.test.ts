import { describe, expect, it } from "vitest";
import type { LayoutRegion, TemplateLayout } from "../../../api/configuration";
import { templateLayoutReducer } from "./templateLayoutReducer";

function region(id: string, questionId: string): LayoutRegion {
  return {
    id, question_id: questionId, label: questionId,
    x: .1, y: .2, width: .3, height: .4, option_regions: []
  };
}
function layout(): TemplateLayout {
  return {
    pages: [1, 2].map((pageNo) => ({
      page_no: pageNo, width: 100, height: 200,
      registration_marks: [], identity_regions: [],
      question_regions: pageNo === 1 ? [region("old", "q1")] : []
    }))
  };
}

describe("template layout reducer", () => {
  it("moves a question region between pages without duplicate coverage", () => {
    const next = templateLayoutReducer(layout(), {
      type: "replaceQuestionRegion", questionId: "q1", pageNo: 2,
      region: region("new", "q1")
    });
    expect(next.pages[0].question_regions).toEqual([]);
    expect(next.pages[1].question_regions.map((item) => item.id)).toEqual(["new"]);
  });

  it("updates geometry and option regions without changing unrelated pages", () => {
    const original = layout();
    const moved = templateLayoutReducer(original, {
      type: "updateRegion", regionId: "old", patch: { x: .45, width: .25 }
    });
    const withOption = templateLayoutReducer(moved, {
      type: "addOption", regionId: "old",
      option: { id: "option-a", label: "A", x: .1, y: .2, width: .1, height: .1 }
    });
    const resized = templateLayoutReducer(withOption, {
      type: "updateOption", regionId: "old", optionId: "option-a", patch: { width: .2 }
    });
    expect(resized.pages[0].question_regions[0]).toMatchObject({
      x: .45, width: .25, option_regions: [{ id: "option-a", width: .2 }]
    });
    expect(resized.pages[1]).toBe(original.pages[1]);
    expect(original.pages[0].question_regions[0].x).toBe(.1);
  });

  it("replaces only matched suggestions and preserves other regions", () => {
    const original = layout();
    const withOther = templateLayoutReducer(original, {
      type: "addRegion", pageNo: 1, region: region("other", "q2")
    });
    const suggested = templateLayoutReducer(withOther, {
      type: "suggestRegions", pages: new Map([[1, [region("suggested", "q1")]]])
    });
    expect(suggested.pages[0].question_regions.map((item) => item.id)).toEqual(["other", "suggested"]);
  });

  it("moves suggested questions across all pages while preserving unmatched manual regions", () => {
    const original = layout(); original.pages[0].question_regions.push(region("manual", "q2"));
    const next = templateLayoutReducer(original, { type: "suggestRegions", pages: new Map([[2, [region("suggested", "q1")]]]) });
    expect(next.pages[0].question_regions.map(item => item.id)).toEqual(["manual"]);
    expect(next.pages[1].question_regions.map(item => item.id)).toEqual(["suggested"]);
    expect(next.pages.flatMap(page => page.question_regions).filter(item => item.question_id === "q1")).toHaveLength(1);
    expect(original.pages[0].question_regions.map(item => item.id)).toEqual(["old", "manual"]);
  });

  it("accepts a manually associated candidate only if that question still has no region", () => {
    const original = layout();
    const rejected = templateLayoutReducer(original, { type: "acceptSuggestedRegion", pageNo: 2,
      questionId: "q1", region: region("candidate", "q1") });
    expect(rejected).toBe(original);
    const accepted = templateLayoutReducer(original, { type: "acceptSuggestedRegion", pageNo: 2,
      questionId: "q2", region: region("candidate", "q2") });
    expect(accepted.pages[0].question_regions[0]).toBe(original.pages[0].question_regions[0]);
    expect(accepted.pages[1].question_regions).toEqual([{ ...region("candidate", "q2"), origin: "human", review_state: "edited" }]);
  });

  it("does not accept candidates without a matching nonempty question reference", () => {
    const original = layout();
    expect(templateLayoutReducer(original, { type: "acceptSuggestedRegion", pageNo: 2,
      questionId: "", region: { ...region("candidate", ""), question_id: undefined } })).toBe(original);
    expect(templateLayoutReducer(original, { type: "acceptSuggestedRegion", pageNo: 2,
      questionId: "q2", region: region("candidate", "q3") })).toBe(original);
  });

  it("preserves an existing manual question and deduplicates late automatic suggestions across pages", () => {
    const original = layout();
    const next = templateLayoutReducer(original, { type: "suggestRegions", preserveExisting: true, pages: new Map([
      [1, [region("q2-first", "q2"), { ...region("no-question", ""), question_id: undefined }]],
      [2, [region("manual-conflict", "q1"), region("q2-duplicate", "q2"), region("q3", "q3")]]
    ]) });
    expect(next.pages[0].question_regions.map(item => item.id)).toEqual(["old", "q2-first"]);
    expect(next.pages[1].question_regions.map(item => item.id)).toEqual(["q3"]);
  });

  it("keeps late identity suggestions in their own collection and preserves manual fields across retries", () => {
    const original = layout();
    const manual: LayoutRegion = { id: "manual-name", label: "姓名", x: .05, y: .04, width: .2, height: .05 };
    original.pages[0].identity_regions = [manual];
    const suggestions = new Map([[1, [
      { ...manual, id: "new-name", x: .4 },
      { ...manual, id: "new-id", label: "学号", y: .1, question_id: "q2" }
    ]], [2, [{ ...manual, id: "page-two-name" }]]]);
    const once = templateLayoutReducer(original, { type: "suggestIdentityRegions", pages: suggestions });
    const twice = templateLayoutReducer(once, { type: "suggestIdentityRegions", pages: suggestions });
    expect(twice.pages[0].identity_regions.map(item => item.id)).toEqual(["manual-name", "new-id"]);
    expect(twice.pages[0].identity_regions[0]).toBe(manual);
    expect(twice.pages[0].identity_regions[1].question_id).toBeUndefined();
    expect(twice.pages[1].identity_regions.map(item => item.id)).toEqual(["page-two-name"]);
    expect(twice.pages[0].question_regions).toBe(original.pages[0].question_regions);
    expect(original.pages[0].identity_regions).toEqual([manual]);
  });

  it("deduplicates overlapping generic identity fields while retaining separate blocks", () => {
    const field: LayoutRegion = { id: "one", label: "身份信息", x: .1, y: .1, width: .3, height: .1 };
    const next = templateLayoutReducer(layout(), { type: "suggestIdentityRegions", pages: new Map([[1,
      [field, { ...field, id: "duplicate", x: .11 }, { ...field, id: "separate", y: .3 }]]]) });
    expect(next.pages[0].identity_regions.map(item => item.id)).toEqual(["one", "separate"]);
  });

  it("updates and removes identity geometry without adding question references or options", () => {
    const original = layout();
    const withField = templateLayoutReducer(original, { type: "addIdentityRegion", pageNo: 1,
      region: { id: "identity", label: "学号", x: .1, y: .1, width: .3, height: .05 } });
    const edited = templateLayoutReducer(withField, { type: "updateRegion", regionId: "identity",
      patch: { x: .2, label: "张三", question_id: "q2", option_regions: [{ id: "A", label: "A", x: 0, y: 0, width: .1, height: .1 }] } });
    expect(edited.pages[0].identity_regions[0]).toMatchObject({ x: .2, label: "身份信息" });
    expect(edited.pages[0].identity_regions[0].question_id).toBeUndefined();
    expect(edited.pages[0].identity_regions[0].option_regions).toBeUndefined();
    const rejectedOption = templateLayoutReducer(edited, { type: "addOption", regionId: "identity",
      option: { id: "B", label: "B", x: 0, y: 0, width: .1, height: .1 } });
    expect(rejectedOption.pages[0].identity_regions).toBe(edited.pages[0].identity_regions);
    const removed = templateLayoutReducer(rejectedOption, { type: "removeRegion", regionId: "identity" });
    expect(removed.pages[0].identity_regions).toEqual([]);
    expect(removed.pages[0].question_regions).toEqual(original.pages[0].question_regions);
  });
});
