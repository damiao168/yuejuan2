import type { Question } from "../../api/papers";

export interface RubricSolutionSource {
  id: string;
  position: number;
  content: string;
}

export interface RubricSolutionSpan {
  source_id: string;
  /** 相对于 canonical source.content 的 UTF-16 偏移，可直接用于 String.slice。 */
  start: number;
  end: number;
}

export interface RubricSolutionLine extends RubricSolutionSource {
  // 旧调用方可只提供显示内容；自动划分产生的单元总是带原文范围。
  spans?: RubricSolutionSpan[];
}

interface MathRange {
  start: number;
  end: number;
  bodyStart: number;
  bodyEnd: number;
  display: boolean;
  closed: boolean;
  wrapper: string | null;
  wrapperPrefix: string;
  contentStart: number;
  contentEnd: number;
  rowOffsets: number[];
}

const splittableEnvironments = new Set(["aligned", "align", "align*", "alignedat", "gathered", "gather", "gather*", "eqnarray", "eqnarray*"]);
const environmentToken = /\\(begin|end)\{([^{}]+)\}/g;

/** 保留后端既有的 source 规范；展示单元继续细分，但证据 ID 不随划分而改写。 */
function splitLogicalLines(text: string): string[] {
  const result: string[] = [];
  let current: string[] = [];
  let displayMath: "dollar" | "bracket" | null = null;
  for (const rawLine of text.replace(/\r\n?/g, "\n").split("\n")) {
    if (!rawLine.trim() && !displayMath) continue;
    current.push(rawLine);
    for (const delimiter of rawLine.match(/\$\$|\\\[|\\\]/g) ?? []) {
      if (delimiter === "$$") {
        if (displayMath === "bracket") continue;
        displayMath = displayMath === "dollar" ? null : "dollar";
      } else if (delimiter === "\\[" && !displayMath) displayMath = "bracket";
      else if (delimiter === "\\]" && displayMath === "bracket") displayMath = null;
    }
    if (!displayMath) {
      result.push(current.join("\n").trim());
      current = [];
    }
  }
  if (current.length) result.push(current.join("\n").trim());
  return result;
}

/** 与 Go 端共用 trim、空步骤保留原数组编号、raw_text 回退规则。偏移从这里的内容起算。 */
export function rubricSolutionSources(question: Question | null | undefined): RubricSolutionSource[] {
  const steps = (question?.solution?.steps ?? [])
    .map((step, index) => ({ id: `step-${index + 1}`, content: step.content.trim() }))
    .filter((step) => step.content);
  if (steps.length) return steps.map((step, index) => ({ ...step, position: index + 1 }));
  const rawText = question?.solution?.raw_text?.trim();
  const answer = question?.answer_key?.standard_answer;
  const text = rawText || (typeof answer === "string" ? answer.trim() : "");
  return text ? splitLogicalLines(text).map((content, index) => ({ id: `step-${index + 1}`, position: index + 1, content })) : [];
}

function escaped(text: string, index: number): boolean {
  let count = 0;
  for (let at = index - 1; at >= 0 && text[at] === "\\"; at -= 1) count += 1;
  return count % 2 === 1;
}

function closingDelimiter(text: string, delimiter: string, start: number): number {
  for (let at = text.indexOf(delimiter, start); at >= 0; at = text.indexOf(delimiter, at + delimiter.length)) {
    if (!escaped(text, at)) return at;
  }
  return -1;
}

function outerWrapper(text: string, start: number, end: number): { name: string; start: number; end: number } | null {
  const body = text.slice(start, end);
  const leading = body.match(/^\s*\\begin\{([^{}]+)\}/);
  if (!leading || !splittableEnvironments.has(leading[1])) return null;
  const name = leading[1];
  const closing = `\\end{${name}}`;
  const final = body.trimEnd();
  if (!final.endsWith(closing)) return null;
  const stack: string[] = [];
  environmentToken.lastIndex = 0;
  for (const token of body.matchAll(environmentToken)) {
    if (token[1] === "begin") stack.push(token[2]);
    else if (stack.pop() !== token[2]) return null;
    // 同级的第二个环境不属于同一个外壳，不能把它们误当推导的多行。
    if (!stack.length && (token.index ?? 0) + token[0].length !== final.length) return null;
  }
  if (stack.length) return null;
  let contentStart = start + leading[0].length;
  // alignedat 的列数参数是环境元数据，不能成为第一行解答内容。
  if (name === "alignedat") contentStart += text.slice(contentStart, end).match(/^\s*\{\d+\}/)?.[0].length ?? 0;
  return { name, start: contentStart, end: start + final.length - closing.length };
}

function rowOffsets(text: string, start: number, end: number, allowBareNewlines: boolean): number[] {
  const offsets: number[] = [];
  const environments: string[] = [];
  let braces = 0;
  let pairedDelimiters = 0;
  let malformed = false;
  for (let at = start; at < end; at += 1) {
    const token = text.slice(at, end).match(/^\\(begin|end)\{([^{}]+)\}/);
    if (token && !escaped(text, at)) {
      if (token[1] === "begin") environments.push(token[2]);
      else if (environments.pop() !== token[2]) malformed = true;
      at += token[0].length - 1;
      continue;
    }
    const paired = text.slice(at, end).match(/^\\(left|right)\b/);
    if (paired && !escaped(text, at)) {
      pairedDelimiters += paired[1] === "left" ? 1 : -1;
      if (pairedDelimiters < 0) malformed = true;
      at += paired[0].length - 1;
      continue;
    }
    if (text[at] === "{" && !escaped(text, at)) braces += 1;
    if (text[at] === "}" && !escaped(text, at)) {
      braces -= 1;
      if (braces < 0) malformed = true;
    }
    if (braces !== 0 || pairedDelimiters !== 0 || environments.length || malformed) continue;
    if (text.startsWith("\\\\", at) && !escaped(text, at)) {
      let finish = at + 2;
      const spacing = text.slice(finish, end).match(/^\*?(?:\[[^\]\n]*\])?/);
      finish += spacing?.[0].length ?? 0;
      offsets.push(finish);
      at = finish - 1;
    } else if (allowBareNewlines && text[at] === "\n") offsets.push(at + 1);
  }
  // 不完整的括号或环境没有可靠的结构边界，保留整个公式供教师核对。
  return braces || pairedDelimiters || environments.length || malformed ? [] : offsets;
}

function mathRanges(text: string): MathRange[] {
  const ranges: MathRange[] = [];
  for (let at = 0; at < text.length; at += 1) {
    if (escaped(text, at)) continue;
    let opening = "";
    let closing = "";
    let display = false;
    if (text.startsWith("$$", at)) { opening = "$$"; closing = "$$"; display = true; }
    else if (text.startsWith("\\[", at)) { opening = "\\["; closing = "\\]"; display = true; }
    else if (text.startsWith("\\(", at)) { opening = "\\("; closing = "\\)"; }
    else if (text[at] === "$") { opening = "$"; closing = "$"; }
    if (!opening) {
      const environment = text.slice(at).match(/^\\begin\{([^{}]+)\}/);
      if (!environment) continue;
      const stack: string[] = [];
      let environmentEnd = text.length;
      let environmentClosed = false;
      for (const token of text.slice(at).matchAll(/\\(begin|end)\{([^{}]+)\}/g)) {
        if (token[1] === "begin") stack.push(token[2]);
        else if (stack.pop() !== token[2]) break;
        if (!stack.length) {
          environmentEnd = at + (token.index ?? 0) + token[0].length;
          environmentClosed = true;
          break;
        }
      }
      const wrapper = environmentClosed ? outerWrapper(text, at, environmentEnd) : null;
      const contentStart = wrapper?.start ?? at;
      const contentEnd = wrapper?.end ?? environmentEnd;
      ranges.push({ start: at, end: environmentEnd, bodyStart: at, bodyEnd: environmentEnd, display: true, closed: environmentClosed,
        wrapper: wrapper?.name ?? null, wrapperPrefix: wrapper ? text.slice(at, contentStart).trim() : "",
        contentStart, contentEnd, rowOffsets: environmentClosed && wrapper ? rowOffsets(text, contentStart, contentEnd, false) : [] });
      at = environmentEnd - 1;
      continue;
    }
    const bodyStart = at + opening.length;
    const closingAt = closingDelimiter(text, closing, bodyStart);
    const closed = closingAt >= 0;
    const bodyEnd = closed ? closingAt : text.length;
    const end = closed ? closingAt + closing.length : text.length;
    const wrapper = closed && display ? outerWrapper(text, bodyStart, bodyEnd) : null;
    const contentStart = wrapper?.start ?? bodyStart;
    const contentEnd = wrapper?.end ?? bodyEnd;
    ranges.push({ start: at, end, bodyStart, bodyEnd, display, closed, wrapper: wrapper?.name ?? null,
      wrapperPrefix: wrapper ? text.slice(bodyStart, contentStart).trim() : "", contentStart, contentEnd,
      rowOffsets: closed && display ? rowOffsets(text, contentStart, contentEnd, !wrapper) : [] });
    at = end - 1;
  }
  return ranges;
}

function isCodePointBoundary(text: string, offset: number): boolean {
  const before = text.charCodeAt(offset - 1);
  const after = text.charCodeAt(offset);
  return !(before >= 0xd800 && before <= 0xdbff && after >= 0xdc00 && after <= 0xdfff);
}

function sourceOffsetSafe(text: string, offset: number, ranges: MathRange[]): boolean {
  if (offset <= 0 || offset >= text.length || !isCodePointBoundary(text, offset)) return false;
  const math = ranges.find((range) => offset > range.start && offset < range.end);
  return !math || math.rowOffsets.includes(offset);
}

function suggestedOffsets(text: string, ranges: MathRange[]): number[] {
  const offsets = new Set<number>();
  for (const range of ranges) {
    if (range.display) {
      offsets.add(range.start);
      offsets.add(range.end);
      for (const offset of range.rowOffsets) offsets.add(offset);
    }
  }
  for (const match of text.matchAll(/\n|[。；;！？!?]|(?:[（(]\d+[）)]|[①-⑳])/g)) {
    const index = match.index ?? 0;
    const offset = /^[（(①-⑳]/.test(match[0]) ? index : index + match[0].length;
    if (sourceOffsetSafe(text, offset, ranges)) offsets.add(offset);
  }
  return [...offsets].filter((offset) => sourceOffsetSafe(text, offset, ranges)).sort((left, right) => left - right);
}

function meaningful(text: string): boolean {
  return Boolean(text.replace(/\$\$|\\\[|\\\]|\\(?:begin|end)\{(?:aligned|align\*?|alignedat|gathered|gather\*?|eqnarray\*?)\}|\\\\\*?(?:\[[^\]\n]*\])?/g, "").trim());
}

function automaticOffsets(text: string, ranges: MathRange[]): number[] {
  const candidates = suggestedOffsets(text, ranges);
  const offsets: number[] = [];
  let previous = 0;
  for (const offset of candidates) {
    if (!meaningful(text.slice(previous, offset))) continue;
    offsets.push(offset);
    previous = offset;
  }
  // 最后一行后的换行及公式闭合符并入上一行，避免产生只有外壳的空单元。
  if (offsets.length && !meaningful(text.slice(previous))) offsets.pop();
  return offsets;
}

function renderSourceSpan(span: RubricSolutionSpan, source: RubricSolutionSource): string {
  const text = source.content;
  let result = "";
  let at = span.start;
  for (const range of mathRanges(text)) {
    if (range.end <= at || range.start >= span.end) continue;
    const begin = Math.max(at, range.start);
    const end = Math.min(span.end, range.end);
    result += text.slice(at, begin);
    if (begin === range.start && end === range.end) result += text.slice(begin, end);
    else if (range.display && range.closed) {
      const innerStart = Math.max(begin, range.contentStart);
      const innerEnd = Math.min(end, range.contentEnd);
      const body = text.slice(innerStart, innerEnd).trim().replace(/^(?:\\\\\*?(?:\[[^\]\n]*\])?\s*)+|(?:\\\\\*?(?:\[[^\]\n]*\])?\s*)+$/g, "").trim();
      // 只有显示层补外壳；span 始终指向未改写原文，服务端可以据此重建证据。
      if (body) result += range.wrapper ? `$$\n${range.wrapperPrefix}\n${body}\n\\end{${range.wrapper}}\n$$` : `$$\n${body}\n$$`;
    } else result += text.slice(begin, end);
    at = end;
  }
  return (result + text.slice(at, span.end)).trim();
}

/** 将连续原文范围渲染成可独立预览的解答；不修改保存的范围及原始公式。 */
export function renderSolutionSpans(spans: RubricSolutionSpan[], sources: RubricSolutionSource[]): string {
  const merged: RubricSolutionSpan[] = [];
  for (const span of spans) {
    const previous = merged[merged.length - 1];
    if (previous && previous.source_id === span.source_id && previous.end === span.start) previous.end = span.end;
    else merged.push({ ...span });
  }
  return merged.map((span) => {
    const source = sources.find((item) => item.id === span.source_id);
    return source ? renderSourceSpan(span, source) : "";
  }).filter(Boolean).join("\n");
}

export function rubricSolutionLines(question: Question | null | undefined): RubricSolutionLine[] {
  const sources = rubricSolutionSources(question);
  const lines: RubricSolutionLine[] = [];
  for (const source of sources) {
    const offsets = [0, ...automaticOffsets(source.content, mathRanges(source.content)), source.content.length];
    for (let index = 1; index < offsets.length; index += 1) {
      const start = offsets[index - 1];
      const end = offsets[index];
      const spans = [{ source_id: source.id, start, end }];
      lines.push({ id: `${source.id}:${start}-${end}`, position: lines.length + 1, content: renderSolutionSpans(spans, sources), spans });
    }
  }
  return lines;
}

function lineSources(line: RubricSolutionLine, sources: RubricSolutionSource[]): { source: RubricSolutionSource; span: RubricSolutionSpan; offset: number }[] {
  let offset = 0;
  const result: { source: RubricSolutionSource; span: RubricSolutionSpan; offset: number }[] = [];
  for (const span of line.spans ?? []) {
    const source = sources.find((item) => item.id === span.source_id);
    if (source) result.push({ source, span, offset });
    offset += span.end - span.start;
  }
  return result;
}

/** offset 相对于 line.spans 原文顺序拼接后的字符串，而非补了公式外壳的显示内容。 */
export function isSolutionSplitOffsetSafe(line: RubricSolutionLine, sources: RubricSolutionSource[], offset: number): boolean {
  const parts = lineSources(line, sources);
  const length = parts.reduce((sum, part) => sum + part.span.end - part.span.start, 0);
  if (!Number.isInteger(offset) || offset <= 0 || offset >= length) return false;
  for (const { source, span, offset: base } of parts) {
    const size = span.end - span.start;
    if (offset === base || offset === base + size) return true;
    if (offset > base && offset < base + size) return sourceOffsetSafe(source.content, span.start + offset - base, mathRanges(source.content));
  }
  return false;
}

export function solutionSplitOffsets(line: RubricSolutionLine, sources: RubricSolutionSource[]): { offset: number; label: string }[] {
  const offsets = new Set<number>();
  for (const { source, span, offset: base } of lineSources(line, sources)) {
    if (base) offsets.add(base);
    for (const offset of suggestedOffsets(source.content, mathRanges(source.content))) {
      if (offset > span.start && offset < span.end) offsets.add(base + offset - span.start);
    }
  }
  return [...offsets].sort((left, right) => left - right).map((offset) => ({ offset, label: `原文第 ${offset} 个字符后` }));
}
