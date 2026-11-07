package grading

import (
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// OMRCalibrationSession 冻结用于证明自动确认权限的确切模板、参考资源、工作器配置和确定性样本集合。
// 新会话覆盖一个不可变模板中的全部 OMR 题目；ScopeType 保持旧的题目级证据可读，不静默扩大其范围。
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

// OMRCalibrationCase 是不可变 OMR 输出快照，操作员检查受保护的裁剪图和覆盖图后进行标注；会话为草稿时，标签只能设置一次。
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
	// 校准需要重放实际自动确认门禁；此快照仅供服务端使用，不能泄露给盲标注界面。
	ObservedResultGate map[string]any `json:"-"`
	ExpectedOptions    []string       `json:"expected_options,omitempty"`
	Matches            *bool          `json:"matches,omitempty"`
	LabeledBy          string         `json:"labeled_by,omitempty"`
	LabeledAt          *time.Time     `json:"labeled_at,omitempty"`
	CreatedAt          time.Time      `json:"created_at"`
	OverlayFileAssetID string         `json:"overlay_file_asset_id,omitempty"`
	SegmentImageURL    string         `json:"segment_image_url,omitempty"`
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
	// QuestionID 仅为旧客户端保留无副作用、向后兼容的请求结构；新会话始终覆盖完整的不可变模板。
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

// OMRCalibrationStore 与基础评分 Store 分开，使旧内存评分测试保持独立；生产 PostgreSQL 负责审批及证据链。
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

// 模板、识别配置和参考图共同限定校准证据的适用范围，读取时必须匹配同一组冻结值。
type omrCalibrationScope struct {
	templateID           string
	templateContentHash  string
	profileVersion       string
	profileHash          string
	referenceFileAssetID string
	referenceSHA256      string
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
	scope := omrCalibrationScope{
		templateID:           templateID,
		templateContentHash:  contentHash,
		profileVersion:       policy.RuntimeProfile.Version,
		profileHash:          policy.ProfileHash,
		referenceFileAssetID: policy.Reference.FileAssetID,
		referenceSHA256:      policy.Reference.HashSHA256,
	}
	if existing, existingErr := getActiveOMRCalibrationSessionTx(ctx, tx, tenantID, scope); existingErr == nil {
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

// loadApprovedOMRCalibrationTx 在创建 OMR 运行时调用，早于工作器收到任何任务；不信任客户端或工作器载荷提供的会话 ID。
func (s *PostgresStore) loadApprovedOMRCalibrationTx(ctx context.Context, tx *sql.Tx, tenantID string, scope omrCalibrationScope, questionID string) (*paper.OMRCalibrationApproval, error) {
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
LIMIT 1`, tenantID, scope.templateID, scope.templateContentHash, questionID, scope.profileVersion, scope.profileHash, scope.referenceFileAssetID, scope.referenceSHA256).Scan(
		&out.ID, &out.TemplateID, &out.TemplateContentHash, &out.ScopeType, &out.QuestionID, &out.ProfileVersion, &out.ProfileHash,
		&out.ReferenceFileAssetID, &out.ReferenceSHA256, &out.MinimumConfidence, &out.EvidenceHash, &out.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	// 历史批准的哈希不重写；按当前自动接受分支复验样本，旧规则漏过的错例不能继续授权新任务。
	valid, err := omrCalibrationEvidenceAllowsAutoConfirmTx(ctx, tx, tenantID, out.ID)
	if err != nil || !valid {
		return nil, err
	}
	return &out, nil
}

func omrCalibrationEvidenceAllowsAutoConfirmTx(ctx context.Context, tx *sql.Tx, tenantID, id string) (bool, error) {
	session, err := getOMRCalibrationSession(ctx, tx, tenantID, id, false)
	if err != nil {
		return false, err
	}
	if session.Status != "approved" {
		return false, nil
	}
	cases, err := listOMRCalibrationCases(ctx, tx, tenantID, id)
	if err != nil {
		return false, err
	}
	// draft 仅用于计算证据资格，不更改数据库里的审批状态和原始证据哈希。
	session.Status = "draft"
	return summarizeOMRCalibration(session, cases).ReadyToApprove, nil
}
