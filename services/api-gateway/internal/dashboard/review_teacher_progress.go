package dashboard

import (
	"context"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

const reviewTeacherOnlineWindowSeconds = 300

type ReviewTeacherProgress struct {
	UserID           string     `json:"user_id"`
	Username         string     `json:"username"`
	DisplayName      string     `json:"display_name"`
	TeachingSubjects []string   `json:"teaching_subjects"`
	AssignedCount    int        `json:"assigned_count"`
	InProgressCount  int        `json:"in_progress_count"`
	ReturnedCount    int        `json:"returned_count"`
	SubmittedCount   int        `json:"submitted_count"`
	RemainingCount   int        `json:"remaining_count"`
	IsOnline         bool       `json:"is_online"`
	LastActiveAt     *time.Time `json:"last_active_at,omitempty"`
}

type ReviewTeacherSummary struct {
	TeacherCount        int    `json:"teacher_count"`
	OnlineCount         int    `json:"online_count"`
	OnlineWindowSeconds int    `json:"online_window_seconds"`
	TaskCountingNote    string `json:"task_counting_note"`
	OnlineNote          string `json:"online_note"`
}

type ReviewTeacherProgressStore interface {
	DashboardReviewTeacherProgress(context.Context, string, auth.AccessScope, time.Time) ([]ReviewTeacherProgress, error)
}

func initialReviewTeacherSummary() ReviewTeacherSummary {
	return ReviewTeacherSummary{
		OnlineWindowSeconds: reviewTeacherOnlineWindowSeconds,
		TaskCountingNote:    "按未发布、未归档考试的小题阅卷任务计数；双评按每位教师分别统计，取消任务不计。",
		OnlineNote:          "近5分钟内使用系统且登录仍有效的阅卷教师视为在线；退出登录或账号停用后不计入。",
	}
}

func (s *Service) applyReviewTeacherProgress(ctx context.Context, user auth.User, scope auth.AccessScope, result *Summary) {
	// 同校并不表示可以查看同事进度；只有学校管理员持有学校管理范围时，才读取人员和在线投影。
	if !auth.HasRole(user, "school_admin") || !scope.IsSchoolWide() || scope.TenantWide || scope.IsPlatform {
		return
	}
	store, ok := s.deps.Organizations.(ReviewTeacherProgressStore)
	if !ok {
		result.Warnings = append(result.Warnings, "教师阅卷进度暂时无法加载")
		return
	}
	teachers, err := store.DashboardReviewTeacherProgress(ctx, user.TenantID, scope, result.UpdatedAt)
	if err != nil {
		result.Warnings = append(result.Warnings, "教师阅卷进度暂时无法加载")
		return
	}
	result.ReviewTeachers = teachers
	result.ReviewTeacherSummary.TeacherCount = len(teachers)
	for _, teacher := range teachers {
		if teacher.IsOnline {
			result.ReviewTeacherSummary.OnlineCount++
		}
	}
}
