import { describe, expect, it } from "vitest";
import type { LayoutRegion, TemplateLayout } from "../../../api/configuration";
import { templateLayoutReducer } from "./templateLayoutReducer";

const region = (id: string, questionID: string, x = .1): LayoutRegion => ({ id, question_id: questionID, x, y: .2, width: .2, height: .1,
  option_coordinate_space: "question_relative", option_regions: ["A", "B"].map((label, index) => ({ id: `${id}-${label}`, label,
    x: .1 + index * .4, y: .1, width: .2, height: .5 })) });
const layout = (): TemplateLayout => ({ omr_profile: { mode: "manual_only", version: "opencv-fill-v1" }, pages: [1, 2].map(page => ({
  page_no: page, width: 1000, height: 1000, identity_regions: [], registration_marks: [],
  question_regions: page === 1 ? [region("old", "1"), region("unrelated", "3", .7)] : []
})) });

describe("objective grid atomic reducer", () => {
  it("replaces only selected questions across pages and preserves the OMR mode and unrelated regions", () => {
    const original = layout();
    const regions = [region("new-1", "1"), region("new-2", "2", .4)];
    const next = templateLayoutReducer(original, { type: "applyObjectiveGrid", pageNo: 2, regions, expectedLayout: original });
    expect(next.pages[0].question_regions.map(item => item.id)).toEqual(["unrelated"]);
    expect(next.pages[1].question_regions).toEqual(regions.map(region => ({ ...region, origin: "human", review_state: "edited" })));
    expect(next.pages[0].question_regions[0]).toBe(original.pages[0].question_regions[1]);
    expect(next.omr_profile).toBe(original.omr_profile);
    expect(original.pages[0].question_regions).toHaveLength(2);
  });

  it("rejects the whole batch after the draft changed during overwrite confirmation", () => {
    const original = layout(); const changed = { ...original };
    expect(templateLayoutReducer(changed, { type: "applyObjectiveGrid", pageNo: 2, regions: [region("new", "1")], expectedLayout: original })).toBe(changed);
  });

  it("does not partially apply duplicate, out-of-bounds or conflicting question regions", () => {
    const original = layout();
    for (const regions of [[region("a", "1"), region("b", "1", .4)], [{ ...region("a", "1"), x: .95 }],
      [{ ...region("a", "1"), option_coordinate_space: undefined }], [region("a", "2", .7)]]) {
      expect(templateLayoutReducer(original, { type: "applyObjectiveGrid", pageNo: 1, regions, expectedLayout: original })).toBe(original);
    }
  });
});
