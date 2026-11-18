package capture

import (
	"context"
	"database/sql"
	"encoding/json"
)

// GetStudentPrintContext 返回不可变打印历史和当前考试考生名单。它归属 exam:manage，使打印工作流不依赖成绩管理权限。
func (s *PostgresStore) GetStudentPrintContext(ctx context.Context, tenantID, templateID string) (StudentPrintContext, error) {
	out := StudentPrintContext{
		TemplateID: templateID,
		Candidates: []StudentPrintCandidate{},
		Batches:    []StudentPrintBatchSummary{},
	}
	var layoutRaw []byte
	if err := s.db.QueryRowContext(ctx, `
SELECT t.exam_id::text,t.content_hash,t.layout,f.id::text,f.content_type,f.hash_sha256
FROM answer_sheet_template t
JOIN exam_paper p ON p.tenant_id=t.tenant_id AND p.id=t.exam_paper_id AND p.exam_id=t.exam_id AND p.deleted_at IS NULL
JOIN file_asset f ON f.tenant_id=p.tenant_id AND f.id=p.file_asset_id AND f.deleted_at IS NULL AND f.lifecycle_status='active'
WHERE t.tenant_id=$1 AND t.id=$2::uuid AND t.status='locked' AND t.deleted_at IS NULL
`, tenantID, templateID).Scan(&out.ExamID, &out.TemplateContentHash, &layoutRaw, &out.PrintSource.FileAssetID, &out.PrintSource.ContentType, &out.PrintSource.SHA256); err != nil {
		return StudentPrintContext{}, mapNotFound(err)
	}
	var layout templateLayout
	if err := json.Unmarshal(layoutRaw, &layout); err != nil {
		return StudentPrintContext{}, err
	}
	pages, err := printPackagePages(layoutRaw, 1, len(layout.Pages))
	if err != nil {
		return StudentPrintContext{}, err
	}
	out.QRLayoutPageNos = missingPrintQRPages(pages)
	out.QRLayoutRequired = len(out.QRLayoutPageNos) > 0

	// 打印覆盖完整冻结名册，不能按历史人工缺考或尚未收卷排除学生。expected 仅保留旧响应兼容。
	// 活动打印只看同一模板和内容哈希的最新未撤销纸张。
	candidateRows, err := s.db.QueryContext(ctx, `
SELECT
  candidate.student_id::text,candidate.student_no_snapshot,candidate.student_name_snapshot,
  candidate.class_id_snapshot::text,candidate.class_name_snapshot,
  'expected'::text,
  active_sheet.id::text,active_sheet.status,active_sheet.print_batch_id::text
FROM exam_candidate_snapshot candidate
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
  b.issued_at,b.print_layout
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
		var qrLayoutRaw []byte
		if err = batchRows.Scan(
			&batch.PrintBatchID, &batch.Operation, &batch.Reason,
			&batch.SheetCount, &batch.PageCount,
			&batch.IssuedCount, &batch.ObservedCount, &batch.ConflictCount, &batch.RevokedCount,
			&batch.Downloadable, &batch.IssuedAt, &qrLayoutRaw,
		); err != nil {
			return StudentPrintContext{}, err
		}
		batch.IssuedAt = batch.IssuedAt.UTC()
		batch.QRLayout, err = decodeStudentPrintLayout(qrLayoutRaw)
		if err != nil {
			return StudentPrintContext{}, err
		}
		batch.QRLayoutRequired = out.QRLayoutRequired && batch.QRLayout == nil
		out.Batches = append(out.Batches, batch)
	}
	if err = batchRows.Err(); err != nil {
		return StudentPrintContext{}, err
	}
	return out, nil
}
