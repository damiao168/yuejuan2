import type { ReactNode } from "react";
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

export function normalizeMathMarkdown(value: string) {
  const normalized = value
    .replace(/\\\[([\s\S]*?)\\\]/g, (_match, formula: string) => `\n\n$$\n${formula.trim()}\n$$\n\n`)
    .replace(/\\\(([\s\S]*?)\\\)/g, (_match, formula: string) => `$${formula.trim()}$`);

  // Deeply nested fractions are technically valid inline LaTeX, but browsers
  // compress them into a very small line box. Render only those expressions as
  // display math so every numerator and denominator gets its own vertical room.
  return normalized.replace(/(^|[^$])\$([^$\n]+)\$(?!\$)/g, (match, prefix: string, formula: string) => (
    hasNestedFraction(formula)
      ? `${prefix}\n\n$$\n${formula.trim()}\n$$\n\n`
      : match
  ));
}

export function MathMarkdown({ children, className = "" }: { children: string; className?: string }) {
  if (!children.trim()) return null;

  return (
    <div className={`math-markdown ${className}`.trim()}>
      <ReactMarkdown
        skipHtml
        remarkPlugins={[[remarkMath, { singleDollarTextMath: true }]]}
        rehypePlugins={[[rehypeKatex, { strict: "warn", throwOnError: false, trust: false }]]}
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
}
