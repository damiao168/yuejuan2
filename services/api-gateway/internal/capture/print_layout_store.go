package capture

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// ConfirmStudentPrintLayout 仅补齐未扫描旧批次的首次位置确认。已有确认只允许相同内容重放。
func (s *PostgresStore) ConfirmStudentPrintLayout(ctx context.Context, tenantID, printBatchID string, input StudentPrintLayout) (StudentPrintLayout, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return StudentPrintLayout{}, err
	}
	defer tx.Rollback()
	var templateID, templateHash string
	var templateRaw, storedRaw []byte
	var sheetCount, pageCount int
	err = tx.QueryRowContext(ctx, `SELECT b.template_id::text,b.template_content_hash,t.layout,b.sheet_count,b.page_count,b.print_layout
FROM answer_sheet_print_batch b JOIN answer_sheet_template t ON t.tenant_id=b.tenant_id AND t.id=b.template_id
  AND t.content_hash=b.template_content_hash AND t.status='locked' AND t.deleted_at IS NULL
WHERE b.tenant_id=$1 AND b.id=$2::uuid FOR UPDATE OF b NOWAIT`, tenantID, printBatchID).Scan(&templateID, &templateHash, &templateRaw, &sheetCount, &pageCount, &storedRaw)
	if err != nil {
		return StudentPrintLayout{}, printLayoutWriteError(mapNotFound(err))
	}
	// 扫描、作废和补打都可能先持有纸张锁。此处不能持批次锁反向等待。
	rows, err := tx.QueryContext(ctx, `SELECT status FROM answer_sheet_print_sheet WHERE tenant_id=$1 AND print_batch_id=$2::uuid ORDER BY id FOR UPDATE NOWAIT`, tenantID, printBatchID)
	if err != nil {
		return StudentPrintLayout{}, printLayoutWriteError(err)
	}
	count, allIssued := 0, true
	for rows.Next() {
		var status string
		if err = rows.Scan(&status); err != nil {
			rows.Close()
			return StudentPrintLayout{}, printLayoutWriteError(err)
		}
		count++
		allIssued = allIssued && status == "issued"
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return StudentPrintLayout{}, printLayoutWriteError(err)
	}
	if count != sheetCount || !allIssued {
		return StudentPrintLayout{}, ErrInvalidTransition
	}
	layout, err := s.prepareStudentPrintPackageTx(ctx, tx, tenantID, templateID, templateHash, templateRaw, sheetCount, pageCount, &input)
	if err != nil {
		return StudentPrintLayout{}, err
	}
	stored, err := decodeStudentPrintLayout(storedRaw)
	if err != nil {
		return StudentPrintLayout{}, err
	}
	if stored != nil && !sameStudentPrintLayout(stored, layout) {
		return StudentPrintLayout{}, ErrConflict
	}
	if stored == nil {
		if _, err = tx.ExecContext(ctx, `UPDATE answer_sheet_print_batch SET print_layout=$3::jsonb WHERE tenant_id=$1 AND id=$2::uuid AND print_layout IS NULL`, tenantID, printBatchID, printLayoutJSON(layout)); err != nil {
			return StudentPrintLayout{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return StudentPrintLayout{}, err
	}
	return *layout, nil
}

func printLayoutWriteError(err error) error {
	var postgres *pgconn.PgError
	if errors.As(err, &postgres) && postgres.Code == "55P03" {
		return ErrConflict
	}
	return err
}
