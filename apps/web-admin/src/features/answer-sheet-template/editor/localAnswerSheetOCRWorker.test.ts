import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { PSM } from "tesseract.js";
import { createLocalAnswerSheetOCRWorker } from "./localAnswerSheetOCRWorker";

type Packet = { workerId: string; jobId: string; action: string; payload: Record<string, unknown> };
type Client = Awaited<ReturnType<typeof createLocalAnswerSheetOCRWorker>>;
const instances: FakeWorker[] = [];
const clients: Client[] = [];
const page = { blocks: [], text: "" };
const SPARSE_TEXT = "11" as PSM;
const SINGLE_LINE = "7" as PSM;
let onPacket: (worker: FakeWorker, packet: Packet) => void;

class FakeWorker extends EventTarget {
  packets: Packet[] = [];
  terminate = vi.fn();
  postMessage = vi.fn((packet: Packet) => {
    this.packets.push(packet);
    onPacket(this, packet);
  });
  constructor(readonly url: string, readonly options: WorkerOptions) {
    super();
    instances.push(this);
  }
  reply(packet: Packet, data: unknown = {}, status = "resolve") {
    this.dispatchEvent(new MessageEvent("message", { data: { ...packet, status, data } }));
  }
}

const workerOptions = { workerPath: "/assets/ocr-worker.js", corePath: "/assets/ocr-core.wasm.js", langPath: "/ocr/" };
const autoReply = (worker: FakeWorker, packet: Packet) => queueMicrotask(() => worker.reply(packet, packet.action === "recognize" ? page : {}));
const lastPacket = (worker: FakeWorker) => worker.packets[worker.packets.length - 1];
const canvas = () => ({
  width: 100, height: 200,
  toBlob: vi.fn((callback: BlobCallback) => callback({ arrayBuffer: async () => new Uint8Array([137, 80, 78, 71]).buffer } as Blob)),
}) as unknown as HTMLCanvasElement;
const flush = async () => { for (let i = 0; i < 12; i++) await Promise.resolve(); };
async function ready(signal?: AbortSignal) {
  const client = await createLocalAnswerSheetOCRWorker({ ...workerOptions, signal });
  clients.push(client);
  return client;
}

beforeEach(() => {
  vi.useFakeTimers();
  instances.length = 0;
  clients.length = 0;
  onPacket = autoReply;
  vi.stubGlobal("Worker", FakeWorker);
  vi.stubGlobal("window", { location: { href: "http://127.0.0.1:4173/#/admin/exams/exam1/template" } });
});

afterEach(async () => {
  await Promise.all(clients.map(client => client.terminate()));
  vi.useRealTimers();
  vi.unstubAllGlobals();
});

describe("owned local Tesseract 7 worker protocol", () => {
  it("initializes the classic worker in order using only explicit same-origin resources", async () => {
    await ready();
    const worker = instances[0];
    expect(worker.url).toBe("http://127.0.0.1:4173/assets/ocr-worker.js");
    expect(worker.options).toEqual({ type: "classic", name: "answer-sheet-ocr" });
    expect(worker.packets.map(packet => packet.action)).toEqual(["load", "loadLanguage", "initialize"]);
    expect(worker.packets[0].payload).toEqual({ options: { lstmOnly: true, corePath: "http://127.0.0.1:4173/assets/ocr-core.wasm.js", logging: false } });
    expect(worker.packets[1].payload).toEqual({ langs: "eng", options: { langPath: "http://127.0.0.1:4173/ocr/", cacheMethod: "none", gzip: true, lstmOnly: true } });
    expect(worker.packets[2].payload).toEqual({ langs: "eng", oem: 1, config: {} });
    expect(new Set(worker.packets.map(packet => packet.jobId)).size).toBe(3);
    expect(new Set(worker.packets.map(packet => packet.workerId)).size).toBe(1);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("passes worker parameters and PNG bytes with block output to recognition", async () => {
    const client = await ready();
    await client.setParameters({ tessedit_pageseg_mode: SPARSE_TEXT, tessedit_char_whitelist: "0123456789" });
    const image = canvas();
    expect(await client.recognize(image, { rectangle: { left: 1, top: 2, width: 30, height: 40 } })).toEqual({ data: page });
    expect(image.toBlob).toHaveBeenCalledWith(expect.any(Function), "image/png");
    const packets = instances[0].packets;
    expect(packets[3].payload).toEqual({ params: { tessedit_pageseg_mode: "11", tessedit_char_whitelist: "0123456789" } });
    expect(packets[4].payload).toEqual({ image: new Uint8Array([137, 80, 78, 71]), options: { rectangle: { left: 1, top: 2, width: 30, height: 40 } }, output: { blocks: true, text: false } });
    expect(vi.getTimerCount()).toBe(0);
  });

  it.each(["load", "loadLanguage", "initialize"])("rejects and destroys the worker when startup %s rejects", async action => {
    onPacket = (worker, packet) => packet.action === action
      ? queueMicrotask(() => worker.reply(packet, `${action} failed`, "reject")) : autoReply(worker, packet);
    await expect(createLocalAnswerSheetOCRWorker(workerOptions)).rejects.toThrow(`${action} failed`);
    expect(instances[0].terminate).toHaveBeenCalledTimes(1);
    expect(lastPacket(instances[0])?.action).toBe(action);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("times out a language load that never replies and releases the startup wait", async () => {
    onPacket = (worker, packet) => { if (packet.action !== "loadLanguage") autoReply(worker, packet); };
    const rejection = expect(createLocalAnswerSheetOCRWorker(workerOptions)).rejects.toThrow("超时");
    await flush();
    expect(lastPacket(instances[0])?.action).toBe("loadLanguage");
    await vi.advanceTimersByTimeAsync(60_000);
    await rejection;
    expect(instances[0].terminate).toHaveBeenCalledTimes(1);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("does not create a worker for an already aborted scope", async () => {
    const scope = new AbortController();
    scope.abort();
    await expect(createLocalAnswerSheetOCRWorker({ ...workerOptions, signal: scope.signal })).rejects.toMatchObject({ name: "AbortError" });
    expect(instances).toHaveLength(0);
  });

  it("aborts initialization before the core worker replies", async () => {
    onPacket = () => {};
    const scope = new AbortController();
    const rejection = expect(createLocalAnswerSheetOCRWorker({ ...workerOptions, signal: scope.signal })).rejects.toMatchObject({ name: "AbortError" });
    scope.abort();
    await rejection;
    expect(instances[0].terminate).toHaveBeenCalledTimes(1);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("aborts a pending PNG encoding without posting recognition or accepting a late callback", async () => {
    const scope = new AbortController();
    const client = await ready(scope.signal);
    let completeEncoding: BlobCallback | undefined;
    const image = canvas();
    image.toBlob = vi.fn(callback => { completeEncoding = callback; });
    const rejection = expect(client.recognize(image)).rejects.toMatchObject({ name: "AbortError" });
    await flush();
    expect(image.toBlob).toHaveBeenCalledTimes(1);
    scope.abort();
    await rejection;
    const read = vi.fn(async () => new ArrayBuffer(4));
    completeEncoding?.({ arrayBuffer: read } as unknown as Blob);
    await flush();
    expect(read).not.toHaveBeenCalled();
    expect(instances[0].packets.some(packet => packet.action === "recognize")).toBe(false);
    expect(instances[0].terminate).toHaveBeenCalledTimes(1);
  });

  it("settles both an active recognition and queued operations on explicit termination", async () => {
    const client = await ready();
    onPacket = () => {};
    const results = Promise.allSettled([client.recognize(canvas()), client.setParameters({ tessedit_pageseg_mode: SINGLE_LINE }), client.recognize(canvas())]);
    await flush();
    expect(lastPacket(instances[0])?.action).toBe("recognize");
    await client.terminate();
    expect(await results).toEqual(Array.from({ length: 3 }, () => ({ status: "rejected", reason: expect.objectContaining({ name: "AbortError" }) })));
    await expect(client.recognize(canvas())).rejects.toMatchObject({ name: "AbortError" });
    await client.terminate();
    expect(instances[0].terminate).toHaveBeenCalledTimes(1);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("times out a recognition that never replies and rejects the queued work", async () => {
    const client = await ready();
    onPacket = () => {};
    const results = Promise.allSettled([client.recognize(canvas()), client.setParameters({ tessedit_pageseg_mode: SINGLE_LINE })]);
    await flush();
    await vi.advanceTimersByTimeAsync(60_000);
    for (const result of await results) expect(result).toMatchObject({ status: "rejected", reason: expect.objectContaining({ message: expect.stringContaining("超时") }) });
    expect(instances[0].terminate).toHaveBeenCalledTimes(1);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("does not confuse progress, another action or another worker with the current job result", async () => {
    const client = await ready();
    onPacket = () => {};
    let resolved = false;
    const result = client.setParameters({ tessedit_pageseg_mode: SINGLE_LINE }).then(() => { resolved = true; });
    await flush();
    const worker = instances[0];
    const packet = lastPacket(worker)!;
    worker.reply(packet, { progress: .5 }, "progress");
    worker.reply({ ...packet, workerId: "old-scope" });
    worker.reply({ ...packet, action: "recognize" });
    await flush();
    expect(resolved).toBe(false);
    worker.reply(packet);
    await result;
    expect(resolved).toBe(true);
    expect(vi.getTimerCount()).toBe(0);
  });

  it.each(["error", "messageerror"])("rejects active work when the native worker emits %s", async eventType => {
    const client = await ready();
    onPacket = () => {};
    const rejection = expect(client.setParameters({ tessedit_pageseg_mode: SINGLE_LINE })).rejects.toThrow();
    await flush();
    const event = new Event(eventType, { cancelable: true });
    Object.defineProperty(event, "message", { value: "worker crashed" });
    instances[0].dispatchEvent(event);
    await rejection;
    expect(instances[0].terminate).toHaveBeenCalledTimes(1);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("rejects and destroys the worker if posting a job throws", async () => {
    const client = await ready();
    instances[0].postMessage.mockImplementation(() => { throw new Error("post failed"); });
    await expect(client.setParameters({ tessedit_pageseg_mode: SINGLE_LINE })).rejects.toThrow("post failed");
    expect(instances[0].terminate).toHaveBeenCalledTimes(1);
  });

  it("rejects a null PNG encoding instead of posting an invalid image", async () => {
    const client = await ready();
    const image = canvas();
    image.toBlob = vi.fn(callback => callback(null));
    await expect(client.recognize(image)).rejects.toThrow("无法编码");
    expect(instances[0].terminate).toHaveBeenCalledTimes(1);
    expect(instances[0].packets.some(packet => packet.action === "recognize")).toBe(false);
  });

  it("bounds the canvas encoding wait even if toBlob never calls back", async () => {
    const client = await ready();
    const image = canvas();
    image.toBlob = vi.fn();
    const rejection = expect(client.recognize(image)).rejects.toThrow("超时");
    await flush();
    await vi.advanceTimersByTimeAsync(60_000);
    await rejection;
    expect(instances[0].terminate).toHaveBeenCalledTimes(1);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("rejects failed blob reads and releases the owned worker", async () => {
    const client = await ready();
    const image = canvas();
    image.toBlob = vi.fn(callback => callback({ arrayBuffer: async () => { throw new Error("blob read failed"); } } as unknown as Blob));
    await expect(client.recognize(image)).rejects.toThrow("blob read failed");
    expect(instances[0].terminate).toHaveBeenCalledTimes(1);
  });

  it.each(["workerPath", "corePath", "langPath"] as const)("rejects an external %s before constructing a worker", async path => {
    await expect(createLocalAnswerSheetOCRWorker({ ...workerOptions, [path]: "https://cdn.example.test/resource.js" })).rejects.toThrow("当前站点");
    expect(instances).toHaveLength(0);
  });
});
