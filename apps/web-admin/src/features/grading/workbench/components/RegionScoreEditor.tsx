import { useEffect, useRef, useState, type Dispatch, type SetStateAction } from "react";
import { Alert, Button } from "antd";
import { ApiClientError } from "../../../../api/client";
import { getReviewTaskContext, renewReviewTask, saveReviewDraft, submitHumanGrade, type ReviewTask, type ReviewTaskContext } from "../../../../api/review";
import { SharedScoreControl } from "../../SharedScoreControl";
import { MathMarkdown } from "../../../../components/MathMarkdown";
import { referenceText } from "./FrozenQuestionReference";
import { frozenRubricFromContext } from "../reviewContext";
import { createInitialDraft, serverDraftSnapshot } from "../gradingWorkbench.model";
import { TaskDraftSaveQueue } from "../hooks/taskDraftSaveQueue";
import type { ScoreDraft, WorkbenchContext } from "../gradingWorkbench.types";

type GroupRef = NonNullable<ReviewTaskContext["answer_artifact"]["grading_group_tasks"]>[number];
type Member = { context: ReviewTaskContext; score: number | null; points: Record<string, number>; reason: string;
  dirty: boolean; saved: boolean; busy: boolean; edit: number; submittedScore?: number; error?: string; saveError?: string; conflicted?: boolean };

export function groupTaskRefs(context: WorkbenchContext): GroupRef[] {
  return context.reviewContext.answer_artifact?.grading_group_tasks ?? [];
}

/** 共用图像只改变阅读范围；每行使用自己的任务版本、冻结量规、草稿和独立提交回执。 */
export function RegionScoreEditor({ context, currentUserId, canGrade, draft, setDraft, onStateChange, onSubmitted, onDraftSaved, isCurrentScope, registerSubmit }: {
  context: WorkbenchContext; currentUserId: string; canGrade: boolean; draft: ScoreDraft;
  setDraft: Dispatch<SetStateAction<ScoreDraft>>; onStateChange: (dirty: boolean, busy: boolean) => void;
  onSubmitted: (task: ReviewTask) => void;
  onDraftSaved: (taskId: string) => void;
  isCurrentScope: () => boolean;
  registerSubmit?: (submit: (() => Promise<boolean>) | null) => void;
}) {
  const refs = groupTaskRefs(context);
  const key = `${context.task.tenant_id}:${currentUserId}:${context.task.id}:${context.task.submission_id}:${context.task.grade_round}:${context.gradingGroupId}`;
  const [rows, setRows] = useState<Member[]>([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const generation = useRef(0);
  const inFlight = useRef(new Set<string>());
  const draftQueue = useRef(new TaskDraftSaveQueue());
  const draftConflicts = useRef(new Set<string>());
  const editSequence = useRef(0);
  const autoAttempts = useRef(new Map<string, number>());
  const batchLock = useRef(false);
  const [batchBusy, setBatchBusy] = useState(false);
  const live = useRef(rows); live.current = rows;
  const currentDraft = useRef(draft); currentDraft.current = draft;
  const tasksKey = refs.map(ref => ref.id).sort().join("|");
  const draftKey = (id: string) => JSON.stringify([context.task.tenant_id, currentUserId, id]);
  // biome-ignore lint/correctness/useExhaustiveDependencies: key 与 tasksKey 包含会话、答卷、轮次和成员；单题回执不能重载整区并覆盖其它行输入。
  useEffect(() => {
    const version = ++generation.current;
    const controller = new AbortController();
    live.current = []; setRows([]); setError(""); setLoading(true);
    batchLock.current = false; setBatchBusy(false);
    void Promise.all(refs.map(async ref => {
      const value = ref.id === context.task.id ? context.reviewContext : (await getReviewTaskContext(ref.id, controller.signal)).context;
      // 即使服务端已聚合，延迟响应仍须绑定当前人、答卷和评分轮次，不能把另一份答案带进评分行。
      if (value.task.id !== ref.id || value.task.question_id !== ref.question_id || value.task.tenant_id !== context.task.tenant_id
          || value.task.assigned_to !== currentUserId || value.task.submission_id !== context.task.submission_id
          || value.task.grade_round !== context.task.grade_round || value.task.exam_id !== context.task.exam_id
          || value.answer_artifact.grading_group_id !== context.gradingGroupId) throw new Error("group_context_changed");
      draftQueue.current.observeRevision(draftKey(ref.id), value.draft?.revision ?? 0);
      draftConflicts.current.delete(draftKey(ref.id));
      const current = ref.id === context.task.id ? currentDraft.current : null;
      const dirty = Boolean(current && (current.score !== (value.draft?.score ?? null)
        || current.comments !== (value.draft?.comments ?? "") || current.privateNote !== (value.draft?.private_note ?? "")
        || current.studentFeedback !== (value.draft?.student_feedback ?? "")
        || current.reason !== (value.draft?.viewer_state.grading_reason ?? "")
        || Object.entries(current.rubricSelections).some(([id, score]) => score !== (value.draft?.rubric_selections.find(point => point.point_id === id)?.score ?? 0))));
      return { context: value, score: value.draft?.score ?? null, submittedScore: value.human_grade?.score,
        points: Object.fromEntries((value.draft?.rubric_selections ?? []).map(point => [point.point_id, point.score])),
        reason: typeof value.draft?.viewer_state.grading_reason === "string" ? value.draft.viewer_state.grading_reason : "", dirty, saved: false, busy: false, edit: dirty ? ++editSequence.current : 0 } as Member;
    })).then(values => { if (generation.current === version && isCurrentScope()) { live.current = values; setRows(values); } })
      .catch(() => { if (!controller.signal.aborted && generation.current === version) setError("本区题目加载失败，请重新打开当前答卷。"); })
      .finally(() => { if (generation.current === version) setLoading(false); });
    return () => { ++generation.current; controller.abort(); onStateChange(false, false); };
    // 同区提交一题只改变该行版本，不重载其它未保存评分；会话、成员或答卷变化才重新加载。
  }, [key, tasksKey]);
  useEffect(() => {
    // 草稿保存成功仍未完成交分；整区内任一题已有分数而没有提交回执，都须保留退出提示。
    const pendingSubmission = rows.some(row => ["assigned", "in_progress", "returned"].includes(row.context.task.status)
      && (row.context.task.id === context.task.id ? draft.score : row.score) !== null);
    onStateChange(pendingSubmission, batchBusy || rows.some(row => row.busy));
  }, [rows, onStateChange, context.task.id, draft.score, batchBusy]);
  // 网络回执与输入事件共用同步引用；同批 React 更新尚未绘制时也不能用旧行覆盖后续输入。
  const update = (id: string, patch: Partial<Member>) => {
    live.current = live.current.map(row => row.context.task.id === id ? { ...row, ...patch } : row);
    setRows(live.current);
  };
  const rowValue = (row: Member) => {
    const isCurrent = row.context.task.id === context.task.id;
    const source = row.context.draft;
    const value = isCurrent ? currentDraft.current : null;
    return { score: value ? value.score : row.score, points: value ? value.rubricSelections : row.points,
      reason: value ? value.reason : row.reason,
      comments: value ? value.comments : source?.comments ?? "",
      private_note: value ? value.privateNote : source?.private_note ?? "",
      student_feedback: value ? value.studentFeedback : source?.student_feedback ?? "" };
  };
  const saveRow = async (id: string): Promise<boolean> => {
    const version = generation.current, queueKey = draftKey(id);
    const row = live.current.find(item => item.context.task.id === id);
    if (!isCurrentScope() || !row || !canGrade || draftConflicts.current.has(queueKey)
        || row.context.task.assigned_to !== currentUserId || !row.context.claim.can_renew
        || !["assigned", "in_progress", "returned"].includes(row.context.task.status)) return false;
    const value = rowValue(row), edit = row.edit;
    if (value.score !== null && (!Number.isFinite(value.score) || value.score < 0 || value.score > row.context.question.score)) {
      update(id, { saveError: "本题得分超出分值范围，草稿尚未保存。" }); return false;
    }
    try {
      const revision = await draftQueue.current.enqueue(queueKey, async expectedRevision => {
        if (generation.current !== version || !isCurrentScope() || draftConflicts.current.has(queueKey)) return null;
        await renewReviewTask(id);
        if (generation.current !== version || !isCurrentScope()) return null;
        const saved = await saveReviewDraft(id, { score: value.score,
          rubric_selections: Object.entries(value.points).map(([point_id, score]) => ({ point_id, score })),
          comments: value.comments, private_note: value.private_note, student_feedback: value.student_feedback,
          viewer_state: { ...(row.context.draft?.viewer_state ?? {}), grading_reason: value.reason },
          expected_revision: expectedRevision, client_updated_at: new Date().toISOString() });
        if (generation.current !== version || !isCurrentScope()) return saved.draft.revision;
        const latest = live.current.find(item => item.context.task.id === id);
        if (latest) {
          // 旧保存只推进服务端版本；教师在等待期间继续输入时，保留新分数并继续安排下一次草稿保存。
          const sameEdit = latest.edit === edit;
          update(id, { context: { ...latest.context, draft: saved.draft }, dirty: !sameEdit,
            saved: sameEdit, saveError: undefined });
          onDraftSaved(id);
        }
        return saved.draft.revision;
      });
      return revision !== null && generation.current === version && isCurrentScope() && !draftConflicts.current.has(queueKey);
    } catch (cause) {
      if (generation.current === version && isCurrentScope()) {
        const conflict = cause instanceof ApiClientError && cause.status === 409;
        if (conflict) draftConflicts.current.add(queueKey);
        update(id, { conflicted: conflict, saveError: conflict
          ? "草稿已被其他会话修改。本页输入暂保留，请重读并采用服务端草稿后继续。"
          : "本题草稿保存失败，请联网后重试保存；当前输入仍保留。" });
      }
      return false;
    }
  };
  const autoSave = useRef(saveRow); autoSave.current = saveRow;
  useEffect(() => {
    if (batchBusy || !canGrade) return;
    // 停止输入后只保存草稿；正式交分必须等待教师点击下一份或重试提交。
    const timers = rows.filter(row => row.dirty && row.edit && !row.busy && !row.conflicted
      && autoAttempts.current.get(row.context.task.id) !== row.edit).map(row => window.setTimeout(() => {
      if (batchLock.current || inFlight.current.has(row.context.task.id)) return;
      autoAttempts.current.set(row.context.task.id, row.edit);
      void autoSave.current(row.context.task.id);
    }, 1200));
    return () => timers.forEach(timer => window.clearTimeout(timer));
  }, [rows, batchBusy, canGrade]);
  const reloadRowDraft = async (id: string) => {
    const version = generation.current;
    update(id, { busy: true });
    try {
      const fresh = (await getReviewTaskContext(id)).context;
      if (generation.current !== version || !isCurrentScope()) return;
      const previous = live.current.find(row => row.context.task.id === id);
      if (!previous || fresh.task.id !== id || fresh.task.assigned_to !== currentUserId
          || fresh.task.tenant_id !== context.task.tenant_id || fresh.task.exam_id !== context.task.exam_id
          || fresh.task.submission_id !== context.task.submission_id || fresh.task.question_id !== previous.context.task.question_id
          || fresh.task.grade_round !== context.task.grade_round || fresh.answer_artifact.grading_group_id !== context.gradingGroupId) throw new Error("group_context_changed");
      const initial = createInitialDraft({ ...context, task: fresh.task, reviewContext: fresh });
      const restored = serverDraftSnapshot(initial, fresh.draft, false, "group").snapshot.draft;
      draftConflicts.current.delete(draftKey(id));
      draftQueue.current.observeRevision(draftKey(id), fresh.draft?.revision ?? 0);
      // 仅在教师明确采用服务端草稿后替换这一行；同区其它题尚未提交的输入不受影响。
      if (id === context.task.id) { currentDraft.current = restored; setDraft(restored); }
      update(id, { context: fresh, score: restored.score, points: restored.rubricSelections, reason: restored.reason,
        submittedScore: fresh.human_grade?.score, dirty: false, saved: true, edit: 0, conflicted: false, saveError: undefined, error: undefined });
      if (["submitted", "completed"].includes(fresh.task.status)) onSubmitted(fresh.task);
    } catch {
      if (generation.current === version && isCurrentScope()) update(id, { saveError: "服务端草稿读取失败，当前输入仍保留，请稍后重试。" });
    } finally { if (generation.current === version && isCurrentScope()) update(id, { busy: false }); }
  };
  const action = async (id: string): Promise<boolean> => {
    const version = generation.current, queueKey = draftKey(id);
    const row = live.current.find(item => item.context.task.id === id);
    if (!isCurrentScope() || !row || inFlight.current.has(id) || row.busy || !canGrade || row.context.task.assigned_to !== currentUserId
        || !row.context.claim.can_renew || !["assigned", "in_progress", "returned"].includes(row.context.task.status)) return false;
    const value = rowValue(row);
    if (value.score === null) { update(id, { error: "请填写本题得分，0 分也需要明确输入。" }); return false; }
    if (!Number.isFinite(value.score) || value.score < 0 || value.score > row.context.question.score) { update(id, { error: "本题得分超出分值范围。" }); return false; }
    inFlight.current.add(id); update(id, { busy: true, error: undefined });
    try {
      // 显式交分与自动草稿共用每题版本队列，先等旧保存落定；冲突不能被交分绕过。
      await draftQueue.current.whenIdle(queueKey);
      if (generation.current !== version || !isCurrentScope() || draftConflicts.current.has(queueKey) || !await saveRow(id)) return false;
      const latest = live.current.find(item => item.context.task.id === id)!;
      const fields = rowValue(latest);
      const result = await submitHumanGrade(id, { comments: fields.comments, private_note: fields.private_note,
        student_feedback: fields.student_feedback, expected_revision: latest.context.expected_revision,
        score_only: true, score: value.score, rubric_selections: [], reason: value.reason || "教师复核完成" });
      if (generation.current !== version || !isCurrentScope()) return false;
      update(id, { context: { ...latest.context, task: result.task, expected_revision: result.task.revision }, submittedScore: result.human_grade?.score ?? value.score, dirty: false, saved: true });
      onSubmitted(result.task);
      return true;
    } catch (cause) { if (generation.current === version && isCurrentScope()) update(id, { error: cause instanceof ApiClientError && cause.code === "review_task_marked" ? "本题已标记，分数仅保存为草稿。" : "本题未能提交，请重试；其它题已提交的结果保留。" }); return false; }
    finally { inFlight.current.delete(id); if (generation.current === version && isCurrentScope()) update(id, { busy: false }); }
  };
  const latestSubmit = useRef<() => Promise<boolean>>(async () => false);
  latestSubmit.current = async () => {
    if (loading || error || batchLock.current || !isCurrentScope()) return false;
    const batchGeneration = generation.current;
    const pending = live.current.filter(row => ["assigned", "in_progress", "returned"].includes(row.context.task.status));
    if (pending.some(row => draftConflicts.current.has(draftKey(row.context.task.id)))) return false;
    // 一区多题先完整核对；不能因为下一行未录分而提交前一行后仍跳走。
    const missing = pending.find(row => (row.context.task.id === context.task.id ? currentDraft.current.score : row.score) === null);
    if (missing) { update(missing.context.task.id, { error: "请填写本题得分，0 分也需要明确输入。" }); return false; }
    for (const row of pending) {
      const score = row.context.task.id === context.task.id ? currentDraft.current.score : row.score;
      if (score === null || !Number.isFinite(score) || score < 0 || score > row.context.question.score) {
        update(row.context.task.id, { error: "本题得分超出分值范围。" }); return false;
      }
    }
    // 整区确认期间固定输入；重复快捷键或点击不能另开一批，也不能中途改动后续行。
    batchLock.current = true; setBatchBusy(true);
    try {
      for (const row of pending) if (!await action(row.context.task.id)) return false;
      return true;
    } finally { if (generation.current === batchGeneration) { batchLock.current = false; setBatchBusy(false); } }
  };
  useEffect(() => {
    registerSubmit?.(() => latestSubmit.current());
    return () => registerSubmit?.(null);
  }, [registerSubmit]);
  const missing = (context.reviewContext.answer_artifact.grading_group_question_ids?.length ?? refs.length) - refs.length;
  return <section className="score-panel" aria-label="整区逐题录分">
    <div className="panel-head"><div><h2>整区批阅 · 各题分别录分</h2><p>共用左侧答题图，各题使用独立评分细则并分别保存。</p></div></div>
    {loading ? <p role="status">正在加载本区题目…</p> : null}
    {error ? <Alert type="error" message={error} /> : null}
    {missing > 0 ? <Alert type="info" message={`本区另有 ${missing} 道题尚未分配给你，请管理员按本区题目分配任务。`} /> : null}
    {rows.map(row => {
      const id = row.context.task.id, isCurrent = id === context.task.id;
      const finished = ["submitted", "completed"].includes(row.context.task.status);
      const editable = canGrade && !row.conflicted && row.context.claim.can_renew && !finished && ["assigned", "in_progress", "returned"].includes(row.context.task.status);
      const rubric = frozenRubricFromContext(row.context);
      const value = isCurrent ? { score: draft.score, rubricSelections: draft.rubricSelections } : { score: row.score, rubricSelections: row.points };
      return <section key={id} aria-label={`第 ${row.context.task.question_no} 题评分`}>
        <h3>第 {row.context.task.question_no} 题 · 满分 {row.context.question.score}{finished ? " · 已提交" : ""}</h3>
        <details><summary>标准答案与本题评分细则</summary><MathMarkdown>{referenceText(row.context.question.answer_key) || "未保存参考答案"}</MathMarkdown>
          {rubric.points.map(point => <div key={point.id}><MathMarkdown>{point.description}</MathMarkdown><span>{point.score} 分</span></div>)}</details>
        {finished ? <p role="status">{row.submittedScore === undefined ? "本题已提交，得分请查看提交记录。" : `本题已提交：${row.submittedScore} / ${row.context.question.score} 分`}</p> : <>
        <SharedScoreControl disabled={!editable || row.busy || batchBusy} maxScore={row.context.question.score} rubricPoints={rubric.points} showRubric={false} value={value}
          onChange={change => { if (isCurrent) { currentDraft.current = { ...currentDraft.current, ...change }; setDraft(currentDraft.current); } update(id, { score: change.score, points: change.rubricSelections, dirty: true, saved: false, edit: ++editSequence.current, error: undefined, saveError: undefined }); }} />
        </>}
        {row.saveError ? <p role="alert">{row.saveError}</p> : null}
        {!finished && row.saveError ? row.conflicted
          ? <Button disabled={row.busy || batchBusy} onClick={() => void reloadRowDraft(id)}>重读并采用服务端草稿</Button>
          : <Button disabled={!editable || row.busy || batchBusy} onClick={() => void saveRow(id)}>重试保存草稿</Button> : null}
        {row.error ? <p role="alert">{row.error}</p> : null}
        {!finished && row.error ? <Button disabled={!editable || row.busy || batchBusy} loading={row.busy} onClick={() => void action(id)}>重试本题提交</Button> : null}
      </section>;
    })}
  </section>;
}
