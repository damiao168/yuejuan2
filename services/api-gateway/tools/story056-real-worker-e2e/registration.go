package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

func seedRegistrationFixture(ctx context.Context, dsn string, scope registrationSeedScope, template lockedTemplate, source map[string]any) (registrationFixture, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return registrationFixture{}, fmt.Errorf("open isolated PostgreSQL database: %w", err)
	}
	defer db.Close()
	return seedRegistrationFixtureDB(ctx, db, scope, template, source)
}

// 只构造已完成上游捕获的测试事实；各步保持原先独立写入，失败时保留部分预置供诊断。
func seedRegistrationFixtureDB(ctx context.Context, db *sql.DB, scope registrationSeedScope, template lockedTemplate, source map[string]any) (registrationFixture, error) {
	sourceID := stringField(source, "id")
	sourceHash := stringField(source, "hash_sha256")
	sourceBytes := int64(intField(source, "size_bytes"))
	if sourceID == "" || sourceHash == "" || sourceBytes <= 0 {
		return registrationFixture{}, errors.New("source asset lacks id, hash, or size")
	}
	var submissionID string
	err := db.QueryRowContext(ctx, `
INSERT INTO submission (
  tenant_id, exam_id, student_id, candidate_no, source_type, status,
  expected_page_count, actual_page_count, quality_status, quality_issues, collected_by,
  identity_status, identity_evidence
)
VALUES ($1::uuid,$2::uuid,$3::uuid,'S056-E2E-001','scanner_upload','ready_for_ocr',1,1,'passed','[]'::jsonb,$4::uuid,'matched','{}'::jsonb)
RETURNING id::text
`, scope.tenantID, scope.examID, scope.studentID, scope.adminID).Scan(&submissionID)
	if err != nil {
		return registrationFixture{}, fmt.Errorf("seed isolated submission: %w", err)
	}
	var pageID string
	err = db.QueryRowContext(ctx, `
INSERT INTO submission_page (tenant_id,submission_id,file_asset_id,page_no,status,quality_status)
VALUES ($1::uuid,$2::uuid,$3::uuid,1,'accepted','passed')
RETURNING id::text
`, scope.tenantID, submissionID, sourceID).Scan(&pageID)
	if err != nil {
		return registrationFixture{}, fmt.Errorf("seed isolated submission page: %w", err)
	}
	var batchID string
	err = db.QueryRowContext(ctx, `
INSERT INTO capture_batch (tenant_id,exam_id,name,source_type,status,operator_id)
VALUES ($1::uuid,$2::uuid,'STORY-056 real-worker fixture','scanner_upload','ready',$3::uuid)
RETURNING id::text
`, scope.tenantID, scope.examID, scope.adminID).Scan(&batchID)
	if err != nil {
		return registrationFixture{}, fmt.Errorf("seed isolated capture batch: %w", err)
	}
	var captureFileID string
	err = db.QueryRowContext(ctx, `
INSERT INTO capture_file (tenant_id,capture_batch_id,file_asset_id,original_name,content_type,sha256,byte_size,status,idempotency_key,uploaded_by)
VALUES ($1::uuid,$2::uuid,$3::uuid,'story056-source-answer.png','image/png',$4,$5,'completed','story056-real-worker-source',$6::uuid)
RETURNING id::text
`, scope.tenantID, batchID, sourceID, sourceHash, sourceBytes, scope.adminID).Scan(&captureFileID)
	if err != nil {
		return registrationFixture{}, fmt.Errorf("seed isolated capture file: %w", err)
	}
	var capturePageID string
	err = db.QueryRowContext(ctx, `
INSERT INTO capture_page (
  tenant_id,capture_batch_id,capture_file_id,source_index,submission_id,submission_page_id,
  assigned_page_no,sequence_no,decoded_file_asset_id,status,page_identity,match_candidates
)
VALUES ($1::uuid,$2::uuid,$3::uuid,1,$4::uuid,$5::uuid,1,1,$6::uuid,'ready','{}'::jsonb,'[]'::jsonb)
RETURNING id::text
`, scope.tenantID, batchID, captureFileID, submissionID, pageID, sourceID).Scan(&capturePageID)
	if err != nil {
		return registrationFixture{}, fmt.Errorf("seed isolated capture page: %w", err)
	}
	var registrationRunID string
	err = db.QueryRowContext(ctx, `
INSERT INTO page_registration_run (
  tenant_id,capture_page_id,submission_page_id,source_file_asset_id,source_sha256,
  template_id,template_content_hash,page_no,processing_status,profile_version,started_at
)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6::uuid,$7,1,'processing','fixture-identity-v1',now())
RETURNING id::text
`, scope.tenantID, capturePageID, pageID, sourceID, sourceHash, template.id, template.contentHash).Scan(&registrationRunID)
	if err != nil {
		return registrationFixture{}, fmt.Errorf("seed isolated registration run: %w", err)
	}
	return registrationFixture{submissionID: submissionID, submissionPageID: pageID, registrationRunID: registrationRunID}, nil
}

func completeRegistrationAndSeedSegment(ctx context.Context, dsn, tenantID, questionID string, template lockedTemplate, registration registrationFixture, assets registrationAssets) (string, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return "", fmt.Errorf("open isolated PostgreSQL database: %w", err)
	}
	defer db.Close()
	return completeRegistrationAndSeedSegmentDB(ctx, db, tenantID, questionID, template, registration, assets)
}

// 配准只允许从 processing 完成一行；后续片段继续引用同一模板版本和两类输出资产。
func completeRegistrationAndSeedSegmentDB(ctx context.Context, db *sql.DB, tenantID, questionID string, template lockedTemplate, registration registrationFixture, assets registrationAssets) (string, error) {
	registeredID := stringField(assets.registered, "id")
	cropID := stringField(assets.crop, "id")
	cropHash := stringField(assets.crop, "hash_sha256")
	if registeredID == "" || cropID == "" || cropHash == "" {
		return "", errors.New("registration assets lack required identity or hash")
	}
	result, err := db.ExecContext(ctx, `
UPDATE page_registration_run
SET processing_status='completed',match_status='matched',confidence=1.0,method='fixture_identity',
    registered_file_asset_id=$3::uuid,result_version='fixture-registration-v1',duration_ms=1,completed_at=now(),updated_at=now()
WHERE tenant_id=$1::uuid AND id=$2::uuid AND processing_status='processing'
`, tenantID, registration.registrationRunID, registeredID)
	if err != nil {
		return "", fmt.Errorf("complete isolated registration run: %w", err)
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return "", fmt.Errorf("complete isolated registration run affected %d rows", updated)
	}
	var segmentID string
	err = db.QueryRowContext(ctx, `
INSERT INTO answer_segment (
  tenant_id,submission_id,submission_page_id,question_id,question_no,bbox,source,status,
  template_id,template_content_hash,registration_run_id,normalized_bbox,pixel_bbox,crop_file_asset_id,crop_sha256,
  question_version,processing_status,confidence
)
VALUES (
  $1::uuid,$2::uuid,$3::uuid,$4::uuid,'Q1','{"x":0,"y":0,"width":1,"height":1}'::jsonb,'configured_answer_area','accepted',
  $5::uuid,$6,$7::uuid,'{"x":0,"y":0,"width":1,"height":1}'::jsonb,'{"x":0,"y":0,"width":160,"height":70}'::jsonb,$8::uuid,$9,
  1,'completed',0.99
)
RETURNING id::text
`, tenantID, registration.submissionID, registration.submissionPageID, questionID, template.id, template.contentHash, registration.registrationRunID, cropID, cropHash).Scan(&segmentID)
	if err != nil {
		return "", fmt.Errorf("seed isolated answer segment: %w", err)
	}
	return segmentID, nil
}
