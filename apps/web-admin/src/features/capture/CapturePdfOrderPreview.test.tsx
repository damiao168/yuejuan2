// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CapturePdfOrderPreview } from "./CapturePdfOrderPreview";

const api = vi.hoisted(() => ({ preview: vi.fn(), reorder: vi.fn(), tiffPreview: vi.fn(), tiffReorder: vi.fn() }));
vi.mock("./capturePdfPreview", () => ({ previewCapturePdf: api.preview, reorderCapturePdf: api.reorder }));
vi.mock("./captureTiffPreview", () => ({ previewCaptureTiff: api.tiffPreview, reorderCaptureTiff: api.tiffReorder }));
vi.mock("antd", () => ({ Button: ({ children, disabled, onClick }: { children: ReactNode; disabled?: boolean; onClick: () => void }) => <button disabled={disabled} onClick={onClick}>{children}</button>, Space: ({ children }: { children: ReactNode }) => <div>{children}</div> }));
afterEach(() => { cleanup(); vi.resetAllMocks(); });
const file = new File(["pdf"], "合成答卷.pdf");
describe("PDF预检提交边界", () => {
  it("TIFF沿用完整页序确认，应用成功前不替换原件", async () => {
    api.tiffPreview.mockImplementation(async (_file, _signal, onPage) => { onPage("data:image/jpeg;base64,YQ==", 2); onPage("data:image/jpeg;base64,Yg==", 2); });
    const source = new File(["tiff"], "合成答卷.tiff", { type: "image/tiff" }); const output = new File(["reordered"], "页序已调整.tiff");
    const change = vi.fn(); const pending = vi.fn(); api.tiffReorder.mockRejectedValueOnce(new Error("原件数据不完整"));
    render(<CapturePdfOrderPreview file={source} disabled={false} onChange={change} onPendingChange={pending} />);
    await waitFor(() => expect((screen.getAllByRole("button", { name: "下移此页" })[0] as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(screen.getAllByRole("button", { name: "下移此页" })[0]); expect(pending).toHaveBeenLastCalledWith(true);
    fireEvent.click(screen.getByRole("button", { name: "应用TIFF页序" })); await screen.findByRole("alert"); expect(change).not.toHaveBeenCalled();
    api.tiffReorder.mockResolvedValueOnce(output); fireEvent.click(screen.getByRole("button", { name: "应用TIFF页序" }));
    await waitFor(() => expect(change).toHaveBeenCalledExactlyOnceWith(output)); expect(api.tiffReorder).toHaveBeenLastCalledWith(source, [1, 0]); expect(api.reorder).not.toHaveBeenCalled();
  });
  it("修改页序先阻止外层导入，应用生成后才替换本地文件", async () => {
    api.preview.mockImplementation(async (_file, _signal, onPage) => { onPage("data:image/jpeg;base64,YQ==", 2); onPage("data:image/jpeg;base64,Yg==", 2); });
    const output = new File(["reordered"], "页序已调整.pdf"); api.reorder.mockResolvedValue(output);
    const change = vi.fn(); const pending = vi.fn();
    render(<CapturePdfOrderPreview file={file} disabled={false} onChange={change} onPendingChange={pending} />);
    await waitFor(() => expect((screen.getAllByRole("button", { name: "下移此页" })[0] as HTMLButtonElement).disabled).toBe(false));
    fireEvent.click(screen.getAllByRole("button", { name: "下移此页" })[0]);
    expect(pending).toHaveBeenLastCalledWith(true); expect(change).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "应用PDF页序" }));
    await waitFor(() => expect(change).toHaveBeenCalledExactlyOnceWith(output));
    expect(api.reorder).toHaveBeenCalledWith(file, [1, 0]);
  });
  it("关闭后取消本地解码，不接受迟到页面", async () => {
    let signal!: AbortSignal; let page!: (image: string, count: number) => void;
    api.preview.mockImplementation((_file, nextSignal, nextPage) => { signal = nextSignal; page = nextPage; return new Promise(() => {}); });
    const view = render(<CapturePdfOrderPreview file={file} disabled={false} onChange={vi.fn()} onPendingChange={vi.fn()} />);
    view.unmount();
    expect(signal.aborted).toBe(true); await act(async () => page("late", 1));
    expect(screen.queryByAltText("原文件第 1 页预览")).toBeNull();
  });
  it("自动预览没有折叠入口，换文件取消旧解码并忽略迟到页面",async()=>{
    let signal!:AbortSignal,page!:(image:string,count:number)=>void;
    api.preview.mockImplementationOnce((_file,nextSignal,nextPage)=>{signal=nextSignal;page=nextPage;return new Promise(()=>{});});
    api.preview.mockImplementationOnce(async(_file,_signal,onPage)=>{onPage("new-page",1);});
    const pending=vi.fn();const view=render(<CapturePdfOrderPreview file={file} disabled={false} onChange={vi.fn()} onPendingChange={pending}/>);
    expect(pending).toHaveBeenLastCalledWith(true);
    expect(screen.queryByRole("button",{name:/查看PDF|收起PDF/})).toBeNull();
    const next=new File(["new"],"新答卷.pdf");view.rerender(<CapturePdfOrderPreview file={next} disabled={false} onChange={vi.fn()} onPendingChange={pending}/>);
    expect(signal.aborted).toBe(true);await act(async()=>page("late",1));
    await waitFor(()=>expect(pending).toHaveBeenLastCalledWith(false));
    expect(screen.getByAltText("原文件第 1 页预览").getAttribute("src")).toBe("new-page");
  });
  it("解码失败告知外层改用原件确认，读取中阻止导入且不替换原文件",async()=>{
    let fail!:(reason:Error)=>void;api.preview.mockReturnValue(new Promise((_resolve,reject)=>{fail=reject;}));
    const pending=vi.fn(),fallback=vi.fn(),change=vi.fn();
    render(<CapturePdfOrderPreview file={file} disabled={false} onChange={change} onPendingChange={pending} onPreviewFailureChange={fallback}/>);
    expect(pending).toHaveBeenLastCalledWith(true);
    await act(async()=>fail(new Error("此文件含暂不能显示的特殊编码")));
    expect(fallback).toHaveBeenLastCalledWith("此文件含暂不能显示的特殊编码");expect(pending).toHaveBeenLastCalledWith(false);expect(change).not.toHaveBeenCalled();
    expect(screen.queryByRole("button",{name:"应用PDF页序"})).toBeNull();
  });
});
