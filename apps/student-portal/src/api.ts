import { EduGradeApi, type ApiTransport, type StudentReviewAnnotation } from "@edugrade/sdk";

export interface AuthUser {
  id: string;
  tenant_id: string;
  tenant_code: string;
  username: string;
  display_name: string;
  roles: string[];
  permissions: string[];
  data_scope: Record<string, unknown>;
}

export interface PublishedExam {
  exam_id: string;
  name: string;
  subject: string;
  exam_type?: string;
  release_version: number;
  published_at: string;
  total_score?: number;
  max_score?: number;
}

export interface StudentScoreReference {
  scope: "grade" | "class" | "exam";
  sample_size: number;
  statistics_available: boolean;
  // 无法公开统计的原因来自服务端，类型保留新增的原因代码。
  unavailable_reason?: string;
  mean_score?: number;
  median_score?: number;
  q1?: number;
  q3?: number;
  min_score?: number;
  max_score?: number;
  percentile?: number;
  rank?: number;
}

export interface StudentQuestion {
  question_id: string;
  question_no: string;
  subject?: string;
  question_type?: string;
  stem?: string;
  score: number;
  max_score: number;
  score_rate?: number;
  knowledge_points?: string[];
  cohort?: {
    sample_size: number;
    mean_score_rate: number;
    full_score_rate: number;
    zero_score_rate: number;
    class_mean_score?: number;
    class_unavailable_reason?: string;
    school_mean_score: number;
    median_score: number;
  };
  cohort_unavailable_reason?: string;
  correct_answer?: string;
  actual_answer?: string;
  page_no?: number;
  submission_page_id?: string;
  answer_geometry?: { x: number; y: number; width: number; height: number };
  feedback?: string;
  rubric_summary?: string[];
}

export interface StudentResult {
  exam_id: string;
  // 发布标识是不可变的公开成绩版本锚点，
  // 题目、批注和图片读取及申诉都会回传，防止新证据混入旧版申请；
  // 服务端仍从该发布版本读取原始成绩，不信任浏览器提供的分数。
  release_id: string;
  release_version: number;
  exam?: {
    name: string;
    subject: string;
    exam_type: string;
    published_at: string;
  };
  total_score: number;
  max_score: number;
  overall_total_score?: number;
  overall_max_score?: number;
  score_rate?: number;
  reference?: StudentScoreReference;
  rankings?: { class_rank: number; class_size: number; grade_rank: number; grade_size: number; class_unavailable_reason?: string; grade_unavailable_reason?: string };
  session_summary?: { published_count: number; expected_count: number; subjects: Array<{ exam_id: string; subject: string; name: string; status: string; score?: number; max_score?: number; published_at?: string; statistics_available: boolean; statistics_reason?: string; mean_score_rate?: number; sample_size?: number }> };
  paper_pages?: Array<{ page_no: number; question_id: string; submission_page_id?: string }>;
  high_score_paper?: {
    available: boolean;
    total_score?: number;
    max_score?: number;
    pages?: Array<{ page_no: number; question_id: string; submission_page_id?: string }>;
    score_marks?: Array<{ question_id: string; question_no: string; score: number; max_score: number; page_no: number; answer_geometry?: { x: number; y: number; width: number; height: number } }>;
  };
  subject_balance?: Array<{
    subject: string;
    student_score_rate: number;
    school_mean_score_rate: number;
    sample_size: number;
  }>;
  questions?: StudentQuestion[];
  appeal_window: {
    open: boolean;
    state?: "disabled" | "upcoming" | "open" | "closed";
    opens_at?: string;
    closes_at?: string;
    allowed_reason_codes?: string[];
  };
}

export interface StudentQuestionAppeal {
  id: string;
  exam_id: string;
  source_release_id: string;
  source_release_version: number;
  question_id: string;
  question_no: string;
  source_score: number;
  source_max_score: number;
  reason_code: string;
  reason: string;
  status: string;
  decision?: string;
  public_response?: string;
  new_release_id?: string;
  new_release_version?: number;
  reviewed_score?: number;
  reviewed_max_score?: number;
  created_at: string;
  updated_at: string;
}

export interface SelectedAppealRegion {
  coordinate_space: "canonical_image_normalized";
  x: number;
  y: number;
  width: number;
  height: number;
}

// 由公开 OpenAPI 契约生成，刻意不包含
// reviewer、tenant、revision、visibility 或 payload 字段。
export type StudentQuestionAnnotation = StudentReviewAnnotation;

export class PortalApiError extends Error {
  constructor(readonly status: number, readonly code: string, message: string) {
    super(message);
    this.name = "PortalApiError";
  }
}

function normalizeApiBaseUrl(value: string): string {
  // 空地址用于同源请求，相对路径也可配置；只清除首尾空白和末尾斜杠。
  const trimmed = value.trim();
  let end = trimmed.length;
  while (end > 0 && trimmed[end - 1] === "/") end -= 1;
  return trimmed.slice(0, end);
}

const baseUrl = normalizeApiBaseUrl(import.meta.env.VITE_API_BASE_URL ?? "");

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const response = await fetch(`${baseUrl}${path}`, {
    ...init,
    credentials: "include",
    headers: { Accept: "application/json", ...init.headers }
  });
  if (!response.ok) {
    let payload: { error?: { code?: string; message?: string }; code?: string; message?: string } | undefined;
    try {
      payload = await response.json() as typeof payload;
    } catch {
      // 代理拒绝请求且未返回 JSON 时，状态码仍有助于判断错误。
    }
    throw new PortalApiError(
      response.status,
      payload?.error?.code ?? payload?.code ?? "request_failed",
      payload?.error?.message ?? payload?.message ?? response.statusText
    );
  }
  return response.json() as Promise<T>;
}

// 保留学生端现有会话、Cookie 和 PortalApiError 行为，
// 同时通过共享的生成 SDK 调用新的契约接口。
const sdkTransport: ApiTransport = { request };
const sdk = new EduGradeApi(sdkTransport);

export function currentUser() {
  return request<{ user: AuthUser }>("/api/v1/auth/me");
}

export function login(input: { tenant_code: string; username: string; password: string }) {
  return request<{ user: AuthUser }>("/api/v1/auth/login", {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-EduGrade-CSRF": "1" },
    body: JSON.stringify(input)
  });
}

export function logout() {
  return request<void>("/api/v1/auth/logout", { method: "POST", headers: { "X-EduGrade-CSRF": "1" } });
}

export function listPublishedExams() {
  return request<{ exams: PublishedExam[] }>("/api/v1/student/exams");
}

export function getResult(examID: string) {
  return request<{ result: StudentResult }>(`/api/v1/student/exams/${encodeURIComponent(examID)}/result`);
}

export function getQuestion(examID: string, questionID: string, expectedReleaseID?: string) {
  return request<{ question: StudentQuestion }>(`/api/v1/student/exams/${encodeURIComponent(examID)}/questions/${encodeURIComponent(questionID)}${studentReleaseQuery(expectedReleaseID)}`);
}

// 这是限定到学生及单道题的裁切图 URL。API 根据
// 该学生当前已发布的成绩版本进行授权；学生端不会获得
// 答题片段 ID 或存储 URL。
export function studentQuestionAnswerImageURL(examID: string, questionID: string, expectedReleaseID?: string) {
  return `${baseUrl}/api/v1/student/exams/${encodeURIComponent(examID)}/questions/${encodeURIComponent(questionID)}/answer-image${studentReleaseQuery(expectedReleaseID)}`;
}

export function studentPaperPageImageURL(examID: string, questionID: string, highScore = false, expectedReleaseID?: string) {
  const query = studentReleaseQuery(expectedReleaseID, highScore);
  return `${baseUrl}/api/v1/student/exams/${encodeURIComponent(examID)}/questions/${encodeURIComponent(questionID)}/page-image${query}`;
}

export function listQuestionAnnotations(examID: string, questionID: string, expectedReleaseID?: string) {
  return sdk.listStudentQuestionReviewAnnotations({ path: { examId: examID, questionId: questionID }, ...(expectedReleaseID ? { query: { expected_release_id: expectedReleaseID } } : {}) });
}

export function createQuestionAppeal(examID: string, input: {
  source_release_id: string;
  question_id: string;
  reason_code: string;
  reason: string;
  selected_region?: SelectedAppealRegion;
}) {
  return request<{ appeal: StudentQuestionAppeal }>(`/api/v1/student/exams/${encodeURIComponent(examID)}/question-appeals`, {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-EduGrade-CSRF": "1" },
    body: JSON.stringify(input)
  });
}

export function listQuestionAppeals(examID: string) {
  return request<{ appeals: StudentQuestionAppeal[] }>(`/api/v1/student/question-appeals?exam_id=${encodeURIComponent(examID)}`);
}

// 页面绑定的发布 ID 随每次证据请求发送；服务端比较实际读取版本，不能由前端预查指针代替。
function studentReleaseQuery(expectedReleaseID?: string, highScore = false) {
 const query = new URLSearchParams();
 if (expectedReleaseID) query.set("expected_release_id", expectedReleaseID);
 if (highScore) query.set("variant", "high_score");
 const value = query.toString();
 return value ? `?${value}` : "";
}
