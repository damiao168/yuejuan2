import { describe, expect, it } from "vitest";
import { isCurrentArbitrationDetail, parseArbitrationScore } from "./ArbitrationPage";

describe("arbitration task switching", () => {
  it("blocks actions while the new task is loading or the detail belongs to the previous task", () => {
    expect(isCurrentArbitrationDetail("task-a", "task-b", false)).toBe(false);
    expect(isCurrentArbitrationDetail("task-b", "task-b", true)).toBe(false);
    expect(isCurrentArbitrationDetail(undefined, "task-b", false)).toBe(false);
    expect(isCurrentArbitrationDetail("task-b", "task-b", false)).toBe(true);
  });

  it("requires an explicit final score and still accepts an entered zero", () => {
    expect(parseArbitrationScore(null, 10)).toBeNull();
    expect(parseArbitrationScore(0, 10)).toBe(0);
    expect(parseArbitrationScore(10, 10)).toBe(10);
    expect(parseArbitrationScore(-1, 10)).toBeNull();
    expect(parseArbitrationScore(11, 10)).toBeNull();
    expect(parseArbitrationScore(Number.NaN, 10)).toBeNull();
  });
});
