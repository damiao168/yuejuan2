import { fromMarkdown } from "mdast-util-from-markdown";
import { gfmFromMarkdown } from "mdast-util-gfm";
import { gfm } from "micromark-extension-gfm";
import type { Root, RootContent } from "mdast";

export interface MathMarkdownOptions { promoteInlineMath?: boolean; streaming?: boolean }

// GFM 先分列，数学插件后解析。暂存单元公式的竖线，解析完表格再还原，保留绝对值和 array 列定义的原意。
export const mathTablePipeToken = "\uE000markdown-math-pipe\uE001";
type SourceRange = { start: number; end: number };

function isEscaped(value: string, index: number) {
  let count = 0;
  for (let cursor = index - 1; cursor >= 0 && value[cursor] === "\\"; cursor -= 1) count += 1;
  return count % 2 === 1;
}

function readFormula(value: string, cursor: number) {
  const latex = value.startsWith(String.raw`\(`, cursor) || value.startsWith(String.raw`\[`, cursor);
  if ((!latex && value[cursor] !== "$") || isEscaped(value, cursor)) return;
  const block = latex ? value[cursor + 1] === "[" : value.startsWith("$$", cursor);
  const opening = latex || block ? 2 : 1;
  let marker = block ? "$$" : "$";
  if (latex) marker = block ? String.raw`\]` : String.raw`\)`;
  let closing = value.indexOf(marker, cursor + opening);
  while (closing >= 0 && (isEscaped(value, closing) || (!block && !latex && value[closing + 1] === "$"))) closing = value.indexOf(marker, closing + marker.length);
  if (closing < 0 || (!latex && !block && value.slice(cursor, closing).includes("\n"))) return;
  return { block, opening, closing, end: closing + marker.length };
}

function mathPipeParseView(value: string, protectedRanges: SourceRange[]) {
  const characters = value.split("");
  let cursor = 0;
  while (cursor < value.length) {
    const protectedRange = protectedRanges.find((range) => cursor >= range.start && cursor < range.end);
    if (protectedRange) { cursor = protectedRange.end; continue; }
    const formula = readFormula(value, cursor);
    if (!formula) { cursor += 1; continue; }
    if (!protectedRanges.some((range) => range.start < formula.end && range.end > cursor)) {
      for (let index = cursor + formula.opening; index < formula.closing; index += 1) if (characters[index] === "|") characters[index] = "∣";
    }
    cursor = formula.end;
  }
  return characters.join("");
}

type MarkdownRanges = {
  protectedRanges: SourceRange[];
  structuredRanges: SourceRange[];
  inlineRanges: SourceRange[];
  tableRanges: SourceRange[];
};

function protectLinkTarget(node: Root | RootContent, value: string, range: SourceRange, ranges: SourceRange[]) {
  if (node.type !== "link" && node.type !== "linkReference") return;
  const labelEnd = node.children[node.children.length - 1]?.position?.end.offset ?? range.start + 1;
  if (node.type === "link") {
    ranges.push(value[range.start] === "[" ? { start: labelEnd, end: range.end } : range);
    return;
  }
  ranges.push(node.referenceType === "full" ? { start: labelEnd, end: range.end } : range);
}

function recordNodeRange(node: Root | RootContent, value: string, ranges: MarkdownRanges) {
  const start = node.position?.start.offset;
  const end = node.position?.end.offset;
  if (start === undefined || end === undefined) return true;
  const range = { start, end };
  // CommonMark 会把脚注定义当成普通链接定义；GFM 脚注正文仍需经过公式兼容处理。
  if (["code", "inlineCode", "html", "image", "imageReference"].includes(node.type) || (node.type === "definition" && !value.slice(start, end).startsWith("[^"))) {
    ranges.protectedRanges.push(range);
    return false;
  }
  if (["table", "listItem", "blockquote", "footnoteDefinition", "heading", "link", "linkReference", "emphasis", "strong", "delete"].includes(node.type)) ranges.structuredRanges.push(range);
  if (["heading", "link", "linkReference", "emphasis", "strong", "delete"].includes(node.type)) ranges.inlineRanges.push(range);
  if (node.type === "table") ranges.tableRanges.push(range);
  protectLinkTarget(node, value, range, ranges.protectedRanges);
  return true;
}

function protectUnclosedCode(value: string, protectedRanges: SourceRange[]) {
  let cursor = value.indexOf("`");
  while (cursor >= 0) {
    const protectedRange = protectedRanges.find((range) => cursor >= range.start && cursor < range.end);
    if (protectedRange) {
      cursor = value.indexOf("`", protectedRange.end);
      continue;
    }
    if (!isEscaped(value, cursor)) {
      // 未配对反引号属于尚未输出完的代码；闭合后由 CommonMark 接管，流式中间帧不先把示例改成公式。
      protectedRanges.push({ start: cursor, end: value.length });
      break;
    }
    cursor = value.indexOf("`", cursor + 1);
  }
}

function collectRanges(value: string) {
  const ranges: MarkdownRanges = { protectedRanges: [], structuredRanges: [], inlineRanges: [], tableRanges: [] };
  const visit = (node: Root | RootContent) => {
    if (!recordNodeRange(node, value, ranges)) return;
    if ("children" in node) node.children.forEach(visit);
  };
  // 使用语法树的源位置保护缩进代码、嵌套/未闭合围栏和链接目标，不靠正则猜代码边界。
  visit(fromMarkdown(value));
  protectUnclosedCode(value, ranges.protectedRanges);
  if (value.includes("|")) {
    // 表头公式的竖线也会使 GFM 无法识别整张表；仅在解析视图中等长遮盖，源位置和用户原文不变。
    visit(fromMarkdown(mathPipeParseView(value, ranges.protectedRanges), { extensions: [gfm()], mdastExtensions: [gfmFromMarkdown()] }));
  } else {
    visit(fromMarkdown(value, { extensions: [gfm()], mdastExtensions: [gfmFromMarkdown()] }));
  }
  ranges.protectedRanges.sort((a, b) => a.start - b.start);
  return ranges;
}

function fractionArgument(formula: string, start: number) {
  let cursor = start;
  while (/\s/.test(formula[cursor] ?? "")) cursor += 1;
  if (formula[cursor] !== "{") return;
  const contentStart = ++cursor;
  let depth = 1;
  while (cursor < formula.length && depth > 0) {
    if (formula[cursor] === "{") depth += 1;
    if (formula[cursor] === "}") depth -= 1;
    cursor += 1;
  }
  return { end: cursor, content: formula.slice(contentStart, cursor - 1) };
}

function hasNestedFraction(formula: string) {
  for (const match of formula.matchAll(/\\[dt]?frac\b/g)) {
    let cursor = (match.index ?? 0) + match[0].length;
    for (let argument = 0; argument < 2; argument += 1) {
      const parsed = fractionArgument(formula, cursor);
      if (!parsed) break;
      cursor = parsed.end;
      if (/\\[dt]?frac\b/.test(parsed.content)) return true;
    }
  }
  return false;
}

const displayEnvironments = new Set(["aligned", "alignedat", "array", "bmatrix", "cases", "gathered", "matrix", "pmatrix", "smallmatrix", "split", "vmatrix", "Vmatrix"]);
const tallMathCommands = new Set(["cfrac", "dfrac", "tfrac", "frac", "dbinom", "tbinom", "binom", "sqrt", "sum", "prod", "coprod", "int", "iint", "iiint", "oint", "lim", "substack", "stackrel", "overset", "underset", "overbrace", "underbrace"]);

function hasDisplayEnvironment(formula: string) {
  for (const match of formula.matchAll(/\\begin\{([A-Za-z]+)\}/g)) {
    if (displayEnvironments.has(match[1])) return true;
  }
  return false;
}

function tallCommandCount(formula: string) {
  let count = 0;
  for (const match of formula.matchAll(/\\([A-Za-z]+)\b/g)) {
    if (tallMathCommands.has(match[1])) count += 1;
  }
  return count;
}

/** 识别稿中的长推导可独立成行；列表、引用和表格不能因提升公式而拆开所属块。 */
export function requiresDisplayMath(formula: string) {
  const source = formula.trim();
  if (!source) return false;
  if (hasDisplayEnvironment(source) || hasNestedFraction(source)) return true;
  const count = (pattern: RegExp) => Array.from(source.matchAll(pattern)).length;
  const tallCommands = tallCommandCount(source);
  const relations = count(/(?:[=<>]|\\(?:approx|cong|equiv|ge|geq|gt|le|leq|lt|ne|neq|sim)\b)/g);
  const scripts = count(/[_^]\s*(?:\{|[A-Za-z0-9+-])/g);
  return source.length >= 140 || (tallCommands >= 1 && source.length >= 52)
    || (tallCommands >= 2 && source.length >= 42) || (source.length >= 84 && (relations >= 2 || scripts >= 4));
}

function containerPrefix(value: string) {
  let cursor = 0;
  let column = 0;
  let continuation = "";
  let hasQuote = false;
  // CommonMark 的 tab 按四列制表位展开；列表续行缩进使用显示列宽，而不是字符数。
  const columnAfter = (start: number, text: string) => Array.from(text).reduce((current, character) => character === "\t" ? current + 4 - current % 4 : current + 1, start);
  while (cursor < value.length) {
    const whitespace = /^[ \t]*/.exec(value.slice(cursor))?.[0] ?? "";
    continuation += whitespace;
    cursor += whitespace.length;
    column = columnAfter(column, whitespace);
    const quote = /^>[ \t]?/.exec(value.slice(cursor))?.[0];
    if (quote) { continuation += quote; cursor += quote.length; column = columnAfter(column, quote); hasQuote = true; continue; }
    const footnote = /^\[\^[^\]\n]+\]:[ \t]*/.exec(value.slice(cursor))?.[0];
    if (footnote) { continuation += "    "; cursor += footnote.length; column += 4; continue; }
    const marker = /^(?:[-+*]|\d+[.)])[ \t]+/.exec(value.slice(cursor))?.[0];
    if (marker) {
      const nextColumn = columnAfter(column, marker);
      continuation += " ".repeat(nextColumn - column);
      column = nextColumn;
      cursor += marker.length;
      continue;
    }
    break;
  }
  return { continuation, hasQuote, end: cursor };
}

function displayFormula(value: string, index: number, formula: string) {
  const before = value.slice(value.lastIndexOf("\n", index - 1) + 1, index);
  const { continuation, hasQuote, end } = containerPrefix(before);
  // 引用和列表可以交替嵌套；每个列表标记替换为续行缩进，引用标记按原顺序保留。
  const body = formula.split("\n").map((line, lineIndex) => {
    if (hasQuote && lineIndex > 0) {
      if (line.startsWith(continuation)) line = line.slice(continuation.length);
      else {
        const prefix = containerPrefix(line);
        if (prefix.hasQuote) line = line.slice(prefix.end);
      }
    }
    return line.trim();
  }).join("\n").trim();
  const lines = body.split("\n").join(`\n${continuation}`);
  const math = `$$\n${continuation}${lines}\n${continuation}$$`;
  if (before.length === end) return `${math}\n${continuation}\n${continuation}`;
  return `\n${continuation}\n${continuation}${math}\n${continuation}\n${continuation}`;
}

export function normalizeMathMarkdown(value: string, { promoteInlineMath = true, streaming = false }: MathMarkdownOptions = {}) {
  const { protectedRanges, structuredRanges, inlineRanges, tableRanges } = collectRanges(value);
  const within = (ranges: SourceRange[], index: number) => ranges.some((range) => index >= range.start && index < range.end);
  const normalizeText = (text: string, offset: number) => {
    let output = "";
    let cursor = 0;
    while (cursor < text.length) {
      const delimited = readFormula(text, cursor);
      // remark-math 会在文末隐式结束 $$ 块；流式半帧先按原文展示，避免未闭合花括号触发公式错误。
      if (!delimited && streaming && text.startsWith("$$", cursor) && !isEscaped(text, cursor)) {
        output += "\\$\\$";
        cursor += 2;
        continue;
      }
      if (!delimited) { output += text[cursor++]; continue; }
      const { block, opening, closing } = delimited;
      let formula = text.slice(cursor + opening, closing);
      const absolute = offset + cursor;
      const inTable = within(tableRanges, absolute);
      if (inTable) formula = formula.split("|").join(mathTablePipeToken);
      const promote = !block && promoteInlineMath && !within(structuredRanges, absolute) && requiresDisplayMath(formula);
      let end = delimited.end;
      // 标题、强调和链接只能放行内节点；显式块分隔符也不能把这些父节点拆开。
      if ((block || promote) && !inTable && !within(inlineRanges, absolute)) {
        if (promote && /^[，。；：、,.;:]/.test(text[end] ?? "")) formula = String.raw`${formula.trim()}\text{${text[end++]}}`;
        output += displayFormula(value, absolute, formula);
      } else {
        output += `$${formula.trim()}$`;
      }
      cursor = end;
    }
    return output;
  };
  let output = "";
  let cursor = 0;
  for (const range of protectedRanges) {
    if (range.start < cursor) continue;
    output += normalizeText(value.slice(cursor, range.start), cursor) + value.slice(range.start, range.end);
    cursor = range.end;
  }
  return output + normalizeText(value.slice(cursor), cursor);
}
