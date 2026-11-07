package modelgovernance

import (
	"context"
	"database/sql"
	"errors"
	"github.com/jackc/pgx/v5/pgconn"
	"strings"
	"time"
)

func (s *PostgresStore) ListModelApprovals(ctx context.Context, tenantID string) ([]ModelApproval, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, tenant_id::text, evaluation_run_id::text,
       evaluation_candidate_id::text, coalesce(deployment_id::text, ''),
       coalesce(managed_model_api_config_id::text, ''), provider_key,
       coalesce(deployment_key, ''), model_name, model_version, prompt_version, rubric_version,
       dataset_reference, dataset_sha256, authorization_reference,
       subject, grade, question_type, modality, manual_review_rate,
       decision_reference, expires_at, revoked_at, revision, created_at
FROM model_approval
WHERE tenant_id = $1
ORDER BY created_at DESC, id
`, tenantID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ModelApproval{}
	for rows.Next() {
		item, scanErr := scanModelApproval(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

// 使用事务和 advisory lock 串行化同一租户/模型范围的审批，避免并发请求产生两个有效审批。
func (s *PostgresStore) CreateModelApproval(
	ctx context.Context,
	tenantID string,
	actorID string,
	input ModelApprovalInput,
) (ModelApproval, error) {
	now := time.Now().UTC()
	if ValidateModelApprovalInput(input, now) != nil {
		return ModelApproval{}, ErrInvalidPromotion
	}
	run, err := s.evaluationRunWithCandidates(ctx, tenantID, input.EvaluationRunID)
	if err != nil {
		return ModelApproval{}, err
	}
	if run.Status != EvaluationStatusCompleted ||
		run.EvidenceClass != EvaluationEvidenceAuthorizedFrozenSet {
		return ModelApproval{}, ErrInvalidPromotion
	}
	var candidate EvaluationCandidate
	found := false
	for _, item := range run.Candidates {
		if (input.ModelConfigID != "" && item.ModelConfigID == input.ModelConfigID) ||
			(input.DeploymentID != "" && item.DeploymentID == input.DeploymentID) {
			candidate = item
			found = true
			break
		}
	}
	if !found {
		return ModelApproval{}, ErrNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ModelApproval{}, err
	}
	defer tx.Rollback()
	lockKey := strings.Join([]string{
		tenantID, candidate.ModelConfigID, candidate.DeploymentID, candidate.ModelVersion,
		candidate.PromptVersion, candidate.RubricVersion,
		run.Subject, run.Grade, run.QuestionType, run.Modality,
	}, "\x1f")
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return ModelApproval{}, err
	}
	var duplicate bool
	if err := tx.QueryRowContext(ctx, `
SELECT EXISTS (
  SELECT 1
  FROM model_approval
  WHERE tenant_id = $1
    AND ((NULLIF($2, '') IS NOT NULL AND managed_model_api_config_id::text = $2)
         OR (NULLIF($2, '') IS NULL AND deployment_id::text = $3))
    AND model_version = $4
    AND prompt_version = $5
    AND rubric_version = $6
    AND subject = $7
    AND grade = $8
    AND question_type = $9
    AND modality = $10
    AND revoked_at IS NULL
    AND expires_at > now()
)
`, tenantID, candidate.ModelConfigID, candidate.DeploymentID, candidate.ModelVersion,
		candidate.PromptVersion, candidate.RubricVersion,
		run.Subject, run.Grade, run.QuestionType, run.Modality).Scan(&duplicate); err != nil {
		return ModelApproval{}, err
	}
	if duplicate {
		return ModelApproval{}, ErrConflict
	}
	row := tx.QueryRowContext(ctx, `
INSERT INTO model_approval (
  tenant_id, evaluation_run_id, evaluation_candidate_id, deployment_id,
  managed_model_api_config_id, provider_key, deployment_key, model_name,
  model_version, prompt_version, rubric_version,
  dataset_reference, dataset_sha256, authorization_reference,
  subject, grade, question_type, modality,
  manual_review_rate, decision_reference, expires_at, created_by
)
VALUES (
  $1, $2, $3, NULLIF($4, '')::uuid,
  NULLIF($5, '')::uuid, $6, NULLIF($7, ''), $8,
  $9, $10, $11,
  $12, $13, $14,
  $15, $16, $17, $18,
  $19, $20, $21, NULLIF($22, '')::uuid
)
RETURNING id::text, tenant_id::text, evaluation_run_id::text,
          evaluation_candidate_id::text, coalesce(deployment_id::text, ''),
          coalesce(managed_model_api_config_id::text, ''), provider_key,
          coalesce(deployment_key, ''), model_name, model_version, prompt_version, rubric_version,
          dataset_reference, dataset_sha256, authorization_reference,
          subject, grade, question_type, modality, manual_review_rate,
          decision_reference, expires_at, revoked_at, revision, created_at
`, tenantID, run.ID, candidate.ID, candidate.DeploymentID,
		candidate.ModelConfigID, candidate.ProviderKey, candidate.DeploymentKey,
		candidate.ModelName, candidate.ModelVersion,
		candidate.PromptVersion, candidate.RubricVersion,
		run.DatasetReference, run.DatasetSHA256, run.AuthorizationRef,
		run.Subject, run.Grade, run.QuestionType, run.Modality,
		input.ManualReviewRate, strings.TrimSpace(input.DecisionReference),
		input.ExpiresAt.UTC(), actorID)
	item, err := scanModelApproval(row)
	if err != nil {
		return ModelApproval{}, mapModelApprovalStoreError(err)
	}
	if err := tx.Commit(); err != nil {
		return ModelApproval{}, mapModelApprovalStoreError(err)
	}
	return item, nil
}

func (s *PostgresStore) RevokeModelApproval(
	ctx context.Context,
	tenantID string,
	actorID string,
	id string,
	input ModelApprovalRevokeInput,
) (ModelApproval, error) {
	if strings.TrimSpace(input.Reason) == "" || input.ExpectedRevision < 1 {
		return ModelApproval{}, ErrInvalidPromotion
	}
	row := s.db.QueryRowContext(ctx, `
UPDATE model_approval
SET revoked_at = now(),
    revoked_by = NULLIF($3, '')::uuid,
    revocation_reason = $4,
    revision = revision + 1
WHERE tenant_id = $1
  AND id::text = $2
  AND revoked_at IS NULL
  AND revision = $5
RETURNING id::text, tenant_id::text, evaluation_run_id::text,
          evaluation_candidate_id::text, coalesce(deployment_id::text, ''),
          coalesce(managed_model_api_config_id::text, ''), provider_key,
          coalesce(deployment_key, ''), model_name, model_version, prompt_version, rubric_version,
          dataset_reference, dataset_sha256, authorization_reference,
          subject, grade, question_type, modality, manual_review_rate,
          decision_reference, expires_at, revoked_at, revision, created_at
`, tenantID, id, actorID, strings.TrimSpace(input.Reason), input.ExpectedRevision)
	item, err := scanModelApproval(row)
	if errors.Is(err, sql.ErrNoRows) {
		var currentRevision int64
		var revokedAt sql.NullTime
		checkErr := s.db.QueryRowContext(ctx,
			`SELECT revision, revoked_at FROM model_approval WHERE tenant_id = $1 AND id::text = $2`,
			tenantID, id,
		).Scan(&currentRevision, &revokedAt)
		if errors.Is(checkErr, sql.ErrNoRows) {
			return ModelApproval{}, ErrNotFound
		}
		if checkErr != nil {
			return ModelApproval{}, checkErr
		}
		if currentRevision != input.ExpectedRevision {
			return ModelApproval{}, ErrRevisionConflict
		}
		if revokedAt.Valid {
			return ModelApproval{}, ErrConflict
		}
		return ModelApproval{}, ErrConflict
	}
	if err != nil {
		return ModelApproval{}, mapModelApprovalStoreError(err)
	}
	return item, nil
}

func scanModelApproval(row rowScanner) (ModelApproval, error) {
	var item ModelApproval
	if err := row.Scan(
		&item.ID, &item.TenantID, &item.EvaluationRunID,
		&item.EvaluationCandidateID, &item.DeploymentID,
		&item.ModelConfigID, &item.ProviderKey, &item.DeploymentKey,
		&item.ModelName, &item.ModelVersion,
		&item.PromptVersion, &item.RubricVersion,
		&item.DatasetReference, &item.DatasetSHA256, &item.AuthorizationRef,
		&item.Subject, &item.Grade, &item.QuestionType, &item.Modality,
		&item.ManualReviewRate, &item.DecisionReference,
		&item.ExpiresAt, &item.RevokedAt, &item.Revision, &item.CreatedAt,
	); err != nil {
		return ModelApproval{}, err
	}
	return item, nil
}

func mapModelApprovalStoreError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return ErrConflict
		case "23514", "23503", "22P02":
			return ErrInvalidPromotion
		}
	}
	return err
}
