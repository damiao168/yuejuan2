package paper

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

func (s *PostgresStore) ListTemplates(ctx context.Context, tenantID string, examID string) ([]AnswerSheetTemplate, error) {
	return listTemplates(ctx, s.db, tenantID, examID)
}

func listTemplates(ctx context.Context, queryer postgresQueryer, tenantID string, examID string) ([]AnswerSheetTemplate, error) {
	rows, err := queryer.QueryContext(ctx, `
SELECT id::text, tenant_id::text, exam_id::text, exam_paper_id::text, version_no, revision,
       name, status, page_count, layout, content_hash, created_by::text,
       COALESCE(locked_by::text, ''), locked_at, created_at, updated_at
FROM answer_sheet_template
WHERE tenant_id = $1::uuid AND exam_id = $2::uuid AND deleted_at IS NULL
ORDER BY version_no DESC
`, tenantID, examID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AnswerSheetTemplate{}
	for rows.Next() {
		var item AnswerSheetTemplate
		if err := scanTemplate(rows, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *PostgresStore) CreateTemplate(ctx context.Context, tenantID string, examID string, userID string, input CreateTemplateInput) (AnswerSheetTemplate, error) {
	input.Layout = NormalizeTemplateLayout(input.Layout)
	if err := ValidateTemplateInput(input.Name, input.PageCount, input.Layout); err != nil {
		return AnswerSheetTemplate{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AnswerSheetTemplate{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, examID); err != nil {
		return AnswerSheetTemplate{}, err
	}
	if err := ensureExamPaperMutableTx(ctx, tx, tenantID, examID); err != nil {
		return AnswerSheetTemplate{}, err
	}
	input.Layout, err = s.materializeTemplateOMRReferenceTx(ctx, tx, tenantID, examID, input.ExamPaperID, input.Layout)
	if err != nil {
		return AnswerSheetTemplate{}, err
	}
	if err := ValidateTemplateInput(input.Name, input.PageCount, input.Layout); err != nil {
		return AnswerSheetTemplate{}, ErrInvalidInput
	}
	layout, _ := json.Marshal(input.Layout)
	row := tx.QueryRowContext(ctx, `
INSERT INTO answer_sheet_template (tenant_id, exam_id, exam_paper_id, version_no, name, page_count, layout, content_hash, created_by)
SELECT $1::uuid, $2::uuid, p.id,
       COALESCE((SELECT MAX(version_no) + 1 FROM answer_sheet_template WHERE tenant_id = $1::uuid AND exam_id = $2::uuid), 1),
       $4, $5, $6::jsonb, $7, $8::uuid
FROM exam_paper p
WHERE p.tenant_id = $1::uuid AND p.exam_id = $2::uuid AND p.id = $3::uuid AND p.deleted_at IS NULL
RETURNING id::text, tenant_id::text, exam_id::text, exam_paper_id::text, version_no, revision,
          name, status, page_count, layout, content_hash, created_by::text,
          COALESCE(locked_by::text, ''), locked_at, created_at, updated_at
`, tenantID, examID, input.ExamPaperID, input.Name, input.PageCount, layout, stableContentHash(input.Layout), userID)
	var item AnswerSheetTemplate
	if err := scanTemplate(row, &item); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AnswerSheetTemplate{}, ErrInvalidInput
		}
		return AnswerSheetTemplate{}, err
	}
	if err := tx.Commit(); err != nil {
		return AnswerSheetTemplate{}, err
	}
	return item, nil
}

func (s *PostgresStore) UpdateTemplate(ctx context.Context, tenantID string, id string, input UpdateTemplateInput) (AnswerSheetTemplate, error) {
	input.Layout = NormalizeTemplateLayout(input.Layout)
	if err := ValidateTemplateInput(input.Name, input.PageCount, input.Layout); err != nil {
		return AnswerSheetTemplate{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AnswerSheetTemplate{}, err
	}
	defer tx.Rollback()
	var examID, examPaperID, status string
	var revision int
	err = tx.QueryRowContext(ctx, `
SELECT exam_id::text, exam_paper_id::text, status, revision
FROM answer_sheet_template
WHERE tenant_id = $1::uuid AND id = $2::uuid AND deleted_at IS NULL
FOR UPDATE
`, tenantID, id).Scan(&examID, &examPaperID, &status, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return AnswerSheetTemplate{}, ErrNotFound
	}
	if err != nil {
		return AnswerSheetTemplate{}, err
	}
	if status != "draft" {
		return AnswerSheetTemplate{}, ErrTemplateLocked
	}
	if revision != input.ExpectedRevision {
		return AnswerSheetTemplate{}, ErrConflict
	}
	if err := ensureExamPaperMutableTx(ctx, tx, tenantID, examID); err != nil {
		return AnswerSheetTemplate{}, err
	}
	input.Layout, err = s.materializeTemplateOMRReferenceTx(ctx, tx, tenantID, examID, examPaperID, input.Layout)
	if err != nil {
		return AnswerSheetTemplate{}, err
	}
	if err := ValidateTemplateInput(input.Name, input.PageCount, input.Layout); err != nil {
		return AnswerSheetTemplate{}, ErrInvalidInput
	}
	layout, _ := json.Marshal(input.Layout)
	row := tx.QueryRowContext(ctx, `
UPDATE answer_sheet_template
SET name = $3, page_count = $4, layout = $5::jsonb, content_hash = $6, revision = revision + 1, updated_at = now()
WHERE tenant_id = $1::uuid AND id = $2::uuid AND deleted_at IS NULL AND status = 'draft' AND revision = $7
RETURNING id::text, tenant_id::text, exam_id::text, exam_paper_id::text, version_no, revision,
          name, status, page_count, layout, content_hash, created_by::text,
          COALESCE(locked_by::text, ''), locked_at, created_at, updated_at
`, tenantID, id, input.Name, input.PageCount, layout, stableContentHash(input.Layout), input.ExpectedRevision)
	var item AnswerSheetTemplate
	if err := scanTemplate(row, &item); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AnswerSheetTemplate{}, ErrConflict
		}
		return AnswerSheetTemplate{}, err
	}
	if err := tx.Commit(); err != nil {
		return AnswerSheetTemplate{}, err
	}
	return item, nil
}

func (s *PostgresStore) LockTemplate(ctx context.Context, tenantID string, id string, userID string) (AnswerSheetTemplate, error) {
	return s.lockTemplateAtRevision(ctx, tenantID, id, userID, 0)
}

// LockTemplateAtRevision 将复核版本和锁定写入放在同一事务中，避免先读取后锁定时采用其他人的改框。
func (s *PostgresStore) LockTemplateAtRevision(ctx context.Context, tenantID, id, userID string, expectedRevision int) (AnswerSheetTemplate, error) {
	if expectedRevision < 1 {
		return AnswerSheetTemplate{}, ErrInvalidInput
	}
	return s.lockTemplateAtRevision(ctx, tenantID, id, userID, expectedRevision)
}

func (s *PostgresStore) lockTemplateAtRevision(ctx context.Context, tenantID, id, userID string, expectedRevision int) (AnswerSheetTemplate, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AnswerSheetTemplate{}, err
	}
	defer tx.Rollback()
	row := tx.QueryRowContext(ctx, `
SELECT id::text, tenant_id::text, exam_id::text, exam_paper_id::text, version_no, revision,
       name, status, page_count, layout, content_hash, created_by::text,
       COALESCE(locked_by::text, ''), locked_at, created_at, updated_at
FROM answer_sheet_template
WHERE tenant_id = $1::uuid AND id = $2::uuid AND deleted_at IS NULL AND status = 'draft'
FOR UPDATE
`, tenantID, id)
	var draft AnswerSheetTemplate
	if err := scanTemplate(row, &draft); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AnswerSheetTemplate{}, s.templateWriteError(ctx, tenantID, id, 0)
		}
		return AnswerSheetTemplate{}, err
	}
	if expectedRevision > 0 && draft.Revision != expectedRevision {
		return AnswerSheetTemplate{}, ErrConflict
	}
	if err := ensureExamPaperMutableTx(ctx, tx, tenantID, draft.ExamID); err != nil {
		return AnswerSheetTemplate{}, err
	}
	draft.Layout = NormalizeTemplateLayout(draft.Layout)
	draft.Layout, err = s.materializeTemplateOMRReferenceTx(ctx, tx, tenantID, draft.ExamID, draft.ExamPaperID, draft.Layout)
	if err != nil {
		return AnswerSheetTemplate{}, err
	}
	if err := ValidateTemplateInput(draft.Name, draft.PageCount, draft.Layout); err != nil {
		return AnswerSheetTemplate{}, ErrInvalidInput
	}
	questions, err := listQuestions(ctx, tx, tenantID, draft.ExamID)
	if err != nil {
		return AnswerSheetTemplate{}, err
	}
	if err := validateTemplateReadyToLock(draft.Layout, questions); err != nil {
		return AnswerSheetTemplate{}, err
	}
	layout, _ := json.Marshal(draft.Layout)
	row = tx.QueryRowContext(ctx, `
UPDATE answer_sheet_template
SET status = 'locked', locked_by = $3::uuid, locked_at = now(), layout = $4::jsonb, content_hash = $5,
    revision = revision + 1, updated_at = now()
WHERE tenant_id = $1::uuid AND id = $2::uuid AND deleted_at IS NULL AND status = 'draft'
RETURNING id::text, tenant_id::text, exam_id::text, exam_paper_id::text, version_no, revision,
          name, status, page_count, layout, content_hash, created_by::text,
          COALESCE(locked_by::text, ''), locked_at, created_at, updated_at
`, tenantID, id, userID, layout, stableContentHash(draft.Layout))
	var item AnswerSheetTemplate
	if err := scanTemplate(row, &item); err != nil {
		return AnswerSheetTemplate{}, err
	}
	// 锁定模板同时把区域同步到正式题目；任一区域引用无效时整笔事务回滚。
	for _, page := range item.Layout.Pages {
		individual := map[string]bool{}
		for _, region := range page.QuestionRegions {
			individual[region.QuestionID] = true
			result, err := tx.ExecContext(ctx, `
UPDATE question
SET answer_area = jsonb_build_object(
  'page', $4::int,
  'x', $5::float8,
  'y', $6::float8,
  'width', $7::float8,
  'height', $8::float8,
  'option_regions', $9::jsonb
) || jsonb_strip_nulls(jsonb_build_object('option_coordinate_space', NULLIF($10::text, ''))), updated_at = now()
WHERE tenant_id = $1::uuid AND exam_id = $2::uuid AND id = $3::uuid AND deleted_at IS NULL AND status <> 'deleted'
`, tenantID, item.ExamID, region.QuestionID, page.PageNo, region.X, region.Y, region.Width, region.Height, mustJSON(region.OptionRegions), region.OptionCoordinateSpace)
			if err != nil {
				return AnswerSheetTemplate{}, err
			}
			count, _ := result.RowsAffected()
			if count != 1 {
				return AnswerSheetTemplate{}, ErrInvalidInput
			}
		}
		for _, group := range page.GradingGroups {
			for _, questionID := range group.QuestionIDs {
				if individual[questionID] {
					continue
				}
				// 新模板不再有子框时清掉旧版本的精确几何，只留下明确的整区上下文引用。
				result, err := tx.ExecContext(ctx, `UPDATE question SET answer_area=jsonb_build_object('page',$4::int,'answer_scope','grading_group','grading_group_id',$5::text),updated_at=now() WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND id=$3::uuid AND deleted_at IS NULL AND status<>'deleted'`, tenantID, item.ExamID, questionID, page.PageNo, group.GroupID)
				if err != nil {
					return AnswerSheetTemplate{}, err
				}
				if count, _ := result.RowsAffected(); count != 1 {
					return AnswerSheetTemplate{}, ErrInvalidInput
				}
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return AnswerSheetTemplate{}, err
	}
	return item, nil
}

func (s *PostgresStore) CloneTemplate(ctx context.Context, tenantID string, id string, userID string) (AnswerSheetTemplate, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id::text, tenant_id::text, exam_id::text, exam_paper_id::text, version_no, revision,
       name, status, page_count, layout, content_hash, created_by::text,
       COALESCE(locked_by::text, ''), locked_at, created_at, updated_at
FROM answer_sheet_template
WHERE tenant_id = $1::uuid AND id = $2::uuid AND deleted_at IS NULL
`, tenantID, id)
	var source AnswerSheetTemplate
	if err := scanTemplate(row, &source); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AnswerSheetTemplate{}, ErrNotFound
		}
		return AnswerSheetTemplate{}, err
	}
	cloned, err := s.CreateTemplate(ctx, tenantID, source.ExamID, userID, CreateTemplateInput{
		ExamPaperID: source.ExamPaperID,
		Name:        source.Name + " 副本",
		PageCount:   source.PageCount,
		Layout:      source.Layout,
	})
	if err != nil {
		return AnswerSheetTemplate{}, err
	}
	// 模板创建已单独提交；继承校准失败时副本仍存在，调用方不能把错误当成没有创建。
	if err := s.inheritApprovedOMRCalibrationForClone(ctx, tenantID, source.ID, cloned); err != nil {
		return AnswerSheetTemplate{}, err
	}
	return cloned, nil
}

func (s *PostgresStore) inheritApprovedOMRCalibrationForClone(ctx context.Context, tenantID, sourceTemplateID string, cloned AnswerSheetTemplate) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// 继承的批准仍绑定来源证据哈希，并显式记录该来源。
	// 运行时查询还要求副本的内容、配置和参考哈希保持相同。
	if _, err = tx.ExecContext(ctx, `
WITH copied_session AS (
  INSERT INTO omr_calibration_session (
    tenant_id,template_id,template_content_hash,scope_type,question_id,question_ids,question_type,
    profile_version,profile_hash,reference_file_asset_id,reference_sha256,option_labels,sample_seed,
    sample_count,minimum_samples,minimum_samples_per_option,minimum_samples_per_stratum,
    minimum_confidence,status,created_by,created_at,inherited_from_session_id,
    approved_by,approved_at,approval_note,evidence_hash,updated_at
  )
  SELECT s.tenant_id,$3::uuid,s.template_content_hash,s.scope_type,s.question_id,s.question_ids,s.question_type,
    s.profile_version,s.profile_hash,s.reference_file_asset_id,s.reference_sha256,s.option_labels,s.sample_seed,
    s.sample_count,s.minimum_samples,s.minimum_samples_per_option,s.minimum_samples_per_stratum,
    s.minimum_confidence,'approved',s.created_by,now(),s.id,
    s.approved_by,s.approved_at,s.approval_note,s.evidence_hash,now()
  FROM omr_calibration_session s
  WHERE s.tenant_id=$1::uuid AND s.template_id=$2::uuid
    AND s.template_content_hash=$4 AND s.scope_type='template'
    AND s.status='approved' AND s.deleted_at IS NULL
  ORDER BY s.approved_at DESC
  LIMIT 1
  RETURNING id,inherited_from_session_id
)
INSERT INTO omr_calibration_case (
  tenant_id,calibration_session_id,omr_run_id,answer_segment_id,question_id,question_no,
  question_type,option_labels,sample_stratum,crop_sha256,observed_decision,observed_options,
  observed_confidence,measurements,expected_options,matches,labeled_by,labeled_at,created_at,observed_result_gate
)
SELECT c.tenant_id,copy.id,c.omr_run_id,c.answer_segment_id,c.question_id,c.question_no,
  c.question_type,c.option_labels,c.sample_stratum,c.crop_sha256,c.observed_decision,c.observed_options,
  c.observed_confidence,c.measurements,c.expected_options,c.matches,c.labeled_by,c.labeled_at,c.created_at,c.observed_result_gate
FROM copied_session copy
JOIN omr_calibration_case c
  ON c.tenant_id=$1::uuid AND c.calibration_session_id=copy.inherited_from_session_id
`, tenantID, sourceTemplateID, cloned.ID, cloned.ContentHash); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) templateWriteError(ctx context.Context, tenantID string, id string, expectedRevision int) error {
	var status string
	var revision int
	err := s.db.QueryRowContext(ctx, `SELECT status, revision FROM answer_sheet_template WHERE tenant_id = $1::uuid AND id = $2::uuid AND deleted_at IS NULL`, tenantID, id).Scan(&status, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if status != "draft" {
		return ErrTemplateLocked
	}
	if expectedRevision != 0 && revision != expectedRevision {
		return ErrConflict
	}
	return ErrConflict
}

type templateScanner interface{ Scan(dest ...any) error }

func scanTemplate(row templateScanner, item *AnswerSheetTemplate) error {
	var layout []byte
	var lockedAt sql.NullTime
	err := row.Scan(&item.ID, &item.TenantID, &item.ExamID, &item.ExamPaperID, &item.VersionNo, &item.Revision,
		&item.Name, &item.Status, &item.PageCount, &layout, &item.ContentHash, &item.CreatedBy,
		&item.LockedBy, &lockedAt, &item.CreatedAt, &item.UpdatedAt)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(layout, &item.Layout); err != nil {
		return fmt.Errorf("decode template layout: %w", err)
	}
	if lockedAt.Valid {
		item.LockedAt = &lockedAt.Time
	}
	return nil
}

func mustJSON(value any) []byte {
	raw, _ := json.Marshal(value)
	return raw
}

func (s *PostgresStore) materializeTemplateOMRReferenceTx(ctx context.Context, tx *sql.Tx, tenantID, examID, examPaperID string, layout TemplateLayout) (TemplateLayout, error) {
	if layout.OMRProfile.Mode != OMRProfileModeTemplateDifference && layout.EditorState == nil {
		return BindTemplateOMRReference(layout, TemplateOMRReference{}), nil
	}
	var reference TemplateOMRReference
	err := tx.QueryRowContext(ctx, `
SELECT fa.id::text, fa.hash_sha256, fa.content_type
FROM exam_paper ep
JOIN file_asset fa ON fa.tenant_id = ep.tenant_id AND fa.id = ep.file_asset_id AND fa.deleted_at IS NULL
WHERE ep.tenant_id = $1::uuid AND ep.exam_id = $2::uuid AND ep.id = $3::uuid AND ep.deleted_at IS NULL
`, tenantID, examID, examPaperID).Scan(&reference.FileAssetID, &reference.HashSHA256, &reference.ContentType)
	if errors.Is(err, sql.ErrNoRows) {
		return TemplateLayout{}, ErrInvalidInput
	}
	if err != nil {
		return TemplateLayout{}, err
	}
	if err := validateTemplateEditorSource(layout, reference.HashSHA256); err != nil {
		return TemplateLayout{}, err
	}
	if layout.OMRProfile.Mode != OMRProfileModeTemplateDifference {
		return BindTemplateOMRReference(layout, TemplateOMRReference{}), nil
	}
	reference.Source = OMRReferenceSourceExamPaper
	if err := ValidateTemplateOMRReference(reference); err != nil {
		return TemplateLayout{}, ErrInvalidInput
	}
	return BindTemplateOMRReference(layout, reference), nil
}
