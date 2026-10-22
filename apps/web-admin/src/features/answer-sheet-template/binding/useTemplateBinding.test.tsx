// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { answerTemplate, templateBinding } from "../testing/answerSheetFixtures";
import { useTemplateBinding } from "./useTemplateBinding";

const mocks = vi.hoisted(() => ({ bind: vi.fn(), unbind: vi.fn(), changed: vi.fn(), confirm: vi.fn(), success: vi.fn(), error: vi.fn() }));
vi.mock("../../../api/configuration", () => ({ bindExamTemplate: mocks.bind, unbindExamTemplate: mocks.unbind }));
vi.mock("antd", () => ({ App: { useApp: () => ({ message: { success: mocks.success, error: mocks.error }, modal: { confirm: mocks.confirm } }) } }));

describe("本场模板明确绑定后的准备状态刷新", () => {
  let root: Root;
  let host: HTMLDivElement;
  let current: ReturnType<typeof useTemplateBinding>;
  const locked = { ...answerTemplate, status: "locked" as const };
  beforeEach(() => {
    vi.resetAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    host = document.createElement("div"); document.body.appendChild(host); root = createRoot(host);
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); });
  function Probe({ examId, canEdit }: { examId: string; canEdit: boolean }) {
    current = useTemplateBinding(examId, mocks.changed, canEdit); return null;
  }
  async function mount(examId = "exam", canEdit = true, loadState = true) {
    await act(async () => root.render(<Probe examId={examId} canEdit={canEdit} />));
    if (loadState && current.bindingRevision === undefined) {
      await act(async () => current.setBindingState({ binding: null, revision: 0 }));
    }
  }

  it("确认已锁定版本绑定成功后刷新准备检查，初次挂载不触发刷新", async () => {
    mocks.bind.mockResolvedValue({ binding: templateBinding("locked_with_guard", "template"), revision: 1 });
    await mount();
    expect(mocks.changed).not.toHaveBeenCalled();
    act(() => current.bindTemplateForExam(locked));
    await act(async () => mocks.confirm.mock.calls[0][0].onOk());
    expect(mocks.bind).toHaveBeenCalledWith("exam", "template", 0);
    expect(current.examBinding?.mode).toBe("locked_with_guard");
    expect(mocks.changed).toHaveBeenCalledOnce();
  });

  it("绑定失败保留未确认状态，不通知完成", async () => {
    mocks.bind.mockRejectedValue(new Error("offline"));
    await mount(); act(() => current.bindTemplateForExam(locked));
    await act(async () => mocks.confirm.mock.calls[0][0].onOk());
    expect(current.examBinding).toBeNull();
    expect(mocks.changed).not.toHaveBeenCalled();
    expect(mocks.error).toHaveBeenCalledOnce();
  });

  it("保存锁定活化链路由生命周期通知，直接活化不重复触发刷新", async () => {
    mocks.bind.mockResolvedValue({ binding: templateBinding("locked_with_guard", "template"), revision: 1 });
    await mount();
    await act(async () => current.activateTemplate(locked));
    expect(current.examBinding?.template_id).toBe("template");
    expect(mocks.changed).not.toHaveBeenCalled();
  });

  it("解除绑定成功后刷新真实状态", async () => {
    mocks.unbind.mockResolvedValue({ binding: templateBinding("locked_with_guard", "template"), revision: 2 }); await mount();
    await act(async () => current.setBindingState({ binding: templateBinding("locked_with_guard", "template"), revision: 1 }));
    act(() => current.releaseExamBinding());
    await act(async () => mocks.confirm.mock.calls[0][0].onOk());
    expect(current.examBinding).toBeNull();
    expect(current.bindingRevision).toBe(2);
    expect(mocks.changed).toHaveBeenCalledOnce();
  });

  it.each(["permission", "exam"])("旧确认窗口在 %s 改变后，即使恢复原界面也不能提交", async boundary => {
    await mount(); act(() => current.bindTemplateForExam(locked));
    const oldConfirm = mocks.confirm.mock.calls[0][0].onOk;
    if (boundary === "permission") { await mount("exam", false); await mount("exam", true); }
    else { await mount("other"); await mount("exam"); }
    await act(async () => oldConfirm());
    expect(mocks.bind).not.toHaveBeenCalled();
    expect(mocks.changed).not.toHaveBeenCalled();
  });

  it("连续确认只提交一次，写入响应到达新考试时不回填或通知完成", async () => {
    let finish: (response: { binding: ReturnType<typeof templateBinding>; revision: number }) => void = () => {};
    mocks.bind.mockReturnValue(new Promise(resolve => { finish = resolve; }));
    await mount(); act(() => current.bindTemplateForExam(locked));
    const onOk = mocks.confirm.mock.calls[0][0].onOk;
    let saving: Promise<void> = Promise.resolve();
    await act(async () => { saving = onOk(); await onOk(); });
    expect(mocks.bind).toHaveBeenCalledOnce();
    await mount("other");
    await act(async () => { finish({ binding: templateBinding("locked_with_guard", "template"), revision: 1 }); await saving; });
    expect(current.examBinding).toBeNull();
    expect(current.bindingBusy).toBe(false);
    expect(mocks.changed).not.toHaveBeenCalled();
    expect(mocks.success).not.toHaveBeenCalled();
  });

  it("旧解除窗口在权限被收回后不能解除绑定", async () => {
    await mount();
    await act(async () => current.setBindingState({ binding: templateBinding("locked_with_guard", "template"), revision: 1 }));
    act(() => current.releaseExamBinding());
    const onOk = mocks.confirm.mock.calls[0][0].onOk;
    await mount("exam", false);
    await act(async () => onOk());
    expect(mocks.unbind).not.toHaveBeenCalled();
  });

  it("重新打开已解绑考试时，用读取到的空绑定游标合法重建", async () => {
    await mount();
    await act(async () => current.setBindingState({ binding: null, revision: 2 }));
    const rebound = { ...templateBinding("locked_with_guard", "template"), revision: 3 };
    mocks.bind.mockResolvedValue({ binding: rebound, revision: 3 });
    act(() => current.bindTemplateForExam(locked));
    await act(async () => mocks.confirm.mock.calls[0][0].onOk());
    expect(mocks.bind).toHaveBeenCalledWith("exam", "template", 2);
    expect(current.examBinding).toEqual(rebound);
    expect(current.bindingRevision).toBe(3);
  });

  it("本页解绑后直接重建也沿用新游标，迟到读取不能回退到零或旧绑定", async () => {
    await mount();
    const original = templateBinding("locked_with_guard", "template");
    await act(async () => current.setBindingState({ binding: original, revision: 1 }));
    mocks.unbind.mockResolvedValue({ binding: original, revision: 2 });
    act(() => current.releaseExamBinding());
    await act(async () => mocks.confirm.mock.calls[0][0].onOk());
    await act(async () => current.setBindingState({ binding: original, revision: 1 }));
    expect(current.examBinding).toBeNull();
    const rebound = { ...original, revision: 3 };
    mocks.bind.mockResolvedValue({ binding: rebound, revision: 3 });
    await act(async () => current.activateTemplate(locked));
    expect(mocks.bind).toHaveBeenCalledWith("exam", "template", 2);
    await act(async () => {
      current.setBindingState({ binding: null, revision: 0 });
      current.setBindingState({ binding: null, revision: 2 });
    });
    expect(current.examBinding).toEqual(rebound);
    expect(current.bindingRevision).toBe(3);
  });

  it("尚未读取游标时禁止写入，旧确认框也不能覆盖后来读取的新版本", async () => {
    await mount("exam", true, false);
    await act(async () => {
      await expect(current.activateTemplate(locked)).rejects.toThrow("尚未读取");
    });
    expect(mocks.bind).not.toHaveBeenCalled();
    await act(async () => current.setBindingState({ binding: null, revision: 0 }));
    act(() => current.bindTemplateForExam(locked));
    await act(async () => current.setBindingState({ binding: null, revision: 2 }));
    await act(async () => mocks.confirm.mock.calls[0][0].onOk());
    expect(mocks.bind).not.toHaveBeenCalled();
    expect(mocks.error).toHaveBeenCalledOnce();
    expect(current.bindingRevision).toBe(2);
  });
});
