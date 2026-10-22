package review

import (
	"context"
	"encoding/json"

	"edugrade-enterprise/services/api-gateway/internal/paper"
)

// BookmarkMaterial 是个人回看所需的窄投影，不包含 AI、终分、草稿或其他阅卷人的意见。
type BookmarkMaterial struct {
	SnapshotID string
	Question   paper.Question
	Rubric     paper.Rubric
	Text       string
	OwnGrade   *HumanGrade
}

// GetBookmarkMaterial 不调用工作台组装器，避免回看时带出后来更新的自动评分结果。
func (s *MemoryStore) GetBookmarkMaterial(_ context.Context, tenantID, taskID, ownerID string) (BookmarkMaterial, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	task, ok := s.tasks[key(tenantID, taskID)]
	if !ok || task.AssignedTo != ownerID {
		return BookmarkMaterial{}, ErrNotFound
	}
	ctx, ok := s.contexts[key(tenantID, task.AnswerSegmentID)]
	// 题块上下文可被重新注入；旧任务不能因此转而读取另一次提交或另一道题。
	if !ok || ctx.ExamID != task.ExamID || ctx.SubmissionID != task.SubmissionID || ctx.Question.ID != task.QuestionID {
		return BookmarkMaterial{}, ErrNotFound
	}
	out := bookmarkMaterial(tenantID, ctx)
	for _, grade := range s.grades[key(tenantID, taskID)] {
		if grade.ReviewerID == ownerID && (out.OwnGrade == nil || grade.CreatedAt.After(out.OwnGrade.CreatedAt)) {
			copy := grade
			copy.Comments, copy.PrivateNote, copy.StudentFeedback, copy.Reason = "", "", "", ""
			copy.RubricSelections = append([]RubricSelection{}, grade.RubricSelections...)
			out.OwnGrade = &copy
		}
	}
	return out, nil
}

// GetBookmarkQuestion 只查内存已经冻结的题目，不以当前题库配置替代缺失的快照。
func (s *MemoryStore) GetBookmarkQuestion(_ context.Context, tenantID, examID, questionID, snapshotID string) (BookmarkMaterial, error) {
	if snapshotID == "" {
		return BookmarkMaterial{}, ErrNotFound
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	for storageKey, ctx := range s.contexts {
		if storageKey != key(tenantID, ctx.AnswerSegmentID) || ctx.ExamID != examID || ctx.Question.ID != questionID || ctx.AssessmentSnapshot.ID != snapshotID {
			continue
		}
		material := bookmarkMaterial(tenantID, ctx)
		if material.SnapshotID == snapshotID {
			return material, nil
		}
	}
	return BookmarkMaterial{}, ErrNotFound
}

func bookmarkMaterial(tenantID string, ctx Context) BookmarkMaterial {
	q := paper.Question{ID: ctx.Question.ID, TenantID: ctx.Question.TenantID, ExamID: ctx.Question.ExamID, QuestionNo: ctx.Question.QuestionNo, Stem: ctx.Question.Stem, Options: append([]string{}, ctx.Question.Options...), Score: ctx.Question.Score}
	rubric := taskFrozenRubric(ctx.AssessmentSnapshot.RubricSnapshot, q.ID, q.Score)
	// 内存环境也复制嵌套评分点，调用方不能修改已经冻结的题目依据。
	raw, _ := json.Marshal(rubric)
	_ = json.Unmarshal(raw, &rubric)
	text := ctx.OCRText
	if text == "" {
		text = ctx.RawAnswer
	}
	// 标记状态和写入无需冻结证据；身份缺失或错配时只省略快照身份，由回看入口拒绝上下文。
	// 不能仅凭同一题号或非空快照 ID，把另一租户、考试或题目的评分依据带入当前任务。
	snapshotID := ""
	snapshot := ctx.AssessmentSnapshot
	if snapshot.ID != "" && snapshot.TenantID == tenantID && snapshot.ExamID == ctx.ExamID && snapshot.QuestionID == q.ID && q.ExamID == ctx.ExamID && (q.TenantID == "" || q.TenantID == tenantID) {
		snapshotID = snapshot.ID
	}
	return BookmarkMaterial{SnapshotID: snapshotID, Question: q, Rubric: rubric, Text: text}
}
