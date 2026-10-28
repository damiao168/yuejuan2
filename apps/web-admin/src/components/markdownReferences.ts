import type { Element, Root, RootContent } from "hast";

function plainText(node: RootContent): string {
  if (node.type === "text") return node.value;
  return "children" in node ? node.children.map(plainText).join("") : "";
}

// 在同一份 Markdown 中收集网页引用；只生成本地标记，模型地址始终不会成为可导航链接。
export function rehypeCollectReferences(options: { prefix: string }) {
  return (tree: Root) => {
    const references = new Map<string, { index: number; label: string }>();
    const visit = (node: Root | RootContent) => {
      if (node.type === "element" && node.tagName === "a" && typeof node.properties.href === "string") {
        try {
          const url = new URL(node.properties.href);
          if (["https:", "http:"].includes(url.protocol)) {
            const known = references.get(url.href);
            const reference = known ?? { index: references.size + 1, label: plainText(node) || url.hostname };
            references.set(url.href, reference);
            node.properties.dataReferenceTarget = `${options.prefix}reference-${reference.index}`;
            node.properties.dataReferenceIndex = reference.index;
          }
        } catch { /* 相对地址、业务路径和非法地址仅保留正文，不加入网页资料。 */ }
      }
      if ("children" in node) node.children.forEach(visit);
    };
    visit(tree);
    if (!references.size) return;
    const entries: Element[] = [...references].map(([address, reference]) => ({
      type: "element", tagName: "li", properties: { id: `${options.prefix}reference-${reference.index}` },
      children: [{ type: "element", tagName: "div", properties: { dataReferenceAddress: address, dataReferenceLabel: reference.label }, children: [] }]
    }));
    tree.children.push({ type: "element", tagName: "div", properties: { dataReferenceCount: references.size }, children: [{ type: "element", tagName: "ol", properties: {}, children: entries }] });
  };
}
