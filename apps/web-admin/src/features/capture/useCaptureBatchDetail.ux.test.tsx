// @vitest-environment jsdom
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useCaptureBatchDetail } from "./useCaptureBatchDetail";
import type { CaptureBatchDetail, MatchingQueue } from "../../api/capture";

const mocks = vi.hoisted(() => ({ detail: vi.fn(), matching: vi.fn() }));
vi.mock("../../api/capture", () => ({ getCaptureBatch: mocks.detail, getMatchingQueue: mocks.matching }));
const detail = (id: string) => ({ batch: { id, failed_count: 0 }, pages: [], processing_summaries: [{ submission_id: `${id}-answer` }] }) as unknown as CaptureBatchDetail;
const queue = (id: string) => ({ batch_id: id, submissions: [] }) as unknown as MatchingQueue;
const setBatches = vi.fn(); const setTab = vi.fn();

describe("采集批次读取绑定当前对象", () => {
  let root: ReturnType<typeof createRoot> | undefined;
  let value: ReturnType<typeof useCaptureBatchDetail>;
  function Harness({ id, tab }: { id: string; tab?: string }) { value = useCaptureBatchDetail(setBatches, setTab, id, tab); return null; }
  afterEach(async () => { await act(async () => root?.unmount()); root = undefined; vi.resetAllMocks(); });
  async function render(id: string, tab?: string) {
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    root ??= createRoot(document.createElement("div"));
    await act(async () => root!.render(<Harness id={id} tab={tab} />));
  }

  it("新批次失败不能保留旧详情、匹配队列或处理汇总，且支持重试", async () => {
    await render("old"); mocks.detail.mockResolvedValueOnce(detail("old")); mocks.matching.mockResolvedValueOnce(queue("old"));
    await act(async () => { await value.loadDetail("old"); await value.loadMatching("old"); });
    await render("new"); expect(value.detail).toBeUndefined(); expect(value.matching).toBeUndefined(); expect(value.processingSummaries).toEqual({});
    mocks.detail.mockRejectedValueOnce(new Error("网络断开")); mocks.matching.mockRejectedValueOnce(new Error("名册断开"));
    await act(async () => { await value.loadDetail("new"); await value.loadMatching("new"); });
    expect(value.detailError).toBe("网络断开"); expect(value.matchingError).toBe("名册断开"); expect(value.detailLoading).toBe(false);
    mocks.detail.mockResolvedValueOnce(detail("new")); await act(async () => { await value.loadDetail("new"); });
    expect(value.detail?.batch.id).toBe("new"); expect(value.detailError).toBeUndefined();
  });

  it("旧请求晚回不能覆盖新批次；旧批次刷新不会启动新请求", async () => {
    await render("old"); let finish!: (result: CaptureBatchDetail) => void;
    mocks.detail.mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
    let pending!: Promise<void>; await act(async () => { pending = value.loadDetail("old"); });
    await render("new"); mocks.detail.mockResolvedValueOnce(detail("new"));
    await act(async () => { await value.loadDetail("new"); finish(detail("old")); await pending; await value.loadDetail("old", true); });
    expect(value.detail?.batch.id).toBe("new"); expect(mocks.detail).toHaveBeenCalledTimes(2);
  });

  it("同批次后台刷新失败保留已读事实并显示刷新错误", async () => {
    await render("same"); mocks.detail.mockResolvedValueOnce(detail("same"));
    await act(async () => { await value.loadDetail("same"); });
    mocks.detail.mockRejectedValueOnce(new Error("刷新失败")); await act(async () => { await value.loadDetail("same", true); });
    expect(value.detail?.batch.id).toBe("same"); expect(value.detailError).toBe("刷新失败");
  });

  it("定位到质检页时不被批次问题抢走标签，后台刷新不再次切换用户标签", async () => {
    await render("same", "image-quality"); mocks.detail.mockResolvedValue({ ...detail("same"), batch: { ...detail("same").batch, failed_count: 1 } });
    await act(async () => { await value.loadDetail("same"); });
    expect(setTab).toHaveBeenLastCalledWith("image-quality");
    setTab.mockClear(); await act(async () => { await value.loadDetail("same", true); });
    expect(setTab).not.toHaveBeenCalled();
  });
});
