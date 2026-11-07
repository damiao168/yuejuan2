package grading

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"encoding/json"
	"fmt"
)

// queueScoringOMRTx 先冻结服务端策略，再建立 Worker 任务和运行关联；外层事务失败时这些事实一起回滚。
func (s *PostgresStore) queueScoringOMRTx(ctx context.Context, tx *sql.Tx, tenantID, examID, runID, actorID string, item scoringOMRSegment) error {
	segment, options := item.segment, item.options
	var templateLayout paper.TemplateLayout
	if err := decodeJSONB(segment.templateLayout, &templateLayout, "answer_sheet_template.layout"); err != nil {
		return err
	}
	currentReference := paper.TemplateOMRReference{
		Source:      paper.OMRReferenceSourceExamPaper,
		FileAssetID: segment.referenceAssetID,
		HashSHA256:  segment.referenceHash,
		ContentType: segment.referenceContentType,
	}
	basePolicy := paper.OMRAutoConfirmPolicyForTemplateReference(templateLayout, segment.templateStatus, segment.currentTemplateHash, segment.templateHash, currentReference, segment.questionID)
	var calibration *paper.OMRCalibrationApproval
	if basePolicy.Reference != nil && basePolicy.RuntimeProfile.Mode == paper.OMRProfileModeTemplateDifference {
		scope := omrCalibrationScope{
			templateID:           segment.templateID,
			templateContentHash:  segment.templateHash,
			profileVersion:       basePolicy.RuntimeProfile.Version,
			profileHash:          basePolicy.ProfileHash,
			referenceFileAssetID: basePolicy.Reference.FileAssetID,
			referenceSHA256:      basePolicy.Reference.HashSHA256,
		}
		var err error
		calibration, err = s.loadApprovedOMRCalibrationTx(ctx, tx, tenantID, scope, segment.questionID)
		if err != nil {
			return err
		}
	}
	// 冻结服务端校准资格、参考图和阈值到 OMR 运行；Worker 只接收识别配置，不能自行授予自动确认。
	template := paper.AnswerSheetTemplate{Layout: templateLayout, Status: segment.templateStatus, ContentHash: segment.currentTemplateHash, ID: segment.templateID}
	policy := paper.OMRAutoConfirmPolicyForTemplateReferenceAndCalibration(template, segment.templateHash, currentReference, segment.questionID, calibration)
	referenceAssetID, referenceSHA256 := "", ""
	if policy.Reference != nil {
		referenceAssetID, referenceSHA256 = policy.Reference.FileAssetID, policy.Reference.HashSHA256
	}
	calibrationID, calibrationEvidenceHash := "", ""
	if policy.Calibration != nil {
		calibrationID, calibrationEvidenceHash = policy.Calibration.ID, policy.Calibration.EvidenceHash
	}
	var omrID string
	err := tx.QueryRowContext(ctx, `INSERT INTO omr_run (tenant_id,scoring_run_id,answer_segment_id,crop_file_asset_id,crop_sha256,template_id,template_content_hash,profile_version,profile_hash,reference_file_asset_id,reference_sha256,calibration_session_id,calibration_evidence_hash,auto_confirm_min_confidence,auto_confirm_eligible,auto_confirm_reason)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6::uuid,$7,$8,$9,NULLIF($10,'')::uuid,NULLIF($11,''),NULLIF($12,'')::uuid,NULLIF($13,''),$14,$15,$16) RETURNING id::text`, tenantID, runID, segment.id, segment.cropID, segment.cropHash, segment.templateID, segment.templateHash, policy.RuntimeProfile.Version, policy.ProfileHash, referenceAssetID, referenceSHA256, calibrationID, calibrationEvidenceHash, policy.MinimumConfidence, policy.AutoConfirmEligible, policy.Reason).Scan(&omrID)
	if err != nil {
		return err
	}
	taskPayload := map[string]any{"exam_id": examID, "answer_segment_id": segment.id, "omr_run_id": omrID, "source_download_url": "/api/v1/internal/answer-segments/" + segment.id + "/image", "source_content_type": segment.contentType, "option_regions": options, "multiple": segment.kind == "multiple_choice", "profile": policy.RuntimeProfile, "profile_hash": policy.ProfileHash}
	if policy.Reference != nil {
		pageWidth, pageHeight := templatePageDimensions(templateLayout, segment.questionID)
		taskPayload["reference"] = map[string]any{
			"file_asset_id": policy.Reference.FileAssetID,
			"download_url":  "/api/v1/files/" + policy.Reference.FileAssetID + "/download",
			"sha256":        policy.Reference.HashSHA256,
			"content_type":  policy.Reference.ContentType,
			"page_no":       policy.Reference.PageNo,
			"page_width":    pageWidth,
			"page_height":   pageHeight,
			"question_region": map[string]any{
				"x": policy.Reference.X, "y": policy.Reference.Y,
				"width": policy.Reference.Width, "height": policy.Reference.Height,
			},
		}
	}
	payload, err := json.Marshal(taskPayload)
	if err != nil {
		return fmt.Errorf("encode omr worker task payload: %w", err)
	}
	var taskID string
	err = tx.QueryRowContext(ctx, `INSERT INTO agent_worker_task (tenant_id,task_type,queue_name,source_type,source_id,priority,payload,payload_schema_version,idempotency_key,dedupe_key,max_attempts,retry_backoff_seconds,created_by)
VALUES ($1::uuid,'omr_extract','page-processing','omr_run',$2::uuid,50,$3::jsonb,'omr-task-v2',$4,$4,3,5,$5::uuid)
ON CONFLICT (tenant_id,task_type,idempotency_key) DO UPDATE SET idempotency_key=EXCLUDED.idempotency_key RETURNING id::text`, tenantID, omrID, payload, "omr:"+omrID+":"+segment.cropHash, actorID).Scan(&taskID)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE omr_run SET runtime_task_id=$3::uuid WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, omrID, taskID); err != nil {
		return err
	}
	return nil
}

func cropRelativeOptionRegions(area map[string]any, options []any) []any {
	// 题区内的坐标有时恰好也落在页面题区范围；显式标记优先，避免旧启发式再次转换。
	if area["option_coordinate_space"] == "question_relative" {
		return options
	}
	areaX, xOK := area["x"].(float64)
	areaY, yOK := area["y"].(float64)
	areaWidth, widthOK := area["width"].(float64)
	areaHeight, heightOK := area["height"].(float64)
	if !xOK || !yOK || !widthOK || !heightOK || areaWidth <= 0 || areaHeight <= 0 {
		return options
	}
	normalized := make([]any, 0, len(options))
	for _, raw := range options {
		option, ok := raw.(map[string]any)
		if !ok {
			return options
		}
		x, xOK := option["x"].(float64)
		y, yOK := option["y"].(float64)
		width, widthOK := option["width"].(float64)
		height, heightOK := option["height"].(float64)
		if !xOK || !yOK || !widthOK || !heightOK ||
			x < areaX || y < areaY || x+width > areaX+areaWidth+0.000001 || y+height > areaY+areaHeight+0.000001 {
			// 部分集成已提供相对裁剪图的区域；保留这些值，避免重复转换。
			return options
		}
		relative := make(map[string]any, len(option))
		for key, value := range option {
			relative[key] = value
		}
		relative["x"] = (x - areaX) / areaWidth
		relative["y"] = (y - areaY) / areaHeight
		relative["width"] = width / areaWidth
		relative["height"] = height / areaHeight
		normalized = append(normalized, relative)
	}
	return normalized
}

func templatePageDimensions(layout paper.TemplateLayout, questionID string) (int, int) {
	for _, page := range layout.Pages {
		for _, region := range page.QuestionRegions {
			if region.QuestionID == questionID {
				return page.Width, page.Height
			}
		}
	}
	return 0, 0
}
