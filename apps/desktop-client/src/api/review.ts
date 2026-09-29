import type { AiGrade, HumanGrade, ReviewTask, RubricSelection } from "../types";
import type { DesktopApiClient } from "./client";

export interface ReviewTaskFilter {
  status?: string;
  assigned_to?: string;
}

export async function listReviewTasks(client: DesktopApiClient, filter: ReviewTaskFilter = {}) {
  const params = new URLSearchParams();
  if (filter.status) {
    params.set("status", filter.status);
  }
  if (filter.assigned_to) {
    params.set("assigned_to", filter.assigned_to);
  }
  const query = params.toString();
  return client.request<{ tasks: ReviewTask[] }>(`/api/v1/review-tasks${query ? `?${query}` : ""}`);
}

export async function getReviewTask(client: DesktopApiClient, id: string) {
  return client.request<{ task: ReviewTask }>(`/api/v1/review-tasks/${encodeURIComponent(id)}`);
}

export async function listAiGrades(client: DesktopApiClient, segmentId: string) {
  return client.request<{ grades: AiGrade[] }>(`/api/v1/answer-segments/${encodeURIComponent(segmentId)}/ai-grades`);
}

export interface SubmitHumanGradePayload {
  // 必须使用下载任务包时的版本；服务端据此阻止旧草稿覆盖后续评分。
  expected_revision: number;
  score: number;
  rubric_selections: RubricSelection[];
  comments: string;
  private_note: string;
  student_feedback: string;
  reason: string;
}

export async function submitHumanGrade(client: DesktopApiClient, taskId: string, payload: SubmitHumanGradePayload) {
  return client.request<{ task: ReviewTask; human_grade: HumanGrade }>(`/api/v1/review-tasks/${encodeURIComponent(taskId)}/submit`, {
    method: "POST",
    body: JSON.stringify(payload)
  });
}
