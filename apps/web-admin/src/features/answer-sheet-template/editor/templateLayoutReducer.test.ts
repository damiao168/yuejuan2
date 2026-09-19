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
});
