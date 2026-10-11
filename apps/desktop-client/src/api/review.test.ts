import { describe, expect, it, vi } from "vitest";
import { DesktopApiClient } from "./client";
import { downloadReviewTaskImage, getReviewCommand, getReviewTaskContext, submitHumanGrade } from "./review";

describe("offline grade submission", () => {
  it("reads the task-bound frozen context using the task identifier", async () => {
    const client = new DesktopApiClient({ baseUrl: "https://grading.example.edu" });
    const request = vi.spyOn(client, "request").mockResolvedValue({});
    await getReviewTaskContext(client, "task/1");
    expect(request).toHaveBeenCalledWith("/api/v1/review-tasks/task%2F1/context");
  });
  it("downloads only the same-server image path supplied by the task context", async () => {
    const client = new DesktopApiClient({ baseUrl: "https://grading.example.edu" });
    const requestBlob = vi.spyOn(client, "requestBlob").mockResolvedValue({ blob: new Blob(), contentType: "image/png", filename: undefined });
    await downloadReviewTaskImage(client, "/api/v1/review-tasks/task-1/segment-image", "task-1");
    expect(requestBlob).toHaveBeenCalledWith("/api/v1/review-tasks/task-1/segment-image");
    await downloadReviewTaskImage(client, "/api/v1/review-tasks/task-1/segment-image?v=crop-hash", "task-1");
    expect(requestBlob).toHaveBeenLastCalledWith("/api/v1/review-tasks/task-1/segment-image?v=crop-hash");
  });
  it.each([
    "https://other.example/image.png", "//other.example/image.png", "/api/v1/files/file-1/download",
    "/api/v1/submissions/submission-1/pages", "/api/v1/review-tasks/task-2/segment-image",
    "/api/v1/review-tasks/task-1/segment-image/../original-image", "/api/v1/review-tasks/task-1/segment-image#image"
  ])("rejects out-of-task image entry %s without sending credentials", async (imagePath) => {
    const client = new DesktopApiClient({ baseUrl: "https://grading.example.edu" });
    const requestBlob = vi.spyOn(client, "requestBlob");
    await expect(downloadReviewTaskImage(client, imagePath, "task-1")).rejects.toThrow("任务答卷图片地址无效");
    expect(requestBlob).not.toHaveBeenCalled();
  });
  it("sends the offline package revision to the server", async () => {
    const client = new DesktopApiClient({ baseUrl: "https://grading.example.edu" });
    const request = vi.spyOn(client, "request").mockResolvedValue({});
    await submitHumanGrade(client, "task-1", {
      expected_revision: 7, score: 3, rubric_selections: [], comments: "",
      private_note: "", student_feedback: "", reason: "offline sync"
    });
    expect(request).toHaveBeenCalledWith("/api/v1/review-tasks/task-1/submit", {
      method: "POST", body: expect.any(String)
    });
    expect(JSON.parse(request.mock.calls[0][1]!.body as string).expected_revision).toBe(7);
  });

  it("reuses the persisted command key and preserves the frozen request body", async () => {
    const client = new DesktopApiClient({ baseUrl: "https://grading.example.edu" });
    const request = vi.spyOn(client, "request").mockResolvedValue({});
    const payload = { expected_revision: 7, score: 0, rubric_selections: [], comments: "原评语",
      private_note: "原备注", student_feedback: "原反馈", reason: "offline sync" };
    await submitHumanGrade(client, "task/1", payload, "persisted-id");
    expect(request).toHaveBeenCalledWith("/api/v1/review-tasks/task%2F1/submit", {
      method: "POST", headers: { "Idempotency-Key": "persisted-id" }, body: JSON.stringify(payload)
    });
  });

  it("reads the original command receipt without starting another write", async () => {
    const client = new DesktopApiClient({ baseUrl: "https://grading.example.edu" });
    const request = vi.spyOn(client, "request").mockResolvedValue({ status: "processing" });
    await getReviewCommand(client, "fixed/id");
    expect(request).toHaveBeenCalledWith("/api/v1/review-commands/fixed%2Fid");
  });
});
