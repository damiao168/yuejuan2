package score

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRosterAttendanceUsesSubmissionFactsAndIgnoresHistoricalManualMarks(t *testing.T) {
	store := NewMemoryStore()
	for _, student := range []string{"missing", "matched", "bad-pages"} {
		store.AddRosterStudent(RosterSeed{ExamID: "exam", StudentID: student})
	}
	// 历史人工状态保留在原记录中，但既不能把收到的答卷隐藏为缺考，也不能让无答卷者继续等待人工确认。
	store.attendance[key("exam", "missing")] = RosterEntry{Status: "expected", AttendanceReason: "old expected"}
	store.attendance[key("exam", "matched")] = RosterEntry{Status: "absent", AttendanceReason: "old absent"}
	store.attendance[key("exam", "bad-pages")] = RosterEntry{Status: "absent"}
	store.AddRosterSubmission(RosterSubmissionSeed{ExamID: "exam", StudentID: "matched", SubmissionID: "matched-submission", ExpectedPages: 1, ActualPages: 1, QualityStatus: "passed"})
	store.AddRosterSubmission(RosterSubmissionSeed{ExamID: "exam", StudentID: "bad-pages", SubmissionID: "bad-submission", ExpectedPages: 2, ActualPages: 0, QualityStatus: "failed", SubmissionStatus: "rejected"})
	report, err := store.ListRoster(t.Context(), "tenant", "exam")
	if err != nil {
		t.Fatal(err)
	}
	byStudent := map[string]RosterEntry{}
	for _, entry := range report.Entries {
		byStudent[entry.StudentID] = entry
		if entry.AttendanceReason != "" || entry.MarkedBy != "" || entry.MarkedAt != nil {
			t.Fatal("historical manual marks leaked into current derived attendance")
		}
	}
	if report.Summary.Absent != 1 || byStudent["missing"].Status != "absent" || byStudent["matched"].Status != "submitted" || byStudent["bad-pages"].Status != "missing_pages" {
		t.Fatalf("derived attendance=%#v", report)
	}
	issues := appendRosterQualityIssues(nil, report)
	if !hasIssue(issues, "missing_pages_unresolved") || hasIssue(issues, "missing_submission_unresolved") {
		t.Fatalf("received bad pages must remain blocking: %#v", issues)
	}
	if len(store.attendance) != 3 {
		t.Fatal("current attendance calculation deleted historical marks")
	}
	if _, err := store.SetAttendance(t.Context(), "tenant", "exam", "matched", "admin", AttendanceInput{Status: "absent"}); !errors.Is(err, ErrManualAttendanceRetired) {
		t.Fatalf("retired manual write error=%v", err)
	}
	for _, identity := range []string{"unknown", "conflict"} {
		store.rosterSubmissions[0].IdentityStatus = identity
		report, err := store.ListRoster(t.Context(), "tenant", "exam")
		if err != nil || report.Summary.Absent != 1 || report.Summary.Unidentified != 1 || !hasIssue(appendRosterQualityIssues(nil, report), "unidentified_submission") {
			t.Fatalf("received unresolved identity hidden as absence=%#v %v", report, err)
		}
	}
}

func TestManualAttendanceHandlerCannotReachAnyStore(t *testing.T) {
	response := httptest.NewRecorder()
	NewHandler(nil, nil).SetAttendance(response, httptest.NewRequest(http.MethodPut, "/old-attendance", nil))
	if response.Code != http.StatusGone {
		t.Fatalf("retired handler status=%d", response.Code)
	}
}
