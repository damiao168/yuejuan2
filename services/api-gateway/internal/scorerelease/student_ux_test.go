package scorerelease

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func TestStudentResultHTTPKeepsRequiredEmptyQuestionsWithoutReopeningHiddenEvidence(t *testing.T) {
	store := NewMemoryStore()
	service := NewService(store)
	store.SeedFacts(exam, []SubmissionFact{fixtureFact(4)})
	release, err := service.Create(t.Context(), tenant, exam, actor, CreateInput{Reason: "hidden question result", IdempotencyKey: "required-empty-questions", VisibilityPolicy: VisibilityPolicy{}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Publish(t.Context(), tenant, release.ID, actor); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(service, nil)
	request := httptest.NewRequest(http.MethodGet, "/student/result", nil)
	request.SetPathValue("examId", exam)
	request = request.WithContext(auth.WithUser(request.Context(), auth.User{TenantID: tenant, Permissions: []string{"student:grade:read"}, DataScope: map[string]any{"student_id": "student-1"}}))
	recorder := httptest.NewRecorder()
	handler.StudentResult(recorder, request)
	var response struct {
		Result map[string]json.RawMessage `json:"result"`
	}
	if err = json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != http.StatusOK || string(response.Result["questions"]) != "[]" {
		t.Fatalf("required array missing: %d %s", recorder.Code, recorder.Body.String())
	}
	if _, err = service.StudentQuestion(t.Context(), tenant, exam, "student-1", "question-1"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("hidden evidence reopened: %v", err)
	}
}

func TestStudentAppealWindowUsesServerClockAndHalfOpenInterval(t *testing.T) {
	start := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	window := AppealWindow{Enabled: true, OpensAt: &start, ClosesAt: &end, AllowedReasonCodes: []string{"other"}}
	for _, tc := range []struct {
		now   time.Time
		state string
		open  bool
	}{{start.Add(-time.Nanosecond), "upcoming", false}, {start, "open", true}, {end.Add(-time.Nanosecond), "open", true}, {end, "closed", false}} {
		view := appealView(window, tc.now)
		if view.State != tc.state || view.Open != tc.open || view.OpensAt == nil || !view.OpensAt.Equal(start) {
			t.Fatalf("window at %s: %+v", tc.now, view)
		}
	}
	window.Enabled = false
	if view := appealView(window, start); view.State != "disabled" || view.Open || view.OpensAt != nil || view.ClosesAt != nil {
		t.Fatalf("disabled leaked stale schedule: %+v", view)
	}
}

// 脚本行覆盖投影和隐私边界；SQL约束同时检查，但不把它当作真实PostgreSQL查询集成。
type studentSessionUXDriver struct{}
type studentSessionUXConn struct{}
type studentSessionUXRows struct{ index int }

func (studentSessionUXDriver) Open(string) (driver.Conn, error) { return studentSessionUXConn{}, nil }
func (studentSessionUXConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("not supported")
}
func (studentSessionUXConn) Close() error              { return nil }
func (studentSessionUXConn) Begin() (driver.Tx, error) { return nil, errors.New("not supported") }
func (studentSessionUXConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	for _, constraint := range []string{"c.tenant_id=e.tenant_id", "c.student_id=$3::uuid", "mine.student_id=$3::uuid", "WHEN e.id=$2::uuid THEN $4::uuid", "r.status='published'", "target.deleted_at IS NULL"} {
		if !strings.Contains(query, constraint) {
			return nil, errors.New("missing scope: " + constraint)
		}
	}
	if len(args) != 4 || args[0].Value != "tenant" || args[1].Value != "exam" || args[2].Value != "student" || args[3].Value != "frozen-release" {
		return nil, errors.New("lost identity/release parameters")
	}
	return &studentSessionUXRows{}, nil
}
func (*studentSessionUXRows) Columns() []string {
	return []string{"exam", "subject", "name", "score", "max", "published", "visible", "n", "mean"}
}
func (*studentSessionUXRows) Close() error { return nil }
func (r *studentSessionUXRows) Next(dest []driver.Value) error {
	published := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	rows := [][]driver.Value{
		{"math", "mathematics", "数学", 0.0, 100.0, published, true, int64(10), .7},
		{"eng", "english", "英语", 80.0, 100.0, published, true, int64(9), .8},
		{"chi", "chinese", "语文", 70.0, 100.0, published, false, int64(50), .9},
		{"phy", "physics", "物理", nil, nil, nil, true, int64(200), .99},
	}
	if r.index >= len(rows) {
		return io.EOF
	}
	copy(dest, rows[r.index])
	r.index++
	return nil
}
func init() { sql.Register("student-session-ux", studentSessionUXDriver{}) }

func TestStudentSessionListsUnpublishedWithoutLeakingScoresOrSmallCohortMeans(t *testing.T) {
	db, err := sql.Open("student-session-ux", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store := NewPostgresStore(db, nil)
	summary, total, max, err := store.studentSessionSummary(t.Context(), "tenant", "exam", "student", "frozen-release")
	if err != nil {
		t.Fatal(err)
	}
	if summary.ExpectedCount != 4 || summary.PublishedCount != 3 || total != 150 || max != 300 {
		t.Fatalf("partial totals %+v %v/%v", summary, total, max)
	}
	zero, small, hidden, unpublished := summary.Subjects[0], summary.Subjects[1], summary.Subjects[2], summary.Subjects[3]
	if zero.Score == nil || *zero.Score != 0 || !zero.StatisticsAvailable || zero.SampleSize != 10 {
		t.Fatalf("explicit zero lost: %+v", zero)
	}
	if small.MeanScoreRate != nil || small.SampleSize != 0 || small.StatisticsReason != "small_cohort" || small.StatisticsAvailable {
		t.Fatalf("small cohort leak: %+v", small)
	}
	if hidden.MeanScoreRate != nil || hidden.StatisticsReason != "not_public" {
		t.Fatalf("private mean leak: %+v", hidden)
	}
	if unpublished.Score != nil || unpublished.MaxScore != nil || unpublished.PublishedAt != nil || unpublished.MeanScoreRate != nil || unpublished.Status != "unpublished" {
		t.Fatalf("unpublished leak: %+v", unpublished)
	}
}
