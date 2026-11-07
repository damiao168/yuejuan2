import { apiClient } from "./client";

// STORY-080 新增的字段都标为可选：旧后端不返回它们。缺失表示“未知”，界面隐藏对应行或去掉分母，不能当成 0 显示。
// 首页考试类计数只覆盖未发布、未归档的考试，与 active_exams 范围一致。
export interface DashboardStatistics {
  active_exam_count: number;
  collecting_exam_count: number;
  /** draft、configured、ready 的考试数。 */
  preparing_exam_count?: number;
  /** grading、reviewing 的考试数。 */
  grading_exam_count?: number;
  pending_review_question_count: number;
  pending_review_submission_count: number;
  pending_arbitration_count: number;
  pending_arbitration_submission_count: number;
  failed_submission_count: number;
  unmatched_submission_count: number;
  quality_issue_submission_count: number;
  finalized_exam_count: number;
}

export interface DashboardOrganizationStatistics {
  active_student_count: number;
  grade_count: number;
  class_count: number;
  teacher_count: number;
  grader_count: number;
  empty_class_count: number;
  unassigned_teacher_count: number;
}

export interface DashboardBlockingIssue {
  code: string;
  label: string;
  count: number;
  unit: string;
  impact: string;
  action: string;
  drilldown_path: string;
}

export interface DashboardActiveExam {
  id: string;
  name: string;
  subject: string;
  status: string;
  submission_count: number;
  failed_count: number;
  quality_issue_count: number;
  unmatched_count: number;
  created_at: string;
  /** 本场待人工复核项数。 */
  pending_arbitration_count?: number;
  /** 本场小题阅卷任务数与已交数；排除 cancelled，已交指 submitted、completed。 */
  review_task_count?: number;
  review_done_count?: number;
  /** 名册来源不可用时为 null 并返回告警；本场尚无冻结名册时 roster_count 为 0。 */
  roster_count?: number | null;
  roster_submitted_count?: number | null;
}

export interface DashboardActivity {
  id: string;
  action: string;
  target_type: string;
  target_id?: string;
  title?: string;
  summary?: string;
  severity?: "info" | "success" | "warning";
  reason?: string;
  created_at: string;
  drilldown_path?: string;
}

export interface DashboardReviewTeacher {
  user_id: string;
  username: string;
  display_name: string;
  teaching_subjects: string[];
  assigned_count: number;
  in_progress_count: number;
  returned_count: number;
  submitted_count: number;
  remaining_count: number;
  is_online: boolean;
  last_active_at?: string;
}

export interface DashboardReviewTeacherSummary {
  teacher_count: number;
  online_count: number;
  online_window_seconds: number;
  task_counting_note: string;
  online_note: string;
}

export interface DashboardGradingQuestionProgress {
  exam_id: string;
  question_id: string;
  question_no: string;
  task_count: number;
  done_count: number;
  remaining_count: number;
  /** 最后更新时间落在时窗内的已交任务数；交卷后再次变更的任务按最近一次更新时间计入，是近似值。 */
  recent_done_count: number;
}

export interface DashboardGradingProgress {
  /** recent_done_count 的时窗，单位秒，当前固定 3600。 */
  window_seconds: number;
  questions: DashboardGradingQuestionProgress[];
}

export interface DashboardAppealStatistics {
  /** status=submitted，等待分派复核教师。 */
  pending_assignment_count: number;
  /** status=under_review 且复核教师已提交意见，等待管理员裁决。 */
  pending_decision_count: number;
}

export interface DashboardSummary {
  scope: {
    tenant_id: string;
    school_id?: string;
  };
  updated_at: string;
  organization_statistics: DashboardOrganizationStatistics;
  statistics: DashboardStatistics;
  blocking_issues: DashboardBlockingIssue[];
  active_exams: DashboardActiveExam[];
  recent_activities: DashboardActivity[];
  warnings: string[];
  review_teachers?: DashboardReviewTeacher[];
  review_teacher_summary?: DashboardReviewTeacherSummary;
  /** 阅卷任务来源失败时为 null（同时返回“阅卷待办暂时无法统计”）；旧后端不返回。 */
  grading_progress?: DashboardGradingProgress | null;
  /** 没有 appeal:manage 时省略；来源失败时也省略并返回告警。范围与教职工申诉列表一致，不受“未发布考试”口径限制。 */
  appeal_statistics?: DashboardAppealStatistics;
}

export function getDashboardSummary() {
  return apiClient.request<DashboardSummary>("/api/v1/dashboard/summary");
}
