package grading

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"testing"
)

func TestBatch47RuleConfigAllowsOriginalTypesAndMissingOptions(t *testing.T) {
	for _, ruleType := range []string{"single_choice", "true_false", "multiple_choice", "fill_blank", "numeric", "manual"} {
		t.Run(ruleType, func(t *testing.T) {
			if err := validateRuleConfig(ruleType, nil); err != nil {
				t.Fatalf("nil config: %v", err)
			}
			if err := validateRuleConfig(ruleType, map[string]any{"extra": "unrestricted"}); err != nil {
				t.Fatalf("extra option: %v", err)
			}
		})
	}
	for _, ruleType := range []string{"", "unknown", " numeric "} {
		if err := validateRuleConfig(ruleType, nil); err != ErrInvalidInput {
			t.Fatalf("ruleType=%q err=%v, want ErrInvalidInput", ruleType, err)
		}
	}
}

func TestBatch47RuleConfigPreservesNumericConversionsAndBoundaries(t *testing.T) {
	rules := []struct {
		ruleType string
		keys     []string
	}{
		{ruleType: "multiple_choice", keys: []string{"score_per_correct_option", "wrong_option_penalty", "minimum_score"}},
		{ruleType: "numeric", keys: []string{"absolute", "relative"}},
	}
	values := []struct {
		name  string
		value any
		valid bool
	}{
		{name: "zero", value: float64(0), valid: true},
		{name: "float64", value: float64(1.25), valid: true},
		{name: "float32", value: float32(1.5), valid: true},
		{name: "int", value: int(2), valid: true},
		{name: "int64", value: int64(3), valid: true},
		{name: "numeric_string", value: " 4.5 ", valid: true},
		{name: "negative_zero_string", value: "-0", valid: true},
		{name: "negative", value: float64(-1)},
		{name: "negative_string", value: " -1 "},
		{name: "nan", value: math.NaN()},
		{name: "positive_infinity", value: math.Inf(1)},
		{name: "negative_infinity", value: math.Inf(-1)},
		{name: "nan_string", value: "NaN"},
		{name: "infinity_string", value: "+Inf"},
		{name: "unparseable_string", value: "invalid"},
		{name: "nil", value: nil},
		{name: "bool", value: true},
		{name: "unsupported_int32", value: int32(1)},
		{name: "unsupported_json_number", value: json.Number("1")},
	}
	for _, rule := range rules {
		for _, key := range rule.keys {
			for _, value := range values {
				t.Run(rule.ruleType+"/"+key+"/"+value.name, func(t *testing.T) {
					assertRuleNumericBoundary(t, rule.ruleType, key, value.value, value.valid)
				})
			}
		}
	}
}

func TestBatch47RuleConfigIgnoresUnrelatedFieldsWithoutMutation(t *testing.T) {
	for _, ruleType := range []string{"single_choice", "true_false", "fill_blank", "manual"} {
		t.Run(ruleType, func(t *testing.T) {
			config := map[string]any{
				"score_per_correct_option": "invalid", "wrong_option_penalty": -1,
				"minimum_score": false, "absolute": "invalid", "relative": -1,
			}
			before := map[string]any{}
			for key, value := range config {
				before[key] = value
			}
			if err := validateRuleConfig(ruleType, config); err != nil {
				t.Fatalf("unrelated fields rejected: %v", err)
			}
			if !reflect.DeepEqual(config, before) {
				t.Fatalf("config mutated: got=%#v want=%#v", config, before)
			}
		})
	}
	config := map[string]any{"score_per_correct_option": " 1.5 ", "wrong_option_penalty": int64(0), "minimum_score": float32(0), "relative": "ignored"}
	before := map[string]any{}
	for key, value := range config {
		before[key] = value
	}
	if err := validateRuleConfig("multiple_choice", config); err != nil || !reflect.DeepEqual(config, before) {
		t.Fatalf("valid config changed or rejected: config=%#v err=%v", config, err)
	}
	if err := validateRuleConfig("numeric", map[string]any{"absolute": 0, "minimum_score": "ignored"}); err != nil {
		t.Fatalf("numeric rule checked unrelated field: %v", err)
	}
}

func TestBatch47CreateScoringRuleRejectsInvalidConfigBeforeDatabase(t *testing.T) {
	// nil 数据库使任何越过输入校验的查询立即暴露；这里仅验证公开创建入口的首次拒绝边界。
	store := NewPostgresStore(nil)
	cases := []struct {
		name  string
		input CreateScoringRuleInput
	}{
		{name: "unknown_type", input: CreateScoringRuleInput{RuleType: "unknown"}},
		{name: "negative_choice_option", input: CreateScoringRuleInput{RuleType: "multiple_choice", Config: map[string]any{"wrong_option_penalty": -1}}},
		{name: "nonfinite_numeric", input: CreateScoringRuleInput{RuleType: "numeric", Config: map[string]any{"relative": math.Inf(1)}}},
		{name: "unparseable_numeric", input: CreateScoringRuleInput{RuleType: " numeric ", Config: map[string]any{"absolute": "invalid"}}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			rule, err := store.CreateScoringRule(context.Background(), "tenant-1", "question-1", "actor-1", test.input)
			if err != ErrInvalidInput || !reflect.DeepEqual(rule, ScoringRule{}) {
				t.Fatalf("rule=%#v err=%v, want ErrInvalidInput before database", rule, err)
			}
		})
	}
}

func assertRuleNumericBoundary(t *testing.T, ruleType, key string, value any, valid bool) {
	t.Helper()
	err := validateRuleConfig(ruleType, map[string]any{key: value})
	if valid && err != nil {
		t.Fatalf("valid value rejected: %v", err)
	}
	if !valid && err != ErrInvalidInput {
		t.Fatalf("invalid value accepted: %v", err)
	}
}
