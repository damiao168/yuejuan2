package scorerelease

import (
	"context"
	"math"
	"strings"
)

func (s *PostgresStore) Create(ctx context.Context, tenantID, examID, actorID string, input CreateInput) (Release, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Release{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockExam(ctx, tx, tenantID, examID); err != nil {
		return Release{}, err
	}
	if release, found, err := s.findIdempotentTx(ctx, tx, tenantID, examID, input.IdempotencyKey); err != nil {
		return Release{}, err
	} else if found {
		if err := tx.Commit(); err != nil {
			return Release{}, err
		}
		return release, nil
	}
	if s.gradePreparer != nil {
		ready, err := s.gradePreparer.PrepareForReleaseTx(ctx, tx, tenantID, examID, actorID)
		if err != nil {
			return Release{}, err
		}
		if !ready {
			return Release{}, ErrGateBlocked
		}
	}
	facts, sourceReleaseID, err := s.ordinaryFactsTx(ctx, tx, tenantID, examID)
	if err != nil {
		return Release{}, err
	}
	if len(facts) == 0 {
		return Release{}, ErrNotFound
	}
	version, err := s.nextVersionTx(ctx, tx, tenantID, examID)
	if err != nil {
		return Release{}, err
	}
	release, err := s.insertReleaseTx(ctx, tx, Release{
		TenantID: tenantID, ExamID: examID, CreatedBy: actorID, Version: version,
		Source: input.Source, Reason: input.Reason, IdempotencyKey: input.IdempotencyKey,
		SourceReleaseID:  sourceReleaseID,
		VisibilityPolicy: input.VisibilityPolicy, AppealWindow: input.AppealWindow,
	})
	if err != nil {
		return Release{}, err
	}
	if err := s.insertFactsTx(ctx, tx, tenantID, release.ID, facts); err != nil {
		return Release{}, err
	}
	if err := tx.Commit(); err != nil {
		return Release{}, err
	}
	return release, nil
}

func (s *PostgresStore) CreateRollback(ctx context.Context, tenantID, examID, actorID string, input RollbackInput) (Release, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Release{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockExam(ctx, tx, tenantID, examID); err != nil {
		return Release{}, err
	}
	if release, found, err := s.findIdempotentTx(ctx, tx, tenantID, examID, input.IdempotencyKey); err != nil {
		return Release{}, err
	} else if found {
		if err := tx.Commit(); err != nil {
			return Release{}, err
		}
		return release, nil
	}
	source, err := s.releaseTx(ctx, tx, tenantID, input.SourceReleaseID, false)
	if err != nil {
		return Release{}, err
	}
	if source.ExamID != examID || source.Status != StatusPublished {
		return Release{}, ErrInvalidTransition
	}
	// 历史发布可能携带高分答卷分享策略。匿名资源不可用时，不能将该策略传播到新发布。
	source.VisibilityPolicy.ShowHighScorePaper = false
	version, err := s.nextVersionTx(ctx, tx, tenantID, examID)
	if err != nil {
		return Release{}, err
	}
	release, err := s.insertReleaseTx(ctx, tx, Release{
		TenantID: tenantID, ExamID: examID, CreatedBy: actorID, Version: version,
		Source: SourceRollback, Reason: input.Reason, IdempotencyKey: input.IdempotencyKey,
		VisibilityPolicy: source.VisibilityPolicy, AppealWindow: source.AppealWindow, SourceReleaseID: source.ID,
	})
	if err != nil {
		return Release{}, err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO score_release_item (tenant_id, release_id, student_id, submission_id, total_score, max_score, status, snapshot_hash)
SELECT tenant_id, $3::uuid, student_id, submission_id, total_score, max_score, status, snapshot_hash
FROM score_release_item WHERE tenant_id = $1 AND release_id = $2::uuid
`, tenantID, source.ID, release.ID); err != nil {
		return Release{}, err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO score_release_question (tenant_id, release_id, submission_id, question_id, question_no, final_grade_id, score, max_score, source_type, source_id, student_explanation)
SELECT tenant_id, $3::uuid, submission_id, question_id, question_no, final_grade_id, score, max_score, source_type, source_id, student_explanation
FROM score_release_question WHERE tenant_id = $1 AND release_id = $2::uuid
`, tenantID, source.ID, release.ID); err != nil {
		return Release{}, err
	}
	if err := tx.Commit(); err != nil {
		return Release{}, err
	}
	return release, nil
}

// CreateFromRegrade 在考试锁内复制已发布来源快照，仅在内存中应用已复核计划的题目替换项，再写入新草稿。
// 它不连接当前 final_grade 或 submission_grade，避免后续可变活动混入发布。
func (s *PostgresStore) CreateFromRegrade(ctx context.Context, tenantID, examID, actorID string, input CreateRegradeInput) (Release, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Release{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := lockExam(ctx, tx, tenantID, examID); err != nil {
		return Release{}, err
	}
	if release, found, err := s.findIdempotentTx(ctx, tx, tenantID, examID, input.IdempotencyKey); err != nil {
		return Release{}, err
	} else if found {
		if err := tx.Commit(); err != nil {
			return Release{}, err
		}
		return release, nil
	}
	source, err := s.releaseTx(ctx, tx, tenantID, input.SourceReleaseID, false)
	if err != nil {
		return Release{}, err
	}
	if source.ExamID != examID || source.Status != StatusPublished {
		return Release{}, ErrInvalidTransition
	}
	source.VisibilityPolicy.ShowHighScorePaper = false
	facts, err := s.releaseFactsTx(ctx, tx, tenantID, source.ID)
	if err != nil {
		return Release{}, err
	}
	if len(facts) == 0 || !applyRegradeChanges(facts, input) {
		return Release{}, ErrInvalidInput
	}
	if strings.HasPrefix(input.IdempotencyKey, "regrade-job:") {
		// 考试锁覆盖旧随机键草稿回认与新建；保留旧键，不破坏历史客户端的幂等重试。
		rows, lookupErr := tx.QueryContext(ctx, releaseColumns+` FROM score_release WHERE tenant_id=$1 AND exam_id=$2 AND source=$3 AND source_release_id=$4 AND status IN ('draft','published') ORDER BY version`, tenantID, examID, SourceRegrade, source.ID)
		if lookupErr != nil {
			return Release{}, lookupErr
		}
		candidates := []Release{}
		for rows.Next() {
			candidate, scanErr := scanRelease(rows)
			if scanErr != nil {
				rows.Close()
				return Release{}, scanErr
			}
			candidates = append(candidates, candidate)
		}
		lookupErr = rows.Err()
		rows.Close()
		if lookupErr != nil {
			return Release{}, lookupErr
		}
		var recovered *Release
		for _, candidate := range candidates {
			actual, readErr := s.releaseFactsTx(ctx, tx, tenantID, candidate.ID)
			if readErr != nil {
				return Release{}, readErr
			}
			if sameRegradeFacts(facts, actual) {
				if recovered != nil {
					return Release{}, ErrInvalidTransition
				}
				copy := candidate
				recovered = &copy
			}
		}
		if recovered != nil {
			if err := tx.Commit(); err != nil {
				return Release{}, err
			}
			return *recovered, nil
		}
	}
	version, err := s.nextVersionTx(ctx, tx, tenantID, examID)
	if err != nil {
		return Release{}, err
	}
	release, err := s.insertReleaseTx(ctx, tx, Release{
		TenantID: tenantID, ExamID: examID, CreatedBy: actorID, Version: version,
		Source: SourceRegrade, Reason: input.Reason, IdempotencyKey: input.IdempotencyKey,
		VisibilityPolicy: source.VisibilityPolicy, AppealWindow: source.AppealWindow, SourceReleaseID: source.ID,
	})
	if err != nil {
		return Release{}, err
	}
	if err := s.insertFactsTx(ctx, tx, tenantID, release.ID, facts); err != nil {
		return Release{}, err
	}
	if err := tx.Commit(); err != nil {
		return Release{}, err
	}
	return release, nil
}

func applyRegradeChanges(facts []SubmissionFact, input CreateRegradeInput) bool {
	changes := make(map[string]RegradeChange, len(input.Changes))
	for _, change := range input.Changes {
		changes[change.SubmissionID+"\x00"+change.QuestionID] = change
	}
	matched := 0
	for factIndex := range facts {
		fact := &facts[factIndex]
		for questionIndex := range fact.Questions {
			question := &fact.Questions[questionIndex]
			change, found := changes[fact.SubmissionID+"\x00"+question.QuestionID]
			if !found {
				continue
			}
			if question.QuestionID != input.QuestionID || math.Abs(question.MaxScore-change.MaxScore) > 0.000001 {
				return false
			}
			question.Score, question.SourceType, question.SourceID = change.Score, "single_review", change.ReviewedGradeID
			// 已复核重评计划没有获批的学生反馈。来源发布的说明属于旧分数。
			question.Explanation.Feedback = ""
			question.Explanation.RubricSummary = nil
			matched++
		}
		fact.TotalScore = 0
		for _, question := range fact.Questions {
			fact.TotalScore += question.Score
		}
		if fact.TotalScore > fact.MaxScore+0.000001 {
			return false
		}
	}
	return matched == len(changes)
}
