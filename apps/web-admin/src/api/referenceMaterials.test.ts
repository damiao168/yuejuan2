import { afterEach, describe, expect, it, vi } from "vitest";
import { apiClient } from "./client";
import { createReferenceCrop, resumeReferenceCommand } from "./referenceMaterials";

afterEach(() => vi.restoreAllMocks());

describe("参考材料命令 HTTP 边界", () => {
  it("resumes only the command ID under the exact exam and question path", async () => {
    const request = vi.spyOn(apiClient, "request").mockResolvedValue({ command: { command_id: "command/id", status: "rejected" } });
    await resumeReferenceCommand("exam/id", "question/id", "command/id");
    expect(request).toHaveBeenCalledWith("/api/v1/exams/exam%2Fid/questions/question%2Fid/reference-material-commands/command%2Fid/resume", { method: "POST" });
  });
  it("uses one command ID for both crop input and idempotency header", async () => {
    const request = vi.spyOn(apiClient, "request").mockResolvedValue({ crop: {} });
    const input = { command_id: "opaque-command", page_ref: "opaque-page", page_sha256: "hash", page_width: 1200, page_height: 1000, pixel_bbox: { x: 10, y: 20, width: 300, height: 200 } };
    await createReferenceCrop("exam", "question", input);
    expect(request).toHaveBeenCalledWith("/api/v1/exams/exam/questions/question/reference-material-crops", { method: "POST", headers: { "Idempotency-Key": input.command_id }, body: JSON.stringify(input) });
  });
});
