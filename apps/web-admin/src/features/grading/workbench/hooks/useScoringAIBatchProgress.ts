import { useEffect, useMemo, useRef, useState } from "react";
import { getSubjectiveGradingBatch, type SubjectiveGradingBatch } from "../../../../api/subjectiveGrading";
import { formatError } from "../gradingWorkbench.model";

// AI 批次进度独立轮询；考试变更清空批次归属，effect 清理使上一批次的响应失效。
export function useScoringAIBatchProgress(initialExamId: string) {
  const [aiBatchIds, setAIBatchIds] = useState<string[]>([]);
  const [aiBatches, setAIBatches] = useState<SubjectiveGradingBatch[]>([]);
  const [aiProgress, setAIProgress] = useState<{ total: number; queued: number; processing: number; succeeded: number; failed: number; cancelled: number } | null>(null);
  const [aiIssue, setAIIssue] = useState<string | null>(null);
  const aiBatchRun = useRef("");
  const retryableAIBatches = useMemo(() => aiBatches.filter((batch) => batch.failed_count > 0 &&
    !aiBatches.some((candidate) => candidate.idempotency_key === `scoring-ai-retry-${batch.id}`)), [aiBatches]);
  // biome-ignore lint/correctness/useExhaustiveDependencies: 考试变化必须清空上一场的批次显示。
  useEffect(() => {
    setAIBatchIds([]); setAIBatches([]); setAIProgress(null); setAIIssue(null); aiBatchRun.current = "";
  }, [initialExamId]);
  useEffect(() => {
    if (!aiBatchIds.length) { setAIProgress(null); return; }
    let disposed = false;
    const refresh = async () => {
      try {
        const batches = await Promise.all(aiBatchIds.map((id) => getSubjectiveGradingBatch(id)));
        if (disposed) return;
        setAIBatches((current) => [...current.filter((batch) => !aiBatchIds.includes(batch.id)), ...batches.map(({ batch }) => batch)]);
        setAIProgress(batches.reduce((sum, { batch }) => ({
          total: sum.total + batch.total_count,
          queued: sum.queued + (batch.status === "cancelled" ? 0 : batch.queued_count),
          processing: sum.processing + (batch.status === "cancelled" ? 0 : batch.processing_count),
          succeeded: sum.succeeded + batch.succeeded_count,
          failed: sum.failed + batch.failed_count,
          cancelled: sum.cancelled + (batch.status === "cancelled" ? Math.max(0, batch.total_count - batch.succeeded_count - batch.failed_count) : 0)
        }), { total: 0, queued: 0, processing: 0, succeeded: 0, failed: 0, cancelled: 0 }));
      } catch (error) { if (!disposed) setAIIssue(`AI 批次进度刷新失败：${formatError(error)}`); }
    };
    void refresh();
    const timer = window.setInterval(() => { if (document.visibilityState !== "hidden") void refresh(); }, 5000);
    return () => { disposed = true; window.clearInterval(timer); };
  }, [aiBatchIds]);

  return { aiBatchIds, setAIBatchIds, aiBatches, setAIBatches, aiProgress, aiIssue, setAIIssue, aiBatchRun, retryableAIBatches };
}
