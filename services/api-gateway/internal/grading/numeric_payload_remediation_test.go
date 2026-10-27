package grading_test

import (
	"context"
	"edugrade-enterprise/services/api-gateway/internal/grading"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"math"
	"testing"
)

// 合成答案通过正式输入校验与存储，再进入真实规则引擎；不连接服务或外部数据库。
func TestNonFiniteNumericPayloadRequiresReview(t *testing.T) {
	for _, value := range []string{"NaN", "+Inf", "-Inf"} {
		t.Run(value, func(t *testing.T) {
			store := grading.NewMemoryStore()
			store.AddContext("tenant", "segment", paper.Question{
				ID: "question", TenantID: "tenant", ExamID: "exam", QuestionNo: "1", QuestionType: "numeric", Score: 5,
				AnswerKey: &paper.AnswerKey{ID: "key", AnswerVersion: "v1", StandardAnswer: 1.25},
			})
			answer, err := store.RecordAnswer(context.Background(), "tenant", "segment", "actor", grading.RecordAnswerInput{
				Source: "manual_entry", AnswerPayload: map[string]any{"value": value},
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx, err := store.LoadContext(context.Background(), "tenant", "segment")
			if err != nil {
				t.Fatal(err)
			}
			grade, err := grading.NewEngine().Grade(ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("accepted=%q score=%v confidence=%v auto=%v human=%v flags=%v", answer.AnswerText, grade.SuggestedScore, grade.Confidence, grade.AutoPass, grade.NeedsHumanReview, grade.RiskFlags)
			if grade.AutoPass || !grade.NeedsHumanReview {
				t.Error("non-finite numeric payload bypassed numeric parse review")
			}
			if _, err := store.RecordAnswer(context.Background(), "tenant", "segment", "actor", grading.RecordAnswerInput{Source: "manual_entry", AnswerText: value}); err != nil {
				t.Fatal(err)
			}
			plain, err := store.LoadContext(context.Background(), "tenant", "segment")
			if err != nil {
				t.Fatal(err)
			}
			control, err := grading.NewEngine().Grade(plain)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("plain_text_control=%q auto=%v human=%v flags=%v", value, control.AutoPass, control.NeedsHumanReview, control.RiskFlags)
			if control.AutoPass || !control.NeedsHumanReview {
				t.Fatal("plain-text rejection control unexpectedly passed")
			}
		})
	}
}

func TestNumericPayloadAndKeyShareFiniteBoundary(t *testing.T) {
	for _, value := range []any{"NaN", "+Inf", "-Inf", math.NaN(), math.Inf(1), math.Inf(-1)} {
		for _, invalidKey := range []bool{false, true} {
			standard, actual := any(map[string]any{"value": 1.25}), any(1.25)
			if invalidKey {
				standard = map[string]any{"value": value}
			} else {
				actual = value
			}
			ctx := gradingContext("numeric", standard, nil, nil, "", map[string]any{"value": actual})
			grade, err := grading.NewEngine().Grade(ctx)
			if err != nil || grade.AutoPass || !grade.NeedsHumanReview || !contains(grade.RiskFlags, "numeric_parse_failed") {
				t.Fatalf("value=%v invalidKey=%v: grade=%#v err=%v", value, invalidKey, grade, err)
			}
		}
	}
	for _, value := range []any{0.0, "0", -1.25, "-1.25"} {
		ctx := gradingContext("numeric", map[string]any{"value": value}, nil, nil, "", map[string]any{"value": value})
		grade, err := grading.NewEngine().Grade(ctx)
		if err != nil || !grade.AutoPass || grade.NeedsHumanReview || grade.SuggestedScore != grade.MaxScore {
			t.Fatalf("finite numeric control=%v: grade=%#v err=%v", value, grade, err)
		}
	}
}
