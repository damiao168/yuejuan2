package dashboard

import (
	"context"
	"sort"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/review"
)

// gradingProgressWindowSeconds 是 recent_done_count 的统计时窗（秒），契约固定为 3600；
// 随 window_seconds 返回，客户端不应自行假设时窗长度。
const gradingProgressWindowSeconds = 3600

type GradingProgress struct {
	WindowSeconds int                       `json:"window_seconds"`
	Questions     []GradingQuestionProgress `json:"questions"`
}

// GradingQuestionProgress 按小题阅卷任务计数，双评时每位教师的任务各计一项；口径与 ActiveExam.ReviewTaskCount 相同。
type GradingQuestionProgress struct {
	ExamID         string `json:"exam_id"`
	QuestionID     string `json:"question_id"`
	QuestionNo     string `json:"question_no"`
	TaskCount      int    `json:"task_count"`
	DoneCount      int    `json:"done_count"`
	RemainingCount int    `json:"remaining_count"`
	// RecentDoneCount：review_task 没有交卷时间列，按已交任务的最后更新时间近似；交卷后再变更的任务按最近一次更新时间计入。
	RecentDoneCount int `json:"recent_done_count"`
}

// ExamRosterCount 中 SubmittedCount 只计冻结名册内已有未删除答卷的学生，不含未匹配或名册外的答卷。
type ExamRosterCount struct {
	RosterCount    int
	SubmittedCount int
}

// ExamRosterCountStore 一次读取多场考试的冻结名册人数。examIDs 必须来自已按访问范围筛选的考试列表；
// 结果中缺少的考试表示尚未冻结名册。
type ExamRosterCountStore interface {
	DashboardExamRosterCounts(ctx context.Context, tenantID string, examIDs []string) (map[string]ExamRosterCount, error)
}

func reviewTaskRemaining(status string) bool {
	return status == "pending" || status == "assigned" || status == "in_progress" || status == "returned"
}

func reviewTaskDone(status string) bool {
	return status == "submitted" || status == "completed"
}

// activeExamIndex 记录 ActiveExams 的行号；只能在最终排序前使用，排序后行号失效。
func activeExamIndex(exams []ActiveExam) map[string]int {
	index := make(map[string]int, len(exams))
	for position, item := range exams {
		index[item.ID] = position
	}
	return index
}

// applyReviewTaskProgress 只统计 ActiveExams 中的考试（未发布、未归档），与教师阅卷进度同一范围；
// 已取消的任务不再需要阅卷，不计入待阅、任务总数或已交数。
func applyReviewTaskProgress(tasks []review.ReviewTask, activeIndex map[string]int, now time.Time, result *Summary) {
	// 来源可用时每场先写 0，与来源失败时的 nil 区分开。
	taskCounts := make([]int, len(result.ActiveExams))
	doneCounts := make([]int, len(result.ActiveExams))
	// 只设下界：数据库时钟略快时，刚交的任务更新时间可能晚于 now，仍应算作近期完成。
	windowStart := now.Add(-gradingProgressWindowSeconds * time.Second)
	type questionKey struct{ examID, questionID string }
	questions := map[questionKey]*GradingQuestionProgress{}
	order := []questionKey{}
	pendingQuestions := 0
	pendingSubmissions := map[string]struct{}{}
	for _, task := range tasks {
		position, active := activeIndex[task.ExamID]
		if !active || task.Status == "cancelled" {
			continue
		}
		key := questionKey{task.ExamID, task.QuestionID}
		progress, seen := questions[key]
		if !seen {
			progress = &GradingQuestionProgress{ExamID: task.ExamID, QuestionID: task.QuestionID, QuestionNo: task.QuestionNo}
			questions[key] = progress
			order = append(order, key)
		}
		progress.TaskCount++
		taskCounts[position]++
		switch {
		case reviewTaskDone(task.Status):
			progress.DoneCount++
			doneCounts[position]++
			if !task.UpdatedAt.Before(windowStart) {
				progress.RecentDoneCount++
			}
		case reviewTaskRemaining(task.Status):
			progress.RemainingCount++
			pendingQuestions++
			pendingSubmissions[task.SubmissionID] = struct{}{}
		}
	}
	for position := range result.ActiveExams {
		result.ActiveExams[position].ReviewTaskCount = intPointer(taskCounts[position])
		result.ActiveExams[position].ReviewDoneCount = intPointer(doneCounts[position])
	}
	result.Statistics.PendingReviewQuestionCount = pendingQuestions
	result.Statistics.PendingReviewSubmissionCount = len(pendingSubmissions)

	list := make([]GradingQuestionProgress, 0, len(order))
	for _, key := range order {
		list = append(list, *questions[key])
	}
	// 剩余多的在前；其余键只用于让同一数据得到稳定顺序。
	sort.Slice(list, func(i, j int) bool {
		if list[i].RemainingCount != list[j].RemainingCount {
			return list[i].RemainingCount > list[j].RemainingCount
		}
		if list[i].ExamID != list[j].ExamID {
			return list[i].ExamID < list[j].ExamID
		}
		if list[i].QuestionNo != list[j].QuestionNo {
			return list[i].QuestionNo < list[j].QuestionNo
		}
		return list[i].QuestionID < list[j].QuestionID
	})
	result.GradingProgress = &GradingProgress{WindowSeconds: gradingProgressWindowSeconds, Questions: list}
}

// applyArbitrationProgress 只计 pending、assigned 的仲裁任务：submitted 表示已裁决，cancelled 无需处理。
// 已发布或已归档考试的任务不再是工作台待办。
func applyArbitrationProgress(tasks []review.ArbitrationTask, activeIndex map[string]int, result *Summary) {
	counts := make([]int, len(result.ActiveExams))
	pending := 0
	submissions := map[string]struct{}{}
	for _, task := range tasks {
		position, active := activeIndex[task.ExamID]
		if !active || (task.Status != "pending" && task.Status != "assigned") {
			continue
		}
		counts[position]++
		pending++
		submissions[task.SubmissionID] = struct{}{}
	}
	for position := range result.ActiveExams {
		result.ActiveExams[position].PendingArbitrationCount = intPointer(counts[position])
	}
	result.Statistics.PendingArbitrationCount = pending
	result.Statistics.PendingArbitrationSubmissionCount = len(submissions)
}

// applyRosterCounts 用一次查询读取全部进行中考试的名册人数。来源缺失或失败时保留 nil（序列化为 null）并告警，
// 不能写成 0：0 专指本场尚未冻结名册。
func (s *Service) applyRosterCounts(ctx context.Context, tenantID string, result *Summary) {
	if len(result.ActiveExams) == 0 {
		return
	}
	store, ok := s.deps.Organizations.(ExamRosterCountStore)
	if !ok {
		result.Warnings = append(result.Warnings, "考试名册人数暂时无法统计")
		return
	}
	examIDs := make([]string, 0, len(result.ActiveExams))
	for _, item := range result.ActiveExams {
		examIDs = append(examIDs, item.ID)
	}
	counts, err := store.DashboardExamRosterCounts(ctx, tenantID, examIDs)
	if err != nil {
		result.Warnings = append(result.Warnings, "考试名册人数暂时无法统计")
		return
	}
	for position := range result.ActiveExams {
		// 读取成功但没有该场记录，表示尚未冻结名册，人数按 0 返回。
		count := counts[result.ActiveExams[position].ID]
		result.ActiveExams[position].RosterCount = intPointer(count.RosterCount)
		result.ActiveExams[position].RosterSubmittedCount = intPointer(count.SubmittedCount)
	}
}

func intPointer(value int) *int {
	return &value
}
