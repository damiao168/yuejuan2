package grading

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/paper"
)

type scoringRunRuleSnapshot struct {
	Version   int                               `json:"version"`
	Questions map[string]scoringRunQuestionRule `json:"questions"`
	AIPlan    *GradingAIPlan                    `json:"ai_plan,omitempty"`
}

type scoringRunQuestionRule struct {
	QuestionID         string                          `json:"question_id"`
	QuestionNo         string                          `json:"question_no"`
	QuestionType       string                          `json:"question_type"`
	Score              float64                         `json:"score"`
	Question           paper.Question                  `json:"question"`
	Rubric             *paper.Rubric                   `json:"rubric,omitempty"`
	Rule               *ScoringRule                    `json:"rule,omitempty"`
	AnswerKey          *paper.AnswerKey                `json:"answer_key,omitempty"`
	AnswerHash         string                          `json:"answer_hash"`
	AssessmentSnapshot assessment.ExamQuestionSnapshot `json:"assessment_snapshot"`
	Hash               string                          `json:"hash"`
}

func scoringRunQuestionRuleHash(item scoringRunQuestionRule) string {
	item.Hash = ""
	raw, err := json.Marshal(item)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// 同一运行只写一次规则与答案内容。后续发布/退役不改变本轮比较依据，也不能靠读取最新版本补漏。
func freezeScoringRunRulesTx(ctx context.Context, tx *sql.Tx, tenantID, examID, runID string) error {
	rows, err := tx.QueryContext(ctx, `
SELECT q.id::text,q.question_no,q.question_type,q.score::float8,
       COALESCE(to_jsonb(rule),'null'::jsonb),COALESCE(to_jsonb(ak),'null'::jsonb),
       COALESCE(to_jsonb(snapshot),'null'::jsonb),to_jsonb(q),COALESCE(to_jsonb(rubric),'null'::jsonb)
FROM question q
LEFT JOIN LATERAL (SELECT * FROM scoring_rule WHERE tenant_id=q.tenant_id AND question_id=q.id AND status='published' AND deleted_at IS NULL ORDER BY version DESC LIMIT 1) rule ON true
LEFT JOIN LATERAL (SELECT * FROM question_answer_key WHERE tenant_id=q.tenant_id AND question_id=q.id AND deleted_at IS NULL ORDER BY created_at DESC,id DESC LIMIT 1) ak ON true
LEFT JOIN LATERAL (SELECT * FROM exam_question_snapshot WHERE tenant_id=q.tenant_id AND exam_id=q.exam_id AND question_id=q.id ORDER BY snapshot_version DESC LIMIT 1) snapshot ON true
LEFT JOIN LATERAL (SELECT qr.*,rv.version FROM question_rubric qr JOIN rubric_version rv ON rv.tenant_id=qr.tenant_id AND rv.id=qr.rubric_version_id AND rv.deleted_at IS NULL WHERE qr.tenant_id=q.tenant_id AND qr.question_id=q.id AND qr.deleted_at IS NULL ORDER BY qr.created_at DESC,qr.id DESC LIMIT 1) rubric ON true
WHERE q.tenant_id=$1::uuid AND q.exam_id=$2::uuid AND q.deleted_at IS NULL AND q.status<>'deleted'
ORDER BY q.id`, tenantID, examID)
	if err != nil {
		return err
	}
	snapshot := scoringRunRuleSnapshot{Version: 1, Questions: map[string]scoringRunQuestionRule{}}
	for rows.Next() {
		var item scoringRunQuestionRule
		var ruleRaw, keyRaw, assessmentRaw, questionRaw, rubricRaw []byte
		if err := rows.Scan(&item.QuestionID, &item.QuestionNo, &item.QuestionType, &item.Score, &ruleRaw, &keyRaw, &assessmentRaw, &questionRaw, &rubricRaw); err != nil {
			rows.Close()
			return err
		}
		if err := json.Unmarshal(ruleRaw, &item.Rule); err != nil {
			rows.Close()
			return err
		}
		if err := json.Unmarshal(keyRaw, &item.AnswerKey); err != nil {
			rows.Close()
			return err
		}
		if err := json.Unmarshal(assessmentRaw, &item.AssessmentSnapshot); err != nil {
			rows.Close()
			return err
		}
		if err := json.Unmarshal(questionRaw, &item.Question); err != nil {
			rows.Close()
			return err
		}
		if err := json.Unmarshal(rubricRaw, &item.Rubric); err != nil {
			rows.Close()
			return err
		}
		keyHash, _ := json.Marshal(item.AnswerKey)
		sum := sha256.Sum256(keyHash)
		item.AnswerHash = hex.EncodeToString(sum[:])
		item.Hash = scoringRunQuestionRuleHash(item)
		snapshot.Questions[item.QuestionID] = item
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	plan, err := loadGradingAIPlan(ctx, tx, tenantID, examID)
	if err != nil {
		return err
	}
	snapshot.AIPlan = &plan.Plan
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE scoring_run SET rule_snapshot=$3::jsonb WHERE tenant_id=$1::uuid AND id=$2::uuid AND exam_id=$4::uuid AND rule_snapshot='{}'::jsonb`, tenantID, runID, raw, examID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return ErrRevisionConflict
	}
	return nil
}

func applyScoringRunRuleSnapshot(snapshot scoringRunRuleSnapshot, current Context) (Context, string, error) {
	item, ok := snapshot.Questions[current.Question.ID]
	if snapshot.Version != 1 || !ok || item.Hash == "" || item.Hash != scoringRunQuestionRuleHash(item) || item.QuestionID != current.Question.ID {
		return Context{}, "", ErrStaleScoringRule
	}
	if item.Rule == nil || item.AnswerKey == nil || item.Rule.ID == "" || item.AssessmentSnapshot.ID == "" || item.Rule.RuleType != item.QuestionType || item.Rule.QuestionID != item.QuestionID || item.AnswerKey.QuestionID != item.QuestionID || item.AssessmentSnapshot.QuestionID != item.QuestionID || item.AssessmentSnapshot.ScoringPolicySnapshot.Mode != assessment.ScoringRuleAuto {
		return Context{}, "", ErrStaleScoringRule
	}
	_, ruleHash, err := marshalRule(item.Rule.RuleType, item.Rule.Config)
	keyRaw, keyErr := json.Marshal(item.AnswerKey)
	keySum := sha256.Sum256(keyRaw)
	if err != nil || keyErr != nil || ruleHash != item.Rule.ContentHash || item.AnswerHash != hex.EncodeToString(keySum[:]) {
		return Context{}, "", ErrStaleScoringRule
	}
	if item.Question.ID != item.QuestionID || item.Question.QuestionType != item.QuestionType || item.Question.Score != item.Score {
		return Context{}, "", ErrStaleScoringRule
	}
	current.Question = item.Question
	current.Question.QuestionNo, current.Question.QuestionType, current.Question.Score = item.QuestionNo, item.QuestionType, item.Score
	current.Question.Rubric = item.Rubric
	current.AnswerKey = *item.AnswerKey
	// 容差先继承答案键，再由本轮冻结的教师策略覆盖；不再查询回调时的 published。
	policy := map[string]any{}
	for key, value := range toleranceMap(item.AnswerKey.Tolerance) {
		policy[key] = value
	}
	for key, value := range item.Rule.Config {
		policy[key] = value
	}
	current.AnswerKey.Tolerance = policy
	current.Question.AnswerKey = &current.AnswerKey
	current.AssessmentSnapshot = item.AssessmentSnapshot
	return current, item.Rule.ID, nil
}

func applyFrozenScoringRunContextTx(ctx context.Context, tx *sql.Tx, tenantID, runID string, current Context) (Context, string, error) {
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT rule_snapshot FROM scoring_run WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL`, tenantID, runID).Scan(&raw); err != nil {
		return Context{}, "", err
	}
	var snapshot scoringRunRuleSnapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return Context{}, "", err
	}
	return applyScoringRunRuleSnapshot(snapshot, current)
}

// 旧运行没有可验证的快照时保留历史并转复核，不能在回调时为它偷偷采用新规则。
func frozenScoringRuleIDTx(ctx context.Context, tx *sql.Tx, tenantID, runID, questionID string) (string, string, error) {
	current, ruleID, err := applyFrozenScoringRunContextTx(ctx, tx, tenantID, runID, Context{Question: paper.Question{ID: questionID}})
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	return ruleID, current.AssessmentSnapshot.ID, err
}
