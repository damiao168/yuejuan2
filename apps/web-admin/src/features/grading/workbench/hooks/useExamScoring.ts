import { submitScoringCommand } from "./scoringCommand";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { App } from "antd";
import { createSubjectiveGradingBatch, enqueueSubjectiveGradingBatch, getFailedSubjectiveBatchSegments, getScoringRunAIBatches, getSubjectiveGradingBatch, type SubjectiveGradingBatch } from "../../../../api/subjectiveGrading";
import {
  cancelScoringRun,
  downloadScoringResultImage,
  getExamAutomationResults,
  getScoringReadiness,
  getScoringRun,
  getScoringSummary,
  retryFailedScoringRun,

  type ExamAutomationResults,
  type ScoringReadiness,
  type ScoringRunItem,
  type ScoringSummary
} from "../../../../api/review";
import { formatAnswer, formatError } from "../gradingWorkbench.model";
import type { ScoringImagePreview, ScoringResultState, ScoringResultType } from "../gradingWorkbench.types";
import { launchScoringAIBatches } from "../scoringAIBatches";
import { loadScoringPlan, type ScoringPlan } from "../scoringPlan";

export interface UseExamScoringOptions {
  initialExamId: string;
  currentUserId: string;
  currentTenantId: string;
  canGrade: boolean;
  onTasksChanged: () => Promise<void>;
}

export function useExamScoring({ initialExamId, currentUserId, currentTenantId, canGrade, onTasksChanged }: UseExamScoringOptions) {
  const { message } = App.useApp();
  const [summary, setSummary] = useState<ScoringSummary | null>(null);
  const [readiness, setReadiness] = useState<ScoringReadiness | null>(null);
  const [loading, setLoading] = useState(false);
  const [runDetail, setRunDetail] = useState<ExamAutomationResults | null>(null);
  const [detailOpen, setDetailOpen] = useState(false);
  const [resultType, setResultType] = useState<ScoringResultType>("all");
  const [resultState, setResultState] = useState<ScoringResultState>("all");
  const [resultKeyword, setResultKeyword] = useState("");
  const [image, setImage] = useState<ScoringImagePreview | null>(null);
  const [imageLoading, setImageLoading] = useState("");
  const [actioning, setActioning] = useState<string | null>(null);
  const [plan, setPlan] = useState<ScoringPlan | null>(null);
  const [planOpen, setPlanOpen] = useState(false);
  const [aiBatchIds, setAIBatchIds] = useState<string[]>([]);
  const [aiBatches, setAIBatches] = useState<SubjectiveGradingBatch[]>([]);
  const [aiProgress, setAIProgress] = useState<{ total: number; queued: number; processing: number; succeeded: number; failed: number; cancelled: number } | null>(null);
  const [aiIssue, setAIIssue] = useState<string | null>(null);
  const commandStorageKey = `scoring-command:${currentTenantId}:${currentUserId}:${initialExamId}`;
  const [pendingCommand, setPendingCommand] = useState(() => localStorage.getItem(commandStorageKey));
  const startBusy = useRef(false);
  const reconciledRun = useRef("");
  const aiBatchRun = useRef("");
  useEffect(() => { setPendingCommand(localStorage.getItem(commandStorageKey)); }, [commandStorageKey]);
  const summaryRequestRef = useRef(0);
  const imageRequestRef = useRef(0);

  const blockingChecks = useMemo(() => readiness?.checks.filter((check) => check.severity === "blocker" && !check.passed) ?? [], [readiness]);
  const warningChecks = useMemo(() => readiness?.checks.filter((check) => check.severity === "warning" && !check.passed) ?? [], [readiness]);
  const hasUnresolvedRun = Boolean(summary?.run && ["queued", "processing", "needs_review", "failed", "cancelling"].includes(summary.run.status));
  const retryableAIBatches = useMemo(() => aiBatches.filter((batch) => batch.failed_count > 0 &&
    !aiBatches.some((candidate) => candidate.idempotency_key === `scoring-ai-retry-${batch.id}`)), [aiBatches]);
  const filteredItems = useMemo(() => {
    const text = resultKeyword.trim().toLowerCase();
    return (runDetail?.items ?? []).filter((item) => {
      const typeMatched = resultType === "all" ||
        (resultType === "choice" && ["single_choice", "multiple_choice", "true_false"].includes(item.question_type)) ||
        (resultType === "fill" && ["fill_blank", "numeric"].includes(item.question_type));
      const stateMatched = resultState === "all" ||
        (resultState === "processing" ? ["pending", "processing", "cancelling"].includes(item.state) : item.state === resultState);
      const keywordMatched = !text || [item.anonymous_code, item.question_no, item.recognized_answer, formatAnswer(item.standard_answer)]
        .some((value) => (value ?? "").toLowerCase().includes(text));
      return typeMatched && stateMatched && keywordMatched;
    });
  }, [resultKeyword, resultState, resultType, runDetail?.items]);
  const resultMetrics = useMemo(() => {
    const items = runDetail?.items ?? [];
    return {
      total: items.length,
      auto: items.filter((item) => item.state === "confirmed" && item.grade_source === "rule_confirmed").length,
      review: items.filter((item) => item.state === "review").length,
      failed: items.filter((item) => item.state === "failed").length
    };
  }, [runDetail?.items]);

  const loadSummary = useCallback(async () => {
    const requestId = ++summaryRequestRef.current;
    if (!initialExamId || !canGrade) {
      setSummary(null);
      setReadiness(null);
      setLoading(false);
      return;
    }
    setLoading(true);
    try {
      const [result, readinessResult] = await Promise.all([getScoringSummary(initialExamId), getScoringReadiness(initialExamId)]);
      if (requestId !== summaryRequestRef.current) return;
      setSummary(result.scoring_summary);
      setReadiness(readinessResult.scoring_readiness);
      const runId = result.scoring_summary.run?.id;
      const batches = runId ? await getScoringRunAIBatches(runId) : { batches: [] };
      if (requestId === summaryRequestRef.current) {
        const fetched = batches.batches.map((batch) => batch.id);
        if (aiBatchRun.current !== runId) {
          aiBatchRun.current = runId ?? "";
          setAIBatchIds(fetched);
          setAIBatches(batches.batches);
        } else {
          setAIBatchIds((current) => [...new Set([...current, ...fetched])]);
          setAIBatches((current) => [...current.filter((batch) => !fetched.includes(batch.id)), ...batches.batches]);
        }
      }
    } catch (error) {
      if (requestId === summaryRequestRef.current) message.error(formatError(error));
    } finally {
      if (requestId === summaryRequestRef.current) setLoading(false);
    }
  }, [canGrade, initialExamId, message]);

  const prepareStart = useCallback(async () => {
    if (!initialExamId || !canGrade) return;
    setActioning("prepare-scoring");
    try {
      const [nextSummary, nextReadiness] = await Promise.all([getScoringSummary(initialExamId), getScoringReadiness(initialExamId)]);
      setSummary(nextSummary.scoring_summary);
      setReadiness(nextReadiness.scoring_readiness);
      if (!nextReadiness.scoring_readiness.ready) { message.warning("评分准备检查未通过，请先处理阻塞项"); return; }
      setPlan(await loadScoringPlan(initialExamId, nextSummary.scoring_summary.questions));
      setPlanOpen(true);
    } catch (error) { message.error(formatError(error)); }
    finally { setActioning(null); }
  }, [canGrade, initialExamId, message]);

  const launchAI = useCallback(async (runId: string, nextPlan: ScoringPlan, items: Array<Pick<ScoringRunItem, "question_id" | "answer_segment_id" | "state">>) => {
    if (!nextPlan.questions.some((question) => question.lane === "ai")) return;
    setActioning("enqueue-ai");
    setAIIssue(null);
    try {
      if (aiBatchRun.current !== runId) { aiBatchRun.current = runId; setAIBatchIds([]); }
      const result = await launchScoringAIBatches(runId, nextPlan, items, (batchId) => {
        setAIBatchIds((current) => current.includes(batchId) ? current : [...current, batchId]);
      });
      if (result.failed) setAIIssue(`${result.failed} 个 AI 建议任务入队失败，可重试；人工阅卷任务仍可继续。`);
      else if (result.requested) message.success(`已安排 ${result.requested} 份 AI 辅助建议，教师仍需逐题确认`);
    } catch (error) {
      setAIIssue(`AI 建议未全部入队：${formatError(error)}。可重试，已创建批次不会重复。`);
    } finally { setActioning(null); }
  }, [message]);

  const start = useCallback(async () => {
    if (!initialExamId || !canGrade || startBusy.current) return;
    startBusy.current = true;
    setActioning("start-scoring");
    try {
      const completed = await submitScoringCommand({
        examId: initialExamId, storageKey: commandStorageKey, storage: localStorage, pending: setPendingCommand,
        ready: async () => {
          const result = await getScoringReadiness(initialExamId);
          setReadiness(result.scoring_readiness);
          if (!result.scoring_readiness.ready) message.warning("评分准备检查未通过，请先处理阻塞项");
          return result.scoring_readiness.ready;
        }
      });
      if (!completed) return;
      message.success("评分任务已生成");
      setPlanOpen(false);
      setDetailOpen(true);
      const [nextSummary, detail, nextReadiness] = await Promise.all([
        getScoringSummary(initialExamId),
        getExamAutomationResults(initialExamId),
        getScoringReadiness(initialExamId)
      ]);
      setSummary(nextSummary.scoring_summary);
      setRunDetail(detail);
      setReadiness(nextReadiness.scoring_readiness);
      await onTasksChanged();
      // The active-run reconciliation effect also runs after a reload or a
      // browser switch, closing the gap after the scoring command is accepted.
    } catch (error) {
      message.error(formatError(error));
      setAIIssue(`评分可能已经启动；AI 建议尚未确认入队：${formatError(error)}。请刷新后重试 AI 入队。`);
      void getScoringReadiness(initialExamId).then((result) => setReadiness(result.scoring_readiness)).catch(() => undefined);
    } finally {
      startBusy.current = false;
      setActioning(null);
    }
  }, [canGrade, initialExamId, commandStorageKey, message, onTasksChanged]);

  const retryAI = useCallback(async () => {
    if (!initialExamId) return;
    try {
      const nextSummary = await getScoringSummary(initialExamId);
      const run = nextSummary.scoring_summary.run;
      if (!run || !["queued", "processing", "needs_review", "failed"].includes(run.status)) return;
      const detail = await getScoringRun(run.id);
      const nextPlan = await loadScoringPlan(initialExamId, nextSummary.scoring_summary.questions);
      setPlan(nextPlan);
      await launchAI(run.id, nextPlan, detail.items);
    } catch (error) { setAIIssue(formatError(error)); }
  }, [initialExamId, launchAI]);

  const retryFailedAI = useCallback(async () => {
    const run = summary?.run;
    if (!run || !["queued", "processing", "needs_review", "failed"].includes(run.status)) return;
    setActioning("retry-failed-ai");
    try {
      const listed = await getScoringRunAIBatches(run.id);
      let queued = 0;
      for (const source of listed.batches.filter((batch) => batch.failed_count > 0)) {
        const key = `scoring-ai-retry-${source.id}`;
        const existing = listed.batches.find((batch) => batch.idempotency_key === key);
        if (existing) {
          setAIBatchIds((current) => [...new Set([...current, existing.id])]);
          if (existing.status === "planned" || existing.queued_count + existing.processing_count + existing.succeeded_count + existing.failed_count < existing.total_count) {
            const resumed = await enqueueSubjectiveGradingBatch(existing.id);
            queued += resumed.enqueue_result.accepted_count;
          }
          continue;
        }
        const { segment_ids: segments } = await getFailedSubjectiveBatchSegments(source.id);
        if (!segments.length) continue;
        const { batch } = await createSubjectiveGradingBatch(key, segments, run.id);
        setAIBatchIds((current) => [...new Set([...current, batch.id])]);
        const result = await enqueueSubjectiveGradingBatch(batch.id);
        queued += result.enqueue_result.accepted_count;
        if (result.enqueue_result.failed_count) setAIIssue(`${result.enqueue_result.failed_count} 个 AI 重试任务未能入队，可再次尝试。`);
      }
      await loadSummary();
      if (queued) message.success(`已重新安排 ${queued} 份失败的 AI 建议；教师仍需确认最终分数`);
    } catch (error) { setAIIssue(`AI 建议重试失败：${formatError(error)}`); }
    finally { setActioning(null); }
  }, [loadSummary, message, summary?.run]);

  useEffect(() => {
    const run = summary?.run;
    if (!canGrade || !run || run.exam_id !== initialExamId || !["queued", "processing", "needs_review", "failed"].includes(run.status)) return;
    const key = `${initialExamId}:${run.id}`;
    if (reconciledRun.current === key) return;
    reconciledRun.current = key;
    void retryAI();
  }, [canGrade, initialExamId, retryAI, summary?.run]);

  const showDetail = useCallback(async () => {
    if (!initialExamId) return;
    setActioning("scoring-detail");
    try {
      const [nextSummary, detail] = await Promise.all([getScoringSummary(initialExamId), getExamAutomationResults(initialExamId)]);
      setSummary(nextSummary.scoring_summary);
      setRunDetail(detail);
      setDetailOpen(true);
    } catch (error) {
      message.error(formatError(error));
    } finally {
      setActioning(null);
    }
  }, [initialExamId, message]);

  const showImage = useCallback(async (item: ScoringRunItem) => {
    const requestId = ++imageRequestRef.current;
    setImageLoading(item.answer_segment_id);
    try {
      const file = await downloadScoringResultImage(item.answer_segment_id);
      if (requestId !== imageRequestRef.current) return;
      setImage((current) => {
        if (current?.url) URL.revokeObjectURL(current.url);
        return { url: URL.createObjectURL(file.blob), contentType: file.contentType, filename: file.filename, title: `${item.question_no} · ${item.anonymous_code}` };
      });
    } catch (error) {
      if (requestId === imageRequestRef.current) message.error(formatError(error));
    } finally {
      if (requestId === imageRequestRef.current) setImageLoading("");
    }
  }, [message]);

  const retry = useCallback(async () => {
    const runId = summary?.run?.id;
    if (!runId) return;
    setActioning("retry-scoring");
    try {
      const result = await retryFailedScoringRun(runId);
      result.requeued > 0 ? message.success(`已重新安排 ${result.requeued} 个失败项`) : message.info("当前没有可重新处理的失败项");
      await Promise.all([loadSummary(), onTasksChanged()]);
    } catch (error) {
      message.error(formatError(error));
    } finally {
      setActioning(null);
    }
  }, [loadSummary, message, onTasksChanged, summary?.run?.id]);

  const cancel = useCallback(async () => {
    const runId = summary?.run?.id;
    if (!runId) return;
    setActioning("cancel-scoring");
    try {
      await cancelScoringRun(runId);
      message.success("本次评分已取消，未完成任务不会继续写入结果");
      setAIIssue(null);
      await Promise.all([loadSummary(), onTasksChanged()]);
    } catch (error) {
      message.error(formatError(error));
      await loadSummary();
    } finally {
      setActioning(null);
    }
  }, [loadSummary, message, onTasksChanged, summary?.run?.id]);

  const closeImage = useCallback(() => {
    imageRequestRef.current += 1;
    setImageLoading("");
    setImage(null);
  }, []);

  useEffect(() => {
    summaryRequestRef.current += 1;
    imageRequestRef.current += 1;
    setSummary(null);
    setReadiness(null);
    setLoading(false);
    setRunDetail(null);
    setDetailOpen(false);
    setImage(null);
    setImageLoading("");
    setPlan(null);
    setPlanOpen(false);
    setAIBatchIds([]);
    setAIBatches([]);
    setAIProgress(null);
    setAIIssue(null);
    reconciledRun.current = "";
    aiBatchRun.current = "";
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

  useEffect(() => {
    void loadSummary();
  }, [loadSummary]);

  useEffect(() => {
    const status = summary?.run?.status;
    if (!detailOpen || !initialExamId || !status || !["queued", "processing"].includes(status)) return;
    let disposed = false;
    const refresh = () => {
      void Promise.all([getScoringSummary(initialExamId), getExamAutomationResults(initialExamId), getScoringReadiness(initialExamId)])
        .then(([nextSummary, detail, nextReadiness]) => {
          if (disposed) return;
          setSummary(nextSummary.scoring_summary);
          setRunDetail(detail);
          setReadiness(nextReadiness.scoring_readiness);
        })
        .catch(() => undefined);
    };
    const timer = window.setInterval(refresh, 2500);
    return () => {
      disposed = true;
      window.clearInterval(timer);
    };
  }, [detailOpen, initialExamId, summary?.run?.status]);

  useEffect(() => () => {
    if (image?.url) URL.revokeObjectURL(image.url);
  }, [image?.url]);

  return {
    pendingCommand,
    summary,
    readiness,
    loading,
    runDetail,
    detailOpen,
    setDetailOpen,
    resultType,
    setResultType,
    resultState,
    setResultState,
    resultKeyword,
    setResultKeyword,
    image,
    imageLoading,
    actioning,
    plan,
    planOpen,
    setPlanOpen,
    aiProgress,
    aiIssue,
    prepareStart,
    retryAI,
    retryFailedAI,
    retryableAIBatches,
    blockingChecks,
    warningChecks,
    hasUnresolvedRun,
    filteredItems,
    resultMetrics,
    loadSummary,
    start,
    showDetail,
    showImage,
    retry,
    cancel,
    closeImage
  };
}

export type ExamScoringController = ReturnType<typeof useExamScoring>;
