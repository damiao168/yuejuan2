// 冻结材料映射和提交预检不读写编辑器；调用方负责在异步边界校验本次会话和请求代次。
import type { ReviewTaskContext } from '@edugrade/sdk';
import type { DesktopApiClient } from '../../api/client';
import { getReviewTask, getReviewTaskContext } from '../../api/review';
import { getUserErrorMessage } from '../../api/userError';
import { reviewTaskState } from '../../statusLabels';
import type { AiGrade, AuthUser, OfflineTaskPackage, Question, ReviewTask } from '../../types';
import type { OfflineTaskPackageWithAnswer } from './types';

export function selectedOfflineReviewTask(tasks: readonly ReviewTask[], selectedTaskId: string, pkg: OfflineTaskPackage | null): ReviewTask | undefined {
  // 当前列表优先；离线恢复后列表尚未读取时，仅回退到同一已选任务的保存材料。
  return tasks.find((item) => item.id === selectedTaskId) ?? (pkg?.task.id === selectedTaskId ? pkg.task : undefined);
}

export function packageAnswerText(pkg: OfflineTaskPackageWithAnswer) {
  // 旧加密包只有 ocrText；允许继续审读原文，但冻结细则来源仍由独立预检校验。
  return pkg.answerText?.trim() || pkg.ocrText?.trim() || "";
}

export function assertTaskContext(context: ReviewTaskContext, task: ReviewTask) {
  if (context.task.id !== task.id || context.question.id !== task.question_id
    || context.task.answer_segment_id !== task.answer_segment_id
    || (context.answer_artifact && context.answer_artifact.answer_segment_id !== task.answer_segment_id)) {
    throw new Error("任务冻结上下文与所选任务不一致，请重新下载。");
  }
}

export function taskContextAiGrades(context: ReviewTaskContext, tenantId: string): AiGrade[] {
  const opinion = context.ai_second_opinion;
  // 可采用的建议分仅来自任务上下文；人工主导策略没有授权预填时不能从其他证据字段补出分数。
  if (!opinion?.available || !opinion.score_prefill_allowed
    || (context.question_snapshot?.risk_tier === "R3" && context.question_snapshot.scoring_policy_snapshot?.mode === "HUMAN_PRIMARY")) return [];
  const metadata = opinion.metadata;
  const score = metadata.suggested_score;
  const maxScore = metadata.max_score ?? context.frozen_rubric.max_score;
  if (typeof score !== "number" || !Number.isFinite(score) || typeof maxScore !== "number" || !Number.isFinite(maxScore)
    || score < 0 || maxScore < 0 || score > maxScore) return [];
  return [{
    id: typeof metadata.id === "string" ? metadata.id : `task-ai-${context.task.id}`,
    tenant_id: tenantId, answer_segment_id: context.task.answer_segment_id, question_id: context.question.id,
    question_no: context.question.question_no, suggested_score: score, max_score: maxScore,
    confidence: typeof metadata.confidence === "number" ? metadata.confidence : 0,
    matched_points: [], missing_points: [], risk_flags: [], mock: metadata.mock === true,
    student_feedback: typeof metadata.student_feedback === "string" ? metadata.student_feedback : undefined,
    created_at: typeof metadata.created_at === "string" ? metadata.created_at : ""
  }];
}

export async function buildTaskPackage(client: DesktopApiClient, task: ReviewTask, tenantID: string): Promise<OfflineTaskPackageWithAnswer> {
  let context: Awaited<ReturnType<typeof getReviewTaskContext>>["context"] | undefined;
  const warnings: string[] = [];
  try {
    ({ context } = await getReviewTaskContext(client, task.id));
  } catch (error) {
    warnings.push(`任务材料读取失败：${formatError(error)}`);
  }
  if (context) assertTaskContext(context, task);

  // 只映射任务上下文里的冻结材料；考试题库、提交片段和文件管理接口都可能超出阅卷权限，不能回退读取。
  const frozenRubric = context?.frozen_rubric;
  const question: Question | undefined = context ? {
    ...context.question, tenant_id: tenantID, knowledge_points: context.question.knowledge_points ?? [],
    rubric: frozenRubric ? {
      ...frozenRubric,
      points: Array.isArray(frozenRubric.points) ? frozenRubric.points.map((point) => ({
        id: typeof point.id === "string" ? point.id : "", description: typeof point.description === "string" ? point.description : "", score: Number(point.score ?? 0), required: point.required === true
      })) : []
    } : undefined
  } : undefined;
  const artifact = context?.answer_artifact;
  const answerText = artifact?.raw_answer?.trim() || artifact?.ocr_text?.trim() || "";
  const answerImageUrl = artifact?.segment_image_url?.trim() || undefined;
  if (!context) warnings.push("未能获取任务冻结题目与评分细则。");
  else if (!question) warnings.push("未能获取任务冻结题目与评分细则。");
  else if (question.rubric?.status === "missing") warnings.push("本任务冻结评分细则缺失；未回退到当前题库。");
  if (!answerText && !answerImageUrl) warnings.push("未获取可核对的答卷文字或图片，请重新读取任务材料或联系管理员。");
  return {
    task: context?.task ?? task,
    ocrText: answerText,
    answerText,
    answerImageUrl,
    question,
    aiGrades: context ? taskContextAiGrades(context, tenantID) : [],
    aiCandidates: (context?.ai_candidates ?? []).filter((candidate) => candidate.answer_segment_id === task.answer_segment_id),
    aiSecondOpinion: context?.ai_second_opinion,
    warnings,
    downloadedAt: new Date().toISOString(),
    rubricVersion: question?.rubric?.version,
    ...(context ? { assessmentSource: "task_context" as const } : {})
  };
}

export async function detectConflict(client: DesktopApiClient, pkg: OfflineTaskPackage, user: AuthUser | null, assertCurrent: () => void) {
  // 预检提供可读冲突原因；提交仍携带下载时的 revision，由服务端处理预检后的再次变更。
  if (!Number.isInteger(pkg.task.revision) || pkg.task.revision <= 0) {
    return "本地任务包缺少版本，请重新下载任务包后核对草稿。";
  }
  const latest = await getReviewTask(client, pkg.task.id);
  assertCurrent();
  if (latest.task.revision !== pkg.task.revision) {
    return "服务端任务版本已变化，请重新下载任务包后核对草稿。";
  }
  const status = latest.task.status;
  if (["submitted", "completed", "cancelled", "withdrawn", "revoked"].includes(status)) {
    const state = reviewTaskState(status);
    const nextStep = ["submitted", "completed"].includes(status)
      ? "请刷新任务列表核对服务端评分结果；如结果不符，请联系学校管理员。"
      : "请刷新任务列表，并联系学校管理员核对任务安排。";
    return `服务端任务${state.label}，本地草稿不能提交。${state.detail}。本地草稿仍保留。${nextStep}`;
  }
  if (latest.task.assigned_to && user?.id && latest.task.assigned_to !== user.id) {
    return "服务端任务已不再分配给当前用户。";
  }
  const questionResult = await getReviewTaskContext(client, pkg.task.id);
  assertCurrent();
  assertTaskContext(questionResult.context, pkg.task);
  if (pkg.assessmentSource !== "task_context") return "旧任务包未记录冻结细则来源，请重新下载任务包后核对草稿。";
  const latestRubricVersion = questionResult.context.frozen_rubric.version;
  if (pkg.rubricVersion && latestRubricVersion && latestRubricVersion !== pkg.rubricVersion) {
    return `评分标准版本变化：本地 ${pkg.rubricVersion}，服务端 ${latestRubricVersion}。`;
  }
  return null;
}

export function packageAssessmentIdentity(pkg: OfflineTaskPackageWithAnswer) {
  return JSON.stringify({ task: { id: pkg.task.id, revision: pkg.task.revision, question_id: pkg.task.question_id, answer_segment_id: pkg.task.answer_segment_id, assigned_to: pkg.task.assigned_to }, question: pkg.question, answerText: pkg.answerText, answerImageUrl: pkg.answerImageUrl, source: pkg.assessmentSource });
}

export function latestGrade(grades: AiGrade[]) {
  return grades.filter((grade) => !grade.mock).sort((a, b) => new Date(b.created_at).getTime() - new Date(a.created_at).getTime())[0] ?? grades[0];
}

export function formatError(error: unknown) {
  return getUserErrorMessage(error);
}
