import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiClientError } from "./client";
import { CONVERSATION_REQUEST_TIMEOUT_MS, streamSchoolConversation, type SchoolConversationEvent } from "./schoolAiChat";

const api = vi.hoisted(() => ({ stream: vi.fn() }));
vi.mock("./client", async (original) => ({ ...await original<typeof import("./client")>(), apiClient: { requestEventStream: api.stream } }));

describe("school conversation terminal and timeout boundaries", () => {
  let emit: (event: SchoolConversationEvent) => void;
  let transportSignal: AbortSignal;
  beforeEach(() => {
    vi.useFakeTimers();
    api.stream.mockReset().mockImplementation((_path, init: RequestInit, callback) => {
      transportSignal = init.signal as AbortSignal;
      emit = callback;
      // 模拟代理收到终态后仍不关闭 HTTP：前端必须按终态退出，不能依赖 EOF 释放发送锁。
      return new Promise<void>(() => undefined);
    });
  });
  afterEach(() => vi.useRealTimers());
  const start = (onEvent: (event: SchoolConversationEvent) => void = vi.fn(), signal = new AbortController().signal) => streamSchoolConversation([{ role: "user", content: "北京天气" }], undefined, signal, onEvent, undefined, true);

  it("settles done without waiting for a proxy EOF and releases its timer", async () => {
    const onEvent = vi.fn();
    const request = start(onEvent);
    const done: SchoolConversationEvent = { type: "done", request_id: "request", seq: 1, completion: {
      message: { role: "assistant", content: "天气结果" }, model_name: "synthetic", display_name: "测试模型", provider_key: "provider",
      usage: { input_tokens: 1, cached_input_tokens: 0, output_tokens: 1, reasoning_tokens: 0, total_tokens: 2 },
    } };
    emit(done);
    await expect(request).resolves.toBeUndefined();
    expect(onEvent).toHaveBeenCalledWith(done);
    expect(transportSignal.aborted).toBe(true);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("rejects a controlled error frame even when the callback only observes it", async () => {
    const request = start();
    const rejected = expect(request).rejects.toMatchObject({ code: "ai_chat_model_timeout" });
    emit({ type: "error", request_id: "request", seq: 1, code: "ai_chat_model_timeout", message: "private upstream details" });
    await rejected;
    expect(transportSignal.aborted).toBe(true);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("releases a silent stream with a retryable timeout instead of waiting forever", async () => {
    const request = start();
    const rejected = expect(request).rejects.toMatchObject({ name: "ApiClientError", code: "ai_chat_timeout" });
    await vi.advanceTimersByTimeAsync(CONVERSATION_REQUEST_TIMEOUT_MS);
    await rejected;
    expect(transportSignal.aborted).toBe(true);
    expect(vi.getTimerCount()).toBe(0);
  });

  it("preserves user cancellation separately from the deadline and removes listeners", async () => {
    api.stream.mockImplementation((_path, init: RequestInit) => new Promise<void>((_resolve, reject) => {
      transportSignal = init.signal as AbortSignal;
      transportSignal.addEventListener("abort", () => reject(new DOMException("Stopped", "AbortError")), { once: true });
    }));
    const controller = new AbortController();
    const remove = vi.spyOn(controller.signal, "removeEventListener");
    const request = start(vi.fn(), controller.signal);
    const rejected = expect(request).rejects.toMatchObject({ name: "AbortError" });
    controller.abort();
    await rejected;
    expect(transportSignal.aborted).toBe(true);
    expect(remove).toHaveBeenCalledWith("abort", expect.any(Function));
    expect(vi.getTimerCount()).toBe(0);
  });

  it("preserves callback rejection while stopping the transport", async () => {
    api.stream.mockImplementation(async (_path, init: RequestInit, onEvent) => {
      transportSignal = init.signal as AbortSignal;
      onEvent({ type: "error", request_id: "request", seq: 1, code: "ai_chat_model_timeout", message: "private detail" });
    });
    await expect(start(() => { throw new ApiClientError(0, "ai_chat_model_timeout", "private detail"); })).rejects.toMatchObject({ code: "ai_chat_model_timeout" });
    expect(transportSignal.aborted).toBe(true);
    expect(vi.getTimerCount()).toBe(0);
  });
});
