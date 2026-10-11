// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useUnsavedChangesGuard } from "./useUnsavedChangesGuard";

const mocks = vi.hoisted(() => ({ block: vi.fn(), confirm: vi.fn(), warning: vi.fn(), unblock: vi.fn() }));
vi.mock("@tanstack/react-router", () => ({ useRouter: () => ({ history: { block: mocks.block } }) }));
vi.mock("antd", () => ({ App: { useApp: () => ({ modal: { confirm: mocks.confirm }, message: { warning: mocks.warning } }) } }));

function Guard({ dirty, busy = false }: { dirty: boolean; busy?: boolean }) {
  useUnsavedChangesGuard({ dirty, busy, description: "仅当前页面保留未保存内容" });
  return null;
}

describe("未保存内容的导航保护", () => {
  let root: Root;
  let host: HTMLDivElement;
  const transition = { currentLocation: { href: "/teacher/appeals" }, nextLocation: { href: "/teacher/dashboard" } };
  beforeEach(() => {
    vi.clearAllMocks();
    mocks.block.mockReturnValue(mocks.unblock);
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    host = document.createElement("div");
    root = createRoot(host);
  });
  afterEach(async () => { await act(async () => root.unmount()); });

  it("取消离开保留页面，明确放弃后才允许导航", async () => {
    await act(async () => root.render(<Guard dirty />));
    const blocker = mocks.block.mock.calls[mocks.block.mock.calls.length - 1][0].blockerFn;
    const cancelled = blocker(transition);
    mocks.confirm.mock.calls[mocks.confirm.mock.calls.length - 1][0].onCancel();
    expect(await cancelled).toBe(true);
    const discarded = blocker(transition);
    mocks.confirm.mock.calls[mocks.confirm.mock.calls.length - 1][0].onOk();
    expect(await discarded).toBe(false);
  });

  it("保存完成后读取最新状态，提交中阻止离开", async () => {
    await act(async () => root.render(<Guard dirty />));
    const blocker = mocks.block.mock.calls[mocks.block.mock.calls.length - 1][0].blockerFn;
    await act(async () => root.render(<Guard dirty={false} />));
    expect(blocker(transition)).toBe(false);
    expect(mocks.confirm).not.toHaveBeenCalled();
    await act(async () => root.render(<Guard dirty={false} busy />));
    expect(blocker(transition)).toBe(true);
    expect(mocks.warning).toHaveBeenCalled();
  });

  it("未保存时取消卸载并提供属性返回值，保存后移除保护", async () => {
    const previous = window.onbeforeunload;
    await act(async () => root.render(<Guard dirty />));
    const unsaved = new Event("beforeunload", { cancelable: true });
    expect(window.onbeforeunload?.call(window, unsaved as BeforeUnloadEvent)).toBe("unsaved");
    window.dispatchEvent(unsaved);
    expect(unsaved.defaultPrevented).toBe(true);
    await act(async () => root.render(<Guard dirty={false} />));
    expect(window.onbeforeunload).toBe(previous);
    const saved = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(saved);
    expect(saved.defaultPrevented).toBe(false);
  });

  it("仅保存中也取消卸载，dirty 与 busy 都结束后恢复", async () => {
    await act(async () => root.render(<Guard dirty={false} busy />));
    const busy = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(busy);
    expect(busy.defaultPrevented).toBe(true);
    expect(window.onbeforeunload?.call(window, new Event("beforeunload", { cancelable: true }) as BeforeUnloadEvent)).toBe("unsaved");
    await act(async () => root.render(<Guard dirty={false} />));
    const idle = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(idle);
    expect(idle.defaultPrevented).toBe(false);
  });

  it("先卸载较早的实例后，另一个实例继续保护直到自身保存并卸载", async () => {
    const previous = window.onbeforeunload;
    await act(async () => root.render(<><Guard key="first" dirty /><Guard key="second" dirty={false} busy /></>));
    const wrapper = window.onbeforeunload;
    await act(async () => root.render(<><Guard key="second" dirty={false} busy /></>));
    expect(window.onbeforeunload).toBe(wrapper);
    const remaining = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(remaining);
    expect(remaining.defaultPrevented).toBe(true);
    await act(async () => root.render(<><Guard key="second" dirty={false} /></>));
    expect(window.onbeforeunload).toBe(previous);
    const saved = new Event("beforeunload", { cancelable: true });
    window.dispatchEvent(saved);
    expect(saved.defaultPrevented).toBe(false);
    await act(async () => root.render(<><Guard key="second" dirty /></>));
    await act(async () => root.render(null));
    expect(window.onbeforeunload).toBe(previous);
  });
});
