package capture

import (
	"context"
	"database/sql/driver"
	"errors"
	"testing"
	"time"
)

func matchingReadQueries() []printLedgerQuery {
	now := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	page := func(id, submission string, pageNo int64) []driver.Value {
		return []driver.Value{id, "tenant", "batch", "file", pageNo, submission, "submission-page-" + id,
			pageNo, pageNo, int64(0), "asset-" + id, "", "template", "sheet", "normalized", "", int64(3),
			[]byte(`{"quality_status":"passed"}`), []byte(`[]`), []byte(`{}`), now}
	}
	return []printLedgerQuery{
		{fragments: []string{"FROM capture_batch WHERE tenant_id=$1 AND id=$2::uuid AND deleted_at IS NULL"},
			args: []driver.Value{"tenant", "batch"}, columns: 1, values: [][]driver.Value{{"exam"}}},
		{fragments: []string{"FROM exam_candidate_snapshot", "WHERE tenant_id=$1 AND exam_id=$2::uuid", "ORDER BY class_name_snapshot,student_no_snapshot"},
			args: []driver.Value{"tenant", "exam"}, columns: 5, values: [][]driver.Value{{"student", "001", "Synthetic student", "class", "Synthetic class"}}},
		{fragments: []string{"FROM capture_page cp JOIN submission s ON s.tenant_id=cp.tenant_id", "WHERE cp.tenant_id=$1 AND cp.capture_batch_id=$2::uuid AND cp.deleted_at IS NULL AND s.deleted_at IS NULL"},
			args: []driver.Value{"tenant", "batch"}, columns: 6, values: [][]driver.Value{
				{"submission-a", "student", "001", "matched", int64(4), []byte(`{"method":"manual_confirmation","reason":"checked roster"}`)},
				{"submission-b", "", "", "unassigned", int64(1), []byte(`null`)},
			}},
		{fragments: []string{"FROM capture_page WHERE tenant_id=$1 AND capture_batch_id=$2::uuid AND submission_id=$3::uuid AND deleted_at IS NULL ORDER BY sequence_no"},
			args: []driver.Value{"tenant", "batch", "submission-a"}, columns: 21, values: [][]driver.Value{page("page-a1", "submission-a", 1), page("page-a2", "submission-a", 2)}},
		{fragments: []string{"FROM capture_page WHERE tenant_id=$1 AND capture_batch_id=$2::uuid AND submission_id=$3::uuid AND deleted_at IS NULL ORDER BY sequence_no"},
			args: []driver.Value{"tenant", "batch", "submission-b"}, columns: 21, values: [][]driver.Value{page("page-b1", "submission-b", 1)}},
	}
}

func TestMatchingQueueReadKeepsFrozenRosterAndSeparateSubmissionPages(t *testing.T) {
	store := printLedgerStore(t, matchingReadQueries())
	// 子页查询也应能复用唯一连接，不能持有父游标后再等待自己归还连接。
	store.db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	queue, err := store.GetMatchingQueue(ctx, "tenant", "batch")
	if err != nil || queue.ExamID != "exam" || queue.BatchID != "batch" || len(queue.Candidates) != 1 || len(queue.Submissions) != 2 {
		t.Fatalf("incomplete matching queue: %+v err=%v", queue, err)
	}
	candidate := queue.Candidates[0]
	if candidate.ID != "student" || candidate.StudentNo != "001" || candidate.ClassID != "class" || candidate.ClassName != "Synthetic class" {
		t.Fatalf("frozen roster identity changed: %+v", candidate)
	}
	first, second := queue.Submissions[0], queue.Submissions[1]
	if first.StudentID != candidate.ID || first.CandidateNo != candidate.StudentNo || first.IdentityRevision != 4 ||
		first.IdentityEvidence["reason"] != "checked roster" || len(first.Pages) != 2 ||
		first.Pages[0].ID != "page-a1" || first.Pages[1].ID != "page-a2" || first.Pages[1].AssignedPageNo != 2 ||
		first.Pages[0].PageIdentity["quality_status"] != "passed" {
		t.Fatalf("matched submission lost identity evidence or ordered pages: %+v", first)
	}
	if second.StudentID != "" || second.IdentityStatus != "unassigned" || second.IdentityEvidence == nil || len(second.IdentityEvidence) != 0 ||
		len(second.Pages) != 1 || second.Pages[0].ID != "page-b1" || second.Pages[0].SubmissionID != second.ID {
		t.Fatalf("unassigned submission inherited another student's facts: %+v", second)
	}
}

func TestMatchingQueueReadRejectsIncompleteEvidenceWithoutPartialQueue(t *testing.T) {
	streamFailure := errors.New("synthetic matching stream interrupted")
	for _, scenario := range []struct {
		name   string
		change func([]printLedgerQuery) []printLedgerQuery
		want   error
	}{
		{"batch outside tenant", func(q []printLedgerQuery) []printLedgerQuery { q[0].values = nil; return q[:1] }, ErrNotFound},
		{"frozen roster interrupted", func(q []printLedgerQuery) []printLedgerQuery { q[1].readErr = streamFailure; return q[:2] }, streamFailure},
		{"submission evidence interrupted", func(q []printLedgerQuery) []printLedgerQuery { q[2].readErr = streamFailure; return q[:3] }, streamFailure},
		{"invalid identity evidence", func(q []printLedgerQuery) []printLedgerQuery { q[2].values[1][5] = []byte(`{`); return q[:3] }, nil},
		{"second submission page query fails", func(q []printLedgerQuery) []printLedgerQuery { q[4].queryErr = streamFailure; return q }, streamFailure},
		{"second submission page stream fails", func(q []printLedgerQuery) []printLedgerQuery { q[4].readErr = streamFailure; return q }, streamFailure},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			store := printLedgerStore(t, scenario.change(matchingReadQueries()))
			queue, err := store.GetMatchingQueue(t.Context(), "tenant", "batch")
			if err == nil || scenario.want != nil && !errors.Is(err, scenario.want) || queue.ExamID != "" || len(queue.Submissions) != 0 || len(queue.Candidates) != 0 {
				t.Fatalf("failed matching read returned a partial queue: %+v err=%v want=%v", queue, err, scenario.want)
			}
		})
	}
}

func processingSummaryQuery(predicate, resource string, rows [][]driver.Value) printLedgerQuery {
	return printLedgerQuery{
		fragments: []string{"WHERE cp.tenant_id=$1 AND " + predicate, "r.processing_status<>'invalidated' AND r.deleted_at IS NULL",
			"a.registration_run_id=pr.id", "cp.status<>'deleted' AND cp.deleted_at IS NULL", "ORDER BY cp.submission_id, cp.sequence_no, cp.id"},
		args: []driver.Value{"tenant", resource}, columns: 9, values: rows,
	}
}

func TestProcessingSummaryReadExposesCurrentQualityRegistrationAndCropBlockers(t *testing.T) {
	query := processingSummaryQuery("cp.capture_batch_id=$2::uuid", "batch", [][]driver.Value{
		{"submission-a", "ready", int64(1), "ready", "passed", "run-ready", "completed", "matched", int64(1)},
		{"submission-a", "quality", int64(2), "needs_review", "review", "run-quality", "completed", "matched", int64(1)},
		{"submission-a", "terminal", int64(3), "normalized", "passed", "run-terminal", "terminal_error", "", int64(0)},
		{"submission-a", "confidence", int64(4), "normalized", "passed", "run-confidence", "completed", "needs_review", int64(1)},
		{"submission-a", "missing-crop", int64(5), "normalized", "passed", "run-empty", "completed", "matched", int64(0)},
		{"submission-a", "pending", int64(6), "normalized", "passed", "run-pending", "processing", "", int64(0)},
		{"submission-b", "other-ready", int64(1), "ready", "passed", "run-other-ready", "completed", "matched", int64(1)},
	})
	store := printLedgerStore(t, []printLedgerQuery{query})
	items, err := store.ListProcessingSummaries(t.Context(), "tenant", "batch")
	if err != nil || len(items) != 2 {
		t.Fatalf("unexpected processing summary: %+v err=%v", items, err)
	}
	first, second := items[0], items[1]
	if first.SubmissionID != "submission-a" || first.CanComplete || first.TotalPages != 6 || first.ReadyPages != 1 || first.BlockedPages != 4 || first.PendingPages != 1 {
		t.Fatalf("processing readiness did not reflect all current pages: %+v", first)
	}
	want := []ProcessingBlocker{
		{PageID: "quality", PageNo: 2, RegistrationRunID: "run-quality", Stage: "quality", Code: "quality_review", Action: "review_quality"},
		{PageID: "terminal", PageNo: 3, RegistrationRunID: "run-terminal", Stage: "registration", Code: "registration_failed", Action: "retry_registration"},
		{PageID: "confidence", PageNo: 4, RegistrationRunID: "run-confidence", Stage: "registration", Code: "low_confidence", Action: "confirm_registration"},
		{PageID: "missing-crop", PageNo: 5, RegistrationRunID: "run-empty", Stage: "segmentation", Code: "segments_missing", Action: "retry_registration"},
		{PageID: "pending", PageNo: 6, RegistrationRunID: "run-pending", Stage: "processing", Code: "pending", Action: "wait"},
	}
	if len(first.Blockers) != len(want) {
		t.Fatalf("missing processing blocker: %+v", first.Blockers)
	}
	for i, expected := range want {
		if first.Blockers[i] != expected {
			t.Errorf("blocker %d=%+v want=%+v", i, first.Blockers[i], expected)
		}
	}
	if second.SubmissionID != "submission-b" || !second.CanComplete || second.ReadyPages != 1 || second.TotalPages != 1 || len(second.Blockers) != 0 {
		t.Fatalf("completed submission inherited another submission's blockers: %+v", second)
	}
}

func TestProcessingSummaryReadDoesNotTreatMissingOrInterruptedRowsAsComplete(t *testing.T) {
	failure := errors.New("synthetic processing summary interrupted")
	for _, scenario := range []struct {
		name   string
		change func(*printLedgerQuery)
		want   error
	}{
		{"submission missing", func(q *printLedgerQuery) { q.values = nil }, ErrNotFound},
		{"query failed", func(q *printLedgerQuery) { q.queryErr = failure }, failure},
		{"late row stream failed", func(q *printLedgerQuery) { q.readErr = failure }, failure},
		{"invalid page number", func(q *printLedgerQuery) { q.values[0][2] = "invalid-number" }, nil},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			query := processingSummaryQuery("cp.submission_id=$2::uuid", "submission", [][]driver.Value{
				{"submission", "ready", int64(1), "ready", "passed", "run", "completed", "matched", int64(1)},
			})
			scenario.change(&query)
			store := printLedgerStore(t, []printLedgerQuery{query})
			result, err := store.GetProcessingSummary(t.Context(), "tenant", "submission")
			if err == nil || scenario.want != nil && !errors.Is(err, scenario.want) || result.CanComplete || result.SubmissionID != "" || len(result.Blockers) != 0 {
				t.Fatalf("failed summary became a complete submission: %+v err=%v want=%v", result, err, scenario.want)
			}
		})
	}
}
