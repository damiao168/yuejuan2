import { EduGradeApi } from "@edugrade/sdk";
import { apiClient } from "./client";
export { uploadFile } from "./files";
export type { FileAsset } from "./files";

const generatedApi = new EduGradeApi(apiClient);

export interface PaperFile {
  original_name: string;
  content_type: string;
  size_bytes: number;
  hash_sha256: string;
  storage_bucket?: string;
  storage_key?: string;
}

export interface PaperVersion {
  id: string;
  tenant_id: string;
  exam_id: string;
  file_asset_id: string;
  version_no: number;
  status: string;
  file: PaperFile;
}

export interface PaperImportDraftQuestion {
	candidate_id?: string;
	answer_candidate_id?: string;
	solution_candidate_id?: string;
	rubric_candidate_id?: string;
	source_refs: PaperImportSourceRef[];
  question_no: string;
  question_type: string;
  assessment_archetype?: PaperImportAssessmentArchetype;
  score: number;
  score_source?: "material" | "missing" | "blueprint" | "human";
  score_resolution?: "use_blueprint" | "use_material";
  question_type_resolution?: "use_blueprint" | "use_material";
  stem: string;
  options?: string[];
  parent_question_no?: string;
  subquestion_no?: string;
  knowledge_points: string[];
  confidence: number;
  issues: string[];
  matched_question_id?: string;
  match_status?: "create" | "matched" | "matched_by_order" | "mismatch" | "extra" | "ambiguous";
	answer_key?: AnswerKeyInput;
	solution?: SolutionInput;
	rubric?: RubricPayload;
	completeness_status?: "complete" | "needs_review";
	human_confirmed_fields?: string[];
}

export type PaperImportRole = "auto" | "question" | "answer" | "solution" | "rubric" | "mixed" | "unknown";
export type PaperImportAssessmentArchetype = "selected_response" | "exact_text" | "numeric_expression" | "structured_steps" | "short_constructed" | "extended_response" | "diagram_graph" | "table_experiment";
export interface PaperImportSource { id: string; file_asset_id: string; document_index: number; role_hint: PaperImportRole; detected_role: Exclude<PaperImportRole, "auto">; role_confidence: number; processing_status: "pending" | "processing" | "processed" | "failed"; original_name?: string; content_type?: string; }
export interface PaperImportSourceRef { source_id: string; file_asset_id: string; document_index: number; page_no?: number; block_id?: string; bbox?: unknown; text_start?: number; text_end?: number; ocr_confidence?: number; }
export interface QuestionCandidate {
  candidate_id: string;
  question_no_raw?: string;
  question_no_normalized?: string;
  parent_question_no?: string;
  subquestion_no?: string;
  section_hint?: string;
  stem?: string;
  options: string[];
  question_type?: string;
  score?: number;
  knowledge_point_hints: string[];
  confidence: number;
  source_refs: PaperImportSourceRef[];
  issues: string[];
}
export interface AnswerCandidate { candidate_id: string; question_no_hint?: string; question_no_normalized?: string; subquestion_no_hint?: string; standard_answer?: unknown; equivalent_answers: unknown[]; tolerance?: unknown; confidence: number; source_refs: PaperImportSourceRef[]; issues: string[]; }
export interface SolutionCandidate { candidate_id: string; question_no_hint?: string; question_no_normalized?: string; subquestion_no_hint?: string; raw_text: string; steps: SolutionStep[]; confidence: number; source_refs: PaperImportSourceRef[]; issues: string[]; }
export interface RubricCandidatePoint { id: string; description: string; score?: number | null; required?: boolean | null; evidence_requirements?: RubricEvidenceRequirement[]; }
export interface RubricCandidate { candidate_id: string; question_no_hint?: string; question_no_normalized?: string; max_score?: number | null; points: RubricCandidatePoint[]; deductions: unknown[]; examples: unknown[]; confidence: number; source_refs: PaperImportSourceRef[]; issues: string[]; }
export interface SuggestedRubricPoint { id: string; description: string; suggested_score: number | null; evidence_step_ids: string[]; source_refs: PaperImportSourceRef[]; review_note: string; }
export interface SuggestedRubricCandidate { candidate_id: string; question_no_normalized?: string; max_score: number | null; points: SuggestedRubricPoint[]; origin: "ai_suggestion_from_solution"; status: "review_required"; provenance?: Record<string, unknown>; issues: string[]; }
export interface SolutionStep { step_no: number; content: string; }
export interface SolutionInput { raw_text: string; steps: SolutionStep[]; source_refs: PaperImportSourceRef[]; }
export interface PaperImportIssue { code: string; severity: "info" | "warning" | "error"; certainty: "confirmed" | "suspected" | "unknown"; question_no?: string; section?: string; message: string; confidence?: number; source_refs: PaperImportSourceRef[]; resolution_hint?: string; }

export interface PaperImportJob {
  id: string;
  exam_id: string;
  exam_paper_id: string;
  paper_file_asset_id: string;
  answer_file_asset_id: string;
  status: "processing" | "review_required" | "failed" | "cancelled" | "applied";
  generation: number;
  run_id: string;
  source_revision: string;
  result_generation?: number;
  subject: string;
	authoritative_subject_code?: string;
	recognition_policy_version?: string;
	recognition_policy_hash?: string;
	formula_status?: "pending" | "running" | "succeeded" | "review_required" | "failed";
	formula_region_count?: number;
	formula_review_count?: number;
	model_usage?: {
		input_tokens?: number;
		cached_input_tokens?: number;
		output_tokens?: number;
		reasoning_tokens?: number;
		total_tokens?: number;
	};
	runtime_progress?: {
		task_type: string;
		task_status: string;
		stage?: string;
		phase?: string;
		completed?: number;
		total?: number;
		unit?: "document" | "page" | "formula_region" | string;
		page_no?: number;
		page_total?: number;
		batch_no?: number;
		batch_total?: number;
		batch_size?: number;
		event_seq?: number;
		cold_start?: boolean;
		model?: string;
		runtime_mode?: "resident" | "per_job" | string;
		runtime_plan_source?: "explicit" | "measured_profile" | "compatibility_default" | string;
		runtime_batch_size?: number;
		parse_route?: string;
		message?: string;
		progress_changed_at?: string;
		started_at?: string;
		updated_at: string;
	};
	sources: PaperImportSource[];
	question_candidates: QuestionCandidate[];
	answer_candidates: AnswerCandidate[];
	solution_candidates: SolutionCandidate[];
	rubric_candidates: RubricCandidate[];
	structured_issues: PaperImportIssue[];
  questions: PaperImportDraftQuestion[];
  issues: string[];
  error_code?: string;
  created_at: string;
  updated_at?: string;
  applied_at?: string;
}

export interface AnswerKeyInput {
  standard_answer: unknown;
  equivalent_answers: unknown[];
  tolerance: unknown;
}

export interface AnswerKey extends AnswerKeyInput {
  id: string;
  question_id: string;
  answer_version: string;
  paper_import_id?: string;
  paper_import_candidate_id?: string;
  paper_import_source_refs?: PaperImportSourceRef[];
}

export interface RubricPoint {
  id: string;
  description: string;
  score: number;
  required: boolean;
  evidence_requirements?: RubricEvidenceRequirement[];
}

export interface RubricEvidenceRequirement {
  type: "all_of" | "any_of" | "at_least" | "valid_transformation" | "final_result" | "concept" | "unit" | "domain";
  target?: string;
  minimum?: number;
  children?: RubricEvidenceRequirement[];
}

export interface ScoringRule {
  id: string;
  question_id: string;
  version: number;
  rule_type: string;
  config: Record<string, unknown>;
  status: "draft" | "published" | "retired";
  revision: number;
  content_hash: string;
  published_at?: string;
}

export interface Rubric {
  id: string;
  question_id: string;
  version: string;
  status: string;
  max_score: number;
  points: RubricPoint[];
  deductions: unknown[];
  examples: unknown[];
  paper_import_id?: string;
  paper_import_source_refs?: PaperImportSourceRef[];
}

export interface Question {
  id: string;
  tenant_id: string;
  exam_id: string;
  exam_paper_id?: string;
  question_no: string;
  question_type: string;
  score: number;
  stem?: string;
  options: string[];
  parent_question_no?: string;
  subquestion_no?: string;
  knowledge_points: string[];
  answer_area?: Record<string, unknown>;
  sort_order: number;
  status: string;
  assessment_archetype?: string;
  paper_import_id?: string;
  paper_import_candidate_id?: string;
  paper_import_source_refs?: PaperImportSourceRef[];
  answer_key?: AnswerKey;
  solution?: { id: string; question_id: string; paper_import_id?: string; solution_version: string; raw_text: string; steps: SolutionStep[]; source_refs: PaperImportSourceRef[]; verification_status: "machine" | "human_confirmed" | "conflicted" };
  rubric?: Rubric;
}

export interface QuestionPayload {
  exam_paper_id?: string;
  question_no: string;
  question_type: string;
  score: number;
  stem: string;
  options?: string[];
  parent_question_no?: string;
  subquestion_no?: string;
  knowledge_points: string[];
  answer_area: Record<string, unknown>;
  sort_order: number;
  answer_key: AnswerKeyInput;
}

export interface RubricPayload {
  status: string;
  max_score: number;
  points: RubricPoint[];
  deductions: unknown[];
  examples: unknown[];
}

export interface ValidationIssue {
  code: string;
  message: string;
}

export interface ValidationResult {
  valid: boolean;
  issues: ValidationIssue[];
}

export async function registerPaperFromFile(examId: string, fileAssetId: string) {
  return apiClient.request<{ paper: PaperVersion; note: string }>(`/api/v1/exams/${encodeURIComponent(examId)}/papers`, {
    method: "POST",
    body: JSON.stringify({ file_asset_id: fileAssetId })
  });
}

export async function listPapers(examId: string) {
  return apiClient.request<{ papers: PaperVersion[] }>(`/api/v1/exams/${encodeURIComponent(examId)}/papers`);
}

export async function listPaperImports(examId: string) {
  return generatedApi.listPaperImports({ path: { examId } });
}

export async function getPaperImport(importId: string) {
	return apiClient.request<{ import: PaperImportJob }>(`/api/v1/paper-imports/${encodeURIComponent(importId)}`);
}

export async function createPaperImport(examId: string, payload: { exam_paper_id?: string; subject: string; sources: { file_asset_id: string; document_index: number; role_hint?: PaperImportRole }[]; paper_file_asset_id?: string; answer_file_asset_id?: string }, commandId: string) {
  return apiClient.request<{ import: PaperImportJob }>(`/api/v1/exams/${encodeURIComponent(examId)}/paper-imports`, { method: "POST", headers: { "Idempotency-Key": commandId }, body: JSON.stringify(payload) });
}

export async function addPaperImportSources(importId: string, expectedGeneration: number, sources: { file_asset_id: string; document_index: number; role_hint?: PaperImportRole }[], commandId: string) {
	return apiClient.request<{ import: PaperImportJob }>(`/api/v1/paper-imports/${encodeURIComponent(importId)}/sources`, { method: "POST", headers: { "Idempotency-Key": commandId }, body: JSON.stringify({ sources, expected_generation: expectedGeneration }) });
}

export async function replacePaperImportSources(importId: string, expectedGeneration: number, sources: { id: string; document_index: number; role_hint: PaperImportRole }[], commandId: string) {
	return apiClient.request<{ import: PaperImportJob }>(`/api/v1/paper-imports/${encodeURIComponent(importId)}/sources`, { method: "PUT", headers: { "Idempotency-Key": commandId }, body: JSON.stringify({ sources, expected_generation: expectedGeneration }) });
}

export async function savePaperImportReview(importId: string, expectedGeneration: number, questions: PaperImportDraftQuestion[]) {
	return generatedApi.savePaperImportReview({ path: { id: importId }, body: { expected_generation: expectedGeneration, questions } });
}

export async function generatePaperImportRubricDraft(importId: string, candidateId: string, expectedGeneration: number, expectedUpdatedAt: string) {
  return apiClient.request<{ suggested_rubric_candidates: SuggestedRubricCandidate[] }>(`/api/v1/paper-imports/${encodeURIComponent(importId)}/rubric-draft`, {
    method: "POST",
    body: JSON.stringify({ candidate_id: candidateId, expected_generation: expectedGeneration, expected_updated_at: expectedUpdatedAt })
  });
}

export async function applyPaperImport(importId: string) {
  return generatedApi.applyPaperImport({ path: { id: importId } });
}

export async function cancelPaperImport(importId: string, expectedGeneration: number, commandId: string) {
  return apiClient.request<{ import: PaperImportJob }>(`/api/v1/paper-imports/${encodeURIComponent(importId)}/cancel?expected_generation=${expectedGeneration}`, {
    method: "POST", headers: { "Idempotency-Key": commandId }
  });
}

export async function retryPaperImportParse(importId: string, expectedGeneration: number) {
	return apiClient.request<{ import: PaperImportJob }>(`/api/v1/paper-imports/${encodeURIComponent(importId)}/retry-parse?expected_generation=${expectedGeneration}`, {
		method: "POST"
	});
}

export async function listQuestions(examId: string) {
  return apiClient.request<{ questions: Question[] }>(`/api/v1/exams/${encodeURIComponent(examId)}/questions`);
}

export async function createQuestion(examId: string, payload: QuestionPayload) {
  return apiClient.request<{ question: Question }>(`/api/v1/exams/${encodeURIComponent(examId)}/questions`, {
    method: "POST",
    body: JSON.stringify(payload)
  });
}

export async function updateQuestion(questionId: string, payload: Partial<QuestionPayload>) {
  return apiClient.request<{ question: Question }>(`/api/v1/questions/${encodeURIComponent(questionId)}`, {
    method: "PATCH",
    body: JSON.stringify(payload)
  });
}

export async function deleteQuestion(questionId: string) {
  return apiClient.request<{ status: string }>(`/api/v1/questions/${encodeURIComponent(questionId)}`, {
    method: "DELETE"
  });
}

export async function createRubric(questionId: string, payload: RubricPayload) {
  return apiClient.request<{ rubric: Rubric }>(`/api/v1/questions/${encodeURIComponent(questionId)}/rubric`, {
    method: "POST",
    body: JSON.stringify(payload)
  });
}

export async function validatePaperConfig(examId: string) {
  return apiClient.request<{ result: ValidationResult }>(`/api/v1/exams/${encodeURIComponent(examId)}/validate-paper-config`, {
    method: "POST"
  });
}

export async function listScoringRules(questionId: string) {
  return generatedApi.listScoringRules({ path: { id: questionId } });
}

export async function createScoringRule(questionId: string, ruleType: string, config: Record<string, unknown>) {
  return generatedApi.createScoringRule({ path: { id: questionId }, body: { rule_type: ruleType, config } });
}

export async function updateScoringRule(ruleId: string, config: Record<string, unknown>, expectedRevision: number) {
  return generatedApi.updateScoringRule({ path: { id: ruleId }, body: { config, expected_revision: expectedRevision } });
}

export async function publishScoringRule(ruleId: string) {
  return generatedApi.publishScoringRule({ path: { id: ruleId } });
}
