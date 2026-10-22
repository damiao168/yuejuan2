// @vitest-environment jsdom
import { act, useRef } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useTemplateCanvasPan } from "./useTemplateCanvasPan";

describe("answer sheet canvas pointer pan", () => {
  let host: HTMLDivElement;
  let root: Root;
  let current: ReturnType<typeof useTemplateCanvasPan>;
  let viewport: HTMLDivElement;
  let capture = vi.fn<(pointerId: number) => void>();
  let release = vi.fn<(pointerId: number) => void>();
  const childDown = vi.fn(), childMove = vi.fn(), childUp = vi.fn();

  function Probe({ enabled = true, scopeKey = "paper-1:page-1" }: { enabled?: boolean; scopeKey?: string }) {
    const scrollRef = useRef<HTMLDivElement>(null);
    current = useTemplateCanvasPan({ scrollRef, enabled, scopeKey });
    return <div ref={scrollRef} data-viewport="true"
      onPointerDownCapture={current.onPointerDownCapture} onPointerMoveCapture={current.onPointerMoveCapture}
      onPointerUpCapture={current.onPointerUpCapture} onPointerCancelCapture={current.onPointerCancelCapture}
      onLostPointerCapture={current.onLostPointerCapture}>
      <div data-canvas="true" onPointerDown={childDown} onPointerMove={childMove} onPointerUp={childUp} />
    </div>;
  }

  async function mount() {
    await act(async () => root.render(<Probe />));
    viewport = host.querySelector<HTMLDivElement>("[data-viewport]")!;
    setDimensions(1000, 1200, 400, 500);
    const captured = new Set<number>();
    capture = vi.fn((pointerId: number) => { captured.add(pointerId); });
    release = vi.fn((pointerId: number) => { captured.delete(pointerId); });
    viewport.setPointerCapture = capture;
    viewport.releasePointerCapture = release;
    viewport.hasPointerCapture = pointerId => captured.has(pointerId);
  }
  function setDimensions(scrollWidth: number, scrollHeight: number, clientWidth: number, clientHeight: number) {
    Object.defineProperties(viewport, {
      scrollWidth: { configurable: true, value: scrollWidth }, scrollHeight: { configurable: true, value: scrollHeight },
      clientWidth: { configurable: true, value: clientWidth }, clientHeight: { configurable: true, value: clientHeight }
    });
  }
  function pointer(type: string, options: MouseEventInit & { pointerId?: number } = {}) {
    const { pointerId = 7, ...mouseOptions } = options;
    const event = new MouseEvent(type, { bubbles: true, cancelable: true, button: 0, buttons: 1,
      clientX: 200, clientY: 200, ...mouseOptions });
    Object.defineProperty(event, "pointerId", { value: pointerId });
    viewport.querySelector("[data-canvas]")!.dispatchEvent(event);
    return event;
  }
  beforeEach(() => {
    vi.clearAllMocks(); Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); });

  it("uses a held left pointer to move the viewport without starting child region editing", async () => {
    await mount(); viewport.scrollLeft = 100; viewport.scrollTop = 150;
    let event: MouseEvent;
    await act(async () => { event = pointer("pointerdown"); });
    expect(event!.defaultPrevented).toBe(true); expect(childDown).not.toHaveBeenCalled();
    expect(capture).toHaveBeenCalledWith(7); expect(current.panning).toBe(true);
    await act(async () => { pointer("pointermove", { clientX: 150, clientY: 120 }); });
    expect(viewport.scrollLeft).toBe(150); expect(viewport.scrollTop).toBe(230);
    expect(childMove).not.toHaveBeenCalled();
    await act(async () => { event = pointer("pointerup", { clientX: 150, clientY: 120, buttons: 0 }); });
    expect(event!.defaultPrevented).toBe(true); expect(childUp).not.toHaveBeenCalled();
    expect(current.panning).toBe(false); expect(release).toHaveBeenCalledWith(7);
    await act(async () => { pointer("pointermove", { clientX: 50, clientY: 20 }); });
    expect(viewport.scrollLeft).toBe(150); expect(viewport.scrollTop).toBe(230);
  });

  it("leaves right mouse buttons and the drawing mode to normal handlers", async () => {
    await mount(); let event: MouseEvent;
    await act(async () => { event = pointer("pointerdown", { button: 2, buttons: 2 }); });
    expect(event!.defaultPrevented).toBe(false); expect(childDown).toHaveBeenCalledTimes(1); expect(capture).not.toHaveBeenCalled();
    await act(async () => root.render(<Probe enabled={false} />));
    await act(async () => { event = pointer("pointerdown"); });
    expect(event!.defaultPrevented).toBe(false); expect(childDown).toHaveBeenCalledTimes(2); expect(current.panning).toBe(false);
  });

  it("clamps all four page edges without accumulating overscroll drift", async () => {
    await mount(); viewport.scrollLeft = 100; viewport.scrollTop = 100;
    await act(async () => { pointer("pointerdown"); });
    for (const [clientX, clientY, expectedLeft, expectedTop] of [
      [10000, 10000, 0, 0], [-10000, -10000, 600, 700], [10000, -10000, 0, 700], [-10000, 10000, 600, 0], [190, 180, 110, 120]
    ]) {
      await act(async () => { pointer("pointermove", { clientX, clientY }); });
      expect(viewport.scrollLeft).toBe(expectedLeft); expect(viewport.scrollTop).toBe(expectedTop);
    }
  });

  it("does not move on a click or create blank overscroll for a page smaller than its viewport", async () => {
    await mount(); viewport.scrollLeft = 100; viewport.scrollTop = 150;
    await act(async () => { pointer("pointerdown"); pointer("pointerup"); });
    expect(viewport.scrollLeft).toBe(100); expect(viewport.scrollTop).toBe(150);
    setDimensions(200, 300, 400, 500); viewport.scrollLeft = 0; viewport.scrollTop = 0;
    await act(async () => { pointer("pointerdown"); pointer("pointermove", { clientX: -1000, clientY: 1000 }); });
    expect(viewport.scrollLeft).toBe(0); expect(viewport.scrollTop).toBe(0);
  });

  it("ignores other pointers and clears capture on cancellation or capture loss", async () => {
    await mount(); await act(async () => { pointer("pointerdown"); });
    await act(async () => { pointer("pointermove", { clientX: 100, pointerId: 8 }); pointer("pointerup", { pointerId: 8 }); });
    expect(current.panning).toBe(true); expect(viewport.scrollLeft).toBe(0);
    await act(async () => { pointer("pointercancel"); });
    expect(current.panning).toBe(false); expect(release).toHaveBeenCalledTimes(1);
    await act(async () => { pointer("pointerdown"); pointer("lostpointercapture"); });
    expect(current.panning).toBe(false); expect(release).toHaveBeenCalledTimes(1);
  });

  it("ends the old drag when changing the source, leaving pan mode, or unmounting", async () => {
    await mount(); await act(async () => { pointer("pointerdown"); });
    await act(async () => root.render(<Probe scopeKey="paper-2:page-1" />));
    expect(current.panning).toBe(false); expect(release).toHaveBeenCalledTimes(1);
    await act(async () => { pointer("pointermove", { clientX: 100 }); });
    expect(viewport.scrollLeft).toBe(0);
    await act(async () => { pointer("pointerdown"); });
    await act(async () => root.render(<Probe enabled={false} scopeKey="paper-2:page-1" />));
    expect(current.panning).toBe(false); expect(release).toHaveBeenCalledTimes(2);
    await act(async () => { pointer("pointermove", { clientX: 100 }); });
    expect(viewport.scrollLeft).toBe(0);
    await act(async () => root.render(<Probe scopeKey="paper-2:page-1" />));
    await act(async () => { pointer("pointerdown"); });
    await act(async () => root.render(null));
    expect(release).toHaveBeenCalledTimes(3);
  });
});
