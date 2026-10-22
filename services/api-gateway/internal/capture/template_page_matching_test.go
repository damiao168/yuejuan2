package capture

import "testing"

func TestTemplateMatchDoesNotTreatSinglePNGSourceIndexAsAnswerSheetPage(t *testing.T) {
	layout := templateLayout{Pages: []templateLayoutPage{{PageNo: 1}, {PageNo: 2}}}
	if candidates := templateMatchCandidatePages(layout, registrationPageRow{pageNo: 1}); len(candidates) != 2 {
		t.Fatalf("default decoded page 1 must compare both template pages: %#v", candidates)
	}
	for _, pageNo := range []int{1, 2} {
		candidates := templateMatchCandidatePages(layout, registrationPageRow{pageNo: pageNo, explicitPageNo: true})
		if len(candidates) != 1 || candidates[0].PageNo != pageNo {
			t.Fatalf("controlled/manual page %d must keep its explicit number: %#v", pageNo, candidates)
		}
	}
}

func TestTemplateMatchSelectsSecondPageWithinSameTemplateAndRequiresQueuedGeometry(t *testing.T) {
	queued := []map[string]any{
		{"template_id": "template", "template_content_hash": "hash", "template_page_index": 1},
		{"template_id": "template", "template_content_hash": "hash", "template_page_index": 2},
	}
	input := TemplateMatchResultInput{
		SelectedTemplateID: "template", SelectedTemplateContentHash: "hash", Score: 0.96, Margin: 0.2,
		Candidates: []map[string]any{{"template_id": "template", "template_content_hash": "hash", "template_page_index": float64(2), "score": 0.96, "geometry_passed": true}},
	}
	if pageNo := selectedTemplateMatchPage(queued, input); pageNo != 2 {
		t.Fatalf("selected same-template reverse side = %d, want 2", pageNo)
	}
	if pageNo := selectedTemplateMatchPage(queued[:1], input); pageNo != 0 {
		t.Fatalf("worker must not introduce a page outside queued candidates: %d", pageNo)
	}
	input.Candidates[0]["geometry_passed"] = false
	if pageNo := selectedTemplateMatchPage(queued, input); pageNo != 0 {
		t.Fatalf("coarse similarity alone must not assign a page: %d", pageNo)
	}
	input.Candidates[0]["geometry_passed"] = true
	input.Margin = 0.01
	if pageNo := selectedTemplateMatchPage(queued, input); pageNo != 0 {
		t.Fatalf("ambiguous geometry must not assign a page: %d", pageNo)
	}
}

func TestRegistrationPageNumberRequiresExplicitOrCurrentVisualEvidence(t *testing.T) {
	template := registrationQueueTemplate{id: "template", contentHash: "hash"}
	page := registrationPageRow{pageNo: 1, hash: "source"}
	if registrationPageNumberResolved(page, template) {
		t.Fatal("bound template does not prove the decoded page number")
	}
	page.explicitPageNo = true
	if !registrationPageNumberResolved(page, template) {
		t.Fatal("controlled/manual page number must take priority")
	}
	page.explicitPageNo = false
	page.pageNo = 2
	page.identity = registrationPageIdentity{PageNoSource: "visual_template_match", MatchedTemplateID: "template", MatchedTemplateHash: "hash", MatchedSourceHash: "source", MatchedPageNo: 2}
	if !registrationPageNumberResolved(page, template) {
		t.Fatal("current reverse-side visual evidence must allow registration")
	}
	page.hash = "new-source"
	if registrationPageNumberResolved(page, template) {
		t.Fatal("replacing the normalized image must invalidate its visual page evidence")
	}
}
