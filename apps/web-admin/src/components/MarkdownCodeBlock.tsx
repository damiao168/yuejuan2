import { useEffect, useRef, useState, type ComponentPropsWithoutRef } from "react";
import type { ExtraProps } from "react-markdown";

export function MarkdownCodeBlock({ children, node }: ComponentPropsWithoutRef<"pre"> & ExtraProps) {
  const contentRef = useRef<HTMLPreElement>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const [copyStatus, setCopyStatus] = useState("复制代码");
  const code = node?.children.find((child) => child.type === "element" && child.tagName === "code");
  const classes = code?.type === "element" ? code.properties.className : undefined;
  const language = Array.isArray(classes) ? classes.find((name) => typeof name === "string" && name.startsWith("language-"))?.toString().slice(9) : undefined;
  useEffect(() => () => clearTimeout(timer.current), []);

  const copy = async () => {
    try {
      // 从渲染后的代码节点取纯文本，高亮标签和 Markdown 围栏不能混进复制结果。
      await navigator.clipboard.writeText(contentRef.current?.textContent ?? "");
      setCopyStatus("已复制");
    } catch {
      setCopyStatus("复制失败");
    }
    clearTimeout(timer.current);
    timer.current = setTimeout(() => setCopyStatus("复制代码"), 1600);
  };

  return <div className="math-markdown-code-block">
    <div className="math-markdown-code-header"><span>{language || "代码"}</span><button type="button" onClick={() => void copy()}>{copyStatus}</button></div>
    <pre ref={contentRef}>{children}</pre>
  </div>;
}
