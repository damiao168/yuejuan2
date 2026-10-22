import { describe, expect, it } from "vitest";
import { lineFromSpans, mergeSolutionLines, proposedSolutionGroups, solutionLineRawText, splitSolutionLine } from "./rubricSegmentation";
import { isSolutionSplitOffsetSafe, type RubricSolutionSource } from "./rubricSolutionLines";

const sources: RubricSolutionSource[] = [
  { id: "step-1", position: 1, content: "先设😀为未知数" },
  { id: "step-3", position: 2, content: "代入检验" },
  { id: "step-4", position: 3, content: "结论成立" }
];
const original = () => sources.map((source) => lineFromSpans([{ source_id: source.id, start: 0, end: source.content.length }], sources));

describe("教师分段与原文证据范围", () => {
  it("拆分再合并保持原文、UTF16范围和稳定身份，跨来源合并不伪造偏移", () => {
    const line = original()[0];
    expect(isSolutionSplitOffsetSafe(line, sources, 3)).toBe(false);
    expect(isSolutionSplitOffsetSafe(line, sources, 4)).toBe(true);
    const parts = splitSolutionLine(line, 4, sources);
    expect(parts.map((part) => solutionLineRawText(part, sources)).join("")).toBe(sources[0].content);
    expect(mergeSolutionLines(parts, sources)).toEqual(line);
    const joined = mergeSolutionLines(original().slice(0, 2), sources);
    expect(solutionLineRawText(joined, sources)).toBe(sources[0].content + sources[1].content);
    const restored = splitSolutionLine(joined, sources[0].content.length, sources);
    expect(restored.map((part) => part.spans)).toEqual(original().slice(0, 2).map((part) => part.spans));
  });

  it("AI只组合相邻完整单元，未引用的单元和原数组空洞来源全部保留", () => {
    const lines = original();
    const result = proposedSolutionGroups(lines, [["step-2", "step-1"]], sources);
    expect(result).toHaveLength(2);
    expect(result.flatMap((line) => line.spans ?? [])).toEqual(lines.flatMap((line) => line.spans ?? []));
    expect(result.map((line) => line.position)).toEqual([1, 2]);
    expect(result[1].id).toBe(lines[2].id);
  });

  it("AI证据交叉、漏序、未知单元或空目标不能改写分段", () => {
    for (const groups of [[["step-1", "step-3"]], [["step-1", "step-2"], ["step-2"]], [["step-4"]], [[]]]) {
      expect(() => proposedSolutionGroups(original(), groups, sources)).toThrow();
    }
  });
});
