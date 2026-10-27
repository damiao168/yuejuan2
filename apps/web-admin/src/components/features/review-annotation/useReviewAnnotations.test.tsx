// @vitest-environment jsdom
import { act, renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ApiClientError } from "../../../api/client";
import { useReviewAnnotations } from "./useReviewAnnotations";

const api = vi.hoisted(() => ({ list: vi.fn(), create: vi.fn(), remove: vi.fn() }));
vi.mock("../../../api/reviewAnnotations", () => ({ listReviewAnnotations: api.list, createReviewAnnotation: api.create, deleteReviewAnnotation: api.remove }));

describe("图标批注只读写原答卷", () => {
  it("切题后旧写入回执不进入新答卷，不请求已删除的常用评语", async () => {
    api.list.mockResolvedValue({ annotations: [] });
    let finish!: (value: unknown) => void;
    api.create.mockImplementation(() => new Promise(resolve => { finish = resolve; }));
    const view = renderHook(({ taskId }) => useReviewAnnotations(taskId), { initialProps: { taskId: "A" } });
    await waitFor(() => expect(view.result.current.loading).toBe(false));
    let pending!: Promise<unknown>;
    const input = { type: "note" as const, content: "✓", geometry: { coordinate_space: "canonical_image_normalized" as const, x: .2, y: .3, width: 0, height: 0 } };
    act(() => { pending = view.result.current.saveAnnotation(input); });
    view.rerender({ taskId: "B" });
    await waitFor(() => expect(view.result.current.loading).toBe(false));
    await act(async () => { finish({ annotation: { id: "old", ...input } }); await pending; });
    expect(view.result.current.annotations).toEqual([]);
    expect(view.result.current.saveState).toBe("idle");
    expect(api.create).toHaveBeenCalledWith("A", input);
    view.unmount();
  });

  it("版本冲突刷新原批注并显示真实失败，不自动重试删除", async () => {
    vi.clearAllMocks();
    const annotation = { id: "mark", revision: 2 };
    api.list.mockResolvedValue({ annotations: [annotation] });
    api.remove.mockRejectedValue(new ApiClientError(409, "conflict", "conflict"));
    const view = renderHook(() => useReviewAnnotations("A"));
    await waitFor(() => expect(view.result.current.loading).toBe(false));
    await act(async () => {
      try { await view.result.current.removeAnnotation(annotation as Parameters<typeof view.result.current.removeAnnotation>[0]); } catch { /* 确认失败仍交给调用方处理。 */ }
    });
    expect(view.result.current.annotations).toEqual([annotation]);
    expect(view.result.current.saveState).toBe("conflict");
    expect(api.remove).toHaveBeenCalledExactlyOnceWith("mark", 2);
    expect(api.list).toHaveBeenCalledTimes(2);
    view.unmount();
  });
});
