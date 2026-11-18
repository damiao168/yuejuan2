import type { Question } from "../../../api/papers";
import type {
  AiGrade,
  AutomationResult,
  EvidenceJob,
  ReviewTask,
  ScoringRunItem,
  ReviewTaskContext
} from "../../../api/review";
import type { AnswerSegment, OcrResult, OcrTask, SubmissionPage } from "../../../api/submissions";

export type TaskFilter = "all" | "active" | "pending" | "assigned" | "in_progress" | "returned" | "submitted" | "reviewed";
export type ViewerMode = "segment" | "group" | "original" | "ocr";
export type DraftSaveStatus = "idle" | "pending" | "saving" | "saved" | "offline" | "conflict" | "error" | "readonly";
export type ScoringResultType = "all" | "choice" | "fill" | "subjective";
export type ScoringResultState = "all" | "confirmed" | "review" | "failed" | "processing";

export interface WorkbenchContext {
  task: ReviewTask;
  reviewContext: ReviewTaskContext;
  segment?: AnswerSegment;
  question?: Question;
  pages: SubmissionPage[];
  page?: SubmissionPage;
  ocrTasks: OcrTask[];
  ocrResults: OcrResult[];
  aiGrades: AiGrade[];
  automationResult?: AutomationResult;
  evidenceJob?: EvidenceJob;
  warnings: string[];
  ocrText: string;
  segmentImageUrl: string;
  answerScope?: "question" | "grading_group";
  gradingGroupImageUrl?: string;
  gradingGroupId?: string;
  originalImageUrl?: string;
}

export interface PreviewState {
  url: string;
  contentType: string;
  filename?: string;
  contentRevision?: string;
}

export interface ScoringImagePreview extends PreviewState {
  title: string;
  item?: ScoringRunItem;
}

export interface PrefetchedTaskBundle {
  task: ReviewTask;
  context: WorkbenchContext;
}

export interface ScoreDraft {
  score: number | null;
  comments: string;
  privateNote: string;
  studentFeedback: string;
  reason: string;
  disputeReason: string;
  rubricSelections: Record<string, number>;
  answerText: string;
  // 缺失或旧版 null 表示未知；明确的采纳和手工决定都必须绑定当时的任务及模型建议。
  aiAdoption?: DraftAIAdoption | null;
}

export interface DraftAIAdoption {
  taskId: string;
  aiGradeId: string;
  suggestedScore: number;
  accepted: boolean;
}

export interface ScoreDraftChange {
  score: number | null;
  rubricSelections: Record<string, number>;
  aiAdoption?: DraftAIAdoption | null;
}

export interface ReviewerProgress {
  id: string;
  name: string;
  total: number;
  completed: number;
  active: number;
  percent: number;
}

export interface DraftFallbackSnapshot {
  draft: ScoreDraft;
  viewer: {
    mode: ViewerMode;
    scale: number;
    rotation: number;
    offset: { x: number; y: number };
    fit: boolean;
  };
}
