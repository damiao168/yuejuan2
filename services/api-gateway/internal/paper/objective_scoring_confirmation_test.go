package paper

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
)

func confirmationQuestion(kind string) Question {
	return Question{ID: "q1", QuestionNo: "1", QuestionType: kind, Score: 6, AnswerKey: &AnswerKey{QuestionID: "q1", StandardAnswer: []any{"A", "C"}}}
}

func TestObjectiveReadinessPreviewOnlyReadsRulePlan(t *testing.T) {
	question := confirmationQuestion("multiple_choice")
	script := &paperBatch10Script{steps: []paperBatch10Step{{kind: "query", fragment: "status IN ('draft','published')", columns: 5}}}
	db := sql.OpenDB(paperBatch10Connector{script: script})
	defer db.Close()
	input := ReadinessResult{Ready: true, ConfigurationHash: "configuration", ScoringPreview: []ReadinessScoringPreview{{QuestionID: question.ID, ScoringPolicy: assessment.ScoringPolicy{Mode: assessment.ScoringRuleAuto}}}}
	result, err := withObjectiveReadinessPlan(context.Background(), db, "tenant-1", "exam-1", "configured", []Question{question}, input)
	if err != nil || !result.Ready || result.ConfigurationHash == input.ConfigurationHash || script.next != len(script.steps) || script.unmatched != nil {
		t.Fatalf("read-only plan failed: %#v %v %#v", result, err, script)
	}
}

func TestObjectiveConfirmationCreatesNewVersionWhenOnlyRetiredHistoryExists(t *testing.T) {
	question := confirmationQuestion("multiple_choice")
	question.ID, question.AnswerKey.QuestionID = "question-1", "question-1"
	steps := []paperBatch10Step{
		{kind: "query", fragment: "config.scoring_policy_json", columns: 2, rows: [][]driver.Value{{question.ID, readinessFactJSON(t, `{"mode":"RULE_AUTO","require_evidence":true,"human_review_below_confidence":true}`)}}},
		{kind: "query", fragment: "status IN ('draft','published')", columns: 5},
		{kind: "exec", fragment: "status='published' AND deleted_at IS NULL", result: driver.RowsAffected(0)},
		{kind: "exec", fragment: "COALESCE(MAX(version),0)+1", result: driver.RowsAffected(1)},
	}
	store, script := openPaperBatch10Store(t, steps)
	tx, err := store.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := publishObjectiveConfirmationRulesTx(context.Background(), tx, "tenant-1", "exam-1", "actor-1", "configured", []Question{question}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if script.next != len(steps) || script.unmatched != nil {
		t.Fatalf("unexpected publication SQL: %#v", script)
	}
}

func TestObjectiveConfirmationKeepsTeacherPublishedPolicyOverImportedDraft(t *testing.T) {
	question := confirmationQuestion("multiple_choice")
	generated, _ := objectiveRuleConfig(PaperImportDraftQuestion{QuestionType: question.QuestionType, Score: question.Score, AnswerKey: &AnswerKeyInput{StandardAnswer: question.AnswerKey.StandardAnswer}})
	teacher := objectiveConfirmationRule{ID: "teacher", Status: "published", RuleType: question.QuestionType, Config: map[string]any{"max_score": 6.0, "allow_partial": true, "score_per_correct_option": 2.0, "wrong_option_penalty": 1.0}}
	rule, err := objectiveRuleForConfirmation(question, []objectiveConfirmationRule{{ID: "imported", Status: "draft", RuleType: question.QuestionType, Config: generated}, teacher})
	if err != nil || rule.ID != teacher.ID || rule.Config["score_per_correct_option"] != 2.0 {
		t.Fatalf("published teacher policy lost: %#v, %v", rule, err)
	}
}

func TestObjectiveConfirmationPublishesTeacherDraftAndRejectsScoreConflict(t *testing.T) {
	question := confirmationQuestion("multiple_choice")
	draft := objectiveConfirmationRule{ID: "teacher-draft", Status: "draft", RuleType: question.QuestionType, Config: map[string]any{"max_score": 6, "allow_partial": true, "score_per_correct_option": 2.0}}
	rule, err := objectiveRuleForConfirmation(question, []objectiveConfirmationRule{draft})
	if err != nil || rule.ID != draft.ID || rule.Config["allow_partial"] != true {
		t.Fatalf("teacher draft replaced: %#v, %v", rule, err)
	}
	question.Score = 8
	if _, err := objectiveRuleForConfirmation(question, []objectiveConfirmationRule{draft}); !errors.Is(err, ErrImportedObjectiveRuleConflict) {
		t.Fatalf("score conflict accepted: %v", err)
	}
}

func TestObjectiveConfirmationDoesNotReviveRetiredPolicy(t *testing.T) {
	question := confirmationQuestion("multiple_choice")
	rule, err := objectiveRuleForConfirmation(question, []objectiveConfirmationRule{{ID: "old", Status: "retired", RuleType: question.QuestionType, Config: map[string]any{"allow_partial": true, "score_per_correct_option": 3.0}}})
	if err != nil || rule.ID != "" || rule.Config["allow_partial"] != false {
		t.Fatalf("retired policy was reused: %#v, %v", rule, err)
	}
}

func TestObjectiveConfirmationRequiresExplicitPartialScore(t *testing.T) {
	question := confirmationQuestion("multiple_choice")
	question.AnswerKey.Tolerance = map[string]any{"allow_partial": true}
	if _, err := objectiveRuleForConfirmation(question, nil); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("missing partial score accepted: %v", err)
	}
}

func TestObjectiveConfirmationPreservesLegacyNumericTolerance(t *testing.T) {
	question := confirmationQuestion("numeric")
	question.AnswerKey.StandardAnswer = 0.5
	question.AnswerKey.Tolerance = map[string]any{"value": 0.01, "unit_required": true}
	rule, err := objectiveRuleForConfirmation(question, nil)
	if err != nil || rule.Config["absolute"] != 0.01 || rule.Config["unit_required"] != true {
		t.Fatalf("numeric tolerance lost: %#v, %v", rule, err)
	}
}
