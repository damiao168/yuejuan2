// @vitest-environment jsdom
import { act, type MouseEvent as ReactMouseEvent } from "react";
import { createRoot, type Root } from "react-dom/client";
import type { ButtonProps } from "antd";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { InternalRouteButton } from "./InternalRouteButton";

const mocks = vi.hoisted(() => ({
  push: vi.fn(),
  hasRouter: true,
  props: null as ButtonProps | null,
}));
vi.mock("@tanstack/react-router", () => ({ useRouter: () => mocks.hasRouter ? { history: { push: mocks.push } } : undefined }));
vi.mock("antd", () => ({ Button: (props: ButtonProps) => {
  mocks.props = props;
  return <a href={props.href} target={props.target} download={props.download} onClick={props.onClick}>{props.children}</a>;
} }));

describe("站内按钮导航", () => {
  let root: Root;
  let host: HTMLDivElement;
  beforeEach(() => {
    vi.clearAllMocks(); mocks.hasRouter = true; mocks.props = null;
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    host = document.createElement("div"); root = createRoot(host);
  });
  afterEach(async () => { await act(async () => root.unmount()); });
  async function render(props: ButtonProps = {}) {
    await act(async () => root.render(<InternalRouteButton href="/admin/exams?status=draft" {...props}>查看考试</InternalRouteButton>));
  }
  function click(overrides: Partial<ReactMouseEvent<HTMLElement>> = {}) {
    const event = {
      button: 0, defaultPrevented: false, metaKey: false, ctrlKey: false, shiftKey: false, altKey: false,
      preventDefault() { this.defaultPrevented = true; }, ...overrides,
    } as ReactMouseEvent<HTMLElement>;
    mocks.props!.onClick!(event);
    return event;
  }

  it("普通左键经过路由历史并保留可复制的干净地址", async () => {
    await render(); const event = click();
    expect(host.querySelector("a")?.getAttribute("href")).toBe("/admin/exams?status=draft");
    expect(event.defaultPrevented).toBe(true);
    expect(mocks.push).toHaveBeenCalledWith("/admin/exams?status=draft");
  });
  it.each(["ctrlKey", "metaKey", "shiftKey", "altKey"] as const)("%s 点击保留浏览器行为", async (key) => {
    await render(); expect(click({ [key]: true }).defaultPrevented).toBe(false);
    expect(mocks.push).not.toHaveBeenCalled();
  });
  it.each([{ target: "_blank" }, { target: "preview" }, { download: "" }, { download: "report.html" }])("新窗口和下载不被路由器接管：%j", async (props) => {
    await render(props); expect(click().defaultPrevented).toBe(false);
    expect(mocks.push).not.toHaveBeenCalled();
  });
  it("中键点击和调用方取消的点击不会跳转", async () => {
    await render(); expect(click({ button: 1 }).defaultPrevented).toBe(false);
    await render({ onClick: (event) => event.preventDefault() });
    expect(click().defaultPrevented).toBe(true); expect(mocks.push).not.toHaveBeenCalled();
  });
  it("独立预览没有路由上下文时仍保留原生链接", async () => {
    mocks.hasRouter = false; await render();
    expect(click().defaultPrevented).toBe(false); expect(mocks.push).not.toHaveBeenCalled();
  });
  it.each(["https://example.test/report", "//example.test/report", "/\\example.test/report", "#note"])("只接管明确的站内路径：%s", async (href) => {
    await render({ href }); expect(click().defaultPrevented).toBe(false);
    expect(mocks.push).not.toHaveBeenCalled();
  });
});
