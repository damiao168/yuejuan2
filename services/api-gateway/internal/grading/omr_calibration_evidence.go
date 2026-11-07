package grading

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"encoding/hex"
	"encoding/json"
	"sort"
	"strings"
)

func (s *PostgresStore) populateOMRCalibrationCasesTx(ctx context.Context, tx *sql.Tx, tenantID string, session OMRCalibrationSession, questions []calibrationQuestion) error {
	questionIDsRaw, err := json.Marshal(session.QuestionIDs)
	if err != nil {
		return ErrInvalidInput
	}
	// 先取每题块最新的同配置结果，再按固定种子分层抽样；样本写入后不随后续识别更新。
	// 优先抽取稀缺选项和结果层不等于保证覆盖，审批前仍须检查实际样本分布。
	rows, err := tx.QueryContext(ctx, `
WITH latest_by_segment AS (
  SELECT DISTINCT ON (o.answer_segment_id)
    o.id::text,o.answer_segment_id::text,seg.question_id::text,seg.question_no,
    o.crop_sha256,o.decision,o.selected_options,o.confidence,o.measurements,
    CASE WHEN task.status='succeeded' AND task.result->>'omr_run_id'=o.id::text
      AND task.result->>'result_version'=o.result_version
      THEN jsonb_build_object('needs_human_review',task.result->'needs_human_review','thresholds',task.result->'thresholds')
      ELSE '{}'::jsonb END AS observed_result_gate
  FROM omr_run o
  JOIN answer_segment seg ON seg.tenant_id=o.tenant_id AND seg.id=o.answer_segment_id AND seg.deleted_at IS NULL
  LEFT JOIN agent_worker_task task ON task.tenant_id=o.tenant_id AND task.id=o.runtime_task_id
    AND task.source_type='omr_run' AND task.source_id=o.id
  WHERE o.tenant_id=$1::uuid AND o.template_id=$2::uuid AND o.template_content_hash=$3
    AND seg.question_id::text IN (SELECT jsonb_array_elements_text($4::jsonb))
    AND o.profile_version=$5 AND o.profile_hash=$6
    AND o.reference_file_asset_id=$7::uuid AND o.reference_sha256=$8
    AND o.status='completed' AND o.decision IS NOT NULL AND o.confidence IS NOT NULL
    AND o.deleted_at IS NULL
  ORDER BY o.answer_segment_id,o.completed_at DESC NULLS LAST,o.id DESC
), classified AS (
  SELECT *,
    CASE
      WHEN decision='selected' AND jsonb_array_length(selected_options)=1 AND confidence >= $9 THEN 'selected_high'
      WHEN decision='selected' AND jsonb_array_length(selected_options)=1 THEN 'selected_low'
      WHEN decision='blank' THEN 'blank'
      ELSE 'ambiguous'
    END AS sample_stratum,
    CASE
      WHEN decision='selected' AND jsonb_array_length(selected_options)=1 THEN selected_options->>0
      WHEN decision='ambiguous' THEN COALESCE(measurements->0->>'option','')
      ELSE ''
    END AS sample_option
  FROM latest_by_segment
), ranked AS (
  SELECT *,row_number() OVER (
    PARTITION BY question_id,sample_stratum,sample_option
    ORDER BY md5(id || $10)
  ) AS bucket_rank,
  row_number() OVER (
    PARTITION BY sample_option
    ORDER BY md5(id || $10)
  ) AS option_rank,
  row_number() OVER (
    PARTITION BY sample_stratum
    ORDER BY md5(id || $10)
  ) AS stratum_rank
  FROM classified
)
SELECT id,answer_segment_id,question_id,question_no,crop_sha256,decision,
  selected_options,confidence,measurements,sample_stratum,observed_result_gate
FROM ranked
ORDER BY
  CASE
    WHEN (sample_option <> '' AND option_rank <= $11) OR stratum_rank <= $12 THEN 0
    ELSE 1
  END,
  bucket_rank,md5(id || $10)
LIMIT $13`, tenantID, session.TemplateID, session.TemplateContentHash, questionIDsRaw,
		session.ProfileVersion, session.ProfileHash, session.ReferenceFileAssetID, session.ReferenceSHA256,
		session.MinimumConfidence, session.SampleSeed, session.MinimumSamplesPerOption,
		session.MinimumSamplesPerStratum, session.MinimumSamples)
	if err != nil {
		return err
	}
	type candidate struct {
		runID, segmentID, questionID, questionNo string
		cropSHA256, decision, sampleStratum      string
		selected, measurements, resultGate       []byte
		confidence                               float64
	}
	questionByID := make(map[string]calibrationQuestion, len(questions))
	for _, question := range questions {
		questionByID[question.ID] = question
	}
	candidates := []candidate{}
	for rows.Next() {
		var item candidate
		if err := rows.Scan(&item.runID, &item.segmentID, &item.questionID, &item.questionNo, &item.cropSHA256,
			&item.decision, &item.selected, &item.confidence, &item.measurements, &item.sampleStratum, &item.resultGate); err != nil {
			_ = rows.Close()
			return err
		}
		candidates = append(candidates, item)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, item := range candidates {
		question, ok := questionByID[item.questionID]
		if !ok {
			return ErrInvalidInput
		}
		optionLabelsRaw, marshalErr := json.Marshal(question.OptionLabels)
		if marshalErr != nil {
			return ErrInvalidInput
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO omr_calibration_case (
  tenant_id,calibration_session_id,omr_run_id,answer_segment_id,question_id,question_no,
  question_type,option_labels,sample_stratum,crop_sha256,observed_decision,
  observed_options,observed_confidence,measurements,observed_result_gate
)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,$7,$8::jsonb,$9,$10,$11,$12::jsonb,$13,$14::jsonb,$15::jsonb)`,
			tenantID, session.ID, item.runID, item.segmentID, item.questionID, item.questionNo,
			question.Type, optionLabelsRaw, item.sampleStratum, item.cropSHA256, item.decision,
			item.selected, item.confidence, item.measurements, item.resultGate); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE omr_calibration_session SET sample_count=$3,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, session.ID, len(candidates))
	return err
}

func appendOMRCalibrationEvidenceBlockers(session OMRCalibrationSession, summary *OMRCalibrationSummary) {
	if summary.TotalCount < session.MinimumSamples {
		summary.Blockers = append(summary.Blockers, "sample_count_below_minimum")
	}
	if summary.PendingCount > 0 {
		summary.Blockers = append(summary.Blockers, "calibration_labels_pending")
	}
	if summary.EligibleMismatchCount > 0 {
		blocker := "eligible_calibration_mismatch_detected"
		if session.ScopeType != "template" {
			blocker = "calibration_mismatch_detected"
		}
		summary.Blockers = append(summary.Blockers, blocker)
	}
}

func appendOMRCalibrationCoverageBlockers(session OMRCalibrationSession, summary *OMRCalibrationSummary) {
	for _, label := range session.OptionLabels {
		if summary.OptionCoverage[label] < session.MinimumSamplesPerOption {
			summary.Blockers = append(summary.Blockers, "option_coverage_incomplete")
			break
		}
	}
	for _, questionID := range session.QuestionIDs {
		if summary.QuestionCoverage[questionID] == 0 {
			summary.Blockers = append(summary.Blockers, "question_coverage_incomplete")
			break
		}
	}
	if session.ScopeType != "template" {
		return
	}
	for _, stratum := range []string{"selected_high", "selected_low", "blank", "ambiguous"} {
		if summary.StratumCoverage[stratum] < session.MinimumSamplesPerStratum {
			summary.Blockers = append(summary.Blockers, "stratum_coverage_incomplete")
			break
		}
	}
}

type calibrationQuestion struct {
	ID           string
	No           string
	Type         string
	OptionLabels []string
}

func calibrationQuestionsForTemplateTx(ctx context.Context, tx *sql.Tx, tenantID, examID string, layout paper.TemplateLayout) ([]calibrationQuestion, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT id::text,question_no,question_type
FROM question
WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND deleted_at IS NULL
`, tenantID, examID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	metadata := map[string]calibrationQuestion{}
	for rows.Next() {
		var item calibrationQuestion
		if err := rows.Scan(&item.ID, &item.No, &item.Type); err != nil {
			return nil, err
		}
		metadata[item.ID] = item
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return calibrationQuestionsForLayout(layout, metadata)
}

func calibrationQuestionsForLayout(layout paper.TemplateLayout, metadata map[string]calibrationQuestion) ([]calibrationQuestion, error) {
	out := []calibrationQuestion{}
	seenQuestions := map[string]bool{}
	// 只按模板实际题区建立校准题集，并排除没有 OMR 自动路径的题型，避免校准范围悄悄扩大。
	for _, page := range layout.Pages {
		for _, region := range page.QuestionRegions {
			item, ok := metadata[region.QuestionID]
			if !ok || seenQuestions[item.ID] {
				continue
			}
			if item.Type != "single_choice" && item.Type != "multiple_choice" && item.Type != "true_false" {
				continue
			}
			labels, err := calibrationOptionLabels(region.OptionRegions)
			if err != nil {
				return nil, err
			}
			item.OptionLabels = labels
			seenQuestions[item.ID] = true
			out = append(out, item)
		}
	}
	return out, nil
}

func calibrationOptionLabels(options []paper.OptionRegion) ([]string, error) {
	labels := []string{}
	seenLabels := map[string]bool{}
	for _, option := range options {
		label := strings.TrimSpace(option.Label)
		if label == "" || seenLabels[label] {
			return nil, ErrInvalidInput
		}
		seenLabels[label] = true
		labels = append(labels, label)
	}
	if len(labels) < 2 {
		return nil, ErrInvalidInput
	}
	return labels, nil
}

func containsCalibrationOption(options []string, expected string) bool {
	for _, option := range options {
		if option == expected {
			return true
		}
	}
	return false
}

func normalizeCalibrationOptions(allowed, expected []string) ([]string, bool) {
	selected := map[string]bool{}
	for _, value := range expected {
		value = strings.TrimSpace(value)
		if value == "" || selected[value] || !containsCalibrationOption(allowed, value) {
			return nil, false
		}
		selected[value] = true
	}
	normalized := make([]string, 0, len(selected))
	for _, option := range allowed {
		if selected[option] {
			normalized = append(normalized, option)
		}
	}
	return normalized, true
}

func calibrationOptionsMatch(decision string, observed, expected []string) bool {
	if len(expected) == 0 {
		return decision == "blank" && len(observed) == 0
	}
	if len(observed) != len(expected) {
		return false
	}
	expectedSet := make(map[string]bool, len(expected))
	for _, option := range expected {
		expectedSet[option] = true
	}
	for _, option := range observed {
		if !expectedSet[option] {
			return false
		}
	}
	return decision == "selected" || decision == "multiple"
}

// 审批证据包含冻结配置和样本标签；按样本 ID 排序，避免查询返回顺序改变证据哈希。
func omrCalibrationEvidenceHash(session OMRCalibrationSession, cases []OMRCalibrationCase) (string, error) {
	sorted := append([]OMRCalibrationCase(nil), cases...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	payload := struct {
		Session struct {
			TemplateID               string   `json:"template_id"`
			TemplateContentHash      string   `json:"template_content_hash"`
			ScopeType                string   `json:"scope_type"`
			QuestionID               string   `json:"question_id"`
			QuestionIDs              []string `json:"question_ids"`
			QuestionType             string   `json:"question_type"`
			ProfileVersion           string   `json:"profile_version"`
			ProfileHash              string   `json:"profile_hash"`
			ReferenceFileAssetID     string   `json:"reference_file_asset_id"`
			ReferenceSHA256          string   `json:"reference_sha256"`
			OptionLabels             []string `json:"option_labels"`
			SampleSeed               string   `json:"sample_seed"`
			MinimumSamples           int      `json:"minimum_samples"`
			MinimumSamplesPerOption  int      `json:"minimum_samples_per_option"`
			MinimumSamplesPerStratum int      `json:"minimum_samples_per_stratum"`
			MinimumConfidence        float64  `json:"minimum_confidence"`
		} `json:"session"`
		Cases []struct {
			OMRCalibrationCase
			ObservedResultGate map[string]any `json:"observed_result_gate"`
		} `json:"cases"`
	}{}
	payload.Session.TemplateID = session.TemplateID
	payload.Session.TemplateContentHash = session.TemplateContentHash
	payload.Session.ScopeType = session.ScopeType
	payload.Session.QuestionID = session.QuestionID
	payload.Session.QuestionIDs = session.QuestionIDs
	payload.Session.QuestionType = session.QuestionType
	payload.Session.ProfileVersion = session.ProfileVersion
	payload.Session.ProfileHash = session.ProfileHash
	payload.Session.ReferenceFileAssetID = session.ReferenceFileAssetID
	payload.Session.ReferenceSHA256 = session.ReferenceSHA256
	payload.Session.OptionLabels = session.OptionLabels
	payload.Session.SampleSeed = session.SampleSeed
	payload.Session.MinimumSamples = session.MinimumSamples
	payload.Session.MinimumSamplesPerOption = session.MinimumSamplesPerOption
	payload.Session.MinimumSamplesPerStratum = session.MinimumSamplesPerStratum
	payload.Session.MinimumConfidence = session.MinimumConfidence
	for _, item := range sorted {
		payload.Cases = append(payload.Cases, struct {
			OMRCalibrationCase
			ObservedResultGate map[string]any `json:"observed_result_gate"`
		}{item, item.ObservedResultGate})
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
