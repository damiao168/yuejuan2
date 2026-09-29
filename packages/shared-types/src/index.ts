export type GradingMode =
  | "auto"
  | "ai_assisted_human_final"
  | "single_mark_with_sampling"
  | "double_mark_arbitration"
  | "blind_mark_arbitration";

export type WorkflowStatus =
  | "CREATED"
  | "UPLOADED"
  | "PREPROCESSED"
  | "OCR_DONE"
  | "SEGMENTED"
  | "RUBRIC_READY"
  | "AI_GRADED"
  | "VERIFIED"
  | "HUMAN_REVIEWING"
  | "FINALIZED"
  | "PUBLISHED"
  | "APPEALING"
  | "ARCHIVED";

export interface AiGradeEvidence {
  rubricPointId: string;
  score: number;
  evidence: string;
  bbox?: [number, number, number, number];
}

export interface AiGradeResult {
  answerId: string;
  questionId: string;
  // 模型建议值；是否采纳及最终发布由评分工作流决定，类型本身不校验分数范围。
  suggestedScore: number;
  maxScore: number;
  confidence: number;
  modelVersion: string;
  rubricVersion: string;
  matchedPoints: AiGradeEvidence[];
  missingPoints: Array<{
    rubricPointId: string;
    lostScore: number;
    reason: string;
  }>;
  riskFlags: string[];
  needsHumanReview: boolean;
}
