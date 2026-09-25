import { getQuestionAssessmentSnapshot } from "../../../api/assessment";
import type { ScoringQuestionSummary } from "../../../api/review";

export type ScoringLane = "rule" | "ai" | "human";

export interface PlannedQuestion {
  questionId: string;
  questionNo: string;
  questionType: string;
  count: number;
  mode: string;
  riskTier: string;
  lane: ScoringLane;
  note: string;
}

export interface ScoringPlan {
  examId: string;
  questions: PlannedQuestion[];
  counts: Record<ScoringLane, number>;
  snapshotFailures: number;
}

const aiQuestionTypes = new Set(["short_answer", "calculation", "essay", "discussion"]);
const aiModes = new Set(["AI_ASSIST", "AI_FAST_CONFIRM"]);

export const scoringModeLabels: Record<string, string> = {
  RULE_AUTO: "规则自动",
  AI_ASSIST: "AI 建议 + 教师确认",
  AI_FAST_CONFIRM: "AI 建议 + 快速确认",
  HUMAN_PRIMARY: "人工主评",
  DUAL_HUMAN: "双评策略",
  MANUAL_ONLY: "纯人工",
  SNAPSHOT_UNAVAILABLE: "策略不可读"
};

export const scoringRiskLabels: Record<string, string> = { R1: "低风险", R2: "中风险", R3: "高风险" };

export function planQuestion(question: ScoringQuestionSummary, mode: string, riskTier: string): PlannedQuestion {
  const ai = aiQuestionTypes.has(question.question_type) && aiModes.has(mode);
  const rule = !ai && mode === "RULE_AUTO" && ["single_choice", "multiple_choice", "true_false", "fill_blank", "numeric"].includes(question.question_type);
  const lane: ScoringLane = ai ? "ai" : rule ? "rule" : "human";
  const note = ai ? "AI 仅生成建议，须由教师确认" : rule ? "规则判分；异常转人工" : mode === "SNAPSHOT_UNAVAILABLE" ? "策略快照暂不可读，按人工处理" : mode === "DUAL_HUMAN" ? "同校两名阅卷员互盲双评，分数不一致即仲裁" : "人工评分或复核";
  return { questionId: question.question_id, questionNo: question.question_no, questionType: question.question_type, count: question.total, mode, riskTier, lane, note };
}

export async function loadScoringPlan(examId: string, questions: ScoringQuestionSummary[]): Promise<ScoringPlan> {
  const settled = await Promise.allSettled(questions.map((question) => getQuestionAssessmentSnapshot(examId, question.question_id)));
  const planned = questions.map((question, index) => {
    const result = settled[index];
    if (result.status !== "fulfilled") return planQuestion(question, "SNAPSHOT_UNAVAILABLE", "-");
    const snapshot = result.value.assessment_snapshot;
    return planQuestion(question, snapshot.scoring_policy_snapshot.mode, snapshot.risk_tier);
  });
  return {
    examId,
    questions: planned,
    counts: {
      rule: planned.filter((item) => item.lane === "rule").reduce((sum, item) => sum + item.count, 0),
      ai: planned.filter((item) => item.lane === "ai").reduce((sum, item) => sum + item.count, 0),
      human: planned.filter((item) => item.lane === "human").reduce((sum, item) => sum + item.count, 0)
    },
    snapshotFailures: settled.filter((item) => item.status === "rejected").length
  };
}
