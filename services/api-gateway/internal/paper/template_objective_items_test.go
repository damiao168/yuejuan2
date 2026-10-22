package paper

import (
	"slices"
	"testing"
)

func testObjectivePageAnalysis(input TemplatePageAnalysisInput) TemplatePageAnalysis {
	out := testPageAnalysisResult(input)
	row := &out.Candidates[0]
	row.Role, row.AssociationStatus, row.PrintedQuestionLabel = "objective_group", "unassociated", nil
	row.ObjectiveItems = []TemplateAnalyzedObjectiveItem{{PrintedQuestionLabel: "1", BBox: TemplateAnalysisBBox{X: .15, Y: .15, Width: .5, Height: .1}, Options: []TemplateAnalyzedObjectiveOption{
		{Label: "A", BBox: TemplateAnalysisBBox{X: .2, Y: .17, Width: .05, Height: .05}},
		{Label: "B", BBox: TemplateAnalysisBBox{X: .3, Y: .17, Width: .05, Height: .05}},
	}}}
	return out
}

func TestTemplateObjectiveItemsSupportDirectQuestionMatching(t *testing.T) {
	input := testPageAnalysisInput(t)
	out := testObjectivePageAnalysis(input)
	defendTemplatePageSemantics(&out, templateAnalysisQuestionLabels{All: []string{"1", "17"}, Objective: []string{"1"}})
	if out.Candidates[0].Role != "objective_group" || out.Candidates[0].SemanticCheck != "supported" || len(out.Candidates[0].ObjectiveItems) != 1 {
		t.Fatalf("valid objective subregions were not retained: %+v", out.Candidates[0])
	}
	if err := validateTemplatePageAnalysisResult(out, input, []string{"1", "17"}); err != nil {
		t.Fatalf("objective subregions rejected: %v", err)
	}
	// 旧工作节点没有子题和选项时仍可显示大区，但不能据此自动补造题目网格。
	out.Candidates[0].ObjectiveItems = nil
	if err := validateTemplatePageAnalysisResult(out, input, []string{"1", "17"}); err != nil {
		t.Fatalf("legacy objective group rejected: %v", err)
	}
}

func TestTemplateObjectiveItemConflictsDowngradeWithoutLosingParentBox(t *testing.T) {
	for _, test := range []struct {
		name string
		edit func(*TemplatePageAnalysis)
	}{
		{"subjective question", func(out *TemplatePageAnalysis) { out.Candidates[0].ObjectiveItems[0].PrintedQuestionLabel = "17" }},
		{"invented question", func(out *TemplatePageAnalysis) { out.Candidates[0].ObjectiveItems[0].PrintedQuestionLabel = "99" }},
		{"item outside parent", func(out *TemplatePageAnalysis) { out.Candidates[0].ObjectiveItems[0].BBox.X = .01 }},
		{"option outside item", func(out *TemplatePageAnalysis) { out.Candidates[0].ObjectiveItems[0].Options[0].BBox.Y = .4 }},
		{"duplicate option", func(out *TemplatePageAnalysis) { out.Candidates[0].ObjectiveItems[0].Options[1].Label = "A" }},
		{"unknown option", func(out *TemplatePageAnalysis) { out.Candidates[0].ObjectiveItems[0].Options[1].Label = "M" }},
		{"missing intermediate option", func(out *TemplatePageAnalysis) { out.Candidates[0].ObjectiveItems[0].Options[1].Label = "C" }},
		{"insufficient options", func(out *TemplatePageAnalysis) {
			out.Candidates[0].ObjectiveItems[0].Options = out.Candidates[0].ObjectiveItems[0].Options[:1]
		}},
		{"overlapping options", func(out *TemplatePageAnalysis) {
			out.Candidates[0].ObjectiveItems[0].Options[1].BBox = out.Candidates[0].ObjectiveItems[0].Options[0].BBox
		}},
		{"duplicate question across groups", func(out *TemplatePageAnalysis) {
			second := out.Candidates[0]
			second.CandidateID = "second-group"
			out.Candidates = append(out.Candidates, second)
		}},
		{"overlapping different questions across groups", func(out *TemplatePageAnalysis) {
			second := out.Candidates[0]
			second.CandidateID = "second-group"
			second.ObjectiveItems = slices.Clone(second.ObjectiveItems)
			second.ObjectiveItems[0].PrintedQuestionLabel = "2"
			out.Candidates = append(out.Candidates, second)
		}},
		{"items on identity", func(out *TemplatePageAnalysis) { out.Candidates[0].Role = "identity" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := testPageAnalysisInput(t)
			out := testObjectivePageAnalysis(input)
			test.edit(&out)
			parent := out.Candidates[0].ProposedBBox
			defendTemplatePageSemantics(&out, templateAnalysisQuestionLabels{All: []string{"1", "2", "17"}, Objective: []string{"1", "2"}})
			for _, row := range out.Candidates {
				if row.Role != "uncertain" || row.SemanticCheck != "needs_review" || len(row.ObjectiveItems) != 0 || !slices.Contains(row.ReasonCodes, "SEMANTIC_OBJECTIVE_ITEMS_CONFLICT") {
					t.Fatalf("unsafe objective items survived: %+v", row)
				}
			}
			if out.Candidates[0].ProposedBBox != parent {
				t.Fatal("objective downgrade changed parent geometry")
			}
			if err := validateTemplatePageAnalysisResult(out, input, []string{"1", "2", "17"}); err != nil {
				t.Fatalf("reviewable parent was rejected: %v", err)
			}
		})
	}
}
