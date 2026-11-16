// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
import { CaptureFileOrderPreview, captureSelectionError } from "./CaptureFileOrderPreview";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach } from "vitest";

const counts = vi.hoisted(() => ({ pdf: vi.fn(), tiff: vi.fn(), previewFailure: undefined as ((reason: string) => void) | undefined }));
vi.mock("./capturePdfPreview", () => ({ countCapturePdfPages: counts.pdf }));
vi.mock("./captureTiffPreview", () => ({ inspectCaptureTiff: async (file: File) => { const result = await counts.tiff(file); return typeof result === "number" ? { pageCount: result, previewUnavailable: "" } : result; } }));
vi.mock("./CapturePdfOrderPreview", () => ({ CapturePdfOrderPreview: ({ pageOffset, onPreviewFailureChange }: { pageOffset?: number; onPreviewFailureChange?: (reason: string) => void }) => { counts.previewFailure = onPreviewFailureChange; return <span>已核对前页：{pageOffset ?? "未知"}</span>; } }));
afterEach(() => { cleanup(); vi.clearAllMocks(); });

vi.mock("antd", () => ({ Button: ({ children, onClick, disabled, "aria-label": label }: { children: ReactNode; onClick: () => void; disabled: boolean; "aria-label": string }) => <button disabled={disabled} aria-label={label} onClick={onClick}>{children}</button>, Space: ({ children }: { children: ReactNode }) => <div>{children}</div> }));

describe("上传前页序预检", () => {
  it("单页图片需组成完整答卷，PDF/TIFF不按文件数量伪造页数", () => {
    const front = new File(["front"], "front.png");
    expect(captureSelectionError([front], 2)).toContain("还缺 1 页");
    expect(captureSelectionError([new File(["pdf"], "两页.pdf")], 2)).toBe("");
    expect(captureSelectionError([new File(["tiff"], "两页.tiff")], 2)).toBe("");
    expect(captureSelectionError([front], undefined)).toContain("请选择每人页数");
  });
  it("预检展示分组边界，排序只更新本地文件数组", async () => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    const files = [new File(["front"], "front.png"), new File(["back"], "back.png")];
    const change = vi.fn(); const host = document.createElement("div"); const root = createRoot(host);
    try {
      await act(async () => root.render(<CaptureFileOrderPreview files={files} pages={2} onChange={change} />));
      expect(host.textContent).toContain("front.png · 第 1 份答卷，第 1 页");
      expect(host.textContent).toContain("back.png · 第 1 份答卷，第 2 页");
      await act(async () => host.querySelector<HTMLButtonElement>('button[aria-label="下移文件 1"]')!.click());
      expect(change).toHaveBeenCalledExactlyOnceWith([files[1], files[0]]);
      expect(files[0].name).toBe("front.png");
    } finally { await act(async () => root.unmount()); }
  });
  it("两种多页文件共享同次分组，缺页时阻止确认，换文件后不接收旧页数", async () => {
    const pdf = new File(["pdf"], "前三页.pdf"), tiff = new File(["tiff"], "后三页.tiff");
    counts.pdf.mockResolvedValue(3); counts.tiff.mockResolvedValue(3);
    const pending = vi.fn();
    const view = render(<CaptureFileOrderPreview files={[pdf,tiff]} pages={2} onChange={vi.fn()} onPendingChange={pending} />);
    // 分组先渲染，父级阻塞状态随后由 effect 通知；两者都完成后才验证下一次选择。
    await waitFor(() => {
      expect(screen.getByLabelText("逐份答卷分组").textContent).toContain("第 2 份答卷：前三页.pdf 第 3 页、后三页.tiff 第 1 页");
      expect(pending).toHaveBeenLastCalledWith(false);
    });
    view.rerender(<CaptureFileOrderPreview files={[pdf]} pages={2} onChange={vi.fn()} onPendingChange={pending} />);
    await waitFor(() => {
      expect(screen.getByRole("alert").textContent).toContain("还缺 1 页");
      expect(pending).toHaveBeenLastCalledWith(true);
    });
    let late!: (count: number) => void;
    const old = new File(["pdf"], "旧材料.pdf"); counts.pdf.mockImplementationOnce(() => new Promise(resolve => { late = resolve; }));
    view.rerender(<CaptureFileOrderPreview files={[old]} pages={2} onChange={vi.fn()} onPendingChange={pending} />);
    view.rerender(<CaptureFileOrderPreview files={[tiff]} pages={2} onChange={vi.fn()} onPendingChange={pending} />);
    await act(async () => late(90));
    expect(screen.queryByText(/旧材料.pdf 第/)).toBeNull();
  });
  it("未知页数必须显式核对原件，重新分组或调整文件顺序后作废旧确认", async () => {
    counts.tiff.mockRejectedValue(new Error("特殊目录"));
    const file = new File(["tiff"], "特殊原件.tiff"), files = [file];
    const pending = vi.fn(), change = vi.fn();
    const view = render(<CaptureFileOrderPreview files={files} pages={2} onChange={change} onPendingChange={pending} />);
    const confirmation = await screen.findByRole("checkbox");
    await waitFor(() => expect(confirmation.hasAttribute("disabled")).toBe(false));
    expect(pending).toHaveBeenLastCalledWith(true);expect(screen.queryByLabelText("逐份答卷分组")).toBeNull();
    fireEvent.click(confirmation);expect(pending).toHaveBeenLastCalledWith(false);
    view.rerender(<CaptureFileOrderPreview files={files} pages={3} onChange={change} onPendingChange={pending} />);
    expect((screen.getByRole("checkbox") as HTMLInputElement).checked).toBe(false);expect(pending).toHaveBeenLastCalledWith(true);
    fireEvent.click(screen.getByRole("checkbox"));expect(pending).toHaveBeenLastCalledWith(false);
    view.rerender(<CaptureFileOrderPreview files={[file]} pages={3} onChange={change} onPendingChange={pending} />);
    await waitFor(() => expect((screen.getByRole("checkbox") as HTMLInputElement).checked).toBe(false));expect(pending).toHaveBeenLastCalledWith(true);
    expect(change).not.toHaveBeenCalled();
  });
  it("已知缺页不能由原件确认绕过，晚到的预览失败也会重新阻止导入", async () => {
    const file = new File(["tiff"], "三页原件.tiff"), files = [file];
    counts.tiff.mockResolvedValue({pageCount:3,previewUnavailable:"含子页面，不能本地完整预览"});
    const pending = vi.fn();
    const view = render(<CaptureFileOrderPreview files={files} pages={2} onChange={vi.fn()} onPendingChange={pending} />);
    await screen.findByRole("checkbox");await waitFor(() => expect((screen.getByRole("checkbox") as HTMLInputElement).disabled).toBe(false));
    fireEvent.click(screen.getByRole("checkbox"));expect(pending).toHaveBeenLastCalledWith(true);
    view.rerender(<CaptureFileOrderPreview files={files} pages={3} onChange={vi.fn()} onPendingChange={pending} />);
    fireEvent.click(screen.getByRole("checkbox"));expect(pending).toHaveBeenLastCalledWith(false);
    await act(async () => counts.previewFailure?.("解码器无法显示全部页面"));
    expect((screen.getByRole("checkbox") as HTMLInputElement).checked).toBe(false);expect(pending).toHaveBeenLastCalledWith(true);
  });
  it("大于100页仍展示真实页数和分组，但需核对无法完整预览的原件", async () => {
    counts.pdf.mockResolvedValue(202);const pending = vi.fn();
    render(<CaptureFileOrderPreview files={[new File(["pdf"], "长答卷.pdf")]} pages={2} onChange={vi.fn()} onPendingChange={pending} />);
    await screen.findByText("预计 101 份答卷，共 202 页");
    expect(screen.getByLabelText("逐份答卷分组").children).toHaveLength(100);
    expect(screen.getByLabelText("逐份答卷分组").closest("details")).toBeNull();
    expect(screen.getByLabelText("答卷分组预览").hidden).toBe(false);
    expect(pending).toHaveBeenLastCalledWith(true);
    fireEvent.click(screen.getByRole("checkbox"));expect(pending).toHaveBeenLastCalledWith(false);
  });
});
