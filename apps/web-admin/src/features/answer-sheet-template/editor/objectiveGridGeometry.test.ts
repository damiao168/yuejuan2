import { describe, expect, it } from "vitest";
import type { ObjectiveGridGroup } from "../../../api/answerSheetObjectiveGrid";
import type { Question } from "../../../api/papers";
import { buildObjectiveGridRegions, orderObjectiveGroups, validateObjectiveGroups } from "./objectiveGridGeometry";

const block = { x: .05, y: .1, width: .9, height: .8 };
const question = (id: string, type = "single_choice") => ({ id, question_no: id, question_type: type, options: ["A", "B", "C", "D"] }) as Question;
function group(id: string, x = .1, y = .2): ObjectiveGridGroup {
  return { group_id: id, bbox: { x, y, width: .3, height: .1 }, reason_codes: [],
    options: Array.from({ length: 4 }, (_, index) => ({ bbox: { x: x + .02 + index * .06, y: y + .02, width: .04, height: .05 } })) };
}
const build = (groups: ObjectiveGridGroup[], assignments: Record<string, string>, questions: Question[], reverse = false) =>
  buildObjectiveGridRegions({ groups, block, assignments, questions, optionCount: 4, orientation: "horizontal", reverse });

describe("objective grid geometry", () => {
  it("converts measured page option boxes once to question-relative coordinates", () => {
    const result = build([group("g")], { g: "1" }, [question("1")]);
    expect(result.error).toBeUndefined();
    expect(result.regions[0]).toMatchObject({ question_id: "1", x: .1, y: .2, width: .3, height: .1, option_coordinate_space: "question_relative" });
    expect(result.regions[0].option_regions?.map(option => option.label)).toEqual(["A", "B", "C", "D"]);
    expect(result.regions[0].option_regions?.[0]).toMatchObject({ x: .06666667, y: .2, width: .13333333, height: .5 });
  });

  it("reverses labels over actual measured options without moving or inventing boxes", () => {
    const normal = build([group("g")], { g: "1" }, [question("1")]);
    const reverse = build([group("g")], { g: "1" }, [question("1")], true);
    expect(reverse.regions[0].option_regions?.[0].x).toBe(normal.regions[0].option_regions?.[3].x);
    expect(reverse.regions[0].option_regions?.[0].label).toBe("A");
  });

  it("sorts real question groups by row or column while allowing tiny alignment differences", () => {
    const groups = [group("d", .55, .5), group("b", .55, .201), group("c", .1, .5), group("a", .1, .2)];
    expect(orderObjectiveGroups(groups, "row_first").map(item => item.group_id)).toEqual(["a", "b", "c", "d"]);
    expect(orderObjectiveGroups(groups, "column_first").map(item => item.group_id)).toEqual(["a", "c", "b", "d"]);
  });

  it("rejects incomplete, clipped and overlapping groups rather than partitioning the block", () => {
    const incomplete = group("g"); incomplete.options.pop();
    expect(validateObjectiveGroups([incomplete], block, 4)).toContain("缺失");
    expect(validateObjectiveGroups([group("g", .9)], block, 4)).toContain("边界无效");
    expect(validateObjectiveGroups([group("g"), group("other", .11)], block, 4)).toContain("重叠");
    expect(build([], {}, []).regions).toEqual([]);
  });

  it("rejects missing, duplicate and non-choice question mappings as one batch", () => {
    expect(build([group("g")], {}, [question("1")]).error).toContain("正式选择题");
    expect(build([group("g"), group("other", .5)], { g: "1", other: "1" }, [question("1")]).error).toContain("同一题");
    expect(build([group("g")], { g: "1" }, [question("1", "calculation")]).error).toContain("正式选择题");
  });

  it("rejects an option count inconsistent with the exam question", () => {
    const q = question("1"); q.options = ["A", "B", "C"];
    expect(build([group("g")], { g: "1" }, [q]).error).toContain("选项数");
  });
});
