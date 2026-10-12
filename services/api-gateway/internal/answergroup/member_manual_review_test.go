package answergroup

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
)

// 只核对真实方法发出的查询范围、无任务语义和响应扫描；驱动未连接实际 PostgreSQL。
func TestMemberManualReviewProjectsActualScopedTaskOrNoTask(t *testing.T) {
	for _, exists := range []bool{false, true} {
		t.Run(fmt.Sprint(exists), func(t *testing.T) {
			db := sql.OpenDB(memberReviewConnector{exists})
			defer db.Close()
			store := &PostgresStore{db: db}
			task, err := store.memberManualReview(t.Context(), "tenant", Group{ExamID: "exam", QuestionID: "question"}, Member{SubmissionID: "submission", SegmentID: "segment"})
			if err != nil {
				t.Fatal(err)
			}
			if exists && (task == nil || task.TaskID != "real-task" || task.Status != "in_progress" || !task.Assigned) {
				t.Fatalf("wrong task: %+v", task)
			}
			if !exists && task != nil {
				t.Fatalf("no task was invented: %+v", task)
			}
		})
	}
}

type memberReviewConnector struct{ exists bool }

func (c memberReviewConnector) Connect(context.Context) (driver.Conn, error) {
	return memberReviewConn(c), nil
}
func (c memberReviewConnector) Driver() driver.Driver { return memberReviewDriver(c) }

type memberReviewDriver struct{ exists bool }

func (d memberReviewDriver) Open(string) (driver.Conn, error) { return memberReviewConn(d), nil }

type memberReviewConn struct{ exists bool }

func (memberReviewConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected write")
}
func (memberReviewConn) Close() error              { return nil }
func (memberReviewConn) Begin() (driver.Tx, error) { return nil, errors.New("unexpected write") }
func (c memberReviewConn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	actual := make([]any, len(args))
	for i := range args {
		actual[i] = args[i].Value
	}
	if !reflect.DeepEqual(actual, []any{"tenant", "exam", "question", "submission", "segment"}) {
		return nil, fmt.Errorf("wrong scope args %v", actual)
	}
	for _, scope := range []string{"task.tenant_id=$1::uuid", "task.exam_id=$2::uuid", "task.question_id=$3::uuid", "task.submission_id=$4::uuid", "task.answer_segment_id=$5::uuid", "task.deleted_at IS NULL", "ORDER BY (task.status IN ('pending','assigned','in_progress','returned')) DESC"} {
		if !strings.Contains(query, scope) {
			return nil, fmt.Errorf("missing manual task boundary %s", scope)
		}
	}
	return &memberReviewRows{exists: c.exists}, nil
}

type memberReviewRows struct{ exists, done bool }

func (*memberReviewRows) Columns() []string { return []string{"id", "status", "assigned"} }
func (*memberReviewRows) Close() error      { return nil }
func (r *memberReviewRows) Next(dest []driver.Value) error {
	if !r.exists || r.done {
		return io.EOF
	}
	copy(dest, []driver.Value{"real-task", "in_progress", true})
	r.done = true
	return nil
}
