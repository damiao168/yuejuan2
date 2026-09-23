// @vitest-environment jsdom
import { beforeEach, describe, expect, it, vi } from "vitest";
import { bindDurableSession, clearDurableSession, listDurableScanQueue, loadDurableSpoolFile, spoolScanAsset } from "./durableStore";

const mocks = vi.hoisted(() => ({ invoke: vi.fn(), sha256ForFile: vi.fn() }));
vi.mock("@tauri-apps/api/core", () => ({ invoke: mocks.invoke }));
vi.mock("./localRuntime", () => ({ isTauriRuntime: () => true }));
vi.mock("../api/captureUploads", () => ({ sha256ForFile: mocks.sha256ForFile }));

describe("durable session leases", () => {
  beforeEach(async () => {
    mocks.invoke.mockReset();
    mocks.sha256ForFile.mockReset();
    await clearDurableSession();
    mocks.invoke.mockReset();
    let nextSession = 0;
    mocks.invoke.mockImplementation(async (command: string, args?: { sessionId?: string; localAssetId?: string }) => {
      if (command === "bind_durable_session") return `lease-${++nextSession}`;
      if (command === "read_durable_local_asset") {
        if (args?.sessionId === "lease-1" && args.localAssetId === "asset-a") {
          return { filename: "a.pdf", mime: "application/pdf", size: 3, sha256: "hash", chunkSize: 3 };
        }
        throw new Error("asset not found in this account");
      }
      if (command === "read_durable_local_asset_chunk") return [1, 2, 3];
      return undefined;
    });
  });

  it("rejects A's open asset handle after B logs in and cannot resolve A's ID in B", async () => {
    await bindDurableSession("https://example.test", "tenant", "A");
    const sourceA = await loadDurableSpoolFile("asset-a");
    await bindDurableSession("https://example.test", "tenant", "B");
    await expect(sourceA.slice(0, 3)).rejects.toThrow("登录账号已切换");
    expect(mocks.invoke).not.toHaveBeenCalledWith("read_durable_local_asset_chunk", expect.anything());
    await expect(loadDurableSpoolFile("asset-a")).rejects.toThrow("asset not found in this account");
    expect(mocks.invoke).toHaveBeenCalledWith("read_durable_local_asset", { localAssetId: "asset-a", sessionId: "lease-2" });
  });

  it("does not begin an A spool under B after file hashing finishes late", async () => {
    let finishHash!: (hash: string) => void;
    mocks.sha256ForFile.mockImplementationOnce(() => new Promise((resolve) => { finishHash = resolve; }));
    await bindDurableSession("https://example.test", "tenant", "A");
    const spooling = spoolScanAsset({ file: new File(["abc"], "a.pdf", { type: "application/pdf" }) });
    await bindDurableSession("https://example.test", "tenant", "B");
    finishHash("0123456789abcdef");
    await expect(spooling).rejects.toThrow("登录账号已切换");
    expect(mocks.invoke).not.toHaveBeenCalledWith("begin_spool_local_asset", expect.anything());
  });

  it("keeps A's queue through logout and restart while B and another server stay empty", async () => {
    const scopes = new Map<string, string>();
    let lease = 0;
    mocks.invoke.mockImplementation(async (command: string, args?: { sessionId?: string; server?: string; tenantId?: string; actorId?: string }) => {
      if (command === "bind_durable_session") {
        const next = `lease-${++lease}`;
        scopes.set(next, `${args?.server}|${args?.tenantId}|${args?.actorId}`);
        return next;
      }
      if (command === "list_durable_scan_queue") {
        return scopes.get(args?.sessionId ?? "") === "https://school.test|tenant|A"
          ? [{ id: "A-private-queue", status: "pending" }]
          : [];
      }
      return undefined;
    });
    const oldA = await bindDurableSession("https://school.test", "tenant", "A");
    expect((await listDurableScanQueue(oldA)).map((item) => item.id)).toEqual(["A-private-queue"]);
    await clearDurableSession();
    await expect(listDurableScanQueue(oldA)).rejects.toThrow("登录账号已切换");
    const b = await bindDurableSession("https://school.test", "tenant", "B");
    expect(await listDurableScanQueue(b)).toEqual([]);
    const otherServer = await bindDurableSession("https://other.test", "tenant", "A");
    expect(await listDurableScanQueue(otherServer)).toEqual([]);
    await clearDurableSession(); // a restarted app has no usable native lease
    const newA = await bindDurableSession("https://school.test", "tenant", "A");
    expect(newA).not.toBe(oldA);
    expect((await listDurableScanQueue(newA)).map((item) => item.id)).toEqual(["A-private-queue"]);
    await expect(listDurableScanQueue(oldA)).rejects.toThrow("登录账号已切换");
  });
});
