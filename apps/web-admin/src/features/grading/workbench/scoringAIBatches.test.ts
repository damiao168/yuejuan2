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
    expect(result).toEqual({ batchIds: ["batch"], createdBatchIds: ["batch"], resumedBatchIds: [], requested: 1, failed: 0 });
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
    expect(result.createdBatchIds).toEqual([]);
    expect(result.resumedBatchIds).toEqual(["existing"]);
  });

  it("creates only uncovered review segments when an earlier chunk already exists", async () => {
    const list = vi.fn(async () => ({ batches: [batch("existing", ["s1"], "processing")] }));
    const create = vi.fn(async (_key: string, ids: string[]) => ({ batch: batch("new", ids) }));
    const enqueue = vi.fn(async () => ({ enqueue_result: { failed_count: 0 } }));
    const items = ["s1", "s2"].map((id) => ({ question_id: "ai", answer_segment_id: id, state: "review" })) as ScoringRunItem[];
    await launchScoringAIBatches("run", plan, items, vi.fn(), { list, create, enqueue } as never);
    expect(create).toHaveBeenCalledWith(expect.any(String), ["s2"], "run");
  });

  it("when a trial whitelist is supplied, enqueues only the selected segments", async () => {
    const list = vi.fn(async () => ({ batches: [] as SubjectiveGradingBatch[] }));
    const create = vi.fn(async (_key: string, ids: string[]) => ({ batch: batch("trial-batch", ids) }));
    const enqueue = vi.fn(async () => ({ enqueue_result: { failed_count: 0 } }));
    const items = ["s1", "s2", "s3"].map((id) => ({ question_id: "ai", answer_segment_id: id, state: "review" })) as ScoringRunItem[];
    const result = await launchScoringAIBatches("run", plan, items, vi.fn(), { list, create, enqueue } as never, { segmentIds: ["s2"] });
    expect(create).toHaveBeenCalledWith(expect.any(String), ["s2"], "run");
    expect(result.requested).toBe(1);
  });

  it("does not resume a planned full-run batch that only overlaps the trial whitelist", async () => {
    const list = vi.fn(async () => ({ batches: [batch("full-run", ["s1", "s2", "s3"])] }));
    const create = vi.fn();
    const enqueue = vi.fn();
    const onBatch = vi.fn();
    const items = ["s1", "s2", "s3"].map((id) => ({ question_id: "ai", answer_segment_id: id, state: "review" })) as ScoringRunItem[];
    const result = await launchScoringAIBatches("run", plan, items, onBatch, { list, create, enqueue } as never, { segmentIds: ["s1", "s2"] });
    expect(enqueue).not.toHaveBeenCalled();
    expect(create).not.toHaveBeenCalled();
    expect(onBatch).not.toHaveBeenCalled();
    expect(result).toEqual({ batchIds: [], createdBatchIds: [], resumedBatchIds: [], requested: 0, failed: 0 });
  });

  it("treats an empty trial whitelist as permission to enqueue nothing", async () => {
    const list = vi.fn(async () => ({ batches: [batch("existing", ["s1"])] }));
    const create = vi.fn();
    const enqueue = vi.fn();
    const items = [{ question_id: "ai", answer_segment_id: "s2", state: "review" }] as ScoringRunItem[];
    const result = await launchScoringAIBatches("run", plan, items, vi.fn(), { list, create, enqueue } as never, { segmentIds: [] });
    expect(create).not.toHaveBeenCalled();
    expect(enqueue).not.toHaveBeenCalled();
    expect(result.batchIds).toEqual([]);
  });

  it("resumes only the trial's authorized batch IDs even when another batch has the same selected segments", async () => {
    const list = vi.fn(async () => ({ batches: [batch("unrelated", ["s1"]), batch("trial", ["s1"])] }));
    const create = vi.fn();
    const enqueue = vi.fn(async () => ({ enqueue_result: { failed_count: 0 } }));
    const items = [{ question_id: "ai", answer_segment_id: "s1", state: "review" }] as ScoringRunItem[];
    const result = await launchScoringAIBatches("run", plan, items, vi.fn(), { list, create, enqueue } as never,
      { segmentIds: ["s1"], allowedBatchIds: ["trial"] });
    expect(enqueue).toHaveBeenCalledExactlyOnceWith("trial");
    expect(create).not.toHaveBeenCalled();
    expect(result.resumedBatchIds).toEqual(["trial"]);
  });

  it("requires the whole batch to belong to the selected AI question scope", async () => {
    const list = vi.fn(async () => ({ batches: [batch("mixed", ["s1", "s2"]), batch("unknown", ["missing"]), batch("selected", ["s3"])] }));
    const create = vi.fn();
    const enqueue = vi.fn(async () => ({ enqueue_result: { failed_count: 0 } }));
    const items = [
      { question_id: "ai", answer_segment_id: "s1", state: "review" },
      { question_id: "human", answer_segment_id: "s2", state: "review" },
      { question_id: "ai", answer_segment_id: "s3", state: "review" }
    ] as ScoringRunItem[];
    const result = await launchScoringAIBatches("run", plan, items, vi.fn(), { list, create, enqueue } as never, { questionIds: ["ai", "human"] });
    expect(enqueue).toHaveBeenCalledExactlyOnceWith("selected");
    expect(create).not.toHaveBeenCalled();
    expect(result.batchIds).toEqual(["selected"]);
  });

  it("an empty allowed batch list blocks recovery but still permits uncovered trial segments", async () => {
    const list = vi.fn(async () => ({ batches: [batch("old", ["s1"])] }));
    const create = vi.fn(async (_key: string, ids: string[]) => ({ batch: batch("new", ids) }));
    const enqueue = vi.fn(async () => ({ enqueue_result: { failed_count: 0 } }));
    const items = ["s1", "s2"].map((id) => ({ question_id: "ai", answer_segment_id: id, state: "review" })) as ScoringRunItem[];
    const result = await launchScoringAIBatches("run", plan, items, vi.fn(), { list, create, enqueue } as never,
      { segmentIds: ["s1", "s2"], allowedBatchIds: [] });
    expect(create).toHaveBeenCalledWith(expect.any(String), ["s2"], "run");
    expect(enqueue).toHaveBeenCalledExactlyOnceWith("new");
    expect(result.createdBatchIds).toEqual(["new"]);
    expect(result.resumedBatchIds).toEqual([]);
  });

  it("expansion excludes trial-covered segments and other questions without resuming completed trial batches", async () => {
    const trial = { ...batch("trial", ["s1"], "completed"), succeeded_count: 1 };
    const scopedPlan = { ...plan, questions: [...plan.questions, { ...plan.questions[0], questionId: "other-ai", questionNo: "3" }] };
    const list = vi.fn(async () => ({ batches: [trial] }));
    const create = vi.fn(async (_key: string, ids: string[]) => ({ batch: batch("expansion", ids) }));
    const enqueue = vi.fn(async () => ({ enqueue_result: { failed_count: 0 } }));
    const items = [
      { question_id: "ai", answer_segment_id: "s1", state: "review" },
      { question_id: "ai", answer_segment_id: "s2", state: "review" },
      { question_id: "other-ai", answer_segment_id: "s3", state: "review" }
    ] as ScoringRunItem[];
    const result = await launchScoringAIBatches("run", scopedPlan, items, vi.fn(), { list, create, enqueue } as never,
      { questionIds: ["ai"], allowedBatchIds: ["trial"] });
    expect(create).toHaveBeenCalledWith(expect.any(String), ["s2"], "run");
    expect(enqueue).toHaveBeenCalledExactlyOnceWith("expansion");
    expect(result).toEqual({ batchIds: ["trial", "expansion"], createdBatchIds: ["expansion"], resumedBatchIds: [], requested: 1, failed: 0 });
  });
});
