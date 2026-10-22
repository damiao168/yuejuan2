import { useCallback, useEffect, useRef, useState } from "react";
import { ApiClientError } from "../../../../api/client";
import { getBusinessCommandSession } from "../../../../api/businessCommand";
import { getQuestionAssessmentSnapshot, type AssessmentSnapshot } from "../../../../api/assessment";
import { getScoringRun, type ScoringRunItem } from "../../../../api/review";
import { createScoringAITrial, decideScoringAITrial, enqueueScoringAITrial, getScoringRunAITrials,
  type CreateScoringAITrialInput, type ScoringTrial } from "../../../../api/scoringTrials";
import { formatError } from "../gradingWorkbench.model";

type TrialRunItem = Pick<ScoringRunItem, "answer_segment_id" | "question_id" | "question_no" | "anonymous_code" | "state" | "question_type">;

/** 服务端保存试改事实；本地只暂存回执尚未确认的创建命令，不能用本地布尔值批准扩批。 */
export function useScoringTrials(examId: string, runId: string, scopeKey: string) {
  const [items, setItems] = useState<TrialRunItem[]>([]);
  const [trials, setTrials] = useState<ScoringTrial[]>([]);
  const [trialId, setTrialId] = useState("");
  const [questionId, setQuestionId] = useState("");
  const [snapshot, setSnapshot] = useState<AssessmentSnapshot | null>(null);
  const [loading, setLoading] = useState(true);
  const [snapshotLoading, setSnapshotLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [pending, setPending] = useState<CreateScoringAITrialInput | null>(null);
  const ownerKey = `${scopeKey}:${examId}:${runId}`;
  const owner = useRef(ownerKey);
  const mounted = useRef(true);
  const actionLock = useRef(false);
  const refreshSequence = useRef(0);
  owner.current = ownerKey;
  const expectedSession = getBusinessCommandSession();
  // 刷新或同账号重登会改变会话代次；原命令按身份、考试和运行恢复，旧异步仍由完整 owner 和会话对象作废。
  const commandKey = `scoring-trial-command:${expectedSession.scope}:${examId}:${runId}`;
  const active = () => mounted.current && owner.current === ownerKey && getBusinessCommandSession() === expectedSession;
  const saveTrial = (trial: ScoringTrial) => {
    setTrials(current => [trial, ...current.filter(item => item.id !== trial.id)]);
    setTrialId(trial.id);
    setQuestionId(trial.question_id);
  };

  const refresh = useCallback(async () => {
    const sequence = ++refreshSequence.current;
    try {
      const result = await getScoringRunAITrials(runId);
      if (!mounted.current || owner.current !== ownerKey || getBusinessCommandSession() !== expectedSession || sequence !== refreshSequence.current) return;
      setTrials(result.trials);
      setError("");
    } catch (cause) {
      if (mounted.current && owner.current === ownerKey && getBusinessCommandSession() === expectedSession && sequence === refreshSequence.current) setError(formatError(cause));
    }
  }, [runId, ownerKey, expectedSession]);

  useEffect(() => {
    mounted.current = true;
    const controller = new AbortController();
    let disposed = false;
    setLoading(true);
    setItems([]); setTrials([]); setTrialId(""); setQuestionId(""); setSnapshot(null); setError("");
    let restored: CreateScoringAITrialInput | null = null;
    try {
      const raw = sessionStorage.getItem(commandKey);
      if (raw) {
        const parsed = JSON.parse(raw) as CreateScoringAITrialInput;
        if (typeof parsed.command_id === "string" && typeof parsed.question_id === "string" && Array.isArray(parsed.samples)) restored = parsed;
      }
    } catch { /* 读取失败不代表旧命令获准，列表恢复后仍以服务端记录为准。 */ }
    setPending(restored);
    void Promise.all([getScoringRun(runId), getScoringRunAITrials(runId, controller.signal)])
      .then(([detail, response]) => {
        if (disposed || owner.current !== ownerKey || getBusinessCommandSession() !== expectedSession) return;
        setItems(detail.items);
        setTrials(response.trials);
        const recovered = restored && response.trials.find(trial => trial.command_id === restored.command_id);
        const selected = recovered || (restored ? undefined : response.trials[0]);
        if (selected) { setTrialId(selected.id); setQuestionId(selected.question_id); }
        else setQuestionId(restored?.question_id ?? detail.items.find(item => item.state === "review" && ["short_answer", "calculation", "essay", "discussion"].includes(item.question_type))?.question_id ?? "");
        if (recovered) { try { sessionStorage.removeItem(commandKey); } catch { /* 远端记录已确认，清理失败不能抹掉恢复结果。 */ } setPending(null); }
      })
      .catch(cause => { if (!disposed && owner.current === ownerKey && getBusinessCommandSession() === expectedSession) setError(formatError(cause)); })
      .finally(() => { if (!disposed && owner.current === ownerKey && getBusinessCommandSession() === expectedSession) setLoading(false); });
    return () => { disposed = true; mounted.current = false; refreshSequence.current += 1; controller.abort(); };
  }, [runId, ownerKey, expectedSession, commandKey]);

  useEffect(() => {
    if (!questionId) { setSnapshot(null); return; }
    let disposed = false;
    setSnapshot(null); setSnapshotLoading(true);
    void getQuestionAssessmentSnapshot(examId, questionId)
      .then(result => { if (!disposed && owner.current === ownerKey && getBusinessCommandSession() === expectedSession) setSnapshot(result.assessment_snapshot); })
      .catch(cause => { if (!disposed && owner.current === ownerKey && getBusinessCommandSession() === expectedSession) setError(formatError(cause)); })
      .finally(() => { if (!disposed && owner.current === ownerKey && getBusinessCommandSession() === expectedSession) setSnapshotLoading(false); });
    return () => { disposed = true; };
  }, [examId, questionId, ownerKey, expectedSession]);

  // AI 工作在 needs_review 阶段仍会运行；不能依赖正式评分 run 的 queued/processing 才刷新结果。
  useEffect(() => {
    if (!trials.some(trial => ["awaiting_enqueue", "running", "expanded"].includes(trial.status))) return;
    const timer = window.setInterval(() => { if (!actionLock.current && document.visibilityState !== "hidden") void refresh(); }, 4000);
    return () => window.clearInterval(timer);
  }, [trials, refresh]);

  const perform = async (operation: () => Promise<void>) => {
    if (!active() || actionLock.current) return;
    actionLock.current = true; setBusy(true); setError("");
    refreshSequence.current += 1;
    try { await operation(); }
    catch (cause) { if (active()) setError(formatError(cause)); }
    finally { actionLock.current = false; if (active()) setBusy(false); }
  };

  const enqueue = async (trial: ScoringTrial) => {
    if (!active()) return;
    const response = await enqueueScoringAITrial(runId, trial.id);
    if (!active()) return;
    await refresh();
    if (response.enqueue_result.failed_count > 0 && active()) setError(`${response.enqueue_result.failed_count} 个建议尚未入队，请继续安排原批次；人工阅卷可继续。`);
  };

  const create = (draft: Omit<CreateScoringAITrialInput, "command_id">) => perform(async () => {
    const command = pending ?? { ...draft, command_id: crypto.randomUUID() };
    // 先耐久保存原命令，再发请求；回执丢失时不能把后来编辑的样本作为同一次请求。
    sessionStorage.setItem(commandKey, JSON.stringify(command));
    setPending(command);
    let result;
    try { result = await createScoringAITrial(runId, command); }
    catch (cause) {
      // 已明确拒绝的输入可修改后重建；网络或未知结果继续保留原命令，不能猜测没有被接受。
      if (active() && cause instanceof ApiClientError && [400, 422].includes(cause.status)) {
        try { sessionStorage.removeItem(commandKey); } catch { /* 清理失败时下次仍能核对原命令。 */ }
        setPending(null);
      }
      throw cause;
    }
    if (!active()) return;
    saveTrial(result.trial);
    try { sessionStorage.removeItem(commandKey); } catch { /* 原命令已接受，后续恢复仍能按 command_id 找回。 */ } setPending(null);
    await enqueue(result.trial);
  });

  const decide = (trial: ScoringTrial, decision: "stop" | "expand", reason: string) => perform(async () => {
    const result = await decideScoringAITrial(runId, trial.id, { decision, reason, expected_revision: trial.revision });
    if (!active()) return;
    saveTrial(result.trial);
    if (decision === "expand") await enqueue(result.trial);
  });

  const selectTrial = (id: string) => {
    setTrialId(id);
    const trial = trials.find(item => item.id === id);
    if (trial) setQuestionId(trial.question_id);
  };

  return { items, trials, trial: trials.find(item => item.id === trialId) ?? null, trialId, selectTrial,
    questionId, setQuestionId, snapshot, snapshotLoading, loading, busy, error, pending, refresh,
    create, decide, resume: (trial: ScoringTrial) => perform(() => enqueue(trial)) };
}
