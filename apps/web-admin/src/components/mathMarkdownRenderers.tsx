import { createContext, useContext, useId, useRef, useState, type ComponentProps, type KeyboardEvent } from "react";
import { ArrowLeft, ArrowRight } from "lucide-react";
import type { Components, ExtraProps } from "react-markdown";
import { MarkdownCodeBlock } from "./MarkdownCodeBlock";

// 每条回复沿用自己的脚注前缀；渲染组件的身份保持稳定，不随流式文本重新定义。
export const MarkdownFootnoteContext = createContext("");

function MarkdownLink({ children, href, node, id }: Readonly<ComponentProps<"a"> & ExtraProps>) {
  const prefix = useContext(MarkdownFootnoteContext);
  const referenceTarget = node?.properties.dataReferenceTarget;
  const footnote = node?.properties.dataFootnoteRef !== undefined || node?.properties.dataFootnoteBackref !== undefined;
  // 脚注只允许跳到当前回复的自动生成锚点；模型文稿不能提供业务入口或触发外部访问。
  if (prefix && footnote && href?.startsWith(`#${prefix}`)) {
    return <a
      href={href}
      id={id}
      data-footnote-ref={node?.properties.dataFootnoteRef}
      data-footnote-backref={node?.properties.dataFootnoteBackref}
      aria-describedby={`${prefix}footnote-label`}
      aria-label={node?.properties.dataFootnoteBackref !== undefined ? "返回引用" : "查看注释"}
      onClick={(event) => {
        // 脚注只在当前回复内定位，不改变业务地址或浏览器历史。
        event.preventDefault();
        const root = event.currentTarget.closest(".math-markdown");
        const target = root?.querySelector(`[id="${CSS.escape(href.slice(1))}"]`);
        if (target instanceof HTMLElement) {
          target.scrollIntoView({ block: "nearest" });
          target.tabIndex = -1;
          target.focus({ preventScroll: true });
        }
      }}
    >{children}</a>;
  }
  return <span className="math-markdown-link">{children}{typeof referenceTarget === "string" && referenceTarget.startsWith(`${prefix}reference-`) ? <sup><button type="button" aria-label={`查看参考资料 ${node?.properties.dataReferenceIndex}`} onClick={event => {
    const root = event.currentTarget.closest(".math-markdown");
    const target = root?.querySelector<HTMLElement>(`[id="${CSS.escape(referenceTarget)}"]`);
    if (!target) return;
    const details = target.closest("details");
    if (details) details.open = true;
    target.tabIndex = -1;
    target.focus({ preventScroll: true });
    target.scrollIntoView({ block: "nearest" });
  }}>[{node?.properties.dataReferenceIndex}]</button></sup> : null}</span>;
}

function MarkdownReferenceBlock({ children, node, ...props }: Readonly<ComponentProps<"div"> & ExtraProps>) {
  const [copyStatus, setCopyStatus] = useState("");
  const count = node?.properties.dataReferenceCount;
  if (typeof count === "number") return <details className="math-markdown-references"><summary>参考资料（{count}）</summary>{children}</details>;
  const address = node?.properties.dataReferenceAddress;
  if (typeof address !== "string") return <div {...props}>{children}</div>;
  return <div>
    <span>{node?.properties.dataReferenceLabel} · {new URL(address).hostname}</span>
    <details><summary>查看完整地址</summary><span className="math-markdown-reference-address">{address}</span>
      <button type="button" className="math-markdown-copy-reference" onClick={async () => {
        try { await navigator.clipboard.writeText(address); setCopyStatus("参考地址已复制"); }
        catch { setCopyStatus("复制失败，请手动复制此地址。"); }
      }}>复制地址</button>
      <output aria-live="polite" aria-atomic="true">{copyStatus}</output>
    </details>
  </div>;
}

function MarkdownImage({ alt }: Readonly<ComponentProps<"img">>) {
  return <span className="math-markdown-image-label">{alt || "图片"}</span>;
}

function MarkdownHeading({ children, id, className }: Readonly<ComponentProps<"h2">>) {
  const prefix = useContext(MarkdownFootnoteContext);
  return <h2 id={id === "footnote-label" ? `${prefix}footnote-label` : id} className={className}>{children}</h2>;
}

function MarkdownTable({ children }: Readonly<ComponentProps<"table">>) {
  const viewportId = useId();
  const viewport = useRef<HTMLDivElement>(null);
  const scrollTable = (direction: -1 | 1) => {
    const element = viewport.current;
    if (element) {
      element.scrollBy({ left: direction * Math.max(80, element.clientWidth * 0.75), behavior: "auto" });
    }
  };
  const scrollWithArrow = (event: KeyboardEvent<HTMLButtonElement>) => {
    if (event.altKey || event.ctrlKey || event.metaKey) return;
    if (event.key !== "ArrowLeft" && event.key !== "ArrowRight") return;
    event.preventDefault();
    scrollTable(event.key === "ArrowLeft" ? -1 : 1);
  };
  // 焦点留在真实按钮上；Enter/Space 使用原生激活，左右键只移动本表视口，不改表格语义。
  return <section className="math-markdown-table-region" aria-label="表格，可横向滚动">
    <div className="math-markdown-table-controls">
      <button type="button" aria-label="向左滚动表格" title="向左滚动表格" aria-controls={viewportId}
        onClick={() => scrollTable(-1)} onKeyDown={scrollWithArrow}><ArrowLeft size={16} aria-hidden="true" /></button>
      <button type="button" aria-label="向右滚动表格" title="向右滚动表格" aria-controls={viewportId}
        onClick={() => scrollTable(1)} onKeyDown={scrollWithArrow}><ArrowRight size={16} aria-hidden="true" /></button>
    </div>
    <div className="math-markdown-table-scroll" id={viewportId} ref={viewport}><table>{children}</table></div>
  </section>;
}

export const mathMarkdownComponents: Components = {
  a: MarkdownLink,
  div: MarkdownReferenceBlock,
  img: MarkdownImage,
  h2: MarkdownHeading,
  pre: MarkdownCodeBlock,
  table: MarkdownTable
};
