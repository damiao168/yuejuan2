import { describe, expect, it } from "vitest";
import type { TemplateFillGradingGroup } from "../../../api/answerSheetTemplateAnalysis";
import type { LayoutRegion, TemplateLayout } from "../../../api/configuration";
import { answerQuestion } from "../testing/answerSheetFixtures";
import { applyAutomaticFillGradingGroups, clearInvalidAutomaticFillGroups, fillGradingGroupMembers } from "./fillGradingGroups";
import { templateLayoutReducer } from "./templateLayoutReducer";

const questions = [12, 13, 14].map(number => ({ ...answerQuestion, id: `q${number}`, question_no: `${number}`, question_type: "fill_blank", options: [] }));
const rows: LayoutRegion[] = questions.map((question, index) => ({ id: `r${index}`, question_id: question.id, label: question.question_no,
  x: index === 1 ? .23 : .09, y: index === 2 ? .59 : .54, width: .1, height: .025,
  origin: "local_detector", review_state: "suggested", semantic_check: "supported" }));
const group: TemplateFillGradingGroup = { group_id: "printed-fill-section", question_labels: ["12", "13", "14"],
  bbox: { x: .06, y: .52, width: .29, height: .11 }, reason_codes: ["LOCAL_FILL_SECTION_VERIFIED"] };
function layout(regions = rows): TemplateLayout {
  return { pages: [{ page_no: 1, width: 1600, height: 1132, registration_marks: [], identity_regions: [], question_regions: regions }] };
}

describe("fill grading region configuration", () => {
  it("automatically groups the supported 12–14 subregions using an independently measured section", () => {
    const grouped = applyAutomaticFillGradingGroups([group], rows, [], questions);
    expect(grouped).toHaveLength(3);
    expect(grouped.every(region => region.grading_group_id === group.group_id && region.grading_bbox?.height === .11)).toBe(true);
    expect(grouped.map(({ grading_group_id: _id, grading_bbox: _box, ...roi }) => roi)).toEqual(rows);
    expect(rows.every(region => !region.grading_group_id)).toBe(true);
  });

  it.each(["missing-member", "wrong-type", "unmeasured-frame", "unreviewed-member", "other-answer", "identity", "duplicate-group"])(
    "does not auto group a section with %s", failure => {
      const regions = structuredClone(rows), official = structuredClone(questions), groups = [structuredClone(group)];
      const identities: LayoutRegion[] = [];
      if (failure === "missing-member") regions.pop();
      if (failure === "wrong-type") official[0].question_type = "calculation";
      if (failure === "unmeasured-frame") groups[0].reason_codes = [];
      if (failure === "unreviewed-member") regions[0].semantic_check = "needs_review";
      if (failure === "other-answer") regions.push({ ...rows[0], id: "other", question_id: "other-question" });
      if (failure === "identity") identities.push({ ...rows[0], id: "name", question_id: undefined, label: "姓名" });
      if (failure === "duplicate-group") groups.push({ ...group, group_id: "ambiguous" });
      expect(applyAutomaticFillGradingGroups(groups, regions, identities, official).every(region => !region.grading_group_id)).toBe(true);
    });

  it("switches shared and individual grading atomically without changing subregions or clearing semantic issues", () => {
    const old = layout(rows.map(row => ({ ...row, semantic_check: "needs_review" })));
    const shared = templateLayoutReducer(old, { type: "setFillGradingGroup", pageNo: 1, mode: "shared", groupId: group.group_id,
      bbox: group.bbox, questionIds: questions.map(question => question.id) });
    expect(shared.pages[0].question_regions.every(region => region.grading_group_id === group.group_id)).toBe(true);
    expect(shared.pages[0].question_regions.every(region => region.semantic_check === "needs_review" && region.review_state === "suggested")).toBe(true);
    const individual = templateLayoutReducer(shared, { type: "setFillGradingGroup", pageNo: 1, mode: "individual", groupId: group.group_id,
      bbox: group.bbox, questionIds: [] });
    expect(individual).toEqual(old);
  });

  it("keeps a manual question region and falls back to independent grading if an automatic group becomes incomplete", () => {
    const grouped = applyAutomaticFillGradingGroups([group], rows, [], questions);
    const manual = { ...rows[0], review_state: "edited" as const };
    const final = clearInvalidAutomaticFillGroups([manual, ...grouped.slice(1)], []);
    expect(final[0]).toBe(manual);
    expect(final.every(region => !region.grading_group_id)).toBe(true);
    expect(final.map(region => region.question_id)).toEqual(rows.map(region => region.question_id));
  });

  it("allows removing a stale invalid group rather than trapping the teacher behind its validation error", () => {
    const grouped = applyAutomaticFillGradingGroups([group], rows, [], questions);
    const other = { ...rows[0], id: "other", question_id: "other-question" };
    const individual = templateLayoutReducer(layout([...grouped, other]), { type: "setFillGradingGroup", pageNo: 1, mode: "individual",
      groupId: group.group_id, bbox: { ...group.bbox, width: -1 }, questionIds: [] });
    expect(individual.pages[0].question_regions).toEqual([...rows, other]);
  });

  it("rejects a shared area that reaches a different answer or personal information", () => {
    const other = { ...rows[0], id: "other", question_id: "other-question" };
    expect(fillGradingGroupMembers(group, [...rows, other], [], questions)).toBeUndefined();
    const old = layout([...rows, other]);
    expect(templateLayoutReducer(old, { type: "setFillGradingGroup", pageNo: 1, mode: "shared", groupId: group.group_id,
      bbox: group.bbox, questionIds: questions.map(question => question.id) })).toBe(old);
  });
});
