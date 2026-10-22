package review

import (
	"context"
	"sort"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/segment"
)

// GradingGroupTaskRef 仅引用同答卷、同批阅区、同轮次且分配给本人的任务；成绩仍逐题提交。
type GradingGroupTaskRef struct {
	ID         string `json:"id"`
	QuestionID string `json:"question_id"`
	QuestionNo string `json:"question_no"`
	Status     string `json:"status"`
	Revision   int64  `json:"revision"`
}

type gradingGroupTaskReader interface {
	ListGradingGroupTasks(context.Context, string, string, string) ([]ReviewTask, error)
}

// 整区导航是可选补充；任务或资产读取失败时省略未核实的引用，不影响当前单题上下文。
func (h *Handler) gradingGroupTaskRefs(ctx context.Context, tenantID, reviewerID string, anchor ReviewTask, group *segment.GradingGroupCrop) []GradingGroupTaskRef {
	if anchor.TenantID != tenantID || anchor.AssignedTo != reviewerID || group == nil {
		return nil
	}
	reader, ok := h.store.(gradingGroupTaskReader)
	if !ok {
		return nil
	}
	images, ok := h.segmentImage.(segment.GradingGroupReader)
	if !ok {
		return nil
	}
	tasks, err := reader.ListGradingGroupTasks(ctx, tenantID, anchor.ID, reviewerID)
	if err != nil {
		return nil
	}
	allowed := map[string]bool{}
	for _, id := range group.QuestionIDs {
		allowed[id] = true
	}
	refs := []GradingGroupTaskRef{}
	seen := map[string]bool{}
	for _, task := range tasks {
		// 即使存储已按本人和轮次筛选，这里仍核对每个引用；整区共享不能扩大单题任务的访问范围。
		if task.TenantID != tenantID || task.AssignedTo != reviewerID || task.SubmissionID != anchor.SubmissionID || task.ExamID != anchor.ExamID || task.GradeRound != anchor.GradeRound || task.Status == "cancelled" || !allowed[task.QuestionID] || seen[task.QuestionID] {
			continue
		}
		other, err := images.GetGradingGroup(ctx, tenantID, task.AnswerSegmentID)
		// 同名组可能跨答卷复用；资产和哈希必须相同，不能只按group_id聚合学生或不同配准版本。
		if err != nil || other == nil || other.ID != group.ID || other.FileAssetID != group.FileAssetID || other.SHA256 != group.SHA256 {
			continue
		}
		seen[task.QuestionID] = true
		refs = append(refs, GradingGroupTaskRef{ID: task.ID, QuestionID: task.QuestionID, QuestionNo: task.QuestionNo, Status: task.Status, Revision: task.Revision})
	}
	sort.SliceStable(refs, func(i, j int) bool {
		if compared := compareGradingGroupQuestionNo(refs[i].QuestionNo, refs[j].QuestionNo); compared != 0 {
			return compared < 0
		}
		return refs[i].ID < refs[j].ID
	})
	return refs
}

func compareGradingGroupQuestionNo(first, second string) int {
	// 题号中的每段数字按数值比较，含小问时也保持 2(2) 在 2(10) 前；不用定长整数避免溢出。
	i, j := 0, 0
	for i < len(first) && j < len(second) {
		if first[i] >= '0' && first[i] <= '9' && second[j] >= '0' && second[j] <= '9' {
			left, right := i, j
			for i < len(first) && first[i] >= '0' && first[i] <= '9' {
				i++
			}
			for j < len(second) && second[j] >= '0' && second[j] <= '9' {
				j++
			}
			a, b := strings.TrimLeft(first[left:i], "0"), strings.TrimLeft(second[right:j], "0")
			if len(a) < len(b) {
				return -1
			}
			if len(a) > len(b) {
				return 1
			}
			if compared := strings.Compare(a, b); compared != 0 {
				return compared
			}
			continue
		}
		if first[i] < second[j] {
			return -1
		}
		if first[i] > second[j] {
			return 1
		}
		i++
		j++
	}
	if i < len(first) {
		return 1
	}
	if j < len(second) {
		return -1
	}
	return strings.Compare(first, second)
}

// ListGradingGroupTasks 只读取同配准整区、同评分运行及同轮次的本人任务，避免混入重新配准或重新评分的旧记录。
func (s *PostgresStore) ListGradingGroupTasks(ctx context.Context, tenantID, taskID, reviewerID string) ([]ReviewTask, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT rt.id::text
FROM review_task anchor
JOIN answer_segment origin ON origin.tenant_id=anchor.tenant_id AND origin.id=anchor.answer_segment_id AND origin.deleted_at IS NULL
JOIN answer_segment member ON member.tenant_id=origin.tenant_id AND member.submission_page_id=origin.submission_page_id
 AND member.registration_run_id IS NOT DISTINCT FROM origin.registration_run_id AND member.deleted_at IS NULL
 AND member.normalized_bbox->'grading_group'->>'id'=origin.normalized_bbox->'grading_group'->>'id'
 AND member.normalized_bbox->'grading_group'->>'file_asset_id'=origin.normalized_bbox->'grading_group'->>'file_asset_id'
 AND member.normalized_bbox->'grading_group'->>'sha256'=origin.normalized_bbox->'grading_group'->>'sha256'
JOIN review_task rt ON rt.tenant_id=member.tenant_id AND rt.answer_segment_id=member.id
 AND rt.scoring_run_id IS NOT DISTINCT FROM anchor.scoring_run_id AND rt.grade_round=anchor.grade_round
 AND rt.submission_id=anchor.submission_id AND rt.exam_id=anchor.exam_id
WHERE anchor.tenant_id=$1::uuid AND anchor.id::text=$2 AND anchor.assigned_to=$3::uuid AND anchor.deleted_at IS NULL
 AND rt.assigned_to=$3::uuid AND rt.deleted_at IS NULL AND rt.status NOT IN ('cancelled')
ORDER BY rt.question_no,rt.id LIMIT 500`, tenantID, taskID, reviewerID)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	tasks := []ReviewTask{}
	for _, id := range ids {
		task, err := s.GetTask(ctx, tenantID, id)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, nil
}

// 内存任务没有配准和整区资产索引；这里限制答卷、考试、教师和轮次，Handler 再逐项核对资产。
func (s *MemoryStore) ListGradingGroupTasks(_ context.Context, tenantID, taskID, reviewerID string) ([]ReviewTask, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	anchor, ok := s.tasks[key(tenantID, taskID)]
	if !ok || anchor.AssignedTo != reviewerID {
		return nil, ErrNotFound
	}
	tasks := []ReviewTask{}
	for _, task := range s.tasks {
		if task.TenantID == tenantID && task.AssignedTo == reviewerID && task.SubmissionID == anchor.SubmissionID && task.ExamID == anchor.ExamID && task.GradeRound == anchor.GradeRound && task.Status != "cancelled" {
			tasks = append(tasks, cloneTask(task))
		}
	}
	return tasks, nil
}
