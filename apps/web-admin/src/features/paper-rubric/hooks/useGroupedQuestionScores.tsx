import { useEffect, useRef, useState } from "react";
import { App } from "antd";
import type { Exam } from "../../../api/exams";
import { listQuestions, listScoringRules, updateQuestion, type Question } from "../../../api/papers";
import { getUserErrorMessage } from "../../../api/client";
import { groupedQuestionScores, scoreGroupSnapshot, uniformScorePreview, type QuestionScoreGroup } from "../groupedQuestionScores";

interface ScoreOptions {
  selectedExam?: Exam;
  questions: Question[];
  canManage: boolean;
  hasUnsavedWork: boolean;
  isMutationBlocked: () => boolean;
  runMutation: (action: () => Promise<unknown>) => Promise<void>;
  runRefresh?: (action: () => Promise<unknown>) => Promise<void>;
  onRefreshPending?: (pending: boolean) => void;
  loadConfig: (examId: string, options?: { silent?: boolean; propagateError?: boolean }) => Promise<void>;
  onChanged?: () => void;
}

async function groupHasPublishedRules(questions: Question[]) {
  const entries = await Promise.all(questions.map(async question => ({ question,
    rules: (await listScoringRules(question.id)).scoring_rules })));
  return entries.find(item => item.rules.some(rule => rule.status === "published"));
}

async function validateScoreGroup(examId: string, group: QuestionScoreGroup, snapshot: string, changed: Question[], canApply: () => boolean) {
  const latest = (await listQuestions(examId)).questions;
  if (!canApply()) return false;
  const latestGroup = groupedQuestionScores(latest).find(item => item.type === group.type);
  if (!latestGroup || scoreGroupSnapshot(latestGroup.questions) !== snapshot) throw new Error("题目或分值已变化，请重新读取后核对统一设置");
  const published = await groupHasPublishedRules(changed);
  if (!canApply()) return false;
  if (published) throw new Error(`第 ${published.question.question_no} 题已有发布的评分规则，请先逐题核对规则与分值，不能批量修改`);
  return true;
}

function scoreSaveResult(saved: number, total: number, failure: unknown, readFailure: unknown) {
  const remaining = total - saved;
  const detail = failure ? getUserErrorMessage(failure, "统一分值暂未完成") : "";
  const reread = readFailure ? "页面回读失败，请刷新核对已保存的分值；无需重复保存已成功题目。" : "";
  const savedDescription = remaining === 0 ? `已保存 ${saved} 题，未保存 0 题。`
    : `已确认保存 ${saved} 题，剩余 ${remaining} 题保存状态请核对读取结果。`;
  return { text: `${savedDescription}${detail}${reread}`, success: remaining === 0 && !failure && !readFailure };
}

async function saveGroupQuestions(questions: Question[], score: number, canApply: () => boolean,
  progress: { saved: number }) {
  // 接口逐题提交，不提供全组事务；失败即停止，成功计数留给后续回读和恢复提示。
  for (const question of questions) {
    if (!canApply()) break;
    await updateQuestion(question.id, { score });
    progress.saved++;
  }
}

export function useGroupedQuestionScores(options: ScoreOptions) {
  const { message, modal } = App.useApp();
  const current = useRef(options);
  current.current = options;
  const scope = useRef({ examId: options.selectedExam?.id, version: 0 });
  if (scope.current.examId !== options.selectedExam?.id) scope.current = { examId: options.selectedExam?.id, version: scope.current.version + 1 };
  const active = useRef(true);
  useEffect(() => { active.current = true; return () => { active.current = false; }; }, []);
  const [targets, setTargets] = useState<Record<string, number | null>>({});
  const latestTargets = useRef(targets);
  latestTargets.current = targets;
  const [result, setResult] = useState<{ examId: string; version: number; text: string; success: boolean; refreshPending: boolean } | null>(null);
  const pendingRead = useRef<{ examId: string; version: number } | null>(null);
  const groups = groupedQuestionScores(options.questions);
  const editable = options.canManage && ["draft", "configured"].includes(options.selectedExam?.status ?? "");
  const targetKey = (type: string) => `${options.selectedExam?.id ?? ""}:${type}`;
  const scoreForGroup = (group: QuestionScoreGroup) => Object.prototype.hasOwnProperty.call(targets, targetKey(group.type)) ? targets[targetKey(group.type)] : group.uniformScore;

  async function refreshGroupResults(examId: string, isCurrent: () => boolean) {
    if (!isCurrent()) return;
    let readFailure: unknown;
    try { await current.current.loadConfig(examId, { silent: true, propagateError: true }); }
    catch (error) { readFailure = error; }
    if (isCurrent()) current.current.onChanged?.();
    return readFailure;
  }

  function showGroupSaveResult(examId: string, version: number, label: string,
    progress: { saved: number }, total: number, failure: unknown, readFailure: unknown) {
    const { text, success } = scoreSaveResult(progress.saved, total, failure, readFailure);
    if (readFailure) {
      // 未读取到保存结果时，旧单题表单不能继续写回刚刚更新的分值。
      pendingRead.current = { examId, version };
      current.current.onRefreshPending?.(true);
    }
    setResult({ examId, version, text, success, refreshPending: Boolean(readFailure) });
    if (success) message.success(`${label}分值已保存；如有评分规则草稿，请逐题核对后发布。`);
    else message.warning(text);
  }

  async function saveReviewedGroups(examId: string, version: number,
    plans: { group: QuestionScoreGroup; snapshot: string; changed: Question[]; score: number }[], isCurrent: () => boolean, canApply: () => boolean) {
    const progress = { saved: 0 };
    let failure: unknown;
    let readFailure: unknown;
    try {
      // 多题型集中保存前先核对全部题组；后面某组已有发布规则时，前面也不能先写入。
      for (const plan of plans) {
        if (!await validateScoreGroup(examId, plan.group, plan.snapshot, plan.changed, canApply)) return;
      }
      for (const plan of plans) await saveGroupQuestions(plan.changed, plan.score, canApply, progress);
    } catch (error) { failure = error; }
    finally {
      // 回读属于发起保存的考试及代次；切换后不能刷新新考试或报告旧批次结果。
      readFailure = await refreshGroupResults(examId, isCurrent);
    }
    if (!isCurrent()) return;
    showGroupSaveResult(examId, version, plans.map(plan => plan.group.label).join("、"), progress,
      plans.reduce((total, plan) => total + plan.changed.length, 0), failure, readFailure);
  }

  function applyScoreGroups(selectedGroups: QuestionScoreGroup[]) {
    const examId = options.selectedExam?.id;
    const plans = selectedGroups.flatMap(group => {
      const score = scoreForGroup(group);
      const preview = uniformScorePreview(group, score, options.questions);
      return score !== null && preview?.changed.length ? [{ group, score, preview, changed: preview.changed, snapshot: scoreGroupSnapshot(group.questions) }] : [];
    });
    if (!examId || !editable || pendingRead.current?.examId === examId && pendingRead.current.version === scope.current.version
      || options.hasUnsavedWork || options.isMutationBlocked() || !plans.length
      || selectedGroups.some(group => Object.prototype.hasOwnProperty.call(targets, targetKey(group.type)) && !uniformScorePreview(group, scoreForGroup(group), options.questions))) return;
    const version = scope.current.version;
    const isCurrent = () => active.current && scope.current.version === version && current.current.selectedExam?.id === examId;
    const canApply = () => isCurrent() && current.current.canManage && !current.current.hasUnsavedWork
      && plans.every(plan => latestTargets.current[`${examId}:${plan.group.type}`] === plan.score)
      && ["draft", "configured"].includes(current.current.selectedExam?.status ?? "");
    modal.confirm({
      title: "保存分值修改？",
      content: <div>{plans.map(({ group, score, preview }) => <p key={group.type}>{group.label}：修改 {preview.changed.length} 题为每题 {score} 分。
        {group.lockedCount > 0 ? ` ${group.lockedCount} 题已锁定，保留原分值。` : ""}</p>)}
        <p>修改后全卷题目合计 {Number((options.questions.reduce((total, question) => total + question.score, 0) + plans.reduce((total, plan) => total + plan.preview.difference, 0)).toFixed(4))} 分，考试满分 {options.selectedExam?.total_score} 分。</p>
        <p>只保存本次修改的分值，解答题仍逐题设置；已有评分规则草稿需核对后发布。</p>
      </div>,
      okText: "确认统一并保存", cancelText: "返回核对",
      onOk: async () => {
        // 确认框可能跨越换科或新编辑；真正提交前仍检查当前考试和页面的同步保存锁。
        if (!canApply() || current.current.isMutationBlocked()) return;
        await current.current.runMutation(() => saveReviewedGroups(examId, version, plans, isCurrent, canApply));
      }
    });
  }

  async function rereadSavedResults() {
    const pending = pendingRead.current;
    if (!pending || !active.current || current.current.selectedExam?.id !== pending.examId || scope.current.version !== pending.version) return;
    const isCurrent = () => active.current && current.current.selectedExam?.id === pending.examId && scope.current.version === pending.version;
    const refresh = async () => {
      try {
        await current.current.loadConfig(pending.examId, { silent: true, propagateError: true });
        if (!isCurrent()) return;
        pendingRead.current = null;
        current.current.onRefreshPending?.(false);
        current.current.onChanged?.();
        setResult({ examId: pending.examId, version: pending.version, success: true, refreshPending: false,
          text: "已重新读取分值结果，请核对各题当前分值后继续编辑；已有评分规则草稿需逐题核对后发布。" });
      } catch (error) { if (isCurrent()) message.warning(getUserErrorMessage(error, "暂时无法读取保存结果，请稍后重试")); }
    };
    await (current.current.runRefresh ?? current.current.runMutation)(refresh);
  }

  const draftedGroups = groups.filter(group => Object.prototype.hasOwnProperty.call(targets, targetKey(group.type)));
  const dirty = draftedGroups.some(group => { const preview = uniformScorePreview(group, scoreForGroup(group), options.questions); return !preview || preview.changed.length > 0; });
  const hasInvalidDraft = draftedGroups.some(group => !uniformScorePreview(group, scoreForGroup(group), options.questions));
  return { groups, editable, scoreForGroup, dirty, hasInvalidDraft,
    setScore: (type: string, score: number | null) => setTargets((drafts) => ({ ...drafts, [targetKey(type)]: score })),
    discardChanges: () => setTargets(drafts => Object.fromEntries(Object.entries(drafts).filter(([key]) => !key.startsWith(`${options.selectedExam?.id ?? ""}:`)))),
    applyUniformScore: (group: QuestionScoreGroup) => applyScoreGroups([group]),
    applyAllScores: () => applyScoreGroups(draftedGroups), rereadSavedResults,
    result: result && result.examId === options.selectedExam?.id && result.version === scope.current.version ? result : null };
}
