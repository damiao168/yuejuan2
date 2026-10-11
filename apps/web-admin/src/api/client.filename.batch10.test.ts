import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "./client";

afterEach(() => vi.unstubAllGlobals());
describe("下载文件名解析", () => {
  it.each([
    [null, undefined], ["attachment", undefined], ['attachment; filename="paper.pdf"', "paper.pdf"],
    ["attachment; filename=paper.pdf; other=value", "paper.pdf"],
    ["attachment; filename=plain.pdf; filename*=UTF-8''%E8%AF%95%E5%8D%B7.pdf", "试卷.pdf"],
    ["attachment; FILENAME*=utf-8''%E8%AF%95.pdf", "试.pdf"],
    ["attachment; filename=plain.pdf; filename*=UTF-8''%ZZ.pdf", "%ZZ.pdf"]
  ])("保持响应头 %s 的解析与回退", async (disposition, expected) => {
    const headers = new Headers();
    if (disposition !== null) headers.set("Content-Disposition", disposition);
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("content", { headers })));
    const response = await new ApiClient({ baseUrl: "https://grading.example.edu" }).requestBlob("/api/v1/files/download");
    expect(response.filename).toBe(expected);
    expect(await response.blob.text()).toBe("content");
  });
});
