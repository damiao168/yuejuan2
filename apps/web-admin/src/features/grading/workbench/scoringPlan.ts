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

function scoringNote(lane: ScoringLane, mode: string): string {
  if (lane === "ai") return "自动识别学生作答并生成建议分，由教师确认最终分数";
  if (lane === "rule") return "规则判分；异常转人工";
  if (mode === "SNAPSHOT_UNAVAILABLE") return "策略快照暂不可读，按人工处理";
  if (mode === "DUAL_HUMAN") return "同校两名阅卷员互盲双评，分数不一致即仲裁";
  return "人工评分或复核";
}

export function planQuestion(question: ScoringQuestionSummary, mode: string, riskTier: string): PlannedQuestion {
  const ai = aiQuestionTypes.has(question.question_type) && aiModes.has(mode);
  const rule = !ai && mode === "RULE_AUTO" && ["single_choice", "multiple_choice", "true_false", "fill_blank", "numeric"].includes(question.question_type);
  // 分流只接受题型和冻结模式同时匹配；不能把不支持的组合当作自动评分授权。
  let lane: ScoringLane = "human";
  if (ai) lane = "ai";
  else if (rule) lane = "rule";
  const note = scoringNote(lane, mode);
  return { questionId: question.question_id, questionNo: question.question_no, questionType: question.question_type, count: question.total, mode, riskTier, lane, note };
}

export async function loadScoringPlan(examId: string, questions: ScoringQuestionSummary[]): Promise<ScoringPlan> {
  const settled = await Promise.allSettled(questions.map((question) => getQuestionAssessmentSnapshot(examId, question.question_id)));
  const planned = questions.map((question, index) => {
    const result = settled[index];
    // 策略快照读取失败时保守转人工，不能依据当前可编辑配置猜测考试冻结时的授权策略。
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
