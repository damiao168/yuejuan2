import type { Page, Route } from "@playwright/test";
import { emptyBackmarkBatchPage } from "./backmarkContractFixtures";
import { installApiMocks } from "./apiMocks";

const now = "2026-08-13T08:00:00Z";

const subjectFixtures = [
  { code: "chinese", label: "语文", archetype: "extended_response", evidence: ["text_span"], tool: "全文与分项评价" },
  { code: "mathematics", label: "数学", archetype: "structured_steps", evidence: ["math_step", "unit_value"], tool: "公式与步骤核对" },
  { code: "physics", label: "物理", archetype: "structured_steps", evidence: ["math_step", "unit_value"], tool: "公式与步骤核对" },
  { code: "history", label: "历史", archetype: "short_constructed", evidence: ["concept", "relation"], tool: "材料证据核对" }
] as const;

type FixtureSubject = (typeof subjectFixtures)[number];

interface FixtureTask {
  id: string;
  tenant_id: string;
  exam_id: string;
  question_id: string;
  question_no: string;
  answer_segment_id: string;
  submission_id: string;
  anonymous_code: string;
  source: string;
  status: string;
  priority: number;
  assigned_to: string;
  grade_round: string;
  revision: number;
  created_by: string;
  created_at: string;
  updated_at: string;
  subject: FixtureSubject;
}

interface FixtureDraft {
  id: string;
  review_task_id: string;
  reviewer_id: string;
  score?: number;
  rubric_selections: Array<{ point_id: string; score: number }>;
  comments: string;
  private_note: string;
  student_feedback: string;
  viewer_state: Record<string, unknown>;
  revision: number;
  client_updated_at?: string;
  updated_at: string;
}

export interface GradingWorkbenchMockState {
  tasks: FixtureTask[];
  drafts: Map<string, FixtureDraft>;
  claims: string[];
  submissions: string[];
  draftWrites: string[];
  contextsRead: string[];
  listCursors: string[];
}

export function createGradingWorkbenchMockState(count = 20): GradingWorkbenchMockState {
  const tasks = Array.from({ length: count }, (_, index) => {
    const subject = subjectFixtures[index % subjectFixtures.length];
    const number = String(index + 1).padStart(2, "0");
    return {
      id: `review-task-${number}`,
      tenant_id: "tenant-school",
      exam_id: "exam-keyboard-fixtures",
      question_id: `question-${number}`,
      question_no: `Q${number}`,
      answer_segment_id: `segment-${number}`,
      submission_id: `submission-${number}`,
      anonymous_code: `fixture-${number}`,
      source: "manual_review",
      status: "assigned",
      priority: 50,
      assigned_to: "user-grader",
      grade_round: "first",
      revision: 1,
      created_by: "user-school_admin",
      created_at: now,
      updated_at: now,
      subject
    } satisfies FixtureTask;
  });
  return { tasks, drafts: new Map(), claims: [], submissions: [], draftWrites: [], contextsRead: [], listCursors: [] };
}

function json(route: Route, body: unknown, status = 200) {
  return route.fulfill({ status, contentType: "application/json", body: JSON.stringify(body) });
}

function requestBody(route: Route): Record<string, unknown> {
  const body = route.request().postData();
  if (!body) return {};
  return JSON.parse(body) as Record<string, unknown>;
}

function contextFor(task: FixtureTask, draft: FixtureDraft | undefined) {
  const { subject } = task;
  return {
    task,
    expected_revision: task.revision,
    question_snapshot: {
      id: `snapshot-${task.id}`,
      tenant_id: task.tenant_id,
      exam_id: task.exam_id,
      question_id: task.question_id,
      snapshot_version: 1,
      subject_profile_id: `profile-${subject.code}`,
      subject_profile_code: `${subject.code}-junior`,
      subject_profile_version: 1,
      education_stage: "junior",
      subject_code: subject.code,
      archetype_code: subject.archetype,
      allowed_evidence_types: subject.evidence,
      risk_tier: "R2",
      profile_snapshot: {},
      archetype_snapshot: {},
      rubric_snapshot: {},
      scoring_policy_snapshot: {},
      content_hash: `hash-${task.id}`,
      created_at: now
    },
    question: {
      id: task.question_id,
      exam_id: task.exam_id,
      question_no: task.question_no,
      question_type: subject.archetype,
      score: 9,
      stem: `${subject.label} 键盘阅卷夹具 ${task.question_no}`,
      knowledge_points: []
    },
    answer_artifact: {
      answer_segment_id: task.answer_segment_id,
      source: "mock",
      raw_answer: `${subject.label} 作答`,
      ocr_text: `${subject.label} 作答`,
      status: "ready",
      confidence: 0.99,
      // Keyboard/revision flows do not require a rendered image. Keeping this
      // empty also prevents image-fit state from creating an unrelated draft
      // write before either browser window starts the conflict scenario.
      segment_image_url: ""
    },
    frozen_rubric: {
      id: `rubric-${task.id}`,
      question_id: task.question_id,
      version: "1",
      status: "frozen",
      max_score: 9,
      points: []
    },
    ai_candidates: [],
    scoring_evidence: [],
    claim: {
      owner_id: task.assigned_to,
      state: task.status === "in_progress" ? "claimed" : "assigned",
      claimed_at: task.status === "in_progress" ? now : undefined,
      expires_at: "2026-08-13T09:00:00Z",
      can_renew: true
    },
    draft: draft ?? null,
    subject_tool_hints: {
      subject_code: subject.code,
      archetype_code: subject.archetype,
      allowed_evidence_types: subject.evidence,
      parser_policy: {},
      evidence_policy: {},
      response_schema: {}
    }
  };
}

function taskFromPath(path: string, tasks: FixtureTask[]) {
  const match = path.match(/^\/api\/v1\/review-tasks\/([^/]+)(?:\/|$)/);
  return match ? tasks.find((task) => task.id === decodeURIComponent(match[1])) : undefined;
}

/**
 * Adds a stateful review-task API on top of the product-wide auth/navigation
 * mocks. The explicit revision check lets two independent browser contexts
 * exercise the same optimistic-locking contract the workbench uses in production.
 */
export async function installGradingWorkbenchMocks(page: Page, state = createGradingWorkbenchMockState()) {
  await installApiMocks(page, { role: "grader", initiallyAuthenticated: true });

  await page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const method = request.method();
    const url = new URL(request.url());
    const path = url.pathname;

    if (path.startsWith("/api/v1/review-assets/") && method === "GET") {
      return route.fulfill({
        status: 200,
        contentType: "image/svg+xml",
        body: '<svg xmlns="http://www.w3.org/2000/svg" width="600" height="320"><rect width="100%" height="100%" fill="#fff"/><text x="40" y="80" font-size="28">fixture answer</text></svg>'
      });
    }

    if (path === "/api/v1/review-tasks" && method === "GET") {
      const assignedTo = url.searchParams.get("assigned_to");
      const examID = url.searchParams.get("exam_id");
      const status = url.searchParams.get("status");
      const cursor = url.searchParams.get("cursor") ?? "";
      const limit = Math.max(1, Math.min(Number(url.searchParams.get("limit") ?? 50), 200));
      state.listCursors.push(cursor);
      const tasks = state.tasks.filter((task) =>
        (!assignedTo || task.assigned_to === assignedTo) && (!examID || task.exam_id === examID) && (!status || task.status === status)
      );
      const aggregate = {
        total_count: tasks.length,
        completed_count: tasks.filter((task) => ["submitted", "completed"].includes(task.status)).length,
        remaining_count: tasks.filter((task) => !["submitted", "completed"].includes(task.status)).length,
        status_counts: Object.fromEntries([...new Set(tasks.map((task) => task.status))].map((value) => [value, tasks.filter((task) => task.status === value).length])),
        reviewers: [...new Set(tasks.map((task) => task.assigned_to).filter(Boolean))].map((reviewerId) => {
          const assigned = tasks.filter((task) => task.assigned_to === reviewerId);
          const completed = assigned.filter((task) => ["submitted", "completed"].includes(task.status)).length;
          return { reviewer_id: reviewerId, total_count: assigned.length, completed_count: completed, remaining_count: assigned.length - completed };
        })
      };
      const anchor = cursor ? state.tasks.findIndex((task) => task.id === cursor) : -1;
      const remaining = state.tasks.slice(anchor + 1).filter((task) =>
        (!assignedTo || task.assigned_to === assignedTo) && (!examID || task.exam_id === examID) && (!status || task.status === status)
      );
      const page = remaining.slice(0, limit);
      const hasMore = remaining.length > page.length;
      return json(route, { tasks: page, aggregate, next_cursor: hasMore ? page[page.length - 1]?.id ?? "" : "", has_more: hasMore });
    }

    if (path === "/api/v1/review-tasks/next" && method === "POST") {
      const body = requestBody(route);
      const task = state.tasks.find((item) => item.status !== "submitted" &&
        (!body.exam_id || item.exam_id === body.exam_id) && (!body.question_id || item.question_id === body.question_id));
      if (!task) return json(route, { error: { code: "review_task_not_found", message: "没有可领取的任务" } }, 404);
      task.status = "in_progress";
      task.revision += 1;
      task.updated_at = now;
      state.claims.push(task.id);
      return json(route, { task });
    }

    const task = taskFromPath(path, state.tasks);
    if (task && path.endsWith("/context") && method === "GET") {
      state.contextsRead.push(task.id);
      return json(route, { context: contextFor(task, state.drafts.get(task.id)) });
    }
    if (task && path.endsWith("/renew") && method === "POST") return json(route, { renewed: true });

    if (task && path.endsWith("/draft") && method === "PUT") {
      const body = requestBody(route);
      const current = state.drafts.get(task.id);
      const expectedRevision = Number(body.expected_revision ?? 0);
      const currentRevision = current?.revision ?? 0;
      if (expectedRevision !== currentRevision) {
        return json(route, {
          error: { code: "revision_conflict", message: "草稿已被其他会话更新" },
          conflict_revision: currentRevision
        }, 409);
      }
      const draft: FixtureDraft = {
        id: current?.id ?? `draft-${task.id}`,
        review_task_id: task.id,
        reviewer_id: "user-grader",
        score: typeof body.score === "number" ? body.score : undefined,
        rubric_selections: Array.isArray(body.rubric_selections) ? body.rubric_selections as FixtureDraft["rubric_selections"] : [],
        comments: typeof body.comments === "string" ? body.comments : "",
        private_note: typeof body.private_note === "string" ? body.private_note : "",
        student_feedback: typeof body.student_feedback === "string" ? body.student_feedback : "",
        viewer_state: body.viewer_state && typeof body.viewer_state === "object" ? body.viewer_state as Record<string, unknown> : {},
        revision: currentRevision + 1,
        client_updated_at: typeof body.client_updated_at === "string" ? body.client_updated_at : undefined,
        updated_at: now
      };
      state.drafts.set(task.id, draft);
      state.draftWrites.push(task.id);
      return json(route, { draft });
    }

    if (task && path.endsWith("/submit") && method === "POST") {
      const body = requestBody(route);
      task.status = "submitted";
      task.revision += 1;
      task.updated_at = now;
      state.submissions.push(task.id);
      return json(route, {
        task,
        human_grade: {
          id: `grade-${task.id}`,
          tenant_id: task.tenant_id,
          review_task_id: task.id,
          answer_segment_id: task.answer_segment_id,
          reviewer_id: "user-grader",
          score: Number(body.score),
          max_score: 9,
          rubric_selections: Array.isArray(body.rubric_selections) ? body.rubric_selections : [],
          comments: typeof body.comments === "string" ? body.comments : "",
          private_note: typeof body.private_note === "string" ? body.private_note : "",
          student_feedback: typeof body.student_feedback === "string" ? body.student_feedback : "",
          reason: typeof body.reason === "string" ? body.reason : "",
          grade_round: "first",
          created_at: now
        }
      });
    }

    if (path === "/api/v1/review-annotations" && method === "GET") return json(route, { annotations: [] });
    if (path === "/api/v1/backmark-batches" && method === "GET") return json(route, emptyBackmarkBatchPage);
    if (path === "/api/v1/regrade-jobs" && method === "GET") return json(route, { jobs: [], next_cursor: "", has_more: false });

    return route.fallback();
  });

  return state;
}

export { subjectFixtures };
