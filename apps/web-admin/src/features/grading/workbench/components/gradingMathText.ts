import { fromMarkdown } from "mdast-util-from-markdown";
import type { Root, RootContent } from "mdast";
import katex from "katex";

type Range = { start: number; end: number };
const mathFunctions = new Set(["sin", "cos", "tan", "cot", "sec", "csc", "log", "ln", "exp", "max", "min", "lim", "sup", "inf", "det", "gcd"]);

function protectedRanges(value: string): Range[] {
  const ranges: Range[] = [];
  const visit = (node: Root | RootContent) => {
    // 原始识别结果可能混有 Markdown；代码示例、链接地址和 HTML 不能被当成学生公式改写。
    if (["code", "inlineCode", "html", "link", "linkReference", "image", "imageReference", "definition"].includes(node.type)) {
      const start = node.position?.start.offset, end = node.position?.end.offset;
      if (start !== undefined && end !== undefined) ranges.push({ start, end });
    } else if ("children" in node) node.children.forEach(visit);
  };
  visit(fromMarkdown(value));
  return ranges;
}

function explicitFormulaEnd(value: string, start: number) {
  const opening = value.startsWith("$$", start) ? "$$" : value[start] === "$" ? "$"
    : value.startsWith(String.raw`\(`, start) ? String.raw`\(` : value.startsWith(String.raw`\[`, start) ? String.raw`\[` : "";
  if (!opening) return;
  const closing = opening === String.raw`\(` ? String.raw`\)` : opening === String.raw`\[` ? String.raw`\]` : opening;
  let end = value.indexOf(closing, start + opening.length);
  while (end >= 0 && value[end - 1] === "\\") end = value.indexOf(closing, end + closing.length);
  // 未闭合的显式公式保留原文，不把里面的局部表达式重新套一层分隔符。
  return end < 0 ? value.length : end + closing.length;
}

function bareFormulaEnd(value: string, start: number) {
  let cursor = start, braces = 0;
  while (cursor < value.length) {
    const character = value[cursor];
    if (value.startsWith("**", cursor)) break;
    if (character === "\\") {
      const command = /^\\(?:[A-Za-z]+|[,;!{}|%_])/u.exec(value.slice(cursor))?.[0];
      if (!command) break;
      cursor += command.length;
    } else if (/[A-Za-z]/u.test(character)) {
      const word = /^[A-Za-z]+/u.exec(value.slice(cursor))![0];
      if (word.length > 3 && !mathFunctions.has(word)) break;
      cursor += word.length;
    } else if (character === "{") { braces += 1; cursor += 1; }
    else if (character === "}") {
      if (!braces) break;
      braces -= 1; cursor += 1;
    } else if (/[0-9_^[\]()+\-*/=<>|.,;:!% \tα-ωΑ-Ω∞∈∉≤≥≠≈∪∩∅∑∏√·×÷±…]/u.test(character)) cursor += 1;
    else if (braces > 0 && character !== "\n" && character !== "$" && character !== "`") cursor += 1;
    else break;
  }
  return cursor;
}

function canRenderFormula(value: string) {
  // 只补足展示分隔符，不推导、纠正或重写答案。普通型号、日期和英文说明没有明确数学特征时仍按原文显示。
  const hasMath = /\\[A-Za-z]+|[_^](?:\{|[A-Za-z0-9])|[=<>≤≥≠≈∈∉]|\d\s*\/\s*\d|[\[(]\s*[+-]?\d[^\n]*[,;][^\n]*[\])]/u.test(value);
  if (!hasMath) return false;
  try {
    katex.renderToString(value, { throwOnError: true, trust: false, strict: "ignore", maxExpand: 1000, maxSize: 10 });
    return true;
  } catch { return false; }
}

/** 兼容历史模型返回的裸 LaTeX；仅生成展示文本，不修改识别稿、评分记录或教师填写的内容。 */
export function normalizeGradingMathText(value: string) {
  const protectedSource = protectedRanges(value);
  let output = "", cursor = 0;
  while (cursor < value.length) {
    const protectedRange = protectedSource.find(range => cursor >= range.start && cursor < range.end);
    if (protectedRange) { output += value.slice(cursor, protectedRange.end); cursor = protectedRange.end; continue; }
    const explicitEnd = explicitFormulaEnd(value, cursor);
    if (explicitEnd !== undefined) { output += value.slice(cursor, explicitEnd); cursor = explicitEnd; continue; }
    if (/[A-Za-z0-9\\([α-ωΑ-Ω]/u.test(value[cursor])) {
      const limit = protectedSource.find(range => range.start > cursor)?.start ?? value.length;
      const end = Math.min(bareFormulaEnd(value, cursor), limit);
      const candidate = value.slice(cursor, end).trimEnd().replace(/[.,;:]+$/u, "");
      if (candidate && canRenderFormula(candidate)) {
        output += `$${candidate}$`;
        cursor += candidate.length;
        continue;
      }
      // 整段无法解析时原样保留，不能只美化其中一半而掩盖识别稿里的异常符号。
      if (end > cursor) { output += value.slice(cursor, end); cursor = end; continue; }
    }
    output += value[cursor++];
  }
  return output;
}
