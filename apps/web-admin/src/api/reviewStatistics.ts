import { EduGradeApi } from "@edugrade/sdk";
import { apiClient } from "./client";
export type { ReviewQuestionStatistics, ReviewStatistics } from "@edugrade/sdk";

const api = new EduGradeApi(apiClient);

/** 逐题平均分沿用服务器身份与学校范围，不从本地已加载的答卷页面推算。 */
export async function getReviewStatistics(examId: string, actorId: string, personalScope: boolean, signal?: AbortSignal) {
  const query = { exam_id: examId, ...(personalScope ? { assigned_to: actorId } : {}), limit: 1, statistics: true };
  const page = await api.listReviewTasks({ query, signal });
  const statistics = page.aggregate?.statistics;
  if (!statistics || statistics.exam_id !== examId || statistics.scope !== (personalScope ? "personal" : "exam")) {
    throw new Error("当前考试统计暂不可读取，请重新读取");
  }
  return statistics;
}
