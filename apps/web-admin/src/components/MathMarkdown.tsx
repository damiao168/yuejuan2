import { memo, type ReactNode } from "react";
import ReactMarkdown from "react-markdown";
import rehypeKatex from "rehype-katex";
import remarkMath from "remark-math";
import "katex/dist/katex.min.css";

function hasNestedFraction(formula: string) {
  const command = /\\(?:d|t)?frac\b/g;
  for (const match of formula.matchAll(command)) {
    let cursor = (match.index ?? 0) + match[0].length;
    for (let argument = 0; argument < 2; argument += 1) {
      while (/\s/.test(formula[cursor] ?? "")) cursor += 1;
      if (formula[cursor] !== "{") break;
      const contentStart = cursor + 1;
      let depth = 1;
      cursor += 1;
      while (cursor < formula.length && depth > 0) {
        if (formula[cursor] === "{") depth += 1;
        if (formula[cursor] === "}") depth -= 1;
        cursor += 1;
      }
      const content = formula.slice(contentStart, Math.max(contentStart, cursor - 1));
      if (/\\(?:d|t)?frac\b/.test(content)) return true;
    }
  }
  return false;
}

const displayEnvironmentPattern = /\\begin\{(?:aligned|alignedat|array|bmatrix|cases|gathered|matrix|pmatrix|smallmatrix|split|vmatrix|Vmatrix)\}/;
const tallMathCommandPattern = /\\(?:cfrac|dfrac|tfrac|frac|dbinom|tbinom|binom|sqrt|sum|prod|coprod|int|iint|iiint|oint|lim|substack|stackrel|overset|underset|overbrace|underbrace)\b/g;
const relationPattern = /(?:=|<|>|\\(?:approx|cong|equiv|ge|geq|gt|le|leq|lt|ne|neq|sim)\b)/g;
const scriptPattern = /[_^]\s*(?:\{|[A-Za-z0-9+-])/g;
const trailingPunctuationPattern = /^[，。；：、,.;:]/;

function matchCount(value: string, pattern: RegExp) {
  return Array.from(value.matchAll(pattern)).length;
}

/**
 * TeX and Office keep short formulas inline, but give structurally tall or
 * long derivations a display line of their own. Recognition output commonly
 * uses `$...$` for both, so apply that missing document-layout decision here.
 */
export function requiresDisplayMath(formula: string) {
  const source = formula.trim();
  if (!source) return false;
  if (displayEnvironmentPattern.test(source) || hasNestedFraction(source)) return true;

  const tallCommands = matchCount(source, tallMathCommandPattern);
  const relations = matchCount(source, relationPattern);
  const scripts = matchCount(source, scriptPattern);

  return source.length >= 140
    || (tallCommands >= 1 && source.length >= 52)
    || (tallCommands >= 2 && source.length >= 42)
    || (source.length >= 84 && (relations >= 2 || scripts >= 4));
}

function isEscaped(value: string, index: number) {
  let backslashes = 0;
  for (let cursor = index - 1; cursor >= 0 && value[cursor] === "\\"; cursor -= 1) backslashes += 1;
  return backslashes % 2 === 1;
}

function findClosingDollar(value: string, start: number) {
  for (let cursor = start + 1; cursor < value.length; cursor += 1) {
    if (value[cursor] === "\n") return -1;
    if (value[cursor] === "$" && value[cursor + 1] !== "$" && !isEscaped(value, cursor)) return cursor;
  }
  return -1;
}

function promoteComplexInlineMath(value: string) {
  let output = "";
  let cursor = 0;

  while (cursor < value.length) {
    if (value.startsWith("$$", cursor) && !isEscaped(value, cursor)) {
      const closing = value.indexOf("$$", cursor + 2);
      if (closing < 0) return output + value.slice(cursor);
      output += value.slice(cursor, closing + 2);
      cursor = closing + 2;
      continue;
    }

    if (value[cursor] !== "$" || isEscaped(value, cursor)) {
      output += value[cursor];
      cursor += 1;
      continue;
    }

    const closing = findClosingDollar(value, cursor);
    if (closing < 0) return output + value.slice(cursor);
    const formula = value.slice(cursor + 1, closing);
    if (!requiresDisplayMath(formula)) {
      output += value.slice(cursor, closing + 1);
      cursor = closing + 1;
      continue;
    }

    const punctuation = value.slice(closing + 1).match(trailingPunctuationPattern)?.[0] ?? "";
    const displayFormula = punctuation ? `${formula.trim()}\\text{${punctuation}}` : formula.trim();
    output += `\n\n$$\n${displayFormula}\n$$\n\n`;
    cursor = closing + 1 + punctuation.length;
  }

  return output;
}

function protectMarkdownCode(value: string) {
  const segments: string[] = [];
  const content = value.replace(/```[\s\S]*?```|~~~[\s\S]*?~~~|`[^`\n]*`/g, (segment) => {
    const token = `\uE000MATH_CODE_${segments.length}\uE001`;
    segments.push(segment);
    return token;
  });
  return { content, segments };
}

export function normalizeMathMarkdown(value: string) {
  const protectedCode = protectMarkdownCode(value);
  const normalized = protectedCode.content
    .replace(/\\\[([\s\S]*?)\\\]/g, (_match, formula: string) => `\n\n$$\n${formula.trim()}\n$$\n\n`)
    .replace(/\\\(([\s\S]*?)\\\)/g, (_match, formula: string) => `$${formula.trim()}$`);

  return promoteComplexInlineMath(normalized)
    .replace(/\uE000MATH_CODE_(\d+)\uE001/g, (_match, index: string) => protectedCode.segments[Number(index)] ?? "");
}

export const MathMarkdown = memo(function MathMarkdown({ children, className = "" }: { children: string; className?: string }) {
  if (!children.trim()) return null;

  return (
    <div className={`math-markdown ${className}`.trim()}>
      <ReactMarkdown
        skipHtml
        remarkPlugins={[[remarkMath, { singleDollarTextMath: true }]]}
        rehypePlugins={[[rehypeKatex, {
          strict: "warn",
          throwOnError: false,
          trust: false,
          output: "htmlAndMathml",
          maxExpand: 1000,
          maxSize: 10,
          errorColor: "#b42318"
        }]]}
        components={{
          // Recognition output is untrusted document content. Keep labels but
          // never turn model-produced links or images into active navigation.
          a: ({ children: label }: { children?: ReactNode }) => <span>{label}</span>,
          img: () => null
        }}
      >
        {normalizeMathMarkdown(children)}
      </ReactMarkdown>
    </div>
  );
});
