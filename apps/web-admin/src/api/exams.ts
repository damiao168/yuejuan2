import { apiClient } from "./client";
import { buildQueryString } from "./query";

export type ExamStatus = "draft" | "configured" | "ready" | "collecting" | "grading" | "reviewing" | "finalized" | "published" | "archived";

export type GradingMode = "auto_objective_only" | "ai_assisted" | "human_review_required" | "double_mark" | "blind_double_mark";

export interface Exam {
  id: string;
  tenant_id: string;
  school_id: string;
  exam_session_id?: string;
  exam_session_name?: string;
  exam_session_grade_id?: string;
  name: string;
  subject: string;
  exam_type: string;
  total_score: number;
  // 响应允许服务端新增状态或模式，界面按已知值决定展示和操作入口。
  status: string;
  grading_mode: string;
  appeal_enabled: boolean;
  publish_policy: string;
  created_by: string;
  class_ids: string[];
  revision: number;
  created_at?: string;
}

export interface ExamListFilter {
  status?: string;
  school_id?: string;
  limit?: number;
  cursor?: string;
}

export interface ExamPayload {
  school_id: string;
  name: string;
  subject: string;
  exam_type: string;
  total_score: number;
  grading_mode: string;
  appeal_enabled: boolean;
  publish_policy: string;
  class_ids: string[];
}

export interface ExamSessionPayload {
  creation_mode?: "materials"|"quick"|"template";
  school_id: string;
  grade_id: string;
  template_id?: string;
  name: string;
  exam_type: string;
  grading_mode: string;
  appeal_enabled: boolean;
  publish_policy: string;
  class_ids: string[];
  subjects: Array<{
    subject: string;
    total_score: number;
    duration_minutes: number;
    candidate_rule: string;
    class_ids: string[];
    sections?: Array<{ title: string; question_type: string; question_count: number; score_per_question: number }>;
  }>;
}

export interface ExamSession {
  id: string;
  school_id: string;
  grade_id: string;
  template_id?: string;
  template_version?: number;
  name: string;
  exam_type: string;
  status: string;
  exams: Exam[];
}

export interface CandidateRefreshResult {
  before_count: number;
  after_count: number;
  added_count: number;
  removed_count: number;
}

export async function listExams(filter: ExamListFilter = {}) {
  return apiClient.request<{ exams: Exam[]; next_cursor?: string; has_more?: boolean }>(`/api/v1/exams${buildQueryString(filter)}`);
}

/**
 * 考试选择器需要读取全部分页，避免后续页的考试无法进入阅卷、发布或分析。
 * 任一页请求失败或游标异常都会拒绝；调用方不能把已读取的部分考试当作完整列表。
 */
export async function listAllExams(filter: Omit<ExamListFilter, "limit" | "cursor"> = {}): Promise<{ exams: Exam[] }> {
  const exams = new Map<string, Exam>();
  const seenCursors = new Set<string>();
  let cursor: string | undefined;
  while (true) {
    const page = await listExams({ ...filter, limit: 100, cursor });
    for (const exam of page.exams) exams.set(exam.id, exam);
    if (!page.has_more) break;
    // 服务端仍声明有下一页时，空或重复游标代表分页异常，不能静默返回半份列表或反复请求同一页。
    if (!page.next_cursor || seenCursors.has(page.next_cursor)) throw new Error("考试分页异常，请刷新后重试");
    seenCursors.add(page.next_cursor);
    cursor = page.next_cursor;
  }
  return { exams: [...exams.values()] };
}

export async function getExam(id: string) {
  return apiClient.request<{ exam: Exam }>(`/api/v1/exams/${encodeURIComponent(id)}`);
}

export async function createExam(payload: ExamPayload) {
  return apiClient.request<{ exam: Exam }>("/api/v1/exams", {
    method: "POST",
    body: JSON.stringify(payload)
  });
}

export async function createExamSession(payload: ExamSessionPayload, commandId: string) {
  return apiClient.request<{ exam_session: ExamSession }>("/api/v1/exam-sessions", {
    method: "POST",
    headers: { "Idempotency-Key": commandId },
    body: JSON.stringify({ ...payload, command_id: commandId })
  });
}

export interface ExamSessionCommandResult {
  command_id: string;
  status: "not_accepted" | "processing" | "succeeded" | "rejected" | "unknown";
  http_status?: number;
  error_code?: string;
  exam_session?: ExamSession;
}

export async function recoverExamSessionCommand(commandId: string) {
  return apiClient.request<{ command: ExamSessionCommandResult }>(`/api/v1/exam-sessions/commands/${encodeURIComponent(commandId)}`);
}

export async function updateExam(id: string, payload: Partial<ExamPayload> & { expected_revision: number }) {
  return apiClient.request<{ exam: Exam }>(`/api/v1/exams/${encodeURIComponent(id)}`, {
    method: "PATCH",
    body: JSON.stringify(payload)
  });
}

export async function updateExamStatus(id: string, status: string, expectedRevision: number) {
  return apiClient.request<{ exam: Exam }>(`/api/v1/exams/${encodeURIComponent(id)}/status`, {
    method: "POST",
    body: JSON.stringify({ status, expected_revision: expectedRevision })
  });
}

export async function refreshExamCandidates(id: string) {
  return apiClient.request<{ candidate_refresh: CandidateRefreshResult }>(`/api/v1/exams/${encodeURIComponent(id)}/candidates/refresh`, {
    method: "POST"
  });
}

export async function archiveExam(id: string, expectedRevision: number) {
  return apiClient.request<{ exam: Exam }>(`/api/v1/exams/${encodeURIComponent(id)}/archive`, {
    method: "POST",
    body: JSON.stringify({ expected_revision: expectedRevision })
  });
}

export type ManagementExamKind = "session" | "standalone";

export interface ExamArchiveBlocker {
  code: string;
  message: string;
  exam_id?: string;
  subject?: string;
}

/** 整场查询保留完整学科范围；客户端筛选不能改变编辑或归档对象。 */
export interface ManagementExam {
  id: string;
  kind: ManagementExamKind;
  school_id: string;
  grade_id: string;
  name: string;
  exam_type: string;
  revision: number;
  created_by: string;
  created_at?: string;
  updated_at?: string;
  class_ids: string[];
  default_class_ids: string[];
  inherited_subject_ids: string[];
  exams: Exam[];
  progress: {
    status: string;
    label: string;
    total_count: number;
    ready_count: number;
    published_count: number;
    counts: Record<string, number>;
    facts_available?: boolean;
  };
  archived_at?: string;
  archived_by?: string;
  archive_reason?: string;
  legacy_archived?: boolean;
  archived_subject_ids?: string[];
  scope_complete: boolean;
  can_manage: boolean;
  can_edit: boolean;
  can_archive: boolean;
  can_restore: boolean;
  archive_blockers?: ExamArchiveBlocker[];
}

export interface ManagementExamFilter {
  school_id?: string;
  grade_id?: string;
  search?: string;
  exam_type?: string;
  status?: string;
  archive?: "active" | "archived" | "all";
  limit?: number;
  cursor?: string;
}

export interface ManagementExamVersion {
  expected_revision: number;
  expected_subject_revisions: Record<string, number>;
}

export interface ManagementExamUpdate extends ManagementExamVersion {
  name?: string;
  exam_type?: string;
  class_ids?: string[];
}

export function managementExamVersion(exam: ManagementExam): ManagementExamVersion {
  return { expected_revision: exam.revision, expected_subject_revisions: Object.fromEntries(exam.exams.map((subject) => [subject.id, subject.revision])) };
}

function managementExamPath(kind: ManagementExamKind, id: string) {
  return `/api/v1/exam-management/${kind}/${encodeURIComponent(id)}`;
}

export async function listManagementExams(filter: ManagementExamFilter = {}) {
  return apiClient.request<{ exams: ManagementExam[]; next_cursor?: string; has_more?: boolean }>(`/api/v1/exam-management${buildQueryString(filter)}`);
}

export async function listAllManagementExams(filter: Omit<ManagementExamFilter, "limit" | "cursor"> = {}) {
  const exams = new Map<string, ManagementExam>();
  const seenCursors = new Set<string>();
  let cursor: string | undefined;
  while (true) {
    const result = await listManagementExams({ ...filter, limit: 100, cursor });
    for (const exam of result.exams) exams.set(`${exam.kind}:${exam.id}`, exam);
    if (!result.has_more) break;
    // 分页按整场进行；异常游标不能让半份列表被当作完整考试范围。
    if (!result.next_cursor || seenCursors.has(result.next_cursor)) throw new Error("考试分页异常，请刷新后重试");
    seenCursors.add(result.next_cursor);
    cursor = result.next_cursor;
  }
  return { exams: [...exams.values()] };
}

export async function getManagementExam(kind: ManagementExamKind, id: string) {
  return apiClient.request<{ exam: ManagementExam }>(managementExamPath(kind, id));
}

export async function updateManagementExam(exam: Pick<ManagementExam, "kind" | "id">, payload: ManagementExamUpdate, commandId: string) {
  return apiClient.request<{ exam: ManagementExam }>(managementExamPath(exam.kind, exam.id), {
    method: "PATCH", headers: { "Idempotency-Key": commandId }, body: JSON.stringify(payload)
  });
}

export async function checkManagementExamArchive(exam: Pick<ManagementExam, "kind" | "id">) {
  return apiClient.request<{ check: { allowed: boolean; blockers: ExamArchiveBlocker[] } }>(`${managementExamPath(exam.kind, exam.id)}/archive-check`);
}

export async function archiveManagementExam(exam: Pick<ManagementExam, "kind" | "id">, version: ManagementExamVersion, commandId: string) {
  return apiClient.request<{ exam: ManagementExam }>(`${managementExamPath(exam.kind, exam.id)}/archive`, {
    method: "POST", headers: { "Idempotency-Key": commandId }, body: JSON.stringify({ ...version, command_id: commandId })
  });
}

export async function restoreManagementExam(exam: Pick<ManagementExam, "kind" | "id">, version: ManagementExamVersion, commandId: string) {
  return apiClient.request<{ exam: ManagementExam }>(`${managementExamPath(exam.kind, exam.id)}/restore`, {
    method: "POST", headers: { "Idempotency-Key": commandId }, body: JSON.stringify({ ...version, command_id: commandId })
  });
}

export async function recoverManagementExamCommand(commandId: string) {
  return apiClient.request<{ command: { command_id: string; status: "not_accepted" | "processing" | "succeeded" | "rejected" | "unknown"; exam?: ManagementExam } }>(`/api/v1/exam-management/commands/${encodeURIComponent(commandId)}`);
}
