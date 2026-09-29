package grading

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"
)

var ErrRevisionConflict = errors.New("grading revision conflict")
var ErrStaleScoringRule = errors.New("scoring rule conflicts with current question")

type ScoringRule struct {
	ID          string         `json:"id"`
	TenantID    string         `json:"tenant_id"`
	ExamID      string         `json:"exam_id"`
	QuestionID  string         `json:"question_id"`
	Version     int            `json:"version"`
	RuleType    string         `json:"rule_type"`
	Config      map[string]any `json:"config"`
	Status      string         `json:"status"`
	Revision    int            `json:"revision"`
	ContentHash string         `json:"content_hash"`
	CreatedBy   string         `json:"created_by"`
	PublishedBy string         `json:"published_by,omitempty"`
	PublishedAt *time.Time     `json:"published_at,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

type CreateScoringRuleInput struct {
	RuleType string         `json:"rule_type"`
	Config   map[string]any `json:"config"`
}

type UpdateScoringRuleInput struct {
	Config           map[string]any `json:"config"`
	ExpectedRevision int            `json:"expected_revision"`
}

type RuleStore interface {
	CreateScoringRule(context.Context, string, string, string, CreateScoringRuleInput) (ScoringRule, error)
	ListScoringRules(context.Context, string, string) ([]ScoringRule, error)
	UpdateScoringRule(context.Context, string, string, UpdateScoringRuleInput) (ScoringRule, error)
	PublishScoringRule(context.Context, string, string, string) (ScoringRule, error)
}

func (s *PostgresStore) CreateScoringRule(ctx context.Context, tenantID, questionID, actorID string, input CreateScoringRuleInput) (ScoringRule, error) {
	input.RuleType = strings.TrimSpace(input.RuleType)
	if input.Config == nil {
		input.Config = map[string]any{}
	}
	if err := validateRuleConfig(input.RuleType, input.Config); err != nil {
		return ScoringRule{}, err
	}
	config, hash, err := marshalRule(input.RuleType, input.Config)
	if err != nil {
		return ScoringRule{}, ErrInvalidInput
	}
	row := s.db.QueryRowContext(ctx, `
WITH target AS (
  SELECT q.exam_id, q.question_type
  FROM question q
  WHERE q.tenant_id = $1::uuid AND q.id = $2::uuid AND q.deleted_at IS NULL
), next_version AS (
  SELECT COALESCE(MAX(version), 0) + 1 AS version
  FROM scoring_rule
  WHERE tenant_id = $1::uuid AND question_id = $2::uuid
)
INSERT INTO scoring_rule (
  tenant_id, exam_id, question_id, version, rule_type, config,
  status, revision, content_hash, created_by
)
SELECT $1::uuid, target.exam_id, $2::uuid, next_version.version, $3, $4, 'draft', 1, $5, $6::uuid
FROM target, next_version
WHERE target.question_type = $3 OR ($3 = 'manual' AND target.question_type NOT IN ('single_choice','true_false','multiple_choice','fill_blank','numeric'))
RETURNING id::text, tenant_id::text, exam_id::text, question_id::text, version,
  rule_type, config, status, revision, content_hash, created_by::text,
  COALESCE(published_by::text, ''), published_at, created_at, updated_at
`, tenantID, questionID, input.RuleType, config, hash, actorID)
	return scanScoringRule(row)
}

func (s *PostgresStore) ListScoringRules(ctx context.Context, tenantID, questionID string) ([]ScoringRule, error) {
	rows, err := s.db.QueryContext(ctx, `
SELECT id::text, tenant_id::text, exam_id::text, question_id::text, version,
  rule_type, config, status, revision, content_hash, created_by::text,
  COALESCE(published_by::text, ''), published_at, created_at, updated_at
FROM scoring_rule
WHERE tenant_id = $1 AND question_id::text = $2 AND deleted_at IS NULL
ORDER BY version DESC
`, tenantID, questionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ScoringRule{}
	for rows.Next() {
		rule, err := scanScoringRule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rule)
	}
	return out, rows.Err()
}

func (s *PostgresStore) UpdateScoringRule(ctx context.Context, tenantID, id string, input UpdateScoringRuleInput) (ScoringRule, error) {
	if input.ExpectedRevision < 1 || input.Config == nil {
		return ScoringRule{}, ErrInvalidInput
	}
	var ruleType string
	if err := s.db.QueryRowContext(ctx, `SELECT rule_type FROM scoring_rule WHERE tenant_id=$1 AND id::text=$2 AND status='draft' AND deleted_at IS NULL`, tenantID, id).Scan(&ruleType); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ScoringRule{}, ErrNotFound
		}
		return ScoringRule{}, err
	}
	if err := validateRuleConfig(ruleType, input.Config); err != nil {
		return ScoringRule{}, err
	}
	config, hash, _ := marshalRule(ruleType, input.Config)
	rule, err := scanScoringRule(s.db.QueryRowContext(ctx, `
UPDATE scoring_rule SET config=$3, content_hash=$4, revision=revision+1, updated_at=now()
WHERE tenant_id=$1 AND id::text=$2 AND status='draft' AND revision=$5 AND deleted_at IS NULL
RETURNING id::text, tenant_id::text, exam_id::text, question_id::text, version,
  rule_type, config, status, revision, content_hash, created_by::text,
  COALESCE(published_by::text, ''), published_at, created_at, updated_at
`, tenantID, id, config, hash, input.ExpectedRevision))
	if errors.Is(err, ErrNotFound) {
		return ScoringRule{}, ErrRevisionConflict
	}
	return rule, err
}

func (s *PostgresStore) PublishScoringRule(ctx context.Context, tenantID, id, actorID string) (ScoringRule, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ScoringRule{}, err
	}
	defer tx.Rollback()
	var questionID, ruleType, questionType string
	var questionScore float64
	var configRaw []byte
	if err := tx.QueryRowContext(ctx, `SELECT sr.question_id::text,sr.rule_type,sr.config,q.question_type,q.score::float8
FROM scoring_rule sr JOIN question q ON q.tenant_id=sr.tenant_id AND q.id=sr.question_id AND q.deleted_at IS NULL AND q.status<>'deleted'
WHERE sr.tenant_id=$1 AND sr.id::text=$2 AND sr.status='draft' AND sr.deleted_at IS NULL FOR UPDATE OF sr,q`, tenantID, id).Scan(&questionID, &ruleType, &configRaw, &questionType, &questionScore); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ScoringRule{}, ErrNotFound
		}
		return ScoringRule{}, err
	}
	var config map[string]any
	if err := json.Unmarshal(configRaw, &config); err != nil {
		return ScoringRule{}, err
	}
	// 发布前在锁内重核题型与满分；导入可能已改变题目，旧草稿不能直接替代当前规则。
	if !scoringRuleMatchesQuestion(ruleType, config, questionType, questionScore) {
		return ScoringRule{}, ErrStaleScoringRule
	}
	if _, err := tx.ExecContext(ctx, `UPDATE scoring_rule SET status='retired', updated_at=now() WHERE tenant_id=$1 AND question_id::text=$2 AND status='published' AND deleted_at IS NULL`, tenantID, questionID); err != nil {
		return ScoringRule{}, err
	}
	rule, err := scanScoringRule(tx.QueryRowContext(ctx, `
UPDATE scoring_rule SET status='published', published_by=$3, published_at=now(), revision=revision+1, updated_at=now()
WHERE tenant_id=$1 AND id::text=$2 AND status='draft' AND deleted_at IS NULL
RETURNING id::text, tenant_id::text, exam_id::text, question_id::text, version,
  rule_type, config, status, revision, content_hash, created_by::text,
  COALESCE(published_by::text, ''), published_at, created_at, updated_at
`, tenantID, id, actorID))
	if err != nil {
		return ScoringRule{}, err
	}
	if err := tx.Commit(); err != nil {
		return ScoringRule{}, err
	}
	return rule, nil
}

func scoringRuleMatchesQuestion(ruleType string, config map[string]any, questionType string, score float64) bool {
	objective := questionType == "single_choice" || questionType == "true_false" || questionType == "multiple_choice" || questionType == "fill_blank" || questionType == "numeric"
	if ruleType != questionType && !(ruleType == "manual" && !objective) {
		return false
	}
	if raw, present := config["max_score"]; present {
		maximum, ok := floatValue(raw)
		return ok && !math.IsNaN(maximum) && !math.IsInf(maximum, 0) && math.Abs(maximum-score) <= 1e-6
	}
	// Legacy authored rules use the question score rather than a copied maximum.
	return true
}

type ruleScanner interface{ Scan(...any) error }

func scanScoringRule(row ruleScanner) (ScoringRule, error) {
	var out ScoringRule
	var config []byte
	var publishedAt sql.NullTime
	if err := row.Scan(&out.ID, &out.TenantID, &out.ExamID, &out.QuestionID, &out.Version,
		&out.RuleType, &config, &out.Status, &out.Revision, &out.ContentHash, &out.CreatedBy,
		&out.PublishedBy, &publishedAt, &out.CreatedAt, &out.UpdatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ScoringRule{}, ErrNotFound
		}
		return ScoringRule{}, err
	}
	out.Config = map[string]any{}
	if err := decodeJSONB(config, &out.Config, "scoring_rule.config"); err != nil {
		return ScoringRule{}, err
	}
	if publishedAt.Valid {
		value := publishedAt.Time.UTC()
		out.PublishedAt = &value
	}
	out.CreatedAt = out.CreatedAt.UTC()
	out.UpdatedAt = out.UpdatedAt.UTC()
	return out, nil
}

func marshalRule(ruleType string, config map[string]any) ([]byte, string, error) {
	payload, err := json.Marshal(map[string]any{"rule_type": ruleType, "config": config})
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(payload)
	configOnly, err := json.Marshal(config)
	return configOnly, hex.EncodeToString(sum[:]), err
}

func validateRuleConfig(ruleType string, config map[string]any) error {
	allowed := map[string]bool{"single_choice": true, "true_false": true, "multiple_choice": true, "fill_blank": true, "numeric": true, "manual": true}
	if !allowed[ruleType] {
		return ErrInvalidInput
	}
	if ruleType == "multiple_choice" {
		for _, key := range []string{"score_per_correct_option", "wrong_option_penalty", "minimum_score"} {
			if value, ok := config[key]; ok {
				number, ok := floatValue(value)
				if !ok || math.IsNaN(number) || math.IsInf(number, 0) || number < 0 {
					return ErrInvalidInput
				}
			}
		}
	}
	if ruleType == "numeric" {
		for _, key := range []string{"absolute", "relative"} {
			if value, ok := config[key]; ok {
				number, ok := floatValue(value)
				if !ok || math.IsNaN(number) || math.IsInf(number, 0) || number < 0 {
					return ErrInvalidInput
				}
			}
		}
	}
	return nil
}
