package paper

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
)

type objectiveConfirmationRule struct {
	ID       string         `json:"-"`
	RuleType string         `json:"rule_type"`
	Config   map[string]any `json:"config"`
	Status   string         `json:"-"`
}

// 默认导入草稿可随当前答案重建；教师编辑过的草稿必须原样发布或明确报冲突。
// retired 从不参与选择，即使只有历史版本，也必须创建新的身份。
func objectiveRuleForConfirmation(question Question, rules []objectiveConfirmationRule) (objectiveConfirmationRule, error) {
	draft := PaperImportDraftQuestion{QuestionType: question.QuestionType, Score: question.Score}
	if question.AnswerKey != nil {
		draft.AnswerKey = &AnswerKeyInput{StandardAnswer: question.AnswerKey.StandardAnswer, Tolerance: question.AnswerKey.Tolerance}
	}
	generated, ok := objectiveRuleConfig(draft)
	if !ok {
		return objectiveConfirmationRule{}, ErrInvalidInput
	}
	var published, importedDraft *objectiveConfirmationRule
	for _, rule := range rules {
		if rule.Status == "draft" {
			if untouchedImportedObjectiveDraft(rule.Config) {
				copy := rule
				importedDraft = &copy
				continue
			}
			if !teacherObjectiveDraftCompatible(rule.RuleType, rule.Config, draft) {
				return objectiveConfirmationRule{}, ErrImportedObjectiveRuleConflict
			}
			return rule, validateObjectiveConfirmationConfig(question.QuestionType, rule.Config, question.Score)
		}
		if rule.Status == "published" && teacherObjectiveDraftCompatible(rule.RuleType, rule.Config, draft) {
			copy := rule
			published = &copy
		}
	}
	if published != nil {
		// 未编辑的默认规则应反映本次确认的容差；教师规则的显式政策则优先保留。
		if !untouchedImportedObjectiveDraft(published.Config) || objectiveRuleFingerprint(published.Config) == objectiveRuleFingerprint(generated) {
			return *published, validateObjectiveConfirmationConfig(question.QuestionType, published.Config, question.Score)
		}
	}
	if importedDraft != nil {
		// 自动导入草稿不能盖过有效教师规则；确实需要新版本时才按当前已确认答案重建。
		importedDraft.RuleType, importedDraft.Config = question.QuestionType, generated
		return *importedDraft, validateObjectiveConfirmationConfig(question.QuestionType, generated, question.Score)
	}
	return objectiveConfirmationRule{RuleType: question.QuestionType, Config: generated}, validateObjectiveConfirmationConfig(question.QuestionType, generated, question.Score)
}

func validateObjectiveConfirmationConfig(ruleType string, config map[string]any, maxScore float64) error {
	_, hasPartial := config["partial_score"]
	_, hasWrong := config["wrong_answer_score"]
	fixedChoice := ruleType == "multiple_choice" && (hasPartial || hasWrong)
	if fixedChoice {
		// 两项固定分必须同时明确；兼容保留的旧按项配置不再影响固定模式的校验或判分。
		for _, field := range []string{"partial_score", "wrong_answer_score"} {
			number, valid := jsonNumericValue(config[field])
			if !valid || math.IsNaN(number) || math.IsInf(number, 0) || number < 0 || number > maxScore {
				return ErrInvalidInput
			}
		}
	}
	for _, field := range []string{"allow_partial", "ignore_case", "ignore_spaces", "ignore_punctuation", "unit_required"} {
		if fixedChoice && field == "allow_partial" {
			continue
		}
		if value, present := config[field]; present {
			if _, valid := value.(bool); !valid {
				return ErrInvalidInput
			}
		}
	}
	keys := []string{"max_score"}
	if ruleType == "numeric" {
		keys = append(keys, "absolute", "relative", "value")
	}
	if ruleType == "multiple_choice" && !fixedChoice {
		keys = append(keys, "score_per_correct_option", "wrong_option_penalty", "minimum_score")
	}
	for _, key := range keys {
		if value, present := config[key]; present {
			number, valid := jsonNumericValue(value)
			if !valid || math.IsNaN(number) || math.IsInf(number, 0) || number < 0 {
				return ErrInvalidInput
			}
		}
	}
	// 少选计分一旦开启，需要明确每项分值；不沿用引擎中按比例猜分的兼容缺省。
	if ruleType == "multiple_choice" && !fixedChoice && config["allow_partial"] == true {
		value, valid := jsonNumericValue(config["score_per_correct_option"])
		if !valid || value <= 0 {
			return ErrInvalidInput
		}
	}
	return nil
}

func jsonNumericValue(value any) (float64, bool) {
	switch number := value.(type) {
	case float64:
		return number, true
	case int:
		return float64(number), true
	case json.Number:
		result, err := number.Float64()
		return result, err == nil
	default:
		return 0, false
	}
}

// RepairConfirmedObjectiveRules 用于明确的维护/恢复命令；GET 准备度不调用此方法。
// 仅为内容仍与已确认快照一致的考试补齐有效规则，任何题冲突都会回滚整个修复。
func (s *PostgresStore) RepairConfirmedObjectiveRules(ctx context.Context, tenantID, examID, actorID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, examID+":scoring-run"); err != nil {
		return err
	}
	if err := RepairConfirmedObjectiveRulesInTx(ctx, tx, tenantID, examID, actorID); err != nil {
		return err
	}
	return tx.Commit()
}

func loadObjectiveConfirmationRules(ctx context.Context, queryer postgresQueryer, tenantID, examID string) (map[string][]objectiveConfirmationRule, error) {
	rows, err := queryer.QueryContext(ctx, `SELECT question_id::text,id::text,rule_type,config,status FROM scoring_rule WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND status IN ('draft','published') AND deleted_at IS NULL ORDER BY question_id,version DESC`, tenantID, examID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]objectiveConfirmationRule{}
	for rows.Next() {
		var questionID string
		var rule objectiveConfirmationRule
		var raw []byte
		if err := rows.Scan(&questionID, &rule.ID, &rule.RuleType, &raw, &rule.Status); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &rule.Config); err != nil {
			return nil, err
		}
		out[questionID] = append(out[questionID], rule)
	}
	return out, rows.Err()
}

func objectiveConfirmationPlan(ctx context.Context, queryer postgresQueryer, tenantID, examID, status string, questions []Question) (map[string]objectiveConfirmationRule, error) {
	facts, err := loadReadinessAssessmentFacts(ctx, queryer, tenantID, examID, status)
	if err != nil {
		return nil, err
	}
	modes := map[string]assessment.ScoringMode{}
	for _, fact := range facts {
		modes[fact.QuestionID] = fact.Policy.Mode
	}
	return objectiveConfirmationPlanForModes(ctx, queryer, tenantID, examID, questions, modes)
}

func objectiveConfirmationPlanForModes(ctx context.Context, queryer postgresQueryer, tenantID, examID string, questions []Question, modes map[string]assessment.ScoringMode) (map[string]objectiveConfirmationRule, error) {
	rules, err := loadObjectiveConfirmationRules(ctx, queryer, tenantID, examID)
	if err != nil {
		return nil, err
	}
	plan := map[string]objectiveConfirmationRule{}
	for _, question := range questions {
		if modes[question.ID] != assessment.ScoringRuleAuto {
			continue
		}
		rule, err := objectiveRuleForConfirmation(question, rules[question.ID])
		if err != nil {
			return nil, fmt.Errorf("question %s scoring policy: %w", question.QuestionNo, err)
		}
		plan[question.ID] = rule
	}
	return plan, nil
}

// GET 只计算将采用的语义；草稿冲突是可定位的准备阻断项，不在预览时发布或修订规则。
func withObjectiveReadinessPlan(ctx context.Context, queryer postgresQueryer, tenantID, examID, status string, questions []Question, result ReadinessResult) (ReadinessResult, error) {
	if !result.Ready {
		return result, nil
	}
	modes := map[string]assessment.ScoringMode{}
	for _, preview := range result.ScoringPreview {
		modes[preview.QuestionID] = preview.ScoringPolicy.Mode
	}
	if !hasObjectiveRuleAutoPreview(result) {
		return result, nil
	}
	plan, err := objectiveConfirmationPlanForModes(ctx, queryer, tenantID, examID, questions, modes)
	if err != nil {
		if !errors.Is(err, ErrInvalidInput) && !errors.Is(err, ErrImportedObjectiveRuleConflict) {
			return ReadinessResult{}, err
		}
		result.Ready = false
		result.Checks = append(result.Checks, ReadinessCheck{Code: "objective_scoring_policy", Label: "客观题评分策略", Passed: false, Severity: "blocker", Section: "questions", Message: err.Error()})
		return result, nil
	}
	result.ConfigurationHash = stableContentHash(map[string]any{"version": 4, "configuration_hash": result.ConfigurationHash, "objective_rules": plan})
	return result, nil
}

func hasObjectiveRuleAutoPreview(result ReadinessResult) bool {
	for _, preview := range result.ScoringPreview {
		if preview.ScoringPolicy.Mode == assessment.ScoringRuleAuto {
			return true
		}
	}
	return false
}

// 确认事务已锁考试后再锁题目与现行规则，避免检查完摘要后教师草稿继续改变。
func lockObjectiveConfirmationRulesTx(ctx context.Context, tx *sql.Tx, tenantID, examID string) error {
	for _, query := range []string{
		`SELECT id FROM question WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND deleted_at IS NULL ORDER BY id FOR UPDATE`,
		`SELECT id FROM scoring_rule WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND status IN ('draft','published') AND deleted_at IS NULL ORDER BY id FOR UPDATE`,
	} {
		rows, err := tx.QueryContext(ctx, query, tenantID, examID)
		if err != nil {
			return err
		}
		for rows.Next() {
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

func publishObjectiveConfirmationRulesTx(ctx context.Context, tx *sql.Tx, tenantID, examID, actorID, status string, questions []Question) error {
	plan, err := objectiveConfirmationPlan(ctx, tx, tenantID, examID, status, questions)
	if err != nil {
		return err
	}
	for questionID, rule := range plan {
		if rule.Status == "published" {
			continue
		}
		config, err := json.Marshal(rule.Config)
		if err != nil {
			return err
		}
		hash := stableContentHash(map[string]any{"rule_type": rule.RuleType, "config": rule.Config})
		if _, err := tx.ExecContext(ctx, `UPDATE scoring_rule SET status='retired',updated_at=now() WHERE tenant_id=$1::uuid AND question_id=$2::uuid AND status='published' AND deleted_at IS NULL`, tenantID, questionID); err != nil {
			return err
		}
		if rule.Status == "draft" {
			_, err = tx.ExecContext(ctx, `UPDATE scoring_rule SET rule_type=$3,config=$4::jsonb,content_hash=$5,status='published',published_by=$6::uuid,published_at=now(),revision=revision+1,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid AND status='draft' AND deleted_at IS NULL`, tenantID, rule.ID, rule.RuleType, config, hash, actorID)
		} else {
			_, err = tx.ExecContext(ctx, `INSERT INTO scoring_rule(tenant_id,exam_id,question_id,version,rule_type,config,status,revision,content_hash,created_by,published_by,published_at) SELECT $1::uuid,$2::uuid,$3::uuid,COALESCE(MAX(version),0)+1,$4,$5::jsonb,'published',1,$6,$7::uuid,$7::uuid,now() FROM scoring_rule WHERE tenant_id=$1::uuid AND question_id=$3::uuid`, tenantID, examID, questionID, rule.RuleType, config, hash, actorID)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// RepairConfirmedObjectiveRulesInTx 由显式开始/恢复命令补齐历史考试的有效规则。
// 调用方负责事务提交；只有当前编写事实仍等于 passed 内容快照时才能修复，不能用后改的答案冒充原确认。
func RepairConfirmedObjectiveRulesInTx(ctx context.Context, tx *sql.Tx, tenantID, examID, actorID string) error {
	var status string
	if err := tx.QueryRowContext(ctx, `SELECT status FROM exam WHERE tenant_id=$1::uuid AND id=$2::uuid AND deleted_at IS NULL FOR UPDATE`, tenantID, examID).Scan(&status); err != nil {
		return err
	}
	if status != "ready" && status != "collecting" && status != "processing" && status != "grading" {
		return ErrNotReady
	}
	if err := lockObjectiveConfirmationRulesTx(ctx, tx, tenantID, examID); err != nil {
		return err
	}
	var raw []byte
	var hash string
	if err := tx.QueryRowContext(ctx, `SELECT import_snapshot_json,COALESCE(import_snapshot_hash,'') FROM exam_readiness_snapshot WHERE tenant_id=$1::uuid AND exam_id=$2::uuid AND status='passed' ORDER BY confirmed_at DESC,id DESC LIMIT 1 FOR SHARE`, tenantID, examID).Scan(&raw, &hash); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotReady
		}
		return err
	}
	var confirmed readinessImportSnapshot
	if json.Unmarshal(raw, &confirmed) != nil || confirmed.Version < 1 || confirmed.Version > 2 || len(confirmed.Questions) == 0 || hash == "" || stableContentHash(confirmed) != hash {
		return ErrReadinessChanged
	}
	questions, err := listQuestions(ctx, tx, tenantID, examID)
	if err != nil {
		return err
	}
	current := newReadinessImportSnapshot(confirmed.ConfigurationHash, questions)
	if err := bindReadinessAssessmentSnapshots(ctx, tx, tenantID, examID, &current); err != nil {
		return err
	}
	if stableContentHash(current) != hash {
		return ErrReadinessChanged
	}
	return publishObjectiveConfirmationRulesTx(ctx, tx, tenantID, examID, actorID, status, questions)
}
