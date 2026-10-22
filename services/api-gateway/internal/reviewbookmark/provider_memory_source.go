package reviewbookmark

import (
	"context"
	"errors"

	"edugrade-enterprise/services/api-gateway/internal/review"
	"edugrade-enterprise/services/api-gateway/internal/segment"
)

type submissionSegmentReader interface {
	ListBySubmission(context.Context, string, string) ([]segment.Segment, error)
}

func (p *MemorySourceProvider) sourceSegment(ctx context.Context, tenantID string, source Source, submissionID string) (segment.SegmentEvidence, error) {
	if p.segments == nil || source.AnswerSegmentID == "" {
		return segment.SegmentEvidence{}, ErrNotFound
	}
	evidence, err := p.segments.GetEvidence(ctx, tenantID, source.AnswerSegmentID)
	if err == nil {
		if evidence.SegmentID != source.AnswerSegmentID || evidence.ExamID != source.ExamID || evidence.QuestionID != source.QuestionID || (submissionID != "" && evidence.SubmissionID != submissionID) {
			return segment.SegmentEvidence{}, ErrNotFound
		}
		return evidence, nil
	}
	if !errors.Is(err, segment.ErrNotFound) {
		return segment.SegmentEvidence{}, err
	}
	lister, ok := p.segments.(submissionSegmentReader)
	if !ok {
		return segment.SegmentEvidence{}, ErrNotFound
	}
	// 内存题块尚不维护裁剪证据；只在原任务与实际题块的租户、考试、题目和提交关系可核对时保留标记。
	// 此回退不生成图片版本，也不会把普通工作台的图片或 OCR 充当已验证证据。
	submissions := map[string]bool{}
	if submissionID != "" {
		submissions[submissionID] = true
	} else if source.SeedTaskID != "" && p.tasks != nil {
		// 暗样本任务只有服务端来源 URL；借同考试同题的原任务定位提交，不继承原任务的权限或意见。
		tasks, e := p.tasks.ListTasks(ctx, tenantID, review.ListFilter{ExamID: source.ExamID, QuestionID: source.QuestionID})
		if e != nil {
			return segment.SegmentEvidence{}, e
		}
		for _, task := range tasks {
			if task.TenantID == tenantID && task.ExamID == source.ExamID && task.QuestionID == source.QuestionID && task.AnswerSegmentID == source.AnswerSegmentID && task.SubmissionID != "" {
				submissions[task.SubmissionID] = true
			}
		}
	}
	var found *segment.SegmentEvidence
	for id := range submissions {
		items, e := lister.ListBySubmission(ctx, tenantID, id)
		if e != nil {
			return segment.SegmentEvidence{}, e
		}
		for _, item := range items {
			if item.TenantID != tenantID || item.ID != source.AnswerSegmentID || item.SubmissionID != id || item.QuestionID != source.QuestionID {
				continue
			}
			if found != nil {
				return segment.SegmentEvidence{}, ErrNotFound
			}
			found = &segment.SegmentEvidence{SegmentID: item.ID, SubmissionID: item.SubmissionID, SubmissionPageID: item.SubmissionPageID, ExamID: source.ExamID, QuestionID: item.QuestionID, QuestionNo: item.QuestionNo}
		}
	}
	if found == nil {
		return segment.SegmentEvidence{}, ErrNotFound
	}
	return *found, nil
}
