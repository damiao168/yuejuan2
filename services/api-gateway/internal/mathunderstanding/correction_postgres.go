package mathunderstanding

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

type PostgresCorrectionStore struct {
	db *sql.DB
}

func NewPostgresCorrectionStore(db *sql.DB, _ Store) *PostgresCorrectionStore {
	return &PostgresCorrectionStore{db: db}
}
func (s *PostgresCorrectionStore) CreateCorrection(ctx context.Context, tenantID, artifactID, actorID string, input CreateCorrectionInput) (Correction, error) {
	if tenantID == "" || actorID == "" || validateCorrection(input) != nil {
		return Correction{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Correction{}, err
	}
	defer tx.Rollback()
	var artifact Artifact
	err = tx.QueryRowContext(ctx, `SELECT answer_segment_id::text,exam_question_snapshot_id::text,version,is_current,subject_code,input_hash,engine_version FROM math_understanding_artifact WHERE tenant_id=$1::uuid AND id=$2::uuid FOR UPDATE`, tenantID, artifactID).Scan(&artifact.AnswerSegmentID, &artifact.ExamQuestionSnapshotID, &artifact.Version, &artifact.IsCurrent, &artifact.SubjectCode, &artifact.InputHash, &artifact.EngineVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return Correction{}, ErrNotFound
	}
	if err != nil {
		return Correction{}, err
	}
	if !artifact.IsCurrent || artifact.Version != input.ExpectedArtifactVersion || !correctionMatchesArtifact(input.CorrectedContract, artifact) {
		return Correction{}, ErrRevisionConflict
	}
	// 等到基础证据的行锁后再读取修订号，才能看到前一个保存事务提交的修订，拒绝旧表单覆盖。
	var revision int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(max(revision),0) FROM math_understanding_correction WHERE tenant_id=$1::uuid AND artifact_id=$2::uuid`, tenantID, artifactID).Scan(&revision); err != nil {
		return Correction{}, err
	}
	if input.ExpectedCorrectionRevision != nil && *input.ExpectedCorrectionRevision != revision {
		return Correction{}, ErrRevisionConflict
	}
	operations, _ := json.Marshal(input.Operations)
	contract, _ := json.Marshal(input.CorrectedContract)
	var item Correction
	var opRaw, contractRaw []byte
	err = tx.QueryRowContext(ctx, `INSERT INTO math_understanding_correction(tenant_id,artifact_id,answer_segment_id,revision,operations_json,corrected_contract_json,reason,created_by) VALUES($1::uuid,$2::uuid,$3::uuid,$8,$4::jsonb,$5::jsonb,$6,$7::uuid) RETURNING id::text,tenant_id::text,artifact_id::text,answer_segment_id::text,revision,operations_json,corrected_contract_json,reason,created_by::text,created_at`, tenantID, artifactID, artifact.AnswerSegmentID, operations, contract, input.Reason, actorID, revision+1).Scan(&item.ID, &item.TenantID, &item.ArtifactID, &item.AnswerSegmentID, &item.Revision, &opRaw, &contractRaw, &item.Reason, &item.CreatedBy, &item.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Correction{}, ErrRevisionConflict
	}
	if err != nil {
		return Correction{}, err
	}
	if json.Unmarshal(opRaw, &item.Operations) != nil || json.Unmarshal(contractRaw, &item.CorrectedContract) != nil {
		return Correction{}, errors.New("decode math correction")
	}
	if err = tx.Commit(); err != nil {
		return Correction{}, err
	}
	return item, nil
}
func (s *PostgresCorrectionStore) ListCorrections(ctx context.Context, tenantID, artifactID string) ([]Correction, error) {
	return s.list(ctx, tenantID, "artifact_id=$2::uuid", artifactID, 500)
}

// GetLatestCorrection 直接按修订号取最新项；历史列表最多返回 500 项，不能用列表末项代替。
func (s *PostgresCorrectionStore) GetLatestCorrection(ctx context.Context, tenantID, artifactID string) (Correction, error) {
	return s.getCorrection(ctx, tenantID, artifactID, 0)
}
func (s *PostgresCorrectionStore) GetCorrection(ctx context.Context, tenantID, artifactID string, revision int64) (Correction, error) {
	if revision <= 0 {
		return Correction{}, ErrNotFound
	}
	return s.getCorrection(ctx, tenantID, artifactID, revision)
}
func (s *PostgresCorrectionStore) getCorrection(ctx context.Context, tenantID, artifactID string, revision int64) (Correction, error) {
	var item Correction
	var operations, contract []byte
	query := `SELECT id::text,tenant_id::text,artifact_id::text,answer_segment_id::text,revision,operations_json,corrected_contract_json,reason,created_by::text,created_at FROM math_understanding_correction WHERE tenant_id=$1::uuid AND artifact_id=$2::uuid`
	args := []any{tenantID, artifactID}
	if revision > 0 {
		query += ` AND revision=$3`
		args = append(args, revision)
	} else {
		query += ` ORDER BY revision DESC LIMIT 1`
	}
	err := s.db.QueryRowContext(ctx, query, args...).Scan(&item.ID, &item.TenantID, &item.ArtifactID, &item.AnswerSegmentID, &item.Revision, &operations, &contract, &item.Reason, &item.CreatedBy, &item.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Correction{}, ErrNotFound
	}
	if err != nil {
		return Correction{}, err
	}
	if json.Unmarshal(operations, &item.Operations) != nil || json.Unmarshal(contract, &item.CorrectedContract) != nil {
		return Correction{}, errors.New("decode math correction")
	}
	return item, nil
}

func (s *PostgresCorrectionStore) ExportCorrections(ctx context.Context, tenantID, subject string, limit int) ([]Correction, error) {
	if limit <= 0 || limit > 5000 {
		limit = 1000
	}
	return s.list(ctx, tenantID, "corrected_contract_json->>'subject_code'=$2", subject, limit)
}
func (s *PostgresCorrectionStore) list(ctx context.Context, tenantID, predicate, value string, limit int) ([]Correction, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id::text,tenant_id::text,artifact_id::text,answer_segment_id::text,revision,operations_json,corrected_contract_json,reason,created_by::text,created_at FROM math_understanding_correction WHERE tenant_id=$1::uuid AND `+predicate+` ORDER BY created_at,id LIMIT $3`, tenantID, value, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Correction{}
	for rows.Next() {
		var item Correction
		var operations, contract []byte
		if err = rows.Scan(&item.ID, &item.TenantID, &item.ArtifactID, &item.AnswerSegmentID, &item.Revision, &operations, &contract, &item.Reason, &item.CreatedBy, &item.CreatedAt); err != nil {
			return nil, err
		}
		if json.Unmarshal(operations, &item.Operations) != nil || json.Unmarshal(contract, &item.CorrectedContract) != nil {
			return nil, errors.New("decode math correction")
		}
		out = append(out, item)
	}
	return out, rows.Err()
}
