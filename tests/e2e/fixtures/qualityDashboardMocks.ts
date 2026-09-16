import type { Page, Route } from "@playwright/test";
import type { BackmarkBatch, BackmarkPreviewResponse, BackmarkSummaryResponse, CreateBackmarkBatchRequest, QualityDashboardResponse } from "@edugrade/sdk";
import type { SeedPolicy } from "../../../apps/web-admin/src/api/qualityOperations";
import { installApiMocks } from "./apiMocks";

const now = "2026-09-15T01:00:00Z";
const ratio = { numerator: 0, denominator: 0, sample_size: 0 };
const dashboard = {
  dashboard: {
    exam_id: "exam-1", gate: "ready", blocking: [], warnings: [], generated_at: now,
    questions: ["question-1", "question-2"].map((id, i) => ({
      question: { id, question_no: `Q${i + 1}`, archetype_code: "structured_steps", risk_tier: "R2", max_score: 100 },
      gate: "ready" as const,
      gold: { active_approved: 0, ready: true, gaps: [], score_bands: [] },
      calibration: { configured: false, completed: 0, passed: 0, pass_rate: ratio, graders: [] },
      seed: { policy_status: "paused", sample_size: 0, exact_agreement: ratio, within_one_agreement: ratio, criterion_agreement: ratio, graders: [] },
      human_human_agreement: { sample_size: 0, within_rule_agreement: ratio, open_cases: 0 },
      answer_groups: { group_count: 0, member_count: 0, open_sample_groups: 0 },
      drift: { open_warnings: 0, open_critical: 0, incidents: [] },
      backmark: { open_batches: 1, pending_items: 0, completed_items: 120, correction_rate: ratio },
      findings: []
    }))
  }
} satisfies QualityDashboardResponse;

const batch: BackmarkBatch = {
  id: "batch-1", exam_id: "exam-1", question_id: "question-1", source_incident_id: "incident-1", selector: {},
  policy: { disposition: "regrade" }, affected_count: 120, status: "ready_for_confirmation",
  created_by: "admin", created_at: now, updated_at: now
};

export interface QualityDashboardMockState {
  policy: SeedPolicy;
  policyWrites: Array<{ status: SeedPolicy["status"] }>;
  previews: number;
  previewWait?: Promise<void>;
  creates: CreateBackmarkBatchRequest[];
  rejectCreateAsStale: boolean;
  userCursors: string[];
  summaryFilters: string[];
}

function json(route: Route, body: unknown, status = 200) {
  return route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
}

export async function installQualityDashboardMocks(page: Page, policyStatus: SeedPolicy["status"] = "paused") {
  await installApiMocks(page, { role: "school_admin", initiallyAuthenticated: true });
  const state: QualityDashboardMockState = {
    policy: { id: "policy-1", exam_id: "exam-1", question_id: "question-1", rate: .1, min_interval: 10, max_interval: 30, status: policyStatus, revision: 1 },
    policyWrites: [], previews: 0, creates: [], rejectCreateAsStale: false, userCursors: [], summaryFilters: []
  };
  await page.route("**/api/v1/**", async (route) => {
    const url = new URL(route.request().url());
    const path = url.pathname;
    const method = route.request().method();
    if (path.endsWith("/quality-dashboard")) return json(route, dashboard);
    if (path.endsWith("/seed-policy")) {
      if (method === "PUT") {
        const body = route.request().postDataJSON();
        state.policyWrites.push(body);
        state.policy = { ...state.policy, ...body, revision: state.policy.revision + 1 };
      }
      return json(route, { policy: state.policy });
    }
    if (path === "/api/v1/users") {
      const cursor = url.searchParams.get("cursor") ?? "";
      state.userCursors.push(cursor);
      const grader = (id: string) => ({ id, username: id, display_name: id, status: "active", roles: ["grader"] });
      return cursor ? json(route, { users: [{ ...grader("grader-last"), display_name: "第二页阅卷员" }], has_more: false, next_cursor: "" })
        : json(route, { users: Array.from({ length: 200 }, (_, i) => grader(`grader-${i}`)), has_more: true, next_cursor: "users-page-2" });
    }
    if (path === "/api/v1/backmark-batches") return json(route, { backmark_batches: [batch], has_more: false, next_cursor: "" });
    if (path.endsWith("/backmark-preview")) {
      state.previews++;
      await state.previewWait;
      return json(route, { preview: { affected_count: 20, score_bands: [{ score: 80, count: 20 }], time_range: { from: now, to: now }, selector_hash: "a".repeat(64) } } satisfies BackmarkPreviewResponse);
    }
    if (path.endsWith("/backmark-batches") && method === "POST") {
      const body = route.request().postDataJSON() as CreateBackmarkBatchRequest;
      state.creates.push(body);
      if (state.rejectCreateAsStale) return json(route, { error: { code: "backmark_preview_stale", message: "stale" } }, 409);
      return json(route, { backmark: { batch, items: [], diff_histogram: [], regrade_required_count: 0, pending_count: 20, completed_count: 0 }, next_cursor: "", has_more: false } satisfies BackmarkSummaryResponse);
    }
    if (path === "/api/v1/backmark-batches/batch-1") {
      state.summaryFilters.push(url.searchParams.get("status") ?? "");
      // An unloaded/empty detail page must not hide a nonzero batch count.
      return json(route, { backmark: { batch, items: [], diff_histogram: [], regrade_required_count: 8, pending_count: 0, completed_count: 120 }, next_cursor: "items-page-2", has_more: true } satisfies BackmarkSummaryResponse);
    }
    if (path.endsWith("/score-releases")) return json(route, { score_releases: [], next_cursor: "", has_more: false });
    return route.fallback();
  });
  return state;
}
