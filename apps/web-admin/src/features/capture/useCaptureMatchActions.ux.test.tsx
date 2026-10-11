// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
import { useCaptureMatchActions } from "./useCaptureMatchActions";

const mocks = vi.hoisted(() => ({ merge: vi.fn(), message: { success: vi.fn(), warning: vi.fn(), error: vi.fn() } }));
vi.mock("antd", () => ({ App: { useApp: () => ({ message: mocks.message }) } }));
vi.mock("../../api/capture", () => ({ mergeCaptureSubmissions: mocks.merge }));
vi.mock("../../api/client", () => ({ getUserErrorMessage: (error: unknown, fallback: string) => error instanceof Error ? error.message : fallback }));

describe("合并答卷提交与批次刷新结果", () => {
  it("远端合并失败传回面板；合并成功但刷新失败保留成功事实", async () => {
    vi.resetAllMocks(); Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    const refresh = vi.fn(); const setMatching = vi.fn(); const setActioning = vi.fn();
    let actions!: ReturnType<typeof useCaptureMatchActions>;
    function Harness() {
      actions = useCaptureMatchActions({ selectedId: "batch", loadMatching: vi.fn(), loadDetail: refresh, setMatching, setActioning });
      return null;
    }
    const root = createRoot(document.createElement("div"));
    await act(async () => root.render(<Harness />));
    mocks.merge.mockRejectedValueOnce(new Error("合并冲突"));
    await expect(actions.mergeSubmissions("target", "source")).rejects.toThrow("合并冲突");
    expect(setMatching).not.toHaveBeenCalled(); expect(setActioning).toHaveBeenLastCalledWith(false);
    const saved = { submissions: [{ id: "target" }] }; mocks.merge.mockResolvedValueOnce(saved);
    refresh.mockRejectedValueOnce(new Error("网络中断"));
    await expect(actions.mergeSubmissions("target", "source")).resolves.toBeUndefined();
    expect(setMatching).toHaveBeenCalledWith(saved);
    expect(mocks.message.warning).toHaveBeenCalledWith("答卷已合并；批次刷新失败，请刷新查看最新页面。网络中断");
    await act(async () => root.unmount());
  });
});
