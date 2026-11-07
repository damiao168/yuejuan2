package dashboard

import "time"

type Scope struct {
	TenantID string `json:"tenant_id"`
	SchoolID string `json:"school_id,omitempty"`
}

type Statistics struct {
	ActiveExamCount                   int `json:"active_exam_count"`
	CollectingExamCount               int `json:"collecting_exam_count"`
	PendingReviewQuestionCount        int `json:"pending_review_question_count"`
	PendingReviewSubmissionCount      int `json:"pending_review_submission_count"`
	PendingArbitrationCount           int `json:"pending_arbitration_count"`
	PendingArbitrationSubmissionCount int `json:"pending_arbitration_submission_count"`
	FailedSubmissionCount             int `json:"failed_submission_count"`
	UnmatchedSubmissionCount          int `json:"unmatched_submission_count"`
	QualityIssueSubmissionCount       int `json:"quality_issue_submission_count"`
	FinalizedExamCount                int `json:"finalized_exam_count"`
	// 阶段计数与 ActiveExamCount 同一范围：只统计未发布、未归档的考试。
	PreparingExamCount int `json:"preparing_exam_count"`
	GradingExamCount   int `json:"grading_exam_count"`
}

type OrganizationStatistics struct {
	ActiveStudentCount     int `json:"active_student_count"`
	GradeCount             int `json:"grade_count"`
	ClassCount             int `json:"class_count"`
	TeacherCount           int `json:"teacher_count"`
	GraderCount            int `json:"grader_count"`
	EmptyClassCount        int `json:"empty_class_count"`
	UnassignedTeacherCount int `json:"unassigned_teacher_count"`
}

type BlockingIssue struct {
	Code          string `json:"code"`
	Label         string `json:"label"`
	Count         int    `json:"count"`
	Unit          string `json:"unit"`
	Impact        string `json:"impact"`
	Action        string `json:"action"`
	DrilldownPath string `json:"drilldown_path"`
}

type ActiveExam struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Subject           string    `json:"subject"`
	Status            string    `json:"status"`
	SubmissionCount   int       `json:"submission_count"`
	FailedCount       int       `json:"failed_count"`
	QualityIssueCount int       `json:"quality_issue_count"`
	UnmatchedCount    int       `json:"unmatched_count"`
	CreatedAt         time.Time `json:"created_at"`
	// 来源读取失败时下面三项为 nil 并省略，不能把未知写成 0；来源可用但本场没有任务时为 0。
	PendingArbitrationCount *int `json:"pending_arbitration_count,omitempty"`
	ReviewTaskCount         *int `json:"review_task_count,omitempty"`
	ReviewDoneCount         *int `json:"review_done_count,omitempty"`
	// 名册来源不可用时两项为 nil，序列化为 null 并返回告警；本场尚未冻结名册时为 0。
	RosterCount          *int `json:"roster_count"`
	RosterSubmittedCount *int `json:"roster_submitted_count"`
}

type RecentActivity struct {
	ID            string    `json:"id"`
	Action        string    `json:"action"`
	TargetType    string    `json:"target_type"`
	TargetID      string    `json:"target_id,omitempty"`
	Title         string    `json:"title,omitempty"`
	Summary       string    `json:"summary,omitempty"`
	Severity      string    `json:"severity,omitempty"`
	Reason        string    `json:"reason,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	DrilldownPath string    `json:"drilldown_path,omitempty"`
}

type Summary struct {
	Scope                  Scope                   `json:"scope"`
	UpdatedAt              time.Time               `json:"updated_at"`
	OrganizationStatistics OrganizationStatistics  `json:"organization_statistics"`
	ReviewTeachers         []ReviewTeacherProgress `json:"review_teachers"`
	ReviewTeacherSummary   ReviewTeacherSummary    `json:"review_teacher_summary"`
	Statistics             Statistics              `json:"statistics"`
	BlockingIssues         []BlockingIssue         `json:"blocking_issues"`
	ActiveExams            []ActiveExam            `json:"active_exams"`
	RecentActivities       []RecentActivity        `json:"recent_activities"`
	Warnings               []string                `json:"warnings"`
	// 阅卷任务来源失败时为 nil，序列化为 null，告警沿用“阅卷待办暂时无法统计”。
	GradingProgress *GradingProgress `json:"grading_progress"`
	// 没有 appeal:manage 时为 nil 并省略且不告警；来源缺失或读取失败时也省略，但追加告警。
	AppealStatistics *AppealStatistics `json:"appeal_statistics,omitempty"`
}
