import { createSubjectiveGradingBatch, enqueueSubjectiveGradingBatch, getScoringRunAIBatches, type SubjectiveGradingBatch } from "../../../api/subjectiveGrading";
import type { ScoringRunItem } from "../../../api/review";
import type { ScoringPlan } from "./scoringPlan";

export interface AIBatchLaunch {
  batchIds: string[];
  createdBatchIds: string[];
  resumedBatchIds: string[];
  requested: number;
  failed: number;
}

export interface AIBatchLaunchOptions {
  segmentIds?: readonly string[];
  questionIds?: readonly string[];
  allowedBatchIds?: readonly string[];
}

const defaultAIBatchTransport = { list: getScoringRunAIBatches, create: createSubjectiveGradingBatch, enqueue: enqueueSubjectiveGradingBatch };

export async function launchScoringAIBatches(
  runId: string,
  plan: ScoringPlan,
  items: Array<Pick<ScoringRunItem, "question_id" | "answer_segment_id" | "state">>,
  onBatch: (batchId: string) => void,
  transport = defaultAIBatchTransport,
  options: AIBatchLaunchOptions = {}
): Promise<AIBatchLaunch> {
  const selectedQuestionIds = options.questionIds ? new Set(options.questionIds) : null;
  const questionIds = new Set(plan.questions.filter((question) => question.lane === "ai"
    && (!selectedQuestionIds || selectedQuestionIds.has(question.questionId))).map((question) => question.questionId));
  const selectedSegmentIds = options.segmentIds ? new Set(options.segmentIds) : null;
  const allowedBatchIds = options.allowedBatchIds ? new Set(options.allowedBatchIds) : null;
  const existing = (await transport.list(runId)).batches.filter((batch) => batch.status !== "cancelled");
  const itemQuestions = new Map(items.map((item) => [item.answer_segment_id, item.question_id]));
  // 批次入队接口会运行整个批次，不能用“有交集”判断试改白名单；范围外旧批次只能保留，不能借本次操作续跑。
  const resumable = existing.filter((batch) => (!allowedBatchIds || allowedBatchIds.has(batch.id))
    && (!selectedSegmentIds || (batch.segment_ids.length > 0 && batch.segment_ids.every((id) => selectedSegmentIds.has(id))))
    && (!selectedQuestionIds || (batch.segment_ids.length > 0 && batch.segment_ids.every((id) => questionIds.has(itemQuestions.get(id) ?? "")))));
  // 未授权续跑的旧批次仍然覆盖原片段，不能将它拆成新批次再次提交。
  const covered = new Set(existing.flatMap((batch) => batch.segment_ids));
  // 先排除已有批次覆盖的片段并排序，使相同待处理集合在刷新后仍生成相同分块和命令键。
  // 试改或扩批传入片段白名单时，白名单只缩小范围，不能把不属于当前评分策略的片段带入 AI 批次。
  const segmentIds = [...new Set(items.filter((item) => questionIds.has(item.question_id) && item.state === "review"
    && (!selectedSegmentIds || selectedSegmentIds.has(item.answer_segment_id))
    && !covered.has(item.answer_segment_id)).map((item) => item.answer_segment_id))].sort((left, right) => left.localeCompare(right));
  const result: AIBatchLaunch = { batchIds: [], createdBatchIds: [], resumedBatchIds: [], requested: segmentIds.length, failed: 0 };
  const enqueue = async (batch: SubjectiveGradingBatch, resumed: boolean) => {
    result.batchIds.push(batch.id);
    onBatch(batch.id);
    // 已规划或部分入队的批次是需要继续执行的持久化任务；
    // 已完成的模型运行需要单独决定是否重试，不能直接重新入队。
    if (batch.status !== "planned" && batch.queued_count + batch.processing_count + batch.succeeded_count + batch.failed_count >= batch.total_count) return;
    const enqueued = await transport.enqueue(batch.id);
    if (resumed) result.resumedBatchIds.push(batch.id);
    result.failed += enqueued.enqueue_result.failed_count;
  };
  for (const batch of resumable) await enqueue(batch, true);
  for (let offset = 0; offset < segmentIds.length; offset += 1000) {
    const chunk = segmentIds.slice(offset, offset + 1000);
    const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(chunk.join(",")));
    const hash = Array.from(new Uint8Array(digest)).map((value) => value.toString(16).padStart(2, "0")).join("").slice(0, 32);
    const key = `scoring-ai-${runId}-${hash}`;
    const created = await transport.create(key, chunk, runId);
    result.createdBatchIds.push(created.batch.id);
    await enqueue(created.batch, false);
  }
  return result;
}
