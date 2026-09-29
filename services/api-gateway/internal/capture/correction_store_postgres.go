package capture

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"time"
)

const correctionColumns = `id::text,capture_page_id::text,base_registration_run_id::text,COALESCE(applied_registration_run_id::text,''),source_page_revision,template_id::text,template_content_hash,page_no,source_points,template_points,advanced_anchor_mode,status,revision,attempt_count,COALESCE(preview_registered_file_asset_id::text,''),preview_segments,source_to_template_matrix,template_to_source_matrix,COALESCE(coverage,0),COALESCE(reprojection_error,0),validation_report,previous_registration_snapshot,COALESCE(runtime_task_id::text,''),COALESCE(error_code,''),expires_at,applied_at,undone_at,created_at`

// 校正上下文只接受该页最新且未失效的配准运行，避免把旧坐标套到新页面版本。
func (s *PostgresStore) GetRegistrationCorrectionContext(ctx context.Context, tenantID, runID string) (RegistrationCorrectionContext, error) {
	var out RegistrationCorrectionContext
	var layoutRaw []byte
	err := s.db.QueryRowContext(ctx, `SELECT pr.id::text,pr.capture_page_id::text,ep.exam_id::text,cp.revision,pr.page_no,pr.source_file_asset_id::text,ep.file_asset_id::text,fa.content_type,ast.layout FROM page_registration_run pr JOIN capture_page cp ON cp.tenant_id=pr.tenant_id AND cp.id=pr.capture_page_id JOIN answer_sheet_template ast ON ast.tenant_id=pr.tenant_id AND ast.id=pr.template_id JOIN exam_paper ep ON ep.tenant_id=ast.tenant_id AND ep.id=ast.exam_paper_id JOIN file_asset fa ON fa.tenant_id=ep.tenant_id AND fa.id=ep.file_asset_id WHERE pr.tenant_id=$1 AND pr.id=$2::uuid AND pr.deleted_at IS NULL AND cp.deleted_at IS NULL AND pr.id=(SELECT latest.id FROM page_registration_run latest WHERE latest.tenant_id=pr.tenant_id AND latest.capture_page_id=pr.capture_page_id AND latest.processing_status<>'invalidated' AND latest.deleted_at IS NULL ORDER BY latest.created_at DESC LIMIT 1)`, tenantID, runID).Scan(&out.RegistrationRunID, &out.CapturePageID, &out.ExamID, &out.PageRevision, &out.PageNo, &out.SourceFileAssetID, &out.TemplateFileAssetID, &out.TemplateContentType, &layoutRaw)
	if err != nil {
		return RegistrationCorrectionContext{}, mapNotFound(err)
	}
	var layout templateLayout
	if json.Unmarshal(layoutRaw, &layout) != nil {
		return RegistrationCorrectionContext{}, ErrInvalidInput
	}
	for _, page := range layout.Pages {
		if page.PageNo == out.PageNo {
			out.TemplateWidth, out.TemplateHeight = page.Width, page.Height
			break
		}
	}
	if out.TemplateWidth <= 0 || out.TemplateHeight <= 0 {
		return RegistrationCorrectionContext{}, ErrInvalidInput
	}
	return out, nil
}

func (s *PostgresStore) CreateRegistrationCorrection(ctx context.Context, tenantID, runID, actorID string, input CreateRegistrationCorrectionInput) (RegistrationCorrection, error) {
	if input.PageRevision <= 0 || validateCorrectionPoints(input.SourcePoints, input.TemplatePoints) != nil {
		return RegistrationCorrection{}, ErrInvalidInput
	}
	// 普通模式把源图四角映射到整张模板；只有高级模式保留用户给定的模板锚点。
	if !input.AdvancedAnchorMode {
		input.TemplatePoints = []NormalizedPoint{{0, 0}, {1, 0}, {1, 1}, {0, 1}}
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return RegistrationCorrection{}, err
	}
	defer tx.Rollback()
	var pageID, batchID, templateID, templateHash string
	var pageNo, pageRevision int
	err = tx.QueryRowContext(ctx, `SELECT pr.capture_page_id::text,cp.capture_batch_id::text,pr.template_id::text,pr.template_content_hash,pr.page_no,cp.revision FROM page_registration_run pr JOIN capture_page cp ON cp.tenant_id=pr.tenant_id AND cp.id=pr.capture_page_id WHERE pr.tenant_id=$1 AND pr.id=$2::uuid AND pr.deleted_at IS NULL AND cp.deleted_at IS NULL AND pr.id=(SELECT latest.id FROM page_registration_run latest WHERE latest.tenant_id=pr.tenant_id AND latest.capture_page_id=pr.capture_page_id AND latest.processing_status<>'invalidated' AND latest.deleted_at IS NULL ORDER BY latest.created_at DESC LIMIT 1) FOR UPDATE OF pr,cp`, tenantID, runID).Scan(&pageID, &batchID, &templateID, &templateHash, &pageNo, &pageRevision)
	if err != nil {
		return RegistrationCorrection{}, mapNotFound(err)
	}
	if err = ensureBatchWritableTx(ctx, tx, tenantID, batchID); err != nil {
		return RegistrationCorrection{}, err
	}
	if pageRevision != input.PageRevision {
		return RegistrationCorrection{}, ErrConflict
	}
	_, err = tx.ExecContext(ctx, `UPDATE page_registration_correction SET status='superseded',updated_at=now() WHERE tenant_id=$1 AND capture_page_id=$2::uuid AND created_by=$3::uuid AND status IN('draft','queued','preview_ready') AND deleted_at IS NULL`, tenantID, pageID, actorID)
	if err != nil {
		return RegistrationCorrection{}, err
	}
	sourceRaw, _ := json.Marshal(input.SourcePoints)
	templateRaw, _ := json.Marshal(input.TemplatePoints)
	row := tx.QueryRowContext(ctx, `INSERT INTO page_registration_correction(tenant_id,capture_page_id,base_registration_run_id,source_page_revision,template_id,template_content_hash,page_no,source_points,template_points,advanced_anchor_mode,status,points_hash,created_by,expires_at) VALUES($1,$2::uuid,$3::uuid,$4,$5::uuid,$6,$7,$8,$9,$10,'draft',$11,$12::uuid,now()+interval '15 minutes') RETURNING `+correctionColumns, tenantID, pageID, runID, pageRevision, templateID, templateHash, pageNo, sourceRaw, templateRaw, input.AdvancedAnchorMode, correctionPointsHash(input.SourcePoints, input.TemplatePoints), actorID)
	out, err := scanRegistrationCorrection(row)
	if err != nil {
		return RegistrationCorrection{}, err
	}
	return out, tx.Commit()
}

func (s *PostgresStore) GetRegistrationCorrection(ctx context.Context, tenantID, correctionID string) (RegistrationCorrection, error) {
	return scanRegistrationCorrection(s.db.QueryRowContext(ctx, `SELECT `+correctionColumns+` FROM page_registration_correction WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL`, tenantID, correctionID))
}

func (s *PostgresStore) QueueRegistrationCorrectionPreview(ctx context.Context, tenantID, correctionID, actorID string, revision int) (RegistrationCorrection, error) {
	if revision <= 0 {
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
	if current.Revision != revision {
		return RegistrationCorrection{}, ErrConflict
	}
	if current.ExpiresAt.Before(time.Now()) || current.AttemptCount >= 8 || current.Status != "draft" && current.Status != "failed" {
		return RegistrationCorrection{}, ErrInvalidTransition
	}
	var batchID, examID, sourceAssetID, templateAssetID, templateContentType string
	var pageRevision int
	var layoutRaw []byte
	err = tx.QueryRowContext(ctx, `SELECT cp.capture_batch_id::text,s.exam_id::text,cp.revision,pr.source_file_asset_id::text,ep.file_asset_id::text,fa.content_type,ast.layout FROM capture_page cp JOIN submission s ON s.tenant_id=cp.tenant_id AND s.id=cp.submission_id JOIN page_registration_run pr ON pr.tenant_id=cp.tenant_id AND pr.id=$3::uuid JOIN answer_sheet_template ast ON ast.tenant_id=pr.tenant_id AND ast.id=pr.template_id JOIN exam_paper ep ON ep.tenant_id=ast.tenant_id AND ep.id=ast.exam_paper_id JOIN file_asset fa ON fa.tenant_id=ep.tenant_id AND fa.id=ep.file_asset_id WHERE cp.tenant_id=$1 AND cp.id=$2::uuid FOR UPDATE OF cp`, tenantID, current.CapturePageID, current.BaseRegistrationRunID).Scan(&batchID, &examID, &pageRevision, &sourceAssetID, &templateAssetID, &templateContentType, &layoutRaw)
	if err != nil {
		return RegistrationCorrection{}, mapNotFound(err)
	}
	if err = ensureBatchWritableTx(ctx, tx, tenantID, batchID); err != nil {
		return RegistrationCorrection{}, err
	}
	if pageRevision != current.SourcePageRevision {
		return RegistrationCorrection{}, ErrConflict
	}
	var layout templateLayout
	if json.Unmarshal(layoutRaw, &layout) != nil {
		return RegistrationCorrection{}, ErrInvalidInput
	}
	var regions []map[string]any
	for _, page := range layout.Pages {
		if page.PageNo == current.PageNo {
			regions = page.QuestionRegions
		}
	}
	if regions == nil {
		return RegistrationCorrection{}, ErrInvalidInput
	}
	payload, _ := json.Marshal(map[string]any{"correction_id": current.ID, "capture_page_id": current.CapturePageID, "exam_id": examID, "source_download_url": "/api/v1/files/" + sourceAssetID + "/download", "source_content_type": "image/png", "template_download_url": "/api/v1/files/" + templateAssetID + "/download", "template_content_type": templateContentType, "template_page_index": current.PageNo, "source_points": current.SourcePoints, "template_points": current.TemplatePoints, "question_regions": regions, "render_dpi": 300})
	// 同一组点允许人工再次尝试，键中加入尝试次数，避免重用已结束的预览任务。
	key := "registration-correction:" + current.ID + ":" + current.ValidationReportHash() + ":a" + strconv.Itoa(current.AttemptCount+1)
	var taskID string
	err = tx.QueryRowContext(ctx, `INSERT INTO agent_worker_task(tenant_id,task_type,queue_name,source_type,source_id,priority,payload,payload_schema_version,idempotency_key,dedupe_key,max_attempts,retry_backoff_seconds,created_by) VALUES($1,'page_registration_correction_preview','page-processing','page_registration_correction',$2::uuid,65,$3,'page-registration-correction-v1',$4,$4,3,10,$5::uuid) ON CONFLICT(tenant_id,task_type,idempotency_key) DO UPDATE SET updated_at=agent_worker_task.updated_at RETURNING id::text`, tenantID, current.ID, payload, key, actorID).Scan(&taskID)
	if err != nil {
		return RegistrationCorrection{}, err
	}
	out, err := scanRegistrationCorrection(tx.QueryRowContext(ctx, `UPDATE page_registration_correction SET status='queued',runtime_task_id=$3::uuid,attempt_count=attempt_count+1,revision=revision+1,error_code=NULL,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid RETURNING `+correctionColumns, tenantID, correctionID, taskID))
	if err != nil {
		return RegistrationCorrection{}, err
	}
	return out, tx.Commit()
}

func (s *PostgresStore) ApplyRegistrationCorrectionPreview(ctx context.Context, tenantID, correctionID string, input CorrectionPreviewResultInput) (RegistrationCorrection, error) {
	if input.PreviewRegisteredFileAssetID == "" || len(input.SourceToTemplate) == 0 || len(input.TemplateToSource) == 0 || input.Coverage < 0.70 || input.Coverage > 1.15 || len(input.Segments) == 0 {
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
	if current.Status == "preview_ready" {
		return current, tx.Commit()
	}
	if current.Status != "queued" {
		return RegistrationCorrection{}, ErrInvalidTransition
	}
	segments, _ := json.Marshal(input.Segments)
	forward, _ := json.Marshal(input.SourceToTemplate)
	inverse, _ := json.Marshal(input.TemplateToSource)
	report, _ := json.Marshal(input.ValidationReport)
	out, err := scanRegistrationCorrection(tx.QueryRowContext(ctx, `UPDATE page_registration_correction SET status='preview_ready',preview_registered_file_asset_id=$3::uuid,preview_segments=$4,source_to_template_matrix=$5,template_to_source_matrix=$6,coverage=$7,reprojection_error=$8,validation_report=$9,error_code=NULL,revision=revision+1,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid RETURNING `+correctionColumns, tenantID, correctionID, input.PreviewRegisteredFileAssetID, segments, forward, inverse, input.Coverage, input.ReprojectionError, report))
	if err != nil {
		return RegistrationCorrection{}, err
	}
	return out, tx.Commit()
}

func (s *PostgresStore) ApplyRegistrationCorrectionFailure(ctx context.Context, tenantID, correctionID, errorCode string, detail map[string]any) (RegistrationCorrection, error) {
	if errorCode == "" {
		return RegistrationCorrection{}, ErrInvalidInput
	}
	report, _ := json.Marshal(map[string]any{"passed": false, "error": errorCode, "detail": detail})
	return scanRegistrationCorrection(s.db.QueryRowContext(ctx, `UPDATE page_registration_correction SET status='failed',error_code=$3,validation_report=$4,revision=revision+1,updated_at=now() WHERE tenant_id=$1 AND id=$2::uuid AND status='queued' AND deleted_at IS NULL RETURNING `+correctionColumns, tenantID, correctionID, errorCode, report))
}

func (x RegistrationCorrection) ValidationReportHash() string {
	return correctionPointsHash(x.SourcePoints, x.TemplatePoints)
}

func scanRegistrationCorrection(row scanner) (RegistrationCorrection, error) {
	var x RegistrationCorrection
	var source, target, segments, forward, inverse, report, snapshot []byte
	var applied, undone sql.NullTime
	err := row.Scan(&x.ID, &x.CapturePageID, &x.BaseRegistrationRunID, &x.AppliedRegistrationRunID, &x.SourcePageRevision, &x.TemplateID, &x.TemplateContentHash, &x.PageNo, &source, &target, &x.AdvancedAnchorMode, &x.Status, &x.Revision, &x.AttemptCount, &x.PreviewRegisteredFileAssetID, &segments, &forward, &inverse, &x.Coverage, &x.ReprojectionError, &report, &snapshot, &x.RuntimeTaskID, &x.ErrorCode, &x.ExpiresAt, &applied, &undone, &x.CreatedAt)
	if err != nil {
		return RegistrationCorrection{}, mapNotFound(err)
	}
	_ = json.Unmarshal(source, &x.SourcePoints)
	_ = json.Unmarshal(target, &x.TemplatePoints)
	_ = json.Unmarshal(segments, &x.PreviewSegments)
	_ = json.Unmarshal(forward, &x.SourceToTemplate)
	_ = json.Unmarshal(inverse, &x.TemplateToSource)
	_ = json.Unmarshal(report, &x.ValidationReport)
	_ = json.Unmarshal(snapshot, &x.PreviousRegistrationSnapshot)
	if x.PreviewSegments == nil {
		x.PreviewSegments = []SegmentCropInput{}
	}
	if x.SourceToTemplate == nil {
		x.SourceToTemplate = []any{}
	}
	if x.TemplateToSource == nil {
		x.TemplateToSource = []any{}
	}
	if x.ValidationReport == nil {
		x.ValidationReport = map[string]any{}
	}
	if x.PreviousRegistrationSnapshot == nil {
		x.PreviousRegistrationSnapshot = map[string]any{}
	}
	if applied.Valid {
		value := applied.Time.UTC()
		x.AppliedAt = &value
	}
	if undone.Valid {
		value := undone.Time.UTC()
		x.UndoneAt = &value
	}
	return x, nil
}
