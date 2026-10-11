// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ResponsiveTable } from "./ResponsiveTable";

const state = vi.hoisted(() => ({ desktop: false }));

vi.mock("antd", async () => {
  const { createElement } = await import("react");
  const Empty = Object.assign(() => createElement("span", null, "Empty"), { PRESENTED_IMAGE_SIMPLE: "simple" });
  return {
    Grid: { useBreakpoint: () => ({ lg: state.desktop }) },
    Empty,
    Pagination: () => null,
    Spin: () => createElement("span", null, "Loading"),
    Table: ({ dataSource, onRow }: { dataSource: { id: string }[]; onRow?: (record: { id: string }, index: number) => object }) =>
      createElement("div", { className: "ant-table-content" }, createElement("table", null, createElement("tbody", null, dataSource.map((record, index) =>
        createElement("tr", { key: record.id, ...onRow?.(record, index) }, createElement("td", null, record.id)))))),
  };
});

describe("responsive table row identity and keyboard actions", () => {
  let host: HTMLDivElement;
  let root: Root;

  beforeEach(() => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    state.desktop = false;
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
  });

  afterEach(async () => {
    await act(async () => root.unmount());
    host.remove();
    vi.restoreAllMocks();
  });

  async function render(onClick = vi.fn(), onKeyDown?: React.KeyboardEventHandler<HTMLElement>) {
    await act(async () => root.render(
      <ResponsiveTable
        rowKey="id"
        columns={[{ title: "Name", dataIndex: "id" }, { title: "Details", dataIndex: "details" }]}
        dataSource={[{ id: "first", details: "More" }]}
        mobilePrimaryCount={1}
        onRow={() => ({ onClick, onKeyDown })}
        pagination={false}
      />,
    ));
    return onClick;
  }

  it("uses an independent native button for a mobile row action", async () => {
    const onClick = await render();
    const button = host.querySelector<HTMLButtonElement>(".responsive-record-action")!;
    expect(button.tagName).toBe("BUTTON");
    expect(button.type).toBe("button");
    expect(host.querySelector("ul > li")).not.toBeNull();
    expect(host.querySelector('[role="button"], [role="list"]')).toBeNull();
    await act(async () => button.click());
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it("keeps detail disclosure interactions separate from the row action", async () => {
    // 展开详情不是打开记录；鼠标与键盘都不能串入外层行操作。
    const onClick = await render();
    const summary = host.querySelector("summary")!;
    await act(async () => {
      summary.dispatchEvent(new MouseEvent("click", { bubbles: true }));
      summary.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true }));
    });
    expect(onClick).not.toHaveBeenCalled();
  });

  it("respects a caller that prevents the default keyboard action", async () => {
    const onKeyDown = vi.fn((event: React.KeyboardEvent<HTMLElement>) => event.preventDefault());
    state.desktop = true;
    const onClick = await render(vi.fn(), onKeyDown);
    await act(async () => host.querySelector("tr")!.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true })));
    expect(onKeyDown).toHaveBeenCalledTimes(1);
    expect(onClick).not.toHaveBeenCalled();
  });

  it("does not stringify object row keys into duplicate React identities", async () => {
    const error = vi.spyOn(console, "error").mockImplementation(() => undefined);
    await act(async () => root.render(
      <ResponsiveTable
        columns={[{ title: "Name", dataIndex: "name" }]}
        dataSource={[{ key: {}, name: "First" }, { key: {}, name: "Second" }]}
        pagination={false}
      />,
    ));
    expect(host.querySelectorAll("ul > li")).toHaveLength(2);
    expect(error.mock.calls.some((args) => args.some((argument) => String(argument).includes("same key")))).toBe(false);
  });

  it("preserves the table region and desktop row keyboard action", async () => {
    state.desktop = true;
    const onClick = await render();
    expect(host.querySelector('section[aria-label="数据表格"] table')).not.toBeNull();
    await act(async () => host.querySelector("tr")!.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true })));
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it("does not duplicate a caller's native-button keyboard action", async () => {
    const onClick = vi.fn();
    const onKeyDown = vi.fn((event: React.KeyboardEvent<HTMLElement>) => {
      if (event.key !== "Enter") return;
      event.preventDefault();
      onClick();
    });
    await render(onClick, onKeyDown);
    const event = new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true });
    await act(async () => host.querySelector(".responsive-record-action")!.dispatchEvent(event));
    expect(event.defaultPrevented).toBe(true);
    expect(onKeyDown).toHaveBeenCalledTimes(1);
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it("does not create a row action when the caller provides no click handler", async () => {
    await act(async () => root.render(
      <ResponsiveTable columns={[{ title: "Name", dataIndex: "id" }]} dataSource={[{ id: "first" }]} rowKey="id" pagination={false} />,
    ));
    expect(host.querySelector(".responsive-record-action")).toBeNull();
  });

  it.each(["Enter", " "])("keeps desktop keyboard activation for %j", async (key) => {
    state.desktop = true;
    const onClick = await render();
    await act(async () => host.querySelector("tr")!.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true })));
    expect(onClick).toHaveBeenCalledTimes(1);
  });

  it("scrolls the actual overflowing AntD container through native controls", async () => {
    const scrollBy = vi.fn();
    await act(async () => root.render(
      <ResponsiveTable columns={[{ title: "Name", dataIndex: "id", sorter: true }]} dataSource={[{ id: "first" }]} pagination={false} />,
    ));
    const content = host.querySelector<HTMLElement>(".ant-table-content")!;
    Object.defineProperties(content, { scrollWidth: { value: 1000 }, clientWidth: { value: 200 }, scrollBy: { value: scrollBy } });
    const right = host.querySelector<HTMLButtonElement>('[aria-label="向右查看表格列"]')!;
    expect(document.getElementById(right.getAttribute("aria-controls")!)).toBe(host.querySelector("section"));
    expect(host.querySelector("section")!.hasAttribute("tabindex")).toBe(false);
    await act(async () => {
      right.click();
      host.querySelector<HTMLButtonElement>('[aria-label="向左查看表格列"]')!.click();
    });
    expect(scrollBy.mock.calls).toEqual([[{ left: 150, behavior: "auto" }], [{ left: -150, behavior: "auto" }]]);
  });
});
