// @vitest-environment jsdom
import { act, StrictMode } from "react";
import { createRoot, type Root } from "react-dom/client";
import type { DrawerProps } from "antd";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ModelConfigDrawer } from "./ModelConfigDrawer";

const captured = vi.hoisted(() => ({ props: null as DrawerProps | null }));
vi.mock("antd", () => ({ Drawer: (props: DrawerProps) => { captured.props = props; return null; } }));

describe("模型编辑侧栏的页面滚动锁", () => {
  let host: HTMLDivElement;
  let root: Root;
  const scrollTo = vi.fn();
  beforeEach(() => {
    vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
    vi.stubGlobal("scrollX", 36);
    vi.stubGlobal("scrollY", 1200);
    vi.stubGlobal("scrollTo", scrollTo);
    scrollTo.mockClear();
    document.documentElement.style.overflowX = "clip";
    document.documentElement.style.overflowY = "scroll";
    document.body.style.overflowY = "visible";
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
  });
  afterEach(async () => {
    await act(async () => root.unmount());
    host.remove();
    document.documentElement.removeAttribute("style");
    document.body.removeAttribute("style");
    vi.unstubAllGlobals();
  });

  it("锁住页面实际滚动源，关闭动画完成后恢复原位置与样式", async () => {
    const afterOpenChange = vi.fn();
    await act(async () => root.render(<ModelConfigDrawer open afterOpenChange={afterOpenChange} />));
    const portal = captured.props?.getContainer;
    expect(portal).toBeInstanceOf(HTMLDivElement);
    expect((portal as HTMLDivElement).parentElement).toBe(document.body);
    expect(document.documentElement.style.overflowX).toBe("hidden");
    expect(document.documentElement.style.overflowY).toBe("hidden");
    expect(document.body.style.overflowY).toBe("visible");
    expect(captured.props?.autoFocus).not.toBe(false);

    await act(async () => root.render(<ModelConfigDrawer open={false} afterOpenChange={afterOpenChange} />));
    // 关侧栏仍有退场动画；动画结束前背景不能接受滚轮滚动。
    expect(document.documentElement.style.overflowY).toBe("hidden");
    await act(async () => captured.props?.afterOpenChange?.(false));
    expect(document.documentElement.style.overflowX).toBe("clip");
    expect(document.documentElement.style.overflowY).toBe("scroll");
    expect(scrollTo).toHaveBeenCalledWith({ left: 36, top: 1200, behavior: "instant" });
    expect(afterOpenChange).toHaveBeenCalledWith(false);
  });

  it("侧栏打开时离开页面也会清理滚动锁和专用portal", async () => {
    await act(async () => root.render(<StrictMode><ModelConfigDrawer open /></StrictMode>));
    const portal = captured.props?.getContainer as HTMLDivElement;
    expect(document.documentElement.style.overflowY).toBe("hidden");
    await act(async () => root.render(null));
    expect(document.documentElement.style.overflowY).toBe("scroll");
    expect(document.body.contains(portal)).toBe(false);
    expect(scrollTo).not.toHaveBeenCalled();
  });
});
