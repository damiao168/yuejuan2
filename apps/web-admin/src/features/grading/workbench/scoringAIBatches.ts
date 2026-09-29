import { createSubjectiveGradingBatch, enqueueSubjectiveGradingBatch, getScoringRunAIBatches, type SubjectiveGradingBatch } from "../../../api/subjectiveGrading";
import type { ScoringRunItem } from "../../../api/review";
import type { ScoringPlan } from "./scoringPlan";

export interface AIBatchLaunch {
  batchIds: string[];
  requested: number;
  failed: number;
}

export async function launchScoringAIBatches(
  runId: string,
  plan: ScoringPlan,
  items: Array<Pick<ScoringRunItem, "question_id" | "answer_segment_id" | "state">>,
  onBatch: (batchId: string) => void,
  transport = { list: getScoringRunAIBatches, create: createSubjectiveGradingBatch, enqueue: enqueueSubjectiveGradingBatch }
): Promise<AIBatchLaunch> {
  const questionIds = new Set(plan.questions.filter((question) => question.lane === "ai").map((question) => question.questionId));
  const existing = (await transport.list(runId)).batches.filter((batch) => batch.status !== "cancelled");
  const covered = new Set(existing.flatMap((batch) => batch.segment_ids));
  // 先排除已有批次覆盖的片段并排序，使相同待处理集合在刷新后仍生成相同分块和命令键。
  const segmentIds = [...new Set(items.filter((item) => questionIds.has(item.question_id) && item.state === "review" && !covered.has(item.answer_segment_id)).map((item) => item.answer_segment_id))].sort();
  const result: AIBatchLaunch = { batchIds: [], requested: segmentIds.length, failed: 0 };
  const enqueue = async (batch: SubjectiveGradingBatch) => {
    result.batchIds.push(batch.id);
    onBatch(batch.id);
    // A planned or partially enqueued batch is durable work to resume. A
    // finished model run needs a separate retry decision, not re-enqueue.
    if (batch.status !== "planned" && batch.queued_count + batch.processing_count + batch.succeeded_count + batch.failed_count >= batch.total_count) return;
    const enqueued = await transport.enqueue(batch.id);
    result.failed += enqueued.enqueue_result.failed_count;
  };
  for (const batch of existing) await enqueue(batch);
  for (let offset = 0; offset < segmentIds.length; offset += 1000) {
    const chunk = segmentIds.slice(offset, offset + 1000);
    const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(chunk.join(",")));
    const hash = Array.from(new Uint8Array(digest)).map((value) => value.toString(16).padStart(2, "0")).join("").slice(0, 32);
    const key = `scoring-ai-${runId}-${hash}`;
    const created = await transport.create(key, chunk, runId);
    await enqueue(created.batch);
  }
  return result;
}
