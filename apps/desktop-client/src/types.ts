export interface AuthUser {
  id: string;
  tenant_id: string;
  tenant_code: string;
  username: string;
  display_name: string;
  status: string;
  roles: string[];
  permissions: string[];
  data_scope: Record<string, unknown>;
}

export interface LoginResult {
  token_type: string;
  access_token: string;
  expires_at: string;
  user: AuthUser;
}

export interface ReviewTask {
  id: string;
  revision: number;
  exam_id: string;
  question_id: string;
  question_no: string;
  answer_segment_id: string;
  submission_id: string;
  anonymous_code: string;
  source: string;
  status: string;
  priority: number;
  assigned_to?: string;
  grade_round?: string;
  due_at?: string;
  created_at: string;
  updated_at?: string;
}

export interface Exam {
  id: string;
  tenant_id: string;
  school_id: string;
  name: string;
  subject: string;
  exam_type: string;
  total_score: number;
  status: string;
  grading_mode: string;
  created_at?: string;
}

export interface FileAsset {
  id: string;
  tenant_id: string;
  owner_type: string;
  owner_id?: string;
  exam_id?: string;
  submission_id?: string;
  original_name: string;
  content_type: string;
  size_bytes: number;
  hash_sha256: string;
  visibility: string;
  uploaded_by: string;
  created_at: string;
}

export interface SubmissionPage {
  id: string;
  tenant_id: string;
  submission_id: string;
  file_asset_id: string;
  page_no: number;
  status: string;
  quality_issues?: string[];
  created_at: string;
}

export interface AnswerSegment {
  id: string;
  tenant_id: string;
  submission_id: string;
  submission_page_id: string;
  question_id: string;
  question_no: string;
  bbox: number[];
  source: string;
  status: string;
  review_notes?: string;
  created_at: string;
}

export interface OcrResult {
  id: string;
  tenant_id: string;
  ocr_task_id: string;
  submission_id: string;
  submission_page_id: string;
  text: string;
  bbox: number[];
  confidence: number;
  ocr_engine: string;
  ocr_version: string;
  created_at: string;
}

export interface OcrTask {
  id: string;
  tenant_id: string;
  submission_id: string;
  status: string;
  engine: string;
  engine_version: string;
  result_count: number;
  results?: OcrResult[];
  created_at: string;
}

export interface RubricPoint {
  id: string;
  description: string;
  score: number;
  required: boolean;
}

export interface Rubric {
  id: string;
  question_id: string;
  version: string;
  status: string;
  max_score: number;
  points: RubricPoint[];
}

export interface Question {
  id: string;
  tenant_id: string;
  exam_id: string;
  question_no: string;
  question_type: string;
  score: number;
  stem?: string;
  knowledge_points: string[];
  rubric?: Rubric;
}

export interface PointResult {
  code: string;
  label: string;
  score: number;
}

export interface AiGrade {
  id: string;
  tenant_id: string;
  answer_segment_id: string;
  question_id: string;
  question_no: string;
  suggested_score: number;
  max_score: number;
  confidence: number;
  matched_points: PointResult[];
  missing_points: PointResult[];
  risk_flags: string[];
  mock: boolean;
  student_feedback?: string;
  created_at: string;
}

export interface RubricSelection {
  point_id: string;
  score: number;
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
  created_at: string;
}

export interface OfflineTaskPackage {
  task: ReviewTask;
  segment?: AnswerSegment;
  page?: SubmissionPage;
  ocrText: string;
  question?: Question;
  aiGrades: AiGrade[];
  warnings: string[];
  downloadedAt: string;
  rubricVersion?: string;
}

export interface OfflineGradeDraft {
  taskId: string;
  score: number | null;
  rubricSelections: Record<string, number>;
  comments: string;
  studentFeedback: string;
  privateNote: string;
  reason: string;
}

export type OfflineSyncStatus = "draft" | "syncing" | "synced" | "failed" | "conflict";

export interface OfflineDraftRecord {
  taskId: string;
  anonymousCode: string;
  savedAt: string;
  expiresAt: string;
  syncStatus: OfflineSyncStatus;
  syncMessage?: string;
  packageSnapshot: OfflineTaskPackage;
  draft: OfflineGradeDraft;
}

export interface SubmissionQualityIssue {
  code: string;
  message: string;
}

export interface SubmissionQualityResult {
  valid: boolean;
  issues: SubmissionQualityIssue[];
}

export type WorkspaceKey = "connect" | "tasks" | "scan" | "offline" | "sync" | "diagnostics" | "logs";

export type QueueStatus = "pending" | "uploading" | "succeeded" | "failed" | "conflict" | "not_configured";

export type ScanQualityStatus = "passed" | "warning" | "failed" | "not_configured";

export interface ScanQualityCheck {
  key: string;
  label: string;
  status: ScanQualityStatus;
  detail: string;
}

export interface SyncQueueItem {
  id: string;
  title: string;
  kind: "scan_upload" | "offline_grade" | "system";
  status: QueueStatus;
  progress: number;
  detail: string;
  updatedAt: string;
  examId?: string;
  captureBatchId?: string;
  examName?: string;
  submissionId?: string;
  pageNo?: number;
  fileName?: string;
  fileSize?: number;
  contentType?: string;
  previewUrl?: string;
  fileAssetId?: string;
  serverStatus?: string;
  requiresReselect?: boolean;
  qualityChecks?: ScanQualityCheck[];
  /** Native SQLite/spool identity. It is never a filesystem path. */
  localAssetId?: string;
  /** Stable upload identity, derived from the asset hash and answer-page context. */
  idempotencyKey?: string;
  retryCount?: number;
  confirmedOffset?: number;
  remoteUploadId?: string;
}

export type CapabilityStatus = "ready" | "not_configured" | "browser_fallback" | "unavailable";

export interface CapabilityProbe {
  key: string;
  name: string;
  status: CapabilityStatus;
  detail: string;
}

export interface RuntimeDiagnostics {
  runtime: string;
  platform: string;
  appVersion: string;
  logPath?: string;
}

export interface LocalCacheSecurityIssue {
  key: string;
  severity: "warning" | "critical";
  message: string;
}

export interface LocalCacheSecurityStatus {
  status: "passed" | "warning";
  checkedAt: string;
  scannedKeys: number;
  issues: LocalCacheSecurityIssue[];
}

export interface DependencyStatus {
  name: string;
  status: "ok" | "error" | "not_configured";
  error?: string;
  detail?: string;
  duration_ms: number;
  checked_at: string;
}

export interface SystemStatus {
  status: "healthy" | "degraded";
  service: string;
  environment: string;
  version: string;
  started_at: string;
  generated_at: string;
  uptime_sec: number;
  dependencies: DependencyStatus[];
  observability: {
    log_format: string;
    system_log_stream: string;
    audit_log_stream: string;
    request_id_header: string;
    trace_id_header: string;
    slow_request_threshold_ms: number;
    slow_query_log: string;
    sensitive_log_policy: string;
  };
}

export interface LocalLogEntry {
  id: string;
  at: string;
  level: "info" | "warning" | "error";
  message: string;
  context?: string;
}
