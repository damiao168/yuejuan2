import type { ScoringTrialSample, ScoringTrialSampleRole } from "../../../api/scoringTrials";
import type { ScoringRunItem } from "../../../api/review";

export const TRIAL_MIN_SAMPLE_SIZE = 5;
export const TRIAL_MAX_SAMPLE_SIZE = 10;
export const trialRoles: ReadonlyArray<{ value: ScoringTrialSampleRole; label: string }> = [
  { value: "representative", label: "代表答案" },
  { value: "boundary", label: "边界答案" },
  { value: "incomplete_evidence", label: "证据不全" }
];

/** 界面先检查同题、分值和样本覆盖；服务端会重新校验，浏览器记录不授予扩批权限。 */
export function validateTrialDraft(
  questionId: string,
  selected: readonly string[],
  items: readonly Pick<ScoringRunItem, "answer_segment_id" | "question_id" | "state">[],
  baselines: Record<string, number | undefined>,
  roles: Record<string, ScoringTrialSampleRole[] | undefined>,
  maxScore?: number
): string | null {
  if (selected.length < TRIAL_MIN_SAMPLE_SIZE || selected.length > TRIAL_MAX_SAMPLE_SIZE || new Set(selected).size !== selected.length) return "请选择 5–10 份不同的同题样本。";
  if (selected.some(id => !items.some(item => item.answer_segment_id === id && item.question_id === questionId && item.state === "review"))) return "样本不属于本题当前待复核范围，请刷新。";
  if (selected.some(id => typeof baselines[id] !== "number" || !Number.isFinite(baselines[id]) || baselines[id]! < 0 || (maxScore !== undefined && baselines[id]! > maxScore))) return "请为每份样本填写满分范围内的独立人工分数。";
  if (selected.some(id => !roles[id]?.length) || trialRoles.some(role => !selected.some(id => roles[id]?.includes(role.value)))) return "请标注样本类型，并覆盖代表答案、边界答案和证据不全三类。";
  return null;
}

/** 只认本次试改逐样本结果；正式成绩、全场批次计数和未知状态均不能充当试改终态。 */
export function trialSamplesFinished(samples: readonly ScoringTrialSample[]): boolean {
  return samples.length >= TRIAL_MIN_SAMPLE_SIZE && samples.every(sample =>
    sample.result && ["succeeded", "failed", "conflict"].includes(sample.result.status));
}

export function trialScoreDifference(sample: ScoringTrialSample): number | null {
  const result = sample.result;
  return result?.status === "succeeded" && typeof result.suggested_score === "number" && Number.isFinite(result.suggested_score)
    ? result.suggested_score - sample.human_score : null;
}
