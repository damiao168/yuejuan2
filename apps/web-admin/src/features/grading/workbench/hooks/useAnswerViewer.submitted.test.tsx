// @vitest-environment jsdom
import { useRef } from "react";
import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { downloadReviewWorkspaceImage } from "../../../../api/review";
import type { WorkbenchContext } from "../gradingWorkbench.types";
import { useAnswerViewer } from "./useAnswerViewer";

const notices = vi.hoisted(() => ({ error: vi.fn(), warning: vi.fn() }));
vi.mock("antd", () => ({ App: { useApp: () => ({ message: notices }) } }));
vi.mock("../../../../api/review", () => ({ downloadReviewWorkspaceImage: vi.fn() }));

const context = (id: string, status = "in_progress", path = "/"+id) => ({ task: { id, status }, segmentImageUrl: path }) as WorkbenchContext;
beforeEach(() => {
  vi.clearAllMocks();
  vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} });
  let sequence = 0;
  vi.spyOn(URL, "createObjectURL").mockImplementation(() => "blob:answer-"+(++sequence));
  vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => undefined);
  vi.mocked(downloadReviewWorkspaceImage).mockResolvedValue({ blob: new Blob(["png"]), contentType: "image/png", filename: "answer.png", watermark: undefined });
});
afterEach(() => { cleanup(); vi.restoreAllMocks(); vi.unstubAllGlobals(); });

describe("交分与历史答卷图片", () => {
  it("原图权限撤销后清除原图，不再保留或请求该来源", async () => {
    const source = { ...context("A"), originalImageUrl: "/full-original-A" };
    const view = renderHook(({ allowed }) => useAnswerViewer({ context: source, canViewOriginalImage: allowed, prefetchedPreviewRef: useRef(new Map()) }),
      { initialProps: { allowed: true } });
    await waitFor(() => expect(view.result.current.preview).not.toBeNull());
    await act(async () => view.result.current.changeMode("original"));
    await waitFor(() => expect(downloadReviewWorkspaceImage).toHaveBeenLastCalledWith("/full-original-A"));
    view.rerender({ allowed: false });
    await waitFor(() => expect(view.result.current.preview).toBeNull());
    expect(downloadReviewWorkspaceImage).toHaveBeenCalledTimes(2);
    expect(URL.revokeObjectURL).toHaveBeenCalledWith("blob:answer-2");
  });
  it("同一题提交成功仍保留原图，不因只读状态重下图片", async () => {
    const view = renderHook(({ value }) => useAnswerViewer({ context: value, canViewOriginalImage: false, prefetchedPreviewRef: useRef(new Map()) }),
      { initialProps: { value: context("A") } });
    await waitFor(() => expect(view.result.current.preview?.url).toBe("blob:answer-1"));
    const original = view.result.current.preview;
    view.rerender({ value: context("A", "submitted") });
    expect(view.result.current.preview).toBe(original);
    expect(downloadReviewWorkspaceImage).toHaveBeenCalledOnce();
    expect(URL.revokeObjectURL).not.toHaveBeenCalled();
    expect(notices.error).not.toHaveBeenCalled();
  });

  it("切到上一份已提交答卷仍正常取图，真实图片来源变化才重取", async () => {
    const view = renderHook(({ value }) => useAnswerViewer({ context: value, canViewOriginalImage: false, prefetchedPreviewRef: useRef(new Map()) }),
      { initialProps: { value: context("B") } });
    await waitFor(() => expect(view.result.current.preview).not.toBeNull());
    view.rerender({ value: context("A", "submitted") });
    await waitFor(() => expect(downloadReviewWorkspaceImage).toHaveBeenLastCalledWith("/A"));
    view.rerender({ value: context("A", "submitted", "/A-revised") });
    await waitFor(() => expect(downloadReviewWorkspaceImage).toHaveBeenLastCalledWith("/A-revised"));
    expect(downloadReviewWorkspaceImage).toHaveBeenCalledTimes(3);
  });

  it("清空任务时撤销迟到图片，不能将上一题放回当前页面", async () => {
    let complete!: (value: Awaited<ReturnType<typeof downloadReviewWorkspaceImage>>) => void;
    vi.mocked(downloadReviewWorkspaceImage).mockImplementationOnce(() => new Promise(resolve => { complete = resolve; }));
    const view = renderHook(({ value }: { value: WorkbenchContext | null }) => useAnswerViewer({ context: value, canViewOriginalImage: false, prefetchedPreviewRef: useRef(new Map()) }),
      { initialProps: { value: context("A") as WorkbenchContext | null } });
    await waitFor(() => expect(view.result.current.previewLoading).toBe(true));
    view.rerender({ value: null });
    complete({ blob: new Blob(["old"]), contentType: "image/png", filename: "old.png", watermark: undefined });
    await waitFor(() => expect(view.result.current.previewLoading).toBe(false));
    expect(view.result.current.preview).toBeNull();
    expect(URL.createObjectURL).not.toHaveBeenCalled();
  });
});
