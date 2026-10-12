import type { BackmarkSelector, GradingQualityIncidentListResponse, WorkflowActorsResponse } from "@edugrade/sdk";
import type { Page } from "@playwright/test";
import { installQualityDashboardMocks } from "./qualityDashboardMocks";

// 考试目录一次返回已授权人员；第 201 位仍可选择，不能退回全校 /users 或截断为旧目录第一页。
export async function installQualityWorkflowMocks(page: Page, initialStatus: "active" | "paused" = "paused") {
  const state = await installQualityDashboardMocks(page, initialStatus);
  const actorRequests: string[] = [];
  const previewRequests: Array<{ questionID: string; selector: BackmarkSelector }> = [];
  const actors = [
    ...Array.from({ length: 200 }, (_, index) => ({ id: `grader-${index}`, username: `grader-${index}`, display_name: `阅卷员 ${index + 1}` })),
    { id: "grader-last", username: "grader-last", display_name: "第二页阅卷员" }
  ] satisfies WorkflowActorsResponse["actors"];
  const incidents = ["question-1", "question-2"].map((questionID, index) => ({
    id: `incident-${index + 1}`, exam_id: "exam-1", question_id: questionID, grader_id: "grader-0", source_window_id: `window-${index + 1}`,
    type: "grader_bias_high" as const, severity: "critical" as const, metric_snapshot: { sample_count: 20 }, affected_range: {},
    status: "open" as const, created_at: "2026-09-15T01:00:00Z"
  })) satisfies GradingQualityIncidentListResponse["incidents"];
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    if (url.pathname === "/api/v1/exams/exam-1/review-actors") {
      actorRequests.push("exam-1");
      return route.fulfill({ json: { actors } satisfies WorkflowActorsResponse });
    }
    if (url.pathname === "/api/v1/grading-quality-incidents" && url.searchParams.get("exam_id") === "exam-1") {
      return route.fulfill({ json: { incidents } satisfies GradingQualityIncidentListResponse });
    }
    const preview = url.pathname.match(/^\/api\/v1\/exams\/exam-1\/questions\/([^/]+)\/backmark-preview$/);
    if (preview) previewRequests.push({ questionID: preview[1], selector: route.request().postDataJSON().selector });
    return route.fallback();
  });
  return Object.assign(state, { actorRequests, previewRequests });
}
