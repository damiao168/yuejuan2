// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { RegistrationCorrection, RegistrationCorrectionContext } from "../api/capture";
import { RegistrationCorrectionWorkspace } from "./RegistrationCorrectionWorkspace";

const mocks = vi.hoisted(() => ({
  context: vi.fn(), create: vi.fn(), preview: vi.fn(), get: vi.fn(), apply: vi.fn(), undo: vi.fn(), download: vi.fn(),
  changed: vi.fn(), close: vi.fn(), message: { error: vi.fn(), success: vi.fn() },
}));
vi.mock("../api/capture", () => ({
  getRegistrationCorrectionContext: mocks.context, createRegistrationCorrection: mocks.create,
  previewRegistrationCorrection: mocks.preview, getRegistrationCorrection: mocks.get,
  applyRegistrationCorrection: mocks.apply, undoRegistrationCorrection: mocks.undo,
}));
vi.mock("../api/files", () => ({ downloadFileBlob: mocks.download }));
vi.mock("../api/client", () => ({
  getUserErrorMessage: (error: unknown, fallback: string) => error instanceof Error ? error.message : fallback,
}));
vi.mock("pdfjs-dist", () => ({ GlobalWorkerOptions: {}, getDocument: vi.fn() }));
vi.mock("pdfjs-dist/build/pdf.worker.min.mjs?url", () => ({ default: "worker" }));
vi.mock("antd", async () => {
  const React = await import("react");
  const Input = Object.assign(() => null, {
    TextArea: ({ value, onChange, disabled }: { value: string; onChange: React.ChangeEventHandler<HTMLTextAreaElement>; disabled?: boolean }) =>
      React.createElement("textarea", { value, onChange, disabled }),
  });
  return {
    App: { useApp: () => ({ message: mocks.message }) },
    Alert: ({ message, description, action }: { message: string; description?: string; action?: ReactNode }) => React.createElement("div", { role: "alert" }, message, description, action),
    Button: ({ children, onClick, disabled, loading, "aria-label": label }: {
      children?: ReactNode; onClick?: () => void; disabled?: boolean; loading?: boolean; "aria-label"?: string;
    }) => React.createElement("button", { onClick, disabled: disabled || loading, "aria-label": label, "data-loading": String(Boolean(loading)) }, children),
    Input,
    InputNumber: ({ value, disabled, onChange }: { value: number; disabled?: boolean; onChange: (value: number | null) => void }) =>
      React.createElement("input", { type: "number", value, disabled, onChange: (event: React.ChangeEvent<HTMLInputElement>) => onChange(event.target.value === "" ? null : Number(event.target.value)) }),
    Space: ({ children }: { children: ReactNode }) => React.createElement("div", null, children),
    Spin: () => React.createElement("span", null, "加载中"),
    Switch: ({ checked, disabled, onChange }: { checked: boolean; disabled?: boolean; onChange: (checked: boolean) => void }) =>
      React.createElement("button", { role: "switch", "aria-checked": checked, disabled, onClick: () => onChange(!checked) }, "调整模板参考点"),
  };
});

const context: RegistrationCorrectionContext = {
  registration_run_id: "run-a", capture_page_id: "capture-page-a", exam_id: "exam-a", page_revision: 7,
  page_no: 2, source_file_asset_id: "source-file", template_file_asset_id: "template-file",
  template_content_type: "image/png", template_width: 1000, template_height: 1400,
};
const points = [{ x: 0, y: 0 }, { x: 1, y: 0 }, { x: 1, y: 1 }, { x: 0, y: 1 }];
function correction(status: RegistrationCorrection["status"] = "preview_ready", overrides: Partial<RegistrationCorrection> = {}): RegistrationCorrection {
  return {
    id: "correction-a", capture_page_id: "capture-page-a", base_registration_run_id: "run-a", source_page_revision: 7,
    template_id: "template-a", template_content_hash: "template-hash", page_no: 2,
    source_points: points, template_points: points, advanced_anchor_mode: false,
    status, revision: 2, attempt_count: 1, preview_registered_file_asset_id: "preview-file", validation_report: {},
    ...overrides,
  };
}
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

describe("人工版面对齐重构边界", () => {
  let root: Root | null;
  let host: HTMLDivElement;
  let props: Parameters<typeof RegistrationCorrectionWorkspace>[0];

  beforeEach(() => {
    vi.resetAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.context.mockResolvedValue({ context });
    mocks.create.mockResolvedValue({ correction: correction("draft", { revision: 1 }) });
    mocks.preview.mockResolvedValue({ correction: correction() });
    mocks.get.mockResolvedValue({ correction: correction() });
    mocks.apply.mockResolvedValue({ correction: correction("applied", { revision: 3 }) });
    mocks.undo.mockResolvedValue({ correction: correction("undone", { revision: 4 }) });
    mocks.changed.mockResolvedValue(undefined);
    mocks.download.mockResolvedValue({ blob: new Blob(["scan"]), contentType: "image/png" });
    let sequence = 0;
    vi.stubGlobal("URL", Object.assign(class extends URL {}, {
      createObjectURL: vi.fn(() => `blob:image-${++sequence}`), revokeObjectURL: vi.fn(),
    }));
    props = { runId: "run-a", canManage: true, onChanged: mocks.changed, onClose: mocks.close };
    host = document.createElement("div");
    document.body.append(host);
    root = createRoot(host);
  });
  afterEach(async () => {
    await act(async () => { root?.unmount(); });
    host.remove();
    if (vi.isFakeTimers()) vi.clearAllTimers();
    vi.useRealTimers();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });
  async function render(next: Partial<typeof props> = {}) {
    props = { ...props, ...next };
    await act(async () => { root!.render(<RegistrationCorrectionWorkspace {...props} />); });
  }
  function button(label: string) {
    const found = [...host.querySelectorAll<HTMLButtonElement>("button")].find((item) => item.textContent === label || item.getAttribute("aria-label") === label);
    expect(found).toBeDefined();
    return found!;
  }
  async function click(label: string) {
    await act(async () => { button(label).click(); });
  }
  async function changeValue(input: HTMLInputElement | HTMLTextAreaElement, value: string) {
    const prototype = input instanceof HTMLTextAreaElement ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
    await act(async () => {
      Object.getOwnPropertyDescriptor(prototype, "value")!.set!.call(input, value);
      input.dispatchEvent(new Event("input", { bubbles: true }));
    });
  }
  async function reason(value: string) {
    await changeValue(host.querySelector("textarea")!, value);
  }
  async function keyMove(key: string, shiftKey = false) {
    const event = new KeyboardEvent("keydown", { key, shiftKey, bubbles: true, cancelable: true });
    await act(async () => { button("学生答卷页左上点").dispatchEvent(event); });
    return event;
  }
  async function ready() {
    await render();
    await click("预览校正");
    expect(mocks.preview).toHaveBeenCalledWith("correction-a", 1);
    if (host.querySelector<HTMLImageElement>('img[alt="人工校正预览"]')) {
      await act(async () => host.querySelector<HTMLImageElement>('img[alt="人工校正预览"]')!.dispatchEvent(new Event("load")));
    }
  }

  it("初始上下文失败显示持久错误，同页重读后恢复当前页材料", async () => {
    mocks.context.mockRejectedValueOnce(new Error("校正上下文暂不可读"));
    await render();
    expect(host.textContent).toContain("校正材料读取失败");
    expect(host.textContent).toContain("校正上下文暂不可读");
    expect(host.textContent).not.toContain("加载中");
    expect(button("预览校正").disabled).toBe(true);
    await click("重新读取校正材料");
    expect(mocks.context).toHaveBeenCalledTimes(2);
    expect(host.querySelector('img[alt="学生答卷页"]')).not.toBeNull();
    expect(button("预览校正").disabled).toBe(false);
  });

  it("单幅材料下载或显示失败保留另一幅和当前点位，重读不抹掉草稿", async () => {
    await render();
    await keyMove("ArrowRight");
    await reason("保留当前点位");
    mocks.download.mockImplementation(async (id: string) => {
      if (id === "template-file") throw new Error("模板下载失败");
      return { blob: new Blob(["scan"]), contentType: "image/png" };
    });
    await click("重新读取校正材料");
    expect(host.textContent).toContain("模板下载失败");
    expect(host.textContent).not.toContain("加载中");
    expect(host.querySelector('img[alt="学生答卷页"]')).not.toBeNull();
    expect(button("预览校正").disabled).toBe(true);
    expect(button("学生答卷页左上点").style.left).toBe("0.2%");
    mocks.download.mockResolvedValue({ blob: new Blob(["scan"]), contentType: "image/png" });
    await click("重新读取答题卡模板");
    expect(host.querySelector("textarea")!.value).toBe("保留当前点位");
    expect(button("学生答卷页左上点").style.left).toBe("0.2%");
    await act(async () => host.querySelector('img[alt="学生答卷页"]')!.dispatchEvent(new Event("error")));
    expect(host.textContent).toContain("答卷图像无法显示");
    expect(button("预览校正").disabled).toBe(true);
    await click("重新读取学生答卷页");
    expect(button("预览校正").disabled).toBe(false);
  });

  it("同页重读发现版本更新时保留点位但不能应用旧版本预览", async () => {
    await ready(); await reason("核对后应用");
    expect(button("应用校正").disabled).toBe(false);
    mocks.context.mockResolvedValueOnce({ context: { ...context, page_revision: 8, source_file_asset_id: "source-v8" } });
    await click("重新读取校正材料");
    expect(host.textContent).toContain("答卷或模板版本已更新");
    expect(host.querySelector('img[alt="人工校正预览"]')).toBeNull();
    expect(button("应用校正").disabled).toBe(true);
    await click("预览校正");
    expect(mocks.create).toHaveBeenLastCalledWith("run-a", expect.objectContaining({ page_revision: 8 }));
  });

  it("点位修改后清除旧预览与冻结记录，必须重新预览", async () => {
    await ready(); await reason("核对后应用");
    expect(button("应用校正").disabled).toBe(false);
    await keyMove("ArrowRight");
    expect(host.querySelector('img[alt="人工校正预览"]')).toBeNull();
    expect(button("应用校正").disabled).toBe(true);
    await click("应用校正"); expect(mocks.apply).not.toHaveBeenCalled();
  });

  it("新记录图片失败不沿用旧图，只重试该记录且实际显示后才可应用", async () => {
    await ready(); await reason("核对后应用");
    mocks.create.mockResolvedValueOnce({ correction: correction("draft", { id: "correction-b", revision: 3 }) });
    mocks.preview.mockResolvedValueOnce({ correction: correction("preview_ready", { id: "correction-b", revision: 4, preview_registered_file_asset_id: "preview-b" }) });
    mocks.download.mockRejectedValueOnce(new Error("新预览下载失败"));
    await click("预览校正");
    expect(host.querySelector('img[alt="人工校正预览"]')).toBeNull();
    expect(host.textContent).toContain("新预览下载失败");
    expect(button("应用校正").disabled).toBe(true);
    await click("重新加载校正预览");
    expect(mocks.create).toHaveBeenCalledTimes(2);
    expect(mocks.download).toHaveBeenLastCalledWith("preview-b");
    expect(button("应用校正").disabled).toBe(true);
    await act(async () => host.querySelector('img[alt="人工校正预览"]')!.dispatchEvent(new Event("load")));
    await click("应用校正");
    expect(mocks.apply).toHaveBeenCalledWith("correction-b", 4, "核对后应用");
  });

  it("换页面后旧校正创建返回不启动预览", async () => {
    const pending = deferred<{ correction: RegistrationCorrection }>();
    mocks.create.mockReturnValueOnce(pending.promise);
    await render(); await click("预览校正");
    mocks.context.mockResolvedValueOnce({ context: { ...context, registration_run_id: "run-b", page_revision: 8 } });
    await render({ runId: "run-b" });
    await act(async () => pending.resolve({ correction: correction("draft") }));
    expect(mocks.preview).not.toHaveBeenCalled();
    expect(host.querySelector('img[alt="人工校正预览"]')).toBeNull();
    expect(button("应用校正").disabled).toBe(true);
  });

  it("服务端返回其他页面版本的校正记录时不能下载或应用", async () => {
    await render(); await reason("核对后应用");
    mocks.preview.mockResolvedValueOnce({ correction: correction("preview_ready", { source_page_revision: 8 }) });
    await click("预览校正");
    expect(mocks.download).toHaveBeenCalledTimes(2);
    expect(host.textContent).toContain("校正记录与当前页面或点位不一致");
    expect(button("应用校正").disabled).toBe(true);
  });

  it("应用返回前换页面不回填旧状态或旧成功提示", async () => {
    await ready(); await reason("核对后应用");
    const pending = deferred<{ correction: RegistrationCorrection }>();
    mocks.apply.mockReturnValueOnce(pending.promise);
    await click("应用校正"); mocks.message.success.mockClear();
    mocks.context.mockResolvedValueOnce({ context: { ...context, registration_run_id: "run-b", page_revision: 8 } });
    await render({ runId: "run-b" });
    await act(async () => pending.resolve({ correction: correction("applied", { revision: 3 }) }));
    expect(mocks.changed).not.toHaveBeenCalled();
    expect(mocks.message.success).not.toHaveBeenCalled();
    expect(host.querySelector(".correction-validation strong")?.textContent).toBe("等待生成预览");
  });

  it("四个方向键保持普通步长，Shift 保持加速步长", async () => {
    await render();
    const inputs = host.querySelectorAll<HTMLInputElement>(".correction-point-row input");
    await changeValue(inputs[0], "0.5");
    await changeValue(inputs[1], "0.5");
    for (const item of [
      { key: "ArrowLeft", x: 49.8, y: 50 }, { key: "ArrowRight", x: 50, y: 50 },
      { key: "ArrowUp", x: 50, y: 49.8 }, { key: "ArrowDown", x: 50, y: 50 },
    ]) {
      expect((await keyMove(item.key)).defaultPrevented).toBe(true);
      expect(Number.parseFloat(button("学生答卷页左上点").style.left)).toBeCloseTo(item.x);
      expect(Number.parseFloat(button("学生答卷页左上点").style.top)).toBeCloseTo(item.y);
    }
    await keyMove("ArrowRight", true);
    expect(Number.parseFloat(button("学生答卷页左上点").style.left)).toBeCloseTo(51);
    await keyMove("ArrowDown", true);
    expect(Number.parseFloat(button("学生答卷页左上点").style.top)).toBeCloseTo(51);
  });

  it("非方向键不阻止默认行为，越界微调仍限制在 0 到 1", async () => {
    await render();
    expect((await keyMove("Enter")).defaultPrevented).toBe(false);
    await keyMove("ArrowLeft");
    expect(button("学生答卷页左上点").style.left).toBe("0%");
    await changeValue(host.querySelector<HTMLInputElement>(".correction-point-row input")!, "1");
    await keyMove("ArrowRight", true);
    expect(button("学生答卷页左上点").style.left).toBe("100%");
  });

  it("拖动使用当前画布边界归一化，缩放不重置点位", async () => {
    await render();
    const canvas = host.querySelector<HTMLDivElement>(".correction-canvas")!;
    vi.spyOn(canvas, "getBoundingClientRect").mockReturnValue({ left: 100, top: 200, width: 400, height: 800,
      right: 500, bottom: 1000, x: 100, y: 200, toJSON: () => ({}) });
    const point = button("学生答卷页左上点");
    point.setPointerCapture = vi.fn();
    const down = new MouseEvent("pointerdown", { bubbles: true });
    Object.defineProperty(down, "pointerId", { value: 7 });
    await act(async () => { point.dispatchEvent(down); });
    await act(async () => { canvas.dispatchEvent(new MouseEvent("pointermove", { bubbles: true, clientX: 300, clientY: 600 })); });
    expect(point.setPointerCapture).toHaveBeenCalledWith(7);
    expect(point.style.left).toBe("50%");
    expect(point.style.top).toBe("50%");
    await click("放大");
    expect(point.style.left).toBe("50%");
    expect(point.style.top).toBe("50%");
    await click("预览校正");
    expect(mocks.create.mock.calls[0][1].source_points[0]).toEqual({ x: 0.5, y: 0.5 });
  });

  it("普通重渲染保留点位和输入焦点，只读权限保留原因但锁住编辑", async () => {
    await render();
    expect(button("答题卡模板左上点").disabled).toBe(true);
    await click("调整模板参考点");
    expect(button("答题卡模板左上点").disabled).toBe(false);
    await reason("保留的草稿");
    const input = host.querySelector("textarea")!;
    input.focus();
    await render({ onClose: vi.fn() });
    expect(document.activeElement).toBe(input);
    expect(host.querySelector("textarea")).toBe(input);
    await render({ canManage: false });
    expect(input.value).toBe("保留的草稿");
    expect(input.disabled).toBe(true);
    expect(button("学生答卷页左上点").disabled).toBe(true);
    expect(button("答题卡模板左上点").disabled).toBe(true);
    expect(button("预览校正").disabled).toBe(true);
    expect(button("应用校正").disabled).toBe(true);
    expect(button("撤销应用").disabled).toBe(true);
  });

  it.each([
    { status: "preview_ready", label: "预览检查通过" }, { status: "applied", label: "校正已应用" },
    { status: "undone", label: "已撤销校正" }, { status: "failed", label: "预览检查未通过" },
    { status: "expired", label: "等待生成预览" }, { status: "superseded", label: "等待生成预览" },
  ] as const)("$status 保留原状态文案", async ({ status, label }) => {
    mocks.preview.mockResolvedValue({ correction: correction(status) });
    await render();
    await click("预览校正");
    expect(host.querySelector(".correction-validation strong")?.textContent).toBe(label);
  });

  it.each([
    { coverage: 0, text: "预览仅供查看，点击『应用校正』后才会生效" },
    { coverage: 0.456, text: "校正后覆盖页面 46%" },
  ])("coverage=$coverage 保留原 truthiness 与取整", async ({ coverage, text }) => {
    mocks.preview.mockResolvedValue({ correction: correction("preview_ready", { coverage }) });
    await ready();
    expect(host.querySelector(".correction-validation")?.textContent).toContain(text);
  });

  it("排队预览每 1500ms 回读，使用当前记录 id，页面版本和点位提交顺序保持", async () => {
    vi.useFakeTimers();
    mocks.preview.mockResolvedValue({ correction: correction("queued") });
    mocks.get.mockResolvedValueOnce({ correction: correction("draft", { id: "correction-b" }) }).mockResolvedValueOnce({ correction: correction() });
    await render();
    await click("预览校正");
    expect(mocks.create).toHaveBeenCalledWith("run-a", { page_revision: 7, source_points: points, template_points: points, advanced_anchor_mode: false });
    expect(mocks.get).not.toHaveBeenCalled();
    await act(async () => { await vi.advanceTimersByTimeAsync(1499); });
    expect(mocks.get).not.toHaveBeenCalled();
    await act(async () => { await vi.advanceTimersByTimeAsync(1); });
    expect(mocks.get).toHaveBeenNthCalledWith(1, "correction-a");
    await act(async () => { await vi.advanceTimersByTimeAsync(1500); });
    expect(mocks.get).toHaveBeenNthCalledWith(2, "correction-b");
    expect(mocks.download).toHaveBeenLastCalledWith("preview-file");
    expect(mocks.message.success).toHaveBeenCalledWith("校正预览已生成");
  });

  it("预览持续排队时最多回读 60 次，未就绪不得下载预览", async () => {
    vi.useFakeTimers();
    mocks.preview.mockResolvedValue({ correction: correction("queued") });
    mocks.get.mockResolvedValue({ correction: correction("queued") });
    await render();
    await click("预览校正");
    await act(async () => { await vi.advanceTimersByTimeAsync(90000); });
    expect(mocks.get).toHaveBeenCalledTimes(60);
    expect(mocks.download).toHaveBeenCalledTimes(2);
    expect(mocks.message.error).toHaveBeenCalledWith("校正预览未完成，请稍后重试");
    expect(button("预览校正").getAttribute("data-loading")).toBe("false");
  });

  it.each([
    { code: "coverage_too_low", text: "对齐后覆盖不足，请把四个点贴近答卷四角后重试" },
    { code: "unknown_error", text: "预览失败，请调整对应点后重试" },
  ])("$code 失败保留错误码日志和用户提示", async ({ code, text }) => {
    const warning = vi.spyOn(console, "warn").mockImplementation(() => undefined);
    mocks.preview.mockResolvedValue({ correction: correction("failed", { error_code: code }) });
    await render();
    await click("预览校正");
    expect(warning).toHaveBeenCalledWith("registration correction preview failed:", code);
    expect(mocks.message.error).toHaveBeenCalledWith(text);
    expect(mocks.download).toHaveBeenCalledTimes(2);
  });

  it("即使状态就绪，缺少预览文件也不能下载或报预览成功", async () => {
    mocks.preview.mockResolvedValue({ correction: correction("preview_ready", { preview_registered_file_asset_id: undefined }) });
    await render();
    await click("预览校正");
    expect(mocks.download).toHaveBeenCalledTimes(2);
    expect(mocks.message.success).not.toHaveBeenCalled();
    expect(mocks.message.error).toHaveBeenCalledWith("校正预览未完成，请稍后重试");
  });

  it("创建请求返回前卸载，不再启动预览或回填成功提示", async () => {
    const pending = deferred<{ correction: RegistrationCorrection }>();
    mocks.create.mockReturnValue(pending.promise);
    await render();
    await click("预览校正");
    await act(async () => { root!.unmount(); root = null; });
    await act(async () => { pending.resolve({ correction: correction("draft") }); });
    expect(mocks.preview).not.toHaveBeenCalled();
    expect(mocks.message.success).not.toHaveBeenCalled();
  });

  it("上下文切换后旧结果不下载旧素材，新任务仍按新页面版本创建", async () => {
    const pending = deferred<{ context: RegistrationCorrectionContext }>();
    mocks.context.mockReturnValueOnce(pending.promise).mockResolvedValueOnce({ context: { ...context, registration_run_id: "run-b", page_revision: 8, source_file_asset_id: "source-b" } });
    await render();
    await render({ runId: "run-b" });
    await act(async () => { pending.resolve({ context }); });
    expect(mocks.download).not.toHaveBeenCalledWith("source-file");
    await click("预览校正");
    expect(mocks.create.mock.calls[0][0]).toBe("run-b");
    expect(mocks.create.mock.calls[0][1].page_revision).toBe(8);
  });

  it("应用使用预览记录版本和去空格原因，回读完成后才提示成功，撤销沿用返回版本", async () => {
    await ready();
    await reason("  人工对齐  ");
    const pending = deferred<void>();
    mocks.changed.mockReturnValueOnce(pending.promise);
    mocks.message.success.mockClear();
    await click("应用校正");
    expect(mocks.apply).toHaveBeenCalledWith("correction-a", 2, "人工对齐");
    expect(host.querySelector("textarea")!.value).toBe("");
    expect(host.querySelector(".correction-validation strong")?.textContent).toBe("校正已应用");
    expect(mocks.changed).toHaveBeenCalledOnce();
    expect(mocks.message.success).not.toHaveBeenCalled();
    await act(async () => { pending.resolve(undefined); });
    expect(mocks.message.success).toHaveBeenCalledWith("人工校正已应用");
    await reason("  撤销原因  ");
    await click("撤销应用");
    expect(mocks.undo).toHaveBeenCalledWith("correction-a", 3, "撤销原因");
    expect(host.querySelector(".correction-validation strong")?.textContent).toBe("已撤销校正");
    expect(button("学生答卷页左上点").disabled).toBe(false);
    expect(mocks.message.success).toHaveBeenCalledWith("已撤销校正，恢复原始处理结果");
  });

  it.each(["apply", "undo"] as const)("%s 请求失败保留原因和原状态，不触发回读", async (operation) => {
    await ready();
    if (operation === "undo") {
      await reason("先应用");
      await click("应用校正");
    }
    mocks.changed.mockClear();
    mocks[operation].mockRejectedValue(new Error("保存失败"));
    await reason("待重试的原因");
    await click(operation === "apply" ? "应用校正" : "撤销应用");
    expect(host.querySelector("textarea")!.value).toBe("待重试的原因");
    expect(mocks.changed).not.toHaveBeenCalled();
    expect(mocks.message.error).toHaveBeenCalledWith("保存失败");
    expect(host.querySelector(".correction-validation strong")?.textContent).toBe(operation === "apply" ? "预览检查通过" : "校正已应用");
  });

  it("服务端应用成功而批次回读失败，保留返回状态且不伪报应用成功", async () => {
    // 服务端保存已成功，回调失败时本地仍显示已应用，原因字段已清空。
    await ready();
    mocks.message.success.mockClear();
    mocks.changed.mockRejectedValue(new Error("回读失败"));
    await reason("对齐原因");
    await click("应用校正");
    expect(host.querySelector(".correction-validation strong")?.textContent).toBe("校正已应用");
    expect(host.querySelector("textarea")!.value).toBe("");
    expect(mocks.message.error).toHaveBeenCalledWith("回读失败");
    expect(mocks.message.success).not.toHaveBeenCalled();
  });
});
