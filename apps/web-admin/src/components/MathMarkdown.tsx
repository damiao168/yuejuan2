import { memo, useId } from "react";
import type { Root, RootContent } from "hast";
import ReactMarkdown from "react-markdown";
import rehypeHighlight from "rehype-highlight";
import rehypeKatex from "rehype-katex";
import remarkGfm from "remark-gfm";
import remarkMath from "remark-math";
import "katex/dist/katex.min.css";
import { MarkdownFootnoteContext, mathMarkdownComponents } from "./mathMarkdownRenderers";
import { mathTablePipeToken, normalizeMathMarkdown, type MathMarkdownOptions } from "./mathMarkdownNormalization";
import "./math-markdown.css";

export { normalizeMathMarkdown, requiresDisplayMath } from "./mathMarkdownNormalization";
export type { MathMarkdownOptions } from "./mathMarkdownNormalization";

function rehypeRestoreMathPipes() {
  return (tree: Root) => {
    const visit = (node: Root | RootContent, insideMath = false) => {
      const classes = node.type === "element" ? node.properties.className : undefined;
      const math = insideMath || (Array.isArray(classes) && classes.some((name) => name === "language-math" || name === "math-inline" || name === "math-display"));
      if (math && node.type === "text") node.value = node.value.split(mathTablePipeToken).join("|");
      if ("children" in node) node.children.forEach((child) => visit(child, math));
    };
    visit(tree);
  };
}

export const MathMarkdown = memo(function MathMarkdown({ children, className = "", promoteInlineMath = true }: Readonly<{ children: string; className?: string } & MathMarkdownOptions>) {
  const footnotePrefix = `markdown-${useId().replace(/:/g, "")}-`;
  if (!children.trim()) return null;

  return (
    <MarkdownFootnoteContext value={footnotePrefix}>
    <div className={`math-markdown ${className}`.trim()}>
      <ReactMarkdown
        skipHtml
        remarkRehypeOptions={{ clobberPrefix: footnotePrefix, footnoteLabel: "注释", footnoteBackLabel: "返回引用" }}
        remarkPlugins={[[remarkGfm, { singleTilde: false }], [remarkMath, { singleDollarTextMath: true }]]}
        rehypePlugins={[rehypeRestoreMathPipes, [rehypeKatex, {
          strict: "warn",
          throwOnError: false,
          trust: false,
          output: "htmlAndMathml",
          maxExpand: 1000,
          maxSize: 10,
          errorColor: "#b42318"
        }], [rehypeHighlight, { detect: false, plainText: ["text", "txt", "plain", "plaintext", "mermaid"] }]]}
        components={mathMarkdownComponents}
      >
        {normalizeMathMarkdown(children, { promoteInlineMath })}
      </ReactMarkdown>
    </div>
    </MarkdownFootnoteContext>
  );
});
