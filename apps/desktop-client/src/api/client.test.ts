import { readFileSync } from "node:fs";
import { describe, expect, it, vi } from "vitest";
import { ApiClientError, DesktopApiClient, normalizeBaseUrl } from "./client";

describe("desktop API origin validation", () => {
  it("allows HTTPS servers and loopback HTTP development servers", () => {
    expect(normalizeBaseUrl("https://grading.example.edu/"))
      .toBe("https://grading.example.edu");
    expect(normalizeBaseUrl("http://127.0.0.1:8080/"))
      .toBe("http://127.0.0.1:8080");
    expect(normalizeBaseUrl("http://localhost:8080"))
      .toBe("http://localhost:8080");
  });

  it("rejects remote plaintext HTTP and embedded credentials", () => {
    expect(() => normalizeBaseUrl("http://grading.example.edu"))
      .toThrow("远程 API 必须使用 HTTPS");
    expect(() => normalizeBaseUrl("https://user:password@grading.example.edu"))
      .toThrow("不能包含用户名或密码");
  });

  it.each([
    "http://127.255.255.255:8080", "http://127.1:8080", "http://[::1]:8080",
    "http://[0:0:0:0:0:0:0:1]:8080", "http://LOCALHOST:8080"
  ])("allows actual loopback address %s after URL normalization", (address) => {
    expect(normalizeBaseUrl(address)).toBe(new URL(address).origin);
  });

  it.each([
    "http://127.review.example", "http://127.0.0.1.review.example",
    "http://127.0.0.1.review.example.", "http://localhost.review.example",
    "http://128.0.0.1", "http://126.255.255.255", "http://[::2]",
    "http://[::ffff:127.0.0.1]"
  ])("rejects plaintext non-loopback address %s", (address) => {
    expect(() => normalizeBaseUrl(address)).toThrow("远程 API 必须使用 HTTPS");
  });

  it("rejects API paths that could replace the configured origin", () => {
    const client = new DesktopApiClient({ baseUrl: "https://grading.example.edu" });

    expect(() => client.url("//attacker.example/api"))
      .toThrow("same-server absolute path");
    expect(client.url("/api/v1/auth/me"))
      .toBe("https://grading.example.edu/api/v1/auth/me");
  });

  it("preserves diagnostics from the shared Go response sample", async () => {
    const client = new DesktopApiClient({ baseUrl: "https://grading.example.edu" });
    const response = readFileSync(new URL("../../../../contracts/samples/error.response.json", import.meta.url), "utf8");
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(response, {
      status: 409,
      headers: { "Content-Type": "application/json" }
    })));

    const error = await client.request("/api/v1/exams/exam-1", { method: "PUT", body: "{}" }).catch((value) => value);
    expect(error).toBeInstanceOf(ApiClientError);
    expect(error).toMatchObject({
      status: 409,
      code: "revision_conflict",
      requestId: "req-contract-001",
      traceId: "trace-contract-001"
    });
  });
});
