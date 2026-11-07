package modelgovernance

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/jackc/pgx/v5/pgconn"
	"strings"
)

func (s *PostgresStore) ListEvaluationRuns(ctx context.Context, tenantID string, filter EvaluationListFilter) ([]EvaluationRun, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, tenant_id::text, run_key, display_name,
       dataset_reference, dataset_sha256, authorization_reference, evidence_class,
       subject, grade, question_type, modality,
       sample_count, repeat_count, status,
       completed_at, invalidated_at, created_at
FROM model_evaluation_run
WHERE tenant_id = $1
  AND ($3 = '' OR created_at < $2 OR (created_at = $2 AND id::text < $3))
ORDER BY created_at DESC, id::text DESC
LIMIT NULLIF($4, 0)
`, tenantID, filter.CursorCreatedAt, filter.CursorID, filter.Limit)
	if err != nil {
		return nil, err
	}
	runs := []EvaluationRun{}
	runIndex := map[string]int{}
	for rows.Next() {
		item, scanErr := scanEvaluationRun(rows)
		if scanErr != nil {
			rows.Close()
			return nil, scanErr
		}
		runIndex[item.ID] = len(runs)
		runs = append(runs, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	if len(runs) == 0 {
		return runs, nil
	}
	placeholders := make([]string, len(runs))
	args := make([]any, 0, len(runs)+1)
	args = append(args, tenantID)
	for index, run := range runs {
		placeholders[index] = fmt.Sprintf("$%d::uuid", index+2)
		args = append(args, run.ID)
	}
	candidateRows, err := s.db.QueryContext(ctx, `
SELECT id::text, tenant_id::text, run_id::text, coalesce(deployment_id::text, ''),
       coalesce(managed_model_api_config_id::text, ''), provider_key,
       coalesce(deployment_key, ''), model_name, model_version, prompt_version, rubric_version,
       evaluated_samples, teacher_reviewed_samples, teacher_accepted_samples,
       serious_error_samples, evidence_valid_samples,
       repeat_comparisons, stable_repeat_samples,
       p95_latency_ms, total_cost_micros, created_at
FROM model_evaluation_candidate
WHERE tenant_id = $1 AND run_id IN (`+strings.Join(placeholders, ",")+`)
ORDER BY model_name, deployment_key, id
`, args...)
	if err != nil {
		return nil, err
	}
	defer candidateRows.Close()
	for candidateRows.Next() {
		item, scanErr := scanEvaluationCandidate(candidateRows)
		if scanErr != nil {
			return nil, scanErr
		}
		index, ok := runIndex[item.RunID]
		if ok {
			runs[index].Candidates = append(runs[index].Candidates, item)
		}
	}
	if err := candidateRows.Err(); err != nil {
		return nil, err
	}
	return runs, nil
}

func (s *PostgresStore) CreateEvaluationRun(
	ctx context.Context,
	tenantID string,
	actorID string,
	input EvaluationRunInput,
) (EvaluationRun, error) {
	if ValidateEvaluationRunInput(input) != nil {
		return EvaluationRun{}, ErrInvalidEvaluation
	}
	row := s.db.QueryRowContext(ctx, `
INSERT INTO model_evaluation_run (
  tenant_id, run_key, display_name, dataset_reference, dataset_sha256, authorization_reference,
  evidence_class, subject, grade, question_type, modality,
  sample_count, repeat_count, created_by
)
VALUES (
  $1, $2, $3, $4, $5, $6,
  $7, $8, $9, $10, $11,
  $12, $13, NULLIF($14, '')::uuid
)
RETURNING id::text, tenant_id::text, run_key, display_name,
          dataset_reference, dataset_sha256, authorization_reference, evidence_class,
          subject, grade, question_type, modality,
          sample_count, repeat_count, status,
          completed_at, invalidated_at, created_at
`, tenantID, input.Key, strings.TrimSpace(input.DisplayName),
		input.DatasetReference, input.DatasetSHA256, strings.TrimSpace(input.AuthorizationRef),
		input.EvidenceClass,
		strings.TrimSpace(input.Subject), strings.TrimSpace(input.Grade),
		input.QuestionType, input.Modality, input.SampleCount, input.RepeatCount, actorID)
	item, err := scanEvaluationRun(row)
	if err != nil {
		return EvaluationRun{}, mapEvaluationStoreError(err)
	}
	return item, nil
}

func (s *PostgresStore) AddEvaluationCandidate(
	ctx context.Context,
	tenantID string,
	actorID string,
	runID string,
	input EvaluationCandidateInput,
) (EvaluationCandidate, error) {
	run, err := s.evaluationRun(ctx, tenantID, runID)
	if err != nil {
		return EvaluationCandidate{}, err
	}
	if run.Status != EvaluationStatusDraft {
		return EvaluationCandidate{}, ErrConflict
	}
	if ValidateEvaluationCandidateInput(input, run) != nil {
		return EvaluationCandidate{}, ErrInvalidEvaluation
	}
	if input.ModelConfigID != "" {
		return s.addManagedEvaluationCandidate(ctx, tenantID, actorID, runID, input)
	}
	deployments, err := s.ListDeployments(ctx, tenantID)
	if err != nil {
		return EvaluationCandidate{}, err
	}
	var deployment Deployment
	var found bool
	for _, item := range deployments {
		if item.ID == input.DeploymentID {
			deployment = item
			found = true
			break
		}
	}
	if !found || !contains(deployment.Modalities, run.Modality) {
		return EvaluationCandidate{}, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `
INSERT INTO model_evaluation_candidate (
  tenant_id, run_id, deployment_id,
  provider_key, deployment_key, model_name, model_version, prompt_version, rubric_version,
  evaluated_samples, teacher_reviewed_samples, teacher_accepted_samples,
  serious_error_samples, evidence_valid_samples,
  repeat_comparisons, stable_repeat_samples,
  p95_latency_ms, total_cost_micros, created_by
)
SELECT
  $1, evaluation.id, deployment.id,
  provider.provider_key, deployment.deployment_key, deployment.model_name, deployment.model_version, $4, $5,
  $6, $7, $8,
  $9, $10,
  $11, $12,
  $13, $14, NULLIF($15, '')::uuid
FROM model_evaluation_run evaluation
JOIN model_deployment deployment
  ON deployment.tenant_id = evaluation.tenant_id
 AND deployment.id::text = $3
 AND deployment.deleted_at IS NULL
JOIN model_provider provider
  ON provider.tenant_id = deployment.tenant_id
 AND provider.id = deployment.provider_id
 AND provider.deleted_at IS NULL
WHERE evaluation.tenant_id = $1
  AND evaluation.id::text = $2
  AND evaluation.status = 'draft'
RETURNING id::text, tenant_id::text, run_id::text, deployment_id::text,
          coalesce(managed_model_api_config_id::text, ''), provider_key,
          deployment_key, model_name, model_version, prompt_version, rubric_version,
          evaluated_samples, teacher_reviewed_samples, teacher_accepted_samples,
          serious_error_samples, evidence_valid_samples,
          repeat_comparisons, stable_repeat_samples,
          p95_latency_ms, total_cost_micros, created_at
`, tenantID, runID, input.DeploymentID,
		strings.TrimSpace(input.PromptVersion), strings.TrimSpace(input.RubricVersion),
		input.EvaluatedSamples, input.TeacherReviewedSamples, input.TeacherAcceptedSamples,
		input.SeriousErrorSamples, input.EvidenceValidSamples,
		input.RepeatComparisons, input.StableRepeatSamples,
		input.P95LatencyMS, input.TotalCostMicros, actorID)
	item, err := scanEvaluationCandidate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return EvaluationCandidate{}, ErrConflict
	}
	if err != nil {
		return EvaluationCandidate{}, mapEvaluationStoreError(err)
	}
	return item, nil
}

func (s *PostgresStore) addManagedEvaluationCandidate(
	ctx context.Context, tenantID, actorID, runID string, input EvaluationCandidateInput,
) (EvaluationCandidate, error) {
	row := s.db.QueryRowContext(ctx, `
INSERT INTO model_evaluation_candidate (
  tenant_id, run_id, managed_model_api_config_id,
  provider_key, model_name, model_version, prompt_version, rubric_version,
  evaluated_samples, teacher_reviewed_samples, teacher_accepted_samples,
  serious_error_samples, evidence_valid_samples, repeat_comparisons,
  stable_repeat_samples, p95_latency_ms, total_cost_micros, created_by
)
SELECT evaluation.tenant_id, evaluation.id, config.id,
       config.provider_key, config.model_name, config.model_version, $4, $5,
       $6, $7, $8, $9, $10, $11, $12, $13, $14, NULLIF($15, '')::uuid
FROM model_evaluation_run evaluation
JOIN managed_model_api_config config
  ON config.tenant_id = evaluation.tenant_id
 AND config.id::text = $3
 AND config.deleted_at IS NULL
 AND config.status = 'active'
 AND config.last_capability_status = 'success'
 AND config.last_capability_probe_version = 'structured-json-v4'
 AND config.modalities ? evaluation.modality
WHERE evaluation.tenant_id = $1 AND evaluation.id::text = $2
  AND evaluation.status = 'draft'
RETURNING id::text, tenant_id::text, run_id::text, coalesce(deployment_id::text, ''),
          managed_model_api_config_id::text, provider_key,
          coalesce(deployment_key, ''), model_name, model_version, prompt_version, rubric_version,
          evaluated_samples, teacher_reviewed_samples, teacher_accepted_samples,
          serious_error_samples, evidence_valid_samples, repeat_comparisons,
          stable_repeat_samples, p95_latency_ms, total_cost_micros, created_at
`, tenantID, runID, input.ModelConfigID,
		strings.TrimSpace(input.PromptVersion), strings.TrimSpace(input.RubricVersion),
		input.EvaluatedSamples, input.TeacherReviewedSamples, input.TeacherAcceptedSamples,
		input.SeriousErrorSamples, input.EvidenceValidSamples,
		input.RepeatComparisons, input.StableRepeatSamples,
		input.P95LatencyMS, input.TotalCostMicros, actorID)
	item, err := scanEvaluationCandidate(row)
	if errors.Is(err, sql.ErrNoRows) {
		return EvaluationCandidate{}, ErrNotFound
	}
	if err != nil {
		return EvaluationCandidate{}, mapEvaluationStoreError(err)
	}
	return item, nil
}

func (s *PostgresStore) CompleteEvaluationRun(
	ctx context.Context,
	tenantID string,
	actorID string,
	runID string,
	reason string,
) (EvaluationRun, error) {
	if strings.TrimSpace(reason) == "" {
		return EvaluationRun{}, ErrInvalidEvaluation
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return EvaluationRun{}, err
	}
	defer tx.Rollback()
	row := tx.QueryRowContext(ctx, `
UPDATE model_evaluation_run
SET status = 'completed',
    completed_at = now(),
    completed_by = NULLIF($3, '')::uuid
WHERE tenant_id = $1
  AND id::text = $2
  AND status = 'draft'
RETURNING id::text, tenant_id::text, run_key, display_name,
          dataset_reference, dataset_sha256, authorization_reference, evidence_class,
          subject, grade, question_type, modality,
          sample_count, repeat_count, status,
          completed_at, invalidated_at, created_at
`, tenantID, runID, actorID)
	item, err := scanEvaluationRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		if rollbackErr := tx.Rollback(); rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
			return EvaluationRun{}, rollbackErr
		}
		return EvaluationRun{}, s.evaluationTransitionError(ctx, tenantID, runID)
	}
	if err != nil {
		return EvaluationRun{}, mapEvaluationStoreError(err)
	}
	if err := tx.Commit(); err != nil {
		return EvaluationRun{}, mapEvaluationStoreError(err)
	}
	return s.evaluationRunWithCandidates(ctx, tenantID, item.ID)
}

func (s *PostgresStore) InvalidateEvaluationRun(
	ctx context.Context,
	tenantID string,
	actorID string,
	runID string,
	reason string,
) (EvaluationRun, error) {
	if strings.TrimSpace(reason) == "" {
		return EvaluationRun{}, ErrInvalidEvaluation
	}
	row := s.db.QueryRowContext(ctx, `
UPDATE model_evaluation_run
SET status = 'invalidated',
    invalidated_at = now(),
    invalidated_by = NULLIF($3, '')::uuid,
    invalidation_reason = $4
WHERE tenant_id = $1
  AND id::text = $2
  AND status <> 'invalidated'
RETURNING id::text, tenant_id::text, run_key, display_name,
          dataset_reference, dataset_sha256, authorization_reference, evidence_class,
          subject, grade, question_type, modality,
          sample_count, repeat_count, status,
          completed_at, invalidated_at, created_at
`, tenantID, runID, actorID, reason)
	item, err := scanEvaluationRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return EvaluationRun{}, s.evaluationTransitionError(ctx, tenantID, runID)
	}
	if err != nil {
		return EvaluationRun{}, mapEvaluationStoreError(err)
	}
	return s.evaluationRunWithCandidates(ctx, tenantID, item.ID)
}

func (s *PostgresStore) evaluationRun(ctx context.Context, tenantID string, runID string) (EvaluationRun, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id::text, tenant_id::text, run_key, display_name,
       dataset_reference, dataset_sha256, authorization_reference, evidence_class,
       subject, grade, question_type, modality,
       sample_count, repeat_count, status,
       completed_at, invalidated_at, created_at
FROM model_evaluation_run
WHERE tenant_id = $1 AND id::text = $2
`, tenantID, runID)
	item, err := scanEvaluationRun(row)
	if err != nil {
		return EvaluationRun{}, mapEvaluationStoreError(err)
	}
	return item, nil
}

func (s *PostgresStore) evaluationRunWithCandidates(
	ctx context.Context,
	tenantID string,
	runID string,
) (EvaluationRun, error) {
	item, err := s.evaluationRun(ctx, tenantID, runID)
	if err != nil {
		return EvaluationRun{}, err
	}
	candidates, err := s.evaluationCandidates(ctx, tenantID, runID)
	if err != nil {
		return EvaluationRun{}, err
	}
	item.Candidates = candidates
	return item, nil
}

func (s *PostgresStore) evaluationCandidates(ctx context.Context, tenantID string, runID string) ([]EvaluationCandidate, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, tenant_id::text, run_id::text, coalesce(deployment_id::text, ''),
       coalesce(managed_model_api_config_id::text, ''), provider_key,
       coalesce(deployment_key, ''), model_name, model_version, prompt_version, rubric_version,
       evaluated_samples, teacher_reviewed_samples, teacher_accepted_samples,
       serious_error_samples, evidence_valid_samples,
       repeat_comparisons, stable_repeat_samples,
       p95_latency_ms, total_cost_micros, created_at
FROM model_evaluation_candidate
WHERE tenant_id = $1 AND run_id::text = $2
ORDER BY model_name, deployment_key, id
`, tenantID, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []EvaluationCandidate{}
	for rows.Next() {
		item, scanErr := scanEvaluationCandidate(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) evaluationTransitionError(ctx context.Context, tenantID string, runID string) error {
	var status string
	err := s.db.QueryRowContext(ctx, `
SELECT status
FROM model_evaluation_run
WHERE tenant_id = $1 AND id::text = $2
`, tenantID, runID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	return ErrConflict
}

func scanEvaluationRun(row rowScanner) (EvaluationRun, error) {
	var item EvaluationRun
	if err := row.Scan(
		&item.ID, &item.TenantID, &item.Key, &item.DisplayName,
		&item.DatasetReference, &item.DatasetSHA256, &item.AuthorizationRef, &item.EvidenceClass,
		&item.Subject, &item.Grade, &item.QuestionType, &item.Modality,
		&item.SampleCount, &item.RepeatCount, &item.Status,
		&item.CompletedAt, &item.InvalidatedAt, &item.CreatedAt,
	); err != nil {
		return EvaluationRun{}, err
	}
	item.Candidates = []EvaluationCandidate{}
	return item, nil
}

func scanEvaluationCandidate(row rowScanner) (EvaluationCandidate, error) {
	var item EvaluationCandidate
	if err := row.Scan(
		&item.ID, &item.TenantID, &item.RunID, &item.DeploymentID,
		&item.ModelConfigID, &item.ProviderKey, &item.DeploymentKey,
		&item.ModelName, &item.ModelVersion,
		&item.PromptVersion, &item.RubricVersion,
		&item.EvaluatedSamples, &item.TeacherReviewedSamples,
		&item.TeacherAcceptedSamples, &item.SeriousErrorSamples,
		&item.EvidenceValidSamples, &item.RepeatComparisons,
		&item.StableRepeatSamples, &item.P95LatencyMS,
		&item.TotalCostMicros, &item.CreatedAt,
	); err != nil {
		return EvaluationCandidate{}, err
	}
	return PopulateEvaluationMetrics(item), nil
}

func mapEvaluationStoreError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return ErrConflict
		case "23514", "23503", "22P02":
			return ErrInvalidEvaluation
		}
	}
	return err
}
