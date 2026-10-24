// @vitest-environment jsdom
import { act, useMemo, useState } from "react";
import { createRoot, type Root } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setBusinessCommandScope } from "../../../../api/businessCommand";
import { ApiClientError } from "../../../../api/client";
import { createReviewTaskContextScope } from "../../../../query/reviewTaskContext";
import { reviewBookmarkKeys } from "../../../../query/reviewBookmarks";
import { normalizeReviewBookmarkReason, reviewBookmarkCodePointLength, type PersonalReviewBookmark } from "../../../../api/reviewBookmarks";
import { usePersonalReviewBookmarks } from "./usePersonalReviewBookmarks";

const api = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), remove: vi.fn() }));
vi.mock("../../../../api/reviewBookmarks", async (original) => ({ ...await original<typeof import("../../../../api/reviewBookmarks")>(), getPersonalReviewBookmark: api.get, putPersonalReviewBookmark: api.put, deletePersonalReviewBookmark: api.remove }));
const state = (revision = 0, marked = false): PersonalReviewBookmark => ({ id: marked ? "bookmark-A" : null, task_id: "A", marked, reason: "", revision, can_edit_reason: true });
function deferred<T>() { let resolve!: (value: T) => void; const promise = new Promise<T>((done) => { resolve = done; }); return { promise, resolve }; }

describe("个人复查写入的会话与恢复边界", () => {
  let root: Root;
  let client: QueryClient;
  let current: ReturnType<typeof usePersonalReviewBookmarks>;
  let switchTask: (id: string) => void;
  beforeEach(() => {
    vi.clearAllMocks(); setBusinessCommandScope("tenant", "teacher");
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    api.get.mockResolvedValue({ bookmark: state() });
    client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    root = createRoot(document.createElement("div"));
  });
  afterEach(async () => { await act(async () => root.unmount()); client.clear(); });
  async function mount() {
    function Harness() { const [taskId, setTaskId] = useState("A"); switchTask = setTaskId; const scope = useMemo(() => createReviewTaskContextScope(), []); current = usePersonalReviewBookmarks(scope, taskId); return null; }
    await act(async () => { root.render(<QueryClientProvider client={client}><Harness /></QueryClientProvider>); });
    await act(async () => { await new Promise((done) => setTimeout(done, 10)); });
  }
  it("200个emoji按码点允许，201个拒绝且不发送请求", async () => {
    await mount();
    expect(reviewBookmarkCodePointLength("😀".repeat(200))).toBe(200);
    await act(async () => { expect(await current.write(state(), "😀".repeat(201))).toBe(false); });
    expect(api.put).not.toHaveBeenCalled();
    api.put.mockResolvedValue({ bookmark_id: "bookmark-A", revision: 1, marked: true }); api.get.mockResolvedValue({ bookmark: state(1, true) });
    await act(async () => { expect(await current.write(state(), "😀".repeat(200))).toBe(true); });
    expect(api.put).toHaveBeenCalledTimes(1);
  });
  it("先按服务端规则处理首尾空白与CRLF计数，写入和重试仍保留原始载荷", async () => {
    await mount();
    const reason = ` \u0085\r\n${"😀".repeat(198)}\r\n甲\r\n\u3000 `;
    expect(normalizeReviewBookmarkReason(reason)).toBe(`${"😀".repeat(198)}\n甲`);
    expect(reviewBookmarkCodePointLength(reason)).toBe(200);
    // BOM 不是 Go TrimSpace 的空白，不能被前端误删而接受服务端会拒绝的超长说明。
    expect(reviewBookmarkCodePointLength(`\ufeff${"😀".repeat(200)}`)).toBe(201);
    api.put.mockRejectedValueOnce(new TypeError("network"));
    await act(async () => { await current.write(state(), reason); });
    expect(api.put.mock.calls[0][1]).toBe(reason);
    api.put.mockResolvedValue({ bookmark_id: "bookmark-A", revision: 1, marked: true }); api.get.mockResolvedValue({ bookmark: state(1, true) });
    await act(async () => { expect(await current.retry()).toBe(true); });
    expect(api.put.mock.calls[1].slice(0, 4)).toEqual(api.put.mock.calls[0].slice(0, 4));
  });
  it("丢包后原键原载荷显式重试，不把原因写入localStorage", async () => {
    await mount(); const storage = vi.spyOn(Storage.prototype, "setItem");
    api.put.mockRejectedValueOnce(new TypeError("network"));
    await act(async () => { await current.write(state(), "核对步骤"); });
    const original = api.put.mock.calls[0];
    expect(current.uncertainTask).toBe("A");
    api.put.mockResolvedValue({ bookmark_id: "bookmark-A", revision: 1, marked: true }); api.get.mockResolvedValue({ bookmark: state(1, true) });
    await act(async () => { expect(await current.retry()).toBe(true); });
    expect(api.put.mock.calls[1].slice(0, 4)).toEqual(original.slice(0, 4));
    expect(storage).not.toHaveBeenCalled(); storage.mockRestore();
  });
  it("待确认期间不能发起另一条清理命令而遮住原重试", async () => {
    await mount(); api.put.mockRejectedValueOnce(new TypeError("network"));
    await act(async () => { await current.write(state(), "核对步骤"); });
    await act(async () => { expect(await current.write({ ...state(2, true), task_id: "", id: "cleanup-B" }, null)).toBe(false); });
    expect(api.remove).not.toHaveBeenCalled(); expect(current.uncertainTask).toBe("A");
    api.put.mockResolvedValue({ bookmark_id: "bookmark-A", revision: 1, marked: true }); api.get.mockResolvedValue({ bookmark: state(1, true) });
    await act(async () => { expect(await current.retry()).toBe(true); });
    expect(api.put.mock.calls[1].slice(0, 4)).toEqual(api.put.mock.calls[0].slice(0, 4));
  });
  it("撤权清理只用记录ID取消，不读取原任务或说明", async () => {
    await mount(); api.get.mockClear();
    api.remove.mockResolvedValue({ bookmark_id: "cleanup-B", revision: 3, marked: false });
    await act(async () => { expect(await current.write({ ...state(2, true), task_id: "", id: "cleanup-B" }, null)).toBe(true); });
    expect(api.remove).toHaveBeenCalledWith("cleanup-B", 2, expect.any(String), expect.any(AbortSignal));
    expect(api.get).not.toHaveBeenCalled(); expect(api.put).not.toHaveBeenCalled();
  });
  it("409明确拒绝后读取当前revision，新操作换键", async () => {
    await mount(); api.put.mockRejectedValueOnce(new ApiClientError(409, "revision_conflict", "conflict"));
    api.get.mockResolvedValue({ bookmark: state(3, true) });
    await act(async () => { await current.write(state(), "本页说明"); });
    const oldKey = api.put.mock.calls[0][3];
    expect(current.uncertainTask).toBe(""); expect(current.error).toContain("标记状态已变化");
    api.put.mockResolvedValue({ bookmark_id: "bookmark-A", revision: 4, marked: true }); api.get.mockResolvedValue({ bookmark: state(4, true) });
    await act(async () => { await current.write(state(3, true), "本页说明"); });
    expect(api.put.mock.calls[1][3]).not.toBe(oldKey); expect(api.put.mock.calls[1][2]).toBe(3);
  });
  it("旧新增回执只触发读取，已取消的新状态不被重新点亮", async () => {
    await mount(); api.put.mockResolvedValue({ bookmark_id: "bookmark-A", revision: 1, marked: true });
    api.get.mockResolvedValue({ bookmark: state(2, false) });
    await act(async () => { await current.write(state(), ""); });
    await act(async () => { await new Promise((done) => setTimeout(done, 10)); });
    expect(current.state.data?.revision).toBe(2); expect(current.state.data?.marked).toBe(false);
  });
  it("切题时迟到写入只读取原任务，换会话丢弃结果与意图", async () => {
    await mount(); const write = deferred<{ bookmark_id: string; revision: number; marked: boolean }>(); api.put.mockReturnValue(write.promise);
    let writing!: Promise<boolean>; await act(async () => { writing = current.write(state(), "说明"); });
    await act(async () => switchTask("B"));
    api.get.mockImplementation(async (id: string) => ({ bookmark: { ...state(2, false), task_id: id } }));
    await act(async () => { setBusinessCommandScope("tenant", "teacher"); write.resolve({ bookmark_id: "bookmark-A", revision: 1, marked: true }); await writing; });
    expect(current.uncertainTask).toBe("");
    expect(client.getQueriesData({ queryKey: reviewBookmarkKeys.scope(createReviewTaskContextScope()) })).toEqual([]);
  });
});
