package taskmaterials_test

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
	"time"

	"edugrade-enterprise/services/api-gateway/internal/answergroup"
	"edugrade-enterprise/services/api-gateway/internal/capture"
	"edugrade-enterprise/services/api-gateway/internal/processing"
	"edugrade-enterprise/services/api-gateway/internal/taskmaterials"
	"edugrade-enterprise/services/api-gateway/internal/workflowactors"
)

// 脚本驱动检查实际 Store 发出的 SQL、绑定范围与结果；不把无数据库执行宣称为 PostgreSQL 集成测试。
type sqlStep struct {
	required []string
	args     []any
	rows     [][]driver.Value
}
type sqlScript struct {
	t     *testing.T
	steps []sqlStep
	next  int
}
type connector struct{ script *sqlScript }

func (c connector) Connect(context.Context) (driver.Conn, error) { return connection{c.script}, nil }
func (connector) Driver() driver.Driver                          { return fixtureDriver{} }

type fixtureDriver struct{}

func (fixtureDriver) Open(string) (driver.Conn, error) { return nil, errors.New("use connector") }

type connection struct{ script *sqlScript }

func (connection) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unexpected prepare") }
func (connection) Close() error                        { return nil }
func (connection) Begin() (driver.Tx, error)           { return nil, errors.New("unexpected mutation") }
func (c connection) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	s := c.script
	s.t.Helper()
	if s.next >= len(s.steps) {
		s.t.Fatalf("unexpected query: %s", query)
	}
	step := s.steps[s.next]
	s.next++
	normalized := strings.Join(strings.Fields(query), " ")
	for _, part := range step.required {
		if !strings.Contains(normalized, strings.Join(strings.Fields(part), " ")) {
			s.t.Fatalf("SQL lost boundary %q: %s", part, query)
		}
	}
	values := make([]any, len(args))
	for i, value := range args {
		values[i] = value.Value
	}
	if !reflect.DeepEqual(values, step.args) {
		s.t.Fatalf("args=%v want=%v", values, step.args)
	}
	count := 0
	if len(step.rows) > 0 {
		count = len(step.rows[0])
	}
	return &fixtureRows{count: count, values: step.rows}, nil
}

type fixtureRows struct {
	count  int
	values [][]driver.Value
	next   int
}

func (r *fixtureRows) Columns() []string {
	out := make([]string, r.count)
	for i := range out {
		out[i] = fmt.Sprint(i)
	}
	return out
}
func (*fixtureRows) Close() error { return nil }
func (r *fixtureRows) Next(dest []driver.Value) error {
	if r.next >= len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.next])
	r.next++
	return nil
}
func openDB(t *testing.T, steps ...sqlStep) *sql.DB {
	t.Helper()
	script := &sqlScript{t: t, steps: steps}
	db := sql.OpenDB(connector{script})
	t.Cleanup(func() {
		db.Close()
		if script.next != len(steps) {
			t.Errorf("unused SQL steps %d/%d", script.next, len(steps))
		}
	})
	return db
}

const tenant = "00000000-0000-0000-0000-000000000002"
const exam = "00000000-0000-0000-0000-000000000010"
const question = "00000000-0000-0000-0000-000000000011"
const snapshot = "00000000-0000-0000-0000-000000000012"
const group = "00000000-0000-0000-0000-000000000013"
const segment = "00000000-0000-0000-0000-000000000014"
const submission = "00000000-0000-0000-0000-000000000015"

var now = time.Date(2026, 10, 8, 1, 0, 0, 0, time.UTC)

func materialStep() sqlStep {
	return sqlStep{required: []string{"snap.tenant_id=$1::uuid", "snap.exam_id=$2::uuid", "snap.question_id=$3::uuid", "snap.id=$4::uuid", "created_at<=snap.created_at", "'rubric',snap.rubric_snapshot"}, args: []any{tenant, exam, question, snapshot}, rows: [][]driver.Value{{[]byte(`{"id":"` + question + `","exam_id":"` + exam + `","question_no":"1","score":5,"stem":"冻结题干","options":["A.冻结选项"],"answer_key":{"standard_answer":"冻结答案"},"solution":{"raw_text":"冻结解析"},"rubric":{"version":"freeze","deductions":[{"rule":"冻结例外"}]}}`)}}}
}
func TestQuestionMaterialUsesExactTaskSnapshotAndHistoricalReferences(t *testing.T) {
	db := openDB(t, materialStep())
	value, err := (taskmaterials.PostgresReader{DB: db}).QuestionForSnapshot(context.Background(), tenant, exam, question, snapshot)
	if err != nil || value.Stem != "冻结题干" || value.AnswerKey == nil || value.Solution == nil || value.Rubric == nil || value.Options[0] != "A.冻结选项" {
		t.Fatalf("material=%#v %v", value, err)
	}
}
func TestWorkflowDirectoryScopesSchoolActiveStatusAndActualPermission(t *testing.T) {
	db := openDB(t, sqlStep{required: []string{"e.school_id=u.school_id", "e.id=$2::uuid", "u.tenant_id=$1::uuid", "u.status='active'", "r.deleted_at IS NULL", "p.code=$3"}, args: []any{tenant, exam, "review:work"}, rows: [][]driver.Value{{"worker", "teacher-account", "复核教师"}}})
	values, err := workflowactors.List(context.Background(), db, tenant, exam, "review:work")
	if err != nil || len(values) != 1 || values[0].DisplayName != "复核教师" {
		t.Fatalf("actors=%#v %v", values, err)
	}
}
func TestCaptureTargetRequiresExactExamAndRosterOrSubmissionFilter(t *testing.T) {
	db := openDB(t, sqlStep{required: []string{"sub.tenant_id=$1::uuid", "sub.exam_id=$2::uuid", "sub.id::text=$3", "sub.student_id::text=$4", "batch.exam_id=sub.exam_id", "cp.status<>'deleted'"}, args: []any{tenant, exam, submission, "student"}, rows: [][]driver.Value{{"batch", submission, "page", "1008"}}})
	store := capture.NewPostgresStore(db)
	targets, err := store.FindTargets(context.Background(), tenant, exam, submission, "student")
	if err != nil || len(targets) != 1 || targets[0].SubmissionID != submission {
		t.Fatalf("targets=%#v %v", targets, err)
	}
	if _, err = store.FindTargets(context.Background(), tenant, exam, "", ""); !errors.Is(err, capture.ErrInvalidInput) {
		t.Fatalf("unbounded query error=%v", err)
	}
}
func exceptionRow() []driver.Value {
	return []driver.Value{"exception", exam, "page", "submission_page", "page", "BLOCKED_LOW_IMAGE_QUALITY", "P1", true, "open", "", []byte(`{}`), now, now, nil, "", "", ""}
}
func TestExceptionMaterialIsScopedToExceptionPageAndExam(t *testing.T) {
	db := openDB(t, sqlStep{required: []string{"e.tenant_id=$1::uuid", "e.id=$2::uuid"}, args: []any{tenant, "exception"}, rows: [][]driver.Value{exceptionRow()}}, sqlStep{required: []string{"page.id=ex.page_id", "sub.exam_id=ex.exam_id", "sub.deleted_at IS NULL", "ex.tenant_id=$1::uuid", "ex.id=$2::uuid"}, args: []any{tenant, "exception"}, rows: [][]driver.Value{{submission, "1008", int64(2), "batch", "capture-page", "private-asset"}}})
	value, err := processing.NewPostgresStore(db).GetExceptionContext(context.Background(), tenant, "exception")
	if err != nil || value.PageNo != 2 || value.FileAssetID != "private-asset" || value.SubmissionID != submission {
		t.Fatalf("exception material=%#v %v", value, err)
	}
}
func TestExceptionAssignmentChecksEligibilityInSameAtomicUpdate(t *testing.T) {
	db := openDB(t, sqlStep{required: []string{"UPDATE operational_exception", "status IN ('open','assigned')", "AND EXISTS", "e.school_id=u.school_id", "e.id=operational_exception.exam_id", "u.id=$3::uuid", "u.status='active'", "p.code='capture:manage'", "r.deleted_at IS NULL"}, args: []any{tenant, "exception", "worker"}})
	_, err := processing.NewPostgresStore(db).AssignException(context.Background(), tenant, "exception", "manager", processing.AssignInput{AssigneeID: "worker"})
	if !errors.Is(err, processing.ErrNotFound) {
		t.Fatalf("ineligible assignment mutated: %v", err)
	}
}
func groupSteps(hash string) []sqlStep {
	return []sqlStep{
		{required: []string{"FROM answer_group WHERE tenant_id=$1::uuid AND id=$2::uuid"}, args: []any{tenant, group}, rows: [][]driver.Value{{group, tenant, exam, question, snapshot, answergroup.DefaultAlgorithmVersion, (answergroup.DeterministicTextProvider{}).Version(), int64(1), submission, float64(1), "sampling", int64(1), now, now}}},
		{required: []string{"member.tenant_id=$1::uuid", "member.group_id=$2::uuid"}, args: []any{tenant, group}, rows: [][]driver.Value{{submission, segment, float64(1), float64(0), true, false, false, hash, "", "", nil}}},
		{required: []string{"FROM answer_group_decision WHERE tenant_id=$1::uuid AND group_id=$2::uuid"}, args: []any{tenant, group}},
	}
}
func TestGroupMemberMaterialRejectsOutsiderAndChangedRepresentation(t *testing.T) {
	for _, scenario := range []string{"outsider", "changed", "correct"} {
		t.Run(scenario, func(t *testing.T) {
			assertGroupMemberHistoricalMaterialScope(t, scenario)
		})
	}
}

func assertGroupMemberHistoricalMaterialScope(t *testing.T, scenario string) {
	t.Helper()
	provider := answergroup.DeterministicTextProvider{}
	steps := groupSteps(provider.Represent("构建时答案").Hash)
	requested := segment
	if scenario == "outsider" {
		requested = "00000000-0000-0000-0000-000000000099"
	} else {
		text := "构建时答案"
		if scenario == "changed" {
			text = "后来修改答案"
		}
		steps = append(steps, sqlStep{required: []string{"sub.exam_id=$4::uuid", "seg.tenant_id=$1::uuid", "seg.id=$2::uuid", "seg.submission_id=$3::uuid", "seg.question_id=$5::uuid"}, args: []any{tenant, segment, submission, exam, question}, rows: [][]driver.Value{{text}}})
		if scenario == "correct" {
			steps = append(steps, materialStep())
			// 当前材料读取会在历史快照校验后定位单份复核；空结果不代表新建任务，仍须验证完整成员范围。
			steps = append(steps, sqlStep{required: []string{
				"FROM review_task task", "task.tenant_id=$1::uuid", "task.exam_id=$2::uuid", "task.question_id=$3::uuid",
				"task.submission_id=$4::uuid", "task.answer_segment_id=$5::uuid", "task.deleted_at IS NULL",
				"ORDER BY (task.status IN ('pending','assigned','in_progress','returned')) DESC,task.updated_at DESC,task.id DESC LIMIT 1",
			}, args: []any{tenant, exam, question, submission, segment}})
		}
	}
	db := openDB(t, steps...)
	value, err := answergroup.NewPostgresStore(db, provider, answergroup.DefaultPolicy()).GetMemberContext(context.Background(), tenant, group, requested)
	if scenario == "outsider" && !errors.Is(err, answergroup.ErrNotFound) {
		t.Fatalf("outsider err=%v", err)
	}
	if scenario == "changed" && !errors.Is(err, answergroup.ErrRevisionConflict) {
		t.Fatalf("changed err=%v", err)
	}
	if scenario == "correct" && (err != nil || value.AnswerText != "构建时答案" || value.Question.ID != question) {
		t.Fatalf("member material=%#v %v", value, err)
	}
}
