import { describe, expect, it } from "vitest";
import {
  formatQuestionAppealScore,
  questionAppealReleaseSource,
  questionAppealReleaseStatus,
  questionAppealRubricPoints,
  questionAppealStatusColor
} from "./questionAppealPresentation";

describe("题目申诉展示纯决策", () => {
  it("状态颜色和发布文案保持全部已知及未知分支", () => {
    expect(["submitted", "resolved", "under_review", "other"].map(questionAppealStatusColor)).toEqual(["orange", "green", "blue", "blue"]);
    expect(["initial", "regrade", "other"].map(questionAppealReleaseSource)).toEqual(["首次发布", "重评发布", "其他来源"]);
    expect(["published", "superseded", "other"].map(questionAppealReleaseStatus)).toEqual(["已发布", "已被替代", "未知状态"]);
    expect([0, 1, 2.25, NaN, Infinity].map(formatQuestionAppealScore)).toEqual(["0", "1", "2.3", "NaN", "Infinity"]);
  });

  it("rubric 跳过非法点，保留原索引、字段优先级和非有限 number", () => {
    expect(questionAppealRubricPoints(undefined)).toEqual([]); expect(questionAppealRubricPoints({ points: "invalid" })).toEqual([]);
    expect(questionAppealRubricPoints({ points: [null, "bad", { id: 0, code: "  ", description: "  描述  ", name: "名称", score: 0, points: 9 }, { points: 2 }, {}, { score: NaN }, { score: Infinity }] })).toEqual([
      { key: "0", label: "  描述  ", score: 0 }, { key: "3", label: "评分点 4", score: 2 }, { key: "4", label: "评分点 5", score: undefined }, { key: "5", label: "评分点 6", score: NaN }, { key: "6", label: "评分点 7", score: Infinity }
    ]);
  });

  it("合法标量保留 nullish 优先级，未知对象和函数 key 明确回退原索引", () => {
    const symbol = Symbol("point");
    const points = [
      { id: "", code: "code" }, { id: false, code: "code" }, { id: 0 }, { id: 12n }, { id: symbol },
      { id: null, code: "code" }, { id: undefined, code: null }, { id: {}, code: "not-used" }, { id: [] }, { id: () => "not-used" }, { code: {} }
    ];
    const result = questionAppealRubricPoints({ points });
    expect(result.map((point) => point.key)).toEqual(["", "false", "0", "12", "Symbol(point)", "code", "6", "7", "8", "9", "10"]);
    expect(result.some((point) => point.key === "[object Object]")).toBe(false);
  });
});
