import { describe, expect, it } from "vitest";
import { costToMicros, microsToCost } from "./costUnits";

describe("微美元展示的小数尾零", () => {
  it.each([
    [0, "0"], [1, "0.000001"], [10, "0.00001"], [100_000, "0.1"],
    [1_000_000, "1"], [10_000_000, "10"], [10_010_000, "10.01"],
    [Number.MAX_SAFE_INTEGER, "9007199254.740991"]
  ])("金额 %i 不损失整数或小数", (micros, text) => {
    expect(microsToCost(micros)).toBe(text);
    expect(costToMicros(microsToCost(micros))).toBe(micros);
  });

  it.each([-1, 0.5, Number.NaN, Number.POSITIVE_INFINITY, Number.MAX_SAFE_INTEGER + 1])("拒绝非安全微美元 %s", (value) => {
    expect(() => microsToCost(value)).toThrow("成本数据超出可准确显示的范围");
  });
});
