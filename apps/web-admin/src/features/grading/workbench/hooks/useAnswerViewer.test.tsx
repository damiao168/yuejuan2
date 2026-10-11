// @vitest-environment jsdom
import { act, useRef, type PointerEvent } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useAnswerViewer } from "./useAnswerViewer";

const message = vi.hoisted(() => ({ error: vi.fn(), warning: vi.fn() }));
vi.mock("antd", () => ({ App: { useApp: () => ({ message }) } }));
vi.mock("../../../../api/review", () => ({ downloadReviewWorkspaceImage: vi.fn() }));

describe("答题图局部键盘操作", () => {
  let root: Root;
  let host: HTMLDivElement;
  let viewer: ReturnType<typeof useAnswerViewer>;
  beforeEach(() => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    vi.spyOn(window, "requestAnimationFrame").mockImplementation((callback) => { callback(0); return 1; });
    vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} });
    host = document.createElement("div"); document.body.appendChild(host); root = createRoot(host);
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });
  async function mount() {
    function Harness() {
      viewer = useAnswerViewer({ context: null, canViewOriginalImage: false, prefetchedPreviewRef: useRef(new Map()) });
      return <section aria-label="答题图查看区" tabIndex={0} ref={viewer.viewportRef} onKeyDown={viewer.onKeyDown}><button>子控件</button></section>;
    }
    await act(async () => root.render(<Harness />));
    Object.defineProperties(viewer.viewportRef.current!, { clientWidth: { value: 400 }, clientHeight: { value: 300 } });
    await act(async () => viewer.onImageLoad({ width: 800, height: 600 }));
    await act(async () => viewer.zoom(1));
  }
  async function key(value: string, init: KeyboardEventInit = {}, target: HTMLElement = viewer.viewportRef.current!) {
    const event = new KeyboardEvent("keydown", { key: value, bubbles: true, cancelable: true, ...init });
    await act(async () => { target.dispatchEvent(event); }); return event;
  }

  it("放大后方向键平移、Shift加速，Home恢复适配并归零偏移", async () => {
    await mount(); expect((await key("ArrowLeft")).defaultPrevented).toBe(true);
    await key("ArrowDown", { shiftKey: true }); expect(viewer.offset).toEqual({ x: -24, y: 80 });
    await key("Home"); expect(viewer.offset).toEqual({ x: 0, y: 0 }); expect(viewer.autoFit).toBe(true);
    expect(viewer.scale).toBe(viewer.fitScale);
    expect((await key("ArrowRight")).defaultPrevented).toBe(false);
  });

  it("子控件和浏览器组合键不被局部平移接管", async () => {
    await mount();
    expect((await key("ArrowRight", { altKey: true })).defaultPrevented).toBe(false);
    expect((await key("ArrowRight", { ctrlKey: true })).defaultPrevented).toBe(false);
    expect((await key("ArrowRight", { isComposing: true })).defaultPrevented).toBe(false);
    expect((await key("ArrowRight", {}, host.querySelector("button")!)).defaultPrevented).toBe(false);
    expect(viewer.offset).toEqual({ x: 0, y: 0 });
  });

  it("Escape释放当前拖拽，后续pointermove不会继续移动", async () => {
    await mount(); const viewport = viewer.viewportRef.current!;
    viewport.setPointerCapture = vi.fn(); viewport.hasPointerCapture = vi.fn(() => true); viewport.releasePointerCapture = vi.fn();
    await act(async () => viewer.onPointerDown({ clientX: 100, clientY: 100, pointerId: 7 } as PointerEvent<HTMLElement>));
    expect(viewer.dragging).toBe(true); await key("Escape");
    expect(viewport.releasePointerCapture).toHaveBeenCalledWith(7); expect(viewer.dragging).toBe(false);
    await act(async () => viewer.onPointerMove({ clientX: 200, clientY: 200 } as PointerEvent<HTMLElement>));
    expect(viewer.offset).toEqual({ x: 0, y: 0 });
  });
});
