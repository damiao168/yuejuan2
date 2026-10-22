import { isSolutionSplitOffsetSafe, renderSolutionSpans, type RubricSolutionLine, type RubricSolutionSource, type RubricSolutionSpan } from "./rubricSolutionLines";

export function reindexSolutionLines(lines: RubricSolutionLine[]) {
  return lines.map((line, index) => ({ ...line, position: index + 1 }));
}

function coalesceSpans(spans: RubricSolutionSpan[]) {
  const result: RubricSolutionSpan[] = [];
  for (const span of spans) {
    const previous = result[result.length - 1];
    if (previous && previous.source_id === span.source_id && previous.end === span.start) previous.end = span.end;
    else result.push({ ...span });
  }
  return result;
}

export function lineFromSpans(spans: RubricSolutionSpan[], sources: RubricSolutionSource[]): RubricSolutionLine {
  const normalized = coalesceSpans(spans);
  return { id: normalized.map((span) => `${span.source_id}:${span.start}-${span.end}`).join("|"), position: 0,
    content: renderSolutionSpans(normalized, sources), spans: normalized };
}

export function solutionLineRawText(line: RubricSolutionLine, sources: RubricSolutionSource[]) {
  return (line.spans ?? []).map((span) => sources.find((source) => source.id === span.source_id)?.content.slice(span.start, span.end) ?? "").join("");
}

export function splitSolutionLine(line: RubricSolutionLine, offset: number, sources: RubricSolutionSource[]) {
  if (!isSolutionSplitOffsetSafe(line, sources, offset)) throw new Error("请选择完整文字或公式之间的安全分界");
  const left: RubricSolutionSpan[] = [], right: RubricSolutionSpan[] = [];
  let remaining = offset;
  for (const span of line.spans ?? []) {
    const length = span.end - span.start;
    const taken = Math.min(Math.max(remaining, 0), length);
    if (taken > 0) left.push({ ...span, end: span.start + taken });
    if (taken < length) right.push({ ...span, start: span.start + taken });
    remaining -= length;
  }
  if (!left.length || !right.length) throw new Error("请选择解答内容中间的拆分位置");
  const parts = [lineFromSpans(left, sources), lineFromSpans(right, sources)];
  if (parts.some((part) => !part.content.trim())) throw new Error("拆分后的两部分都需要包含解答内容");
  return parts;
}

export function mergeSolutionLines(lines: RubricSolutionLine[], sources: RubricSolutionSource[]) {
  return lineFromSpans(lines.flatMap((line) => line.spans ?? []), sources);
}

/** AI 只能建议相邻单元的分界；交叉引用同一步骤的采分目标不能直接转成分段。 */
export function proposedSolutionGroups(lines: RubricSolutionLine[], evidenceGroups: string[][], sources: RubricSolutionSource[]) {
  const groups = evidenceGroups.map((ids) => [...new Set(ids.map((id) => {
    const match = /^step-([1-9]\d*)$/.exec(id);
    const index = match ? Number(match[1]) - 1 : -1;
    if (index < 0 || index >= lines.length) throw new Error("建议引用的解答单元已失效，请重新生成");
    return index;
  }))].sort((a, b) => a - b)).sort((a, b) => a[0] - b[0]);
  let previous = -1;
  for (const group of groups) {
    if (!group.length || group[0] <= previous || group.some((value, index) => index > 0 && value !== group[index - 1] + 1)) {
      throw new Error("建议存在交叉或不连续的步骤，请使用拆分、合并手动调整");
    }
    previous = group[group.length - 1];
  }
  const result: RubricSolutionLine[] = [];
  let cursor = 0;
  for (const group of groups) {
    while (cursor < group[0]) result.push(lines[cursor++]);
    result.push(group.length === 1 ? lines[group[0]] : mergeSolutionLines(lines.slice(group[0], group[group.length - 1] + 1), sources));
    cursor = group[group.length - 1] + 1;
  }
  result.push(...lines.slice(cursor));
  return reindexSolutionLines(result);
}
