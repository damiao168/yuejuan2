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
    source: "blob:authorized-answer", label: "1 答题图查看区", helpId: "answer-help", imageSize: Object.freeze({ width: 600, height: 6000 }),
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
    expect(button("智能分段").getAttribute("aria-pressed")).toBe("true");
    expect(container.querySelector(".answer-reading-tiles")?.getAttribute("data-reading-axis")).toBe("vertical");
    expect(tiles(container)).toHaveLength(3);
    expect(tiles(container)[0].querySelector("svg")?.getAttribute("viewBox")?.split(" ").slice(0, 2)).toEqual(["0", "0"]);
    expect(tiles(container)[1].textContent).toContain("与前段有重叠");
    expectOriginalImageWindows(container, props.source);
    expect(container.querySelector("img")).toBeNull();
    expect(props.viewportRef.current).toBe(screen.getByRole("region", { name: props.label }));
  });

  it("整图查看使用完整原始地址，重置回到智能阅读第一组并通知父级复位", () => {
    const props = fixture();
    const { container } = render(<AdaptiveAnswerImage {...props} />);
    const firstGroup = sourceRects(container);
    fireEvent.click(button("下一组"));
    expect(sourceRects(container)).not.toEqual(firstGroup);
    fireEvent.click(button("整图查看"));
    expect(container.querySelectorAll("svg image")).toHaveLength(0);
    expect(container.querySelector("img")?.getAttribute("src")).toBe(props.source);
    expect(container.querySelector<HTMLElement>(".answer-viewer-stage")?.style.height).toBe("900px");
    expect(button("整图查看").getAttribute("aria-pressed")).toBe("true");
    fireEvent.click(button("重置视图"));
    expect(button("智能分段").getAttribute("aria-pressed")).toBe("true");
    expect(sourceRects(container)).toEqual(firstGroup);
    expect(button("上一组").disabled).toBe(true);
    expect(props.onResetView).toHaveBeenCalledOnce();
    expectOriginalImageWindows(container, props.source);
  });

  it("分页只能在现有组之间移动，第一页和最后一页分别禁用越界按钮", () => {
    const props = fixture();
    const { container } = render(<AdaptiveAnswerImage {...props} />);
    const firstGroup = sourceRects(container);
    expect(button("上一组").disabled).toBe(true);
    fireEvent.click(button("上一组"));
    expect(props.onFit).not.toHaveBeenCalled();
    let transitions = 0;
    while (!button("下一组").disabled && transitions < 20) {
      fireEvent.click(button("下一组"));
      transitions += 1;
    }
    expect(transitions).toBeGreaterThan(0);
    expect(transitions).toBeLessThan(20);
    expect(button("下一组").disabled).toBe(true);
    expect(button("上一组").disabled).toBe(false);
    const lastGroup = sourceRects(container);
    fireEvent.click(button("下一组"));
    expect(sourceRects(container)).toEqual(lastGroup);
    expect(props.onFit).toHaveBeenCalledTimes(transitions);
    while (!button("上一组").disabled) fireEvent.click(button("上一组"));
    expect(sourceRects(container)).toEqual(firstGroup);
    expectOriginalImageWindows(container, props.source);
  });

  it("未知尺寸先载入整图，父级收到实际像素尺寸后才呈现智能分段", () => {
    const props = fixture({ imageSize: null });
    const { container, rerender } = render(<AdaptiveAnswerImage {...props} />);
    const image = container.querySelector("img")!;
    Object.defineProperties(image, { naturalWidth: { value: 600 }, naturalHeight: { value: 6000 } });
    fireEvent.load(image);
    expect(props.onImageLoad).toHaveBeenCalledExactlyOnceWith({ width: 600, height: 6000 });
    rerender(<AdaptiveAnswerImage {...props} imageSize={{ width: 600, height: 6000 }} />);
    expect(tiles(container)).toHaveLength(3);
    expectOriginalImageWindows(container, props.source);
  });

  it("横长图沿原图横向分段并竖排，窗口只改变横坐标", () => {
    const props = fixture({ imageSize: { width: 18000, height: 600 } });
    const { container } = render(<AdaptiveAnswerImage {...props} />);
    const grid = container.querySelector<HTMLElement>(".answer-reading-tiles")!;
    expect(grid.getAttribute("data-reading-axis")).toBe("horizontal");
    expect(grid.style.gridTemplateColumns).toBe("repeat(1, max-content)");
    expect(tiles(container)).toHaveLength(3);
    const windows = tiles(container).map(tile => tile.dataset.sourceRect!.split(",").map(Number));
    expect(windows[1][0]).toBeGreaterThan(windows[0][0]);
    windows.forEach(window => { expect(window[1]).toBe(0); expect(window[3]).toBe(600); });
    expectOriginalImageWindows(container, props.source, 18000, 600);
  });

  it("窗口变窄后重新规划列数和原图窗口，卸载时释放尺寸观察器", () => {
    const props = fixture();
    const { container, unmount } = render(<AdaptiveAnswerImage {...props} />);
    const firstWindows = sourceRects(container);
    expect(tiles(container)).toHaveLength(3);
    expect(observers[0].observe).toHaveBeenCalledWith(props.viewportRef.current);
    // 只改变外部可用空间并发出浏览器尺寸事件，保护分段能随窗口重排的交互边界。
    act(() => { viewportSize = { width: 724, height: 948 }; observers[0].trigger(); });
    expect(tiles(container)).toHaveLength(1);
    expect(sourceRects(container)).not.toEqual(firstWindows.slice(0, 1));
    expectOriginalImageWindows(container, props.source);
    unmount();
    expect(observers[0].disconnect).toHaveBeenCalledOnce();
    expect(props.viewportRef.current).toBeNull();
  });

  it("定位数学步骤会跳到覆盖证据中心的组，标记继续使用原图像素", () => {
    const selection = Object.freeze({ segmentId: "segment-1", binding: "artifact:1", stepId: "S2", label: "计算结果", bbox: Object.freeze({ x: .2, y: .78, width: .3, height: .08 }) });
    const props = fixture();
    const { container, rerender } = render(<AdaptiveAnswerImage {...props} />);
    const firstGroup = sourceRects(container);
    rerender(<AdaptiveAnswerImage {...props} mathSelection={selection} />);
    expect(sourceRects(container)).not.toEqual(firstGroup);
    const windows = tiles(container).map(tile => tile.dataset.sourceRect!.split(",").map(Number));
    expect(windows.some(([x, y, width, height]) => 210 >= x && 210 <= x + width && 4920 >= y && 4920 <= y + height)).toBe(true);
    container.querySelectorAll("rect.answer-reading-highlight").forEach(highlight => {
      expect(["x", "y", "width", "height"].map(name => highlight.getAttribute(name))).toEqual(["120", "4680", "180", "480"]);
      expect(highlight.getAttribute("aria-label")).toBe("步骤 S2 的答题位置");
    });
    expectOriginalImageWindows(container, props.source);
  });

  it("保留已选数学步骤时，从整图重置仍回到智能第一组", () => {
    const props = fixture();
    const { container, rerender } = render(<AdaptiveAnswerImage {...props} />);
    const firstGroup = sourceRects(container);
    rerender(<AdaptiveAnswerImage {...props} mathSelection={{ segmentId: "segment-1", binding: "artifact:1", stepId: "S2", label: "计算结果", bbox: { x: .2, y: .78, width: .3, height: .08 } }} />);
    expect(sourceRects(container)).not.toEqual(firstGroup);
    fireEvent.click(button("整图查看"));
    // 重置是显式阅读操作；保留的旧证据选择不能再次覆盖用户指定的第一组。
    fireEvent.click(button("重置视图"));
    expect(sourceRects(container)).toEqual(firstGroup);
    expect(button("上一组").disabled).toBe(true);
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
