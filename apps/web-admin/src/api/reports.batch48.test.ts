// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { exportLearningReport } from "./reports";
import { setBusinessCommandScope } from "./businessCommand";

const api = vi.hoisted(() => ({ request: vi.fn(), requestBlob: vi.fn() }));
vi.mock("./client", async (original) => ({
  ...await original<typeof import("./client")>(),
  apiClient: api,
}));

function blobBytes(blob: Blob): Promise<number[]> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(Array.from(new Uint8Array(reader.result as ArrayBuffer)));
    reader.onerror = () => reject(reader.error);
    reader.readAsArrayBuffer(blob);
  });
}

describe("报表下载与真实命令恢复入口", () => {
  beforeEach(() => {
    localStorage.clear();
    vi.clearAllMocks();
    setBusinessCommandScope("school", "teacher");
  });
  afterEach(() => setBusinessCommandScope("", ""));

  function pending(content: string) {
    localStorage.setItem("business-command:school:teacher:report.export:exam", JSON.stringify({ id: "command" }));
    api.request.mockResolvedValue({ command_id: "command", status: "succeeded", result: {
      Content: content, ContentType: "application/octet-stream", Filename: "成绩报表.bin", Watermark: "school:teacher",
    } });
  }

  it("首次下载保留 Blob 并发送考试路径和命令 ID", async () => {
    const download = { blob: new Blob(["report"]), contentType: "text/plain", filename: "report.txt", watermark: "school" };
    api.requestBlob.mockResolvedValue(download);
    expect(await exportLearningReport("exam/one")).toBe(download);
    expect(api.requestBlob).toHaveBeenCalledWith("/api/v1/exams/exam%2Fone/reports/export", {
      method: "POST", headers: { "Idempotency-Key": expect.any(String) },
    });
    expect(api.request).not.toHaveBeenCalled();
  });

  it("成功回执还原全部字节和元数据，不重新提交导出", async () => {
    const bytes = Array.from({ length: 256 }, (_, index) => index);
    pending(btoa(String.fromCharCode(...bytes)));
    const result = await exportLearningReport("exam");
    expect(await blobBytes(result.blob)).toEqual(bytes);
    expect(result).toMatchObject({ contentType: "application/octet-stream", filename: "成绩报表.bin", watermark: "school:teacher" });
    expect(api.request).toHaveBeenCalledWith("/api/v1/report-commands/command");
    expect(api.requestBlob).not.toHaveBeenCalled();
    expect(localStorage.getItem("business-command:school:teacher:report.export:exam")).toBeNull();
  });

  it("空文件回执仍可还原为零字节 Blob", async () => {
    pending("");
    expect((await exportLearningReport("exam")).blob.size).toBe(0);
    expect(api.requestBlob).not.toHaveBeenCalled();
  });

  it("损坏的 Base64 保留原命令记录且不重新导出", async () => {
    pending("%invalid%");
    await expect(exportLearningReport("exam")).rejects.toThrow();
    expect(api.requestBlob).not.toHaveBeenCalled();
    expect(localStorage.getItem("business-command:school:teacher:report.export:exam")).toBe(JSON.stringify({ id: "command" }));
  });
});
