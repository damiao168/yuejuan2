// @vitest-environment jsdom
import { act, useState, type ReactNode } from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ReviewAnnotation } from "@edugrade/sdk";
import { ReviewAnnotationWorkspace, type ReviewAnnotationCanvasControls } from "./ReviewAnnotationWorkspace";

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

  it("内嵌批注左栏共用原画布，本题评语复用草稿且不出现位置数值或独立刷新", async () => {
    const onCount = vi.fn();
    const adopted = vi.fn();
    function FeedbackWorkspace() {
      const [feedback, setFeedback] = useState("先核对 /step");
      return <ReviewAnnotationWorkspace {...props} studentFeedback={feedback} onStudentFeedbackChange={setFeedback}
        onAnnotationCountChange={onCount} historicalComments="历史教师评语" historicalPrivateNote="内部旧记录" onAdoptAiFeedback={adopted} canAdoptAiFeedback
        renderCanvas={controls => <button onClick={() => controls.onPointerDown({ x: 0.2, y: 0.3 })}>原切分图</button>} />;
    }
    mocks.apply.mockResolvedValue({ content: "步骤完整" });
    render(<FeedbackWorkspace />);
    const canvas = screen.getByRole("button", { name: "原切分图" });
    fireEvent.change(screen.getByRole("combobox", { name: "批注工具" }), { target: { value: "note" } });
    fireEvent.click(canvas);
    expect(screen.getByRole("textbox", { name: "批注内容" })).toBeTruthy();
    expect(screen.queryByRole("spinbutton")).toBeNull();
    expect(screen.queryByRole("button", { name: "刷新批注" })).toBeNull();
    expect(screen.queryByRole("button", { name: "新增点批注" })).toBeNull();
    fireEvent.click(screen.getByRole("button", { name: "本题评语" }));
    expect(screen.getByRole("textbox", { name: "本题评语" })).toBeTruthy();
    expect(screen.getByText("教师评语：历史教师评语")).toBeTruthy();
    expect(screen.getByText("内部备注：内部旧记录")).toBeTruthy();
    fireEvent.click(screen.getByText("/step · 步骤"));
    await vi.waitFor(() => expect((screen.getByRole("textbox", { name: "本题评语" }) as HTMLTextAreaElement).value).toBe("先核对 步骤完整"));
    fireEvent.click(screen.getByRole("button", { name: "采用 AI 评语" }));
    expect(adopted).toHaveBeenCalledOnce();
    expect(screen.getByRole("button", { name: "原切分图" })).toBe(canvas);
    expect(onCount).toHaveBeenCalledWith(0);
    expect(mocks.save).not.toHaveBeenCalled();
  });

  it("本题评语模板等待期间继续输入，返回结果不能覆盖新内容", async () => {
    let resolveTemplate!: (template: { content: string }) => void;
    mocks.apply.mockImplementationOnce(() => new Promise(resolve => { resolveTemplate = resolve; }));
    const onChange = vi.fn();
    const view = render(<ReviewAnnotationWorkspace {...props} studentFeedback="原评语 /step" onStudentFeedbackChange={onChange} renderCanvas={() => <div>原图</div>} />);
    fireEvent.click(screen.getByRole("button", { name: "本题评语" }));
    fireEvent.click(screen.getByText("/step · 步骤"));
    view.rerender(<ReviewAnnotationWorkspace {...props} studentFeedback="已继续修改" onStudentFeedbackChange={onChange} renderCanvas={() => <div>原图</div>} />);
    await act(async () => { resolveTemplate({ content: "步骤完整" }); });
    expect(onChange).not.toHaveBeenCalled();
  });

  it("离开单题图片时只隐藏图上标记，不把单题坐标套在整区或原图", () => {
    mocks.annotations = [{ id: "annotation", type: "note", content: "单题批注", visibility: "private", revision: 1, payload: {}, geometry: { coordinate_space: "canonical_image_normalized", x: 0.2, y: 0.3, width: 0, height: 0 } } as ReviewAnnotation];
    const canvas = vi.fn((_controls: ReviewAnnotationCanvasControls) => <div>整区图</div>);
    render(<ReviewAnnotationWorkspace {...props} drawingAvailable={false} studentFeedback="" renderCanvas={canvas} />);
    expect(canvas.mock.lastCall?.[0]).toEqual(expect.objectContaining({ tool: "select", annotations: [], disabled: true }));
    fireEvent.click(screen.getByRole("button", { name: "本题评语" }));
    expect(screen.getByRole("textbox", { name: "本题评语" })).toBeTruthy();
    expect(mocks.save).not.toHaveBeenCalled();
  });

  it.each(["unmount", "readonly"])("旧模板回执在组件卸载或失去编辑权限后不能修改评分草稿：%s", async (change) => {
    let resolveTemplate!: (template: { content: string }) => void;
    mocks.apply.mockImplementationOnce(() => new Promise(resolve => { resolveTemplate = resolve; }));
    const onChange = vi.fn();
    const view = render(<ReviewAnnotationWorkspace {...props} studentFeedback="原评语 /step" onStudentFeedbackChange={onChange} renderCanvas={() => <div>原图</div>} />);
    fireEvent.click(screen.getByRole("button", { name: "本题评语" }));
    fireEvent.click(screen.getByText("/step · 步骤"));
    if (change === "unmount") view.unmount();
    else view.rerender(<ReviewAnnotationWorkspace {...props} disabled studentFeedback="原评语 /step" onStudentFeedbackChange={onChange} renderCanvas={() => <div>原图</div>} />);
    await act(async () => { resolveTemplate({ content: "步骤完整" }); });
    expect(onChange).not.toHaveBeenCalled();
  });

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
