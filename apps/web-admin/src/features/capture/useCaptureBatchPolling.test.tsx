// @vitest-environment jsdom
import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { useCaptureBatchPolling } from "./useCaptureBatchPolling";

beforeEach(() => { vi.useFakeTimers(); vi.spyOn(document, "hidden", "get").mockReturnValue(false); });
afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks(); });

it("慢请求返回前不叠加；返回后再等待三秒，重绘不会重置计时", async () => {
  let finish!: () => void;
  const load = vi.fn().mockImplementationOnce(() => new Promise<void>(resolve => { finish = resolve; })).mockResolvedValue(undefined);
  const { rerender, unmount } = renderHook(() => useCaptureBatchPolling("batch", "processing", load));
  await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
  expect(load).toHaveBeenCalledExactlyOnceWith("batch", true);
  rerender();
  await act(async () => { await vi.advanceTimersByTimeAsync(12000); });
  expect(load).toHaveBeenCalledTimes(1);
  await act(async () => { finish(); });
  await act(async () => { await vi.advanceTimersByTimeAsync(2999); });
  expect(load).toHaveBeenCalledTimes(1);
  await act(async () => { await vi.advanceTimersByTimeAsync(1); });
  expect(load).toHaveBeenCalledTimes(2);
  unmount();
  await act(async () => { await vi.advanceTimersByTimeAsync(6000); });
  expect(load).toHaveBeenCalledTimes(2);
});

it("隐藏、切换或处理结束后不继续轮询，失败后的当前批次仍可恢复", async () => {
  const load = vi.fn().mockRejectedValueOnce(new Error("offline")).mockResolvedValue(undefined);
  const { rerender } = renderHook(({ id, status }) => useCaptureBatchPolling(id, status, load), { initialProps: { id: "a", status: "processing" } });
  await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
  vi.spyOn(document, "hidden", "get").mockReturnValue(true);
  act(() => document.dispatchEvent(new Event("visibilitychange")));
  await act(async () => { await vi.advanceTimersByTimeAsync(10000); });
  expect(load).toHaveBeenCalledTimes(1);
  vi.spyOn(document, "hidden", "get").mockReturnValue(false);
  act(() => document.dispatchEvent(new Event("visibilitychange")));
  rerender({ id: "b", status: "processing" });
  await act(async () => { await vi.advanceTimersByTimeAsync(3000); });
  expect(load).toHaveBeenLastCalledWith("b", true);
  rerender({ id: "b", status: "completed" });
  await act(async () => { await vi.advanceTimersByTimeAsync(6000); });
  expect(load).toHaveBeenCalledTimes(2);
});
