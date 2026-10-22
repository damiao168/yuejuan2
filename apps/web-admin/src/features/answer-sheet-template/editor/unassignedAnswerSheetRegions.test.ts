import { describe, expect, it } from "vitest";
import type { LayoutRegion } from "../../../api/configuration";
import { collectUnassignedFrames, mergeUnassignedFrames } from "./unassignedAnswerSheetRegions";

const frame = { x: .1, y: .1, width: .8, height: .4, confidence: .95 };
const candidate = (id: string, patch: Partial<LayoutRegion> = {}): LayoutRegion => ({ id, ...frame, ...patch });

describe("unassigned printed answer-sheet frames", () => {
  it("keeps missing question references outside normal question regions without guessing", () => {
    const output = collectUnassignedFrames([frame, { ...frame, y: .55 }], []);
    expect(output).toHaveLength(2);
    expect(output.every(region => region.question_id === undefined)).toBe(true);
    expect(new Set(output.map(region => region.id)).size).toBe(2);
  });

  it("recognizes that several matched cells together cover an enclosing printed frame", () => {
    const cells = [candidate("left", { width: .4 }), candidate("right", { x: .5, width: .4 })];
    expect(collectUnassignedFrames([frame], cells)).toEqual([]);
  });

  it("does not double-count overlapping manual regions when deciding whether a frame is covered", () => {
    const cells = [candidate("one", { width: .45 }), candidate("duplicate", { width: .45 })];
    expect(collectUnassignedFrames([frame], cells)).toHaveLength(1);
  });

  it("deduplicates only the same-page geometry and retains existing IDs after retry", () => {
    const previous = new Map([[1, [candidate("old")]]]);
    const output = mergeUnassignedFrames(previous, new Map([
      [1, [candidate("retry", { x: .101 }), candidate("other", { y: .55 })]],
      [2, [candidate("back")]]
    ]), new Map());
    expect(output.get(1)?.map(region => region.id)).toEqual(["old", "other"]);
    expect(output.get(2)?.map(region => region.id)).toEqual(["back"]);
    expect(previous.get(1)?.map(region => region.id)).toEqual(["old"]);
  });

  it("keeps candidates on failed pages and removes frames that became associated", () => {
    const previous = new Map([[1, [candidate("one")]], [2, [candidate("two")]]]);
    const output = mergeUnassignedFrames(previous, new Map(), new Map([[1, [candidate("accepted", { question_id: "q1" })]]]));
    expect(output.has(1)).toBe(false);
    expect(output.get(2)?.map(region => region.id)).toEqual(["two"]);
  });
});
