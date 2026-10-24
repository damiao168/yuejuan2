import { beforeEach, describe, expect, it, vi } from "vitest";
import type { QueryClient } from "@tanstack/react-query";
import type { AiGrade, ReviewTask, ReviewTaskContext } from "../../../api/review";
import type { SubjectiveGradingBatch } from "../../../api/subjectiveGrading";
import type { ReviewTaskContextScope } from "../../../query/reviewTaskContext";
import { assignmentGroupKey, assignmentGroups } from "./assignmentPlan";
import { loadTaskContext } from "./gradingTaskContext";
import { gradingShortcutIntent, type GradingShortcutContext } from "./keyboardShortcuts";
import { launchScoringAIBatches } from "./scoringAIBatches";
import type { ScoringPlan } from "./scoringPlan";

const mocks = vi.hoisted(() => ({ assertScope: vi.fn(), list: vi.fn(), create: vi.fn(), enqueue: vi.fn() }));

vi.mock("../../../query/reviewTaskContext", () => ({
  assertReviewTaskContextScope: mocks.assertScope,
  reviewTaskContextQueryOptions: (_scope: unknown, taskId: string) => ({ queryKey: ["batch28-context", taskId] })
}));
vi.mock("../../../api/subjectiveGrading", () => ({
  getScoringRunAIBatches: mocks.list,
  createSubjectiveGradingBatch: mocks.create,
  enqueueSubjectiveGradingBatch: mocks.enqueue
}));

function aiGrade(id: string, overrides: Partial<AiGrade> = {}): AiGrade {
  return {
    id, tenant_id: "tenant", answer_segment_id: "segment", question_id: "question", question_no: "1",
    question_type: "essay", grader_type: "ai", suggested_score: 3, max_score: 5, confidence: 0.8,
    matched_points: [], missing_points: [], evidence: [], risk_flags: [], needs_human_review: true,
    mock: false, created_by: "grader", created_at: "", ...overrides
  };
}

function contextFixture(metadata: Record<string, unknown>, history: unknown[] = []): ReviewTaskContext {
  return {
    task: { id: "task", answer_segment_id: "segment", tenant_id: "tenant", submission_id: "submission", question_id: "question", question_no: "1", created_at: "" },
    question_snapshot: { id: "snapshot", risk_tier: "R2", scoring_policy_snapshot: { mode: "AI_ASSIST" } },
    question: { id: "question", exam_id: "exam", question_no: "1", question_type: "essay", score: 5 },
    frozen_rubric: { points: [] },
    answer_artifact: { status: "ready", segment_image_url: "/segment", original_image_url: "/original" },
    ai_second_opinion: { available: true, presentation: "suggestion", score_prefill_allowed: true, metadata, history }
  } as unknown as ReviewTaskContext;
}

async function loadedContext(context: ReviewTaskContext) {
  const scope = { examId: "exam", allowOriginalImage: false } as ReviewTaskContextScope;
  const queryClient = { fetchQuery: vi.fn().mockResolvedValue(context) } as unknown as QueryClient;
  return loadTaskContext(queryClient, scope, "task");
}

function shortcut(overrides: Partial<GradingShortcutContext>): GradingShortcutContext {
  return {
    key: "", isInputTarget: false, hasTask: true, canEditDraft: true, canSubmit: true, canReturn: true, canToggleBookmark: true,
    canUndoScoreChange: true, hasAiSuggestion: true, maxScore: 9, rubricPointCount: 0, ...overrides
  };
}

function batch(id: string, segmentIds: string[], status: SubjectiveGradingBatch["status"] = "planned"): SubjectiveGradingBatch {
  return {
    id, tenant_id: "tenant", scoring_run_id: "run", idempotency_key: id, status, segment_ids: segmentIds,
    total_count: segmentIds.length, queued_count: 0, processing_count: 0, succeeded_count: 0, failed_count: 0,
    created_by: "grader", created_at: "", updated_at: ""
  };
}

const plan: ScoringPlan = {
  examId: "exam", counts: { rule: 0, ai: 1, human: 0 }, snapshotFailures: 0,
  questions: [{ questionId: "ai", questionNo: "1", questionType: "essay", count: 1, mode: "AI_ASSIST", riskTier: "R2", lane: "ai", note: "" }]
};

beforeEach(() => {
  mocks.assertScope.mockReset();
  mocks.list.mockReset();
  mocks.create.mockReset();
  mocks.enqueue.mockReset();
});

describe("grading utility batch28 boundaries", () => {
  it("keeps label fallbacks and the first task of each exam-question group", () => {
    const tasks = [
      { id: "first", exam_id: "1234567890", exam_name: "", question_id: "q", question_no: "一", subject: "" },
      { id: "second", exam_id: "1234567890", exam_name: "ignored", question_id: "q", question_no: "二", subject: "math" },
      { id: "other", exam_id: "other-exam", exam_name: "期中", question_id: "q", question_no: "2", subject: "mathematics" },
      { id: "unknown", exam_id: "other-exam", question_id: "unknown", question_no: "3", subject: "new-subject" }
    ] as ReviewTask[];
    const groups = assignmentGroups(tasks);
    expect(groups.map((group) => group.label)).toEqual([
      "考试 12345678 · 学科未标注 · 第 一 题", "期中 · 数学 · 第 2 题", "考试 other-ex · 其他学科 · 第 3 题"
    ]);
    expect(groups[0].key).toBe(assignmentGroupKey(tasks[0]));
    expect(groups[0].tasks).toEqual([tasks[0], tasks[1]]);
    expect(groups[0].tasks[0]).toBe(tasks[0]);
    expect(assignmentGroups([])).toEqual([]);
  });

  it("prefers valid history for the current segment over the latest suggestion", async () => {
    const history = aiGrade("history");
    const context = contextFixture({ ...aiGrade("latest") }, [
      aiGrade("other-segment", { answer_segment_id: "other" }), history, aiGrade("failed", { status: "failed" })
    ]);
    const result = await loadedContext(context);
    expect(result.aiGrades).toEqual([history]);
    expect(result.warnings).toEqual([]);
    expect(result.originalImageUrl).toBeUndefined();
    expect(mocks.assertScope).toHaveBeenCalledTimes(2);
  });

  it("falls back to the current suggestion when no valid current-segment history exists", async () => {
    const latest = aiGrade("latest");
    const result = await loadedContext(contextFixture({ ...latest }, [
      aiGrade("other", { answer_segment_id: "other" }), aiGrade("shadow", { delivery_mode: "shadow_only" })
    ]));
    expect(result.aiGrades).toEqual([latest]);
  });

  it("keeps malformed suggestions empty with the existing manual-review warning", async () => {
    const result = await loadedContext(contextFixture({ id: "incomplete" }));
    expect(result.aiGrades).toEqual([]);
    expect(result.warnings).toEqual(["AI 建议数据不完整，已切换为人工阅卷；本任务仍可正常提交。"]);
  });

  it("does not reveal either history or the latest suggestion for an explicit second opinion", async () => {
    // 初次加载即使携带历史，也必须先遵守真实第二意见策略的显式揭示规则。
    const context = contextFixture({ ...aiGrade("latest") }, [aiGrade("history")]);
    context.ai_second_opinion!.presentation = "explicit_second_opinion";
    context.ai_second_opinion!.score_prefill_allowed = false;
    const result = await loadedContext(context);
    expect(result.aiGrades).toEqual([]);
    expect(result.warnings).toEqual([]);
  });

  it.each([false, true])("does not replace a failed current execution with an earlier successful suggestion (explicit=%s)", async (explicit) => {
    const context = contextFixture({ ...aiGrade("previous-success") }, [aiGrade("previous-history")]);
    context.ai_failure = { failure_kind: "output_truncated" };
    context.draft = { score: 2, comments: "教师未完成的草稿" } as ReviewTaskContext["draft"];
    if (explicit) {
      context.ai_second_opinion!.presentation = "explicit_second_opinion";
      context.ai_second_opinion!.score_prefill_allowed = false;
    }
    const result = await loadedContext(context);
    expect(result.aiGrades).toEqual([]);
    expect(result.warnings).toEqual(["AI 建议未完成，请人工评分。"]);
    expect(result.reviewContext.draft).toEqual(context.draft);
    expect(context.draft?.score).toBe(2);
  });

  it.each([
    { key: "+", direction: 1 }, { key: "-", direction: -1 },
    { key: "", code: "NumpadAdd", direction: 1 }, { key: "", code: "NumpadSubtract", direction: -1 }
  ] as const)("allows task zoom while its draft is read-only: $key $code", ({ key, direction, ...extra }) => {
    expect(gradingShortcutIntent(shortcut({ key, ...extra, canEditDraft: false }))).toEqual({ type: "zoom", direction });
    expect(gradingShortcutIntent(shortcut({ key, ...extra, hasTask: false }))).toBeNull();
  });

  it("preserves keypad zoom priority and does not reinterpret a denied zoom as a draft command", () => {
    expect(gradingShortcutIntent(shortcut({ key: "-", code: "NumpadAdd" }))).toEqual({ type: "zoom", direction: 1 });
    expect(gradingShortcutIntent(shortcut({ key: "+", code: "NumpadSubtract" }))).toEqual({ type: "zoom", direction: 1 });
    expect(gradingShortcutIntent(shortcut({ key: "a", code: "NumpadAdd", hasTask: false }))).toBeNull();
    expect(gradingShortcutIntent(shortcut({ key: "+", ctrlKey: true }))).toBeNull();
    expect(gradingShortcutIntent(shortcut({ key: "+", singleKeyShortcutsEnabled: false }))).toBeNull();
  });

  it("keeps Enter before draft preferences and rubric points before total-score limits", () => {
    expect(gradingShortcutIntent(shortcut({ key: "Enter", quickSubmit: true, canEditDraft: false, singleKeyShortcutsEnabled: false }))).toEqual({ type: "submit" });
    expect(gradingShortcutIntent(shortcut({ key: "Enter", metaKey: true, canSubmit: false }))).toBeNull();
    expect(gradingShortcutIntent(shortcut({ key: "2", rubricPointCount: 2, maxScore: 1 }))).toEqual({ type: "toggle_criterion", index: 1 });
    expect(gradingShortcutIntent(shortcut({ key: "3", rubricPointCount: 2, maxScore: 9 }))).toBeNull();
  });

  it("uses the default API transport to resume a planned batch and ignores cancelled batches", async () => {
    mocks.list.mockResolvedValue({ batches: [batch("existing", ["s1"]), batch("cancelled", ["s2"], "cancelled")] });
    mocks.enqueue.mockResolvedValue({ enqueue_result: { failed_count: 2 } });
    const onBatch = vi.fn();
    const result = await launchScoringAIBatches("run", plan, [], onBatch);
    expect(mocks.list).toHaveBeenCalledWith("run");
    expect(mocks.create).not.toHaveBeenCalled();
    expect(mocks.enqueue).toHaveBeenCalledExactlyOnceWith("existing");
    expect(onBatch).toHaveBeenCalledExactlyOnceWith("existing");
    expect(result).toEqual({ batchIds: ["existing"], createdBatchIds: [], resumedBatchIds: ["existing"], requested: 0, failed: 2 });
  });

  it("fully replaces the default transport and waits for existing enqueue before creating uncovered work", async () => {
    // 模拟入队异步完成，保护先恢复已有持久化批次、再创建新批次的执行顺序。
    const events: string[] = [];
    const transport = {
      list: vi.fn(async () => ({ batches: [batch("existing", ["s1"])] })),
      create: vi.fn(async (_key: string, segments: string[]) => {
        events.push("create");
        return { batch: batch("new", segments) };
      }),
      enqueue: vi.fn(async (id: string) => {
        events.push(`enqueue-start:${id}`);
        await Promise.resolve();
        events.push(`enqueue-end:${id}`);
        return { batch: batch(id, []), tasks: [], enqueue_result: { requested_count: 1, accepted_count: 1, task_count: 1, failed_count: 0, partial_success: false, failures: [] } };
      })
    };
    const items = ["s1", "s2", "s2"].map((id) => ({ question_id: "ai", answer_segment_id: id, state: "review" }));
    const result = await launchScoringAIBatches("run", plan, items, (id) => events.push(`on:${id}`), transport);
    expect(events).toEqual(["on:existing", "enqueue-start:existing", "enqueue-end:existing", "create", "on:new", "enqueue-start:new", "enqueue-end:new"]);
    expect(transport.create).toHaveBeenCalledWith(expect.stringMatching(/^scoring-ai-run-[a-f0-9]{32}$/), ["s2"], "run");
    expect(result).toEqual({ batchIds: ["existing", "new"], createdBatchIds: ["new"], resumedBatchIds: ["existing"], requested: 1, failed: 0 });
    expect(mocks.list).not.toHaveBeenCalled();
    expect(mocks.create).not.toHaveBeenCalled();
    expect(mocks.enqueue).not.toHaveBeenCalled();
  });
});
