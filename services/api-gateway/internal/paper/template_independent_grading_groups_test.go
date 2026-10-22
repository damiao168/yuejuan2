package paper

import "testing"

func independentFillFixture() (TemplateLayout, []Question) {
	layout, questions := fillGradingFixture()
	page := &layout.Pages[0]
	page.GradingGroups = []TemplateGradingGroup{{GroupID: "fill-12-14", QuestionIDs: []string{"q12", "q13", "q14"}, BBox: *page.QuestionRegions[0].GradingBBox}}
	page.QuestionRegions = page.QuestionRegions[:2]
	for index := range page.QuestionRegions {
		page.QuestionRegions[index].GradingGroupID = ""
		page.QuestionRegions[index].GradingBBox = nil
	}
	return layout, questions
}

func TestIndependentFillGroupCoversMissingChildWithoutInventingRegion(t *testing.T) {
	layout, questions := independentFillFixture()
	for _, entireGroup := range []bool{false, true} {
		if entireGroup {
			layout.Pages[0].QuestionRegions = nil
		}
		if err := ValidateTemplateInput("fill", 1, layout); err != nil {
			t.Fatal(err)
		}
		if err := validateTemplateReadyToLock(layout, questions); err != nil {
			t.Fatal(err)
		}
		if !templateCoversReadinessQuestions(&AnswerSheetTemplate{Layout: layout}, questions) {
			t.Fatal("group member missing from readiness")
		}
	}
}

func TestIndependentFillGroupRejectsForeignMembersAndGeometry(t *testing.T) {
	for _, scenario := range []string{"duplicate member", "second group", "cross page member", "cross page group", "different type", "unrelated region", "identity", "missing group", "overlapping groups", "legacy conflict"} {
		t.Run(scenario, func(t *testing.T) {
			layout, questions := independentFillFixture()
			page := &layout.Pages[0]
			switch scenario {
			case "duplicate member":
				page.GradingGroups[0].QuestionIDs[2] = "q12"
			case "second group":
				page.GradingGroups = append(page.GradingGroups, TemplateGradingGroup{GroupID: "other", QuestionIDs: []string{"q14", "q15"}, BBox: TemplateRegionBBox{X: .1, Y: .6, Width: .8, Height: .2}})
			case "cross page member":
				layout.Pages = append(layout.Pages, TemplatePage{PageNo: 2, QuestionRegions: []LayoutRegion{{QuestionID: "q14", X: .1, Y: .2, Width: .2, Height: .1}}})
			case "cross page group":
				layout.Pages = append(layout.Pages, TemplatePage{PageNo: 2, GradingGroups: page.GradingGroups})
			case "different type":
				questions[2].QuestionType = "calculation"
			case "unrelated region":
				page.QuestionRegions = append(page.QuestionRegions, LayoutRegion{QuestionID: "q15", X: .11, Y: .21, Width: .1, Height: .03})
			case "identity":
				page.IdentityRegions = []LayoutRegion{{X: .11, Y: .21, Width: .1, Height: .03}}
			case "missing group":
				page.GradingGroups = nil
			case "overlapping groups":
				page.GradingGroups = append(page.GradingGroups, TemplateGradingGroup{GroupID: "other", QuestionIDs: []string{"q15", "q16"}, BBox: page.GradingGroups[0].BBox})
			case "legacy conflict":
				page.QuestionRegions[0].GradingGroupID = "other"
				page.QuestionRegions[0].GradingBBox = &page.GradingGroups[0].BBox
			}
			if err := validateTemplateReadyToLock(layout, questions); err == nil {
				t.Fatal("invalid independent grading group accepted")
			}
		})
	}
}

func TestIndependentFillGroupLibraryRemapsMissingChildAndFreezesMembers(t *testing.T) {
	layout, questions := independentFillFixture()
	library, err := newLibraryLayout(AnswerSheetTemplate{Name: "fill", PageCount: 1, Layout: layout}, questions)
	if err != nil {
		t.Fatal(err)
	}
	if len(library.Pages[0].QuestionRegions) != 2 || library.Pages[0].GradingGroups[0].QuestionNos[2] != "14" {
		t.Fatal("library invented or lost missing child")
	}
	for index := range questions {
		questions[index].ID = "next-" + questions[index].ID
	}
	reused, err := instantiateLibraryLayout(AnswerSheetTemplateLibraryVersion{Name: "fill", PageCount: 1, Layout: library}, questions)
	if err != nil {
		t.Fatal(err)
	}
	if len(reused.Pages[0].QuestionRegions) != 2 || reused.Pages[0].GradingGroups[0].QuestionIDs[2] != "next-q14" {
		t.Fatal("library member not remapped")
	}
	clone := cloneTemplateLayout(reused)
	clone.Pages[0].GradingGroups[0].QuestionIDs[0] = "changed"
	if reused.Pages[0].GradingGroups[0].QuestionIDs[0] != "next-q12" {
		t.Fatal("mutable group members escaped snapshot")
	}
	before := stableContentHash(readinessPageSnapshot(reused.Pages[0]))
	reused.Pages[0].GradingGroups[0].BBox.Height += .01
	if before == stableContentHash(readinessPageSnapshot(reused.Pages[0])) {
		t.Fatal("group geometry omitted from readiness")
	}
}

func TestIndependentFillGroupAcceptsMatchingLegacyFields(t *testing.T) {
	layout, questions := independentFillFixture()
	for index := range layout.Pages[0].QuestionRegions {
		region := &layout.Pages[0].QuestionRegions[index]
		region.GradingGroupID = "fill-12-14"
		region.GradingBBox = &layout.Pages[0].GradingGroups[0].BBox
	}
	if err := validateTemplateReadyToLock(layout, questions); err != nil {
		t.Fatal(err)
	}
}
