package appeal

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
)

// 脚本驱动验证拒绝分派时没有状态或事件写入，并核对实际发出的查询包含来源与租户边界。
// 它不替代真实 PostgreSQL 对 SQL 执行结果和锁竞争的集成验收。
func TestPublishedQuestionPostgresAssignmentRejectsOriginalReviewerBeforeAnyWrite(t *testing.T) {
	item := publishedQuestionPostgresAppealFixture()
	scenario := &publishedQuestionPostgresScenario{queries: []publishedQuestionPostgresQuery{
		{fragments: []string{"FOR UPDATE", "tenant_id = $1 AND id = $2::uuid"}, args: []any{item.TenantID, item.ID}, row: publishedQuestionPostgresAppealRow(item)},
		{fragments: []string{"user_row.tenant_id = $1", "user_row.id = $2::uuid", "user_row.status = 'active'", "permission_row.code = 'appeal:work'"}, args: []any{item.TenantID, "original-reviewer"}, row: []driver.Value{true}},
		{fragments: []string{"grade.tenant_id = human.tenant_id", "grade.answer_segment_id = human.answer_segment_id", "grade.tenant_id = $1", "grade.id = $2::uuid", "human.reviewer_id = $3::uuid", "grade.deleted_at IS NULL", "human.deleted_at IS NULL"}, args: []any{item.TenantID, item.SourceFinalGradeID, "original-reviewer"}, row: []driver.Value{true}},
	}}
	db := sql.OpenDB(publishedQuestionPostgresConnector{scenario})
	defer db.Close()
	store := NewPublishedQuestionAppealPostgresStore(db)
	if _, err := store.StartPublishedQuestionAppealReview(context.Background(), item.TenantID, item.ID, "manager-1", StartQuestionAppealReviewInput{AssignedTo: "original-reviewer", ExpectedRevision: item.Revision}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("original reviewer assignment: %v", err)
	}
	if len(scenario.queries) != 0 || !scenario.rolledBack || scenario.committed {
		t.Fatalf("assignment query/transaction sequence: %+v", scenario)
	}
}

func TestPublishedQuestionPostgresRegradeScopeRequiresOriginalStudentItem(t *testing.T) {
	item := publishedQuestionPostgresAppealFixture()
	scenario := &publishedQuestionPostgresScenario{queries: []publishedQuestionPostgresQuery{{
		fragments: []string{
			"regrade.tenant_id = job.tenant_id AND regrade.job_id = job.id", "job.tenant_id = $1", "job.id = $2::uuid", "job.exam_id = $3::uuid",
			"job.question_id = $4::uuid", "job.source_release_id = $5::uuid", "job.status <> 'cancelled'", "regrade.status <> 'cancelled'",
			"regrade.submission_id = $6::uuid", "regrade.old_final_grade_id = $7::uuid",
		},
		args: []any{item.TenantID, "regrade-1", item.ExamID, item.QuestionID, item.SourceReleaseID, item.SubmissionID, item.SourceFinalGradeID},
		row:  []driver.Value{false},
	}}}
	db := sql.OpenDB(publishedQuestionPostgresConnector{scenario})
	defer db.Close()
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	matched, err := matchingAppealRegradeJob(context.Background(), tx, item.TenantID, item, "regrade-1")
	if err != nil || matched || len(scenario.queries) != 0 {
		t.Fatalf("matching regrade scope: %t %v, remaining=%d", matched, err, len(scenario.queries))
	}
}

func TestPublishedQuestionPostgresResolutionChecksPublishedRegradeOutcome(t *testing.T) {
	item := publishedQuestionPostgresAppealFixture()
	item.Status, item.RegradeJobID = QuestionAppealUpheldPendingRegrade, "regrade-1"
	scenario := &publishedQuestionPostgresScenario{queries: []publishedQuestionPostgresQuery{
		{fragments: []string{"FOR UPDATE"}, args: []any{item.TenantID, item.ID}, row: publishedQuestionPostgresAppealRow(item)},
		{
			fragments: []string{
				"successor.tenant_id = $1", "successor.id = $2::uuid", "successor.exam_id = $3::uuid", "successor.status = 'published'", "successor.version > $4",
				"successor_item.student_id = $5::uuid", "successor_item.submission_id = $6::uuid", "successor_question.question_id = $7::uuid",
				"job.tenant_id = successor.tenant_id", "job.id = $8::uuid", "job.exam_id = successor.exam_id", "job.question_id = successor_question.question_id",
				"job.source_release_id = successor.source_release_id", "regrade.tenant_id = job.tenant_id", "regrade.job_id = job.id",
				"successor.source = 'regrade'", "successor.source_release_id = $9::uuid", "job.status = 'ready_for_release'", "job.finalized_at IS NOT NULL",
				"successor.published_at >= job.finalized_at", "regrade.status = 'resolved'", "regrade.old_final_grade_id = $10::uuid",
				"successor_question.final_grade_id = regrade.old_final_grade_id", "successor_question.score = COALESCE(regrade.reviewed_score, regrade.old_score)",
				"successor_question.max_score = regrade.max_score", "successor_question.source_type = 'single_review'", "successor_question.source_id IS NOT DISTINCT FROM regrade.reviewed_grade_id",
			},
			args: []any{item.TenantID, "release-2", item.ExamID, int64(item.SourceReleaseVersion), item.StudentID, item.SubmissionID, item.QuestionID, item.RegradeJobID, item.SourceReleaseID, item.SourceFinalGradeID},
			row:  []driver.Value{false},
		},
	}}
	db := sql.OpenDB(publishedQuestionPostgresConnector{scenario})
	defer db.Close()
	store := NewPublishedQuestionAppealPostgresStore(db)
	if _, err := store.ResolvePublishedQuestionAppeal(context.Background(), item.TenantID, item.ID, "manager-1", ResolveQuestionAppealInput{NewReleaseID: "release-2", ExpectedRevision: item.Revision}); !errors.Is(err, ErrResolutionRelease) {
		t.Fatalf("unmatched successor release: %v", err)
	}
	if len(scenario.queries) != 0 || !scenario.rolledBack || scenario.committed {
		t.Fatalf("resolution query/transaction sequence: %+v", scenario)
	}
}

func publishedQuestionPostgresAppealFixture() PublishedQuestionAppeal {
	now := time.Date(2026, 10, 8, 9, 0, 0, 0, time.UTC)
	return PublishedQuestionAppeal{
		ID: "appeal-1", TenantID: "tenant-1", ExamID: "exam-1", StudentID: "student-1", SubmissionID: "submission-1",
		SourceReleaseID: "release-1", SourceReleaseVersion: 1, QuestionID: "question-1", QuestionNo: "Q1",
		SourceFinalGradeID: "grade-original", SourceScore: 4, SourceMaxScore: 8, ReasonCode: ReasonMissingStepCredit, Reason: "过程分遗漏",
		Status: QuestionAppealSubmitted, CreatedBy: "student-user-1", CreatedAt: now, UpdatedAt: now, Revision: 1,
	}
}

func publishedQuestionPostgresAppealRow(item PublishedQuestionAppeal) []driver.Value {
	var opinion driver.Value
	if item.ReviewOpinion != nil {
		encoded, _ := json.Marshal(item.ReviewOpinion)
		opinion = encoded
	}
	return []driver.Value{
		item.ID, item.TenantID, item.ExamID, item.StudentID, item.SubmissionID,
		item.SourceReleaseID, int64(item.SourceReleaseVersion), item.QuestionID, item.QuestionNo,
		item.SourceFinalGradeID, item.SourceScore, item.SourceMaxScore, item.ReasonCode, item.Reason, []byte("{}"),
		item.Status, item.AssignedTo, item.Decision, item.PublicResponse, item.PrivateNote,
		item.RegradeJobID, item.NewReleaseID, item.CreatedBy, item.DecidedBy, nil,
		item.CreatedAt, item.UpdatedAt, item.Revision, int64(item.NewReleaseVersion),
		nil, nil, opinion,
	}
}

type publishedQuestionPostgresQuery struct {
	fragments []string
	args      []any
	row       []driver.Value
}

type publishedQuestionPostgresScenario struct {
	queries               []publishedQuestionPostgresQuery
	execs                 []publishedQuestionPostgresExec
	allowCommit           bool
	rolledBack, committed bool
}

type publishedQuestionPostgresExec struct {
	fragments []string
	args      []any
	err       error
}

type publishedQuestionPostgresConnector struct {
	scenario *publishedQuestionPostgresScenario
}

func (c publishedQuestionPostgresConnector) Connect(context.Context) (driver.Conn, error) {
	return &publishedQuestionPostgresConn{c.scenario}, nil
}

func (c publishedQuestionPostgresConnector) Driver() driver.Driver {
	return publishedQuestionPostgresDriver{c.scenario}
}

type publishedQuestionPostgresDriver struct {
	scenario *publishedQuestionPostgresScenario
}

func (d publishedQuestionPostgresDriver) Open(string) (driver.Conn, error) {
	return &publishedQuestionPostgresConn{d.scenario}, nil
}

type publishedQuestionPostgresConn struct {
	scenario *publishedQuestionPostgresScenario
}

func (*publishedQuestionPostgresConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepared statement or write")
}

func (*publishedQuestionPostgresConn) Close() error { return nil }

func (c *publishedQuestionPostgresConn) Begin() (driver.Tx, error) {
	return publishedQuestionPostgresTx{c.scenario}, nil
}

func (c *publishedQuestionPostgresConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	if len(c.scenario.queries) == 0 {
		return nil, errors.New("unexpected query or appeal update")
	}
	expected := c.scenario.queries[0]
	for _, fragment := range expected.fragments {
		if !strings.Contains(query, fragment) {
			return nil, fmt.Errorf("missing scope or evidence predicate: %s", fragment)
		}
	}
	actual := make([]any, len(args))
	for i, arg := range args {
		actual[i] = arg.Value
	}
	if !reflect.DeepEqual(actual, expected.args) {
		return nil, fmt.Errorf("unexpected scope args: got %v want %v", actual, expected.args)
	}
	c.scenario.queries = c.scenario.queries[1:]
	return &publishedQuestionPostgresRows{values: expected.row}, nil
}

func (c *publishedQuestionPostgresConn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	if len(c.scenario.execs) == 0 {
		return nil, errors.New("unexpected write outside appeal opinion event")
	}
	expected := c.scenario.execs[0]
	for _, fragment := range expected.fragments {
		if !strings.Contains(query, fragment) {
			return nil, fmt.Errorf("missing opinion write predicate: %s", fragment)
		}
	}
	actual := make([]any, len(args))
	for i, arg := range args {
		actual[i] = arg.Value
	}
	if !reflect.DeepEqual(actual, expected.args) {
		return nil, fmt.Errorf("unexpected event payload: got %v want %v", actual, expected.args)
	}
	c.scenario.execs = c.scenario.execs[1:]
	if expected.err != nil {
		return nil, expected.err
	}
	return driver.RowsAffected(1), nil
}

type publishedQuestionPostgresTx struct {
	scenario *publishedQuestionPostgresScenario
}

func (tx publishedQuestionPostgresTx) Commit() error {
	tx.scenario.committed = true
	if tx.scenario.allowCommit {
		return nil
	}
	return errors.New("rejected operation must not commit")
}

func (tx publishedQuestionPostgresTx) Rollback() error {
	tx.scenario.rolledBack = true
	return nil
}

type publishedQuestionPostgresRows struct {
	values []driver.Value
	done   bool
}

func (r *publishedQuestionPostgresRows) Columns() []string {
	columns := make([]string, len(r.values))
	for i := range columns {
		columns[i] = fmt.Sprintf("column_%d", i)
	}
	return columns
}

func (*publishedQuestionPostgresRows) Close() error { return nil }

func (r *publishedQuestionPostgresRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	copy(dest, r.values)
	r.done = true
	return nil
}
