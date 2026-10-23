package capture

import "testing"

func TestCollectionImportDoesNotDependOnGrades(t *testing.T) {
	tests := []struct {
		name, attendance, identity, quality               string
		n, pages, expected, ready, bad, numbers, min, max int
		group                                             bool
		status, code                                      string
	}{
		{name: "complete ungraded", attendance: "expected", identity: "matched", n: 1, pages: 2, expected: 2, ready: 2, numbers: 2, min: 1, max: 2, status: "ready", code: "import_complete"},
		{name: "same number twice", identity: "matched", n: 1, pages: 2, expected: 2, ready: 2, numbers: 1, min: 1, max: 1, status: "needs_attention", code: "missing_pages"},
		{name: "duplicate student", identity: "matched", n: 2, status: "needs_attention", code: "duplicate_submission"},
		{name: "absence with sheet", attendance: "absent", n: 1, status: "needs_attention", code: "absent_has_submission"},
		{name: "explicit absence", attendance: "absent", status: "absent", code: "absent"},
		{name: "missing is not absent", status: "missing", code: "missing_submission"},
		{name: "unidentified ready pixels", identity: "unknown", n: 1, pages: 2, expected: 2, ready: 2, status: "needs_attention", code: "student_unidentified"},
		{name: "page processing", identity: "matched", n: 1, pages: 2, expected: 2, ready: 1, numbers: 2, min: 1, max: 2, status: "processing", code: "page_processing"},
	}
	for _, x := range tests {
		t.Run(x.name, func(t *testing.T) {
			status, code := classifyCollectionEntry(x.attendance, x.n, x.identity, x.quality, x.pages, x.expected, x.ready, x.bad, x.numbers, x.min, x.max, x.group)
			if status != x.status || code != x.code {
				t.Fatalf("got %s/%s want %s/%s", status, code, x.status, x.code)
			}
		})
	}
}
func TestCollectionCountsStudentsSeparatelyFromMaterials(t *testing.T) {
	out := CollectionSummary{ExamStatus: "collecting", PagesPerStudent: 2, Entries: []CollectionEntry{{Status: "ready"}, {Status: "absent"}, {Status: "missing"}}, Issues: []CollectionIssue{{Kind: "submission"}, {Kind: "file"}}}
	finishCollectionSummary(&out)
	if out.RosterCount != 3 || out.RequiredCount != 2 || out.ReadyCount != 1 || out.PendingCount != 1 || out.CanComplete {
		t.Fatalf("incorrect summary: %+v", out)
	}
}
