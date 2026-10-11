import type { AiGrade, HumanGrade, ReviewTask, RubricSelection } from "../types";
import type { DesktopApiClient } from "./client";
import type { BusinessCommandReceipt, ReviewTaskContext } from "@edugrade/sdk";

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
  const querySuffix = query ? `?${query}` : "";
  return client.request<{ tasks: ReviewTask[] }>(`/api/v1/review-tasks${querySuffix}`);
}

export async function getReviewTask(client: DesktopApiClient, id: string) {
  return client.request<{ task: ReviewTask }>(`/api/v1/review-tasks/${encodeURIComponent(id)}`);
}

// 离线包和同步预检都读取任务绑定的冻结题目及细则，不回查可继续编辑的考试题库。
export async function getReviewTaskContext(client: DesktopApiClient, id: string) {
  return client.request<{ context: ReviewTaskContext }>(`/api/v1/review-tasks/${encodeURIComponent(id)}/context`);
}

/**
 * 下载任务上下文返回的受控答卷图像地址。
 * 该地址由 review-task context 按当前阅卷人权限生成，只允许当前任务的片段图片入口，
 * 因而离线端不会为了找图像回查提交、答题片段或文件管理接口。
 */
export async function downloadReviewTaskImage(client: DesktopApiClient, imagePath: string, taskId: string) {
  const expectedPath = `/api/v1/review-tasks/${encodeURIComponent(taskId)}/segment-image`;
  // 图片版本可放在查询参数中；旧包里保留的文件地址和其他任务地址不能携带本次登录凭据读取。
  if (!taskId || imagePath.split("?")[0] !== expectedPath || imagePath.includes("#") || imagePath.includes("\\")) {
    throw new Error("任务答卷图片地址无效，请重新读取任务材料。");
  }
  return client.requestBlob(imagePath);
}

/** AI 评分仅作为当前阅卷任务的辅助证据，不能替代教师填写最终分。 */
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

export async function submitHumanGrade(client: DesktopApiClient, taskId: string, payload: SubmitHumanGradePayload, commandId?: string) {
  return client.request<{ task: ReviewTask; human_grade: HumanGrade }>(`/api/v1/review-tasks/${encodeURIComponent(taskId)}/submit`, {
    method: "POST",
    ...(commandId ? { headers: { "Idempotency-Key": commandId } } : {}),
    body: JSON.stringify(payload)
  });
}

export function getReviewCommand(client: DesktopApiClient, commandId: string) {
  return client.request<BusinessCommandReceipt>(`/api/v1/review-commands/${encodeURIComponent(commandId)}`);
}
