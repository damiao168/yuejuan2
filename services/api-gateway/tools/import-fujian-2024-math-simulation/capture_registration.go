// 采集兼容写入和配准事务集中在此；各事务保留原调用边界，不能合成虚假的整体原子保证。
package main

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strconv"
)

type registrationCreateInput struct {
	tenantID       string
	adminID        string
	captureBatchID string
	submissionID   string
	page           pageRecord
	templateID     string
	templateHash   string
	candidateNo    string
}

type registrationCompleteInput struct {
	tenantID         string
	submissionID     string
	submissionPageID string
	registrationID   string
	templateID       string
	templateHash     string
	registered       map[string]any
}

func createCaptureBatch(ctx context.Context, db *sql.DB, tenantID, examID, adminID string) (string, error) {
	var id string
	err := db.QueryRowContext(ctx, `
INSERT INTO capture_batch(tenant_id,exam_id,name,source_type,status,operator_id)
VALUES($1::uuid,$2::uuid,'福建中考数学匿名仿真批次','scanner_upload','ready',$3::uuid)
RETURNING id::text
`, tenantID, examID, adminID).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("create capture batch: %w", err)
	}
	return id, nil
}

func addAnswerAreaCompatibilityAliases(ctx context.Context, db *sql.DB, tenantID, examID string) error {
	result, err := db.ExecContext(ctx, `
UPDATE question
SET answer_area = answer_area || jsonb_build_object('w', answer_area->'width', 'h', answer_area->'height'),
    updated_at = now()
WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND deleted_at IS NULL
`, tenantID, examID)
	if err != nil {
		return fmt.Errorf("add answer-area compatibility aliases: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 19 {
		return fmt.Errorf("answer-area compatibility updated %d questions, want 19", rows)
	}
	return nil
}

func createRegistration(
	ctx context.Context,
	db *sql.DB,
	input registrationCreateInput,
) (string, error) {
	tenantID, adminID := input.tenantID, input.adminID
	captureBatchID, submissionID, page := input.captureBatchID, input.submissionID, input.page
	templateID, templateHash, candidateNo := input.templateID, input.templateHash, input.candidateNo
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	sourceID := stringField(page.FileAsset, "id")
	sourceHash := stringField(page.FileAsset, "hash_sha256")
	sourceBytes := int64(intField(page.FileAsset, "size_bytes"))
	var captureFileID string
	err = tx.QueryRowContext(ctx, `
INSERT INTO capture_file(
 tenant_id,capture_batch_id,file_asset_id,original_name,content_type,sha256,byte_size,status,idempotency_key,uploaded_by
)
VALUES($1::uuid,$2::uuid,$3::uuid,$4,'image/png',$5,$6,'completed',$7,$8::uuid)
RETURNING id::text
`, tenantID, captureBatchID, sourceID, filepath.Base(page.Path), sourceHash, sourceBytes,
		datasetID+":"+candidateNo+":page:"+strconv.Itoa(page.PageNo), adminID).Scan(&captureFileID)
	if err != nil {
		return "", fmt.Errorf("create capture file %s page %d: %w", candidateNo, page.PageNo, err)
	}
	var capturePageID string
	err = tx.QueryRowContext(ctx, `
INSERT INTO capture_page(
 tenant_id,capture_batch_id,capture_file_id,source_index,submission_id,submission_page_id,
 assigned_page_no,sequence_no,decoded_file_asset_id,status,page_identity,match_candidates
)
VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5::uuid,$6::uuid,$4,$4,$7::uuid,'ready',
 jsonb_build_object('synthetic',true,'candidate_no',$8::text,'page_no',$4::int),'[]'::jsonb)
RETURNING id::text
`, tenantID, captureBatchID, captureFileID, page.PageNo, submissionID, page.ID, sourceID, candidateNo).Scan(&capturePageID)
	if err != nil {
		return "", fmt.Errorf("create capture page %s page %d: %w", candidateNo, page.PageNo, err)
	}
	var registrationID string
	err = tx.QueryRowContext(ctx, `
INSERT INTO page_registration_run(
 tenant_id,capture_page_id,submission_page_id,source_file_asset_id,source_sha256,
 template_id,template_content_hash,page_no,processing_status,profile_version,started_at
)
VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6::uuid,$7,$8,'processing','simulation-identity-registration-v1',now())
RETURNING id::text
`, tenantID, capturePageID, page.ID, sourceID, sourceHash, templateID, templateHash, page.PageNo).Scan(&registrationID)
	if err != nil {
		return "", fmt.Errorf("create registration %s page %d: %w", candidateNo, page.PageNo, err)
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	return registrationID, nil
}

func completeRegistration(
	ctx context.Context,
	db *sql.DB,
	input registrationCompleteInput,
) error {
	tenantID, submissionID, submissionPageID := input.tenantID, input.submissionID, input.submissionPageID
	registrationID, templateID, templateHash := input.registrationID, input.templateID, input.templateHash
	registered := input.registered
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.ExecContext(ctx, `
UPDATE page_registration_run
SET processing_status='completed',match_status='matched',confidence=0.99,method='simulation_seeded_identity',
 registered_file_asset_id=$3::uuid,result_version='simulation-registration-v1',duration_ms=5,completed_at=now(),updated_at=now()
WHERE tenant_id=$1::uuid AND id=$2::uuid AND processing_status='processing'
`, tenantID, registrationID, stringField(registered, "id"))
	if err != nil {
		return fmt.Errorf("complete registration: %w", err)
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return fmt.Errorf("complete registration affected %d rows", rows)
	}
	_, err = tx.ExecContext(ctx, `
UPDATE answer_segment
SET status='accepted',
 template_id=$4::uuid,
 template_content_hash=$5,
 registration_run_id=$6::uuid,
 normalized_bbox=jsonb_build_object(
   'x',(bbox->>0)::float8,'y',(bbox->>1)::float8,'width',(bbox->>2)::float8,'height',(bbox->>3)::float8
 ),
  pixel_bbox=jsonb_build_object(
   'x',round((bbox->>0)::numeric * $7),'y',round((bbox->>1)::numeric * $8),
   'width',round((bbox->>2)::numeric * $7),'height',round((bbox->>3)::numeric * $8)
  ),
 question_version=1,
 processing_status='completed',
 confidence=0.99,
 updated_at=now()
WHERE tenant_id=$1::uuid AND submission_id=$2::uuid AND submission_page_id=$3::uuid AND deleted_at IS NULL
`, tenantID, submissionID, submissionPageID, templateID, templateHash, registrationID,
		imageWidth, imageHeight)
	if err != nil {
		return fmt.Errorf("attach segment evidence: %w", err)
	}
	return tx.Commit()
}
