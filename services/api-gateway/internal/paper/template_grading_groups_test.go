package paper

import (
	"math"
	"testing"
)

func fillGradingFixture() (TemplateLayout, []Question) {
	box := TemplateRegionBBox{X: .1, Y: .2, Width: .8, Height: .3}
	regions := []LayoutRegion{}
	questions := []Question{}
	for index, id := range []string{"q12", "q13", "q14"} {
		copy := box
		regions = append(regions, LayoutRegion{ID: id, QuestionID: id, X: .15 + float64(index)*.23, Y: .3, Width: .2, Height: .1, GradingGroupID: "fill-12-14", GradingBBox: &copy, Origin: "model", ReviewState: "suggested", SemanticCheck: "supported"})
		questions = append(questions, Question{ID: id, QuestionNo: id[1:], QuestionType: "fill_blank", Status: "active"})
	}
	return TemplateLayout{Pages: []TemplatePage{{PageNo: 1, Width: 300, Height: 200, QuestionRegions: regions}}}, questions
}

func TestFillGradingGroupDoesNotRequireAnotherRecognitionReview(t *testing.T) {
	layout, questions := fillGradingFixture()
	if err := ValidateTemplateInput("fill", 1, layout); err != nil {
		t.Fatal(err)
	}
	if err := validateTemplateReadyToLock(layout, questions); err != nil {
		t.Fatalf("valid common grading area is not a recognition conflict: %v", err)
	}
	for index := range layout.Pages[0].QuestionRegions {
		layout.Pages[0].QuestionRegions[index].GradingGroupID = ""
		layout.Pages[0].QuestionRegions[index].GradingBBox = nil
	}
	if err := validateTemplateReadyToLock(layout, questions); err != nil {
		t.Fatalf("individual grading rejected: %v", err)
	}
}

func TestFillGradingGroupRejectsInconsistentOrForeignGeometry(t *testing.T) {
	for _, scenario := range []string{"missing box", "missing id", "mismatched box", "outside child", "singleton", "identity", "unrelated question", "different type", "nan", "cross page"} {
		t.Run(scenario, func(t *testing.T) {
			layout, questions := fillGradingFixture()
			regions := layout.Pages[0].QuestionRegions
			switch scenario {
			case "missing box":
				regions[0].GradingBBox = nil
			case "missing id":
				regions[0].GradingGroupID = ""
			case "mismatched box":
				regions[0].GradingBBox.X = .09
			case "outside child":
				regions[0].GradingBBox.Width = .05
			case "singleton":
				layout.Pages[0].QuestionRegions = regions[:1]
			case "identity":
				layout.Pages[0].IdentityRegions = []LayoutRegion{{ID: "name", X: .11, Y: .21, Width: .1, Height: .03}}
			case "unrelated question":
				layout.Pages[0].QuestionRegions = append(regions, LayoutRegion{QuestionID: "q15", X: .11, Y: .21, Width: .1, Height: .03})
			case "different type":
				questions[0].QuestionType = "calculation"
			case "nan":
				regions[0].GradingBBox.X = math.NaN()
			case "cross page":
				second := layout.Pages[0]
				second.PageNo = 2
				layout.Pages = append(layout.Pages, second)
			}
			if err := validateTemplateGradingGroups(layout, questions); err == nil {
				t.Fatal("invalid grading group accepted")
			}
		})
	}
}

func TestFillGradingGroupingSurvivesLibraryReuseAndSnapshotIsolation(t *testing.T) {
	layout, questions := fillGradingFixture()
	source := AnswerSheetTemplate{Name: "fill", PageCount: 1, Layout: layout}
	library, err := newLibraryLayout(source, questions)
	if err != nil {
		t.Fatal(err)
	}
	for index := range questions {
		questions[index].ID = "next-" + questions[index].ID
	}
	reused, err := instantiateLibraryLayout(AnswerSheetTemplateLibraryVersion{Name: "fill", PageCount: 1, Layout: library}, questions)
	if err != nil {
		t.Fatal(err)
	}
	if reused.Pages[0].QuestionRegions[0].QuestionID != "next-q12" || reused.Pages[0].QuestionRegions[0].GradingBBox == nil {
		t.Fatal("library lost grouping or did not remap children")
	}
	copy := cloneTemplateLayout(reused)
	copy.Pages[0].QuestionRegions[0].GradingBBox.X = .9
	if reused.Pages[0].QuestionRegions[0].GradingBBox.X != .1 {
		t.Fatal("shared mutable bbox escaped snapshot")
	}
	before := stableContentHash(readinessRegionSnapshots(reused.Pages[0].QuestionRegions))
	for index := range reused.Pages[0].QuestionRegions {
		reused.Pages[0].QuestionRegions[index].GradingGroupID = ""
		reused.Pages[0].QuestionRegions[index].GradingBBox = nil
	}
	if before == stableContentHash(readinessRegionSnapshots(reused.Pages[0].QuestionRegions)) {
		t.Fatal("grading mode changed without invalidating readiness")
	}
}
