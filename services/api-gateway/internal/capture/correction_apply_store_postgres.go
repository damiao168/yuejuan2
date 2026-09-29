package capture

import (
	"context"
	"encoding/json"
	"strings"
)

type correctionSegmentSnapshot struct {
	SubmissionID      string         `json:"submission_id"`
	SubmissionPageID  string         `json:"submission_page_id"`
	QuestionID        string         `json:"question_id"`
	QuestionNo        string         `json:"question_no"`
	BBox              map[string]any `json:"bbox"`
	Source            string         `json:"source"`
	Status            string         `json:"status"`
	TemplateID        string         `json:"template_id"`
	TemplateHash      string         `json:"template_content_hash"`
	RegistrationRunID string         `json:"registration_run_id"`
	NormalizedBBox    map[string]any `json:"normalized_bbox"`
	PixelBBox         map[string]any `json:"pixel_bbox"`
	CropFileAssetID   string         `json:"crop_file_asset_id"`
	CropSHA256        string         `json:"crop_sha256"`
	QuestionVersion   int            `json:"question_version"`
	ProcessingStatus  string         `json:"processing_status"`
	Confidence        float64        `json:"confidence"`
}

type correctionPreviousSnapshot struct {
	PageStatus           string                      `json:"page_status"`
	BaseProcessingStatus string                      `json:"base_processing_status"`
	BaseMatchStatus      string                      `json:"base_match_status"`
	Segments             []correctionSegmentSnapshot `json:"segments"`
}

func (s *PostgresStore) ApplyRegistrationCorrection(ctx context.Context, tenantID, correctionID, actorID string, input RegistrationCorrectionDecisionInput) (RegistrationCorrection, error) {
	if input.Revision <= 0 || strings.TrimSpace(input.Reason) == "" {
		return RegistrationCorrection{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RegistrationCorrection{}, err
	}
	defer tx.Rollback()
	current, err := scanRegistrationCorrection(tx.QueryRowContext(ctx, `SELECT `+correctionColumns+` FROM page_registration_correction WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, correctionID))
	if err != nil {
		return RegistrationCorrection{}, err
	}
	if current.Revision != input.Revision {
		return RegistrationCorrection{}, ErrConflict
	}
	if current.Status != "preview_ready" || current.PreviewRegisteredFileAssetID == "" || len(current.PreviewSegments) == 0 {
		return RegistrationCorrection{}, ErrInvalidTransition
	}
	var batchID, submissionID, submissionPageID, pageStatus string
	var pageRevision int
	err = tx.QueryRowContext(ctx, `SELECT capture_batch_id::text,submission_id::text,submission_page_id::text,status,revision FROM capture_page WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, current.CapturePageID).Scan(&batchID, &submissionID, &submissionPageID, &pageStatus, &pageRevision)
	if err != nil {
		return RegistrationCorrection{}, mapNotFound(err)
	}
	if err = ensureBatchWritableTx(ctx, tx, tenantID, batchID); err != nil {
		return RegistrationCorrection{}, err
	}
	if pageRevision != current.SourcePageRevision {
		return RegistrationCorrection{}, ErrConflict
	}
	var latestRunID string
	if err = tx.QueryRowContext(ctx, `SELECT id::text FROM page_registration_run WHERE tenant_id=$1 AND capture_page_id=$2::uuid AND deleted_at IS NULL ORDER BY created_at DESC LIMIT 1`, tenantID, current.CapturePageID).Scan(&latestRunID); err != nil || latestRunID != current.BaseRegistrationRunID {
		return RegistrationCorrection{}, ErrConflict
	}
	var sourceAssetID, sourceSHA, baseProcessing, baseMatch string
	err = tx.QueryRowContext(ctx, `SELECT source_file_asset_id::text,source_sha256,processing_status,COALESCE(match_status,'') FROM page_registration_run WHERE tenant_id=$1 AND id=$2::uuid FOR UPDATE`, tenantID, current.BaseRegistrationRunID).Scan(&sourceAssetID, &sourceSHA, &baseProcessing, &baseMatch)
	if err != nil {
		return RegistrationCorrection{}, mapNotFound(err)
	}
	// 先保存原页状态和切片快照，再在同一事务中替换配准结果，供显式撤销恢复。
	var segmentRaw []byte
	err = tx.QueryRowContext(ctx, `SELECT COALESCE(jsonb_agg(jsonb_build_object('submission_id',submission_id::text,'submission_page_id',submission_page_id::text,'question_id',question_id::text,'question_no',question_no,'bbox',bbox,'source',source,'status',status,'template_id',COALESCE(template_id::text,''),'template_content_hash',COALESCE(template_content_hash,''),'registration_run_id',COALESCE(registration_run_id::text,''),'normalized_bbox',COALESCE(normalized_bbox,'{}'),'pixel_bbox',COALESCE(pixel_bbox,'{}'),'crop_file_asset_id',COALESCE(crop_file_asset_id::text,''),'crop_sha256',COALESCE(crop_sha256,''),'question_version',COALESCE(question_version,1),'processing_status',processing_status,'confidence',COALESCE(confidence,0))) FILTER(WHERE id IS NOT NULL),'[]') FROM answer_segment WHERE tenant_id=$1 AND submission_id=$2::uuid AND submission_page_id=$3::uuid AND deleted_at IS NULL`, tenantID, submissionID, submissionPageID).Scan(&segmentRaw)
	if err != nil {
		return RegistrationCorrection{}, err
	}
	previous := correctionPreviousSnapshot{PageStatus: pageStatus, BaseProcessingStatus: baseProcessing, BaseMatchStatus: baseMatch, Segments: []correctionSegmentSnapshot{}}
	_ = json.Unmarshal(segmentRaw, &previous.Segments)
	previousRaw, _ := json.Marshal(previous)
	forward, _ := json.Marshal(current.SourceToTemplate)
	inverse, _ := json.Marshal(current.TemplateToSource)
	var appliedRunID string
	err = tx.QueryRowContext(ctx, `INSERT INTO page_registration_run(tenant_id,capture_page_id,submission_page_id,source_file_asset_id,source_sha256,template_id,template_content_hash,page_no,processing_status,match_status,confidence,method,profile_version,source_to_template_matrix,template_to_source_matrix,feature_count,match_count,inlier_count,inlier_ratio,reprojection_error,registered_file_asset_id,result_version,started_at,completed_at) SELECT $1,$2::uuid,submission_page_id,$3::uuid,$4,$5::uuid,$6,$7,'completed','matched',1,'manual_four_point','manual-four-point-v1',$8,$9,0,4,4,1,$10,$11::uuid,$12,now(),now() FROM capture_page WHERE tenant_id=$1 AND id=$2::uuid RETURNING id::text`, tenantID, current.CapturePageID, sourceAssetID, sourceSHA, current.TemplateID, current.TemplateContentHash, current.PageNo, forward, inverse, current.ReprojectionError, current.PreviewRegisteredFileAssetID, current.ValidationReportHash()).Scan(&appliedRunID)
	if err != nil {
		return RegistrationCorrection{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE page_registration_run SET processing_status='invalidated',updated_at=now() WHERE tenant_id=$1 AND capture_page_id=$2::uuid AND id<>$3::uuid AND processing_status<>'invalidated'`, tenantID, current.CapturePageID, appliedRunID); err != nil {
		return RegistrationCorrection{}, err
	}
	for _, segment := range current.PreviewSegments {
		var questionNo string
		if err = tx.QueryRowContext(ctx, `SELECT question_no FROM question WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL`, tenantID, segment.QuestionID).Scan(&questionNo); err != nil {
			return RegistrationCorrection{}, mapNotFound(err)
		}
		normalized, _ := json.Marshal(segment.NormalizedBBox)
		pixels, _ := json.Marshal(segment.PixelBBox)
		_, err = tx.ExecContext(ctx, `INSERT INTO answer_segment(tenant_id,submission_id,submission_page_id,question_id,question_no,bbox,source,status,template_id,template_content_hash,registration_run_id,normalized_bbox,pixel_bbox,crop_file_asset_id,crop_sha256,question_version,processing_status,confidence) SELECT $1,$2::uuid,submission_page_id,$3::uuid,$4,$5,'configured_answer_area','generated',$6::uuid,$7,$8::uuid,$5,$9,$10::uuid,$11,1,'completed',1 FROM capture_page WHERE tenant_id=$1 AND id=$12::uuid ON CONFLICT(tenant_id,submission_id,question_id) DO UPDATE SET submission_page_id=EXCLUDED.submission_page_id,bbox=EXCLUDED.bbox,source=EXCLUDED.source,status=EXCLUDED.status,template_id=EXCLUDED.template_id,template_content_hash=EXCLUDED.template_content_hash,registration_run_id=EXCLUDED.registration_run_id,normalized_bbox=EXCLUDED.normalized_bbox,pixel_bbox=EXCLUDED.pixel_bbox,crop_file_asset_id=EXCLUDED.crop_file_asset_id,crop_sha256=EXCLUDED.crop_sha256,question_version=1,processing_status='completed',confidence=1,deleted_at=NULL,updated_at=now()`, tenantID, submissionID, segment.QuestionID, questionNo, normalized, current.TemplateID, current.TemplateContentHash, appliedRunID, pixels, segment.FileAssetID, segment.SHA256, current.CapturePageID)
		if err != nil {
			return RegistrationCorrection{}, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE capture_page SET status='ready',revision=revision+1,manual_override=manual_override||jsonb_build_object('registration_correction_id',$3::text,'registration_correction_reason',$4::text),updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, current.CapturePageID, current.ID, strings.TrimSpace(input.Reason)); err != nil {
		return RegistrationCorrection{}, err
	}
	out, err := scanRegistrationCorrection(tx.QueryRowContext(ctx, `UPDATE page_registration_correction SET status='applied',applied_registration_run_id=$3::uuid,applied_by=$4::uuid,reason=$5,previous_registration_snapshot=$6,applied_at=now(),revision=revision+1,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid RETURNING `+correctionColumns, tenantID, correctionID, appliedRunID, actorID, strings.TrimSpace(input.Reason), previousRaw))
	if err != nil {
		return RegistrationCorrection{}, err
	}
	after, _ := json.Marshal(map[string]any{"applied_registration_run_id": appliedRunID, "page_status": "ready"})
	if _, err = tx.ExecContext(ctx, `INSERT INTO capture_operation(tenant_id,capture_batch_id,operation,target_type,target_id,actor_id,reason,before_state,after_state) VALUES($1,$2::uuid,'registration_correction_apply','page_registration_correction',$3::uuid,$4::uuid,$5,$6,$7)`, tenantID, batchID, correctionID, actorID, strings.TrimSpace(input.Reason), previousRaw, after); err != nil {
		return RegistrationCorrection{}, err
	}
	if err = s.aggregateBatchTx(ctx, tx, tenantID, batchID); err != nil {
		return RegistrationCorrection{}, err
	}
	return out, tx.Commit()
}

func (s *PostgresStore) UndoRegistrationCorrection(ctx context.Context, tenantID, correctionID, actorID string, input RegistrationCorrectionDecisionInput) (RegistrationCorrection, error) {
	if input.Revision <= 0 || strings.TrimSpace(input.Reason) == "" {
		return RegistrationCorrection{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RegistrationCorrection{}, err
	}
	defer tx.Rollback()
	current, err := scanRegistrationCorrection(tx.QueryRowContext(ctx, `SELECT `+correctionColumns+` FROM page_registration_correction WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, correctionID))
	if err != nil {
		return RegistrationCorrection{}, err
	}
	if current.Revision != input.Revision {
		return RegistrationCorrection{}, ErrConflict
	}
	if current.Status != "applied" || current.AppliedRegistrationRunID == "" {
		return RegistrationCorrection{}, ErrInvalidTransition
	}
	var previous correctionPreviousSnapshot
	raw, _ := json.Marshal(current.PreviousRegistrationSnapshot)
	if json.Unmarshal(raw, &previous) != nil {
		return RegistrationCorrection{}, ErrInvalidInput
	}
	var batchID, submissionID string
	var pageRevision int
	if err = tx.QueryRowContext(ctx, `SELECT capture_batch_id::text,submission_id::text,revision FROM capture_page WHERE tenant_id=$1 AND id=$2::uuid FOR UPDATE`, tenantID, current.CapturePageID).Scan(&batchID, &submissionID, &pageRevision); err != nil {
		return RegistrationCorrection{}, mapNotFound(err)
	}
	if err = ensureBatchWritableTx(ctx, tx, tenantID, batchID); err != nil {
		return RegistrationCorrection{}, err
	}
	// 只允许撤销紧接本次应用的页版本，防止覆盖应用之后其他人的修改。
	if pageRevision != current.SourcePageRevision+1 {
		return RegistrationCorrection{}, ErrConflict
	}
	if _, err = tx.ExecContext(ctx, `UPDATE answer_segment SET processing_status='invalidated',status='needs_manual_review',updated_at=now() WHERE tenant_id=$1 AND submission_id=$2::uuid AND registration_run_id=$3::uuid`, tenantID, submissionID, current.AppliedRegistrationRunID); err != nil {
		return RegistrationCorrection{}, err
	}
	for _, segment := range previous.Segments {
		bbox, _ := json.Marshal(segment.BBox)
		normalized, _ := json.Marshal(segment.NormalizedBBox)
		pixels, _ := json.Marshal(segment.PixelBBox)
		_, err = tx.ExecContext(ctx, `INSERT INTO answer_segment(tenant_id,submission_id,submission_page_id,question_id,question_no,bbox,source,status,template_id,template_content_hash,registration_run_id,normalized_bbox,pixel_bbox,crop_file_asset_id,crop_sha256,question_version,processing_status,confidence) VALUES($1,$2::uuid,$3::uuid,$4::uuid,$5,$6,$7,$8,NULLIF($9,'')::uuid,NULLIF($10,''),NULLIF($11,'')::uuid,$12,$13,NULLIF($14,'')::uuid,NULLIF($15,''),$16,$17,$18) ON CONFLICT(tenant_id,submission_id,question_id) DO UPDATE SET submission_page_id=EXCLUDED.submission_page_id,bbox=EXCLUDED.bbox,source=EXCLUDED.source,status=EXCLUDED.status,template_id=EXCLUDED.template_id,template_content_hash=EXCLUDED.template_content_hash,registration_run_id=EXCLUDED.registration_run_id,normalized_bbox=EXCLUDED.normalized_bbox,pixel_bbox=EXCLUDED.pixel_bbox,crop_file_asset_id=EXCLUDED.crop_file_asset_id,crop_sha256=EXCLUDED.crop_sha256,question_version=EXCLUDED.question_version,processing_status=EXCLUDED.processing_status,confidence=EXCLUDED.confidence,deleted_at=NULL,updated_at=now()`, tenantID, segment.SubmissionID, segment.SubmissionPageID, segment.QuestionID, segment.QuestionNo, bbox, segment.Source, segment.Status, segment.TemplateID, segment.TemplateHash, segment.RegistrationRunID, normalized, pixels, segment.CropFileAssetID, segment.CropSHA256, segment.QuestionVersion, segment.ProcessingStatus, segment.Confidence)
		if err != nil {
			return RegistrationCorrection{}, err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE page_registration_run SET processing_status='invalidated',updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, current.AppliedRegistrationRunID); err != nil {
		return RegistrationCorrection{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE page_registration_run SET processing_status=$3,match_status=NULLIF($4,''),updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, current.BaseRegistrationRunID, previous.BaseProcessingStatus, previous.BaseMatchStatus); err != nil {
		return RegistrationCorrection{}, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE capture_page SET status=$3,revision=revision+1,manual_override=manual_override||jsonb_build_object('registration_correction_undo_id',$4::text,'registration_correction_undo_reason',$5::text),updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid`, tenantID, current.CapturePageID, previous.PageStatus, current.ID, strings.TrimSpace(input.Reason)); err != nil {
		return RegistrationCorrection{}, err
	}
	out, err := scanRegistrationCorrection(tx.QueryRowContext(ctx, `UPDATE page_registration_correction SET status='undone',reason=$3,undone_at=now(),revision=revision+1,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid RETURNING `+correctionColumns, tenantID, correctionID, strings.TrimSpace(input.Reason)))
	if err != nil {
		return RegistrationCorrection{}, err
	}
	after, _ := json.Marshal(previous)
	if _, err = tx.ExecContext(ctx, `INSERT INTO capture_operation(tenant_id,capture_batch_id,operation,target_type,target_id,actor_id,reason,before_state,after_state) VALUES($1,$2::uuid,'registration_correction_undo','page_registration_correction',$3::uuid,$4::uuid,$5,jsonb_build_object('applied_registration_run_id',$6::text),$7)`, tenantID, batchID, correctionID, actorID, strings.TrimSpace(input.Reason), current.AppliedRegistrationRunID, after); err != nil {
		return RegistrationCorrection{}, err
	}
	if err = s.aggregateBatchTx(ctx, tx, tenantID, batchID); err != nil {
		return RegistrationCorrection{}, err
	}
	return out, tx.Commit()
}
