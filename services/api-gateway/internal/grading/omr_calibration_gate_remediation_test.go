package grading

import (
	"encoding/json"
	"testing"
)

func TestTemplateCalibrationBlocksAllAcceptedBlankAndMultipleChoiceErrors(t *testing.T) {
	for _, index := range []int{2, 3} {
		session, cases := calibrationGateCases()
		baseline := summarizeOMRCalibration(session, cases)
		if !baseline.ReadyToApprove || baseline.EligibleCount != 75 || baseline.MismatchCount != 25 {
			t.Fatalf("balanced evidence must tolerate only review-only errors: %#v", baseline)
		}
		item := &cases[index]
		input, complete := calibrationObservedResult(*item)
		if !complete || !omrResultCanAutoConfirmForQuestion(input, item.QuestionType, true, session.MinimumConfidence) {
			t.Fatal("fixture must exercise a branch actually accepted by production scoring")
		}
		mismatch := false
		item.Matches, item.ExpectedOptions = &mismatch, []string{"A"}
		summary := summarizeOMRCalibration(session, cases)
		if summary.ReadyToApprove || summary.EligibleMismatchCount != 1 || !containsString(summary.Blockers, "eligible_calibration_mismatch_detected") {
			t.Fatalf("accepted %s/%s error must block approval: %#v", item.QuestionType, item.ObservedDecision, summary)
		}
	}
}

func TestTemplateCalibrationUsesFrozenQualityRatherThanSamplingStratum(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*OMRCalibrationCase)
	}{
		{"worker_review", func(c *OMRCalibrationCase) { c.ObservedResultGate["needs_human_review"] = true }},
		{"low_set_confidence", func(c *OMRCalibrationCase) {
			c.ObservedResultGate["thresholds"].(map[string]any)["quality"].(map[string]any)["set_confidence"] = .8
		}},
		{"risk_flags", func(c *OMRCalibrationCase) {
			c.ObservedResultGate["thresholds"].(map[string]any)["quality"].(map[string]any)["risk_flags"] = []string{"uncertain_mark"}
		}},
		{"old_worker_without_quality", func(c *OMRCalibrationCase) { c.ObservedResultGate["thresholds"] = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			session, cases := calibrationGateCases()
			mismatch := false
			cases[3].Matches = &mismatch
			test.edit(&cases[3])
			input, complete := calibrationObservedResult(cases[3])
			if !complete || omrResultCanAutoConfirmForQuestion(input, cases[3].QuestionType, true, session.MinimumConfidence) {
				t.Fatal("control must be review-only under the actual scoring predicate")
			}
			summary := summarizeOMRCalibration(session, cases)
			if !summary.ReadyToApprove || summary.EligibleMismatchCount != 0 {
				t.Fatalf("review-only discrepancy must not invent an automatic-accept error: %#v", summary)
			}
		})
	}
	session, cases := calibrationGateCases()
	cases[2].ObservedResultGate = map[string]any{}
	summary := summarizeOMRCalibration(session, cases)
	if summary.ReadyToApprove || !containsString(summary.Blockers, "calibration_result_gate_missing") {
		t.Fatalf("missing frozen facts must not silently grant approval: %#v", summary)
	}
}

func TestCalibrationGateIsHashedAndNotExposedToBlindLabeling(t *testing.T) {
	session, cases := calibrationGateCases()
	first, err := omrCalibrationEvidenceHash(session, cases)
	if err != nil {
		t.Fatal(err)
	}
	cases[2].ObservedResultGate["needs_human_review"] = true
	second, err := omrCalibrationEvidenceHash(session, cases)
	if err != nil || first == second {
		t.Fatalf("approval hash must bind the eligibility facts: %s, %s, %v", first, second, err)
	}
	raw, err := json.Marshal(cases[2])
	if err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := json.Unmarshal(raw, &response); err != nil {
		t.Fatal(err)
	}
	if _, exposed := response["observed_result_gate"]; exposed {
		t.Fatal("server-only decision facts must not leak through the calibration response")
	}
}

func calibrationGateCases() (OMRCalibrationSession, []OMRCalibrationCase) {
	session := OMRCalibrationSession{ScopeType: "template", Status: "draft", QuestionIDs: []string{"q-single", "q-multiple"},
		OptionLabels: []string{"A", "B"}, MinimumSamples: 100, MinimumSamplesPerOption: 10, MinimumSamplesPerStratum: 10, MinimumConfidence: .98}
	cases := make([]OMRCalibrationCase, 100)
	for index := range cases {
		matched := true
		item := OMRCalibrationCase{QuestionID: "q-single", QuestionType: "single_choice", ObservedDecision: "selected", ObservedOptions: []string{"A"},
			ObservedConfidence: .99, ExpectedOptions: []string{"A"}, Matches: &matched,
			ObservedResultGate: map[string]any{"needs_human_review": false, "thresholds": nil}}
		switch index % 4 {
		case 0:
			item.SampleStratum = "selected_high"
		case 1:
			item.SampleStratum, item.ObservedConfidence, item.ObservedOptions = "selected_low", .8, []string{"B"}
			matched = false
		case 2:
			item.SampleStratum, item.ObservedDecision, item.ObservedOptions, item.ExpectedOptions = "blank", "blank", []string{}, []string{}
			item.ObservedResultGate["thresholds"] = calibrationSetQuality("single_choice", "blank", 0)
		case 3:
			item.QuestionID, item.QuestionType, item.SampleStratum = "q-multiple", "multiple_choice", "ambiguous"
			item.ObservedOptions, item.ExpectedOptions = []string{"A", "B"}, []string{"A", "B"}
			item.ObservedResultGate["thresholds"] = calibrationSetQuality("multiple_choice", "clear", 2)
		}
		cases[index] = item
	}
	return session, cases
}

func calibrationSetQuality(questionType, classification string, selectedCount int) map[string]any {
	return map[string]any{"quality": map[string]any{"version": "omr-set-quality-v1", "question_type": questionType,
		"classification": classification, "set_confidence": .99, "option_count": 2, "selected_count": selectedCount, "risk_flags": []string{}}}
}
