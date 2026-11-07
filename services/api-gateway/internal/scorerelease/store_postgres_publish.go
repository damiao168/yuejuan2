package scorerelease

// 发布仍在同一事务内锁定发布和考试、校验门禁、刷新草稿并切换当前指针；阻断时的证据提交分支保持原顺序。
import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/score/autoprep"
	"encoding/json"
	"errors"
)

func (s *PostgresStore) Publish(ctx context.Context, tenantID, id, actorID string) (Release, error) {
	return s.publish(ctx, tenantID, id, actorID, nil)
}

func (s *PostgresStore) PublishGuarded(ctx context.Context, tenantID, id, actorID string, guard PublishGuard) (Release, error) {
	return s.publish(ctx, tenantID, id, actorID, guard)
}

func (s *PostgresStore) publish(ctx context.Context, tenantID, id, actorID string, guard PublishGuard) (Release, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Release{}, err
	}
	defer func() { _ = tx.Rollback() }()
	release, err := s.releaseTx(ctx, tx, tenantID, id, true)
	if err != nil {
		return Release{}, err
	}
	if err := lockExam(ctx, tx, tenantID, release.ExamID); err != nil {
		return Release{}, err
	}
	if err := autoprep.LockCollection(ctx, tx, tenantID, release.ExamID); err != nil {
		return Release{}, err
	}
	if release.Status == StatusPublished {
		if err := tx.Commit(); err != nil {
			return Release{}, err
		}
		return release, nil
	}
	if release.Status != StatusDraft {
		return Release{}, ErrInvalidTransition
	}
	// 取得考试咨询锁后才刷新跨服务聚合。成绩写入器使用同一锁，
	// 因此门禁与发布不会和正常成绩最终确认转换发生竞争。
	dashboardIssues, err := s.dashboardIssues(ctx, tenantID, release.ExamID)
	if err != nil {
		return Release{}, err
	}
	gate, err := s.gateTx(ctx, tx, tenantID, release.ExamID, release.ID, dashboardIssues, false)
	if err != nil {
		return Release{}, err
	}
	if guard != nil {
		if err := guard(ctx, tx, release, gate); err != nil {
			if errors.Is(err, ErrGateBlocked) {
				// 门禁拒绝时仍保留该临界区中读取的阻断证据，尚未写入任何公开成绩。
				if commitErr := tx.Commit(); commitErr != nil {
					return Release{}, commitErr
				}
			}
			return Release{}, err
		}
	}
	if !gate.Passed {
		return Release{}, ErrGateBlocked
	}
	gateJSON, err := json.Marshal(gate)
	if err != nil {
		return Release{}, err
	}
	var supersedes sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT release_id::text FROM score_release_current WHERE tenant_id = $1 AND exam_id = $2::uuid FOR UPDATE`, tenantID, release.ExamID).Scan(&supersedes)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Release{}, err
	}
	if release.Source == SourceRegrade && release.SourceReleaseID != "" && release.SourceReleaseID != supersedes.String {
		base, err := s.releaseFactsTx(ctx, tx, tenantID, release.SourceReleaseID)
		if err != nil {
			return Release{}, err
		}
		draft, err := s.releaseFactsTx(ctx, tx, tenantID, release.ID)
		if err != nil {
			return Release{}, err
		}
		current, err := s.releaseFactsTx(ctx, tx, tenantID, supersedes.String)
		if err != nil {
			return Release{}, err
		}
		facts, ok := rebaseRegradeFacts(base, draft, current)
		if !ok {
			return Release{}, ErrStaleSource
		}
		// 仅草稿允许重建快照；保留原重评来源，申诉仍可按原任务结案，期间正式版本保持不可变。
		if _, err := tx.ExecContext(ctx, `DELETE FROM score_release_question WHERE tenant_id=$1 AND release_id=$2::uuid`, tenantID, release.ID); err != nil {
			return Release{}, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM score_release_item WHERE tenant_id=$1 AND release_id=$2::uuid`, tenantID, release.ID); err != nil {
			return Release{}, err
		}
		if err := s.insertFactsTx(ctx, tx, tenantID, release.ID, facts); err != nil {
			return Release{}, err
		}
	} else if release.Source != SourceRollback && !(release.Source == SourceRegrade && release.SourceReleaseID != "") {
		draft, err := s.releaseFactsTx(ctx, tx, tenantID, release.ID)
		if err != nil {
			return Release{}, err
		}
		preserved, err := s.preservesRegradeScoresTx(ctx, tx, tenantID, release.ExamID, draft)
		if err != nil {
			return Release{}, err
		}
		if !preserved {
			facts, sourceReleaseID, err := s.ordinaryFactsTx(ctx, tx, tenantID, release.ExamID)
			if err != nil {
				return Release{}, err
			}
			// 草稿可随当前正式版本刷新；只改草稿，不回写终评或已发布快照。来源记录供发布审计追溯。
			if _, err := tx.ExecContext(ctx, `DELETE FROM score_release_question WHERE tenant_id=$1 AND release_id=$2::uuid`, tenantID, release.ID); err != nil {
				return Release{}, err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM score_release_item WHERE tenant_id=$1 AND release_id=$2::uuid`, tenantID, release.ID); err != nil {
				return Release{}, err
			}
			if err := s.insertFactsTx(ctx, tx, tenantID, release.ID, facts); err != nil {
				return Release{}, err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE score_release SET source_release_id=NULLIF($3,'')::uuid WHERE tenant_id=$1 AND id=$2::uuid AND status='draft'`, tenantID, release.ID, sourceReleaseID); err != nil {
				return Release{}, err
			}
			release.SourceReleaseID = sourceReleaseID
		}
	}
	// 草稿分数刷新可能改变入选范围；以最终将发布的快照校验匿名页，候选不一致则整笔发布回滚。
	if release.VisibilityPolicy.ShowHighScorePaper {
		ready, err := s.anonymousPaperReadyTx(ctx, tx, tenantID, release.ID)
		if err != nil {
			return Release{}, err
		}
		if !ready {
			return Release{}, ErrAnonymousPaperUnavailable
		}
	}
	now := s.now().UTC()
	if _, err := tx.ExecContext(ctx, `
UPDATE score_release
SET status = 'published', published_by = $3::uuid, published_at = $4, gate_snapshot = $5::jsonb, supersedes_release_id = NULLIF($6, '')::uuid
WHERE tenant_id = $1 AND id = $2::uuid AND status = 'draft'
`, tenantID, release.ID, actorID, now, string(gateJSON), supersedes.String); err != nil {
		return Release{}, err
	}
	if _, err := tx.ExecContext(ctx, `
INSERT INTO score_release_current (tenant_id, exam_id, release_id, updated_at)
VALUES ($1, $2::uuid, $3::uuid, $4)
ON CONFLICT (tenant_id, exam_id) DO UPDATE SET release_id = EXCLUDED.release_id, updated_at = EXCLUDED.updated_at
`, tenantID, release.ExamID, release.ID, now); err != nil {
		return Release{}, err
	}
	if err := tx.Commit(); err != nil {
		return Release{}, err
	}
	release.Status, release.PublishedBy, release.PublishedAt, release.GateSnapshot, release.SupersedesReleaseID = StatusPublished, actorID, &now, gate, supersedes.String
	return release, nil
}
