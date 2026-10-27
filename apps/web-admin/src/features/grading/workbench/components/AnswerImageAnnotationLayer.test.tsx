// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ReviewAnnotation } from "@edugrade/sdk";
import type { ReviewAnnotationCanvasControls } from "../../../../components/features/review-annotation";
import { AnswerImageAnnotationLayer } from "./AnswerImageAnnotationLayer";

interface Matrix {
  a: number; b: number; c: number; d: number; e: number; f: number;
}

class TestDOMPoint {
  constructor(readonly x = 0, readonly y = 0) {}
  matrixTransform(matrix: Matrix) {
    return new TestDOMPoint(matrix.a * this.x + matrix.c * this.y + matrix.e, matrix.b * this.x + matrix.d * this.y + matrix.f);
  }
}

class TestPointerEvent extends MouseEvent {
  readonly pointerId: number;
  constructor(type: string, init: PointerEventInit = {}) {
    super(type, init);
    this.pointerId = init.pointerId ?? 0;
  }
}

function fixture(overrides: Partial<ReviewAnnotationCanvasControls> = {}) {
  const controls: ReviewAnnotationCanvasControls = {
    tool: "rectangle", annotations: [], disabled: false,
    onPointerDown: vi.fn(), onPointerUp: vi.fn(), onSelect: vi.fn(), ...overrides
  };
  const view = render(<svg><AnswerImageAnnotationLayer controls={controls} imageSize={{ width: 600, height: 6000 }} scale={.5} /></svg>);
  const layer = view.container.querySelector<SVGGElement>(".answer-annotation-layer")!;
  let captured: number | null = null;
  const setPointerCapture = vi.fn((pointerId: number) => { captured = pointerId; });
  const releasePointerCapture = vi.fn(() => { captured = null; });
  // 分段从原图 y=2400 开始，以 50% 显示在屏幕 (100, 50)；逆变换必须补回分段偏移。
  const inverse = vi.fn(() => ({ a: 2, b: 0, c: 0, d: 2, e: -200, f: 2300 }));
  Object.defineProperties(layer, {
    getScreenCTM: { value: () => ({ inverse }) },
    setPointerCapture: { value: setPointerCapture },
    hasPointerCapture: { value: (pointerId: number) => captured === pointerId },
    releasePointerCapture: { value: releasePointerCapture }
  });
  return { controls, layer, inverse, setPointerCapture, releasePointerCapture, ...view };
}

beforeEach(() => {
  vi.stubGlobal("DOMPoint", TestDOMPoint);
  vi.stubGlobal("PointerEvent", TestPointerEvent);
});

afterEach(() => { cleanup(); vi.unstubAllGlobals(); });

describe("分段图批注坐标与指针边界", () => {
  it("框选将分段屏幕起止点逆变换为整张原图比例，预览完成后释放指针", () => {
    const { controls, layer, inverse, container, setPointerCapture, releasePointerCapture } = fixture();
    fireEvent.pointerDown(layer, { pointerId: 7, button: 0, clientX: 175, clientY: 200 });
    expect(controls.onPointerDown).toHaveBeenCalledExactlyOnceWith({ x: .25, y: .45 });
    expect(setPointerCapture).toHaveBeenCalledExactlyOnceWith(7);
    fireEvent.pointerMove(layer, { pointerId: 7, clientX: 325, clientY: 500 });
    const preview = container.querySelector(".answer-annotation-drawing")!;
    expect(["x", "y", "width"].map(name => Number(preview.getAttribute(name)))).toEqual([150, 2700, 300]);
    expect(Number(preview.getAttribute("height"))).toBeCloseTo(600);
    fireEvent.pointerUp(layer, { pointerId: 7, clientX: 325, clientY: 500 });
    expect(controls.onPointerUp).toHaveBeenCalledExactlyOnceWith({ x: .75, y: .55 });
    expect(inverse).toHaveBeenCalledTimes(3);
    expect(releasePointerCapture).toHaveBeenCalledExactlyOnceWith(7);
    expect(container.querySelector(".answer-annotation-drawing")).toBeNull();
  });

  it.each([{ tool: "select" as const }, { disabled: true }])("选择工具或禁用时不绘制：%j", overrides => {
    const { controls, layer, inverse, setPointerCapture } = fixture(overrides);
    fireEvent.pointerDown(layer, { pointerId: 7, button: 0, clientX: 175, clientY: 200 });
    fireEvent.pointerUp(layer, { pointerId: 7, clientX: 325, clientY: 500 });
    expect(controls.onPointerDown).not.toHaveBeenCalled();
    expect(controls.onPointerUp).not.toHaveBeenCalled();
    expect(setPointerCapture).not.toHaveBeenCalled();
    expect(inverse).not.toHaveBeenCalled();
  });

  it("已有批注支持指针与键盘选择，点标记不会触发背景框选", () => {
    const annotation = {
      id: "annotation-1", type: "note", content: "核对最后一步", visibility: "private", revision: 1, payload: {},
      geometry: { coordinate_space: "canonical_image_normalized", x: .25, y: .45, width: 0, height: 0 }
    } as ReviewAnnotation;
    const { controls, layer, setPointerCapture } = fixture({ annotations: [annotation], selectedId: annotation.id });
    const mark = screen.getByRole("button", { name: "批注：核对最后一步" });
    fireEvent.pointerDown(mark, { pointerId: 7, button: 0, clientX: 175, clientY: 200 });
    fireEvent.pointerUp(mark, { pointerId: 7, clientX: 175, clientY: 200 });
    fireEvent.click(mark);
    fireEvent.keyDown(mark, { key: "Enter" });
    fireEvent.keyDown(mark, { key: " " });
    expect(controls.onSelect).toHaveBeenCalledTimes(3);
    expect(controls.onSelect).toHaveBeenNthCalledWith(1, annotation);
    expect(mark.classList.contains("is-selected")).toBe(true);
    expect(layer.querySelector("circle")?.getAttribute("cx")).toBe("150");
    expect(controls.onPointerDown).not.toHaveBeenCalled();
    expect(controls.onPointerUp).not.toHaveBeenCalled();
    expect(setPointerCapture).not.toHaveBeenCalled();
  });
});
