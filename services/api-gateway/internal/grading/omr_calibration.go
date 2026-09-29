package grading

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/paper"
)

// OMRCalibrationSession freezes the exact template, reference asset, worker
// profile, and deterministic sample population used to justify an
// automatic-confirmation permission. New sessions cover every OMR question in
// one immutable template; ScopeType keeps legacy question-scoped evidence
// readable without silently widening it.
type OMRCalibrationSession struct {
	ID                       string                `json:"id"`
	TenantID                 string                `json:"tenant_id"`
	TemplateID               string                `json:"template_id"`
	TemplateContentHash      string                `json:"template_content_hash"`
	ScopeType                string                `json:"scope_type"`
	QuestionID               string                `json:"question_id,omitempty"`
	QuestionIDs              []string              `json:"question_ids"`
	QuestionType             string                `json:"question_type"`
	ProfileVersion           string                `json:"profile_version"`
	ProfileHash              string                `json:"profile_hash"`
	ReferenceFileAssetID     string                `json:"reference_file_asset_id"`
	ReferenceSHA256          string                `json:"reference_sha256"`
	OptionLabels             []string              `json:"option_labels"`
	SampleSeed               string                `json:"sample_seed"`
	SampleCount              int                   `json:"sample_count"`
	MinimumSamples           int                   `json:"minimum_samples"`
	MinimumSamplesPerOption  int                   `json:"minimum_samples_per_option"`
	MinimumSamplesPerStratum int                   `json:"minimum_samples_per_stratum"`
	MinimumConfidence        float64               `json:"minimum_confidence"`
	InheritedFromSessionID   string                `json:"inherited_from_session_id,omitempty"`
	Status                   string                `json:"status"`
	CreatedBy                string                `json:"created_by"`
	CreatedAt                time.Time             `json:"created_at"`
	ApprovedBy               string                `json:"approved_by,omitempty"`
	ApprovedAt               *time.Time            `json:"approved_at,omitempty"`
	ApprovalNote             string                `json:"approval_note,omitempty"`
	EvidenceHash             string                `json:"evidence_hash,omitempty"`
	RevokedBy                string                `json:"revoked_by,omitempty"`
	RevokedAt                *time.Time            `json:"revoked_at,omitempty"`
	RevokeReason             string                `json:"revoke_reason,omitempty"`
	DiscardedBy              string                `json:"discarded_by,omitempty"`
	DiscardedAt              *time.Time            `json:"discarded_at,omitempty"`
	DiscardReason            string                `json:"discard_reason,omitempty"`
	UpdatedAt                time.Time             `json:"updated_at"`
	Summary                  OMRCalibrationSummary `json:"summary"`
}

// OMRCalibrationCase is an immutable OMR output snapshot that an operator
// labels after inspecting the protected crop and overlay. A label can be set
// exactly once while the session is a draft.
type OMRCalibrationCase struct {
	ID                 string           `json:"id"`
	CalibrationID      string           `json:"calibration_id"`
	OMRRunID           string           `json:"omr_run_id"`
	AnswerSegmentID    string           `json:"answer_segment_id"`
	QuestionID         string           `json:"question_id"`
	QuestionNo         string           `json:"question_no"`
	QuestionType       string           `json:"question_type"`
	OptionLabels       []string         `json:"option_labels"`
	SampleStratum      string           `json:"sample_stratum"`
	CropSHA256         string           `json:"crop_sha256"`
	ObservedDecision   string           `json:"observed_decision"`
	ObservedOptions    []string         `json:"observed_options"`
	ObservedConfidence float64          `json:"observed_confidence"`
	Measurements       []map[string]any `json:"measurements"`
	ExpectedOptions    []string         `json:"expected_options,omitempty"`
	Matches            *bool            `json:"matches,omitempty"`
	LabeledBy          string           `json:"labeled_by,omitempty"`
	LabeledAt          *time.Time       `json:"labeled_at,omitempty"`
	CreatedAt          time.Time        `json:"created_at"`
	OverlayFileAssetID string           `json:"overlay_file_asset_id,omitempty"`
	SegmentImageURL    string           `json:"segment_image_url,omitempty"`
}

type OMRCalibrationSummary struct {
	TotalCount            int            `json:"total_count"`
	LabeledCount          int            `json:"labeled_count"`
	PendingCount          int            `json:"pending_count"`
	MatchCount            int            `json:"match_count"`
	MismatchCount         int            `json:"mismatch_count"`
	EligibleCount         int            `json:"eligible_count"`
	EligibleMatchCount    int            `json:"eligible_match_count"`
	EligibleMismatchCount int            `json:"eligible_mismatch_count"`
	OptionCoverage        map[string]int `json:"option_coverage"`
	QuestionCoverage      map[string]int `json:"question_coverage"`
	StratumCoverage       map[string]int `json:"stratum_coverage"`
	ReadyToApprove        bool           `json:"ready_to_approve"`
	Blockers              []string       `json:"blockers"`
}

type OMRCalibrationDetail struct {
	Session OMRCalibrationSession `json:"session"`
	Cases   []OMRCalibrationCase  `json:"cases"`
}

type CreateOMRCalibrationInput struct {
	// QuestionID is retained only so older clients receive a harmless,
	// backwards-compatible request shape. New sessions always cover the
	// complete immutable template.
	QuestionID string `json:"question_id,omitempty"`
}

type LabelOMRCalibrationCaseInput struct {
	ExpectedOption  string   `json:"expected_option,omitempty"`
	ExpectedOptions []string `json:"expected_options"`
}

type ApproveOMRCalibrationInput struct {
	ApprovalNote string `json:"approval_note"`
}

type RevokeOMRCalibrationInput struct {
	Reason string `json:"reason"`
}

type DiscardOMRCalibrationInput struct {
	Reason string `json:"reason"`
}

// OMRCalibrationStore is separate from the base grading Store. This keeps
// legacy in-memory grading tests independent while production PostgreSQL owns
// the approval and evidence chain.
type OMRCalibrationStore interface {
	CreateOMRCalibration(context.Context, string, string, string, CreateOMRCalibrationInput) (OMRCalibrationDetail, error)
	ListOMRCalibrations(context.Context, string, string) ([]OMRCalibrationSession, error)
	GetOMRCalibration(context.Context, string, string) (OMRCalibrationDetail, error)
	LabelOMRCalibrationCase(context.Context, string, string, string, string, LabelOMRCalibrationCaseInput) (OMRCalibrationDetail, error)
	ApproveOMRCalibration(context.Context, string, string, string, ApproveOMRCalibrationInput) (OMRCalibrationDetail, error)
	RevokeOMRCalibration(context.Context, string, string, string, RevokeOMRCalibrationInput) (OMRCalibrationDetail, error)
	DiscardOMRCalibration(context.Context, string, string, string, DiscardOMRCalibrationInput) (OMRCalibrationDetail, error)
}

type calibrationQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

const omrCalibrationSessionColumns = `
id::text,tenant_id::text,template_id::text,template_content_hash,scope_type,COALESCE(question_id::text,''),question_ids,question_type,
profile_version,profile_hash,reference_file_asset_id::text,reference_sha256,option_labels,sample_seed::text,
sample_count,minimum_samples,minimum_samples_per_option,minimum_samples_per_stratum,minimum_confidence,status,created_by::text,created_at,
COALESCE(inherited_from_session_id::text,''),
COALESCE(approved_by::text,''),approved_at,approval_note,COALESCE(evidence_hash,''),COALESCE(revoked_by::text,''),
revoked_at,revoke_reason,COALESCE(discarded_by::text,''),discarded_at,discard_reason,updated_at`

func (s *PostgresStore) CreateOMRCalibration(ctx context.Context, tenantID, templateID, actorID string, input CreateOMRCalibrationInput) (OMRCalibrationDetail, error) {
	templateID = strings.TrimSpace(templateID)
	if templateID == "" || strings.TrimSpace(actorID) == "" {
		return OMRCalibrationDetail{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OMRCalibrationDetail{}, err
	}
	defer tx.Rollback()

	var examID, contentHash, templateStatus, referenceAssetID, referenceSHA256, referenceContentType string
	var layoutRaw []byte
	err = tx.QueryRowContext(ctx, `
SELECT t.exam_id::text,t.content_hash,t.status,t.layout,fa.id::text,fa.hash_sha256,fa.content_type
FROM answer_sheet_template t
JOIN exam_paper ep ON ep.tenant_id=t.tenant_id AND ep.id=t.exam_paper_id AND ep.deleted_at IS NULL
JOIN file_asset fa ON fa.tenant_id=ep.tenant_id AND fa.id=ep.file_asset_id AND fa.deleted_at IS NULL
WHERE t.tenant_id=$1::uuid AND t.id=$2::uuid AND t.deleted_at IS NULL
FOR UPDATE OF t
`, tenantID, templateID).Scan(&examID, &contentHash, &templateStatus, &layoutRaw, &referenceAssetID, &referenceSHA256, &referenceContentType)
	if errors.Is(err, sql.ErrNoRows) {
		return OMRCalibrationDetail{}, ErrNotFound
	}
	if err != nil {
		return OMRCalibrationDetail{}, err
	}
	var layout paper.TemplateLayout
	if err := json.Unmarshal(layoutRaw, &layout); err != nil {
		return OMRCalibrationDetail{}, ErrInvalidInput
	}
	questions, err := calibrationQuestionsForTemplateTx(ctx, tx, tenantID, examID, layout)
	if err != nil {
		return OMRCalibrationDetail{}, err
	}
	if len(questions) == 0 {
		return OMRCalibrationDetail{}, ErrInvalidInput
	}
	reference := paper.TemplateOMRReference{Source: paper.OMRReferenceSourceExamPaper, FileAssetID: referenceAssetID, HashSHA256: referenceSHA256, ContentType: referenceContentType}
	policy := paper.OMRAutoConfirmPolicyForTemplateReference(layout, templateStatus, contentHash, contentHash, reference, questions[0].ID)
	if policy.RuntimeProfile.Mode != paper.OMRProfileModeTemplateDifference || policy.Reference == nil || policy.ProfileHash == "" {
		return OMRCalibrationDetail{}, ErrInvalidInput
	}
	questionIDs := make([]string, 0, len(questions))
	optionLabels := []string{}
	seenLabels := map[string]bool{}
	for _, question := range questions {
		questionIDs = append(questionIDs, question.ID)
		for _, label := range question.OptionLabels {
			if !seenLabels[label] {
				seenLabels[label] = true
				optionLabels = append(optionLabels, label)
			}
		}
	}
	// 同一模板内容、识别配置和参考图共用校准范围锁，避免并发创建两份有效会话。
	scopeKey := strings.Join([]string{tenantID, templateID, contentHash, "template", policy.ProfileHash, policy.Reference.HashSHA256}, "|")
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, scopeKey); err != nil {
		return OMRCalibrationDetail{}, err
	}
	if existing, existingErr := getActiveOMRCalibrationSessionTx(ctx, tx, tenantID, templateID, contentHash, policy.RuntimeProfile.Version, policy.ProfileHash, policy.Reference.FileAssetID, policy.Reference.HashSHA256); existingErr == nil {
		if err = tx.Commit(); err != nil {
			return OMRCalibrationDetail{}, err
		}
		return s.GetOMRCalibration(ctx, tenantID, existing.ID)
	} else if !errors.Is(existingErr, ErrNotFound) {
		return OMRCalibrationDetail{}, existingErr
	}

	labelsRaw, err := json.Marshal(optionLabels)
	if err != nil {
		return OMRCalibrationDetail{}, ErrInvalidInput
	}
	questionIDsRaw, err := json.Marshal(questionIDs)
	if err != nil {
		return OMRCalibrationDetail{}, ErrInvalidInput
	}
	var session OMRCalibrationSession
	err = scanOMRCalibrationSession(tx.QueryRowContext(ctx, `
INSERT INTO omr_calibration_session (
  tenant_id,template_id,template_content_hash,scope_type,question_id,question_ids,question_type,
  profile_version,profile_hash,reference_file_asset_id,reference_sha256,option_labels,
  minimum_samples,minimum_samples_per_option,minimum_samples_per_stratum,minimum_confidence,status,created_by
)
VALUES ($1::uuid,$2::uuid,$3,'template',NULL,$4::jsonb,'template',$5,$6,$7::uuid,$8,$9::jsonb,$10,$11,$12,$13,'draft',$14::uuid)
RETURNING `+omrCalibrationSessionColumns, tenantID, templateID, contentHash, questionIDsRaw,
		policy.RuntimeProfile.Version, policy.ProfileHash, policy.Reference.FileAssetID, policy.Reference.HashSHA256, labelsRaw,
		paper.OMRCalibrationMinimumSampleCount, paper.OMRCalibrationMinimumSamplesPerOption,
		paper.OMRCalibrationMinimumSamplesPerStratum, paper.OMRCalibrationMinimumConfidence, actorID), &session)
	if err != nil {
		return OMRCalibrationDetail{}, err
	}
	if err = s.populateOMRCalibrationCasesTx(ctx, tx, tenantID, session, questions); err != nil {
		return OMRCalibrationDetail{}, err
	}
	if err = tx.Commit(); err != nil {
		return OMRCalibrationDetail{}, err
	}
	return s.GetOMRCalibration(ctx, tenantID, session.ID)
}

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

func (s *PostgresStore) LabelOMRCalibrationCase(ctx context.Context, tenantID, calibrationID, caseID, actorID string, input LabelOMRCalibrationCaseInput) (OMRCalibrationDetail, error) {
	calibrationID, caseID, actorID = strings.TrimSpace(calibrationID), strings.TrimSpace(caseID), strings.TrimSpace(actorID)
	input.ExpectedOption = strings.TrimSpace(input.ExpectedOption)
	if calibrationID == "" || caseID == "" || actorID == "" {
		return OMRCalibrationDetail{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OMRCalibrationDetail{}, err
	}
	defer tx.Rollback()
	session, err := getOMRCalibrationSession(ctx, tx, tenantID, calibrationID, true)
	if err != nil {
		return OMRCalibrationDetail{}, err
	}
	if session.Status != "draft" {
		return OMRCalibrationDetail{}, ErrInvalidTransition
	}
	item, err := getOMRCalibrationCaseTx(ctx, tx, tenantID, calibrationID, caseID)
	if err != nil {
		return OMRCalibrationDetail{}, err
	}
	if item.ExpectedOptions != nil || item.Matches != nil {
		return OMRCalibrationDetail{}, ErrInvalidTransition
	}
	expected := input.ExpectedOptions
	if input.ExpectedOption != "" {
		if expected != nil {
			return OMRCalibrationDetail{}, ErrInvalidInput
		}
		expected = []string{input.ExpectedOption}
	}
	// nil 表示没有提交标签；空数组则是人工确认空白，二者不能合并处理。
	if expected == nil {
		return OMRCalibrationDetail{}, ErrInvalidInput
	}
	expected, ok := normalizeCalibrationOptions(item.OptionLabels, expected)
	if !ok || (item.QuestionType != "multiple_choice" && len(expected) > 1) {
		return OMRCalibrationDetail{}, ErrInvalidInput
	}
	expectedRaw, err := json.Marshal(expected)
	if err != nil {
		return OMRCalibrationDetail{}, ErrInvalidInput
	}
	matches := calibrationOptionsMatch(item.ObservedDecision, item.ObservedOptions, expected)
	result, err := tx.ExecContext(ctx, `
UPDATE omr_calibration_case
SET expected_options=$4::jsonb,matches=$5,labeled_by=$6::uuid,labeled_at=now()
WHERE tenant_id=$1::uuid AND calibration_session_id=$2::uuid AND id=$3::uuid AND expected_options IS NULL`, tenantID, calibrationID, caseID, expectedRaw, matches, actorID)
	if err != nil {
		return OMRCalibrationDetail{}, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return OMRCalibrationDetail{}, ErrInvalidTransition
	}
	if err = tx.Commit(); err != nil {
		return OMRCalibrationDetail{}, err
	}
	return s.GetOMRCalibration(ctx, tenantID, calibrationID)
}

func (s *PostgresStore) ApproveOMRCalibration(ctx context.Context, tenantID, calibrationID, actorID string, input ApproveOMRCalibrationInput) (OMRCalibrationDetail, error) {
	calibrationID, actorID = strings.TrimSpace(calibrationID), strings.TrimSpace(actorID)
	input.ApprovalNote = strings.TrimSpace(input.ApprovalNote)
	if calibrationID == "" || actorID == "" || len([]rune(input.ApprovalNote)) < 10 {
		return OMRCalibrationDetail{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OMRCalibrationDetail{}, err
	}
	defer tx.Rollback()
	session, err := getOMRCalibrationSession(ctx, tx, tenantID, calibrationID, true)
	if err != nil {
		return OMRCalibrationDetail{}, err
	}
	if session.Status != "draft" {
		return OMRCalibrationDetail{}, ErrInvalidTransition
	}
	// 审批人与创建人、任一样本标注人分离；样本标签和审批都先锁住同一会话行。
	if session.CreatedBy == actorID {
		return OMRCalibrationDetail{}, ErrForbidden
	}
	cases, err := listOMRCalibrationCases(ctx, tx, tenantID, calibrationID)
	if err != nil {
		return OMRCalibrationDetail{}, err
	}
	for _, item := range cases {
		if item.LabeledBy == actorID {
			return OMRCalibrationDetail{}, ErrForbidden
		}
	}
	summary := summarizeOMRCalibration(session, cases)
	if !summary.ReadyToApprove {
		return OMRCalibrationDetail{}, ErrInvalidTransition
	}
	evidenceHash, err := omrCalibrationEvidenceHash(session, cases)
	if err != nil {
		return OMRCalibrationDetail{}, ErrInvalidInput
	}
	result, err := tx.ExecContext(ctx, `
UPDATE omr_calibration_session
SET status='approved',approved_by=$3::uuid,approved_at=now(),approval_note=$4,evidence_hash=$5,updated_at=now()
WHERE tenant_id=$1::uuid AND id=$2::uuid AND status='draft' AND deleted_at IS NULL`, tenantID, calibrationID, actorID, input.ApprovalNote, evidenceHash)
	if err != nil {
		return OMRCalibrationDetail{}, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return OMRCalibrationDetail{}, ErrInvalidTransition
	}
	if err = tx.Commit(); err != nil {
		return OMRCalibrationDetail{}, err
	}
	return s.GetOMRCalibration(ctx, tenantID, calibrationID)
}

func (s *PostgresStore) RevokeOMRCalibration(ctx context.Context, tenantID, calibrationID, actorID string, input RevokeOMRCalibrationInput) (OMRCalibrationDetail, error) {
	calibrationID, actorID = strings.TrimSpace(calibrationID), strings.TrimSpace(actorID)
	input.Reason = strings.TrimSpace(input.Reason)
	if calibrationID == "" || actorID == "" || len([]rune(input.Reason)) < 10 {
		return OMRCalibrationDetail{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OMRCalibrationDetail{}, err
	}
	defer tx.Rollback()
	session, err := getOMRCalibrationSession(ctx, tx, tenantID, calibrationID, true)
	if err != nil {
		return OMRCalibrationDetail{}, err
	}
	if session.Status != "approved" {
		return OMRCalibrationDetail{}, ErrInvalidTransition
	}
	result, err := tx.ExecContext(ctx, `
UPDATE omr_calibration_session
SET status='revoked',revoked_by=$3::uuid,revoked_at=now(),revoke_reason=$4,updated_at=now()
WHERE tenant_id=$1::uuid AND id=$2::uuid AND status='approved' AND deleted_at IS NULL`, tenantID, calibrationID, actorID, input.Reason)
	if err != nil {
		return OMRCalibrationDetail{}, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return OMRCalibrationDetail{}, ErrInvalidTransition
	}
	if err = tx.Commit(); err != nil {
		return OMRCalibrationDetail{}, err
	}
	return s.GetOMRCalibration(ctx, tenantID, calibrationID)
}

func (s *PostgresStore) DiscardOMRCalibration(ctx context.Context, tenantID, calibrationID, actorID string, input DiscardOMRCalibrationInput) (OMRCalibrationDetail, error) {
	calibrationID, actorID = strings.TrimSpace(calibrationID), strings.TrimSpace(actorID)
	input.Reason = strings.TrimSpace(input.Reason)
	if calibrationID == "" || actorID == "" || len([]rune(input.Reason)) < 10 {
		return OMRCalibrationDetail{}, ErrInvalidInput
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return OMRCalibrationDetail{}, err
	}
	defer tx.Rollback()
	session, err := getOMRCalibrationSession(ctx, tx, tenantID, calibrationID, true)
	if err != nil {
		return OMRCalibrationDetail{}, err
	}
	if session.Status != "draft" {
		return OMRCalibrationDetail{}, ErrInvalidTransition
	}
	result, err := tx.ExecContext(ctx, `
UPDATE omr_calibration_session
SET status='discarded',discarded_by=$3::uuid,discarded_at=now(),discard_reason=$4,updated_at=now()
WHERE tenant_id=$1::uuid AND id=$2::uuid AND status='draft' AND deleted_at IS NULL`, tenantID, calibrationID, actorID, input.Reason)
	if err != nil {
		return OMRCalibrationDetail{}, err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return OMRCalibrationDetail{}, ErrInvalidTransition
	}
	if err = tx.Commit(); err != nil {
		return OMRCalibrationDetail{}, err
	}
	return s.GetOMRCalibration(ctx, tenantID, calibrationID)
}

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
    o.crop_sha256,o.decision,o.selected_options,o.confidence,o.measurements
  FROM omr_run o
  JOIN answer_segment seg ON seg.tenant_id=o.tenant_id AND seg.id=o.answer_segment_id AND seg.deleted_at IS NULL
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
  selected_options,confidence,measurements,sample_stratum
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
		selected, measurements                   []byte
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
			&item.decision, &item.selected, &item.confidence, &item.measurements, &item.sampleStratum); err != nil {
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
  observed_options,observed_confidence,measurements
)
VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5::uuid,$6,$7,$8::jsonb,$9,$10,$11,$12::jsonb,$13,$14::jsonb)`,
			tenantID, session.ID, item.runID, item.segmentID, item.questionID, item.questionNo,
			question.Type, optionLabelsRaw, item.sampleStratum, item.cropSHA256, item.decision,
			item.selected, item.confidence, item.measurements); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `UPDATE omr_calibration_session SET sample_count=$3,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid`, tenantID, session.ID, len(candidates))
	return err
}

func getActiveOMRCalibrationSessionTx(ctx context.Context, tx *sql.Tx, tenantID, templateID, templateHash, profileVersion, profileHash, referenceAssetID, referenceSHA256 string) (OMRCalibrationSession, error) {
	row := tx.QueryRowContext(ctx, `SELECT `+omrCalibrationSessionColumns+`
FROM omr_calibration_session
WHERE tenant_id=$1::uuid AND template_id=$2::uuid AND template_content_hash=$3 AND scope_type='template'
  AND profile_version=$4 AND profile_hash=$5 AND reference_file_asset_id=$6::uuid AND reference_sha256=$7
  AND status IN ('draft','approved') AND deleted_at IS NULL
ORDER BY CASE status WHEN 'approved' THEN 0 ELSE 1 END,created_at DESC
LIMIT 1
FOR UPDATE`, tenantID, templateID, templateHash, profileVersion, profileHash, referenceAssetID, referenceSHA256)
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
  COALESCE(o.overlay_file_asset_id::text,'')
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
		var optionLabelsRaw, observedRaw, measurementsRaw, expectedRaw []byte
		var matches sql.NullBool
		var labeledAt sql.NullTime
		if err := rows.Scan(&item.ID, &item.CalibrationID, &item.OMRRunID, &item.AnswerSegmentID, &item.CropSHA256,
			&item.QuestionID, &item.QuestionNo, &item.QuestionType, &optionLabelsRaw, &item.SampleStratum,
			&item.ObservedDecision, &observedRaw, &item.ObservedConfidence, &measurementsRaw,
			&expectedRaw, &matches, &item.LabeledBy, &labeledAt, &item.CreatedAt, &item.OverlayFileAssetID); err != nil {
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
		summary.TotalCount++
		summary.QuestionCoverage[item.QuestionID]++
		summary.StratumCoverage[item.SampleStratum]++
		// 模板校准只要求可能自动确认的高置信单个选项结果零错；其他层仍须标注并满足覆盖要求。
		eligible := item.SampleStratum == "selected_high" || (session.ScopeType != "template" && item.SampleStratum == "")
		if eligible {
			summary.EligibleCount++
		}
		if item.ExpectedOptions == nil || item.Matches == nil {
			summary.PendingCount++
			continue
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
	if session.ScopeType == "template" {
		for _, stratum := range []string{"selected_high", "selected_low", "blank", "ambiguous"} {
			if summary.StratumCoverage[stratum] < session.MinimumSamplesPerStratum {
				summary.Blockers = append(summary.Blockers, "stratum_coverage_incomplete")
				break
			}
		}
	}
	if session.Status != "draft" {
		summary.Blockers = append(summary.Blockers, "calibration_not_draft")
	}
	summary.ReadyToApprove = len(summary.Blockers) == 0
	return summary
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
			seenLabels := map[string]bool{}
			for _, option := range region.OptionRegions {
				label := strings.TrimSpace(option.Label)
				if label == "" || seenLabels[label] {
					return nil, ErrInvalidInput
				}
				seenLabels[label] = true
				item.OptionLabels = append(item.OptionLabels, label)
			}
			if len(item.OptionLabels) < 2 {
				return nil, ErrInvalidInput
			}
			seenQuestions[item.ID] = true
			out = append(out, item)
		}
	}
	return out, nil
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
		Cases []OMRCalibrationCase `json:"cases"`
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
	payload.Cases = sorted
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// loadApprovedOMRCalibrationTx is deliberately called while creating the OMR
// run, before the worker receives any task. It never trusts a session id from a
// client or worker payload.
func (s *PostgresStore) loadApprovedOMRCalibrationTx(ctx context.Context, tx *sql.Tx, tenantID, templateID, templateContentHash, questionID, profileVersion, profileHash, referenceFileAssetID, referenceSHA256 string) (*paper.OMRCalibrationApproval, error) {
	var out paper.OMRCalibrationApproval
	err := tx.QueryRowContext(ctx, `
SELECT id::text,template_id::text,template_content_hash,scope_type,COALESCE(question_id::text,''),profile_version,profile_hash,
  reference_file_asset_id::text,reference_sha256,minimum_confidence,evidence_hash,status
FROM omr_calibration_session
WHERE tenant_id=$1::uuid AND template_id=$2::uuid AND template_content_hash=$3
  AND (scope_type='template' OR (scope_type='question' AND question_id=$4::uuid))
  AND profile_version=$5 AND profile_hash=$6 AND reference_file_asset_id=$7::uuid AND reference_sha256=$8
  AND status='approved' AND deleted_at IS NULL
ORDER BY CASE scope_type WHEN 'template' THEN 0 ELSE 1 END,approved_at DESC
LIMIT 1`, tenantID, templateID, templateContentHash, questionID, profileVersion, profileHash, referenceFileAssetID, referenceSHA256).Scan(
		&out.ID, &out.TemplateID, &out.TemplateContentHash, &out.ScopeType, &out.QuestionID, &out.ProfileVersion, &out.ProfileHash,
		&out.ReferenceFileAssetID, &out.ReferenceSHA256, &out.MinimumConfidence, &out.EvidenceHash, &out.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &out, nil
}
