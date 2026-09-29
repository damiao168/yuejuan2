package graderdrift

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type PostgresStore struct{ db *sql.DB }

func NewPostgresStore(db *sql.DB) *PostgresStore { return &PostgresStore{db: db} }

func (s *PostgresStore) UpsertWindow(ctx context.Context, tenantID string, input QualityWindow) (QualityWindow, error) {
	var rubric, middleMAE, middleExact, ewma any
	if input.RubricAgreement != nil {
		rubric = *input.RubricAgreement
	}
	if input.MiddleScoreMAE != nil {
		middleMAE = *input.MiddleScoreMAE
	}
	if input.MiddleScoreExactAgreement != nil {
		middleExact = *input.MiddleScoreExactAgreement
	}
	if input.EWMABias != nil {
		ewma = *input.EWMABias
	}
	return scanWindow(s.db.QueryRowContext(ctx, `
INSERT INTO grader_quality_window (
 tenant_id,exam_id,question_id,grader_id,window_size,window_start,window_end,sample_count,
 mean_error,mae,exact_agreement,rubric_agreement,severe_rate,middle_score_sample_count,
 middle_score_mae,middle_score_exact_agreement,ewma_bias,status,computed_at
) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)
ON CONFLICT (tenant_id,exam_id,question_id,grader_id,window_size,window_start,window_end) DO UPDATE SET
 sample_count=EXCLUDED.sample_count,mean_error=EXCLUDED.mean_error,mae=EXCLUDED.mae,
 exact_agreement=EXCLUDED.exact_agreement,rubric_agreement=EXCLUDED.rubric_agreement,severe_rate=EXCLUDED.severe_rate,
 middle_score_sample_count=EXCLUDED.middle_score_sample_count,middle_score_mae=EXCLUDED.middle_score_mae,
 middle_score_exact_agreement=EXCLUDED.middle_score_exact_agreement,ewma_bias=EXCLUDED.ewma_bias,
 status=EXCLUDED.status,computed_at=EXCLUDED.computed_at
RETURNING `+windowColumns, tenantID, input.ExamID, input.QuestionID, input.GraderID, input.WindowSize,
		input.WindowStart, input.WindowEnd, input.SampleCount, input.MeanError, input.MAE, input.ExactAgreement,
		rubric, input.SevereRate, input.MiddleScoreSampleCount, middleMAE, middleExact, ewma, input.Status, input.ComputedAt))
}

// 批量写事件使用唯一键和 DO NOTHING，重算任务可安全重试而不会重复创建事故。
func (s *PostgresStore) CreateIncidentsIfMissing(ctx context.Context, tenantID string, candidates []Incident) ([]Incident, error) {
	if len(candidates) == 0 {
		return []Incident{}, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	created := []Incident{}
	for _, candidate := range candidates {
		metrics, err := json.Marshal(candidate.MetricSnapshot)
		if err != nil {
			return nil, ErrInvalidInput
		}
		rangeJSON, err := json.Marshal(candidate.AffectedRange)
		if err != nil {
			return nil, ErrInvalidInput
		}
		row := tx.QueryRowContext(ctx, `
INSERT INTO grading_quality_incident
 (tenant_id,exam_id,question_id,grader_id,source_window_id,incident_type,severity,
  metric_snapshot_json,affected_range_json,status,created_at)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,$7,$8::jsonb,$9::jsonb,$10,$11)
ON CONFLICT (tenant_id,source_window_id,incident_type) DO NOTHING
RETURNING `+incidentColumns, tenantID, candidate.ExamID, candidate.QuestionID, candidate.GraderID,
			candidate.SourceWindowID, candidate.Type, candidate.Severity, metrics, rangeJSON, candidate.Status, candidate.CreatedAt)
		incident, err := scanIncident(row)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		created = append(created, incident)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return created, nil
}

func (s *PostgresStore) ListWindows(ctx context.Context, tenantID string, filter WindowFilter) ([]QualityWindow, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+windowColumns+`
FROM grader_quality_window WHERE tenant_id=$1::uuid
 AND ($2='' OR exam_id::text=$2) AND ($3='' OR question_id::text=$3) AND ($4='' OR grader_id::text=$4)
ORDER BY window_end DESC,computed_at DESC LIMIT $5`, tenantID, filter.ExamID, filter.QuestionID, filter.GraderID, filter.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []QualityWindow{}
	for rows.Next() {
		item, err := scanWindow(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *PostgresStore) ListIncidents(ctx context.Context, tenantID string, filter IncidentFilter) ([]Incident, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+incidentColumns+`
FROM grading_quality_incident WHERE tenant_id=$1::uuid
 AND ($2='' OR exam_id::text=$2) AND ($3='' OR question_id::text=$3) AND ($4='' OR grader_id::text=$4)
 AND ($5='' OR status=$5)
ORDER BY created_at DESC,id DESC LIMIT $6`, tenantID, filter.ExamID, filter.QuestionID, filter.GraderID, filter.Status, filter.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Incident{}
	for rows.Next() {
		item, err := scanIncident(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *PostgresStore) ResolveIncident(ctx context.Context, tenantID, incidentID string, now time.Time) (Incident, error) {
	incident, err := scanIncident(s.db.QueryRowContext(ctx, `
UPDATE grading_quality_incident SET status='resolved',resolved_at=$3
WHERE tenant_id=$1::uuid AND id=$2::uuid AND status <> 'resolved'
RETURNING `+incidentColumns, tenantID, incidentID, now.UTC()))
	if errors.Is(err, sql.ErrNoRows) {
		return Incident{}, ErrNotFound
	}
	return incident, err
}

const windowColumns = `id::text,exam_id::text,question_id::text,grader_id::text,window_size,window_start,window_end,
 sample_count,mean_error::float8,mae::float8,exact_agreement::float8,rubric_agreement::float8,severe_rate::float8,
 middle_score_sample_count,middle_score_mae::float8,middle_score_exact_agreement::float8,ewma_bias::float8,status,computed_at`
const incidentColumns = `id::text,exam_id::text,question_id::text,grader_id::text,source_window_id::text,incident_type,
 severity,metric_snapshot_json,affected_range_json,status,created_at,resolved_at`

type rowScanner interface{ Scan(...any) error }

func scanWindow(row rowScanner) (QualityWindow, error) {
	var out QualityWindow
	var rubric, middleMAE, middleExact, ewma sql.NullFloat64
	if err := row.Scan(&out.ID, &out.ExamID, &out.QuestionID, &out.GraderID, &out.WindowSize, &out.WindowStart, &out.WindowEnd,
		&out.SampleCount, &out.MeanError, &out.MAE, &out.ExactAgreement, &rubric, &out.SevereRate, &out.MiddleScoreSampleCount,
		&middleMAE, &middleExact, &ewma, &out.Status, &out.ComputedAt); err != nil {
		return QualityWindow{}, err
	}
	if rubric.Valid {
		value := rubric.Float64
		out.RubricAgreement = &value
	}
	if middleMAE.Valid {
		value := middleMAE.Float64
		out.MiddleScoreMAE = &value
	}
	if middleExact.Valid {
		value := middleExact.Float64
		out.MiddleScoreExactAgreement = &value
	}
	if ewma.Valid {
		value := ewma.Float64
		out.EWMABias = &value
	}
	out.WindowStart, out.WindowEnd, out.ComputedAt = out.WindowStart.UTC(), out.WindowEnd.UTC(), out.ComputedAt.UTC()
	return out, nil
}

func scanIncident(row rowScanner) (Incident, error) {
	var out Incident
	var metrics, affected []byte
	var resolved sql.NullTime
	if err := row.Scan(&out.ID, &out.ExamID, &out.QuestionID, &out.GraderID, &out.SourceWindowID, &out.Type, &out.Severity,
		&metrics, &affected, &out.Status, &out.CreatedAt, &resolved); err != nil {
		return Incident{}, err
	}
	if err := json.Unmarshal(metrics, &out.MetricSnapshot); err != nil {
		return Incident{}, err
	}
	if err := json.Unmarshal(affected, &out.AffectedRange); err != nil {
		return Incident{}, err
	}
	if resolved.Valid {
		value := resolved.Time.UTC()
		out.ResolvedAt = &value
	}
	out.CreatedAt = out.CreatedAt.UTC()
	return out, nil
}
