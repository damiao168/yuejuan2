import { useEffect, useRef, useState, type Dispatch, type SetStateAction } from "react";
import { Alert, Button } from "antd";
import { ApiClientError } from "../../../../api/client";
import { getReviewTaskContext, renewReviewTask, saveReviewDraft, submitHumanGrade, type ReviewTask, type ReviewTaskContext } from "../../../../api/review";
import { SharedScoreControl } from "../../SharedScoreControl";
import { MathMarkdown } from "../../../../components/MathMarkdown";
import { referenceText } from "./FrozenQuestionReference";
import { frozenRubricFromContext } from "../reviewContext";
import type { ScoreDraft, WorkbenchContext } from "../gradingWorkbench.types";

type GroupRef = NonNullable<ReviewTaskContext["answer_artifact"]["grading_group_tasks"]>[number];
type Member = { context: ReviewTaskContext; score: number | null; points: Record<string, number>; reason: string;
  dirty: boolean; saved: boolean; busy: boolean; edit?: number; submittedScore?: number; error?: string };

export function groupTaskRefs(context: WorkbenchContext): GroupRef[] {
  return context.reviewContext.answer_artifact?.grading_group_tasks ?? [];
}

/** 共用图像只改变阅读范围；每行使用自己的任务版本、冻结量规、草稿和独立提交回执。 */
export function RegionScoreEditor({ context, currentUserId, canGrade, draft, setDraft, onStateChange, onSubmitted, onDraftSaved, isCurrentScope }: {
  context: WorkbenchContext; currentUserId: string; canGrade: boolean; draft: ScoreDraft;
  setDraft: Dispatch<SetStateAction<ScoreDraft>>; onStateChange: (dirty: boolean, busy: boolean) => void;
  onSubmitted: (task: ReviewTask) => void;
  onDraftSaved: (taskId: string) => void;
  isCurrentScope: () => boolean;
}) {
  const refs = groupTaskRefs(context);
  const key = `${context.task.tenant_id}:${currentUserId}:${context.task.id}:${context.task.submission_id}:${context.task.grade_round}:${context.gradingGroupId}`;
  const [rows, setRows] = useState<Member[]>([]);
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(false);
  const generation = useRef(0);
  const inFlight = useRef(new Set<string>());
  const editSequence = useRef(0);
  const autoAttempts = useRef(new Set<number>());
  const live = useRef(rows); live.current = rows;
  const currentDraft = useRef(draft); currentDraft.current = draft;
  const tasksKey = refs.map(ref => ref.id).sort().join("|");
  useEffect(() => {
    const version = ++generation.current;
    const controller = new AbortController();
    setRows([]); setError(""); setLoading(true);
    void Promise.all(refs.map(async ref => {
      const value = ref.id === context.task.id ? context.reviewContext : (await getReviewTaskContext(ref.id, controller.signal)).context;
      // 即使服务端已聚合，延迟响应仍须绑定当前人、答卷和评分轮次，不能把另一份答案带进评分行。
      if (value.task.id !== ref.id || value.task.question_id !== ref.question_id || value.task.tenant_id !== context.task.tenant_id
          || value.task.assigned_to !== currentUserId || value.task.submission_id !== context.task.submission_id
          || value.task.grade_round !== context.task.grade_round || value.task.exam_id !== context.task.exam_id
          || value.answer_artifact.grading_group_id !== context.gradingGroupId) throw new Error("group_context_changed");
      const current = ref.id === context.task.id ? currentDraft.current : null;
      const dirty = Boolean(current && (current.score !== (value.draft?.score ?? null)
        || current.comments !== (value.draft?.comments ?? "") || current.privateNote !== (value.draft?.private_note ?? "")
        || current.studentFeedback !== (value.draft?.student_feedback ?? "")
        || current.reason !== (value.draft?.viewer_state.grading_reason ?? "")
        || Object.entries(current.rubricSelections).some(([id, score]) => score !== (value.draft?.rubric_selections.find(point => point.point_id === id)?.score ?? 0))));
      return { context: value, score: value.draft?.score ?? null, submittedScore: value.human_grade?.score,
        points: Object.fromEntries((value.draft?.rubric_selections ?? []).map(point => [point.point_id, point.score])),
        reason: typeof value.draft?.viewer_state.grading_reason === "string" ? value.draft.viewer_state.grading_reason : "", dirty, saved: false, busy: false } as Member;
    })).then(values => { if (generation.current === version && isCurrentScope()) setRows(values); })
      .catch(() => { if (!controller.signal.aborted && generation.current === version) setError("本区题目加载失败，请重新打开当前答卷。"); })
      .finally(() => { if (generation.current === version) setLoading(false); });
    return () => { ++generation.current; controller.abort(); onStateChange(false, false); };
    // 同区提交一题只改变该行版本，不重载其它未保存评分；会话、成员或答卷变化才重新加载。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, tasksKey]);
  useEffect(() => { onStateChange(rows.some(row => row.dirty), rows.some(row => row.busy)); }, [rows, onStateChange]);
  const update = (id: string, patch: Partial<Member>) => setRows(values => values.map(row => row.context.task.id === id ? { ...row, ...patch } : row));
  const action = async (id: string) => {
    const version = generation.current;
    const row = live.current.find(item => item.context.task.id === id);
    if (!isCurrentScope() || !row || inFlight.current.has(id) || row.busy || !canGrade || row.context.task.assigned_to !== currentUserId
        || !row.context.claim.can_renew || !["assigned", "in_progress", "returned"].includes(row.context.task.status)) return;
    const isCurrent = id === context.task.id;
    const value = isCurrent ? { score: currentDraft.current.score, points: currentDraft.current.rubricSelections, reason: currentDraft.current.reason } : row;
    if (value.score === null) return;
    if (value.score !== null && (!Number.isFinite(value.score) || value.score < 0 || value.score > row.context.question.score)) { update(id, { error: "本题得分超出分值范围。" }); return; }
    inFlight.current.add(id);
    update(id, { busy: true, error: undefined });
    try {
      await renewReviewTask(id);
      if (generation.current !== version || !isCurrentScope()) return;
      const selections = Object.entries(value.points).map(([point_id, score]) => ({ point_id, score }));
      const source = row.context.draft;
      const fields = isCurrent ? { comments: currentDraft.current.comments, private_note: currentDraft.current.privateNote,
        student_feedback: currentDraft.current.studentFeedback } : { comments: source?.comments ?? "", private_note: source?.private_note ?? "", student_feedback: source?.student_feedback ?? "" };
      // 先保存本题草稿，标记拦截或正式提交失败时仍可恢复教师已填的分数。
      const saved = await saveReviewDraft(id, { ...fields, score: value.score, rubric_selections: selections,
        viewer_state: source?.viewer_state ?? {}, expected_revision: source?.revision ?? 0, client_updated_at: new Date().toISOString() });
      if (generation.current !== version || !isCurrentScope()) return;
      update(id, { context: { ...row.context, draft: saved.draft }, saved: true, dirty: false });
      onDraftSaved(id);
      const result = await submitHumanGrade(id, { ...fields, expected_revision: row.context.expected_revision,
        auto_submit: true, score: value.score, rubric_selections: [], reason: value.reason || "教师复核完成" });
      if (generation.current !== version || !isCurrentScope()) return;
      update(id, { context: { ...row.context, task: result.task, expected_revision: result.task.revision }, submittedScore: result.human_grade?.score ?? value.score, dirty: false, saved: true });
      onSubmitted(result.task);
    } catch (cause) { if (generation.current === version) update(id, { error: cause instanceof ApiClientError && cause.code === "review_task_marked" ? "本题已标记，分数仅保存为草稿。" : "本题未能提交，请重试；其它题已提交的结果保留。" }); }
    finally { inFlight.current.delete(id); if (generation.current === version) update(id, { busy: false }); }
  };
  const autoAction = useRef(action); autoAction.current = action;
  useEffect(() => {
    // 每行独立去抖，只有教师本次主动填写才提交；失败不循环重试，也不提交其它行。
    const timers = rows.filter(row => row.edit && !autoAttempts.current.has(row.edit) && row.dirty && !row.busy && row.score !== null).map(row => window.setTimeout(() => {
      autoAttempts.current.add(row.edit!);
      void autoAction.current(row.context.task.id);
    }, 1200));
    return () => timers.forEach(timer => window.clearTimeout(timer));
  }, [rows]);
  const missing = (context.reviewContext.answer_artifact.grading_group_question_ids?.length ?? refs.length) - refs.length;
  return <section className="score-panel" aria-label="整区逐题录分">
    <div className="panel-head"><div><h2>整区批阅 · 各题分别录分</h2><p>共用左侧答题图，各题使用独立评分细则并分别保存。</p></div></div>
    {loading ? <p role="status">正在加载本区题目…</p> : null}
    {error ? <Alert type="error" message={error} /> : null}
    {missing > 0 ? <Alert type="info" message={`本区另有 ${missing} 道题尚未分配给你，请管理员按本区题目分配任务。`} /> : null}
    {rows.map(row => {
      const id = row.context.task.id, isCurrent = id === context.task.id;
      const finished = ["submitted", "completed"].includes(row.context.task.status);
      const editable = canGrade && row.context.claim.can_renew && !finished && ["assigned", "in_progress", "returned"].includes(row.context.task.status);
      const rubric = frozenRubricFromContext(row.context);
      const value = isCurrent ? { score: draft.score, rubricSelections: draft.rubricSelections } : { score: row.score, rubricSelections: row.points };
      return <section key={id} aria-label={`第 ${row.context.task.question_no} 题评分`}>
        <h3>第 {row.context.task.question_no} 题 · 满分 {row.context.question.score}{finished ? " · 已提交" : ""}</h3>
        <details><summary>标准答案与本题评分细则</summary><MathMarkdown>{referenceText(row.context.question.answer_key) || "未保存参考答案"}</MathMarkdown>
          {rubric.points.map(point => <div key={point.id}><MathMarkdown>{point.description}</MathMarkdown><span>{point.score} 分</span></div>)}</details>
        {finished ? <p role="status">{row.submittedScore === undefined ? "本题已提交，得分请查看提交记录。" : `本题已提交：${row.submittedScore} / ${row.context.question.score} 分`}</p> : <>
        <SharedScoreControl disabled={!editable || row.busy} maxScore={row.context.question.score} rubricPoints={rubric.points} showRubric={false} value={value}
          onChange={change => { if (isCurrent) setDraft(current => ({ ...current, ...change })); update(id, { score: change.score, points: change.rubricSelections, dirty: true, saved: false, edit: ++editSequence.current, error: undefined }); }} />
        </>}
        {row.error ? <p role="alert">{row.error}</p> : null}
        {!finished && row.error ? <Button disabled={!editable || row.busy} loading={row.busy} onClick={() => void action(id)}>重试本题提交</Button> : null}
      </section>;
    })}
  </section>;
}
