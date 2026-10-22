// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ReviewAnnotation } from "@edugrade/sdk";
import { ReviewAnnotationWorkspace } from "./ReviewAnnotationWorkspace";

const mocks = vi.hoisted(() => ({
  annotations: [] as ReviewAnnotation[], save: vi.fn(), remove: vi.fn(), reload: vi.fn(),
  create: vi.fn(), update: vi.fn(), deleteTemplate: vi.fn(), apply: vi.fn()
}));
vi.mock("./useReviewAnnotations", () => ({
  AnnotationRevisionConflict: class extends Error {},
  useReviewAnnotations: () => ({ loading: false, annotations: mocks.annotations, templates: [{ id: "template", shortcut: "step", title: "步骤", content: "步骤完整" }], saveState: "idle", error: undefined,
    saveAnnotation: mocks.save, removeAnnotation: mocks.remove, reloadAnnotations: mocks.reload,
    createTemplate: mocks.create, updateTemplate: mocks.update, removeTemplate: mocks.deleteTemplate, applyTemplate: mocks.apply })
}));
vi.mock("./CommentTemplateManager", () => ({ CommentTemplateManager: () => null }));
vi.mock("antd", () => {
  const passthrough = ({ children }: { children?: ReactNode }) => <>{children}</>;
  return {
    Button: ({ children, onClick, disabled, loading, "aria-label": label }: { children?: ReactNode; onClick?: () => void; disabled?: boolean; loading?: boolean; "aria-label"?: string }) => <button onClick={onClick} disabled={disabled || loading} aria-label={label}>{children}</button>,
    Input: { TextArea: ({ value, onChange, disabled, "aria-label": label }: { value: string; onChange: (event: React.ChangeEvent<HTMLTextAreaElement>) => void; disabled?: boolean; "aria-label"?: string }) => <textarea value={value} onChange={onChange} disabled={disabled} aria-label={label} /> },
    InputNumber: ({ value, onChange, disabled, min, max, "aria-label": label }: { value: number; onChange: (value: number | null) => void; disabled?: boolean; min: number; max: number; "aria-label": string }) => <input type="number" value={value} min={min} max={max} disabled={disabled} aria-label={label} onChange={(event) => onChange(event.target.value === "" ? null : Number(event.target.value))} />,
    Select: () => null,
    Segmented: ({ value, onChange, options }: { value: string; onChange: (value: string) => void; options: { value: string }[] }) => <select aria-label="批注工具" value={value} onChange={(event) => onChange(event.target.value)}>{options.map((option) => <option key={option.value} value={option.value}>{option.value}</option>)}</select>,
    Space: passthrough, Tag: passthrough, Tooltip: passthrough, Popconfirm: passthrough,
    Spin: () => null, Alert: ({ message, action }: { message: ReactNode; action?: ReactNode }) => <aside role="alert">{message}{action}</aside>,
    Empty: Object.assign(({ description }: { description: ReactNode }) => <div>{description}</div>, { PRESENTED_IMAGE_SIMPLE: null })
  };
});

describe("批注键盘新增与归一化坐标", () => {
  beforeEach(() => { vi.clearAllMocks(); mocks.annotations = []; mocks.save.mockResolvedValue({}); });
  afterEach(cleanup);
  const props = { reviewTaskId: "task", imageUrl: "/answer.png" };

  it("常用评语插入失败时保留输入并允许明确重试", async () => {
    mocks.apply.mockRejectedValueOnce(new Error("评语暂不可读")).mockResolvedValueOnce({ content: "步骤完整" });
    render(<ReviewAnnotationWorkspace {...props} />); fireEvent.click(screen.getByText("新增点批注"));
    fireEvent.change(screen.getByRole("textbox", { name: "批注内容" }), { target: { value: "继续核对 /step" } });
    fireEvent.click(screen.getByText("/step · 步骤")); await screen.findByText("评语暂不可读");
    expect((screen.getByRole("textbox", { name: "批注内容" }) as HTMLTextAreaElement).value).toBe("继续核对 /step");
    fireEvent.click(screen.getByText("重试插入评语")); await vi.waitFor(() => expect((screen.getByRole("textbox", { name: "批注内容" }) as HTMLTextAreaElement).value).toBe("继续核对 步骤完整"));
    expect(mocks.apply).toHaveBeenCalledTimes(2); expect(mocks.save).not.toHaveBeenCalled();
  });

  it("切换任务后旧评语插入失败不能恢复旧草稿或新任务重试按钮", async () => {
    let fail!: (error: Error) => void;
    mocks.apply.mockImplementationOnce(() => new Promise((_resolve, reject) => { fail = reject; }));
    const view = render(<ReviewAnnotationWorkspace {...props} />);
    fireEvent.click(screen.getByText("新增点批注")); fireEvent.change(screen.getByRole("textbox", { name: "批注内容" }), { target: { value: "旧任务 /step" } });
    fireEvent.click(screen.getByText("/step · 步骤"));
    view.rerender(<ReviewAnnotationWorkspace {...props} reviewTaskId="new-task" />);
    await act(async () => { fail(new Error("旧任务插入失败")); });
    expect(screen.queryByText("旧任务插入失败")).toBeNull(); expect(screen.queryByText("重试插入评语")).toBeNull();
    expect(screen.queryByRole("textbox", { name: "批注内容" })).toBeNull();
  });

  it("不使用指针也能创建矩形、调整百分比并沿用保存协议", async () => {
    render(<ReviewAnnotationWorkspace {...props} />);
    fireEvent.click(screen.getByRole("button", { name: "新增矩形批注" }));
    fireEvent.change(screen.getByRole("spinbutton", { name: "批注距左侧百分比" }), { target: { value: "90" } });
    fireEvent.change(screen.getByRole("textbox", { name: "批注内容" }), { target: { value: "核对最后一步" } });
    fireEvent.click(screen.getByRole("button", { name: "保存批注" }));
    await vi.waitFor(() => expect(mocks.save).toHaveBeenCalledOnce());
    const input = mocks.save.mock.calls[0][0];
    expect(input).toEqual(expect.objectContaining({ type: "rectangle", content: "核对最后一步", visibility: "private", payload: {} }));
    expect(input.geometry.coordinate_space).toBe("canonical_image_normalized");
    expect(input.geometry.x).toBe(0.9); expect(input.geometry.y).toBe(0.25);
    expect(input.geometry.width).toBeCloseTo(0.1); expect(input.geometry.height).toBe(0.5);
    expect(mocks.save.mock.calls[0][1]).toBeUndefined();
    expect(screen.getByRole("region", { name: "答题卡批注画布" })).toBeDefined();
  });

  it("点批注始终为零宽高，坐标输入不改变坐标空间", async () => {
    render(<ReviewAnnotationWorkspace {...props} />);
    fireEvent.click(screen.getByRole("button", { name: "新增点批注" }));
    expect(screen.queryByRole("spinbutton", { name: "批注宽度百分比" })).toBeNull();
    fireEvent.change(screen.getByRole("spinbutton", { name: "批注距顶部百分比" }), { target: { value: "35" } });
    fireEvent.change(screen.getByRole("textbox", { name: "批注内容" }), { target: { value: "此处需复核" } });
    fireEvent.click(screen.getByRole("button", { name: "保存批注" }));
    await vi.waitFor(() => expect(mocks.save).toHaveBeenCalledOnce());
    expect(mocks.save.mock.calls[0][0].geometry).toEqual({ coordinate_space: "canonical_image_normalized", x: 0.5, y: 0.35, width: 0, height: 0 });
  });

  it("无编辑权限时仅查看已有批注，编辑器和新增入口不挂载", () => {
    mocks.annotations = [{ id: "annotation", type: "note", content: "已有评语", visibility: "private", revision: 7, payload: {},
      geometry: { coordinate_space: "canonical_image_normalized", x: 0.5, y: 0.5, width: 0, height: 0 } } as ReviewAnnotation];
    render(<ReviewAnnotationWorkspace {...props} disabled />);
    expect(screen.queryByRole("button", { name: "新增点批注" })).toBeNull();
    expect(screen.queryByRole("button", { name: "常用评语" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "查看批注：已有评语" }));
    expect(screen.getByText("已有评语")).toBeTruthy();
    expect(screen.queryByRole("button", { name: "保存批注" })).toBeNull();
    expect(screen.queryByRole("spinbutton")).toBeNull();
    expect(mocks.save).not.toHaveBeenCalled();
  });

  it("调整已有批注时仍把原revision交给共享hook处理冲突", async () => {
    const existing = { id: "annotation", type: "rectangle", content: "已有评语", visibility: "private", revision: 7, payload: {},
      geometry: { coordinate_space: "canonical_image_normalized", x: 0.1, y: 0.1, width: 0.3, height: 0.3 } } as ReviewAnnotation;
    mocks.annotations = [existing]; render(<ReviewAnnotationWorkspace {...props} />);
    fireEvent.click(screen.getByRole("button", { name: "编辑批注：已有评语" }));
    fireEvent.change(screen.getByRole("spinbutton", { name: "批注宽度百分比" }), { target: { value: "40" } });
    fireEvent.click(screen.getByRole("button", { name: "保存批注" }));
    await vi.waitFor(() => expect(mocks.save).toHaveBeenCalledOnce());
    expect(mocks.save.mock.calls[0][1]).toBe(existing); expect(existing.revision).toBe(7);
    expect(mocks.save.mock.calls[0][0].geometry.width).toBe(0.4);
  });

  it("画布使用命名 section，并按原工具切换区域名称", () => {
    render(<ReviewAnnotationWorkspace {...props} />);
    expect(screen.getByRole("region", { name: "答题卡批注画布" }).tagName).toBe("SECTION");
    fireEvent.change(screen.getByRole("combobox", { name: "批注工具" }), { target: { value: "rectangle" } });
    expect(screen.getByRole("region", { name: "在答题卡上拖动框选批注区域" }).tagName).toBe("SECTION");
    fireEvent.change(screen.getByRole("combobox", { name: "批注工具" }), { target: { value: "note" } });
    expect(screen.getByRole("region", { name: "在答题卡上点击添加批注" }).tagName).toBe("SECTION");
  });

  it("空白草稿不保存，父层普通重渲染保留编辑框与内容", () => {
    const view = render(<ReviewAnnotationWorkspace {...props} />);
    fireEvent.click(screen.getByRole("button", { name: "新增点批注" }));
    const input = screen.getByRole("textbox", { name: "批注内容" }) as HTMLTextAreaElement;
    fireEvent.change(input, { target: { value: "   " } });
    input.focus();
    view.rerender(<ReviewAnnotationWorkspace {...props} />);
    expect(screen.getByRole("textbox", { name: "批注内容" })).toBe(input);
    expect(document.activeElement).toBe(input);
    expect(input.value).toBe("   ");
    expect((screen.getByRole("button", { name: "保存批注" }) as HTMLButtonElement).disabled).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "保存批注" }));
    expect(mocks.save).not.toHaveBeenCalled();
  });
});
