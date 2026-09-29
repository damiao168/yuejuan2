package modelcalibration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

type PostgresStore struct{ db *sql.DB }

func NewPostgresStore(db *sql.DB) *PostgresStore { return &PostgresStore{db: db} }

func (s *PostgresStore) Create(ctx context.Context, tenantID, actorID string, input CreateInput) (Calibration, error) {
	if s.db == nil {
		return Calibration{}, ErrInvalidInput
	}
	row := s.db.QueryRowContext(ctx, `
INSERT INTO model_calibration(
 id,tenant_id,calibration_key,evaluation_run_id,model_reference,prompt_version,rubric_version,subject_code,archetype_code,slice_key,method,created_by
) VALUES($1,$2::uuid,$3,$4::uuid,$5,$6,$7,$8,$9,$10,$11,NULLIF($12,'')::uuid)
RETURNING `+calibrationColumns, uuid.NewString(), tenantID, input.Key, input.EvaluationRunID, input.Axis.ModelReference, input.Axis.PromptVersion,
		input.Axis.RubricVersion, input.Axis.Subject, input.Axis.Archetype, input.Axis.SliceKey, input.Method, actorID)
	item, err := scanCalibration(row)
	return item, mapStoreError(err)
}

func (s *PostgresStore) Get(ctx context.Context, tenantID, calibrationID string) (Calibration, error) {
	if s.db == nil {
		return Calibration{}, ErrInvalidInput
	}
	item, err := scanCalibration(s.db.QueryRowContext(ctx, `SELECT `+calibrationColumns+` FROM model_calibration WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, calibrationID))
	return item, mapStoreError(err)
}

func (s *PostgresStore) List(ctx context.Context, tenantID string, axis Axis, limit int) ([]Calibration, error) {
	if s.db == nil {
		return nil, ErrInvalidInput
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+calibrationColumns+` FROM model_calibration
WHERE tenant_id=$1::uuid AND model_reference=$2 AND prompt_version=$3 AND rubric_version=$4 AND subject_code=$5 AND archetype_code=$6 AND slice_key=$7
ORDER BY created_at DESC,id DESC LIMIT $8`, tenantID, axis.ModelReference, axis.PromptVersion, axis.RubricVersion, axis.Subject, axis.Archetype, axis.SliceKey, limit)
	if err != nil {
		return nil, mapStoreError(err)
	}
	defer rows.Close()
	items := []Calibration{}
	for rows.Next() {
		item, scanErr := scanCalibration(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *PostgresStore) AddEvidence(ctx context.Context, tenantID, calibrationID string, input CalibrationEvidence) (CalibrationEvidence, error) {
	if s.db == nil {
		return CalibrationEvidence{}, ErrInvalidInput
	}
	row := s.db.QueryRowContext(ctx, `
INSERT INTO model_calibration_evidence(
 id,tenant_id,calibration_id,evaluation_run_id,response_key,raw_confidence,correct,severe_error,score_band,ocr_quality,observed_at
) VALUES($1,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7,$8,$9,$10,$11)
RETURNING id::text,calibration_id::text,evaluation_run_id::text,response_key,raw_confidence::float8,correct,severe_error,score_band,ocr_quality,observed_at`,
		uuid.NewString(), tenantID, calibrationID, input.EvaluationRunID, input.ResponseKey, input.RawConfidence, input.Correct, input.SevereError, input.ScoreBand, input.OCRQuality, input.ObservedAt.UTC())
	item, err := scanEvidence(row)
	return item, mapStoreError(err)
}

func (s *PostgresStore) ListEvidence(ctx context.Context, tenantID, calibrationID string) ([]CalibrationEvidence, error) {
	if s.db == nil {
		return nil, ErrInvalidInput
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id::text,calibration_id::text,evaluation_run_id::text,response_key,raw_confidence::float8,correct,severe_error,score_band,ocr_quality,observed_at
FROM model_calibration_evidence WHERE tenant_id=$1::uuid AND calibration_id=$2::uuid ORDER BY observed_at,id`, tenantID, calibrationID)
	if err != nil {
		return nil, mapStoreError(err)
	}
	defer rows.Close()
	items := []CalibrationEvidence{}
	for rows.Next() {
		item, scanErr := scanEvidence(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if len(items) == 0 {
		if _, getErr := s.Get(ctx, tenantID, calibrationID); getErr != nil {
			return nil, getErr
		}
	}
	return items, nil
}

func (s *PostgresStore) Complete(ctx context.Context, tenantID, calibrationID string, expectedN int, artifact Artifact, uri, digest string, at time.Time) (Calibration, error) {
	if s.db == nil {
		return Calibration{}, ErrInvalidInput
	}
	raw, err := json.Marshal(artifact)
	if err != nil {
		return Calibration{}, err
	}
	// 完成时锁住校准记录并重新核对证据数量，避免并发新增证据被漏进已签名 artifact。
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Calibration{}, err
	}
	defer tx.Rollback()
	var actual int
	var status Status
	if err = tx.QueryRowContext(ctx, `SELECT status,(SELECT count(*) FROM model_calibration_evidence WHERE tenant_id=$1::uuid AND calibration_id=$2::uuid)
FROM model_calibration WHERE tenant_id=$1::uuid AND id=$2::uuid FOR UPDATE`, tenantID, calibrationID).Scan(&status, &actual); err != nil {
		return Calibration{}, mapStoreError(err)
	}
	if status != StatusDraft {
		return Calibration{}, ErrStateConflict
	}
	if actual != expectedN || actual == 0 {
		return Calibration{}, ErrConflict
	}
	row := tx.QueryRowContext(ctx, `
UPDATE model_calibration SET method=$3,status='completed',calibration_n=$4,artifact_uri=$5,artifact_sha256=$6,artifact_json=$7,completed_at=$8
WHERE tenant_id=$1::uuid AND id=$2::uuid AND status='draft'
RETURNING `+calibrationColumns, tenantID, calibrationID, artifact.Method, expectedN, uri, digest, raw, at.UTC())
	item, err := scanCalibration(row)
	if err != nil {
		return Calibration{}, mapStoreError(err)
	}
	if err = tx.Commit(); err != nil {
		return Calibration{}, mapStoreError(err)
	}
	return item, nil
}

func (s *PostgresStore) Approve(ctx context.Context, tenantID, calibrationID, actorID string, at time.Time) (Calibration, error) {
	if s.db == nil {
		return Calibration{}, ErrInvalidInput
	}
	row := s.db.QueryRowContext(ctx, `
UPDATE model_calibration SET status='approved',approved_at=$4,approved_by=NULLIF($3,'')::uuid
WHERE tenant_id=$1::uuid AND id=$2::uuid AND status='completed'
RETURNING `+calibrationColumns, tenantID, calibrationID, actorID, at.UTC())
	item, err := scanCalibration(row)
	if errors.Is(err, sql.ErrNoRows) {
		if _, getErr := s.Get(ctx, tenantID, calibrationID); getErr != nil {
			return Calibration{}, getErr
		}
		return Calibration{}, ErrStateConflict
	}
	return item, mapStoreError(err)
}

func (s *PostgresStore) Invalidate(ctx context.Context, tenantID, calibrationID, actorID, reason string, at time.Time) (Calibration, error) {
	if s.db == nil {
		return Calibration{}, ErrInvalidInput
	}
	row := s.db.QueryRowContext(ctx, `
UPDATE model_calibration SET status='invalidated',invalidated_at=$5,invalidated_by=NULLIF($3,'')::uuid,invalidation_reason=$4
WHERE tenant_id=$1::uuid AND id=$2::uuid AND status IN ('completed','approved')
RETURNING `+calibrationColumns, tenantID, calibrationID, actorID, reason, at.UTC())
	item, err := scanCalibration(row)
	if errors.Is(err, sql.ErrNoRows) {
		if _, getErr := s.Get(ctx, tenantID, calibrationID); getErr != nil {
			return Calibration{}, getErr
		}
		return Calibration{}, ErrStateConflict
	}
	return item, mapStoreError(err)
}

func (s *PostgresStore) FindApproved(ctx context.Context, tenantID string, axis Axis) (Calibration, error) {
	if s.db == nil {
		return Calibration{}, ErrInvalidInput
	}
	item, err := scanCalibration(s.db.QueryRowContext(ctx, `SELECT `+calibrationColumns+` FROM model_calibration
WHERE tenant_id=$1::uuid AND model_reference=$2 AND prompt_version=$3 AND rubric_version=$4 AND subject_code=$5 AND archetype_code=$6 AND slice_key=$7 AND status='approved'
ORDER BY approved_at DESC,id DESC LIMIT 1`, tenantID, axis.ModelReference, axis.PromptVersion, axis.RubricVersion, axis.Subject, axis.Archetype, axis.SliceKey))
	return item, mapStoreError(err)
}

func (s *PostgresStore) CreateOrGetCandidate(ctx context.Context, tenantID string, input Candidate) (Candidate, error) {
	if s.db == nil {
		return Candidate{}, ErrInvalidInput
	}
	row := s.db.QueryRowContext(ctx, `
INSERT INTO model_score_candidate(
 id,tenant_id,candidate_key,model_reference,prompt_version,rubric_version,subject_code,archetype_code,slice_key,raw_confidence,calibrated_confidence,calibration_id,target_risk,abstain_reason,created_at
) VALUES($1,$2::uuid,$3,$4,$5,$6,$7,$8,$9,$10,$11,NULLIF($12,'')::uuid,$13,$14,$15)
ON CONFLICT (tenant_id,candidate_key) DO NOTHING
RETURNING `+candidateColumns, uuid.NewString(), tenantID, input.CandidateKey, input.Axis.ModelReference, input.Axis.PromptVersion, input.Axis.RubricVersion,
		input.Axis.Subject, input.Axis.Archetype, input.Axis.SliceKey, input.RawConfidence, input.CalibratedConfidence, input.CalibrationID, input.TargetRisk, input.AbstainReason, input.CreatedAt.UTC())
	item, err := scanCandidate(row)
	if errors.Is(err, sql.ErrNoRows) {
		item, err = scanCandidate(s.db.QueryRowContext(ctx, `SELECT `+candidateColumns+` FROM model_score_candidate WHERE tenant_id=$1::uuid AND candidate_key=$2`, tenantID, input.CandidateKey))
	}
	return item, mapStoreError(err)
}

const calibrationColumns = `id::text,tenant_id::text,calibration_key,evaluation_run_id::text,model_reference,prompt_version,rubric_version,subject_code,archetype_code,slice_key,method,status,calibration_n,artifact_uri,artifact_sha256,artifact_json,created_by::text,created_at,completed_at,approved_at,approved_by::text,invalidated_at,invalidated_by::text,invalidation_reason`
const candidateColumns = `id::text,tenant_id::text,candidate_key,model_reference,prompt_version,rubric_version,subject_code,archetype_code,slice_key,raw_confidence::float8,calibrated_confidence::float8,COALESCE(calibration_id::text,''),target_risk::float8,abstain_reason,created_at`

type scanner interface{ Scan(...any) error }

func scanCalibration(row scanner) (Calibration, error) {
	var item Calibration
	var artifact []byte
	var createdBy, approvedBy, invalidatedBy sql.NullString
	var completedAt, approvedAt, invalidatedAt sql.NullTime
	err := row.Scan(&item.ID, &item.TenantID, &item.Key, &item.EvaluationRunID, &item.Axis.ModelReference, &item.Axis.PromptVersion,
		&item.Axis.RubricVersion, &item.Axis.Subject, &item.Axis.Archetype, &item.Axis.SliceKey, &item.Method, &item.Status, &item.CalibrationN,
		&item.ArtifactURI, &item.ArtifactSHA256, &artifact, &createdBy, &item.CreatedAt, &completedAt, &approvedAt, &approvedBy, &invalidatedAt, &invalidatedBy, &item.InvalidationReason)
	if err != nil {
		return Calibration{}, err
	}
	if err = json.Unmarshal(artifact, &item.Artifact); err != nil {
		return Calibration{}, err
	}
	item.CreatedBy, item.ApprovedBy, item.InvalidatedBy = createdBy.String, approvedBy.String, invalidatedBy.String
	if completedAt.Valid {
		item.CompletedAt = &completedAt.Time
	}
	if approvedAt.Valid {
		item.ApprovedAt = &approvedAt.Time
	}
	if invalidatedAt.Valid {
		item.InvalidatedAt = &invalidatedAt.Time
	}
	return item, nil
}
func scanEvidence(row scanner) (CalibrationEvidence, error) {
	var item CalibrationEvidence
	err := row.Scan(&item.ID, &item.CalibrationID, &item.EvaluationRunID, &item.ResponseKey, &item.RawConfidence, &item.Correct, &item.SevereError, &item.ScoreBand, &item.OCRQuality, &item.ObservedAt)
	return item, err
}
func scanCandidate(row scanner) (Candidate, error) {
	var item Candidate
	var calibrated, target sql.NullFloat64
	err := row.Scan(&item.ID, &item.TenantID, &item.CandidateKey, &item.Axis.ModelReference, &item.Axis.PromptVersion, &item.Axis.RubricVersion,
		&item.Axis.Subject, &item.Axis.Archetype, &item.Axis.SliceKey, &item.RawConfidence, &calibrated, &item.CalibrationID, &target, &item.AbstainReason, &item.CreatedAt)
	if err != nil {
		return Candidate{}, err
	}
	if calibrated.Valid {
		value := calibrated.Float64
		item.CalibratedConfidence = &value
	}
	if target.Valid {
		value := target.Float64
		item.TargetRisk = &value
	}
	return item, nil
}

func mapStoreError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23505":
			return ErrConflict
		case "23503", "23514", "22P02":
			return ErrInvalidInput
		}
	}
	return err
}
