// @vitest-environment jsdom
import { act } from "react";
import { createRoot, type Root } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useChatModelAccess } from "./useChatModelAccess";
import type { ChatModelAccess } from "../../../api/chatModelAccess";

const mocks = vi.hoisted(() => ({ get: vi.fn(), save: vi.fn() }));
vi.mock("../../../api/chatModelAccess", () => ({ getChatModelAccess: mocks.get, saveChatModelAccess: mocks.save }));

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: unknown) => void;
  const promise = new Promise<T>((done, fail) => { resolve = done; reject = fail; });
  return { promise, resolve, reject };
}
const access = (revision: string, model_ids: string[] = []) => ({ access: { revision, model_ids } });
type Response = ReturnType<typeof access>;
let current: ReturnType<typeof useChatModelAccess>;
function Harness({ tenantID }: Readonly<{ tenantID: string }>) {
  current = useChatModelAccess(tenantID);
  return <output>{current.ids?.join(",")}</output>;
}

describe("学校对话开放配置的保存与读取边界", () => {
  let root: Root;
  let host: HTMLDivElement;
  beforeEach(() => {
    vi.resetAllMocks();
    Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
    host = document.createElement("div"); document.body.append(host); root = createRoot(host);
    mocks.get.mockResolvedValue(access("r1", ["first"]));
    mocks.save.mockImplementation(async (_tenant: string, _previous: ChatModelAccess, ids: string[]) => access("r2", ids));
  });
  afterEach(async () => { await act(async () => root.unmount()); host.remove(); });
  const render = async (tenantID = "school-a") => { await act(async () => root.render(<Harness tenantID={tenantID} />)); };

  it("连续勾选只保存一次，成功后下一次保留已有开放项并使用新版本", async () => {
    const saved = deferred<Response>(); mocks.save.mockReturnValueOnce(saved.promise);
    await render();
    await act(async () => { void current.toggle("second"); void current.toggle("third"); });
    expect(mocks.save).toHaveBeenCalledTimes(1);
    expect(current.busy).toBe(true);
    await act(async () => saved.resolve(access("r2", ["first", "second"])));
    await act(async () => current.toggle("third"));
    expect(mocks.save).toHaveBeenLastCalledWith("school-a", { revision: "r2", model_ids: ["first", "second"] }, ["first", "second", "third"]);
  });

  it("冲突保留旧选择，重新读取后按新版本保存而不覆盖他人开放项", async () => {
    await render(); mocks.save.mockRejectedValueOnce(new Error("conflict"));
    await act(async () => current.toggle("second"));
    expect(current.ids).toEqual(["first"]); expect(current.error).toBeTruthy();
    mocks.get.mockResolvedValueOnce(access("r3", ["first", "third"]));
    await act(async () => current.refresh());
    expect(current.error).toBe("");
    await act(async () => current.toggle("second"));
    expect(mocks.save).toHaveBeenLastCalledWith("school-a", { revision: "r3", model_ids: ["first", "third"] }, ["first", "third", "second"]);
  });

  it("切换学校后丢弃旧读取，刷新失败时不允许按旧名单保存", async () => {
    const old = deferred<Response>(); mocks.get.mockReturnValueOnce(old.promise);
    await render(); await render("school-b");
    await act(async () => old.resolve(access("old-a", ["foreign"])));
    expect(current.ids).toEqual(["first"]);
    mocks.get.mockRejectedValueOnce(new Error("read failed"));
    await act(async () => current.refresh());
    expect(current.ids).toBeUndefined(); expect(current.error).toBeTruthy();
    await act(async () => current.toggle("second"));
    expect(mocks.save).not.toHaveBeenCalled();
  });

  it("旧学校的保存晚于新学校保存返回时，不能解除新学校的保存互斥", async () => {
    const old = deferred<Response>(); const next = deferred<Response>();
    mocks.save.mockReturnValueOnce(old.promise).mockReturnValueOnce(next.promise);
    await render(); await act(async () => { void current.toggle("second"); });
    await render("school-b"); await act(async () => { void current.toggle("third"); });
    // 两所学校保存可交叠，但旧保存结束不能放开新学校的写锁或污染新学校选择。
    await act(async () => old.resolve(access("old-a", ["foreign"])));
    expect(current.busy).toBe(true); expect(current.ids).toEqual(["first"]);
    await act(async () => { void current.toggle("fourth"); });
    expect(mocks.save).toHaveBeenCalledTimes(2);
    await act(async () => next.resolve(access("next-b", ["first", "third"])));
    expect(current.busy).toBe(false); expect(current.ids).toEqual(["first", "third"]);
  });

  it("新一次刷新先返回后不接受更早的读取结果", async () => {
    const old = deferred<Response>(); mocks.get.mockReturnValueOnce(old.promise);
    await render();
    mocks.get.mockResolvedValueOnce(access("new", ["second"]));
    await act(async () => current.refresh());
    await act(async () => old.resolve(access("old", ["first"])));
    expect(current.ids).toEqual(["second"]);
  });
});
