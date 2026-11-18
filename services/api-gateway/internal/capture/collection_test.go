package capture

import "testing"

func TestCollectionImportDoesNotDependOnGrades(t *testing.T) {
	tests := []struct {
		name, identity, quality                           string
		n, pages, expected, ready, bad, numbers, min, max int
		group                                             bool
		status, code                                      string
	}{
		{name: "complete ungraded", identity: "matched", n: 1, pages: 2, expected: 2, ready: 2, numbers: 2, min: 1, max: 2, status: "ready", code: "import_complete"},
		{name: "same number twice", identity: "matched", n: 1, pages: 2, expected: 2, ready: 2, numbers: 1, min: 1, max: 1, status: "needs_attention", code: "missing_pages"},
		{name: "duplicate student", identity: "matched", n: 2, status: "needs_attention", code: "duplicate_submission"},
		{name: "linked sheet with pending identity is not absent", n: 1, status: "needs_attention", code: "student_unidentified"},
		{name: "no linked submission is automatically absent", status: "absent", code: "absent"},
		{name: "failed sheet is not absent", identity: "matched", quality: "failed", n: 1, pages: 2, expected: 2, numbers: 2, min: 1, max: 2, ready: 2, status: "needs_attention", code: "page_needs_review"},
		{name: "unidentified ready pixels", identity: "unknown", n: 1, pages: 2, expected: 2, ready: 2, status: "needs_attention", code: "student_unidentified"},
		{name: "page processing", identity: "matched", n: 1, pages: 2, expected: 2, ready: 1, numbers: 2, min: 1, max: 2, status: "processing", code: "page_processing"},
	}
	for _, x := range tests {
		t.Run(x.name, func(t *testing.T) {
			status, code := classifyCollectionEntry(CollectionEntry{ActualPages: x.pages, ExpectedPages: x.expected}, collectionEntryFacts{submissions: x.n, identity: x.identity, quality: x.quality, ready: x.ready, bad: x.bad, numbers: x.numbers, minNo: x.min, maxNo: x.max, groupBad: x.group})
			if status != x.status || code != x.code {
				t.Fatalf("got %s/%s want %s/%s", status, code, x.status, x.code)
			}
		})
	}
}
func TestCollectionCountsStudentsSeparatelyFromMaterials(t *testing.T) {
	out := CollectionSummary{ExamStatus: "collecting", PagesPerStudent: 2, Entries: []CollectionEntry{{Status: "ready"}, {Status: "absent"}, {Status: "needs_attention"}}, Issues: []CollectionIssue{{Kind: "submission"}, {Kind: "file"}}}
	finishCollectionSummary(&out)
	if out.RosterCount != 3 || out.RequiredCount != 2 || out.ReadyCount != 1 || out.PendingCount != 1 || out.CanComplete {
		t.Fatalf("incorrect summary: %+v", out)
	}
}
