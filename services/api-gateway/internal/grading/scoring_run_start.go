package grading

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"edugrade-enterprise/services/api-gateway/internal/paper"
)

func (s *PostgresStore) StartScoringRun(ctx context.Context, tenantID, examID, actorID string, input StartScoringRunInput) (ScoringRun, error) {
	return s.startScoringRun(ctx, tenantID, examID, actorID, input, "")
}

func (s *PostgresStore) ReprocessSegmentScore(ctx context.Context, tenantID, segmentID, actorID string, input StartScoringRunInput) (ScoringRun, error) {
	segmentID = strings.TrimSpace(segmentID)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if segmentID == "" || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 100 {
		return ScoringRun{}, ErrInvalidInput
	}
	var examID string
	err := s.db.QueryRowContext(ctx, `SELECT sub.exam_id::text FROM answer_segment seg JOIN submission sub ON sub.tenant_id=seg.tenant_id AND sub.id=seg.submission_id AND sub.deleted_at IS NULL WHERE seg.tenant_id=$1::uuid AND seg.id=$2::uuid AND seg.deleted_at IS NULL`, tenantID, segmentID).Scan(&examID)
	if errors.Is(err, sql.ErrNoRows) {
		return ScoringRun{}, ErrNotFound
	}
	if err != nil {
		return ScoringRun{}, err
	}
	input.IdempotencyKey = "segment-reprocess:" + segmentID + ":" + input.IdempotencyKey
	return s.startScoringRun(ctx, tenantID, examID, actorID, input, segmentID)
}

func (s *PostgresStore) startScoringRun(ctx context.Context, tenantID, examID, actorID string, input StartScoringRunInput, segmentID string) (ScoringRun, error) {
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.IdempotencyKey == "" || len(input.IdempotencyKey) > 160 {
		return ScoringRun{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ScoringRun{}, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, tenantID+":"+actorID+":"+input.IdempotencyKey+":scoring-command"); err != nil {
		return ScoringRun{}, err
	}
	requestHash := scoringCommandHash(examID, segmentID)
	var otherID string
	err = tx.QueryRowContext(ctx, `SELECT id::text FROM scoring_run WHERE tenant_id=$1::uuid AND started_by=$2::uuid AND idempotency_key=$3 AND exam_id<>$4::uuid LIMIT 1`, tenantID, actorID, input.IdempotencyKey, examID).Scan(&otherID)
	if err == nil {
		return ScoringRun{}, ErrCommandConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ScoringRun{}, err
	}
	// Serialise starts for one exam. A second unresolved run would otherwise
	// queue duplicate OMR work and compete to replace the same current grades.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, examID+":scoring-run"); err != nil {
		return ScoringRun{}, err
	}
	var existing string
	var existingActor string
	var existingHash sql.NullString
	err = tx.QueryRowContext(ctx, `SELECT id::text,started_by::text,command_request_hash FROM scoring_run WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND idempotency_key=$3`, tenantID, examID, input.IdempotencyKey).Scan(&existing, &existingActor, &existingHash)
	if err == nil {
		if existingActor != actorID || (existingHash.Valid && existingHash.String != requestHash) {
			return ScoringRun{}, ErrCommandConflict
		}
		run, getErr := scanScoringRun(tx.QueryRowContext(ctx, scoringRunSelect+` WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, existing))
		if getErr != nil {
			return ScoringRun{}, getErr
		}
		return run, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ScoringRun{}, err
	}
	if segmentID == "" {
		readiness, readinessErr := calculateScoringReadiness(ctx, tx, tenantID, examID)
		if readinessErr != nil {
			return ScoringRun{}, readinessErr
		}
		if !readiness.Ready {
			return ScoringRun{}, &ScoringReadinessError{Readiness: readiness}
		}
	}
	var unresolvedRunID string
	err = tx.QueryRowContext(ctx, `
SELECT id::text
FROM scoring_run
WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND deleted_at IS NULL
  AND status IN ('queued','processing','needs_review','failed','cancelling')
ORDER BY created_at DESC
LIMIT 1
`, tenantID, examID).Scan(&unresolvedRunID)
	if err == nil {
		return ScoringRun{}, ErrInvalidTransition
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ScoringRun{}, err
	}
	if segmentID != "" {
		var exists int
		err = tx.QueryRowContext(ctx, `
SELECT 1
FROM answer_segment seg
JOIN submission sub ON sub.tenant_id=seg.tenant_id AND sub.id=seg.submission_id AND sub.deleted_at IS NULL
WHERE seg.tenant_id=$1::uuid AND sub.exam_id=$2::uuid AND seg.id=$3::uuid
  AND seg.deleted_at IS NULL AND seg.processing_status='completed' AND seg.crop_file_asset_id IS NOT NULL
`, tenantID, examID, segmentID).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return ScoringRun{}, ErrNotFound
		}
		if err != nil {
			return ScoringRun{}, err
		}
		var activeReview, activeOMR int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM review_task WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND status IN ('pending','assigned','in_progress','returned') AND deleted_at IS NULL`, tenantID, segmentID).Scan(&activeReview); err != nil {
			return ScoringRun{}, err
		}
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM omr_run WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND status IN ('queued','processing','retryable_error') AND deleted_at IS NULL`, tenantID, segmentID).Scan(&activeOMR); err != nil {
			return ScoringRun{}, err
		}
		if activeReview > 0 || activeOMR > 0 {
			return ScoringRun{}, ErrInvalidTransition
		}
	}
	var runID string
	err = tx.QueryRowContext(ctx, `
INSERT INTO scoring_run (tenant_id, exam_id, idempotency_key, status, started_by, started_at, command_request_hash)
SELECT $1::uuid, e.id, $3, 'processing', $4::uuid, now(), $5
FROM exam e WHERE e.tenant_id=$1::uuid AND e.id=$2::uuid AND e.deleted_at IS NULL AND e.status IN ('collecting','processing','grading')
RETURNING id::text`, tenantID, examID, input.IdempotencyKey, actorID, requestHash).Scan(&runID)
	if errors.Is(err, sql.ErrNoRows) {
		return ScoringRun{}, ErrInvalidInput
	}
	if err != nil {
		return ScoringRun{}, err
	}
	if segmentID != "" {
		if _, err = tx.ExecContext(ctx, `UPDATE answer_candidate SET is_current=false WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND is_current`, tenantID, segmentID); err != nil {
			return ScoringRun{}, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE question_grade SET is_current=false,status='invalidated' WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND is_current AND deleted_at IS NULL`, tenantID, segmentID); err != nil {
			return ScoringRun{}, err
		}
	}

	rows, err := tx.QueryContext(ctx, `
 SELECT seg.id::text, seg.submission_id::text, q.id::text, q.question_no, q.question_type,
   seg.crop_file_asset_id::text, seg.crop_sha256, seg.template_id::text, seg.template_content_hash,
   q.answer_area, COALESCE(NULLIF(sub.candidate_no,''), seg.id::text), fa.content_type,
   COALESCE(sr.id::text,''), COALESCE(ans.id::text,''), COALESCE(ans.answer_text,''),
   COALESCE(ans.answer_payload,'{}'::jsonb), COALESCE(ans.source,''), ans.confidence::float8,
   COALESCE(ast.status,''), COALESCE(ast.content_hash,''), COALESCE(ast.layout,'{}'::jsonb),
   COALESCE(reference_file.id::text,''), COALESCE(reference_file.hash_sha256,''), COALESCE(reference_file.content_type,'')
 FROM answer_segment seg
 JOIN submission sub ON sub.tenant_id=seg.tenant_id AND sub.id=seg.submission_id AND sub.deleted_at IS NULL
 JOIN question q ON q.tenant_id=seg.tenant_id AND q.id=seg.question_id AND q.deleted_at IS NULL
 JOIN file_asset fa ON fa.tenant_id=seg.tenant_id AND fa.id=seg.crop_file_asset_id AND fa.deleted_at IS NULL
	LEFT JOIN answer_sheet_template ast ON ast.tenant_id=seg.tenant_id AND ast.id=seg.template_id AND ast.deleted_at IS NULL
 LEFT JOIN exam_paper reference_paper ON reference_paper.tenant_id=ast.tenant_id AND reference_paper.id=ast.exam_paper_id AND reference_paper.deleted_at IS NULL
 LEFT JOIN file_asset reference_file ON reference_file.tenant_id=reference_paper.tenant_id AND reference_file.id=reference_paper.file_asset_id AND reference_file.deleted_at IS NULL
 LEFT JOIN scoring_rule sr ON sr.tenant_id=q.tenant_id AND sr.question_id=q.id AND sr.status='published' AND sr.deleted_at IS NULL
LEFT JOIN LATERAL (SELECT id,answer_text,answer_payload,source,confidence FROM answer_segment_answer WHERE tenant_id=seg.tenant_id AND answer_segment_id=seg.id AND deleted_at IS NULL ORDER BY created_at DESC LIMIT 1) ans ON true
WHERE seg.tenant_id=$1::uuid AND sub.exam_id=$2::uuid AND seg.deleted_at IS NULL
  AND seg.processing_status='completed' AND seg.crop_file_asset_id IS NOT NULL
  AND (NULLIF($3,'')::uuid IS NULL OR seg.id=NULLIF($3,'')::uuid)
ORDER BY q.sort_order, seg.created_at`, tenantID, examID, segmentID)
	if err != nil {
		return ScoringRun{}, err
	}
	type segmentRow struct {
		id, submissionID, questionID, no, kind, cropID, cropHash, templateID, templateHash, anonymous, contentType, ruleID string
		answerID, answerText, answerSource, templateStatus, currentTemplateHash                                            string
		referenceAssetID, referenceHash, referenceContentType                                                              string
		area, answerPayload, templateLayout                                                                                []byte
		answerConfidence                                                                                                   sql.NullFloat64
	}
	segments := []segmentRow{}
	for rows.Next() {
		var item segmentRow
		if err := rows.Scan(&item.id, &item.submissionID, &item.questionID, &item.no, &item.kind, &item.cropID, &item.cropHash, &item.templateID, &item.templateHash, &item.area, &item.anonymous, &item.contentType, &item.ruleID, &item.answerID, &item.answerText, &item.answerPayload, &item.answerSource, &item.answerConfidence, &item.templateStatus, &item.currentTemplateHash, &item.templateLayout, &item.referenceAssetID, &item.referenceHash, &item.referenceContentType); err != nil {
			rows.Close()
			return ScoringRun{}, err
		}
		segments = append(segments, item)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return ScoringRun{}, err
	}
	if err = rows.Close(); err != nil {
		return ScoringRun{}, err
	}
	insertReviewTask := func(segment segmentRow, source, reason string) (bool, error) {
		result, insertErr := tx.ExecContext(ctx, `INSERT INTO review_task (tenant_id,exam_id,question_id,question_no,answer_segment_id,submission_id,anonymous_code,source,status,priority,grade_round,reason_code,scoring_run_id,created_by)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4,$5::uuid,$6::uuid,$7,$8,'pending',50,'single',$9,$10::uuid,$11::uuid)
ON CONFLICT (tenant_id,answer_segment_id,source,grade_round) WHERE status IN ('pending','assigned','in_progress','returned') AND deleted_at IS NULL AND source <> 'ai_panel_disagreement' DO NOTHING`, tenantID, examID, segment.questionID, segment.no, segment.id, segment.submissionID, segment.anonymous, source, reason, runID, actorID)
		if insertErr != nil {
			return false, insertErr
		}
		count, _ := result.RowsAffected()
		return count == 1, nil
	}
	queued, review := 0, 0
	for _, segment := range segments {
		var area map[string]any
		if err := decodeJSONB(segment.area, &area, "question.answer_area"); err != nil {
			return ScoringRun{}, err
		}
		options, _ := area["option_regions"].([]any)
		options = cropRelativeOptionRegions(area, options)
		isOMR := segment.kind == "single_choice" || segment.kind == "true_false" || segment.kind == "multiple_choice"
		isTextRule := segment.kind == "fill_blank" || segment.kind == "numeric"
		if isTextRule && segment.ruleID != "" && segment.answerID != "" {
			confidence := 0.0
			if segment.answerConfidence.Valid {
				confidence = segment.answerConfidence.Float64
			}
			confirmed := segment.answerSource == "manual_entry" || segment.answerSource == "imported_answer" || confidence >= 0.9
			decision := "ambiguous"
			if confirmed {
				decision = "confirmed"
			}
			source := "ocr"
			if segment.answerSource == "manual_entry" {
				source = "manual"
			}
			if segment.answerSource == "imported_answer" {
				source = "imported"
			}
			if _, err = tx.ExecContext(ctx, `UPDATE answer_candidate SET is_current=false WHERE tenant_id=$1::uuid AND answer_segment_id=$2::uuid AND is_current`, tenantID, segment.id); err != nil {
				return ScoringRun{}, err
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO answer_candidate(tenant_id,answer_segment_id,scoring_run_id,exam_question_snapshot_id,source,payload,display_text,confidence,decision,evidence,engine_version,profile_version,input_hash,is_current,created_by) VALUES($1::uuid,$2::uuid,$3::uuid,(SELECT eqs.id FROM answer_segment snapshot_seg JOIN question snapshot_q ON snapshot_q.tenant_id=snapshot_seg.tenant_id AND snapshot_q.id=snapshot_seg.question_id JOIN exam_question_snapshot eqs ON eqs.tenant_id=snapshot_q.tenant_id AND eqs.exam_id=snapshot_q.exam_id AND eqs.question_id=snapshot_q.id WHERE snapshot_seg.tenant_id=$1::uuid AND snapshot_seg.id=$2::uuid),$4,$5::jsonb,$6,NULLIF($7,0),$8,jsonb_build_object('answer_segment_answer_id',$9::text),'rule-input-v1','rule-input-v1',$10,true,$11::uuid)`, tenantID, segment.id, runID, source, segment.answerPayload, segment.answerText, confidence, decision, segment.answerID, "sha256:"+strings.ReplaceAll(segment.answerID, "-", ""), actorID)
			if err != nil {
				return ScoringRun{}, err
			}
			if !confirmed {
				created, insertErr := insertReviewTask(segment, "rule_review_required", "answer_low_confidence")
				if insertErr != nil {
					return ScoringRun{}, insertErr
				}
				if created {
					review++
				}
			}
		} else if isOMR && segment.ruleID != "" && len(options) >= 2 {
			var templateLayout paper.TemplateLayout
			if err := decodeJSONB(segment.templateLayout, &templateLayout, "answer_sheet_template.layout"); err != nil {
				return ScoringRun{}, err
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
				calibration, err = s.loadApprovedOMRCalibrationTx(ctx, tx, tenantID, segment.templateID, segment.templateHash, segment.questionID, basePolicy.RuntimeProfile.Version, basePolicy.ProfileHash, basePolicy.Reference.FileAssetID, basePolicy.Reference.HashSHA256)
				if err != nil {
					return ScoringRun{}, err
				}
			}
			policy := paper.OMRAutoConfirmPolicyForTemplateReferenceAndCalibration(templateLayout, segment.templateStatus, segment.currentTemplateHash, segment.templateHash, currentReference, segment.questionID, segment.templateID, calibration)
			referenceAssetID, referenceSHA256 := "", ""
			if policy.Reference != nil {
				referenceAssetID, referenceSHA256 = policy.Reference.FileAssetID, policy.Reference.HashSHA256
			}
			calibrationID, calibrationEvidenceHash := "", ""
			if policy.Calibration != nil {
				calibrationID, calibrationEvidenceHash = policy.Calibration.ID, policy.Calibration.EvidenceHash
			}
			var omrID string
			err = tx.QueryRowContext(ctx, `INSERT INTO omr_run (tenant_id,scoring_run_id,answer_segment_id,crop_file_asset_id,crop_sha256,template_id,template_content_hash,profile_version,profile_hash,reference_file_asset_id,reference_sha256,calibration_session_id,calibration_evidence_hash,auto_confirm_min_confidence,auto_confirm_eligible,auto_confirm_reason)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,$6::uuid,$7,$8,$9,NULLIF($10,'')::uuid,NULLIF($11,''),NULLIF($12,'')::uuid,NULLIF($13,''),$14,$15,$16) RETURNING id::text`, tenantID, runID, segment.id, segment.cropID, segment.cropHash, segment.templateID, segment.templateHash, policy.RuntimeProfile.Version, policy.ProfileHash, referenceAssetID, referenceSHA256, calibrationID, calibrationEvidenceHash, policy.MinimumConfidence, policy.AutoConfirmEligible, policy.Reason).Scan(&omrID)
			if err != nil {
				return ScoringRun{}, err
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
				return ScoringRun{}, fmt.Errorf("encode omr worker task payload: %w", err)
			}
			var taskID string
			err = tx.QueryRowContext(ctx, `INSERT INTO agent_worker_task (tenant_id,task_type,queue_name,source_type,source_id,priority,payload,payload_schema_version,idempotency_key,dedupe_key,max_attempts,retry_backoff_seconds,created_by)
VALUES ($1::uuid,'omr_extract','page-processing','omr_run',$2::uuid,50,$3::jsonb,'omr-task-v2',$4,$4,3,5,$5::uuid)
ON CONFLICT (tenant_id,task_type,idempotency_key) DO UPDATE SET idempotency_key=EXCLUDED.idempotency_key RETURNING id::text`, tenantID, omrID, payload, "omr:"+omrID+":"+segment.cropHash, actorID).Scan(&taskID)
			if err != nil {
				return ScoringRun{}, err
			}
			if _, err = tx.ExecContext(ctx, `UPDATE omr_run SET runtime_task_id=$3::uuid WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, omrID, taskID); err != nil {
				return ScoringRun{}, err
			}
			queued++
		} else {
			reason := "rule_review_required"
			if isOMR && len(options) < 2 {
				reason = "omr_option_regions_missing"
			}
			if isOMR && segment.ruleID == "" {
				reason = "scoring_rule_missing"
			}
			created, insertErr := insertReviewTask(segment, "rule_review_required", reason)
			if insertErr != nil {
				return ScoringRun{}, insertErr
			}
			if created {
				review++
			}
		}
	}
	status := "processing"
	if queued == 0 && review > 0 {
		status = "needs_review"
	}
	if len(segments) == 0 {
		status = "failed"
	}
	_, err = tx.ExecContext(ctx, `UPDATE scoring_run SET status=$3,total_count=$4,queued_count=$5,review_count=$6,failed_count=$7,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, runID, status, len(segments), queued, review, map[bool]int{true: 1, false: 0}[len(segments) == 0])
	if err != nil {
		return ScoringRun{}, err
	}
	run, err := scanScoringRun(tx.QueryRowContext(ctx, scoringRunSelect+` WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, runID))
	if err != nil {
		return ScoringRun{}, err
	}
	return run, tx.Commit()
}

func cropRelativeOptionRegions(area map[string]any, options []any) []any {
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
			// Some integrations already provide crop-relative regions. Preserve
			// those values instead of applying the conversion twice.
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
