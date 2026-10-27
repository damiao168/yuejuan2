// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ReviewAnnotation } from "@edugrade/sdk";
import { ReviewAnnotationWorkspace, type ReviewAnnotationCanvasControls } from "./ReviewAnnotationWorkspace";

const mocks = vi.hoisted(() => ({ annotations: [] as ReviewAnnotation[], save: vi.fn(), remove: vi.fn() }));
vi.mock("./useReviewAnnotations", () => ({ useReviewAnnotations: () => ({
  loading: false, annotations: mocks.annotations, saveState: "idle", error: undefined,
  saveAnnotation: mocks.save, removeAnnotation: mocks.remove
}) }));
vi.mock("antd", () => ({
  Button: ({ onClick, disabled, icon, "aria-label": label, "aria-pressed": pressed }: { onClick: () => void; disabled?: boolean; icon?: ReactNode; "aria-label"?: string; "aria-pressed"?: boolean }) =>
    <button aria-label={label} aria-pressed={pressed} disabled={disabled} onClick={onClick}>{icon}</button>,
  Tooltip: ({ children }: { children: ReactNode }) => children,
  Alert: ({ message }: { message: ReactNode }) => <div role="alert">{message}</div>
}));

let controls: ReviewAnnotationCanvasControls;
const renderCanvas = (value: ReviewAnnotationCanvasControls) => { controls = value; return <div data-testid="same-canvas">原切分图</div>; };
const annotation = { id: "a1", type: "note", content: "✓", visibility: "private", revision: 3, payload: {}, geometry: { coordinate_space: "canonical_image_normalized", x: .2, y: .3, width: 0, height: 0 } } as ReviewAnnotation;

beforeEach(() => { vi.clearAllMocks(); mocks.annotations = []; mocks.save.mockResolvedValue(annotation); mocks.remove.mockResolvedValue(undefined); });
afterEach(cleanup);

describe("常驻图标批注", () => {
  it("不展开面板或评语，原画布不随工具改变", () => {
    render(<ReviewAnnotationWorkspace reviewTaskId="task" renderCanvas={renderCanvas} />);
    const canvas = screen.getByTestId("same-canvas");
    expect(screen.getByRole("toolbar", { name: "批注工具" }).getAttribute("aria-orientation")).toBe("vertical");
    for (const label of ["选择批注", "打勾", "打叉", "框选批注", "删除选中批注"]) expect(screen.getByRole("button", { name: label })).toBeTruthy();
    expect(screen.queryByRole("textbox")).toBeNull();
    expect(screen.queryByRole("button", { name: "批注" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "打勾" }));
    expect(screen.getByTestId("same-canvas")).toBe(canvas);
    expect(controls.tool).toBe("note");
  });

  it.each([["打勾", "✓"], ["打叉", "×"]])("%s点击原图后直接保存，无文字编辑和评分副作用", async (label, symbol) => {
    render(<ReviewAnnotationWorkspace reviewTaskId="task" renderCanvas={renderCanvas} />);
    fireEvent.click(screen.getByRole("button", { name: label }));
    await act(async () => { controls.onPointerDown({ x: .2, y: .3 }); controls.onPointerDown({ x: .4, y: .5 }); });
    expect(mocks.save).toHaveBeenCalledExactlyOnceWith({ type: "note", content: symbol, geometry: { coordinate_space: "canonical_image_normalized", x: .2, y: .3, width: 0, height: 0 }, visibility: "private", payload: {} });
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("矩形仍保存原图归一化坐标，取消手势不残留起点", async () => {
    render(<ReviewAnnotationWorkspace reviewTaskId="task" renderCanvas={renderCanvas} />);
    fireEvent.click(screen.getByRole("button", { name: "框选批注" }));
    await act(async () => { controls.onPointerDown({ x: .2, y: .3 }); controls.onPointerUp({ x: .6, y: .8 }); });
    const saved = mocks.save.mock.calls[0][0];
    expect(saved.type).toBe("rectangle");
    expect(saved.content).toBe("框选");
    expect(saved.geometry.width).toBeCloseTo(.4);
    expect(saved.geometry.height).toBeCloseTo(.5);
    await act(async () => { controls.onPointerDown({ x: .1, y: .1 }); controls.onPointerUp({ x: NaN, y: NaN }); controls.onPointerUp({ x: .9, y: .9 }); });
    expect(mocks.save).toHaveBeenCalledOnce();
  });

  it("选中后删除沿用原revision，失败不隐藏原记录", async () => {
    mocks.annotations = [annotation]; mocks.remove.mockRejectedValueOnce(new Error("offline"));
    render(<ReviewAnnotationWorkspace reviewTaskId="task" renderCanvas={renderCanvas} />);
    await act(async () => controls.onSelect(annotation));
    fireEvent.click(screen.getByRole("button", { name: "删除选中批注" }));
    await vi.waitFor(() => expect(mocks.remove).toHaveBeenCalledExactlyOnceWith(annotation));
    await vi.waitFor(() => expect((screen.getByRole("button", { name: "删除选中批注" }) as HTMLButtonElement).disabled).toBe(false));
    expect(controls.annotations).toEqual([annotation]);
  });

  it.each([{ disabled: true }, { drawingAvailable: false }])("只读或非单题图片不绘制：%j", async options => {
    mocks.annotations = [annotation];
    render(<ReviewAnnotationWorkspace reviewTaskId="task" renderCanvas={renderCanvas} {...options} />);
    expect((screen.getByRole("button", { name: "打勾" }) as HTMLButtonElement).disabled).toBe(true);
    await act(async () => { controls.onPointerDown({ x: .2, y: .3 }); controls.onPointerUp({ x: .5, y: .5 }); });
    expect(mocks.save).not.toHaveBeenCalled();
    if (options.drawingAvailable === false) expect(controls.annotations).toEqual([]);
  });
});
