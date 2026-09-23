import { executeBusinessCommand } from "./businessCommand";
import { EduGradeApi } from "@edugrade/sdk";
import type {
  ReviewTask as GeneratedReviewTask,
  ReviewTaskContext as GeneratedReviewTaskContext,
  SubjectiveAIGrade
} from "@edugrade/sdk";
import { apiClient } from "./client";
import type { Question } from "./papers";

export type ReviewTask = GeneratedReviewTask;
// The API deliberately returns null when the reviewer has not saved a draft
// yet. Keep that runtime fact explicit even though older generated contracts
// modelled the field as required.
export type ReviewTaskContext = Omit<GeneratedReviewTaskContext, "draft"> & {
  draft: GeneratedReviewTaskContext["draft"] | null;
};

const generatedApi = new EduGradeApi(apiClient);

export interface RubricSelection {
  point_id: string;
  score: number;
}

export interface SubmitHumanGradePayload {
  expected_revision: number;
  score: number;
  rubric_selections: RubricSelection[];
  comments: string;
  private_note: string;
  student_feedback: string;
  reason: string;
}

export interface HumanGrade {
  id: string;
  tenant_id: string;
  review_task_id: string;
  answer_segment_id: string;
  reviewer_id: string;
  score: number;
  max_score: number;
  rubric_selections: RubricSelection[];
  comments?: string;
  private_note?: string;
  student_feedback?: string;
  reason?: string;
  grade_round: string;
  created_at: string;
}

export interface ReviewDraft {
  id: string;
  review_task_id: string;
  reviewer_id: string;
  score?: number;
  rubric_selections: RubricSelection[];
  comments: string;
  private_note: string;
  student_feedback: string;
  viewer_state: Record<string, unknown>;
  revision: number;
  client_updated_at?: string;
  updated_at: string;
}

export interface SaveReviewDraftPayload {
  score: number | null;
  rubric_selections: RubricSelection[];
  comments: string;
  private_note: string;
  student_feedback: string;
  viewer_state: Record<string, unknown>;
  expected_revision: number;
  client_updated_at: string;
}

export interface ReviewWorkspace {
  task: ReviewTask;
  context: {
    exam_id: string;
    submission_id: string;
    answer_segment_id: string;
    anonymous_code: string;
    question: Question;
    rubric?: Question["rubric"];
    raw_answer: string;
    ocr_text: string;
    ai_suggestion: AiGrade | null;
    automation_result?: AutomationResult;
  };
  segment_image_url: string;
  original_image_url?: string;
  segment_status: string;
  segment_confidence?: number;
}

export interface AutomationResult {
  source?: string;
  recognized_answer?: string;
  confidence?: number;
  decision?: string;
  standard_answer?: unknown;
  rule_type?: string;
  score?: number;
  max_score?: number;
  grade_source?: string;
}

export interface FinalGrade {
  id: string;
  tenant_id?: string;
  exam_id?: string;
  question_id?: string;
  question_no?: string;
  answer_segment_id?: string;
  submission_id?: string;
  anonymous_code?: string;
  score: number;
  max_score: number;
  source: string;
  double_mark_session_id?: string;
  arbitration_task_id?: string;
  resolution_strategy?: string;
  locked: boolean;
  created_by?: string;
  created_at?: string;
  updated_at?: string;
}

export interface SubmitHumanGradeResult {
  task: ReviewTask;
  human_grade: HumanGrade;
  final_grade?: FinalGrade;
  double_mark_session?: unknown;
  arbitration_task?: unknown;
}

export interface SegmentAnswerPayload {
  answer_text: string;
  answer_payload: Record<string, unknown>;
  source: "manual_entry" | "ocr_text" | "imported_answer";
  confidence?: number;
}

export interface SegmentAnswer {
  id: string;
  tenant_id: string;
  answer_segment_id: string;
  answer_text: string;
  answer_payload: Record<string, unknown>;
  source: string;
  confidence?: number;
  recorded_by: string;
  created_at: string;
}

export interface PointResult {
  code: string;
  label: string;
  score: number;
}

export interface GradeEvidence {
  type: string;
  answer_segment_id?: string;
  answer_text?: string;
  standard_answer?: string;
  rule?: string;
  bbox?: number[];
}

export interface AiGrade extends Pick<SubjectiveAIGrade, "math_artifact_id" | "math_artifact_version" | "math_correction_revision" | "math_scoring_version"> {
  id: string;
  tenant_id: string;
  answer_segment_id: string;
  question_id: string;
  question_no: string;
  question_type: string;
  answer_version?: string;
  grader_type: string;
  rule_version?: string;
  model_version?: string;
  prompt_version?: string;
  rubric_version?: string;
  delivery_mode?: "teacher_review" | "teacher_suggestion" | "shadow_only";
  suggested_score: number;
  max_score: number;
  confidence: number;
  matched_points: PointResult[];
  missing_points: PointResult[];
  evidence: GradeEvidence[];
  risk_flags: string[];
  needs_human_review: boolean;
  auto_pass?: boolean;
  mock: boolean;
  status?: string;
  failure_reason?: string;
  student_feedback?: string;
  teacher_note?: string;
  raw_output?: Record<string, unknown>;
  created_by: string;
  created_at: string;
}

export interface EvidenceIssue {
  code: string;
  message: string;
}

export interface EvidenceResult {
  passed: boolean;
  failed: EvidenceIssue[];
  warnings: EvidenceIssue[];
  corrected_flags: string[];
  needs_human_review: boolean;
}

export interface EvidenceJob {
  id: string;
  tenant_id: string;
  job_type: string;
  target_type: string;
  target_id: string;
  status: string;
  result: EvidenceResult;
  needs_human_review: boolean;
  created_by: string;
  created_at: string;
}

export interface ReviewTaskFilter {
  status?: string;
  assigned_to?: string;
  exam_id?: string;
  limit?: number;
  cursor?: string;
}

export type ScoringRun = import("@edugrade/sdk").ScoringRun;

export type ScoringQuestionSummary = import("@edugrade/sdk").ScoringQuestionSummary;
export type ScoringSummary = import("@edugrade/sdk").ScoringSummary;
export type ScoringReadinessCheck = import("@edugrade/sdk").ScoringReadinessCheck;
export type ScoringReadiness = import("@edugrade/sdk").ScoringReadiness;

export interface ScoringRunItem {
  answer_segment_id: string;
  submission_id: string;
  submission_page_id: string;
  page_no: number;
  page_file_asset_id: string;
  question_id: string;
  question_no: string;
  question_type: string;
  anonymous_code: string;
  normalized_bbox: {
    x: number;
    y: number;
    width: number;
    height: number;
  };
  state: string;
  recognition_source?: string;
  recognized_answer?: string;
  recognition_decision?: string;
  recognition_confidence?: number;
  standard_answer?: unknown;
  rule_type?: string;
  score?: number;
  max_score?: number;
  grade_source?: string;
  omr_run_id?: string;
  runtime_task_id?: string;
  runtime_status?: string;
  review_task_id?: string;
  review_status?: string;
  reason_code?: string;
  error_code?: string;
}

export interface ScoringRunDetail {
  scoring_run: ScoringRun;
  items: ScoringRunItem[];
}

export interface ExamAutomationResults {
  items: ScoringRunItem[];
}

export type ArbitrationContext = import("@edugrade/sdk").ReviewCommandReviewContext;
export type ArbitrationTask = import("@edugrade/sdk").ReviewCommandArbitrationTask;
export type CreateArbitrationTaskPayload = import("@edugrade/sdk").ReviewCommandCreateArbitrationTaskInput;

export interface ArbitrationTaskFilter {
  status?: string;
  assigned_to?: string;
  exam_id?: string;
  limit?: number;
  cursor?: string;
}

export type AssignArbitrationPayload = import("@edugrade/sdk").ReviewCommandAssignArbitrationInput;

export type SubmitArbitrationPayload = import("@edugrade/sdk").ReviewCommandSubmitArbitrationInput;
export type SubmitArbitrationResult = import("@edugrade/sdk").ReviewCommandArbitrationSubmitResult;

function queryString(filter: ReviewTaskFilter) {
  const params = new URLSearchParams();
  if (filter.status) {
    params.set("status", filter.status);
  }
  if (filter.assigned_to) {
    params.set("assigned_to", filter.assigned_to);
  }
  if (filter.exam_id) {
    params.set("exam_id", filter.exam_id);
  }
  if (filter.limit) {
    params.set("limit", String(filter.limit));
  }
  if (filter.cursor) {
    params.set("cursor", filter.cursor);
  }
  const query = params.toString();
  return query ? `?${query}` : "";
}

export async function listReviewTasks(filter: ReviewTaskFilter = {}) {
  return generatedApi.listReviewTasks({ query: filter });
}

export async function getReviewTask(id: string) {
  return apiClient.request<{ task: ReviewTask }>(`/api/v1/review-tasks/${encodeURIComponent(id)}`);
}

export async function getReviewTaskContext(id: string, signal?: AbortSignal) {
  const result = await generatedApi.getReviewTaskContext({ path: { taskId: id }, signal });
  return { context: result.context as ReviewTaskContext };
}

export async function getReviewWorkspace(id: string) {
  return apiClient.request<{ workspace: ReviewWorkspace }>(`/api/v1/review-tasks/${encodeURIComponent(id)}/workspace`);
}

export async function assignReviewTask(id: string, assignedTo: string, expectedRevision: number) {
  return apiClient.request<{ task: ReviewTask }>(`/api/v1/review-tasks/${encodeURIComponent(id)}/assign`, {
    method: "POST",
    body: JSON.stringify({ assigned_to: assignedTo, expected_revision: expectedRevision })
  });
}

export async function batchAssignReviewTasks(tasks: Array<Pick<ReviewTask, "id" | "revision">>, assignedTo: string) {
  return apiClient.request<{ tasks: ReviewTask[] }>("/api/v1/review-tasks/batch-assign", {
    method: "POST",
    body: JSON.stringify({
      task_ids: tasks.map((task) => task.id),
      assigned_to: assignedTo,
      expected_revisions: Object.fromEntries(tasks.map((task) => [task.id, task.revision]))
    })
  });
}

export async function downloadReviewWorkspaceImage(path: string) {
  return apiClient.requestBlob(path);
}

export async function claimNextReviewTask(examId = "", questionId = "") {
  return apiClient.request<{ task: ReviewTask }>("/api/v1/review-tasks/next", {
    method: "POST",
    body: JSON.stringify({ exam_id: examId, question_id: questionId })
  });
}

export async function renewReviewTask(taskId: string) {
  return apiClient.request<{ renewed: boolean }>(`/api/v1/review-tasks/${encodeURIComponent(taskId)}/renew`, { method: "POST" });
}

export async function releaseReviewTask(taskId: string) {
  return apiClient.request<{ task: ReviewTask }>(`/api/v1/review-tasks/${encodeURIComponent(taskId)}/release`, { method: "POST" });
}

export async function submitHumanGrade(taskId: string, payload: SubmitHumanGradePayload) {
  return executeBusinessCommand("review.submit", taskId, payload, (commandId, original) => apiClient.request<SubmitHumanGradeResult>(`/api/v1/review-tasks/${encodeURIComponent(taskId)}/submit`, {
    method: "POST",
    headers: { "Idempotency-Key": commandId },
    body: JSON.stringify(original)
  }), result => result as SubmitHumanGradeResult);
}

export async function returnReviewTask(taskId: string, reason: string, expectedRevision: number) {
  return apiClient.request<{ task: ReviewTask }>(`/api/v1/review-tasks/${encodeURIComponent(taskId)}/return`, {
    method: "POST",
    body: JSON.stringify({ reason, expected_revision: expectedRevision })
  });
}

export async function getReviewDraft(taskId: string) {
  return apiClient.request<{ draft: ReviewDraft | null }>(`/api/v1/review-tasks/${encodeURIComponent(taskId)}/draft`);
}

export async function saveReviewDraft(taskId: string, payload: SaveReviewDraftPayload) {
  return apiClient.request<{ draft: ReviewDraft }>(`/api/v1/review-tasks/${encodeURIComponent(taskId)}/draft`, {
    method: "PUT",
    body: JSON.stringify(payload)
  });
}

export async function listArbitrationTasks(filter: ArbitrationTaskFilter = {}) {
  return apiClient.request<{ arbitration_tasks: ArbitrationTask[]; next_cursor: string; has_more: boolean }>(`/api/v1/arbitration-tasks${queryString(filter)}`);
}

export async function createArbitrationTask(payload: CreateArbitrationTaskPayload) {
  return generatedApi.createArbitrationTask({ body: payload });
}

export async function getArbitrationTask(id: string) {
  return generatedApi.getArbitrationTask({ path: { id } });
}

export async function assignArbitrationTask(id: string, payload: AssignArbitrationPayload) {
  return generatedApi.assignArbitrationTask({ path: { id }, body: payload });
}

export async function submitArbitration(id: string, payload: SubmitArbitrationPayload) {
  if (!id) throw new Error("仲裁任务 ID 不能为空");
  return executeBusinessCommand("review.arbitrate", id, payload, (commandId, original) => generatedApi.submitArbitration({
    path: { id },
    headers: { "Idempotency-Key": commandId },
    body: original as SubmitArbitrationPayload
  }), result => result as SubmitArbitrationResult);
}

export async function recordSegmentAnswer(segmentId: string, payload: SegmentAnswerPayload) {
  return generatedApi.recordSegmentAnswer({ path: { id: segmentId }, body: payload });
}

export async function createRuleGrade(segmentId: string) {
  return generatedApi.createRuleGrade({ path: { id: segmentId } });
}

export async function createSubjectiveAiGrade(segmentId: string): Promise<{ grade: AiGrade }> {
  // Model governance and production defaults are owned by the gateway. The
  // workbench must never select a mock deployment or override score authority.
  const result = await generatedApi.createSubjectiveAIGrade({ path: { id: segmentId }, body: {} });
  const mode = result.grade.delivery_mode;
  if (mode !== "teacher_suggestion" && mode !== "teacher_review" && mode !== "shadow_only") throw new Error("Unexpected AI delivery mode");
  return { grade: { ...result.grade, delivery_mode: mode } };
}

export async function listAiGrades(segmentId: string) {
  return generatedApi.listSegmentAIGrades({ path: { id: segmentId } });
}

export async function verifyEvidence(gradeId: string) {
  return apiClient.request<{ job: EvidenceJob }>(`/api/v1/ai-grades/${encodeURIComponent(gradeId)}/verify-evidence`, {
    method: "POST",
    body: JSON.stringify({})
  });
}

export async function recoverScoringCommand(examId: string, commandId: string) {
  return apiClient.request<import("@edugrade/sdk").ScoringCommandRecovery>(`/api/v1/exams/${encodeURIComponent(examId)}/scoring-runs/commands/${encodeURIComponent(commandId)}`);
}

export async function startScoringRun(examId: string, idempotencyKey: string) {
  return apiClient.request<{ scoring_run: ScoringRun }>(`/api/v1/exams/${encodeURIComponent(examId)}/scoring-runs`, {
    method: "POST",
    headers: { "Idempotency-Key": idempotencyKey },
    body: JSON.stringify({ idempotency_key: idempotencyKey })
  });
}

export async function getScoringReadiness(examId: string) {
  return generatedApi.getExamScoringReadiness({ path: { examId } });
}

export async function getScoringSummary(examId: string) {
  return generatedApi.getExamScoringSummary({ path: { examId } });
}

export async function getExamAutomationResults(examId: string) {
  return apiClient.request<ExamAutomationResults>(`/api/v1/exams/${encodeURIComponent(examId)}/automation-results`);
}

export async function getScoringRun(runId: string) {
  return generatedApi.getScoringRun({ path: { runId } });
}

export async function downloadScoringResultImage(segmentId: string) {
  return apiClient.requestBlob(`/api/v1/answer-segments/${encodeURIComponent(segmentId)}/image`);
}

export async function cancelScoringRun(runId: string) {
  return generatedApi.cancelScoringRun({ path: { runId } });
}

export async function retryFailedScoringRun(runId: string) {
  return generatedApi.retryFailedScoringRun({ path: { runId } });
}

export async function reprocessSegmentScore(segmentId: string, idempotencyKey: string) {
  return apiClient.request<{ scoring_run: ScoringRun }>(`/api/v1/answer-segments/${encodeURIComponent(segmentId)}/reprocess-score`, {
    method: "POST",
    body: JSON.stringify({ idempotency_key: idempotencyKey })
  });
}
