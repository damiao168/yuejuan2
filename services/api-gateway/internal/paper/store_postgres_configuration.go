package paper

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/exam"
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
		for _, region := range page.QuestionRegions {
			result, err := tx.ExecContext(ctx, `
UPDATE question
SET answer_area = jsonb_build_object(
  'page', $4::int,
  'x', $5::float8,
  'y', $6::float8,
  'width', $7::float8,
  'height', $8::float8,
  'option_regions', $9::jsonb
), updated_at = now()
WHERE tenant_id = $1::uuid AND exam_id = $2::uuid AND id = $3::uuid AND deleted_at IS NULL AND status <> 'deleted'
`, tenantID, item.ExamID, region.QuestionID, page.PageNo, region.X, region.Y, region.Width, region.Height, mustJSON(region.OptionRegions))
			if err != nil {
				return AnswerSheetTemplate{}, err
			}
			count, _ := result.RowsAffected()
			if count != 1 {
				return AnswerSheetTemplate{}, ErrInvalidInput
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
	// The inherited approval remains bound to the source evidence hash and
	// records that provenance explicitly. Runtime lookup also requires the
	// clone's content/profile/reference hashes to remain identical.
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
  observed_confidence,measurements,expected_options,matches,labeled_by,labeled_at,created_at
)
SELECT c.tenant_id,copy.id,c.omr_run_id,c.answer_segment_id,c.question_id,c.question_no,
  c.question_type,c.option_labels,c.sample_stratum,c.crop_sha256,c.observed_decision,c.observed_options,
  c.observed_confidence,c.measurements,c.expected_options,c.matches,c.labeled_by,c.labeled_at,c.created_at
FROM copied_session copy
JOIN omr_calibration_case c
  ON c.tenant_id=$1::uuid AND c.calibration_session_id=copy.inherited_from_session_id
`, tenantID, sourceTemplateID, cloned.ID, cloned.ContentHash); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PostgresStore) Readiness(ctx context.Context, tenantID string, examID string) (ReadinessResult, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return ReadinessResult{}, err
	}
	defer tx.Rollback()
	result, status, _, err := calculateReadiness(ctx, tx, tenantID, examID)
	if err != nil {
		return ReadinessResult{}, err
	}
	var confirmedAt time.Time
	var confirmedBy string
	var snapshotID string
	var importSnapshotAvailable bool
	err = tx.QueryRowContext(ctx, `
SELECT confirmed_at, confirmed_by::text, id::text, import_snapshot_json IS NOT NULL
FROM exam_readiness_snapshot
WHERE tenant_id = $1::uuid AND exam_id = $2::uuid AND status = 'passed' AND configuration_hash = $3
ORDER BY confirmed_at DESC LIMIT 1
`, tenantID, examID, result.ConfigurationHash).Scan(&confirmedAt, &confirmedBy, &snapshotID, &importSnapshotAvailable)
	if err == nil {
		result.Confirmed, result.ConfirmedAt, result.ConfirmedBy = status == "ready" || status == "collecting", &confirmedAt, confirmedBy
		result.SnapshotID, result.ImportSnapshotAvailable = snapshotID, importSnapshotAvailable
	} else if !errors.Is(err, sql.ErrNoRows) {
		return ReadinessResult{}, err
	}
	if err = tx.Commit(); err != nil {
		return ReadinessResult{}, err
	}
	return result, nil
}

func (s *PostgresStore) ConfirmReadiness(ctx context.Context, tenantID string, examID string, userID string) (ReadinessResult, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return ReadinessResult{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, examID); err != nil {
		return ReadinessResult{}, err
	}
	var lockedStatus string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM exam WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, examID).Scan(&lockedStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ReadinessResult{}, ErrNotFound
		}
		return ReadinessResult{}, err
	}
	if lockedStatus != "draft" && lockedStatus != "configured" && lockedStatus != "ready" {
		return ReadinessResult{}, ErrNotReady
	}
	// Every paper-configuration write takes the same exam row lock. Read all
	// readiness inputs only after that lock is held so the semantic snapshot,
	// candidate freeze, and status transition describe one committed version.
	result, status, candidateIDs, err := calculateReadiness(ctx, tx, tenantID, examID)
	if err != nil {
		return ReadinessResult{}, err
	}
	if !result.Ready || lockedStatus != status {
		return result, ErrNotReady
	}
	if lockedStatus == "draft" || lockedStatus == "configured" {
		if err := exam.RebuildCandidateSnapshot(ctx, tx, tenantID, examID); err != nil {
			return ReadinessResult{}, err
		}
		var frozenCandidateIDsJSON []byte
		if err := tx.QueryRowContext(ctx, `
SELECT COALESCE(jsonb_agg(candidate.student_id::text ORDER BY candidate.student_id::text), '[]'::jsonb)
FROM exam_candidate_snapshot candidate
WHERE candidate.tenant_id=$1::uuid AND candidate.exam_id=$2::uuid
`, tenantID, examID).Scan(&frozenCandidateIDsJSON); err != nil {
			return ReadinessResult{}, err
		}
		var frozenCandidateIDs []string
		if err := json.Unmarshal(frozenCandidateIDsJSON, &frozenCandidateIDs); err != nil {
			return ReadinessResult{}, err
		}
		if !slices.Equal(frozenCandidateIDs, candidateIDs) {
			return result, ErrConflict
		}
	}
	checks, _ := json.Marshal(result.Checks)
	questions, err := listQuestions(ctx, tx, tenantID, examID)
	if err != nil {
		return ReadinessResult{}, err
	}
	if _, err := tx.ExecContext(ctx, `
UPDATE exam_readiness_snapshot SET status = 'invalidated', invalidated_at = now(), invalidation_reason = 'superseded by new confirmation'
WHERE tenant_id = $1::uuid AND exam_id = $2::uuid AND status = 'passed'
`, tenantID, examID); err != nil {
		return ReadinessResult{}, err
	}
	updated, err := tx.ExecContext(ctx, `UPDATE exam SET status = 'ready', updated_at = now() WHERE tenant_id = $1::uuid AND id = $2::uuid AND status IN ('draft', 'configured', 'ready')`, tenantID, examID)
	if err != nil {
		return ReadinessResult{}, err
	}
	count, _ := updated.RowsAffected()
	if count != 1 {
		return result, ErrNotReady
	}
	// The ready transition freezes assessment facts in its database trigger.
	// Bind those exact identities into the content companion before committing.
	importSnapshot := newReadinessImportSnapshot(result.ConfigurationHash, questions)
	for index := range importSnapshot.Questions {
		question := &importSnapshot.Questions[index]
		if err := tx.QueryRowContext(ctx, `SELECT id::text,content_hash FROM exam_question_snapshot WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND question_id=$3::uuid ORDER BY snapshot_version DESC LIMIT 1`, tenantID, examID, question.ID).Scan(&question.AssessmentSnapshotID, &question.AssessmentSnapshotHash); err != nil {
			return ReadinessResult{}, err
		}
	}
	importSnapshotJSON, err := json.Marshal(importSnapshot)
	if err != nil {
		return ReadinessResult{}, err
	}
	importSnapshotHash := stableContentHash(importSnapshot)
	now := time.Now().UTC()
	var snapshotID string
	if err := tx.QueryRowContext(ctx, `
	INSERT INTO exam_readiness_snapshot (
	  tenant_id, exam_id, configuration_hash, status, checks, confirmed_by, confirmed_at,
	  import_snapshot_schema_version, import_snapshot_hash, import_snapshot_json
	)
	VALUES ($1::uuid, $2::uuid, $3, 'passed', $4::jsonb, $5::uuid, $6, 1, $7, $8::jsonb)
	RETURNING confirmed_at,id::text
	`, tenantID, examID, result.ConfigurationHash, checks, userID, now, importSnapshotHash, importSnapshotJSON).Scan(&now, &snapshotID); err != nil {
		return ReadinessResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return ReadinessResult{}, err
	}
	result.Confirmed, result.ConfirmedAt, result.ConfirmedBy = true, &now, userID
	result.SnapshotID, result.ImportSnapshotAvailable = snapshotID, true
	return result, nil
}

func (s *PostgresStore) StartCollection(ctx context.Context, tenantID string, examID string, _ string) (ReadinessResult, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return ReadinessResult{}, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, examID); err != nil {
		return ReadinessResult{}, err
	}
	var lockedStatus string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM exam WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, examID).Scan(&lockedStatus); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ReadinessResult{}, ErrNotFound
		}
		return ReadinessResult{}, err
	}
	if lockedStatus != "ready" {
		return ReadinessResult{}, ErrNotReady
	}
	result, status, _, err := calculateReadiness(ctx, tx, tenantID, examID)
	if err != nil {
		return ReadinessResult{}, err
	}
	if !result.Ready || status != "ready" {
		return result, ErrNotReady
	}
	var confirmedAt time.Time
	var confirmedBy string
	var snapshotID string
	var importSnapshotAvailable bool
	if err := tx.QueryRowContext(ctx, `
SELECT confirmed_at, confirmed_by::text, id::text, import_snapshot_json IS NOT NULL FROM exam_readiness_snapshot
WHERE tenant_id = $1::uuid AND exam_id = $2::uuid AND status = 'passed' AND configuration_hash = $3
ORDER BY confirmed_at DESC LIMIT 1
`, tenantID, examID, result.ConfigurationHash).Scan(&confirmedAt, &confirmedBy, &snapshotID, &importSnapshotAvailable); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return result, ErrNotReady
		}
		return ReadinessResult{}, err
	}
	updated, err := tx.ExecContext(ctx, `UPDATE exam SET status = 'collecting', updated_at = now() WHERE tenant_id = $1::uuid AND id = $2::uuid AND status = 'ready'`, tenantID, examID)
	if err != nil {
		return ReadinessResult{}, err
	}
	count, _ := updated.RowsAffected()
	if count != 1 {
		return result, ErrNotReady
	}
	if err := tx.Commit(); err != nil {
		return ReadinessResult{}, err
	}
	result.Confirmed, result.ConfirmedAt, result.ConfirmedBy = true, &confirmedAt, confirmedBy
	result.SnapshotID, result.ImportSnapshotAvailable = snapshotID, importSnapshotAvailable
	return result, nil
}

// 准备阶段使用当前在读名册，后续阶段使用冻结的考试考生快照；摘要包含具体人员身份。
func calculateReadiness(ctx context.Context, queryer postgresQueryer, tenantID string, examID string) (ReadinessResult, string, []string, error) {
	var total float64
	var status string
	var classIDsJSON, candidateIDsJSON []byte
	err := queryer.QueryRowContext(ctx, `
SELECT e.total_score::float8, e.status,
       COALESCE((
         SELECT jsonb_agg(scope.class_id ORDER BY scope.class_id)
         FROM (
           SELECT DISTINCT ec.class_id::text AS class_id
           FROM exam_class ec
           WHERE ec.tenant_id=e.tenant_id AND ec.exam_id=e.id AND ec.deleted_at IS NULL
         ) scope
       ), '[]'::jsonb),
       CASE WHEN e.status IN ('draft','configured') THEN
         COALESCE((
           SELECT jsonb_agg(scope.student_id ORDER BY scope.student_id)
           FROM (
             SELECT DISTINCT st.id::text AS student_id
             FROM exam_class ec
             JOIN school_class cls ON cls.tenant_id=ec.tenant_id AND cls.id=ec.class_id AND cls.deleted_at IS NULL
             JOIN student_enrollment enrollment ON enrollment.tenant_id=ec.tenant_id AND enrollment.class_id=ec.class_id AND enrollment.academic_year_id=cls.academic_year_id AND enrollment.status='enrolled' AND enrollment.end_date IS NULL AND enrollment.deleted_at IS NULL
             JOIN student st ON st.tenant_id=enrollment.tenant_id AND st.id=enrollment.student_id AND st.status='active' AND st.deleted_at IS NULL
             WHERE ec.tenant_id=e.tenant_id AND ec.exam_id=e.id AND ec.deleted_at IS NULL
           ) scope
         ), '[]'::jsonb)
       ELSE
         COALESCE((
           SELECT jsonb_agg(candidate.student_id::text ORDER BY candidate.student_id::text)
           FROM exam_candidate_snapshot candidate
           WHERE candidate.tenant_id=e.tenant_id AND candidate.exam_id=e.id
         ), '[]'::jsonb)
       END
FROM exam e
WHERE e.tenant_id = $1::uuid AND e.id = $2::uuid AND e.deleted_at IS NULL
`, tenantID, examID).Scan(&total, &status, &classIDsJSON, &candidateIDsJSON)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ReadinessResult{}, "", nil, ErrNotFound
		}
		return ReadinessResult{}, "", nil, err
	}
	var classIDs, candidateIDs []string
	if err := json.Unmarshal(classIDsJSON, &classIDs); err != nil {
		return ReadinessResult{}, "", nil, err
	}
	if err := json.Unmarshal(candidateIDsJSON, &candidateIDs); err != nil {
		return ReadinessResult{}, "", nil, err
	}
	papers, err := listPapers(ctx, queryer, tenantID, examID)
	if err != nil {
		return ReadinessResult{}, "", nil, err
	}
	questions, err := listQuestions(ctx, queryer, tenantID, examID)
	if err != nil {
		return ReadinessResult{}, "", nil, err
	}
	templates, err := listTemplates(ctx, queryer, tenantID, examID)
	if err != nil {
		return ReadinessResult{}, "", nil, err
	}
	return buildReadinessForScope(total, classIDs, candidateIDs, papers, questions, templates), status, candidateIDs, nil
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
	if layout.OMRProfile.Mode != OMRProfileModeTemplateDifference {
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
	reference.Source = OMRReferenceSourceExamPaper
	if err := ValidateTemplateOMRReference(reference); err != nil {
		return TemplateLayout{}, ErrInvalidInput
	}
	return BindTemplateOMRReference(layout, reference), nil
}
