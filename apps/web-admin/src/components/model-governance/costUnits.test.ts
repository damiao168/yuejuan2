import { describe, expect, it } from "vitest";
import { costToMicros, microsToCost } from "./costUnits";

describe("美元预算与微美元保存边界", () => {
	it.each([
		["0", 0],
		["0.000001", 1],
		["0.123456", 123456],
		["100", 100000000],
		["9007199254.740991", Number.MAX_SAFE_INTEGER],
	])("金额 %s 精确保存并重读", (value, micros) => {
		expect(costToMicros(value)).toBe(micros);
		expect(costToMicros(microsToCost(micros))).toBe(micros);
	});
	it.each([
		"",
		null,
		undefined,
		"-1",
		"0.0000001",
		"9007199254.740992",
		"1e3",
		"NaN",
	])("拒绝缺失、越界或不可精确的预算 %s", (value) => {
		expect(() => costToMicros(value)).toThrow();
	});
	it("整数尾零不被当作小数尾零删除", () => {
		expect(microsToCost(100000000)).toBe("100");
	});
});
