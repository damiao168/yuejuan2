package capture

import (
	"context"
	"database/sql"
)

// GetStudentPrintContext returns the immutable print history together with the
// current exam roster. It lives under exam:manage so the printing workflow
// does not depend on score-management permissions.
func (s *PostgresStore) GetStudentPrintContext(ctx context.Context, tenantID, templateID string) (StudentPrintContext, error) {
	out := StudentPrintContext{
		TemplateID: templateID,
		Candidates: []StudentPrintCandidate{},
		Batches:    []StudentPrintBatchSummary{},
	}
	if err := s.db.QueryRowContext(ctx, `
SELECT exam_id::text,content_hash
FROM answer_sheet_template
WHERE tenant_id=$1 AND id=$2::uuid AND status='locked' AND deleted_at IS NULL
`, tenantID, templateID).Scan(&out.ExamID, &out.TemplateContentHash); err != nil {
		return StudentPrintContext{}, mapNotFound(err)
	}

	// 名册取考试冻结快照；活动打印只看同一模板和内容哈希的最新未撤销纸张。
	candidateRows, err := s.db.QueryContext(ctx, `
SELECT
  candidate.student_id::text,candidate.student_no_snapshot,candidate.student_name_snapshot,
  candidate.class_id_snapshot::text,candidate.class_name_snapshot,
  COALESCE(att.status,'expected'),
  active_sheet.id::text,active_sheet.status,active_sheet.print_batch_id::text
FROM exam_candidate_snapshot candidate
LEFT JOIN exam_student_attendance att
  ON att.tenant_id=candidate.tenant_id AND att.exam_id=candidate.exam_id
  AND att.student_id=candidate.student_id AND att.deleted_at IS NULL
LEFT JOIN LATERAL (
  SELECT sh.id,sh.status,sh.print_batch_id
  FROM answer_sheet_print_sheet sh
  WHERE sh.tenant_id=candidate.tenant_id
    AND sh.exam_id=candidate.exam_id
    AND sh.template_id=$2::uuid
    AND sh.template_content_hash=$3
    AND sh.student_id=candidate.student_id
    AND sh.status IN ('issued','observed','conflict')
  ORDER BY sh.created_at DESC,sh.id DESC
  LIMIT 1
) active_sheet ON true
WHERE candidate.tenant_id=$1 AND candidate.exam_id=$4::uuid
ORDER BY candidate.class_name_snapshot,candidate.student_no_snapshot,candidate.student_name_snapshot,candidate.student_id
`, tenantID, templateID, out.TemplateContentHash, out.ExamID)
	if err != nil {
		return StudentPrintContext{}, err
	}
	defer candidateRows.Close()
	for candidateRows.Next() {
		var candidate StudentPrintCandidate
		var sheetSerial, sheetStatus, printBatchID sql.NullString
		if err = candidateRows.Scan(
			&candidate.StudentID, &candidate.StudentNo, &candidate.StudentName,
			&candidate.ClassID, &candidate.ClassName, &candidate.AttendanceStatus,
			&sheetSerial, &sheetStatus, &printBatchID,
		); err != nil {
			return StudentPrintContext{}, err
		}
		candidate.HasActiveSheet = sheetSerial.Valid
		candidate.ActiveSheetSerial = sheetSerial.String
		candidate.ActiveSheetStatus = sheetStatus.String
		candidate.ActivePrintBatchID = printBatchID.String
		out.Candidates = append(out.Candidates, candidate)
	}
	if err = candidateRows.Err(); err != nil {
		return StudentPrintContext{}, err
	}

	batchRows, err := s.db.QueryContext(ctx, `
SELECT
  b.id::text,b.operation,COALESCE(b.reason,''),b.sheet_count,b.page_count,
  count(*) FILTER (WHERE sh.status='issued')::int,
  count(*) FILTER (WHERE sh.status='observed')::int,
  count(*) FILTER (WHERE sh.status='conflict')::int,
  count(*) FILTER (WHERE sh.status='revoked')::int,
  bool_and(sh.status='issued'),
  b.issued_at
FROM answer_sheet_print_batch b
JOIN answer_sheet_print_sheet sh
  ON sh.tenant_id=b.tenant_id AND sh.print_batch_id=b.id
WHERE b.tenant_id=$1 AND b.template_id=$2::uuid
GROUP BY b.id,b.operation,b.reason,b.sheet_count,b.page_count,b.issued_at
ORDER BY b.issued_at DESC,b.id DESC
LIMIT 20
`, tenantID, templateID)
	if err != nil {
		return StudentPrintContext{}, err
	}
	defer batchRows.Close()
	for batchRows.Next() {
		var batch StudentPrintBatchSummary
		if err = batchRows.Scan(
			&batch.PrintBatchID, &batch.Operation, &batch.Reason,
			&batch.SheetCount, &batch.PageCount,
			&batch.IssuedCount, &batch.ObservedCount, &batch.ConflictCount, &batch.RevokedCount,
			&batch.Downloadable, &batch.IssuedAt,
		); err != nil {
			return StudentPrintContext{}, err
		}
		batch.IssuedAt = batch.IssuedAt.UTC()
		out.Batches = append(out.Batches, batch)
	}
	if err = batchRows.Err(); err != nil {
		return StudentPrintContext{}, err
	}
	return out, nil
}
