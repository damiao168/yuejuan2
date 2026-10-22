package grading

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/assessment"
	"edugrade-enterprise/services/api-gateway/internal/paper"
)

func frozenRuleFixture(kind string) scoringRunRuleSnapshot {
	key := &paper.AnswerKey{ID: "key-1", QuestionID: "q1", AnswerVersion: "v1", StandardAnswer: []any{"A", "C"}}
	rule := &ScoringRule{ID: "rule-1", QuestionID: "q1", RuleType: kind, Version: 1, Config: map[string]any{"max_score": 6.0, "allow_partial": true, "score_per_correct_option": 2.0, "wrong_option_penalty": 1.0, "minimum_score": 0.0}}
	_, rule.ContentHash, _ = marshalRule(kind, rule.Config)
	keyRaw, _ := json.Marshal(key)
	sum := sha256.Sum256(keyRaw)
	item := scoringRunQuestionRule{QuestionID: "q1", QuestionNo: "1", QuestionType: kind, Score: 6, Question: paper.Question{ID: "q1", QuestionType: kind, Score: 6}, Rule: rule, AnswerKey: key, AnswerHash: hex.EncodeToString(sum[:]), AssessmentSnapshot: assessment.ExamQuestionSnapshot{ID: "snap-1", QuestionID: "q1", ScoringPolicySnapshot: assessment.ScoringPolicy{Mode: assessment.ScoringRuleAuto}}}
	item.Hash = scoringRunQuestionRuleHash(item)
	return scoringRunRuleSnapshot{Version: 1, Questions: map[string]scoringRunQuestionRule{"q1": item}}
}

func TestFrozenScoringRuleUsesRunAnswerAndTeacherPartialPolicy(t *testing.T) {
	snapshot := frozenRuleFixture("multiple_choice")
	current := Context{SegmentID: "seg-1", Question: paper.Question{ID: "q1", QuestionType: "single_choice", Score: 100}, AnswerKey: paper.AnswerKey{StandardAnswer: "B"}, Answer: SegmentAnswer{AnswerPayload: map[string]any{"answers": []any{"A"}}}}
	frozen, ruleID, err := applyScoringRunRuleSnapshot(snapshot, current)
	if err != nil || ruleID != "rule-1" {
		t.Fatalf("frozen context unavailable: %v", err)
	}
	grade, err := NewEngine().Grade(frozen)
	if err != nil || grade.MaxScore != 6 || grade.SuggestedScore != 2 {
		t.Fatalf("current rule leaked into frozen grade: %#v, %v", grade, err)
	}
	frozen.Answer.AnswerPayload["answers"] = []any{"A", "B"}
	grade, err = NewEngine().Grade(frozen)
	if err != nil || grade.SuggestedScore != 1 {
		t.Fatalf("frozen wrong-option penalty lost: %#v, %v", grade, err)
	}
}

func TestFrozenScoringRuleRejectsCorruptOrHumanPolicy(t *testing.T) {
	snapshot := frozenRuleFixture("multiple_choice")
	item := snapshot.Questions["q1"]
	item.Rule.Config["wrong_option_penalty"] = 0.0
	snapshot.Questions["q1"] = item
	if _, _, err := applyScoringRunRuleSnapshot(snapshot, Context{Question: paper.Question{ID: "q1"}}); !errors.Is(err, ErrStaleScoringRule) {
		t.Fatalf("corrupt snapshot accepted: %v", err)
	}
	snapshot = frozenRuleFixture("multiple_choice")
	item = snapshot.Questions["q1"]
	item.AssessmentSnapshot.ScoringPolicySnapshot.Mode = assessment.ScoringHumanPrimary
	item.Hash = scoringRunQuestionRuleHash(item)
	snapshot.Questions["q1"] = item
	if _, _, err := applyScoringRunRuleSnapshot(snapshot, Context{Question: paper.Question{ID: "q1"}}); !errors.Is(err, ErrStaleScoringRule) {
		t.Fatalf("human policy entered rule auto grade: %v", err)
	}
}

func TestOMRSetQualitySupportsMultipleAndExplicitBlankWithoutBypassingEligibility(t *testing.T) {
	quality := map[string]any{"version": "omr-set-quality-v1", "question_type": "multiple_choice", "classification": "clear", "option_count": 4, "selected_count": 2, "set_confidence": 0.99, "risk_flags": []string{}}
	input := OMRResultInput{Decision: "selected", Selected: []string{"A", "C"}, Confidence: 0.99, Thresholds: map[string]any{"quality": quality}}
	if !omrResultCanAutoConfirmForQuestion(input, "multiple_choice", true, 0.98) {
		t.Fatal("clear multiple-choice set rejected")
	}
	if omrResultCanAutoConfirmForQuestion(input, "multiple_choice", false, 0.98) {
		t.Fatal("set quality bypassed calibration")
	}
	if omrResultCanAutoConfirmForQuestion(input, "single_choice", true, 0.98) {
		t.Fatal("multiple marks confirmed as single choice")
	}
	quality["classification"], quality["selected_count"] = "blank", 0
	input.Decision, input.Selected = "blank", []string{}
	if !omrResultCanAutoConfirmForQuestion(input, "multiple_choice", true, 0.98) {
		t.Fatal("explicit clear blank rejected")
	}
	quality["classification"], quality["risk_flags"] = "possible_erasure", []string{"possible_erasure"}
	if omrResultCanAutoConfirmForQuestion(input, "multiple_choice", true, 0.98) {
		t.Fatal("erasure confirmed as blank")
	}
	if reason := omrReviewReasonForQuestion(input, "multiple_choice", true, "", 0.98); reason != "omr_possible_erasure" {
		t.Fatalf("erasure reason lost: %s", reason)
	}
}
