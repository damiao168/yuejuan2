// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { CaptureBatchDetail } from "../../api/capture";
import { useCaptureUpload } from "./useCaptureUpload";

const mocks = vi.hoisted(() => ({ upload: vi.fn(), register: vi.fn(), load: vi.fn(), success: vi.fn(), error: vi.fn(), info: vi.fn() }));
vi.mock("antd", () => ({ App: { useApp: () => ({ message: { success: mocks.success, error: mocks.error, info: mocks.info } }) } }));
vi.mock("../../api/files", () => ({ uploadFile: mocks.upload }));
vi.mock("../../api/capture", () => ({ registerCaptureFile: mocks.register }));
vi.mock("../../api/client", () => ({ getUserErrorMessage: (error: unknown, fallback: string) => error instanceof Error ? error.message : fallback }));

const front = new File(["front"], "front.png", { type: "image/png" });
const back = new File(["back"], "back.png", { type: "image/png" });
const detail = { batch: { id: "batch-1" } } as CaptureBatchDetail;

describe("学生答卷上传分组", () => {
  let root: Root;
  let container: HTMLDivElement;
  let current: ReturnType<typeof useCaptureUpload>;
  function Probe() { current = useCaptureUpload(detail, "exam-1", mocks.load, "batch-1"); return null; }
  beforeEach(async () => {
    vi.resetAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.upload.mockImplementation(async (file: File) => ({ file: { id: `asset-${file.name}` } }));
    mocks.register.mockImplementation(async (_batch, asset: string) => ({ file: { id: `registered-${asset}` } })); mocks.load.mockResolvedValue(undefined);
    container = document.createElement("div"); document.body.appendChild(container); root = createRoot(container);
    await act(async () => root.render(<Probe />));
  });
  afterEach(async () => { await act(async () => root.unmount()); container.remove(); });

  it("两张PNG按原选择顺序上传与登记，组成同一学生的两页答卷", async () => {
    const events: string[] = [];
    mocks.upload.mockImplementation(async (file: File) => { events.push(`upload:${file.name}`); return { file: { id: `asset-${file.name}` } }; });
    mocks.register.mockImplementation(async (_batch, asset: string) => { events.push(`register:${asset}`); return { file: { id: `registered-${asset}` } }; });
    await act(async () => current.uploadSources([front, back], 2));
    expect(events).toEqual(["upload:front.png", "register:asset-front.png", "upload:back.png", "register:asset-back.png"]);
    const [first, second] = mocks.register.mock.calls;
    expect(first[3]).toEqual({ upload_group_id: expect.any(String), upload_group_file_count: 2, upload_group_file_index: 1, pages_per_student: 2 });
    expect(second[3]).toEqual({ ...first[3], upload_group_file_index: 2 });
    expect(current.pendingUpload).toBeUndefined();
    expect(current.uploading).toBe(false);
  });

  it("两次文件选择使用不同组号，不能跨选择凑页", async () => {
    await act(async () => current.uploadSources([front, back], 2));
    await act(async () => current.uploadSources([front, back], 2));
    expect(mocks.register.mock.calls[0][3].upload_group_id).not.toBe(mocks.register.mock.calls[2][3].upload_group_id);
    expect(mocks.register.mock.calls[2][3].upload_group_file_index).toBe(1);
  });

  it.each([0, 21, 1.5, Number.NaN])("页数%s不合法时不上传", async pages => {
    await act(async () => current.uploadSources([front, back], pages));
    expect(mocks.upload).not.toHaveBeenCalled();
  });

  it("单页图片数量不足整份答卷时在上传前拒绝", async () => {
    await act(async () => current.uploadSources([front], 2));
    expect(mocks.upload).not.toHaveBeenCalled();
    expect(mocks.error).toHaveBeenCalledWith(expect.stringContaining("缺少 1 页"));
    expect(current.pendingUpload).toBeUndefined();
  });

  it.each([["sheet.pdf", "application/pdf"], ["sheet.tiff", "image/tiff"]])("%s按解码页数交给服务端校验，不能把文件数量当页数", async (name, type) => {
    const file = new File(["multipage"], name, { type });
    await act(async () => current.uploadSources([file], 2));
    expect(mocks.register).toHaveBeenCalledWith("batch-1", `asset-${name}`, expect.any(String), expect.objectContaining({ upload_group_file_count: 1, pages_per_student: 2 }));
  });

  it("登记失败后保留asset、组号和命令键，同组重试后才继续下一文件", async () => {
    mocks.register.mockRejectedValueOnce(new Error("登记连接中断"));
    await act(async () => current.uploadSources([front, back], 2));
    const firstCall = [...mocks.register.mock.calls[0]];
    expect(current.pendingUpload).toMatchObject({ completedFiles: 0, totalFiles: 2, error: "登记连接中断" });
    expect(current.uploading).toBe(false);
    await act(async () => current.uploadSources([front, back], 2));
    expect(mocks.upload).toHaveBeenCalledTimes(1);
    await act(async () => current.retryUpload());
    expect(mocks.register.mock.calls[1]).toEqual(firstCall);
    expect(mocks.upload).toHaveBeenCalledTimes(2);
    expect(mocks.register.mock.calls[2][3]).toEqual({ ...firstCall[3], upload_group_file_index: 2 });
    expect(current.pendingUpload).toBeUndefined();
  });

  it("第二文件上传失败后不重传首文件，结束失败选择后新组独立", async () => {
    mocks.upload.mockResolvedValueOnce({ file: { id: "asset-front" } }).mockRejectedValueOnce(new Error("上传中断"));
    await act(async () => current.uploadSources([front, back], 2));
    expect(current.pendingUpload?.completedFiles).toBe(1);
    const oldGroup = mocks.register.mock.calls[0][3].upload_group_id;
    await act(async () => current.discardUpload());
    await act(async () => current.uploadSources([front, back], 2));
    expect(mocks.register.mock.calls[1][3].upload_group_id).not.toBe(oldGroup);
    expect(mocks.register.mock.calls[1][3].upload_group_file_index).toBe(1);
  });

  it("刷新失败也释放上传锁，成功登记的选择不再提供重复重试", async () => {
    mocks.load.mockRejectedValueOnce(new Error("刷新失败"));
    await act(async () => current.uploadSources([front, back], 2));
    expect(current.uploading).toBe(false);
    expect(current.pendingUpload).toBeUndefined();
    await act(async () => current.uploadSources([front, back], 2));
    expect(mocks.upload).toHaveBeenCalledTimes(4);
  });

  it("首文件未返回前，同一时刻的第二次选择不能启动", async () => {
    let finish!: (value: { file: { id: string } }) => void;
    mocks.upload.mockImplementationOnce(() => new Promise(resolve => { finish = resolve; }));
    let pending!: Promise<string[]>;
    await act(async () => { pending = current.uploadSources([front, back], 2); await current.uploadSources([front, back], 2); });
    expect(mocks.upload).toHaveBeenCalledTimes(1);
    await act(async () => { finish({ file: { id: "asset-front" } }); await pending; });
    expect(mocks.register).toHaveBeenCalledTimes(2);
  });

  it("替代选择完整登记后才绑定旧组，并返回按选择顺序排列的文件ID", async () => {
    const replace = vi.fn().mockResolvedValue(undefined);
    let ids: string[] = [];
    await act(async () => { ids = await current.uploadSources([front, back], 2, replace); });
    expect(ids).toEqual(["registered-asset-front.png", "registered-asset-back.png"]);
    expect(replace).toHaveBeenCalledExactlyOnceWith(ids);
    expect(mocks.register.mock.invocationCallOrder[1]).toBeLessThan(replace.mock.invocationCallOrder[0]);
  });

  it("替代绑定失败保留全部已登记ID，重试只重新绑定而不重传或登记", async () => {
    const replace = vi.fn().mockRejectedValueOnce(new Error("替代结果中断")).mockResolvedValue(undefined);
    await act(async () => { expect(await current.uploadSources([front, back], 2, replace)).toEqual([]); });
    expect(current.pendingUpload).toMatchObject({ completedFiles: 2, totalFiles: 2, error: "替代结果中断" });
    await act(async () => current.retryUpload());
    expect(mocks.upload).toHaveBeenCalledTimes(2);
    expect(mocks.register).toHaveBeenCalledTimes(2);
    expect(replace.mock.calls[1]).toEqual(replace.mock.calls[0]);
    expect(current.pendingUpload).toBeUndefined();
  });

  it("替代文件只登记一部分时不退出旧组", async () => {
    const replace = vi.fn();
    mocks.upload.mockResolvedValueOnce({ file: { id: "first" } }).mockRejectedValueOnce(new Error("第二文件失败"));
    await act(async () => { await current.uploadSources([front, back], 2, replace); });
    expect(replace).not.toHaveBeenCalled();
    expect(current.pendingUpload?.completedFiles).toBe(1);
  });
});
