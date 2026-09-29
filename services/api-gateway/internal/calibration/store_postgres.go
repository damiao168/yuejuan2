package calibration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

type PostgresStore struct{ db *sql.DB }

func NewPostgresStore(db *sql.DB) *PostgresStore { return &PostgresStore{db: db} }

func (s *PostgresStore) PutPolicy(ctx context.Context, tenantID, examID, questionID string, input PutPolicyInput) (Policy, error) {
	row := s.db.QueryRowContext(ctx, `
INSERT INTO grader_calibration_policy (
 tenant_id,exam_id,question_id,archetype_code,max_score,minimum_samples,maximum_mae,
 minimum_exact_agreement,minimum_within_one_agreement,minimum_criterion_agreement,
 maximum_severe_rate,severe_error_threshold,qualification_validity_days,revision
) SELECT $1::uuid,$2::uuid,$3::uuid,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,1
WHERE $14=0 OR EXISTS (
 SELECT 1 FROM grader_calibration_policy current
 WHERE current.tenant_id=$1::uuid AND current.exam_id=$2::uuid AND current.question_id=$3::uuid AND current.revision=$14
)
ON CONFLICT (tenant_id,exam_id,question_id) DO UPDATE SET
 archetype_code=EXCLUDED.archetype_code,max_score=EXCLUDED.max_score,minimum_samples=EXCLUDED.minimum_samples,
 maximum_mae=EXCLUDED.maximum_mae,minimum_exact_agreement=EXCLUDED.minimum_exact_agreement,
 minimum_within_one_agreement=EXCLUDED.minimum_within_one_agreement,
 minimum_criterion_agreement=EXCLUDED.minimum_criterion_agreement,maximum_severe_rate=EXCLUDED.maximum_severe_rate,
 severe_error_threshold=EXCLUDED.severe_error_threshold,qualification_validity_days=EXCLUDED.qualification_validity_days,
 revision=grader_calibration_policy.revision+1,updated_at=now()
WHERE grader_calibration_policy.revision=$14
RETURNING id::text,exam_id::text,question_id::text,archetype_code,max_score::float8,minimum_samples,maximum_mae::float8,
 minimum_exact_agreement::float8,minimum_within_one_agreement::float8,minimum_criterion_agreement::float8,
 maximum_severe_rate::float8,severe_error_threshold::float8,qualification_validity_days,revision,created_at,updated_at
`, tenantID, examID, questionID, input.ArchetypeCode, input.MaxScore, input.MinimumSamples, input.MaximumMAE,
		input.MinimumExactAgreement, input.MinimumWithinOneAgreement, input.MinimumCriterionAgreement, input.MaximumSevereRate,
		input.SevereErrorThreshold, input.QualificationValidityDays, input.ExpectedRevision)
	policy, err := scanPolicy(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Policy{}, ErrConflict
	}
	return policy, err
}

func (s *PostgresStore) GetPolicy(ctx context.Context, tenantID, examID, questionID string) (Policy, error) {
	policy, err := scanPolicy(s.db.QueryRowContext(ctx, `
SELECT id::text,exam_id::text,question_id::text,archetype_code,max_score::float8,minimum_samples,maximum_mae::float8,
 minimum_exact_agreement::float8,minimum_within_one_agreement::float8,minimum_criterion_agreement::float8,
 maximum_severe_rate::float8,severe_error_threshold::float8,qualification_validity_days,revision,created_at,updated_at
FROM grader_calibration_policy WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND question_id=$3::uuid
`, tenantID, examID, questionID))
	if errors.Is(err, sql.ErrNoRows) {
		return Policy{}, ErrNotFound
	}
	return policy, err
}

func (s *PostgresStore) CreateSession(ctx context.Context, tenantID string, session Session, policy Policy, references []goldReference) (Session, error) {
	policyJSON, err := json.Marshal(policy)
	if err != nil {
		return Session{}, ErrInvalidInput
	}
	manifestJSON, err := json.Marshal(references)
	if err != nil {
		return Session{}, ErrInvalidInput
	}
	_, err = s.db.ExecContext(ctx, `
INSERT INTO grader_calibration_session (
 id,tenant_id,exam_id,question_id,grader_id,exam_question_snapshot_id,gold_version,
 policy_snapshot_json,sample_manifest_json,status,started_at
) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6::uuid,$7,$8::jsonb,$9::jsonb,$10,$11)
`, session.ID, tenantID, session.ExamID, session.QuestionID, session.GraderID, session.SnapshotID,
		session.GoldSetHash, policyJSON, manifestJSON, session.Status, session.StartedAt)
	if isUnique(err) {
		return Session{}, ErrConflict
	}
	if err != nil {
		return Session{}, err
	}
	return cloneSession(session), nil
}

func (s *PostgresStore) GetSession(ctx context.Context, tenantID, sessionID string) (Session, Policy, []goldReference, []Attempt, error) {
	var session Session
	var policyJSON, manifestJSON, metricsJSON []byte
	var completedAt, invalidatedAt sql.NullTime
	err := s.db.QueryRowContext(ctx, `
SELECT id::text,exam_id::text,question_id::text,grader_id::text,exam_question_snapshot_id::text,gold_version,status,
 policy_snapshot_json,sample_manifest_json,metrics_json,started_at,completed_at,invalidated_at,invalidation_reason,
 (SELECT count(*) FROM grader_calibration_attempt a WHERE a.tenant_id=s.tenant_id AND a.session_id=s.id)
FROM grader_calibration_session s WHERE tenant_id=$1::uuid AND id=$2::uuid
`, tenantID, sessionID).Scan(&session.ID, &session.ExamID, &session.QuestionID, &session.GraderID, &session.SnapshotID,
		&session.GoldSetHash, &session.Status, &policyJSON, &manifestJSON, &metricsJSON, &session.StartedAt,
		&completedAt, &invalidatedAt, &session.InvalidationReason, &session.SubmittedCount)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, Policy{}, nil, nil, ErrNotFound
	}
	if err != nil {
		return Session{}, Policy{}, nil, nil, err
	}
	var policy Policy
	var references []goldReference
	if json.Unmarshal(policyJSON, &policy) != nil || json.Unmarshal(manifestJSON, &references) != nil {
		return Session{}, Policy{}, nil, nil, ErrInvalidInput
	}
	session.ArchetypeCode = policy.ArchetypeCode
	if len(references) > 0 {
		session.RiskTier = references[0].RiskTier
	}
	if len(metricsJSON) > 0 {
		var metrics Metrics
		if err := json.Unmarshal(metricsJSON, &metrics); err != nil {
			return Session{}, Policy{}, nil, nil, err
		}
		session.Metrics = &metrics
	}
	if completedAt.Valid {
		value := completedAt.Time.UTC()
		session.CompletedAt = &value
	}
	if invalidatedAt.Valid {
		value := invalidatedAt.Time.UTC()
		session.InvalidatedAt = &value
	}
	session.StartedAt = session.StartedAt.UTC()
	session.Samples = publicSamples(references)
	attempts, err := s.listAttempts(ctx, tenantID, sessionID)
	return session, policy, references, attempts, err
}

func (s *PostgresStore) CreateAttempt(ctx context.Context, tenantID, sessionID string, attempt Attempt) (Attempt, error) {
	rubricJSON, err := json.Marshal(attempt.RubricSelections)
	if err != nil {
		return Attempt{}, ErrInvalidInput
	}
	differenceJSON, err := json.Marshal(attempt.CriterionDifferences)
	if err != nil {
		return Attempt{}, ErrInvalidInput
	}
	result, err := s.db.ExecContext(ctx, `
INSERT INTO grader_calibration_attempt (
 id,tenant_id,session_id,gold_paper_id,gold_version,submitted_score,reference_score,
 rubric_selection_json,criterion_differences_json,criterion_correct,criterion_count,
 exact_match,within_one,absolute_error,severe_disagreement,created_at
) SELECT $1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7,$8::jsonb,$9::jsonb,$10,$11,$12,$13,$14,$15,$16
WHERE EXISTS (SELECT 1 FROM grader_calibration_session s WHERE s.tenant_id=$2::uuid AND s.id=$3::uuid AND s.status='in_progress')
`, attempt.ID, tenantID, sessionID, attempt.GoldPaperID, attempt.GoldVersion, attempt.SubmittedScore,
		attempt.ReferenceScore, rubricJSON, differenceJSON, attempt.CriterionCorrect, attempt.CriterionCount,
		attempt.ExactMatch, attempt.WithinOne, attempt.AbsoluteError, attempt.SevereDisagreement, attempt.CreatedAt)
	if isUnique(err) {
		return Attempt{}, ErrConflict
	}
	if err != nil {
		return Attempt{}, err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return Attempt{}, ErrConflict
	}
	return cloneAttempt(attempt), nil
}

func (s *PostgresStore) CompleteSession(ctx context.Context, tenantID string, session Session, qualification Qualification) (Session, Qualification, error) {
	metricsJSON, err := json.Marshal(session.Metrics)
	if err != nil {
		return Session{}, Qualification{}, ErrInvalidInput
	}
	// 会话状态和资格必须在同一事务中提交；任一写入失败都回滚，认领门禁不会看到半成品资格。
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Session{}, Qualification{}, err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
UPDATE grader_calibration_session SET status=$3,metrics_json=$4::jsonb,completed_at=$5
WHERE tenant_id=$1::uuid AND id=$2::uuid AND status='in_progress'
`, tenantID, session.ID, session.Status, metricsJSON, session.CompletedAt)
	if err != nil {
		return Session{}, Qualification{}, err
	}
	if count, _ := result.RowsAffected(); count != 1 {
		return Session{}, Qualification{}, ErrConflict
	}
	qualificationJSON, err := json.Marshal(qualification.Metrics)
	if err != nil {
		return Session{}, Qualification{}, ErrInvalidInput
	}
	_, err = tx.ExecContext(ctx, `
INSERT INTO grader_question_qualification (
 id,tenant_id,exam_id,question_id,grader_id,status,valid_until,calibration_session_id,gold_version,
 metric_snapshot_json,created_at,updated_at
) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,$7,$8::uuid,$9,$10::jsonb,$11,$12)
`, qualification.ID, tenantID, qualification.ExamID, qualification.QuestionID, qualification.GraderID,
		qualification.Status, qualification.ValidUntil, qualification.CalibrationSessionID, qualification.GoldSetHash,
		qualificationJSON, qualification.CreatedAt, qualification.UpdatedAt)
	if err != nil {
		return Session{}, Qualification{}, err
	}
	if err := tx.Commit(); err != nil {
		return Session{}, Qualification{}, err
	}
	return cloneSession(session), cloneQualification(qualification), nil
}

func (s *PostgresStore) GetQualification(ctx context.Context, tenantID, examID, questionID, graderID string) (Qualification, error) {
	qualification, err := scanQualification(s.db.QueryRowContext(ctx, `
SELECT id::text,exam_id::text,question_id::text,grader_id::text,status,valid_until,
 calibration_session_id::text,gold_version,metric_snapshot_json,created_at,updated_at
FROM grader_question_qualification
WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND question_id=$3::uuid AND grader_id=$4::uuid
ORDER BY created_at DESC LIMIT 1
`, tenantID, examID, questionID, graderID))
	if errors.Is(err, sql.ErrNoRows) {
		return Qualification{}, ErrNotFound
	}
	return qualification, err
}

func (s *PostgresStore) InvalidateQualification(ctx context.Context, tenantID, qualificationID, reason string) (Qualification, error) {
	status := QualificationRevoked
	if reason == "expired" {
		status = QualificationExpired
	}
	qualification, err := scanQualification(s.db.QueryRowContext(ctx, `
UPDATE grader_question_qualification SET status=$3,updated_at=now()
WHERE tenant_id=$1::uuid AND id=$2::uuid
RETURNING id::text,exam_id::text,question_id::text,grader_id::text,status,valid_until,
 calibration_session_id::text,gold_version,metric_snapshot_json,created_at,updated_at
`, tenantID, qualificationID, status))
	if errors.Is(err, sql.ErrNoRows) {
		return Qualification{}, ErrNotFound
	}
	return qualification, err
}

func (s *PostgresStore) listAttempts(ctx context.Context, tenantID, sessionID string) ([]Attempt, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text,session_id::text,gold_paper_id::text,gold_version,submitted_score::float8,reference_score::float8,
 rubric_selection_json,criterion_differences_json,criterion_correct,criterion_count,
 exact_match,within_one,absolute_error::float8,severe_disagreement,created_at
FROM grader_calibration_attempt WHERE tenant_id=$1::uuid AND session_id=$2::uuid ORDER BY created_at,id
`, tenantID, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Attempt{}
	for rows.Next() {
		var attempt Attempt
		var rubricJSON, differencesJSON []byte
		if err := rows.Scan(&attempt.ID, &attempt.SessionID, &attempt.GoldPaperID, &attempt.GoldVersion,
			&attempt.SubmittedScore, &attempt.ReferenceScore, &rubricJSON, &differencesJSON,
			&attempt.CriterionCorrect, &attempt.CriterionCount, &attempt.ExactMatch, &attempt.WithinOne,
			&attempt.AbsoluteError, &attempt.SevereDisagreement, &attempt.CreatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(rubricJSON, &attempt.RubricSelections); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(differencesJSON, &attempt.CriterionDifferences); err != nil {
			return nil, err
		}
		attempt.CreatedAt = attempt.CreatedAt.UTC()
		out = append(out, attempt)
	}
	return out, rows.Err()
}

type rowScanner interface{ Scan(...any) error }

func scanPolicy(row rowScanner) (Policy, error) {
	var policy Policy
	var criterion sql.NullFloat64
	err := row.Scan(&policy.ID, &policy.ExamID, &policy.QuestionID, &policy.ArchetypeCode, &policy.MaxScore,
		&policy.MinimumSamples, &policy.MaximumMAE, &policy.MinimumExactAgreement, &policy.MinimumWithinOneAgreement,
		&criterion, &policy.MaximumSevereRate, &policy.SevereErrorThreshold, &policy.QualificationValidityDays,
		&policy.Revision, &policy.CreatedAt, &policy.UpdatedAt)
	if err != nil {
		return Policy{}, err
	}
	if criterion.Valid {
		policy.MinimumCriterionAgreement = &criterion.Float64
	}
	policy.CreatedAt, policy.UpdatedAt = policy.CreatedAt.UTC(), policy.UpdatedAt.UTC()
	return policy, nil
}

func scanQualification(row rowScanner) (Qualification, error) {
	var qualification Qualification
	var metricsJSON []byte
	err := row.Scan(&qualification.ID, &qualification.ExamID, &qualification.QuestionID, &qualification.GraderID,
		&qualification.Status, &qualification.ValidUntil, &qualification.CalibrationSessionID, &qualification.GoldSetHash,
		&metricsJSON, &qualification.CreatedAt, &qualification.UpdatedAt)
	if err != nil {
		return Qualification{}, err
	}
	if err := json.Unmarshal(metricsJSON, &qualification.Metrics); err != nil {
		return Qualification{}, err
	}
	qualification.ValidUntil, qualification.CreatedAt, qualification.UpdatedAt = qualification.ValidUntil.UTC(), qualification.CreatedAt.UTC(), qualification.UpdatedAt.UTC()
	return qualification, nil
}

func isUnique(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
