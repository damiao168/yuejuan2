// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";
import { useCaptureBatchList } from "./useCaptureBatchList";

const mocks = vi.hoisted(() => ({ list: vi.fn(), message: { error: vi.fn() } }));
vi.mock("antd", () => ({ App: { useApp: () => ({ message: mocks.message }) } }));
vi.mock("../../api/capture", () => ({ listCaptureBatches: mocks.list }));

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => { resolve = done; });
  return { promise, resolve };
}

describe("capture refresh owns pagination state", () => {
  it.each(["page-first", "refresh-first"])("recovers pagination and rejects stale page contents: %s", async (order) => {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    mocks.list.mockReset();
    const host = document.createElement("div");
    const root = createRoot(host);
    let current!: ReturnType<typeof useCaptureBatchList>;
    type Page = { batches: { id: string }[]; next_cursor: string; has_more: boolean };
    const page = deferred<Page>();
    const refresh = deferred<Page>();
    mocks.list.mockResolvedValueOnce({ batches: [{ id: "original" }], next_cursor: "first", has_more: true })
      .mockReturnValueOnce(page.promise).mockReturnValueOnce(refresh.promise)
      .mockResolvedValueOnce({ batches: [{ id: "next" }], next_cursor: "", has_more: false });
    function Probe() { current = useCaptureBatchList("exam"); return null; }
    try {
      await act(async () => root.render(<Probe />));
      await act(async () => current.loadBatches());
      let paging!: Promise<void>;
      await act(async () => { paging = current.loadMoreBatches(); });
      expect(current.loadingMoreBatches).toBe(true);
      let refreshing!: Promise<void>;
      await act(async () => { refreshing = current.loadBatches(); });
      expect(current.loadingMoreBatches).toBe(false);
      await act(async () => current.loadMoreBatches());
      expect(mocks.list).toHaveBeenCalledTimes(3);
      const finishPage = async () => { page.resolve({ batches: [{ id: "stale" }], next_cursor: "old", has_more: false }); await paging; };
      const finishRefresh = async () => { refresh.resolve({ batches: [{ id: "fresh" }], next_cursor: "fresh-cursor", has_more: true }); await refreshing; };
      if (order === "page-first") { await act(finishPage); await act(finishRefresh); }
      else { await act(finishRefresh); await act(finishPage); }
      expect(current.batches.map(item => item.id)).toEqual(["fresh"]);
      expect(current.loadingMoreBatches).toBe(false);
      await act(async () => current.loadMoreBatches());
      expect(mocks.list).toHaveBeenLastCalledWith("exam", { limit: 30, cursor: "fresh-cursor" });
      expect(current.batches.map(item => item.id)).toEqual(["fresh", "next"]);
    } finally { await act(async () => root.unmount()); }
  });
});
