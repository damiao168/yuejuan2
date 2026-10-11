// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ScrollableTableRegion } from "./responsiveTableInteraction";

describe("scrolling targets and tie priority", () => {
  let host: HTMLDivElement;
  let root: Root;
  beforeEach(() => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
  });
  afterEach(async () => {
    await act(async () => root.unmount());
    host.remove();
  });

  it.each([
    { name: "no table descendants", region: [100, 100], target: "region", distance: 80 },
    { name: "equal overflow keeps region", region: [300, 100], content: [400, 200], target: "region", distance: 80 },
    { name: "body has greatest overflow", region: [100, 100], content: [400, 200], body: [900, 300], target: "body", distance: 225 },
    { name: "content has greatest overflow", region: [100, 100], content: [1000, 200], body: [900, 400], target: "content", distance: 150 },
    { name: "equal descendants keep first", region: [100, 100], content: [500, 200], body: [600, 300], target: "content", distance: 150 },
  ])("preserves target and movement for $name", async (tc) => {
    // 选择溢出量最大的实际容器；相等时保留前者，避免 scrollBy 落到错误的 AntD 层。
    await act(async () => root.render(
      <ScrollableTableRegion>
        {tc.content ? <div className="ant-table-content" /> : null}
        {tc.body ? <div className="ant-table-body" /> : null}
      </ScrollableTableRegion>,
    ));
    const calls: Record<string, ReturnType<typeof vi.fn>> = {};
    for (const name of ["region", "content", "body"] as const) {
      const size = tc[name];
      if (!size) continue;
      const element = host.querySelector(name === "region" ? "section" : `.ant-table-${name}`)!;
      calls[name] = vi.fn();
      Object.defineProperties(element, {
        scrollWidth: { value: size[0] },
        clientWidth: { value: size[1] },
        scrollBy: { value: calls[name] },
      });
    }
    await act(async () => {
      host.querySelector<HTMLButtonElement>('[aria-label="向右查看表格列"]')!.click();
      host.querySelector<HTMLButtonElement>('[aria-label="向左查看表格列"]')!.click();
    });
    expect(calls[tc.target].mock.calls).toEqual([
      [{ left: tc.distance, behavior: "auto" }], [{ left: -tc.distance, behavior: "auto" }],
    ]);
    for (const [name, call] of Object.entries(calls)) {
      if (name !== tc.target) expect(call).not.toHaveBeenCalled();
    }
  });
});
