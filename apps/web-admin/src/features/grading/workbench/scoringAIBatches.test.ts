import { describe, expect, it, vi } from "vitest";
import { launchScoringAIBatches } from "./scoringAIBatches";
import type { ScoringPlan } from "./scoringPlan";
import type { ScoringRunItem } from "../../../api/review";
import type { SubjectiveGradingBatch } from "../../../api/subjectiveGrading";

const batch = (id: string, segments: string[], status: SubjectiveGradingBatch["status"] = "planned"): SubjectiveGradingBatch => ({
  id, tenant_id: "tenant", scoring_run_id: "run", idempotency_key: id, status,
  segment_ids: segments, total_count: segments.length, queued_count: 0, processing_count: 0,
  succeeded_count: 0, failed_count: 0, created_by: "grader", created_at: "", updated_at: ""
});

const plan: ScoringPlan = { examId: "exam", counts: { rule: 0, ai: 2, human: 1 }, snapshotFailures: 0, questions: [
  { questionId: "ai", questionNo: "1", questionType: "essay", count: 2, mode: "AI_ASSIST", riskTier: "R2", lane: "ai", note: "" },
  { questionId: "human", questionNo: "2", questionType: "essay", count: 1, mode: "HUMAN_PRIMARY", riskTier: "R3", lane: "human", note: "" }
] };

describe("scoring AI batch launch", () => {
  it("enqueues only AI-plan review segments with a stable run key", async () => {
    const items = [
      { question_id: "ai", answer_segment_id: "s1", state: "review" },
      { question_id: "ai", answer_segment_id: "s2", state: "confirmed" },
      { question_id: "human", answer_segment_id: "s3", state: "review" }
    ] as ScoringRunItem[];
    const list = vi.fn(async () => ({ batches: [] as SubjectiveGradingBatch[] }));
    const create = vi.fn(async () => ({ batch: batch("batch", ["s1"]) }));
    const enqueue = vi.fn(async () => ({ enqueue_result: { failed_count: 0 } }));
    const onBatch = vi.fn();
    const result = await launchScoringAIBatches("run", plan, items, onBatch, { list, create, enqueue } as never);
    expect(create).toHaveBeenCalledWith(expect.stringMatching(/^scoring-ai-run-[a-f0-9]{32}$/), ["s1"], "run");
    expect(enqueue).toHaveBeenCalledWith("batch");
    expect(onBatch).toHaveBeenCalledWith("batch");
    expect(result).toEqual({ batchIds: ["batch"], requested: 1, failed: 0 });
  });

  it("resumes a durable planned batch after reload without recreating it", async () => {
    const list = vi.fn(async () => ({ batches: [batch("existing", ["s1", "s2"])] }));
    const create = vi.fn();
    const enqueue = vi.fn(async () => ({ enqueue_result: { failed_count: 0 } }));
    const items = [{ question_id: "ai", answer_segment_id: "s2", state: "review" }] as ScoringRunItem[];
    const result = await launchScoringAIBatches("run", plan, items, vi.fn(), { list, create, enqueue } as never);
    expect(create).not.toHaveBeenCalled();
    expect(enqueue).toHaveBeenCalledWith("existing");
    expect(result.batchIds).toEqual(["existing"]);
  });

  it("creates only uncovered review segments when an earlier chunk already exists", async () => {
    const list = vi.fn(async () => ({ batches: [batch("existing", ["s1"], "processing")] }));
    const create = vi.fn(async (_key: string, ids: string[]) => ({ batch: batch("new", ids) }));
    const enqueue = vi.fn(async () => ({ enqueue_result: { failed_count: 0 } }));
    const items = ["s1", "s2"].map((id) => ({ question_id: "ai", answer_segment_id: id, state: "review" })) as ScoringRunItem[];
    await launchScoringAIBatches("run", plan, items, vi.fn(), { list, create, enqueue } as never);
    expect(create).toHaveBeenCalledWith(expect.any(String), ["s2"], "run");
  });
});
