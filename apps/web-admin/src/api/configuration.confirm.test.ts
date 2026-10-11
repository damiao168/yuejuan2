import { afterEach, describe, expect, it, vi } from "vitest";
import { confirmExamReadiness } from "./configuration";

afterEach(() => vi.unstubAllGlobals());

describe("准备冻结正式契约", () => {
  it("提交管理员复核的 hash，防止预读后改动被无提示冻结", async () => {
    const fetch = vi.fn().mockResolvedValue(new Response(JSON.stringify({ readiness: { confirmed: true } })));
    vi.stubGlobal("fetch", fetch);
    await confirmExamReadiness("exam/1", "reviewed-hash");
    const [url, request] = fetch.mock.calls[0];
    expect(url).toContain("/exams/exam%2F1/readiness/confirm");
    expect(request.method).toBe("POST");
    expect(JSON.parse(request.body)).toEqual({ expected_configuration_hash: "reviewed-hash" });
  });
});
