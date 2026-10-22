package subjective

import (
	"context"
	"sort"
)

// CurrentAIFailure 在内存夹具中同样只选择当前答案、题目和量规版本的最新运行。
// BindReviewTaskRun 让夹具复现 review_task.scoring_run_id 的批次边界；必须显式绑定任务，空值表示同步运行。
func (s *MemoryStore) CurrentAIFailure(_ context.Context, tenantID, taskID, segmentID, questionID, snapshotID, rubricVersion string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	boundScoringRunID, taskKnown := s.reviewTaskRuns[key(tenantID, taskID)]
	if !taskKnown || taskID == "" || snapshotID == "" || rubricVersion == "" {
		return "", nil
	}
	contextValue, ok := s.contexts[key(tenantID, segmentID)]
	if !ok || contextValue.Question.ID != questionID || contextValue.AssessmentSnapshot.ID != snapshotID || contextValue.Rubric.Version != rubricVersion {
		return "", nil
	}
	runs := make([]GradingRun, 0)
	for _, run := range s.runs {
		if run.TenantID != tenantID || run.AnswerSegmentID != segmentID || run.QuestionID != questionID || run.RubricVersion != rubricVersion {
			continue
		}
		if boundScoringRunID == "" {
			if run.BatchID != "" {
				continue
			}
		} else {
			batch, ok := s.batchByIDLocked(tenantID, run.BatchID)
			if !ok || batch.ScoringRunID != boundScoringRunID {
				continue
			}
		}
		if contextValue.AnswerVersion != "" && run.AnswerVersion != contextValue.AnswerVersion {
			continue
		}
		if run.ExamQuestionSnapshotID != snapshotID {
			continue
		}
		runs = append(runs, run)
	}
	if len(runs) == 0 {
		return "", nil
	}
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].CreatedAt.Equal(runs[j].CreatedAt) {
			return runs[i].ID > runs[j].ID
		}
		return runs[i].CreatedAt.After(runs[j].CreatedAt)
	})
	// 与数据库实现一样，最新运行证据不再匹配时直接结束，不能再找一条更老的失败来替代。
	if runs[0].MathArtifactID != "" || contextValue.MathEvidence != nil {
		if !contextValue.MathEvidence.bindingMatches(runs[0]) {
			return "", nil
		}
	}
	return teacherVisibleFailureKind(runs[0]), nil
}

func (s *MemoryStore) batchByIDLocked(tenantID, batchID string) (GradingBatch, bool) {
	for _, batch := range s.batches {
		if batch.TenantID == tenantID && batch.ID == batchID {
			return batch, true
		}
	}
	return GradingBatch{}, false
}
