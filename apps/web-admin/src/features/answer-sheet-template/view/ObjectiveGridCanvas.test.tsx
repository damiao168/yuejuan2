// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { AnalysisBox } from "../../../api/answerSheetTemplateAnalysis";
import type { ObjectiveGridGroup } from "../../../api/answerSheetObjectiveGrid";
import { ObjectiveGridCanvas } from "./ObjectiveGridCanvas";

describe("objective grid local canvas", () => {
  let root: Root; let host: HTMLDivElement; let canvas: HTMLDivElement; let viewport: HTMLDivElement;
  const onBlock = vi.fn();
  const captures = new Map<HTMLElement, Set<number>>();
  function pointer(type: string, options: MouseEventInit & { pointerId?: number } = {}) {
    const { pointerId = 1, ...rest } = options;
    const event = new MouseEvent(type, { bubbles: true, cancelable: true, button: 0, clientX: 100, clientY: 200, ...rest });
    Object.defineProperty(event, "pointerId", { value: pointerId });
    canvas.dispatchEvent(event); return event;
  }
  async function mount({ imageURL = "blob:page-1", disabled = false, groups = [], block, analysisKey }: {
    imageURL?: string; disabled?: boolean; groups?: ObjectiveGridGroup[]; block?: AnalysisBox; analysisKey?: string;
  } = {}) {
    await act(async () => root.render(<ObjectiveGridCanvas imageURL={imageURL} disabled={disabled} groups={groups} block={block} analysisKey={analysisKey} labels={{}}
      orientation="horizontal" reverse={false} onBlock={onBlock} />));
    canvas = host.querySelector<HTMLDivElement>('[aria-label="选择题大区框选预览"]')!;
    viewport = host.querySelector<HTMLDivElement>('[aria-label="选择题局部画布"]')!;
    canvas.getBoundingClientRect = () => {
      const extent = parseFloat(canvas.style.width) * 10;
      return { left: 0, top: 0, right: extent, bottom: extent, width: extent, height: extent, x: 0, y: 0, toJSON: () => ({}) };
    };
    for (const element of [canvas, viewport]) {
      if (!captures.has(element)) captures.set(element, new Set());
      element.setPointerCapture = id => { captures.get(element)!.add(id); };
      element.hasPointerCapture = id => captures.get(element)!.has(id);
      element.releasePointerCapture = id => { captures.get(element)!.delete(id); };
    }
  }
  async function click(label: string) {
    const button = [...host.querySelectorAll("button")].find(item => item.textContent === label || item.getAttribute("aria-label") === label)!;
    await act(async () => button.click());
  }
  beforeEach(() => {
    vi.clearAllMocks(); captures.clear(); Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); });

  it("ignores the right button and other pointers while committing only the captured left drag", async () => {
    await mount();
    await act(async () => { pointer("pointerdown", { button: 2 }); pointer("pointerup", { clientX: 400, clientY: 600 }); });
    expect(onBlock).not.toHaveBeenCalled();
    await act(async () => { pointer("pointerdown"); pointer("pointerup", { pointerId: 2, clientX: 800, clientY: 900 }); });
    expect(onBlock).not.toHaveBeenCalled(); expect(canvas.hasPointerCapture(1)).toBe(true);
    await act(async () => { pointer("pointerup", { clientX: 400, clientY: 600 }); });
    expect(onBlock).toHaveBeenCalledOnce();
    expect(onBlock.mock.calls[0][0]).toMatchObject({ x: .1, y: .2 });
    expect(onBlock.mock.calls[0][0].width).toBeCloseTo(.3); expect(onBlock.mock.calls[0][0].height).toBeCloseTo(.4);
    expect(canvas.hasPointerCapture(1)).toBe(false);
  });

  it.each(["pointercancel", "lostpointercapture", "page", "disabled"])("discards a partial box after %s", async reason => {
    await mount(); await act(async () => { pointer("pointerdown"); });
    if (reason === "page") await mount({ imageURL: "blob:page-2" });
    else if (reason === "disabled") await mount({ disabled: true });
    else await act(async () => { pointer(reason); });
    await act(async () => { pointer("pointerup", { clientX: 400, clientY: 600 }); });
    expect(onBlock).not.toHaveBeenCalled(); expect(canvas.hasPointerCapture(1)).toBe(false);
  });

  it("zooms only inside the local viewport and draws in normalized image coordinates after zoom", async () => {
    await mount();
    const plain = new WheelEvent("wheel", { bubbles: true, cancelable: true, deltaY: 120 });
    const modified = new WheelEvent("wheel", { bubbles: true, cancelable: true, ctrlKey: true, deltaY: -120, clientX: 500, clientY: 500 });
    await act(async () => { viewport.dispatchEvent(plain); viewport.dispatchEvent(modified); });
    expect(plain.defaultPrevented).toBe(false); expect(modified.defaultPrevented).toBe(true);
    expect(parseFloat(canvas.style.width)).toBeGreaterThan(100);
    const extent = canvas.getBoundingClientRect().width;
    await act(async () => {
      pointer("pointerdown", { clientX: extent * .1, clientY: extent * .2 });
      pointer("pointerup", { clientX: extent * .4, clientY: extent * .6 });
    });
    expect(onBlock.mock.calls[0][0]).toMatchObject({ x: .1, y: .2 });
    expect(onBlock.mock.calls[0][0].width).toBeCloseTo(.3); expect(onBlock.mock.calls[0][0].height).toBeCloseTo(.4);
    const outside = new WheelEvent("wheel", { bubbles: true, cancelable: true, ctrlKey: true, deltaY: -120 });
    host.dispatchEvent(outside); expect(outside.defaultPrevented).toBe(false);
  });

  it("pans within viewport bounds without replacing the drawn block", async () => {
    await mount(); await click("拖动画布");
    Object.defineProperties(viewport, { scrollWidth: { value: 1400 }, scrollHeight: { value: 1800 }, clientWidth: { value: 400 }, clientHeight: { value: 500 } });
    await act(async () => { pointer("pointerdown"); pointer("pointermove", { clientX: -5000, clientY: -5000 }); });
    expect(viewport.scrollLeft).toBe(1000); expect(viewport.scrollTop).toBe(1300);
    await act(async () => { pointer("pointerup"); });
    expect(onBlock).not.toHaveBeenCalled(); expect(viewport.hasPointerCapture(1)).toBe(false);
  });

  it("focuses each analysis once without changing boxes or resetting a later user zoom", async () => {
    const block = { x: .1, y: .2, width: .2, height: .1 };
    const groups = [{ group_id: "g", bbox: block, reason_codes: [], options: Array.from({ length: 4 }, (_, index) => ({
      bbox: { x: .11 + index * .04, y: .23, width: .02, height: .03 }
    })) }];
    await mount({ block });
    Object.defineProperties(viewport, {
      clientWidth: { configurable: true, value: 600 }, clientHeight: { configurable: true, value: 400 },
      scrollWidth: { configurable: true, get: () => canvas.getBoundingClientRect().width },
      scrollHeight: { configurable: true, get: () => canvas.getBoundingClientRect().height }
    });
    await mount({ block, groups, analysisKey: "analysis-1" });
    expect(parseFloat(canvas.style.width)).toBe(276);
    expect(viewport.scrollLeft).toBeCloseTo(252); expect(viewport.scrollTop).toBeCloseTo(490);
    expect(onBlock).not.toHaveBeenCalled();
    expect([...canvas.querySelectorAll("span")].map(span => span.textContent)).toContain("A");
    const green = [...canvas.querySelectorAll<HTMLDivElement>("div")].find(element => element.style.border.includes("56, 158, 13"))!;
    expect(green.style.background).toBe("");
    await click("缩小选择题预览"); viewport.scrollLeft = 50; viewport.scrollTop = 60;
    await mount({ block: { ...block }, groups: [...groups], analysisKey: "analysis-1" });
    expect(parseFloat(canvas.style.width)).toBe(266);
    expect(viewport.scrollLeft).toBe(50); expect(viewport.scrollTop).toBe(60);
    await click("恢复选择题预览原始缩放");
    expect([...canvas.querySelectorAll("span")].map(span => span.textContent)).not.toContain("A");
    await click("聚焦大区"); expect(parseFloat(canvas.style.width)).toBe(276);
    expect(onBlock).not.toHaveBeenCalled();
    await mount({ imageURL: "blob:page-2", block, groups, analysisKey: "analysis-1" });
    await click("恢复选择题预览原始缩放");
    await mount({ imageURL: "blob:page-2", block, groups: [...groups], analysisKey: "analysis-1" });
    expect(parseFloat(canvas.style.width)).toBe(100);
    await mount({ imageURL: "blob:page-2", block, groups, analysisKey: "analysis-2" });
    expect(parseFloat(canvas.style.width)).toBe(276);
  });
});
