// @vitest-environment jsdom
import type { ButtonHTMLAttributes, ComponentProps, ReactNode } from "react";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AdaptiveAnswerImage } from "./AdaptiveAnswerImage";

vi.mock("antd", () => ({
  Button: ({ children, icon, size: _size, ...props }: ButtonHTMLAttributes<HTMLButtonElement> & { icon?: ReactNode; size?: string }) => <button {...props}>{icon}{children}</button>,
  Space: ({ children }: { children: ReactNode }) => <>{children}</>,
  Tooltip: ({ children }: { children: ReactNode }) => <>{children}</>,
  Segmented: ({ value, onChange, options, "aria-label": label }: {
    value: string; onChange: (value: string) => void; options: { value: string; label: string }[]; "aria-label": string;
  }) => <div role="group" aria-label={label}>{options.map(option => <button key={option.value} type="button" aria-pressed={value === option.value} onClick={() => onChange(option.value)}>{option.label}</button>)}</div>
}));

type Props = ComponentProps<typeof AdaptiveAnswerImage>;
let viewportSize: { width: number; height: number };
let observers: MockResizeObserver[];

class MockResizeObserver {
  constructor(private readonly callback: ResizeObserverCallback) { observers.push(this); }
  observe = vi.fn();
  disconnect = vi.fn();
  trigger() { this.callback([], this as unknown as ResizeObserver); }
}

function fixture(overrides: Partial<Props> = {}): Props {
  return {
    source: "blob:authorized-answer", label: "1 答题图查看区", helpId: "answer-help", questionType: "calculation", imageSize: Object.freeze({ width: 600, height: 6000 }),
    scale: .15, autoFit: true, rotation: 0, offset: Object.freeze({ x: 0, y: 0 }), dragging: false, viewportRef: { current: null },
    onZoom: vi.fn(), onFit: vi.fn(), onResetView: vi.fn(), onImageLoad: vi.fn(), onPointerDown: vi.fn(), onPointerMove: vi.fn(), onPointerUp: vi.fn(),
    ...overrides
  };
}

const tiles = (container: HTMLElement) => Array.from(container.querySelectorAll<HTMLElement>(".answer-reading-tile"));
const sourceRects = (container: HTMLElement) => tiles(container).map(tile => tile.getAttribute("data-source-rect"));
const button = (name: string) => screen.getByRole<HTMLButtonElement>("button", { name });

function expectOriginalImageWindows(container: HTMLElement, source: string, width = 600, height = 6000) {
  const images = container.querySelectorAll("svg image");
  expect(images.length).toBeGreaterThan(0);
  images.forEach(image => {
    expect(image.getAttribute("href")).toBe(source);
    expect(image.getAttribute("width")).toBe(String(width));
    expect(image.getAttribute("height")).toBe(String(height));
  });
  tiles(container).forEach(tile => {
    expect(tile.querySelector("svg")?.getAttribute("viewBox")?.split(" ")).toEqual(tile.dataset.sourceRect?.split(","));
  });
  expect(container.querySelector("canvas, a[download]")).toBeNull();
}

beforeEach(() => {
  viewportSize = { width: 1824, height: 948 };
  observers = [];
  vi.stubGlobal("ResizeObserver", MockResizeObserver);
  vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockImplementation(() => viewportSize.width);
  vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockImplementation(() => viewportSize.height);
});

afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

describe("AdaptiveAnswerImage 阅读交互", () => {
  it("纵长图默认横排分段，每个显示窗口保留同一张完整原图及原图坐标", () => {
    const props = fixture();
    const { container } = render(<AdaptiveAnswerImage {...props} />);
    expect(button("分段阅读").getAttribute("aria-pressed")).toBe("true");
    expect(container.querySelector(".answer-reading-tiles")?.getAttribute("data-reading-axis")).toBe("vertical");
    expect(tiles(container).length).toBeGreaterThan(1);
    expect(tiles(container)[0].querySelector("svg")?.getAttribute("viewBox")?.split(" ").slice(0, 2)).toEqual(["0", "0"]);
    expect(tiles(container)[1].textContent).toContain("与前段有重叠");
    expectOriginalImageWindows(container, props.source);
    expect(container.querySelector("img")).toBeNull();
    expect(props.viewportRef.current).toBe(screen.getByRole("region", { name: props.label }));
  });

  it("整图查看使用完整原始地址，重置后恢复所有分段并通知父级复位", () => {
    const props = fixture();
    const { container } = render(<AdaptiveAnswerImage {...props} />);
    const originalWindows = sourceRects(container);
    fireEvent.click(button("整图查看"));
    expect(container.querySelectorAll("svg image")).toHaveLength(0);
    expect(container.querySelector("img")?.getAttribute("src")).toBe(props.source);
    expect(container.querySelector<HTMLElement>(".answer-viewer-stage")?.style.height).toBe("900px");
    expect(button("整图查看").getAttribute("aria-pressed")).toBe("true");
    fireEvent.click(button("重置视图"));
    expect(button("分段阅读").getAttribute("aria-pressed")).toBe("true");
    expect(sourceRects(container)).toEqual(originalWindows);
    expect(props.onResetView).toHaveBeenCalledOnce();
    expectOriginalImageWindows(container, props.source);
  });

  it("所有分段一次呈现且首尾完整覆盖，不再保留翻组入口", () => {
    const props = fixture();
    const { container } = render(<AdaptiveAnswerImage {...props} />);
    expect(screen.queryByRole("button", { name: "上一组" })).toBeNull();
    expect(screen.queryByRole("button", { name: "下一组" })).toBeNull();
    expect(screen.getByText(/全部显示/).textContent).toBe(`共 ${tiles(container).length} 段 · 全部显示`);
    const windows = tiles(container).map(tile => tile.dataset.sourceRect!.split(",").map(Number));
    expect(windows[0][1]).toBe(0);
    const lastWindow = windows[windows.length - 1];
    expect(lastWindow[1] + lastWindow[3]).toBeCloseTo(6000);
    windows.slice(1).forEach((window, index) => expect(window[1]).toBeLessThan(windows[index][1] + windows[index][3]));
    expect(container.querySelector<HTMLElement>(".answer-reading-tiles")?.style.gridTemplateColumns).toBe(`repeat(${windows.length}, max-content)`);
    expectOriginalImageWindows(container, props.source);
  });

  it("未知尺寸先载入整图，父级收到实际像素尺寸后才呈现分段阅读", () => {
    const props = fixture({ imageSize: null });
    const { container, rerender } = render(<AdaptiveAnswerImage {...props} />);
    const image = container.querySelector("img")!;
    Object.defineProperties(image, { naturalWidth: { value: 600 }, naturalHeight: { value: 6000 } });
    fireEvent.load(image);
    expect(props.onImageLoad).toHaveBeenCalledExactlyOnceWith({ width: 600, height: 6000 });
    rerender(<AdaptiveAnswerImage {...props} imageSize={{ width: 600, height: 6000 }} />);
    expect(tiles(container).length).toBeGreaterThan(1);
    expectOriginalImageWindows(container, props.source);
  });

  it("横向长条保留完整作答，不把同一行切成多个阅读窗口", () => {
    const props = fixture({ imageSize: { width: 18000, height: 600 } });
    const { container } = render(<AdaptiveAnswerImage {...props} />);
    expect(tiles(container)).toHaveLength(0);
    expect(container.querySelector("img")?.getAttribute("src")).toBe(props.source);
    expect(screen.queryByRole("group", { name: "答题图阅读布局" })).toBeNull();
  });

  it.each(["single_choice", "multiple_choice", "true_false", "fill_blank", "numeric", undefined])("题型 %s 即使几何比例很长也保留整图", (questionType) => {
    const props = fixture({ questionType });
    const { container } = render(<AdaptiveAnswerImage {...props} />);
    expect(tiles(container)).toHaveLength(0);
    expect(container.querySelector("img")?.getAttribute("src")).toBe(props.source);
    fireEvent.click(button("重置视图"));
    expect(tiles(container)).toHaveLength(0);
    expect(props.onResetView).toHaveBeenCalledOnce();
  });

  it("短小多选题完整显示且不呈现分段开关或重复选项窗口", () => {
    const props = fixture({ questionType: "multiple_choice", imageSize: { width: 302, height: 28 }, scale: 1 });
    const { container } = render(<AdaptiveAnswerImage {...props} />);
    expect(container.querySelectorAll("img")).toHaveLength(1);
    expect(tiles(container)).toHaveLength(0);
    expect(screen.getByLabelText("答题图缩放比例").textContent).toBe("100%");
    expect(screen.queryByRole("group", { name: "答题图阅读布局" })).toBeNull();
  });

  it("窗口变窄后重新规划列数和原图窗口，卸载时释放尺寸观察器", () => {
    const props = fixture();
    const { container, unmount } = render(<AdaptiveAnswerImage {...props} />);
    const firstWindows = sourceRects(container);
    const initialCount = tiles(container).length;
    expect(initialCount).toBeGreaterThan(1);
    expect(observers[0].observe).toHaveBeenCalledWith(props.viewportRef.current);
    // 只改变外部可用空间并发出浏览器尺寸事件，保护分段能随窗口重排的交互边界。
    act(() => { viewportSize = { width: 724, height: 948 }; observers[0].trigger(); });
    expect(tiles(container).length).toBeLessThan(initialCount);
    expect(sourceRects(container)).not.toEqual(firstWindows.slice(0, 1));
    expectOriginalImageWindows(container, props.source);
    unmount();
    expect(observers[0].disconnect).toHaveBeenCalledOnce();
    expect(props.viewportRef.current).toBeNull();
  });

  it("数学步骤直接标记在已显示的分段内，继续使用原图像素", () => {
    const selection = Object.freeze({ segmentId: "segment-1", binding: "artifact:1", stepId: "S2", label: "计算结果", bbox: Object.freeze({ x: .2, y: .78, width: .3, height: .08 }) });
    const props = fixture();
    const { container, rerender } = render(<AdaptiveAnswerImage {...props} />);
    const firstGroup = sourceRects(container);
    rerender(<AdaptiveAnswerImage {...props} mathSelection={selection} />);
    expect(sourceRects(container)).toEqual(firstGroup);
    const windows = tiles(container).map(tile => tile.dataset.sourceRect!.split(",").map(Number));
    expect(windows.some(([x, y, width, height]) => 210 >= x && 210 <= x + width && 4920 >= y && 4920 <= y + height)).toBe(true);
    container.querySelectorAll("rect.answer-reading-highlight").forEach(highlight => {
      expect(["x", "y", "width", "height"].map(name => highlight.getAttribute(name))).toEqual(["120", "4680", "180", "480"]);
      expect(highlight.getAttribute("aria-label")).toBe("步骤 S2 的答题位置");
    });
    expectOriginalImageWindows(container, props.source);
  });

  it("保留已选数学步骤时，从整图重置仍恢复全部分段", () => {
    const props = fixture();
    const { container, rerender } = render(<AdaptiveAnswerImage {...props} />);
    const firstGroup = sourceRects(container);
    rerender(<AdaptiveAnswerImage {...props} mathSelection={{ segmentId: "segment-1", binding: "artifact:1", stepId: "S2", label: "计算结果", bbox: { x: .2, y: .78, width: .3, height: .08 } }} />);
    expect(sourceRects(container)).toEqual(firstGroup);
    fireEvent.click(button("整图查看"));
    // 重置只恢复阅读视图；已有步骤仍使用同一套原图坐标。
    fireEvent.click(button("重置视图"));
    expect(sourceRects(container)).toEqual(firstGroup);
    expect(screen.queryByRole("button", { name: "上一组" })).toBeNull();
    expect(props.onResetView).toHaveBeenCalledOnce();
  });

  it.each([
    [90, "translate(6000 0) rotate(90)"],
    [180, "translate(600 6000) rotate(180)"],
    [270, "translate(0 600) rotate(270)"]
  ])("旋转 %s 度时原图和数学框共享同一个坐标变换", (rotation, transform) => {
    const selection = Object.freeze({ segmentId: "segment-1", binding: "artifact:1", stepId: "S1", label: "代入", bbox: Object.freeze({ x: .2, y: .3, width: .4, height: .1 }) });
    const props = fixture({ rotation, mathSelection: selection });
    const { container } = render(<AdaptiveAnswerImage {...props} />);
    const highlights = container.querySelectorAll("rect.answer-reading-highlight");
    expect(highlights.length).toBeGreaterThan(0);
    highlights.forEach(highlight => {
      expect(["x", "y", "width", "height"].map(name => highlight.getAttribute(name))).toEqual(["120", "1800", "240", "600"]);
      expect(highlight.parentElement?.getAttribute("transform")).toBe(transform);
      expect(highlight.parentElement?.querySelector("image")?.getAttribute("href")).toBe(props.source);
    });
    expectOriginalImageWindows(container, props.source);
    expect(selection.bbox).toEqual({ x: .2, y: .3, width: .4, height: .1 });
  });

  it("放大和缩小交给父级处理，按钮本身不改原图地址、证据和显示窗口", () => {
    const props = fixture();
    const { container } = render(<AdaptiveAnswerImage {...props} />);
    const originalWindows = sourceRects(container);
    fireEvent.click(button("放大答题图"));
    fireEvent.click(button("缩小答题图"));
    expect(props.onZoom).toHaveBeenNthCalledWith(1, 1);
    expect(props.onZoom).toHaveBeenNthCalledWith(2, -1);
    expect(sourceRects(container)).toEqual(originalWindows);
    expect(props.imageSize).toEqual({ width: 600, height: 6000 });
    expectOriginalImageWindows(container, props.source);
  });
});
