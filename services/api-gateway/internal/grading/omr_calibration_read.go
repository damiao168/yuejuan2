package grading

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

func (s *PostgresStore) ListOMRCalibrations(ctx context.Context, tenantID, templateID string) ([]OMRCalibrationSession, error) {
	templateID = strings.TrimSpace(templateID)
	if templateID == "" {
		return nil, ErrInvalidInput
	}
	rows, err := s.db.QueryContext(ctx, `SELECT `+omrCalibrationSessionColumns+`
FROM omr_calibration_session
WHERE tenant_id=$1::uuid AND template_id=$2::uuid AND deleted_at IS NULL
ORDER BY created_at DESC`, tenantID, templateID)
	if err != nil {
		return nil, err
	}
	out := []OMRCalibrationSession{}
	for rows.Next() {
		var item OMRCalibrationSession
		if err := scanOMRCalibrationSession(rows, &item); err != nil {
			_ = rows.Close()
			return nil, err
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for index := range out {
		cases, caseErr := listOMRCalibrationCases(ctx, s.db, tenantID, out[index].ID)
		if caseErr != nil {
			return nil, caseErr
		}
		out[index].Summary = summarizeOMRCalibration(out[index], cases)
	}
	return out, nil
}

func (s *PostgresStore) GetOMRCalibration(ctx context.Context, tenantID, id string) (OMRCalibrationDetail, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return OMRCalibrationDetail{}, ErrInvalidInput
	}
	session, err := getOMRCalibrationSession(ctx, s.db, tenantID, id, false)
	if err != nil {
		return OMRCalibrationDetail{}, err
	}
	cases, err := listOMRCalibrationCases(ctx, s.db, tenantID, id)
	if err != nil {
		return OMRCalibrationDetail{}, err
	}
	session.Summary = summarizeOMRCalibration(session, cases)
	return OMRCalibrationDetail{Session: session, Cases: cases}, nil
}

func getActiveOMRCalibrationSessionTx(ctx context.Context, tx *sql.Tx, tenantID string, scope omrCalibrationScope) (OMRCalibrationSession, error) {
	row := tx.QueryRowContext(ctx, `SELECT `+omrCalibrationSessionColumns+`
FROM omr_calibration_session
WHERE tenant_id=$1::uuid AND template_id=$2::uuid AND template_content_hash=$3 AND scope_type='template'
  AND profile_version=$4 AND profile_hash=$5 AND reference_file_asset_id=$6::uuid AND reference_sha256=$7
  AND status IN ('draft','approved') AND deleted_at IS NULL
ORDER BY CASE status WHEN 'approved' THEN 0 ELSE 1 END,created_at DESC
LIMIT 1
FOR UPDATE`, tenantID, scope.templateID, scope.templateContentHash, scope.profileVersion, scope.profileHash, scope.referenceFileAssetID, scope.referenceSHA256)
	var out OMRCalibrationSession
	return out, scanOMRCalibrationSession(row, &out)
}

func getOMRCalibrationSession(ctx context.Context, queryer calibrationQueryer, tenantID, id string, forUpdate bool) (OMRCalibrationSession, error) {
	statement := `SELECT ` + omrCalibrationSessionColumns + `
FROM omr_calibration_session
WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL`
	if forUpdate {
		statement += ` FOR UPDATE`
	}
	var out OMRCalibrationSession
	return out, scanOMRCalibrationSession(queryer.QueryRowContext(ctx, statement, tenantID, id), &out)
}

func getOMRCalibrationCaseTx(ctx context.Context, tx *sql.Tx, tenantID, calibrationID, caseID string) (OMRCalibrationCase, error) {
	rows, err := listOMRCalibrationCases(ctx, tx, tenantID, calibrationID)
	if err != nil {
		return OMRCalibrationCase{}, err
	}
	for _, item := range rows {
		if item.ID == caseID {
			return item, nil
		}
	}
	return OMRCalibrationCase{}, ErrNotFound
}

func listOMRCalibrationCases(ctx context.Context, queryer calibrationQueryer, tenantID, calibrationID string) ([]OMRCalibrationCase, error) {
	rows, err := queryer.QueryContext(ctx, `
SELECT c.id::text,c.calibration_session_id::text,c.omr_run_id::text,c.answer_segment_id::text,c.crop_sha256,
  c.question_id::text,c.question_no,c.question_type,c.option_labels,c.sample_stratum,
  c.observed_decision,c.observed_options,c.observed_confidence,c.measurements,
  COALESCE(c.expected_options,'null'::jsonb),c.matches,COALESCE(c.labeled_by::text,''),c.labeled_at,c.created_at,
  COALESCE(o.overlay_file_asset_id::text,''),c.observed_result_gate
FROM omr_calibration_case c
LEFT JOIN omr_run o ON o.tenant_id=c.tenant_id AND o.id=c.omr_run_id
WHERE c.tenant_id=$1::uuid AND c.calibration_session_id=$2::uuid
ORDER BY c.created_at,c.id`, tenantID, calibrationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OMRCalibrationCase{}
	for rows.Next() {
		var item OMRCalibrationCase
		var optionLabelsRaw, observedRaw, measurementsRaw, expectedRaw, resultGateRaw []byte
		var matches sql.NullBool
		var labeledAt sql.NullTime
		if err := rows.Scan(&item.ID, &item.CalibrationID, &item.OMRRunID, &item.AnswerSegmentID, &item.CropSHA256,
			&item.QuestionID, &item.QuestionNo, &item.QuestionType, &optionLabelsRaw, &item.SampleStratum,
			&item.ObservedDecision, &observedRaw, &item.ObservedConfidence, &measurementsRaw,
			&expectedRaw, &matches, &item.LabeledBy, &labeledAt, &item.CreatedAt, &item.OverlayFileAssetID, &resultGateRaw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(optionLabelsRaw, &item.OptionLabels); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(observedRaw, &item.ObservedOptions); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(measurementsRaw, &item.Measurements); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(resultGateRaw, &item.ObservedResultGate); err != nil {
			return nil, err
		}
		if string(expectedRaw) != "null" {
			if err := json.Unmarshal(expectedRaw, &item.ExpectedOptions); err != nil {
				return nil, err
			}
		}
		if matches.Valid {
			value := matches.Bool
			item.Matches = &value
		}
		if labeledAt.Valid {
			value := labeledAt.Time.UTC()
			item.LabeledAt = &value
		}
		item.CreatedAt = item.CreatedAt.UTC()
		out = append(out, item)
	}
	return out, rows.Err()
}

func scanOMRCalibrationSession(row calibrationScanner, out *OMRCalibrationSession) error {
	var questionIDsRaw, optionLabelsRaw []byte
	var approvedAt, revokedAt, discardedAt sql.NullTime
	if err := row.Scan(&out.ID, &out.TenantID, &out.TemplateID, &out.TemplateContentHash, &out.ScopeType, &out.QuestionID, &questionIDsRaw, &out.QuestionType,
		&out.ProfileVersion, &out.ProfileHash, &out.ReferenceFileAssetID, &out.ReferenceSHA256, &optionLabelsRaw, &out.SampleSeed,
		&out.SampleCount, &out.MinimumSamples, &out.MinimumSamplesPerOption, &out.MinimumSamplesPerStratum, &out.MinimumConfidence, &out.Status, &out.CreatedBy, &out.CreatedAt,
		&out.InheritedFromSessionID,
		&out.ApprovedBy, &approvedAt, &out.ApprovalNote, &out.EvidenceHash, &out.RevokedBy, &revokedAt, &out.RevokeReason, &out.DiscardedBy, &discardedAt, &out.DiscardReason, &out.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	if err := json.Unmarshal(questionIDsRaw, &out.QuestionIDs); err != nil {
		return err
	}
	if err := json.Unmarshal(optionLabelsRaw, &out.OptionLabels); err != nil {
		return err
	}
	if approvedAt.Valid {
		value := approvedAt.Time.UTC()
		out.ApprovedAt = &value
	}
	if revokedAt.Valid {
		value := revokedAt.Time.UTC()
		out.RevokedAt = &value
	}
	if discardedAt.Valid {
		value := discardedAt.Time.UTC()
		out.DiscardedAt = &value
	}
	out.CreatedAt = out.CreatedAt.UTC()
	out.UpdatedAt = out.UpdatedAt.UTC()
	return nil
}

type calibrationScanner interface{ Scan(...any) error }

func summarizeOMRCalibration(session OMRCalibrationSession, cases []OMRCalibrationCase) OMRCalibrationSummary {
	summary := OMRCalibrationSummary{
		OptionCoverage:   map[string]int{},
		QuestionCoverage: map[string]int{},
		StratumCoverage: map[string]int{
			"selected_high": 0,
			"selected_low":  0,
			"blank":         0,
			"ambiguous":     0,
		},
		Blockers: []string{},
	}
	for _, label := range session.OptionLabels {
		summary.OptionCoverage[label] = 0
	}
	for _, questionID := range session.QuestionIDs {
		summary.QuestionCoverage[questionID] = 0
	}
	for _, item := range cases {
		addOMRCalibrationCaseToSummary(&summary, session, item)
		if _, complete := calibrationObservedResult(item); session.ScopeType == "template" && !complete && !containsCalibrationOption(summary.Blockers, "calibration_result_gate_missing") {
			summary.Blockers = append(summary.Blockers, "calibration_result_gate_missing")
		}
	}
	appendOMRCalibrationEvidenceBlockers(session, &summary)
	appendOMRCalibrationCoverageBlockers(session, &summary)
	if session.Status != "draft" {
		summary.Blockers = append(summary.Blockers, "calibration_not_draft")
	}
	summary.ReadyToApprove = len(summary.Blockers) == 0
	return summary
}

func addOMRCalibrationCaseToSummary(summary *OMRCalibrationSummary, session OMRCalibrationSession, item OMRCalibrationCase) {
	summary.TotalCount++
	summary.QuestionCoverage[item.QuestionID]++
	summary.StratumCoverage[item.SampleStratum]++
	// 分层只决定抽样覆盖；资格必须重放同一冻结置信度、复核标志与集合质量，空白和多选不能漏计。
	input, complete := calibrationObservedResult(item)
	eligible := complete && omrResultCanAutoConfirmForQuestion(input, item.QuestionType, true, session.MinimumConfidence)
	if session.ScopeType != "template" {
		eligible = item.SampleStratum == "selected_high" || item.SampleStratum == ""
	}
	if eligible {
		summary.EligibleCount++
	}
	// 未标注样本仍计入题目和分层覆盖，但不能贡献选项覆盖或一致性计数。
	if item.ExpectedOptions == nil || item.Matches == nil {
		summary.PendingCount++
		return
	}
	summary.LabeledCount++
	for _, option := range item.ExpectedOptions {
		summary.OptionCoverage[option]++
	}
	if *item.Matches {
		summary.MatchCount++
		if eligible {
			summary.EligibleMatchCount++
		}
	} else {
		summary.MismatchCount++
		if eligible {
			summary.EligibleMismatchCount++
		}
	}
}

func calibrationObservedResult(item OMRCalibrationCase) (OMRResultInput, bool) {
	review, hasReview := item.ObservedResultGate["needs_human_review"].(bool)
	rawThresholds, hasThresholds := item.ObservedResultGate["thresholds"]
	thresholds, validThresholds := rawThresholds.(map[string]any)
	// 旧单选 Worker 允许 null thresholds；缺少该键则无法证明保存过完整的门禁事实。
	return OMRResultInput{Decision: item.ObservedDecision, Selected: item.ObservedOptions, Confidence: item.ObservedConfidence,
		NeedsHumanReview: review, Thresholds: thresholds}, hasReview && hasThresholds && (rawThresholds == nil || validThresholds)
}
