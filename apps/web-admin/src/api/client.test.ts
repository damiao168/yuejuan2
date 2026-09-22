import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient, ApiClientError, parseRetryAfterSeconds, RECENT_AUTH_REQUIRED_EVENT } from "./client";

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("web API diagnostics", () => {
  it("parses chat events across network chunk boundaries", async () => {
    const bytes = new TextEncoder().encode('data: {"type":"reasoning","delta":"思考"}\n\ndata: {"type":"content","delta":"答案"}\n\n');
    const body = new ReadableStream<Uint8Array>({
      start(controller) {
        controller.enqueue(bytes.slice(0, 29));
        controller.enqueue(bytes.slice(29, 46));
        controller.enqueue(bytes.slice(46));
        controller.close();
      }
    });
    const fetchMock = vi.fn().mockResolvedValue(new Response(body, { headers: { "Content-Type": "text/event-stream" } }));
    vi.stubGlobal("fetch", fetchMock);
    const events: Array<{ type: string; delta: string }> = [];
    await new ApiClient({ baseUrl: "https://grading.example.edu" }).requestEventStream("/api/v1/ai-chat/completions/stream", {
      method: "POST", body: "{}"
    }, (event) => events.push(event as { type: string; delta: string }));
    expect(events).toEqual([{ type: "reasoning", delta: "思考" }, { type: "content", delta: "答案" }]);
    expect(fetchMock.mock.calls[0]?.[1]?.headers.get("Accept")).toBe("text/event-stream");
  });

  it("preserves request, trace and field context from the shared error envelope", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({
      error: { code: "invalid_exam", message: "internal detail" },
      request_id: "req-web-1",
      trace_id: "trace-web-1",
      field_errors: { name: ["required"] }
    }), { status: 400, headers: { "Content-Type": "application/json" } })));
    const client = new ApiClient({ baseUrl: "https://grading.example.edu" });

    const error = await client.request("/api/v1/exams", { method: "POST", body: "{}" }).catch((value) => value);

    expect(error).toBeInstanceOf(ApiClientError);
    expect(error).toMatchObject({
      status: 400,
      code: "invalid_exam",
      requestId: "req-web-1",
      traceId: "trace-web-1",
      fieldErrors: { name: ["required"] }
    });
  });

  it("announces that a sensitive action needs recent authentication", async () => {
    const dispatchEvent = vi.fn();
    vi.stubGlobal("window", { dispatchEvent });
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(JSON.stringify({
      error: { code: "recent_auth_required", message: "internal detail" }
    }), { status: 428, headers: { "Content-Type": "application/json" } })));
    const client = new ApiClient({ baseUrl: "https://grading.example.edu" });

    const error = await client.request("/api/v1/users/user-1/status", { method: "PATCH", body: "{}" }).catch((value) => value);

    expect(error).toMatchObject({ status: 428, code: "recent_auth_required" });
    expect(dispatchEvent).toHaveBeenCalledTimes(1);
    expect(dispatchEvent.mock.calls[0]?.[0]).toMatchObject({ type: RECENT_AUTH_REQUIRED_EVENT });
  });
});

describe("Retry-After recovery delay", () => {
  it("accepts both delay seconds and HTTP dates", () => {
    const now = Date.parse("2026-09-07T00:00:00Z");
    expect(parseRetryAfterSeconds("15", now)).toBe(15);
    expect(parseRetryAfterSeconds("Mon, 07 Sep 2026 00:00:30 GMT", now)).toBe(30);
  });

  it("ignores missing, invalid and elapsed delays", () => {
    const now = Date.parse("2026-09-07T00:00:00Z");
    for (const value of [null, "", "invalid", "-1", "0", "Mon, 07 Sep 2026 00:00:00 GMT"]) {
      expect(parseRetryAfterSeconds(value, now)).toBeUndefined();
    }
  });
});
