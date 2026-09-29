package paper

import (
	"context"
	"database/sql"
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
	return scanExamTemplateBinding(s.db.QueryRowContext(ctx, `SELECT `+examTemplateBindingColumns+` FROM exam_answer_sheet_template_binding WHERE tenant_id=$1::uuid AND exam_id=$2::uuid`, tenantID, examID))
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
	err = tx.QueryRowContext(ctx, `SELECT content_hash,status FROM answer_sheet_template WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND id=$3::uuid AND deleted_at IS NULL`, tenantID, examID, input.TemplateID).Scan(&templateHash, &templateStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return ExamTemplateBinding{}, ErrNotFound
	}
	if err != nil {
		return ExamTemplateBinding{}, err
	}
	if templateStatus != "locked" {
		return ExamTemplateBinding{}, ErrTemplateNotLocked
	}
	// 绑定有独立修订号：首次绑定期望 0，后续替换需匹配当前值；不改变模板本身的锁定状态。
	var currentRevision int
	err = tx.QueryRowContext(ctx, `SELECT revision FROM exam_answer_sheet_template_binding WHERE tenant_id=$1::uuid AND exam_id=$2::uuid FOR UPDATE`, tenantID, examID).Scan(&currentRevision)
	switch {
	case errors.Is(err, sql.ErrNoRows) && input.ExpectedRevision != 0:
		return ExamTemplateBinding{}, ErrConflict
	case err == nil && currentRevision != input.ExpectedRevision:
		return ExamTemplateBinding{}, ErrConflict
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return ExamTemplateBinding{}, err
	}
	var item ExamTemplateBinding
	if errors.Is(err, sql.ErrNoRows) {
		item, err = scanExamTemplateBinding(tx.QueryRowContext(ctx, `INSERT INTO exam_answer_sheet_template_binding (tenant_id,exam_id,template_id,template_content_hash,mode,source,bound_by) VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5,'manual',$6::uuid) RETURNING `+examTemplateBindingColumns, tenantID, examID, input.TemplateID, templateHash, mode, userID))
	} else {
		item, err = scanExamTemplateBinding(tx.QueryRowContext(ctx, `UPDATE exam_answer_sheet_template_binding SET template_id=$3::uuid,template_content_hash=$4,mode=$5,source='manual',bound_by=$6::uuid,bound_at=now(),revision=revision+1,updated_at=now() WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND revision=$7 RETURNING `+examTemplateBindingColumns, tenantID, examID, input.TemplateID, templateHash, mode, userID, input.ExpectedRevision))
	}
	if err != nil {
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
	item, err := scanExamTemplateBinding(s.db.QueryRowContext(ctx, `DELETE FROM exam_answer_sheet_template_binding WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND revision=$3 RETURNING `+examTemplateBindingColumns, tenantID, examID, input.ExpectedRevision))
	if errors.Is(err, ErrNotFound) {
		if _, lookupErr := s.GetExamTemplateBinding(ctx, tenantID, examID); lookupErr == nil {
			return ExamTemplateBinding{}, ErrConflict
		}
	}
	return item, err
}
