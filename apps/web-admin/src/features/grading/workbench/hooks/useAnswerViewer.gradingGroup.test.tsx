// @vitest-environment jsdom
import { act, useRef } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { downloadReviewWorkspaceImage } from "../../../../api/review";
import type { WorkbenchContext } from "../gradingWorkbench.types";
import { useAnswerViewer } from "./useAnswerViewer";

vi.mock("antd", () => ({ App: { useApp: () => ({ message: notices }) } }));
const notices = vi.hoisted(() => ({ error: vi.fn(), warning: vi.fn() }));
vi.mock("../../../../api/review", () => ({ downloadReviewWorkspaceImage: vi.fn() }));

afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });

describe("共区批阅图片与单题评分图片分开读取", () => {
  it("整区读取共享图，切回单题仍读取子ROI，数学子图坐标不会沿用到整区", async () => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    vi.stubGlobal("ResizeObserver", class { observe() {} disconnect() {} });
    vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:answer");
    vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => undefined);
    vi.mocked(downloadReviewWorkspaceImage).mockResolvedValue({ blob: new Blob(["png"]), contentType: "image/png", filename: "answer.png", watermark: undefined });
    const context = { task: { id: "task12" }, segmentImageUrl: "/child12", gradingGroupImageUrl: "/shared12-14" } as unknown as WorkbenchContext;
    let viewer!: ReturnType<typeof useAnswerViewer>;
    function Harness() {
      viewer = useAnswerViewer({ context, canViewOriginalImage: false, prefetchedPreviewRef: useRef(new Map()), contentRevision: "child-only-revision" });
      return null;
    }
    const host = document.createElement("div");
    const root = createRoot(host);
    try {
      await act(async () => root.render(<Harness />));
      expect(downloadReviewWorkspaceImage).toHaveBeenLastCalledWith("/child12");
      await act(async () => viewer.changeMode("group"));
      expect(downloadReviewWorkspaceImage).toHaveBeenLastCalledWith("/shared12-14");
      expect(viewer.preview?.contentRevision).toBeUndefined();
      await act(async () => viewer.changeMode("segment"));
      expect(downloadReviewWorkspaceImage).toHaveBeenLastCalledWith("/child12");
      expect(viewer.preview?.contentRevision).toBe("child-only-revision");
    } finally { await act(async () => root.unmount()); }
  });
});
