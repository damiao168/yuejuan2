import { useScoringAIBatchProgress } from "./useScoringAIBatchProgress";
import { submitScoringCommand } from "./scoringCommand";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { App } from "antd";
import { getBusinessCommandSession } from "../../../../api/businessCommand";
import { getScoringRunAIBatches } from "../../../../api/subjectiveGrading";
import {
  cancelScoringRun,
  getExamAutomationResults,
  getScoringReadiness,
  getScoringSummary,
  retryFailedScoringRun,
  resumeScoringRun,

  type ExamAutomationResults,
  type ScoringReadiness,
  type ScoringSummary
} from "../../../../api/review";
import { formatError } from "../gradingWorkbench.model";
import { useScoringResults } from "./useScoringResults";
import { loadScoringPlan, type ScoringPlan } from "../scoringPlan";

export interface UseExamScoringOptions {
  initialExamId: string;
  currentUserId: string;
  currentTenantId: string;
  canGrade: boolean;
  onTasksChanged: () => Promise<void>;
}

function readPendingScoringCommand(key: string) {
  // 命令恢复依赖可读的原ID；挂载失败不能阻断人工阅卷，首次发命令仍由耐久写入门禁负责。
  try { return localStorage.getItem(key); } catch { return null; }
}

export function useExamScoring({ initialExamId, currentUserId, currentTenantId, canGrade, onTasksChanged }: UseExamScoringOptions) {
  const { message } = App.useApp();
  const session = getBusinessCommandSession();
  const scopeKey = `${currentTenantId}:${currentUserId}:${session.scope}:${session.generation}:${initialExamId}:${canGrade}`;
  const scopeRef = useRef(scopeKey);
  scopeRef.current = scopeKey;
  const [summary, setSummary] = useState<ScoringSummary | null>(null);
  const [readiness, setReadiness] = useState<ScoringReadiness | null>(null);
  const [loading, setLoading] = useState(false);
  const [runDetail, setRunDetail] = useState<ExamAutomationResults | null>(null);
  const [detailOpen, setDetailOpen] = useState(false);
  const [detailError, setDetailError] = useState<string | null>(null);
  const detailRequestRef = useRef(0);
  // 后台刷新可替代旧结果；忙碌状态仍归原明细动作结束，且不能清掉另一个评分命令。
  const detailBusyRef = useRef(0);
  const results = useScoringResults(scopeKey, runDetail);
  const [actioning, setActioning] = useState<string | null>(null);
  const [plan, setPlan] = useState<ScoringPlan | null>(null);
  const [planOpen, setPlanOpen] = useState(false);
  const { setAIBatchIds, aiBatches, setAIBatches, aiProgress, aiIssue, setAIIssue, aiBatchRun, retryableAIBatches } = useScoringAIBatchProgress(scopeKey);
  const commandStorageKey = `scoring-command:${currentTenantId}:${currentUserId}:${initialExamId}`;
  const [pendingCommand, setPendingCommand] = useState(() => readPendingScoringCommand(commandStorageKey));
  const startBusy = useRef<string | null>(null);
  useEffect(() => { setPendingCommand(readPendingScoringCommand(commandStorageKey)); }, [commandStorageKey]);
  const summaryRequestRef = useRef(0);

  const blockingChecks = useMemo(() => readiness?.checks.filter((check) => check.severity === "blocker" && !check.passed) ?? [], [readiness]);
  const warningChecks = useMemo(() => readiness?.checks.filter((check) => check.severity === "warning" && !check.passed) ?? [], [readiness]);
  const hasUnresolvedRun = Boolean(summary?.run && ["queued", "processing", "needs_review", "failed", "cancelling"].includes(summary.run.status));
  const loadSummary = useCallback(async () => {
    const owner = scopeKey;
    const requestId = ++summaryRequestRef.current;
    const detailRequestId = ++detailRequestRef.current;
    if (!initialExamId || !canGrade) {
      setSummary(null);
      setReadiness(null);
      setLoading(false);
      return;
    }
    setLoading(true);
    try {
      const [result, readinessResult] = await Promise.all([getScoringSummary(initialExamId), getScoringReadiness(initialExamId)]);
      if (requestId !== summaryRequestRef.current || (scopeRef.current !== owner || getBusinessCommandSession() !== session)) return;
      setSummary(result.scoring_summary);
      setReadiness(readinessResult.scoring_readiness);
      const runId = result.scoring_summary.run?.id;
      // 概览的待办分类也依赖真实明细；读取失败只退回总数，不能把差额猜成主观题。
      const [batches, detail] = await Promise.all([
        runId ? getScoringRunAIBatches(runId) : Promise.resolve({ batches: [] }),
        runId ? getExamAutomationResults(initialExamId).catch(error => {
          if (requestId === summaryRequestRef.current && detailRequestId === detailRequestRef.current && scopeRef.current === owner && getBusinessCommandSession() === session) setDetailError(formatError(error));
          return null;
        }) : Promise.resolve(null)
      ]);
      if (requestId === summaryRequestRef.current && (scopeRef.current === owner && getBusinessCommandSession() === session)) {
        if (detailRequestId === detailRequestRef.current) {
          setRunDetail(detail);
          if (detail || !runId) setDetailError(null);
        }
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
      if (requestId === summaryRequestRef.current && (scopeRef.current === owner && getBusinessCommandSession() === session)) message.error(formatError(error));
    } finally {
      if (requestId === summaryRequestRef.current && (scopeRef.current === owner && getBusinessCommandSession() === session)) setLoading(false);
    }
  }, [canGrade, initialExamId, message, aiBatchRun, setAIBatchIds, setAIBatches, scopeKey, session]);

  const prepareStart = useCallback(async () => {
    if (!initialExamId || !canGrade) return;
    const owner = scopeKey;
    setActioning("prepare-scoring");
    try {
      const [nextSummary, nextReadiness] = await Promise.all([getScoringSummary(initialExamId), getScoringReadiness(initialExamId)]);
      if ((scopeRef.current !== owner || getBusinessCommandSession() !== session)) return;
      setSummary(nextSummary.scoring_summary);
      setReadiness(nextReadiness.scoring_readiness);
      if (!nextReadiness.scoring_readiness.ready) { message.warning("评分准备检查未通过，请先处理阻塞项"); return; }
      const nextPlan = await loadScoringPlan(initialExamId, nextSummary.scoring_summary.questions);
      if ((scopeRef.current !== owner || getBusinessCommandSession() !== session)) return;
      setPlan(nextPlan);
      setPlanOpen(true);
    } catch (error) { if ((scopeRef.current === owner && getBusinessCommandSession() === session)) message.error(formatError(error)); }
    finally { if ((scopeRef.current === owner && getBusinessCommandSession() === session)) setActioning(null); }
  }, [canGrade, initialExamId, message, scopeKey, session]);

  const start = useCallback(async () => {
    if (!initialExamId || !canGrade || startBusy.current) return;
    const owner = scopeKey;
    startBusy.current = owner;
    setActioning("start-scoring");
    try {
      const completed = await submitScoringCommand({
        examId: initialExamId, storageKey: commandStorageKey, storage: localStorage, isCurrent: () => scopeRef.current === owner && getBusinessCommandSession() === session, pending: value => { if ((scopeRef.current === owner && getBusinessCommandSession() === session)) setPendingCommand(value); },
        ready: async () => {
          if ((scopeRef.current !== owner || getBusinessCommandSession() !== session)) return false;
          const result = await getScoringReadiness(initialExamId);
          if ((scopeRef.current !== owner || getBusinessCommandSession() !== session)) return false;
          setReadiness(result.scoring_readiness);
          if (!result.scoring_readiness.ready) message.warning("评分准备检查未通过，请先处理阻塞项");
          return result.scoring_readiness.ready;
        }
      });
      if (!completed || (scopeRef.current !== owner || getBusinessCommandSession() !== session)) return;
      message.success("评分已启动，系统将在后台继续处理");
      setPlanOpen(false);
      setDetailOpen(true);
      const [nextSummary, detail, nextReadiness] = await Promise.all([
        getScoringSummary(initialExamId),
        getExamAutomationResults(initialExamId),
        getScoringReadiness(initialExamId)
      ]);
      if ((scopeRef.current !== owner || getBusinessCommandSession() !== session)) return;
      setSummary(nextSummary.scoring_summary);
      setRunDetail(detail);
      setReadiness(nextReadiness.scoring_readiness);
      await onTasksChanged();
      // 接续由服务端持久计划负责；浏览器只读取进度，关闭页面不会丢失待执行题目。
    } catch (error) {
      if ((scopeRef.current !== owner || getBusinessCommandSession() !== session)) return;
      message.error(formatError(error));
      setAIIssue(`评分状态暂未确认：${formatError(error)}。请查看当前任务，原启动命令可继续恢复。`);
      void getScoringReadiness(initialExamId).then((result) => { if ((scopeRef.current === owner && getBusinessCommandSession() === session)) setReadiness(result.scoring_readiness); }).catch(() => undefined);
    } finally {
      if (startBusy.current === owner) startBusy.current = null;
      if ((scopeRef.current === owner && getBusinessCommandSession() === session)) setActioning(null);
    }
  }, [canGrade, initialExamId, commandStorageKey, message, onTasksChanged, setAIIssue, scopeKey, session]);

  // 恢复同一服务端计划，不在浏览器拼批次；已成功题目和教师修改由服务端保留。
  const retryAI = useCallback(async () => {
    if (!initialExamId || !canGrade) return;
    const owner = scopeKey;
    setActioning("enqueue-ai");
    try {
      const nextSummary = await getScoringSummary(initialExamId);
      if ((scopeRef.current !== owner || getBusinessCommandSession() !== session)) return;
      const run = nextSummary.scoring_summary.run;
      if (!run || !["queued", "processing", "needs_review", "failed"].includes(run.status)) return;
      await resumeScoringRun(run.id);
      if ((scopeRef.current === owner && getBusinessCommandSession() === session)) { setAIIssue(null); await loadSummary(); }
    } catch (error) {
      if ((scopeRef.current === owner && getBusinessCommandSession() === session)) setAIIssue(`评分接续未完成：${formatError(error)}`);
    } finally { if ((scopeRef.current === owner && getBusinessCommandSession() === session)) setActioning(null); }
  }, [initialExamId, canGrade, loadSummary, setAIIssue, scopeKey, session]);

  const retryFailedAI = useCallback(async () => {
    const owner = scopeKey;
    if (!canGrade) return;
    const run = summary?.run;
    if (!run || !["queued", "processing", "needs_review", "failed"].includes(run.status)) return;
    setActioning("retry-failed-ai");
    try {
      const result = await retryFailedScoringRun(run.id);
      if ((scopeRef.current !== owner || getBusinessCommandSession() !== session)) return;
      setAIIssue(null);
      await loadSummary();
      if ((scopeRef.current === owner && getBusinessCommandSession() === session)) {
        result.requeued > 0 ? message.success(`已重新安排 ${result.requeued} 个失败项`) : message.info("当前没有可重新处理的失败项");
      }
    } catch (error) { if ((scopeRef.current === owner && getBusinessCommandSession() === session)) setAIIssue(`AI 建议重试失败：${formatError(error)}`); }
    finally { if ((scopeRef.current === owner && getBusinessCommandSession() === session)) setActioning(null); }
  }, [loadSummary, message, summary?.run, setAIIssue, scopeKey, canGrade, session]);

  const showDetail = useCallback(async () => {
    if (!initialExamId || !canGrade) return;
    const owner = scopeKey;
    const requestId = ++detailRequestRef.current;
    detailBusyRef.current = requestId;
    setDetailOpen(true);
    setDetailError(null);
    setActioning("scoring-detail");
    try {
      const [nextSummary, detail] = await Promise.all([getScoringSummary(initialExamId), getExamAutomationResults(initialExamId)]);
      if (requestId !== detailRequestRef.current || (scopeRef.current !== owner || getBusinessCommandSession() !== session)) return;
      setSummary(nextSummary.scoring_summary);
      setRunDetail(detail);
    } catch (error) {
      if (requestId === detailRequestRef.current && (scopeRef.current === owner && getBusinessCommandSession() === session)) setDetailError(formatError(error));
    } finally {
      if (detailBusyRef.current === requestId && (scopeRef.current === owner && getBusinessCommandSession() === session)) {
        detailBusyRef.current = 0;
        setActioning(current => current === "scoring-detail" ? null : current);
      }
    }
  }, [initialExamId, canGrade, scopeKey, session]);

  const retry = useCallback(async () => {
    const runId = summary?.run?.id;
    if (!runId || !canGrade) return;
    const owner = scopeKey;
    setActioning("retry-scoring");
    try {
      const result = await retryFailedScoringRun(runId);
      if (scopeRef.current !== owner || getBusinessCommandSession() !== session) return;
      result.requeued > 0 ? message.success(`已重新安排 ${result.requeued} 个失败项`) : message.info("当前没有可重新处理的失败项");
      await Promise.all([loadSummary(), onTasksChanged()]);
    } catch (error) {
      if (scopeRef.current === owner && getBusinessCommandSession() === session) message.error(formatError(error));
    } finally {
      if (scopeRef.current === owner && getBusinessCommandSession() === session) setActioning(null);
    }
  }, [canGrade, loadSummary, message, onTasksChanged, summary?.run?.id, scopeKey, session]);

  const cancel = useCallback(async () => {
    const runId = summary?.run?.id;
    if (!runId || !canGrade) return;
    const owner = scopeKey;
    setActioning("cancel-scoring");
    try {
      await cancelScoringRun(runId);
      if (scopeRef.current !== owner || getBusinessCommandSession() !== session) return;
      message.success("本次评分已取消，未完成任务不会继续写入结果");
      setAIIssue(null);
      await Promise.all([loadSummary(), onTasksChanged()]);
    } catch (error) {
      if (scopeRef.current !== owner || getBusinessCommandSession() !== session) return;
      message.error(formatError(error));
      await loadSummary();
    } finally {
      if (scopeRef.current === owner && getBusinessCommandSession() === session) setActioning(null);
    }
  }, [canGrade, loadSummary, message, onTasksChanged, summary?.run?.id, setAIIssue, scopeKey, session]);

  // biome-ignore lint/correctness/useExhaustiveDependencies: 换考试必须作废旧汇总与影像请求，并丢弃上一场的计划、批次和复核窗口。
  useEffect(() => {
    summaryRequestRef.current += 1;
    detailRequestRef.current += 1;
    detailBusyRef.current = 0;
    setActioning(null);
    startBusy.current = null;
    setSummary(null);
    setReadiness(null);
    setLoading(false);
    setRunDetail(null);
    setDetailOpen(false);
    setDetailError(null);
    setPlan(null);
    setPlanOpen(false);
  }, [scopeKey, session]);

  useEffect(() => {
    void loadSummary();
  }, [loadSummary]);

  useEffect(() => {
    const status = summary?.run?.status;
    if (!initialExamId || !status || !["queued", "processing"].includes(status)) return;
    let disposed = false;
    const owner = scopeKey;
    const refresh = () => {
      if (document.visibilityState === "hidden") return;
      const requestId = ++detailRequestRef.current;
      void Promise.all([getScoringSummary(initialExamId), getExamAutomationResults(initialExamId), getScoringReadiness(initialExamId)])
        .then(([nextSummary, detail, nextReadiness]) => {
          if (disposed || requestId !== detailRequestRef.current || scopeRef.current !== owner || getBusinessCommandSession() !== session) return;
          setSummary(nextSummary.scoring_summary);
          if (detail) { setRunDetail(detail); setDetailError(null); }
          setReadiness(nextReadiness.scoring_readiness);
        })
        .catch(() => undefined);
    };
    const timer = window.setInterval(refresh, 2500);
    return () => {
      disposed = true;
      window.clearInterval(timer);
    };
  }, [initialExamId, summary?.run?.status, scopeKey, session]);

  return {
    ...results,
    pendingCommand,
    summary,
    readiness,
    loading,
    runDetail,
    detailOpen,
    detailError,
    setDetailOpen,
    actioning,
    plan,
    planOpen,
    setPlanOpen,
    aiProgress,
    aiBatches,
    aiIssue,
    prepareStart,
    retryAI,
    retryFailedAI,
    retryableAIBatches,
    blockingChecks,
    warningChecks,
    hasUnresolvedRun,
    loadSummary,
    start,
    showDetail,
    retry,
    cancel,
  };
}

export type ExamScoringController = ReturnType<typeof useExamScoring>;
