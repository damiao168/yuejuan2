import { describe, expect, it, vi } from "vitest";
import { DesktopApiClient } from "./client";
import { submitHumanGrade } from "./review";

describe("offline grade submission", () => {
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
});
