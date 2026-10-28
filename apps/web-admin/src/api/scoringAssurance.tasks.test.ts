import { afterEach, describe, expect, it, vi } from "vitest";
import { apiClient } from "./client";
import { listDisagreementReviewTasks } from "./scoringAssurance";

afterEach(() => vi.restoreAllMocks());
const source = { exam_id: "exam-a", question_id: "question-a", submission_id: "submission-a", answer_segment_id: "segment-a" };
const task = { ...source, id: "task-a", tenant_id: "school-a", question_no: "3", anonymous_code: "anonymous-a" };

describe("治理任务关联目录边界", () => {
  it("请求冻结学校与题目，结果仅保留同题同答卷任务，游标继续查找不扩大范围", async () => {
    const request = vi.spyOn(apiClient, "request").mockResolvedValue({ tasks: [task, { ...task, id: "another-answer", answer_segment_id: "segment-b" }, { ...task, id: "another-question", question_id: "question-b" }], has_more: true, next_cursor: "next-1" });
    const response = await listDisagreementReviewTasks("school-a", source, "cursor-a");
    const url = new URL(request.mock.calls[0][0], "https://app.example.test");
    expect(Object.fromEntries(url.searchParams)).toEqual({ tenant_id: "school-a", exam_id: "exam-a", question_id: "question-a", limit: "100", cursor: "cursor-a" });
    expect(response.tasks).toEqual([task]);
    expect(response.next_cursor).toBe("next-1");
  });
  it("异校结果不供用户选择，也不降级读取全校任务", async () => {
    const request = vi.spyOn(apiClient, "request").mockResolvedValue({ tasks: [{ ...task, tenant_id: "school-b" }], has_more: false });
    await expect(listDisagreementReviewTasks("school-a", source)).rejects.toThrow("不能读取所选学校");
    expect(request).toHaveBeenCalledOnce();
  });
  it("权限拒绝保留为失败，不重试更宽范围", async () => {
    const request = vi.spyOn(apiClient, "request").mockRejectedValue(new Error("forbidden"));
    await expect(listDisagreementReviewTasks("school-a", source)).rejects.toThrow("forbidden");
    expect(request).toHaveBeenCalledOnce();
  });
});
