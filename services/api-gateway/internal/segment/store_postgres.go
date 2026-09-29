package segment

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

type PostgresStore struct {
	db *sql.DB
}

func NewPostgresStore(db *sql.DB) *PostgresStore {
	return &PostgresStore{db: db}
}

func (s *PostgresStore) CreateSegments(ctx context.Context, inputs []CreateSegmentInput) ([]Segment, error) {
	// 逐条写入，没有包住整批的事务；中途失败时前面的记录可能已保存，重试靠唯一键复用。
	out := make([]Segment, 0, len(inputs))
	for _, input := range inputs {
		if err := ValidateBBox(input.BBox); err != nil {
			return nil, err
		}
		bbox, _ := json.Marshal(input.BBox)
		row := s.db.QueryRowContext(ctx, `
INSERT INTO answer_segment (
  tenant_id, submission_id, submission_page_id, question_id, question_no, bbox, source, status
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (tenant_id, submission_id, question_id)
DO UPDATE SET updated_at = answer_segment.updated_at
RETURNING id::text, tenant_id::text, submission_id::text, submission_page_id::text,
  question_id::text, question_no, bbox, source, status, COALESCE(review_notes, ''),
  COALESCE(reviewed_by::text, ''), reviewed_at, created_at
`, input.TenantID, input.SubmissionID, input.SubmissionPageID, input.QuestionID, input.QuestionNo, bbox, input.Source, input.Status)
		// 冲突时保持原记录和人工审核字段，只刷新返回值，保证接口重复调用不会覆盖审核结果。
		var item Segment
		if err := scanSegment(row, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	sortSegments(out)
	return out, nil
}

func (s *PostgresStore) ListBySubmission(ctx context.Context, tenantID string, submissionID string) ([]Segment, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, tenant_id::text, submission_id::text, submission_page_id::text,
  question_id::text, question_no, bbox, source, status, COALESCE(review_notes, ''),
  COALESCE(reviewed_by::text, ''), reviewed_at, created_at
FROM answer_segment
WHERE tenant_id = $1 AND submission_id = $2 AND deleted_at IS NULL
ORDER BY question_no
`, tenantID, submissionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Segment{}
	for rows.Next() {
		var item Segment
		if err := scanSegment(rows, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) Update(ctx context.Context, tenantID string, id string, actorID string, input UpdateSegmentInput) (Segment, error) {
	current, err := s.get(ctx, tenantID, id)
	if err != nil {
		return Segment{}, err
	}
	merged := current
	// 先读取再合并可选字段，使 PATCH 只改变请求提供的内容；坐标变化同时转为人工来源。
	if input.BBox != nil {
		if err := ValidateBBox(*input.BBox); err != nil {
			return Segment{}, err
		}
		merged.BBox = append([]float64{}, (*input.BBox)...)
		merged.Source = "manual"
	}
	if input.Status != nil {
		if !IsValidStatus(*input.Status) {
			return Segment{}, ErrInvalidInput
		}
		merged.Status = *input.Status
	}
	if input.ReviewNotes != nil {
		merged.ReviewNotes = *input.ReviewNotes
	}
	bbox, _ := json.Marshal(merged.BBox)
	row := s.db.QueryRowContext(ctx, `
UPDATE answer_segment
SET bbox = $3,
  source = $4,
  status = $5,
  review_notes = $6,
  reviewed_by = NULLIF($7, '')::uuid,
  reviewed_at = now(),
  updated_at = now()
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
RETURNING id::text, tenant_id::text, submission_id::text, submission_page_id::text,
  question_id::text, question_no, bbox, source, status, COALESCE(review_notes, ''),
  COALESCE(reviewed_by::text, ''), reviewed_at, created_at
`, tenantID, id, bbox, merged.Source, merged.Status, merged.ReviewNotes, actorID)
	var out Segment
	if err := scanSegment(row, &out); err != nil {
		return Segment{}, err
	}
	return out, nil
}

func (s *PostgresStore) get(ctx context.Context, tenantID string, id string) (Segment, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id::text, tenant_id::text, submission_id::text, submission_page_id::text,
  question_id::text, question_no, bbox, source, status, COALESCE(review_notes, ''),
  COALESCE(reviewed_by::text, ''), reviewed_at, created_at
FROM answer_segment
WHERE tenant_id = $1 AND id::text = $2 AND deleted_at IS NULL
`, tenantID, id)
	var item Segment
	if err := scanSegment(row, &item); err != nil {
		return Segment{}, err
	}
	return item, nil
}

func (s *PostgresStore) GetEvidence(ctx context.Context, tenantID string, id string) (SegmentEvidence, error) {
	return s.getEvidence(ctx, tenantID, id, "")
}

// GetEvidenceForQuestion is the server-to-server variant used by internal
// grading evidence resolvers. It keeps the question binding in the tenant
// scoped database lookup instead of accepting a segment and comparing later.
func (s *PostgresStore) GetEvidenceForQuestion(ctx context.Context, tenantID string, id string, questionID string) (SegmentEvidence, error) {
	if questionID == "" {
		return SegmentEvidence{}, ErrNotFound
	}
	return s.getEvidence(ctx, tenantID, id, questionID)
}

func (s *PostgresStore) getEvidence(ctx context.Context, tenantID string, id string, questionID string) (SegmentEvidence, error) {
	// 查询核对租户及可选题目归属；操作者是否有权查看这张答卷，仍由业务入口检查。
	var out SegmentEvidence
	var normalized, pixels []byte
	err := s.db.QueryRowContext(ctx, `SELECT s.id::text,s.submission_id::text,s.submission_page_id::text,sub.exam_id::text,s.question_id::text,s.question_no,COALESCE(s.template_id::text,''),COALESCE(s.template_content_hash,''),COALESCE(s.registration_run_id::text,''),COALESCE(r.method,''),COALESCE(r.confidence,0),COALESCE(s.normalized_bbox,'{}'),COALESCE(s.pixel_bbox,'{}'),COALESCE(s.crop_file_asset_id::text,''),COALESCE(c.id::text,''),COALESCE(s.crop_sha256,''),COALESCE(s.question_version,1),s.processing_status,COALESCE(r.processing_status,''),COALESCE(s.confidence,0) FROM answer_segment s JOIN submission sub ON sub.tenant_id=s.tenant_id AND sub.id=s.submission_id LEFT JOIN page_registration_run r ON r.tenant_id=s.tenant_id AND r.id=s.registration_run_id LEFT JOIN page_registration_correction c ON c.tenant_id=s.tenant_id AND c.applied_registration_run_id=s.registration_run_id AND c.status='applied' AND c.deleted_at IS NULL WHERE s.tenant_id=$1 AND s.id=$2::uuid AND ($3='' OR s.question_id::text=$3) AND s.deleted_at IS NULL`, tenantID, id, questionID).Scan(&out.SegmentID, &out.SubmissionID, &out.SubmissionPageID, &out.ExamID, &out.QuestionID, &out.QuestionNo, &out.TemplateID, &out.TemplateContentHash, &out.RegistrationRunID, &out.RegistrationMethod, &out.RegistrationConfidence, &normalized, &pixels, &out.CropFileAssetID, &out.CorrectionID, &out.CropSHA256, &out.QuestionVersion, &out.ProcessingStatus, &out.RegistrationStatus, &out.Confidence)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SegmentEvidence{}, ErrNotFound
		}
		return SegmentEvidence{}, err
	}
	_ = json.Unmarshal(normalized, &out.NormalizedBBox)
	_ = json.Unmarshal(pixels, &out.PixelBBox)
	return out, nil
}

type segmentScanner interface {
	Scan(dest ...any) error
}

func scanSegment(row segmentScanner, out *Segment) error {
	var bbox []byte
	var reviewedAt sql.NullTime
	if err := row.Scan(
		&out.ID,
		&out.TenantID,
		&out.SubmissionID,
		&out.SubmissionPageID,
		&out.QuestionID,
		&out.QuestionNo,
		&bbox,
		&out.Source,
		&out.Status,
		&out.ReviewNotes,
		&out.ReviewedBy,
		&reviewedAt,
		&out.CreatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	_ = json.Unmarshal(bbox, &out.BBox)
	if reviewedAt.Valid {
		value := reviewedAt.Time.UTC()
		out.ReviewedAt = &value
	}
	return nil
}
