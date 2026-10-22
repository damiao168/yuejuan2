package paper

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
)

// ErrImportedObjectiveRuleConflict 表示导入改变题型或分值前，必须先协调已有的教师编写草稿。
var ErrImportedObjectiveRuleConflict = errors.New("imported objective rule conflicts with teacher draft")

const importedObjectiveRuleOrigin = "paper_import_default"

// 评分工作台将客观题策略与答案键分开存储。
// 导入只创建可复核草稿，发布仍需显式操作。
func objectiveRuleConfig(draft PaperImportDraftQuestion) (map[string]any, bool) {
	if draft.Score <= 0 || math.IsNaN(draft.Score) || math.IsInf(draft.Score, 0) || draft.AnswerKey == nil || emptyAnswer(draft.AnswerKey.StandardAnswer) {
		return nil, false
	}
	var config map[string]any
	var toleranceFields []string
	switch draft.QuestionType {
	case "single_choice", "true_false":
		config = map[string]any{"policy": "exact_match", "max_score": draft.Score}
	case "multiple_choice":
		config = map[string]any{"policy": "exact_set", "max_score": draft.Score, "allow_partial": false, "wrong_option_penalty": draft.Score, "minimum_score": 0}
		toleranceFields = []string{"allow_partial", "score_per_correct_option", "wrong_option_penalty", "minimum_score", "partial_score", "wrong_answer_score"}
	case "fill_blank":
		config = map[string]any{"policy": "exact_or_equivalent", "max_score": draft.Score, "ignore_case": false, "ignore_spaces": false, "ignore_punctuation": false}
		toleranceFields = []string{"ignore_case", "ignore_spaces", "ignore_punctuation"}
	case "numeric":
		config = map[string]any{"policy": "numeric_tolerance", "max_score": draft.Score, "absolute": 0.0, "relative": 0.0, "unit_required": false}
		toleranceFields = []string{"absolute", "relative", "unit_required"}
	default:
		return nil, false
	}
	// 答案键可能已有从教师材料中提取的显式策略。默认值不能覆盖这些已复核设置。
	tolerance, _ := draft.AnswerKey.Tolerance.(map[string]any)
	// 旧数值配置用 value 表示绝对容差；不能被新默认值 absolute=0 遮住。
	if draft.QuestionType == "numeric" {
		if value, exists := tolerance["value"]; exists {
			config["absolute"] = value
		}
	}
	for _, field := range toleranceFields {
		if value, exists := tolerance[field]; exists {
			config[field] = value
		}
	}
	if draft.QuestionType == "multiple_choice" {
		_, fixedPartial := tolerance["partial_score"]
		_, fixedWrong := tolerance["wrong_answer_score"]
		legacy := false
		for _, field := range []string{"allow_partial", "score_per_correct_option", "wrong_option_penalty", "minimum_score"} {
			if _, exists := tolerance[field]; exists {
				legacy = true
			}
		}
		// 新规则默认少选、错选均为 0；答案键已有按项计分时保留原政策，不用新字段改变旧卷分数。
		if fixedPartial || fixedWrong || !legacy {
			if !fixedPartial {
				config["partial_score"] = 0
			}
			if !fixedWrong {
				config["wrong_answer_score"] = 0
			}
		}
	}
	config["origin"] = importedObjectiveRuleOrigin
	config["import_generated_hash"] = objectiveRuleFingerprint(config)
	return config, true
}

func objectiveRuleFingerprint(config map[string]any) string {
	canonical := make(map[string]any, len(config))
	for key, value := range config {
		if key != "import_generated_hash" {
			canonical[key] = value
		}
	}
	payload, err := json.Marshal(canonical)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

// 摘要只用来判断导入草稿是否被编辑；教师改过的草稿需保留或报冲突，不能直接覆盖。
func untouchedImportedObjectiveDraft(config map[string]any) bool {
	marker, ok := config["import_generated_hash"].(string)
	return ok && config["origin"] == importedObjectiveRuleOrigin && marker != "" && marker == objectiveRuleFingerprint(config)
}

func insertImportedObjectiveRuleDraft(ctx context.Context, tx *sql.Tx, tenantID, examID, questionID, actorID string, draft PaperImportDraftQuestion) error {
	config, ok := objectiveRuleConfig(draft)
	if !ok {
		return nil
	}
	configJSON, err := json.Marshal(config)
	if err != nil {
		return err
	}
	hashJSON, err := json.Marshal(map[string]any{"rule_type": draft.QuestionType, "config": config})
	if err != nil {
		return err
	}
	hash := sha256.Sum256(hashJSON)
	var existingID, existingType string
	var existingRaw []byte
	err = tx.QueryRowContext(ctx, `SELECT id::text,rule_type,config FROM scoring_rule WHERE tenant_id=$1::uuid AND question_id=$2::uuid AND status='draft' AND deleted_at IS NULL FOR UPDATE`, tenantID, questionID).Scan(&existingID, &existingType, &existingRaw)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		var existingConfig map[string]any
		if err := json.Unmarshal(existingRaw, &existingConfig); err != nil {
			return err
		}
		if !untouchedImportedObjectiveDraft(existingConfig) {
			if !teacherObjectiveDraftCompatible(existingType, existingConfig, draft) {
				return ErrImportedObjectiveRuleConflict
			}
			return nil
		}
		_, err = tx.ExecContext(ctx, `UPDATE scoring_rule SET rule_type=$3,config=$4,content_hash=$5,revision=revision+1,updated_at=now() WHERE tenant_id=$1::uuid AND id=$2::uuid AND status='draft' AND deleted_at IS NULL`, tenantID, existingID, draft.QuestionType, configJSON, hex.EncodeToString(hash[:]))
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO scoring_rule(tenant_id,exam_id,question_id,version,rule_type,config,status,revision,content_hash,created_by)
SELECT $1::uuid,$2::uuid,$3::uuid,COALESCE(MAX(version),0)+1,$4,$5,'draft',1,$6,$7::uuid
FROM scoring_rule WHERE tenant_id=$1::uuid AND question_id=$3::uuid`, tenantID, examID, questionID, draft.QuestionType, configJSON, hex.EncodeToString(hash[:]), actorID)
	return err
}

func teacherObjectiveDraftCompatible(ruleType string, config map[string]any, draft PaperImportDraftQuestion) bool {
	if ruleType != draft.QuestionType {
		return false
	}
	if raw, ok := config["max_score"]; ok {
		score, valid := jsonNumericValue(raw)
		return valid && scoreEqual(score, draft.Score)
	}
	// 未包含 max_score 的旧教师规则在评分时使用 question.score。
	return true
}
