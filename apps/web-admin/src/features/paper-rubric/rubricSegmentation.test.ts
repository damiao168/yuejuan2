import { describe, expect, it } from "vitest";
import { rubricSolutionLines, rubricSolutionSources, type RubricSolutionLine } from "./rubricSolutionLines";
import { lineFromSpans, mergeSolutionLines, proposedSolutionGroups, solutionLineRawText, splitSolutionLine } from "./rubricSegmentation";
import type { Question } from "../../api/papers";

function data(text: string) {
  const question = { solution: { steps: [{ content: text }] } } as unknown as Question;
  return { lines: rubricSolutionLines(question), sources: rubricSolutionSources(question) };
}
describe("解答单元边界调整", () => {
  it("光标拆分长段保留全部原文，合并可恢复原单元身份", () => {
    const { sources } = data("已知长段解答需要教师指定边界🙂并保留公式 $x=2$");
    const original = lineFromSpans([{ source_id: "step-1", start: 0, end: sources[0].content.length }], sources);
    const parts = splitSolutionLine(original, 7, sources);
    expect(parts.map((part) => solutionLineRawText(part, sources)).join("")).toBe(sources[0].content);
    expect(mergeSolutionLines(parts, sources)).toEqual(original);
    expect(() => splitSolutionLine(original, sources[0].content.indexOf("🙂") + 1, sources)).toThrow("安全分界");
    expect(() => splitSolutionLine(original, sources[0].content.indexOf("x=2") + 1, sources)).toThrow("安全分界");
  });
  it("跨来源合并再拆回，不丢弃任一来源范围", () => {
    const sources = [{ id: "step-1", position: 1, content: "列式" }, { id: "step-3", position: 2, content: "求解" }];
    const units = sources.map((source) => lineFromSpans([{ source_id: source.id, start: 0, end: source.content.length }], sources));
    const merged = mergeSolutionLines(units, sources);
    expect(splitSolutionLine(merged, 2, sources)).toEqual(units);
  });
  it("AI仅组合连续不交叉的步骤，未引用的内容仍完整保留", () => {
    const { lines, sources } = data("第一步。第二步。第三步。第四步。");
    const grouped = proposedSolutionGroups(lines, [["step-1", "step-2"]], sources);
    expect(grouped).toHaveLength(3);
    expect(grouped.flatMap((line) => line.spans)).toEqual([
      ...mergeSolutionLines(lines.slice(0, 2), sources).spans!, ...lines[2].spans!, ...lines[3].spans!
    ]);
    expect(grouped.map((line) => solutionLineRawText(line, sources)).join("")).toBe(sources[0].content);
    expect(() => proposedSolutionGroups(lines, [["step-1", "step-3"]], sources)).toThrow("不连续");
    expect(() => proposedSolutionGroups(lines, [["step-1", "step-2"], ["step-2"]], sources)).toThrow("交叉");
    expect(() => proposedSolutionGroups(lines, [["step-99"]], sources)).toThrow("已失效");
  });
  it("拒绝把空白拆为配分单元", () => {
    const sources = [{ id: "step-1", position: 1, content: "文字  " }];
    const line: RubricSolutionLine = lineFromSpans([{ source_id: "step-1", start: 0, end: 4 }], sources);
    expect(() => splitSolutionLine(line, 2, sources)).toThrow("两部分");
  });
});
