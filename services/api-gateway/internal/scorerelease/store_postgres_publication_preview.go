package scorerelease

import (
	"context"
	"database/sql"
	"errors"
)

// 只读事务用一致的 MVCC 快照读取草稿和当前正式版本。正式发布继续在考试锁内重算，不依赖这里读到的状态。
func (s *PostgresStore) publicationPreview(ctx context.Context, tenantID, id string, mode publicationPreviewMode) (Detail, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return Detail{}, err
	}
	defer tx.Rollback()
	release, err := s.releaseTx(ctx, tx, tenantID, id, false)
	if err != nil {
		return Detail{}, err
	}
	var current string
	if release.Status == StatusDraft {
		err = tx.QueryRowContext(ctx, `SELECT release_id::text FROM score_release_current WHERE tenant_id=$1 AND exam_id=$2::uuid`, tenantID, release.ExamID).Scan(&current)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return Detail{}, err
		}
	}
	facts, err := readReleaseFactsTx(ctx, tx, tenantID, id, true)
	if err != nil {
		return Detail{}, err
	}
	if mode == previewCandidate && release.Status == StatusDraft {
		candidateRelease, candidate, err := s.publicationCandidateTx(ctx, tx, release, current, facts)
		if err != nil {
			return Detail{}, err
		}
		if candidate != nil {
			if candidateRelease.VisibilityPolicy.ShowHighScorePaper {
				return Detail{}, ErrPublicationDraftNeedsRefresh
			}
			facts = candidate
		}
		release = candidateRelease
	}
	detail, err := publicationPreviewDetail(release, current, facts)
	if err != nil {
		return Detail{}, err
	}
	return detail, tx.Commit()
}

// 非空 facts 是已经读出的原草稿。返回候选为 nil 时沿用该快照，无需修改任何成绩行。
func (s *PostgresStore) publicationCandidateTx(ctx context.Context, tx *sql.Tx, release Release, currentID string, facts []SubmissionFact) (Release, []SubmissionFact, error) {
	if release.Source == SourceRollback || (release.Source == SourceRegrade && release.SourceReleaseID != "" && release.SourceReleaseID == currentID) {
		return release, nil, nil
	}
	if facts == nil {
		var err error
		facts, err = s.releaseFactsTx(ctx, tx, release.TenantID, release.ID)
		if err != nil {
			return release, nil, err
		}
	}
	if release.Source == SourceRegrade && release.SourceReleaseID != "" {
		base, err := s.releaseFactsTx(ctx, tx, release.TenantID, release.SourceReleaseID)
		if err != nil {
			return release, nil, err
		}
		current, err := s.releaseFactsTx(ctx, tx, release.TenantID, currentID)
		if err != nil {
			return release, nil, err
		}
		candidate, ok := rebaseRegradeFacts(base, facts, current)
		if !ok {
			return release, nil, ErrStaleSource
		}
		return release, candidate, nil
	}
	preserved, err := s.preservesRegradeScoresTx(ctx, tx, release.TenantID, release.ExamID, facts)
	if err != nil || preserved {
		return release, nil, err
	}
	candidate, sourceReleaseID, err := s.ordinaryFactsTx(ctx, tx, release.TenantID, release.ExamID)
	if err != nil {
		return release, nil, err
	}
	release.SourceReleaseID = sourceReleaseID
	return release, candidate, nil
}
