package paper

import "testing"

func TestTemplateOptionCoordinateSpaceValidationAndReuse(t *testing.T) {
	store, scope, source := templateLibraryFixture(t)
	source.Layout.Pages[0].QuestionRegions[0].OptionCoordinateSpace = "question_relative"
	store.templates[source.ID] = source
	published := publishLibraryFixture(t, store, scope, source)
	reused, err := instantiateLibraryLayout(published, []Question{{ID: "new-q", QuestionNo: "17", QuestionType: "calculation"}})
	if err != nil || reused.Pages[0].QuestionRegions[0].OptionCoordinateSpace != "question_relative" {
		t.Fatalf("library reuse lost option coordinate space: %#v, %v", reused, err)
	}
	before := readinessRegionSnapshots(source.Layout.Pages[0].QuestionRegions)
	source.Layout.Pages[0].QuestionRegions[0].OptionCoordinateSpace = ""
	if stableContentHash(before) == stableContentHash(readinessRegionSnapshots(source.Layout.Pages[0].QuestionRegions)) {
		t.Fatal("coordinate interpretation must change the readiness snapshot")
	}
	source.Layout.Pages[0].QuestionRegions[0].OptionCoordinateSpace = "page_relative"
	if err := ValidateTemplateInput(source.Name, source.PageCount, source.Layout); err == nil {
		t.Fatal("unknown option coordinate space accepted")
	}
}

func TestValidateTemplateOptionRegions(t *testing.T) {
	valid := TemplateLayout{Pages: []TemplatePage{{
		PageNo: 1, Width: 1000, Height: 1400,
		QuestionRegions: []LayoutRegion{{
			ID: "q1-region", QuestionID: "question-1", X: 0.1, Y: 0.1, Width: 0.8, Height: 0.2,
			OptionRegions: []OptionRegion{
				{ID: "a", Label: "A", X: 0.05, Y: 0.2, Width: 0.1, Height: 0.5},
				{ID: "b", Label: "B", X: 0.25, Y: 0.2, Width: 0.1, Height: 0.5},
			},
		}},
	}}}
	if err := ValidateTemplateInput("objective sheet", 1, valid); err != nil {
		t.Fatalf("valid option regions rejected: %v", err)
	}

	duplicate := valid
	duplicate.Pages[0].QuestionRegions[0].OptionRegions[1].Label = "a"
	if err := ValidateTemplateInput("objective sheet", 1, duplicate); err == nil {
		t.Fatal("duplicate option labels must be rejected")
	}

	outOfBounds := valid
	outOfBounds.Pages[0].QuestionRegions[0].OptionRegions = []OptionRegion{{ID: "a", Label: "A", X: 0.95, Y: 0, Width: 0.1, Height: 0.2}}
	if err := ValidateTemplateInput("objective sheet", 1, outOfBounds); err == nil {
		t.Fatal("out of bounds option region must be rejected")
	}
}
