package capture

import (
	"context"
	"database/sql"
	"encoding/json"
)

// 同批次解码与归组共用批次锁；先锁批次再锁文件，避免两个工作节点各持一个文件后互相等待。
func lockFileBatchTx(ctx context.Context, tx *sql.Tx, tenantID, fileID string) error {
	var id string
	return mapNotFound(tx.QueryRowContext(ctx, `SELECT b.id::text FROM capture_batch b JOIN capture_file f ON f.tenant_id=b.tenant_id AND f.capture_batch_id=b.id WHERE f.tenant_id=$1 AND f.id=$2::uuid AND f.deleted_at IS NULL AND b.deleted_at IS NULL FOR UPDATE OF b`, tenantID, fileID).Scan(&id))
}

func validateUploadGroupTx(ctx context.Context, tx *sql.Tx, tenantID, batchID string, input RegisterFileInput) error {
	var conflict bool
	// 幂等重放必须保留原组和原顺序；组内文件也必须使用同一份页数设置。
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM capture_file WHERE tenant_id=$1 AND capture_batch_id=$2::uuid AND deleted_at IS NULL AND (
 (idempotency_key=$3 AND (file_asset_id<>$4::uuid OR COALESCE(upload_group_id::text,'')<>$5 OR COALESCE(upload_group_file_count,0)<>$6 OR COALESCE(upload_group_file_index,0)<>$7 OR COALESCE(pages_per_student,0)<>$8))
 OR (upload_group_id=NULLIF($5,'')::uuid AND (upload_group_file_count<>$6 OR pages_per_student<>$8 OR (upload_group_file_index=$7 AND idempotency_key<>$3)))))`, tenantID, batchID, input.IdempotencyKey, input.FileAssetID, input.UploadGroupID, input.UploadGroupFileCount, input.UploadGroupFileIndex, input.PagesPerStudent).Scan(&conflict)
	if err != nil {
		return err
	}
	if conflict {
		return ErrConflict
	}
	return nil
}

type uploadGroupPage struct {
	id, submissionID, pageID, fileID, assetID, sha, studentID, templateID, sheetSerial, barcodeStatus string
	sourceIndex, width, height, assignedPageNo                                                        int
	conflict                                                                                          bool
}

// finalizeUploadGroupTx 仅在本次选择的所有文件解码成功后，按文件选择顺序、文件内页序归组。
// 归组前没有质检任务，才允许迁移新页面的答卷外键；历史答卷和人工改页不参与自动归组。
func (s *PostgresStore) finalizeUploadGroupTx(ctx context.Context, tx *sql.Tx, scope decodedFileScope) error {
	f := scope.file
	var registered, completed int
	if err := tx.QueryRowContext(ctx, `SELECT count(*),count(*) FILTER(WHERE status='completed') FROM capture_file WHERE tenant_id=$1 AND capture_batch_id=$2::uuid AND upload_group_id=$3::uuid AND deleted_at IS NULL`, scope.tenantID, f.CaptureBatchID, f.UploadGroupID).Scan(&registered, &completed); err != nil {
		return err
	}
	if registered != f.UploadGroupFileCount || completed != registered {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT cp.id::text,cp.submission_id::text,cp.submission_page_id::text,cp.capture_file_id::text,cp.decoded_file_asset_id::text,cp.source_index,
 COALESCE(cp.page_identity->>'sha256',''),COALESCE((cp.page_identity->>'width')::int,0),COALESCE((cp.page_identity->>'height')::int,0),COALESCE(cp.assigned_page_no,0),
 COALESCE(cp.barcode_student_id::text,''),COALESCE(cp.barcode_template_id::text,''),COALESCE(cp.sheet_serial::text,''),COALESCE(cp.page_identity->>'barcode_status','none'),
 cp.duplicate_of_page_id IS NOT NULL OR cp.manual_override<>'{}'::jsonb OR sp.latest_quality_run_id IS NOT NULL OR sp.quality_status<>'unchecked'
 FROM capture_page cp JOIN capture_file cf ON cf.tenant_id=cp.tenant_id AND cf.id=cp.capture_file_id
 JOIN submission_page sp ON sp.tenant_id=cp.tenant_id AND sp.id=cp.submission_page_id
 WHERE cp.tenant_id=$1 AND cp.capture_batch_id=$2::uuid AND cf.upload_group_id=$3::uuid AND cp.deleted_at IS NULL AND cp.status<>'deleted'
 ORDER BY cf.upload_group_file_index,cp.source_index`, scope.tenantID, f.CaptureBatchID, f.UploadGroupID)
	if err != nil {
		return err
	}
	pages := []uploadGroupPage{}
	for rows.Next() {
		var p uploadGroupPage
		if err = rows.Scan(&p.id, &p.submissionID, &p.pageID, &p.fileID, &p.assetID, &p.sourceIndex, &p.sha, &p.width, &p.height, &p.assignedPageNo, &p.studentID, &p.templateID, &p.sheetSerial, &p.barcodeStatus, &p.conflict); err != nil {
			rows.Close()
			return err
		}
		pages = append(pages, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	state := "complete"
	if len(pages) == 0 || len(pages)%f.PagesPerStudent != 0 {
		state = "incomplete_pages"
	}
	for start := 0; start < len(pages); start += f.PagesPerStudent {
		students, sheets := map[string]bool{}, map[string]bool{}
		for i := start; i < start+f.PagesPerStudent && i < len(pages); i++ {
			p := pages[i]
			if p.studentID != "" {
				students[p.studentID] = true
			}
			if p.sheetSerial != "" {
				sheets[p.sheetSerial] = true
			}
			if p.conflict {
				state = "identity_conflict"
			}
		}
		if len(students) > 1 || len(sheets) > 1 {
			state = "identity_conflict"
		}
	}
	if state != "complete" {
		_, err = tx.ExecContext(ctx, `UPDATE capture_page cp SET status='needs_review',page_identity=page_identity||jsonb_build_object('grouping_status',$4::text,'expected_pages',$5::int,'received_pages',$6::int,'missing_pages',$7::int),updated_at=now() FROM capture_file cf WHERE cp.tenant_id=$1 AND cp.capture_batch_id=$2::uuid AND cf.tenant_id=cp.tenant_id AND cf.id=cp.capture_file_id AND cf.upload_group_id=$3::uuid AND cp.deleted_at IS NULL`, scope.tenantID, f.CaptureBatchID, f.UploadGroupID, state, f.PagesPerStudent, len(pages), (f.PagesPerStudent-len(pages)%f.PagesPerStudent)%f.PagesPerStudent)
		return err
	}
	oldSubmissions := map[string]bool{}
	for _, p := range pages {
		oldSubmissions[p.submissionID] = true
	}
	for start := 0; start < len(pages); start += f.PagesPerStudent {
		var target string
		err = tx.QueryRowContext(ctx, `INSERT INTO submission(tenant_id,exam_id,source_type,status,expected_page_count,actual_page_count,quality_status,quality_issues,collected_by,identity_status) VALUES($1,$2::uuid,'image_upload','pages_uploaded',$3,$3,'unchecked','[]',$4::uuid,'unassigned') RETURNING id::text`, scope.tenantID, scope.examID, f.PagesPerStudent, f.UploadedBy).Scan(&target)
		if err != nil {
			return err
		}
		grouped := scope
		grouped.submissionID = target
		identity := decodedFileIdentity{verifiedPageNumbers: map[int]bool{}}
		// 未识别页使用内部占位号，不能预占正式第 1、2 页；否则反面先匹配到第 2 页时会被另一张的临时页号挡住。
		// 界面使用 capture_page 的上传页序，正式 submission_page 页号由条码或图像匹配写回。
		for offset := 0; offset < f.PagesPerStudent; offset++ {
			p := pages[start+offset]
			pageNo := offset + 1
			storedPageNo := 1000000 + pageNo
			if p.barcodeStatus == "verified" && p.assignedPageNo > 0 {
				storedPageNo = p.assignedPageNo
			}
			if _, err = tx.ExecContext(ctx, `UPDATE submission_page SET submission_id=$3::uuid,page_no=$4,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, scope.tenantID, p.pageID, target, storedPageNo); err != nil {
				return err
			}
			fields, _ := json.Marshal(map[string]any{"grouping_status": "complete", "expected_pages": f.PagesPerStudent, "received_pages": f.PagesPerStudent, "upload_page_no": pageNo, "upload_group_id": f.UploadGroupID})
			assigned := pageNo
			if p.barcodeStatus == "verified" && p.assignedPageNo > 0 {
				assigned = p.assignedPageNo
			}
			if _, err = tx.ExecContext(ctx, `UPDATE capture_page SET submission_id=$3::uuid,assigned_page_no=$4,status='quality_checking',page_identity=page_identity||$5::jsonb,revision=revision+1,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, scope.tenantID, p.id, target, assigned, fields); err != nil {
				return err
			}
			identity.observePage(decodedCapturePageResult{capturePageID: p.id, assignedPageNo: assigned, barcodeStatus: p.barcodeStatus, barcode: barcodeEvaluation{AssignedStudentID: p.studentID, AssignedTemplateID: p.templateID, AssignedSheetSerial: p.sheetSerial}})
			if err = queueDecodedPageQualityTx(ctx, tx, grouped, DecodedPageInput{SourceIndex: pageNo, FileAssetID: p.assetID, SHA256: p.sha, Width: p.width, Height: p.height}, p.pageID, p.id); err != nil {
				return err
			}
		}
		if err = reconcileDecodedFileIdentityTx(ctx, tx, grouped, identity); err != nil {
			return err
		}
	}
	for id := range oldSubmissions {
		if _, err = tx.ExecContext(ctx, `UPDATE submission SET deleted_at=now(),updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid AND NOT EXISTS(SELECT 1 FROM capture_page WHERE tenant_id=$1 AND submission_id=$2::uuid AND deleted_at IS NULL AND status<>'deleted')`, scope.tenantID, id); err != nil {
			return err
		}
	}
	return nil
}
