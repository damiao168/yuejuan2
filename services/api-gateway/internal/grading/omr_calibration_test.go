package grading

import (
	"errors"
	"reflect"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/paper"
)

func TestSummarizeOMRCalibrationRequiresCompleteBalancedZeroErrorEvidence(t *testing.T) {
	session := OMRCalibrationSession{
		OptionLabels:            []string{"A", "B"},
		MinimumSamples:          100,
		MinimumSamplesPerOption: 10,
		Status:                  "draft",
	}
	cases := make([]OMRCalibrationCase, 100)
	for i := range cases {
		matches := true
		label := "A"
		if i >= 50 {
			label = "B"
		}
		cases[i] = OMRCalibrationCase{
			ID:              string(rune(i + 1)),
			ExpectedOptions: []string{label},
			Matches:         &matches,
		}
	}

	summary := summarizeOMRCalibration(session, cases)
	if !summary.ReadyToApprove || summary.MatchCount != 100 || summary.OptionCoverage["A"] != 50 || summary.OptionCoverage["B"] != 50 {
		t.Fatalf("complete balanced exact evidence must be approvable: %#v", summary)
	}

	mismatch := false
	cases[0].Matches = &mismatch
	summary = summarizeOMRCalibration(session, cases)
	if summary.ReadyToApprove || summary.MismatchCount != 1 || !containsString(summary.Blockers, "calibration_mismatch_detected") {
		t.Fatalf("one disagreement must block approval: %#v", summary)
	}

	cases[0].Matches = nil
	cases[0].ExpectedOptions = nil
	summary = summarizeOMRCalibration(session, cases)
	if summary.ReadyToApprove || summary.PendingCount != 1 || !containsString(summary.Blockers, "calibration_labels_pending") {
		t.Fatalf("an unlabeled sample must block approval: %#v", summary)
	}

	matched := true
	cases[0].Matches = &matched
	cases[0].ExpectedOptions = []string{"A"}
	for i := range cases {
		cases[i].ExpectedOptions = []string{"A"}
	}
	summary = summarizeOMRCalibration(session, cases)
	if summary.ReadyToApprove || summary.OptionCoverage["B"] != 0 || !containsString(summary.Blockers, "option_coverage_incomplete") {
		t.Fatalf("unrepresented option must block approval: %#v", summary)
	}
}

func TestSummarizeTemplateOMRCalibrationRequiresStrataAndOnlyBlocksEligibleErrors(t *testing.T) {
	session := OMRCalibrationSession{
		ScopeType:                "template",
		QuestionIDs:              []string{"q1", "q2"},
		OptionLabels:             []string{"A", "B"},
		MinimumSamples:           100,
		MinimumSamplesPerOption:  10,
		MinimumSamplesPerStratum: 10,
		Status:                   "draft",
	}
	strata := []string{"selected_high", "selected_low", "blank", "ambiguous"}
	cases := make([]OMRCalibrationCase, 100)
	for index := range cases {
		matches := index%4 == 0
		label := "A"
		if index%2 == 1 {
			label = "B"
		}
		expected := []string{label}
		if strata[index%4] == "blank" {
			expected = []string{}
			matches = true
		}
		cases[index] = OMRCalibrationCase{
			ID:              string(rune(index + 1)),
			QuestionID:      session.QuestionIDs[index%2],
			SampleStratum:   strata[index%4],
			ExpectedOptions: expected,
			Matches:         &matches,
		}
	}
	summary := summarizeOMRCalibration(session, cases)
	if !summary.ReadyToApprove || summary.MismatchCount == 0 || summary.EligibleMismatchCount != 0 {
		t.Fatalf("errors outside the auto-confirm branch must remain review-only without blocking approval: %#v", summary)
	}

	mismatch := false
	cases[0].Matches = &mismatch
	summary = summarizeOMRCalibration(session, cases)
	if summary.ReadyToApprove || summary.EligibleMismatchCount != 1 || !containsString(summary.Blockers, "eligible_calibration_mismatch_detected") {
		t.Fatalf("a high-confidence candidate error must block template approval: %#v", summary)
	}

	cases[0].Matches = nil
	cases[0].ExpectedOptions = nil
	for index := range cases {
		if cases[index].SampleStratum == "ambiguous" {
			cases[index].SampleStratum = "selected_low"
		}
	}
	summary = summarizeOMRCalibration(session, cases)
	if summary.ReadyToApprove || !containsString(summary.Blockers, "stratum_coverage_incomplete") {
		t.Fatalf("missing a result stratum must block template approval: %#v", summary)
	}
}

func TestSummarizeOMRCalibrationKeepsPendingCoverageAndBlockerOrder(t *testing.T) {
	session := OMRCalibrationSession{
		ScopeType:                "template",
		QuestionIDs:              []string{"q1", "q2"},
		OptionLabels:             []string{"A", "B"},
		MinimumSamples:           4,
		MinimumSamplesPerOption:  2,
		MinimumSamplesPerStratum: 1,
		Status:                   "approved",
	}
	mismatch := false
	// 标签和比对结果只要缺少一个就仍待标注，不能提前贡献选项覆盖或错例计数。
	cases := []OMRCalibrationCase{
		{QuestionID: "q1", SampleStratum: "selected_high", ExpectedOptions: []string{"B"}},
		{QuestionID: "q1", SampleStratum: "selected_low", Matches: &mismatch},
		{QuestionID: "q1", SampleStratum: "selected_high", ExpectedOptions: []string{"A"}, Matches: &mismatch},
	}
	summary := summarizeOMRCalibration(session, cases)
	if summary.TotalCount != 3 || summary.PendingCount != 2 || summary.LabeledCount != 1 || summary.EligibleCount != 2 || summary.EligibleMismatchCount != 1 || summary.MismatchCount != 1 {
		t.Fatalf("pending samples must retain coverage without contributing comparison counts: %#v", summary)
	}
	if summary.QuestionCoverage["q1"] != 3 || summary.StratumCoverage["selected_high"] != 2 || summary.StratumCoverage["selected_low"] != 1 || summary.OptionCoverage["A"] != 1 || summary.OptionCoverage["B"] != 0 {
		t.Fatalf("pending samples must not contribute option coverage: %#v", summary)
	}
	wantBlockers := []string{
		"sample_count_below_minimum",
		"calibration_labels_pending",
		"eligible_calibration_mismatch_detected",
		"option_coverage_incomplete",
		"question_coverage_incomplete",
		"stratum_coverage_incomplete",
		"calibration_not_draft",
	}
	if summary.ReadyToApprove || !reflect.DeepEqual(summary.Blockers, wantBlockers) {
		t.Fatalf("approval blockers must preserve their order: got %v, want %v", summary.Blockers, wantBlockers)
	}
}

func TestCalibrationQuestionsForLayoutKeepsFirstOMRRegionOrder(t *testing.T) {
	metadata := map[string]calibrationQuestion{
		"q1": {ID: "q1", No: "1", Type: "single_choice"},
		"q2": {ID: "q2", No: "2", Type: "multiple_choice"},
		"q3": {ID: "q3", No: "3", Type: "true_false"},
		"q4": {ID: "q4", No: "4", Type: "essay"},
	}
	// 页码和数据库顺序不能重排题集；重复题目只采用模板中第一个题区的选项。
	layout := paper.TemplateLayout{Pages: []paper.TemplatePage{
		{PageNo: 9, QuestionRegions: []paper.LayoutRegion{
			{QuestionID: "q2", OptionRegions: []paper.OptionRegion{{Label: " C "}, {Label: "A"}}},
			{QuestionID: "missing"},
			{QuestionID: "q4"},
			{QuestionID: "q1", OptionRegions: []paper.OptionRegion{{Label: "A"}, {Label: "B"}}},
		}},
		{PageNo: 1, QuestionRegions: []paper.LayoutRegion{
			{QuestionID: "q2"},
			{QuestionID: "q3", OptionRegions: []paper.OptionRegion{{Label: "T"}, {Label: "F"}}},
		}},
	}}
	want := []calibrationQuestion{
		{ID: "q2", No: "2", Type: "multiple_choice", OptionLabels: []string{"C", "A"}},
		{ID: "q1", No: "1", Type: "single_choice", OptionLabels: []string{"A", "B"}},
		{ID: "q3", No: "3", Type: "true_false", OptionLabels: []string{"T", "F"}},
	}
	got, err := calibrationQuestionsForLayout(layout, metadata)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("calibration questions must follow eligible first regions: got %#v, err %v", got, err)
	}
}

func TestCalibrationQuestionsForLayoutRejectsInvalidOptionLabels(t *testing.T) {
	metadata := map[string]calibrationQuestion{"q1": {ID: "q1", Type: "single_choice"}}
	tests := []struct {
		name    string
		options []paper.OptionRegion
	}{
		{name: "no options"},
		{name: "only one option", options: []paper.OptionRegion{{Label: "A"}}},
		{name: "empty after trimming", options: []paper.OptionRegion{{Label: "A"}, {Label: " "}}},
		{name: "duplicate after trimming", options: []paper.OptionRegion{{Label: " A "}, {Label: "A"}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			layout := paper.TemplateLayout{Pages: []paper.TemplatePage{{QuestionRegions: []paper.LayoutRegion{{QuestionID: "q1", OptionRegions: test.options}}}}}
			got, err := calibrationQuestionsForLayout(layout, metadata)
			if !errors.Is(err, ErrInvalidInput) || got != nil {
				t.Fatalf("invalid options must reject the entire question set: got %#v, err %v", got, err)
			}
		})
	}
}

func TestDecorateOMRCalibrationDetailKeepsWorkerResultBlindUntilLabeled(t *testing.T) {
	detail := OMRCalibrationDetail{Cases: []OMRCalibrationCase{{
		AnswerSegmentID:    "segment-1",
		SampleStratum:      "selected_high",
		ObservedDecision:   "selected",
		ObservedOptions:    []string{"A"},
		ObservedConfidence: 0.99,
		Measurements:       []map[string]any{{"option": "A"}},
	}}}
	blind := decorateOMRCalibrationDetail(detail)
	if blind.Cases[0].ObservedDecision != "" || blind.Cases[0].ObservedOptions != nil || blind.Cases[0].Measurements != nil || blind.Cases[0].SampleStratum != "" {
		t.Fatalf("unlabeled calibration response leaked the worker result: %#v", blind.Cases[0])
	}
	if blind.Cases[0].SegmentImageURL == "" {
		t.Fatal("blind calibration response must retain the protected image route")
	}

	matched := true
	revealed := decorateOMRCalibrationDetail(OMRCalibrationDetail{Cases: []OMRCalibrationCase{{
		AnswerSegmentID:    "segment-1",
		SampleStratum:      "selected_high",
		ObservedDecision:   "selected",
		ObservedOptions:    []string{"A"},
		ObservedConfidence: 0.99,
		Measurements:       []map[string]any{{"option": "A"}},
		Matches:            &matched,
	}}})
	if revealed.Cases[0].ObservedDecision != "selected" || len(revealed.Cases[0].ObservedOptions) != 1 || revealed.Cases[0].SampleStratum != "selected_high" {
		t.Fatalf("labeled calibration response must reveal the frozen comparison: %#v", revealed.Cases[0])
	}
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
