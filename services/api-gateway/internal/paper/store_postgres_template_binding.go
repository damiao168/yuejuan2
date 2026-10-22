package paper

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

const examTemplateBindingColumns = `id::text,tenant_id::text,exam_id::text,template_id::text,template_content_hash,mode,source,revision,bound_by::text,bound_at,updated_at`

func scanExamTemplateBinding(row templateScanner) (ExamTemplateBinding, error) {
	var item ExamTemplateBinding
	err := row.Scan(&item.ID, &item.TenantID, &item.ExamID, &item.TemplateID, &item.TemplateContentHash, &item.Mode, &item.Source, &item.Revision, &item.BoundBy, &item.BoundAt, &item.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ExamTemplateBinding{}, ErrNotFound
	}
	return item, err
}

func (s *PostgresStore) GetExamTemplateBinding(ctx context.Context, tenantID, examID string) (ExamTemplateBinding, error) {
	state, err := s.GetExamTemplateBindingState(ctx, tenantID, examID)
	if err != nil {
		return ExamTemplateBinding{}, err
	}
	if state.Binding == nil {
		return ExamTemplateBinding{}, ErrNotFound
	}
	return *state.Binding, nil
}

func (s *PostgresStore) GetExamTemplateBindingState(ctx context.Context, tenantID, examID string) (ExamTemplateBindingState, error) {
	var state ExamTemplateBindingState
	var bindingJSON []byte
	// 一条语句读取绑定和游标，避免解绑或重建发生在两次读取之间，返回无法提交的混合版本。
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(r.last_revision,b.revision,0),to_jsonb(b)
FROM (SELECT 1) AS anchor
LEFT JOIN exam_answer_sheet_template_binding_revision r ON r.tenant_id=$1::uuid AND r.exam_id=$2::uuid
LEFT JOIN exam_answer_sheet_template_binding b ON b.tenant_id=$1::uuid AND b.exam_id=$2::uuid`, tenantID, examID).Scan(&state.Revision, &bindingJSON)
	if err != nil {
		return ExamTemplateBindingState{}, err
	}
	if len(bindingJSON) > 0 {
		if err = json.Unmarshal(bindingJSON, &state.Binding); err != nil {
			return ExamTemplateBindingState{}, err
		}
	}
	return state, nil
}

func (s *PostgresStore) BindExamTemplate(ctx context.Context, tenantID, examID, userID string, input BindExamTemplateInput) (ExamTemplateBinding, error) {
	mode, ok := normalizeBindingMode(input.Mode)
	if !ok || input.TemplateID == "" || input.ExpectedRevision < 0 {
		return ExamTemplateBinding{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ExamTemplateBinding{}, err
	}
	defer tx.Rollback()
	var templateHash, templateStatus string
	var rawLayout []byte
	err = tx.QueryRowContext(ctx, `SELECT content_hash,status,layout FROM answer_sheet_template WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND id=$3::uuid AND deleted_at IS NULL`, tenantID, examID, input.TemplateID).Scan(&templateHash, &templateStatus, &rawLayout)
	if errors.Is(err, sql.ErrNoRows) {
		return ExamTemplateBinding{}, ErrNotFound
	}
	if err != nil {
		return ExamTemplateBinding{}, err
	}
	if templateStatus != "locked" {
		return ExamTemplateBinding{}, ErrTemplateNotLocked
	}
	var layout TemplateLayout
	if err := json.Unmarshal(rawLayout, &layout); err != nil {
		return ExamTemplateBinding{}, ErrInvalidInput
	}
	if err := validateTemplateReviewResolved(layout); err != nil {
		return ExamTemplateBinding{}, err
	}
	// 修订游标独立于绑定行；解除绑定后游标仍保留，防止旧页面在重建绑定时复用旧修订号。
	if _, err = tx.ExecContext(ctx, `INSERT INTO exam_answer_sheet_template_binding_revision (tenant_id,exam_id,last_revision) VALUES ($1::uuid,$2::uuid,0) ON CONFLICT (tenant_id,exam_id) DO NOTHING`, tenantID, examID); err != nil {
		return ExamTemplateBinding{}, err
	}
	var currentRevision int
	if err = tx.QueryRowContext(ctx, `SELECT last_revision FROM exam_answer_sheet_template_binding_revision WHERE tenant_id=$1::uuid AND exam_id=$2::uuid FOR UPDATE`, tenantID, examID).Scan(&currentRevision); err != nil {
		return ExamTemplateBinding{}, err
	}
	if currentRevision != input.ExpectedRevision {
		return ExamTemplateBinding{}, ErrConflict
	}
	nextRevision := currentRevision + 1
	var boundRevision sql.NullInt64
	if err = tx.QueryRowContext(ctx, `SELECT revision FROM exam_answer_sheet_template_binding WHERE tenant_id=$1::uuid AND exam_id=$2::uuid FOR UPDATE`, tenantID, examID).Scan(&boundRevision); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ExamTemplateBinding{}, err
	}
	if boundRevision.Valid && int(boundRevision.Int64) != currentRevision {
		return ExamTemplateBinding{}, ErrConflict
	}
	var item ExamTemplateBinding
	if !boundRevision.Valid {
		item, err = scanExamTemplateBinding(tx.QueryRowContext(ctx, `INSERT INTO exam_answer_sheet_template_binding (tenant_id,exam_id,template_id,template_content_hash,mode,source,revision,bound_by) VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5,'manual',$7,$6::uuid) RETURNING `+examTemplateBindingColumns, tenantID, examID, input.TemplateID, templateHash, mode, userID, nextRevision))
	} else {
		item, err = scanExamTemplateBinding(tx.QueryRowContext(ctx, `UPDATE exam_answer_sheet_template_binding SET template_id=$3::uuid,template_content_hash=$4,mode=$5,source='manual',bound_by=$6::uuid,bound_at=now(),revision=$7,updated_at=now() WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND revision=$8 RETURNING `+examTemplateBindingColumns, tenantID, examID, input.TemplateID, templateHash, mode, userID, nextRevision, input.ExpectedRevision))
	}
	if err != nil {
		return ExamTemplateBinding{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE exam_answer_sheet_template_binding_revision SET last_revision=$3,updated_at=now() WHERE tenant_id=$1::uuid AND exam_id=$2::uuid`, tenantID, examID, nextRevision); err != nil {
		return ExamTemplateBinding{}, err
	}
	if err = tx.Commit(); err != nil {
		return ExamTemplateBinding{}, err
	}
	return item, nil
}

func (s *PostgresStore) UnbindExamTemplate(ctx context.Context, tenantID, examID string, input UnbindExamTemplateInput) (ExamTemplateBinding, error) {
	if strings.TrimSpace(input.Reason) == "" {
		return ExamTemplateBinding{}, ErrInvalidInput
	}
	if input.ExpectedRevision < 1 {
		return ExamTemplateBinding{}, ErrConflict
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ExamTemplateBinding{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `INSERT INTO exam_answer_sheet_template_binding_revision (tenant_id,exam_id,last_revision) VALUES ($1::uuid,$2::uuid,0) ON CONFLICT (tenant_id,exam_id) DO NOTHING`, tenantID, examID); err != nil {
		return ExamTemplateBinding{}, err
	}
	var currentRevision int
	if err = tx.QueryRowContext(ctx, `SELECT last_revision FROM exam_answer_sheet_template_binding_revision WHERE tenant_id=$1::uuid AND exam_id=$2::uuid FOR UPDATE`, tenantID, examID).Scan(&currentRevision); err != nil {
		return ExamTemplateBinding{}, err
	}
	if currentRevision != input.ExpectedRevision {
		return ExamTemplateBinding{}, ErrConflict
	}
	item, err := scanExamTemplateBinding(tx.QueryRowContext(ctx, `DELETE FROM exam_answer_sheet_template_binding WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND revision=$3 RETURNING `+examTemplateBindingColumns, tenantID, examID, input.ExpectedRevision))
	if errors.Is(err, ErrNotFound) {
		return ExamTemplateBinding{}, ErrNotFound
	}
	if err != nil {
		return ExamTemplateBinding{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE exam_answer_sheet_template_binding_revision SET last_revision=$3,updated_at=now() WHERE tenant_id=$1::uuid AND exam_id=$2::uuid`, tenantID, examID, currentRevision+1); err != nil {
		return ExamTemplateBinding{}, err
	}
	if err = tx.Commit(); err != nil {
		return ExamTemplateBinding{}, err
	}
	return item, nil
}
