// @vitest-environment jsdom
import { act, type ReactNode } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { SessionUser } from "../../auth/session";
import { ExamCapturePage } from "./ExamCapturePage";

const mocks = vi.hoisted(() => ({
  workspace: vi.fn(), startCollection: vi.fn(), refetch: vi.fn(),
  confirm: vi.fn(), destroy: vi.fn(), success: vi.fn(), error: vi.fn(), navigate: vi.fn()
}));

vi.mock("../../api/configuration", () => ({ startExamCollection: mocks.startCollection }));
vi.mock("../../query/examWorkspace", () => ({ useExamWorkspace: mocks.workspace }));
vi.mock("../../pages/CaptureBatchPage", async () => {
  const React = await import("react");
  return {
    CaptureBatchPage: ({ examId, canManage }: { examId: string; canManage: boolean }) =>
      React.createElement("div", { "data-capture-exam": examId, "data-capture-manage": String(canManage) }, "批次入口")
  };
});
vi.mock("../../components/PageState", async () => {
  const React = await import("react");
  return {
    LoadingState: ({ label }: { label: string }) => React.createElement("div", null, label),
    ErrorState: ({ message }: { message: string }) => React.createElement("div", { "data-error-state": true }, message)
  };
});
vi.mock("antd", async () => {
  const React = await import("react");
  return {
    App: { useApp: () => ({ message: { success: mocks.success, error: mocks.error }, modal: { confirm: mocks.confirm } }) },
    Button: ({ children, onClick, disabled }: { children: ReactNode; onClick?: () => void; disabled?: boolean }) =>
      React.createElement("button", { onClick, disabled }, children),
    Space: ({ children }: { children: ReactNode }) => React.createElement("div", null, children)
  };
});

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}

type PageProps = Parameters<typeof ExamCapturePage>[0];
type StartConfirmation = { onOk: () => Promise<void> };

describe("答卷导入状态边界", () => {
  let root: Root | null;
  let container: HTMLDivElement;
  let props: PageProps;

  function workspaceStatus(status: string) {
    mocks.workspace.mockReturnValue({ data: { exam_status: status }, error: null, refetch: mocks.refetch });
  }

  beforeEach(() => {
    vi.resetAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.confirm.mockReturnValue({ destroy: mocks.destroy });
    mocks.startCollection.mockResolvedValue({});
    mocks.refetch.mockResolvedValue({});
    workspaceStatus("ready");
    props = {
      user: { id: "teacher", tenant: "school" } as SessionUser,
      examId: "exam-a", canManage: true, canStart: true, canReview: true, onNavigate: mocks.navigate
    };
    container = document.createElement("div");
    document.body.appendChild(container);
    root = createRoot(container);
  });

  afterEach(async () => {
    await act(async () => { root?.unmount(); });
    container.remove();
  });

  async function renderPage(next: Partial<PageProps> = {}) {
    props = { ...props, ...next };
    await act(async () => {
      root!.render(<ExamCapturePage key={`${props.user.tenant}:${props.user.id}:${props.examId}`} {...props} />);
    });
  }

  function button(label: string) {
    const target = [...container.querySelectorAll<HTMLButtonElement>("button")].find((item) => item.textContent?.trim() === label);
    expect(target).toBeDefined();
    return target!;
  }

  async function openStartDialog() {
    await act(async () => { button("开始导入答卷").click(); });
    expect(mocks.confirm).toHaveBeenCalledOnce();
    return mocks.confirm.mock.calls[0][0] as StartConfirmation;
  }

  it("ready 状态在开始请求完成并回读 collecting 前不能进入批次入口", async () => {
    const started = deferred<void>();
    const refreshed = deferred<void>();
    mocks.startCollection.mockReturnValue(started.promise);
    mocks.refetch.mockImplementation(async () => {
      await refreshed.promise;
      workspaceStatus("collecting");
    });
    await renderPage();
    expect(container.querySelector("[data-capture-exam]")).toBeNull();
    const confirmation = await openStartDialog();
    expect(mocks.startCollection).not.toHaveBeenCalled();

    let completion!: Promise<void>;
    await act(async () => { completion = confirmation.onOk(); });
    expect(mocks.startCollection).toHaveBeenCalledOnce();
    expect(mocks.startCollection).toHaveBeenCalledWith("exam-a");
    expect(mocks.refetch).not.toHaveBeenCalled();
    expect(container.querySelector("[data-capture-exam]")).toBeNull();

    await act(async () => { started.resolve(undefined); });
    expect(mocks.refetch).toHaveBeenCalledOnce();
    expect(container.querySelector("[data-capture-exam]")).toBeNull();
    await act(async () => { refreshed.resolve(undefined); await completion; });
    await renderPage();
    expect(container.querySelector("[data-capture-exam]")?.getAttribute("data-capture-exam")).toBe("exam-a");
  });

  it.each([true, false])("collecting 状态的批次写入权限由 canManage=%s 决定", async (canManage) => {
    workspaceStatus("collecting");
    await renderPage({ canManage });
    expect(container.querySelector("[data-capture-exam]")?.getAttribute("data-capture-manage")).toBe(String(canManage));
    await act(async () => { button("前往阅卷").click(); });
    expect(mocks.navigate).toHaveBeenCalledWith("/exams/exam-a/grading");
  });

  it.each([
    { status: "grading", hasReviewEntry: true, canSupplement: true },
    { status: "reviewing", hasReviewEntry: true, canSupplement: true },
    { status: "finalized", hasReviewEntry: false, canSupplement: false },
    { status: "published", hasReviewEntry: false, canSupplement: false },
    { status: "archived", hasReviewEntry: false, canSupplement: false }
  ])("$status 按阶段开放补传和阅卷入口", async ({ status, hasReviewEntry, canSupplement }) => {
    workspaceStatus(status);
    await renderPage();
    expect(container.querySelector("[data-capture-exam]")?.getAttribute("data-capture-manage")).toBe(String(canSupplement));
    expect([...container.querySelectorAll("button")].some((item) => item.textContent?.trim() === "前往阅卷")).toBe(hasReviewEntry);
  });

  it("未知状态不能开放批次入口", async () => {
    workspaceStatus("unknown");
    await renderPage();
    expect(container.querySelector("[data-error-state]")).not.toBeNull();
    expect(container.querySelector("[data-capture-exam]")).toBeNull();
    expect(mocks.startCollection).not.toHaveBeenCalled();
  });

  it.each(["draft", "configured"])("%s 状态必须回到考试准备", async (status) => {
    workspaceStatus(status);
    await renderPage({ examId: "exam/a" });
    expect(container.querySelector("[data-capture-exam]")).toBeNull();
    expect([...container.querySelectorAll("button")].some((item) => item.textContent === "开始导入答卷")).toBe(false);
    await act(async () => { button("检查考试准备").click(); });
    expect(mocks.navigate).toHaveBeenCalledWith("/exams/exam%2Fa/settings");
    expect(mocks.startCollection).not.toHaveBeenCalled();
  });

  it("没有开始权限时不能提交开始请求", async () => {
    await renderPage({ canStart: false });
    expect(button("开始导入答卷").disabled).toBe(true);
    await act(async () => { button("开始导入答卷").click(); });
    expect(mocks.confirm).not.toHaveBeenCalled();
    expect(mocks.startCollection).not.toHaveBeenCalled();
    expect(container.querySelector("[data-capture-exam]")).toBeNull();
  });

  it.each(["开始权限被收回", "考试已进入 collecting"])("确认框打开后%s，旧确认不能提交请求", async (change) => {
    // 同一考试的权限或状态变化不会重挂载页面；已打开的确认框必须使用最新边界。
    await renderPage();
    const confirmation = await openStartDialog();
    if (change === "开始权限被收回") {
      await renderPage({ canStart: false });
    } else {
      workspaceStatus("collecting");
      await renderPage();
    }
    await act(async () => { await confirmation.onOk(); });
    expect(mocks.startCollection).not.toHaveBeenCalled();
    expect(mocks.refetch).not.toHaveBeenCalled();
  });

  it.each(["切换考试", "卸载页面"])("%s 后销毁确认框，旧确认回调不能提交请求", async (leave) => {
    // 路由通过 key 重挂载考试页面；旧确认回调即使稍后被调用，也不能再提交旧考试。
    await renderPage();
    const confirmation = await openStartDialog();
    if (leave === "切换考试") {
      await renderPage({ examId: "exam-b" });
    } else {
      await act(async () => { root!.unmount(); root = null; });
    }
    expect(mocks.destroy).toHaveBeenCalledOnce();
    await act(async () => { await confirmation.onOk(); });
    expect(mocks.startCollection).not.toHaveBeenCalled();
    expect(mocks.refetch).not.toHaveBeenCalled();
  });

  it("开始请求失败后先回读状态，并接受服务端已进入 collecting 的结果", async () => {
    // 服务端可能已完成状态切换，而客户端只收到失败；回读结果决定是否开放批次入口。
    const refreshed = deferred<void>();
    mocks.startCollection.mockRejectedValueOnce(new Error("connection lost"));
    mocks.refetch.mockImplementation(async () => {
      await refreshed.promise;
      workspaceStatus("collecting");
    });
    await renderPage();
    const confirmation = await openStartDialog();
    let completion!: Promise<void>;
    await act(async () => { completion = confirmation.onOk(); });
    expect(mocks.refetch).toHaveBeenCalledOnce();
    expect(mocks.error).not.toHaveBeenCalled();
    expect(container.querySelector("[data-capture-exam]")).toBeNull();

    await act(async () => { refreshed.resolve(undefined); await completion; });
    await renderPage();
    expect(container.querySelector("[data-capture-exam]")).not.toBeNull();
    expect(mocks.error).toHaveBeenCalledOnce();
    expect(mocks.success).not.toHaveBeenCalled();
  });

  it("开始请求和恢复回读都失败时保留错误反馈，并且不能开放批次入口", async () => {
    mocks.startCollection.mockRejectedValueOnce(new Error("connection lost"));
    mocks.refetch.mockRejectedValueOnce(new Error("workspace unavailable"));
    await renderPage();
    const confirmation = await openStartDialog();
    await act(async () => { await expect(confirmation.onOk()).resolves.toBeUndefined(); });
    expect(mocks.refetch).toHaveBeenCalledOnce();
    expect(mocks.error).toHaveBeenCalledOnce();
    expect(mocks.success).not.toHaveBeenCalled();
    expect(container.querySelector("[data-capture-exam]")).toBeNull();
  });
});
