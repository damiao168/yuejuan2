import { describe, expect, it } from "vitest";
import { newRubricPointId, rubricPointIdentityError } from "./rubricPointIdentity";

describe("评分证据标识", () => {
  it("拒绝空白、首尾空格和重复标识，接受不连续的历史标识", () => {
    for (const ids of [[""], [" p1"], ["p1 "], ["p1", "p1"]]) expect(rubricPointIdentityError(ids.map(id => ({ id })))).toBeDefined();
    expect(rubricPointIdentityError(["p1", "p3", "human-review-p99"].map(id => ({ id })))).toBeUndefined();
  });
  it("新标识不是依赖当前题目数组长度的序号", () => {
    expect(new Set(Array.from({ length: 100 }, () => newRubricPointId())).size).toBe(100);
  });
});
