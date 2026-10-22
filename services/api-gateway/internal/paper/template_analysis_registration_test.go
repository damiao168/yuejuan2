package paper

import "testing"

func TestTemplateAnalysisRegistrationAllowsAbsentMarkersWithoutBlockingRecognition(t *testing.T) {
	input := TemplatePageAnalysisInput{Width: 1000, Height: 800}
	if validateTemplateAnalysisRegistration(nil, input) != nil {
		t.Fatal("old service should remain compatible")
	}
	value := &TemplateAnalysisRegistration{Version: "four-corner-printed-anchors-v1", Status: "not_found", CoordinateSpace: "source_normalized"}
	if validateTemplateAnalysisRegistration(value, input) != nil {
		t.Fatal("unmarked page is valid")
	}
	value.StableQuad = []TemplateAnalysisPoint{{X: .1, Y: .1}}
	if validateTemplateAnalysisRegistration(value, input) == nil {
		t.Fatal("missing anchors cannot carry an invented stable boundary")
	}
}

func TestTemplateAnalysisFillGroupRetainsSeparateQuestionAssociations(t *testing.T) {
	input := testPageAnalysisInput(t)
	result := testPageAnalysisResult(input)
	result.Candidates = nil
	for i, label := range []string{"12", "13", "14"} {
		row := testPageAnalysisResult(input).Candidates[0]
		row.PrintedQuestionLabel = new(string)
		*row.PrintedQuestionLabel = label
		row.ProposedBBox = TemplateAnalysisBBox{X: .1 + float64(i)*.2, Y: .3, Width: .15, Height: .1}
		result.Candidates = append(result.Candidates, row)
	}
	result.FillGradingGroups = []TemplateAnalysisFillGroup{{GroupID: "measured-fill", QuestionLabels: []string{"12", "13", "14"}, BBox: TemplateAnalysisBBox{X: .08, Y: .25, Width: .7, Height: .2}, ReasonCodes: []string{"LOCAL_FILL_SECTION_VERIFIED"}}}
	if validateTemplateAnalysisFillGroups(result, []string{"12", "13", "14"}) != nil {
		t.Fatal("complete printed group rejected")
	}
	result.FillGradingGroups[0].BBox.Width = .1
	if validateTemplateAnalysisFillGroups(result, []string{"12", "13", "14"}) == nil {
		t.Fatal("shared crop may not clip an individual answer")
	}
}
