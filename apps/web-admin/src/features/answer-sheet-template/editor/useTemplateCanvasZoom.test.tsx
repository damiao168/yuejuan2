// @vitest-environment jsdom
import { act, useRef, useState, type Dispatch, type SetStateAction } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MAX_TEMPLATE_ZOOM, MIN_TEMPLATE_ZOOM, normalizeTemplateZoom, useTemplateCanvasZoom } from "./useTemplateCanvasZoom";

describe("answer sheet canvas wheel zoom", () => {
  let host: HTMLDivElement;
  let root: Root;
  let current: { zoom: number; setZoom: Dispatch<SetStateAction<number>>; scroll: HTMLDivElement; canvas: HTMLDivElement };
  let requestedZoom: SetStateAction<number> | undefined;

  function Probe({ scopeKey = "paper-1:page-1", pauseZoom = false, initialZoom = 1 }: {
    scopeKey?: string; pauseZoom?: boolean; initialZoom?: number;
  }) {
    const [zoom, setZoom] = useState(initialZoom);
    const canvasRef = useRef<HTMLDivElement>(null);
    const scrollRef = useTemplateCanvasZoom({ zoom, canvasRef, scopeKey,
      setZoom: pauseZoom ? value => { requestedZoom = value; } : setZoom });
    current = { zoom, setZoom, scroll: scrollRef.current!, canvas: canvasRef.current! };
    return <div ref={element => {
      scrollRef.current = element;
      if (!element) return;
      Object.defineProperty(element, "clientHeight", { configurable: true, value: 600 });
      current.scroll = element;
    }}><div ref={element => {
      canvasRef.current = element;
      if (!element) return;
      // JSDOM 不计算布局；用真实倍率和滚动量模拟带内边距的居中画布，覆盖居中到溢出的变化。
      element.getBoundingClientRect = () => ({
        left: 100 + 24 + Math.max(0, (952 - 760 * zoom) / 2) - scrollRef.current!.scrollLeft,
        top: 80 + 24 - scrollRef.current!.scrollTop,
        width: 760 * zoom, height: 1000 * zoom
      }) as DOMRect;
      current.canvas = element;
    }} /></div>;
  }

  const wheel = (options: WheelEventInit = {}) => {
    const event = new WheelEvent("wheel", { bubbles: true, cancelable: true, ctrlKey: true,
      clientX: 790, clientY: 404, deltaY: -100, ...options });
    current.scroll.dispatchEvent(event);
    return event;
  };
  beforeEach(() => {
    requestedZoom = undefined;
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  });
  afterEach(async () => {
    await act(async () => root.unmount()); host.remove(); vi.restoreAllMocks();
  });

  it("intercepts only Ctrl or Meta wheel inside the canvas viewport, including zero movement", async () => {
    await act(async () => root.render(<Probe />));
    const add = vi.spyOn(current.scroll, "addEventListener");
    await act(async () => root.render(<Probe scopeKey="paper-1:page-2" />));
    expect(add).toHaveBeenCalledWith("wheel", expect.any(Function), { passive: false });
    let event: WheelEvent;
    await act(async () => { event = wheel({ ctrlKey: false }); });
    expect(event!.defaultPrevented).toBe(false); expect(current.zoom).toBe(1);
    await act(async () => { event = wheel({ deltaY: 0 }); });
    expect(event!.defaultPrevented).toBe(true); expect(current.zoom).toBe(1);
    const outside = new WheelEvent("wheel", { cancelable: true, ctrlKey: true, deltaY: -100 });
    document.body.dispatchEvent(outside); expect(outside.defaultPrevented).toBe(false);
    await act(async () => { event = wheel({ ctrlKey: false, metaKey: true }); });
    expect(event!.defaultPrevented).toBe(true); expect(current.zoom).toBeGreaterThan(1);
  });

  it("keeps the page point under the cursor when a centered page becomes wider than the viewport", async () => {
    await act(async () => root.render(<Probe />));
    const before = current.canvas.getBoundingClientRect();
    const x = (790 - before.left) / before.width, y = (404 - before.top) / before.height;
    await act(async () => { wheel({ deltaY: -200 }); });
    const after = current.canvas.getBoundingClientRect();
    expect(current.zoom).toBe(1.271);
    expect(current.scroll.scrollLeft).toBeGreaterThan(0);
    expect(after.left + x * after.width).toBeCloseTo(790, 6);
    expect(after.top + y * after.height).toBeCloseTo(404, 6);
  });

  it("applies quick wheel events cumulatively and keeps large input bounded", async () => {
    await act(async () => root.render(<Probe />));
    await act(async () => { wheel(); wheel(); });
    expect(current.zoom).toBe(1.491);
    await act(async () => current.setZoom(1));
    await act(async () => { wheel({ deltaY: -100000 }); });
    expect(current.zoom).toBe(1.271);
  });

  it("normalizes line and page wheel units to the same pixel movement", async () => {
    await act(async () => root.render(<Probe />));
    for (const [deltaMode, deltaY] of [[0, -10], [1, -10 / 16], [2, -10 / 600]]) {
      await act(async () => current.setZoom(1));
      await act(async () => { wheel({ deltaMode, deltaY }); });
      expect(current.zoom).toBe(1.02);
    }
  });

  it("prevents browser zoom at both canvas limits", async () => {
    await act(async () => root.render(<Probe initialZoom={MAX_TEMPLATE_ZOOM} />));
    let event: WheelEvent;
    await act(async () => { event = wheel(); });
    expect(event!.defaultPrevented).toBe(true); expect(current.zoom).toBe(MAX_TEMPLATE_ZOOM);
    await act(async () => current.setZoom(MIN_TEMPLATE_ZOOM));
    await act(async () => { event = wheel({ deltaY: 100 }); });
    expect(event!.defaultPrevented).toBe(true); expect(current.zoom).toBe(MIN_TEMPLATE_ZOOM);
  });

  it("discards an unapplied anchor after switching pages and unregisters on unmount", async () => {
    await act(async () => root.render(<Probe pauseZoom />));
    await act(async () => { wheel(); });
    expect(requestedZoom).toBeGreaterThan(1); expect(current.zoom).toBe(1);
    await act(async () => root.render(<Probe scopeKey="paper-2:page-1" />));
    await act(async () => current.setZoom(2));
    expect(current.scroll.scrollLeft).toBe(0); expect(current.scroll.scrollTop).toBe(0);
    const previousScroll = current.scroll;
    await act(async () => root.render(null));
    const event = new WheelEvent("wheel", { cancelable: true, ctrlKey: true, deltaY: -100 });
    previousScroll.dispatchEvent(event); expect(event.defaultPrevented).toBe(false);
  });

  it("normalizes shared zoom limits and avoids floating point toolbar percentages", () => {
    expect(normalizeTemplateZoom(0.1)).toBe(MIN_TEMPLATE_ZOOM);
    expect(normalizeTemplateZoom(10)).toBe(MAX_TEMPLATE_ZOOM);
    expect(normalizeTemplateZoom(1.23456)).toBe(1.235);
    expect(normalizeTemplateZoom(Number.NaN)).toBe(1);
  });
});
