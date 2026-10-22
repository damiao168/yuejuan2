package review

import "testing"

func TestImagePanelPrefillRequiresAggregateButPreservesRoleEvidence(t *testing.T) {
	for _, test := range []struct {
		name     string
		panel    any
		expected bool
	}{
		{"partial failure", map[string]any{"decision": "teacher_review"}, false},
		{"valid zero", map[string]any{"final_score": float64(0)}, true},
		{"valid positive", map[string]any{"final_score": float64(8)}, true},
		{"malformed aggregate", map[string]any{"final_score": "8"}, false},
		{"malformed panel", nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			raw := map[string]any{"schema_version": "grading-image-v3", "panel_execution": test.panel}
			suggestion := map[string]any{"suggested_score": float64(9), "raw_output": raw, "evidence": []any{map[string]any{"score": float64(2)}}}
			result := aiSecondOpinionFromSuggestion(suggestion, true)
			if result == nil || result.ScorePrefillAllowed != test.expected {
				t.Fatalf("unexpected prefill state: %#v", result)
			}
			if _, ok := result.Metadata["evidence"]; !ok {
				t.Fatal("review-only aggregate must retain scoring evidence")
			}
			if suggestion["suggested_score"] != float64(9) {
				t.Fatal("read projection changed saved candidate")
			}
		})
	}
	legacy := aiSecondOpinionFromSuggestion(map[string]any{"raw_output": map[string]any{"schema_version": "grading-image-v3"}}, true)
	if !legacy.ScorePrefillAllowed {
		t.Fatal("historical single-model suggestion unexpectedly blocked")
	}
}
