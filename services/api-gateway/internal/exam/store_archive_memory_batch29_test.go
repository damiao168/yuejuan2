package exam

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

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func TestBatch29MemoryListKeepsScopeCursorAndDescendingLimit(t *testing.T) {
	now := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	s := NewMemoryStore()
	for _, seed := range []struct {
		id, tenant, school, status, class string
		at                                time.Time
	}{
		{"new", "tenant", "school", "draft", "class", now.Add(time.Second)},
		{"c", "tenant", "school", "draft", "class", now},
		{"b", "tenant", "school", "draft", "class", now},
		{"a", "tenant", "school", "draft", "class", now.Add(-time.Second)},
		{"other-tenant", "other", "school", "draft", "class", now},
		{"hidden", "tenant", "school", "draft", "hidden", now},
		{"wrong-school", "tenant", "other", "draft", "class", now},
		{"wrong-status", "tenant", "school", "configured", "class", now},
	} {
		s.items[seed.id] = Exam{ID: seed.id, TenantID: seed.tenant, SchoolID: seed.school, Status: seed.status, ClassIDs: []string{seed.class}, CreatedAt: seed.at}
	}
	scope := auth.AccessScope{TenantID: "tenant", ClassIDs: []string{"class"}}
	for _, tc := range []struct {
		name     string
		cursorAt time.Time
		cursorID string
		limit    int
		want     []string
	}{
		{"complete cursor excludes boundary", now, "c", 0, []string{"b", "a"}},
		{"time alone is not a cursor", now, "", 0, []string{"new", "c", "b", "a"}},
		{"id alone is not a cursor", time.Time{}, "c", 0, []string{"new", "c", "b", "a"}},
		{"limit follows descending sort", time.Time{}, "", 2, []string{"new", "c"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			items, err := s.ListExams(context.Background(), scope, ListFilter{SchoolID: "school", Status: "draft", CursorAt: tc.cursorAt, CursorID: tc.cursorID, Limit: tc.limit})
			if err != nil {
				t.Fatal(err)
			}
			ids := []string{}
			for _, item := range items {
				ids = append(ids, item.ID)
			}
			if !reflect.DeepEqual(ids, tc.want) {
				t.Fatalf("ids=%v, want %v", ids, tc.want)
			}
		})
	}
}

func TestBatch29MemoryUpdateRejectsBeforeWritingMergedFields(t *testing.T) {
	for _, tc := range []struct {
		name, status string
		classes      []string
		revision     int64
		want         error
	}{
		{"changed scope", "draft", []string{"hidden"}, 1, ErrScopeForbidden},
		{"stale revision", "draft", []string{"class"}, 2, ErrRevisionConflict},
		{"frozen roster", "ready", []string{"class", "hidden"}, 1, ErrCandidatesFrozen},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := NewMemoryStore()
			scope := auth.AccessScope{TenantID: "tenant", ClassIDs: []string{"class"}}
			before, err := s.CreateExam(context.Background(), scope, "actor", CreateInput{SchoolID: "school", Name: "before", ClassIDs: []string{"class"}})
			if err != nil {
				t.Fatal(err)
			}
			before.Status = tc.status
			s.items[before.ID] = before
			name := "after"
			_, err = s.UpdateExam(context.Background(), scope, before.ID, UpdateInput{Name: &name, ClassIDs: &tc.classes, ExpectedRevision: tc.revision})
			if !errors.Is(err, tc.want) {
				t.Fatalf("update error=%v, want %v", err, tc.want)
			}
			after, err := s.GetExam(context.Background(), scope, before.ID)
			if err != nil || !reflect.DeepEqual(after, before) {
				t.Fatalf("rejected update changed storage: %+v, %v", after, err)
			}
		})
	}
}

func TestBatch29MemoryUpdateKeepsEquivalentFrozenRosterAndCopiesInput(t *testing.T) {
	s := NewMemoryStore()
	scope := auth.AccessScope{TenantID: "tenant", TenantWide: true}
	item, err := s.CreateExam(context.Background(), scope, "actor", CreateInput{SchoolID: "school", Name: "before", ClassIDs: []string{"class"}})
	if err != nil {
		t.Fatal(err)
	}
	item.Status = "ready"
	s.items[item.ID] = item
	name, classes, enabled := "after", []string{"class", "class"}, false
	updated, err := s.UpdateExam(context.Background(), scope, item.ID, UpdateInput{Name: &name, ClassIDs: &classes, AppealEnabled: &enabled, ExpectedRevision: item.Revision})
	if err != nil || updated.Name != name || updated.AppealEnabled || updated.Revision != item.Revision+1 || updated.Status != item.Status {
		t.Fatalf("equivalent roster update: %+v, %v", updated, err)
	}
	classes[0] = "changed-after-call"
	stored, err := s.GetExam(context.Background(), scope, item.ID)
	if err != nil || !reflect.DeepEqual(stored.ClassIDs, []string{"class", "class"}) {
		t.Fatalf("input slice leaked into storage: %+v, %v", stored, err)
	}
}

type examBatch29Step struct {
	kind, fragment string
	columns        int
	rows           [][]driver.Value
	args           map[int]any
	rowsErr        error
}

// 脚本仅保护公开入口的查询顺序、参数和错误传播，不证明真实数据库的锁与并发行为。
type examBatch29Script struct {
	steps          []examBatch29Step
	next           int
	opened, closed int
	end            string
	err            error
}

func (s *examBatch29Script) take(kind, query string, args []driver.NamedValue) (examBatch29Step, error) {
	if s.next >= len(s.steps) {
		s.err = fmt.Errorf("unexpected %s: %s", kind, query)
		return examBatch29Step{}, s.err
	}
	step := s.steps[s.next]
	s.next++
	if kind != step.kind || !strings.Contains(query, step.fragment) {
		s.err = fmt.Errorf("step %d: %s %q, want %s containing %q", s.next, kind, query, step.kind, step.fragment)
		return step, s.err
	}
	for index, value := range step.args {
		if index >= len(args) || !reflect.DeepEqual(args[index].Value, value) {
			s.err = fmt.Errorf("step %d argument %d: %v, want %v", s.next, index, args, value)
			return step, s.err
		}
	}
	return step, nil
}

type examBatch29Connector struct{ script *examBatch29Script }

func (c examBatch29Connector) Connect(context.Context) (driver.Conn, error) {
	return &examBatch29Conn{c.script}, nil
}
func (c examBatch29Connector) Driver() driver.Driver { return c }
func (examBatch29Connector) Open(string) (driver.Conn, error) {
	return nil, errors.New("use test connector")
}

type examBatch29Conn struct{ script *examBatch29Script }

func (*examBatch29Conn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("unexpected prepared statement")
}
func (*examBatch29Conn) Close() error                             { return nil }
func (*examBatch29Conn) CheckNamedValue(*driver.NamedValue) error { return nil }
func (c *examBatch29Conn) Begin() (driver.Tx, error)              { return c, nil }
func (c *examBatch29Conn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) {
	return c, nil
}
func (c *examBatch29Conn) Commit() error   { c.script.end = "commit"; return nil }
func (c *examBatch29Conn) Rollback() error { c.script.end = "rollback"; return nil }
func (c *examBatch29Conn) ExecContext(_ context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	_, err := c.script.take("exec", query, args)
	return driver.RowsAffected(1), err
}
func (c *examBatch29Conn) QueryContext(_ context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	step, err := c.script.take("query", query, args)
	if err != nil {
		return nil, err
	}
	c.script.opened++
	return &examBatch29Rows{script: c.script, step: step}, nil
}

type examBatch29Rows struct {
	script *examBatch29Script
	step   examBatch29Step
	next   int
}

func (r *examBatch29Rows) Columns() []string { return make([]string, r.step.columns) }
func (r *examBatch29Rows) Close() error      { r.script.closed++; return nil }
func (r *examBatch29Rows) Next(dest []driver.Value) error {
	if r.next == len(r.step.rows) {
		if r.step.rowsErr != nil {
			return r.step.rowsErr
		}
		return io.EOF
	}
	copy(dest, r.step.rows[r.next])
	r.next++
	return nil
}

func openExamBatch29Store(t *testing.T, steps []examBatch29Step) (*PostgresStore, *examBatch29Script) {
	t.Helper()
	script := &examBatch29Script{steps: steps}
	db := sql.OpenDB(examBatch29Connector{script})
	t.Cleanup(func() { _ = db.Close() })
	return NewPostgresStore(db), script
}

func assertExamBatch29Script(t *testing.T, script *examBatch29Script, end string) {
	t.Helper()
	if script.err != nil || script.next != len(script.steps) || script.opened != script.closed || script.end != end {
		t.Fatalf("script error=%v steps=%d/%d rows=%d/%d end=%s, want %s", script.err, script.next, len(script.steps), script.closed, script.opened, script.end, end)
	}
}

func examBatch29LoadSteps() []examBatch29Step {
	now := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	return []examBatch29Step{
		{kind: "query", fragment: "FROM exam e WHERE", columns: 23, args: map[int]any{0: "tenant", 5: "exam"}, rows: [][]driver.Value{{
			"exam", "tenant", "school", "current restored exam", "math", "midterm_exam", float64(100),
			"draft", "ai_assisted", true, "manual", "actor", int64(9), now, now,
			"{class}", "", false, true, true, nil, "", "",
		}}},
		{kind: "query", fragment: "count(DISTINCT c.grade_id)", columns: 1, args: map[int]any{0: "tenant", 1: "exam"}, rows: [][]driver.Value{{"grade"}}},
	}
}

func TestBatch29PostgresArchiveCheckReadsAllWindowsAndPropagatesErrors(t *testing.T) {
	rowFailure := errors.New("later window row failed")
	for _, tc := range []struct {
		name    string
		windows [][]driver.Value
		rowsErr error
		wantErr bool
		allowed bool
	}{
		{"disabled window", [][]driver.Value{{[]byte(`{"enabled":false}`)}}, nil, false, true},
		{"open followed by closed", [][]driver.Value{{[]byte(`{"enabled":true}`)}, {[]byte(`{"enabled":false}`)}}, nil, false, false},
		{"open followed by malformed", [][]driver.Value{{[]byte(`{"enabled":true}`)}, {[]byte(`invalid`)}}, nil, true, false},
		{"open followed by row error", [][]driver.Value{{[]byte(`{"enabled":true}`)}}, rowFailure, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			steps := append(examBatch29LoadSteps(),
				examBatch29Step{kind: "query", fragment: "WITH active AS MATERIALIZED", columns: 1, args: map[int]any{0: "tenant"}},
				examBatch29Step{kind: "query", fragment: "EXISTS(SELECT 1 FROM submission", columns: 3, args: map[int]any{0: "tenant", 1: "exam"}, rows: [][]driver.Value{{true, true, false}}},
				examBatch29Step{kind: "query", fragment: "SELECT appeal_window FROM score_release", columns: 1, args: map[int]any{0: "tenant", 1: "exam"}, rows: tc.windows, rowsErr: tc.rowsErr})
			store, script := openExamBatch29Store(t, steps)
			check, err := store.CheckManagementArchive(context.Background(), auth.AccessScope{TenantID: "tenant", TenantWide: true}, "standalone", "exam")
			if (err != nil) != tc.wantErr || check.Allowed != tc.allowed {
				t.Fatalf("archive check=%+v err=%v", check, err)
			}
			end := "commit"
			if tc.wantErr {
				end = "rollback"
			} else if !tc.allowed && (len(check.Blockers) != 1 || check.Blockers[0].Code != "appeal_window_open") {
				t.Fatalf("window blocker=%+v", check.Blockers)
			}
			if tc.rowsErr != nil && !errors.Is(err, tc.rowsErr) {
				t.Fatalf("lost later row error: %v", err)
			}
			assertExamBatch29Script(t, script, end)
		})
	}
}

func TestBatch29PostgresArchiveReplayChecksCurrentPermissionAndReturnsReceipt(t *testing.T) {
	now := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	receipt := ManagementExam{ID: "exam", Kind: "standalone", Name: "original receipt", Revision: 2, ManagementArchiveMetadata: ManagementArchiveMetadata{ArchivedAt: &now}}
	raw, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	input := ManagementArchiveInput{ExpectedRevision: 1, CommandID: "archive-batch29", Reason: "reason"}
	for _, tc := range []struct {
		name     string
		assigned bool
		receipt  []byte
		want     error
	}{
		{"restored current returns original receipt", false, raw, nil},
		{"current permission revoked", true, raw, ErrScopeForbidden},
		{"malformed receipt", false, []byte(`invalid`), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			steps := []examBatch29Step{
				{kind: "exec", fragment: "pg_advisory_xact_lock(hashtextextended", args: map[int]any{0: "tenant:actor:archive-batch29:exam-management"}},
				{kind: "query", fragment: "SELECT request_hash,result_json", columns: 2, args: map[int]any{0: "tenant", 1: "actor", 2: input.CommandID}, rows: [][]driver.Value{{managementArchiveHash("standalone", "exam", "archive", input), tc.receipt}}},
			}
			steps = append(steps, examBatch29LoadSteps()...)
			store, script := openExamBatch29Store(t, steps)
			scope := auth.AccessScope{TenantID: "tenant", TenantWide: !tc.assigned, AssignedOnly: tc.assigned, ExamIDs: []string{"exam"}}
			got, err := store.ArchiveManagementExam(context.Background(), scope, "actor", "standalone", "exam", input)
			end := "rollback"
			if tc.want != nil {
				if !errors.Is(err, tc.want) {
					t.Fatalf("replay error=%v, want %v", err, tc.want)
				}
			} else if tc.name == "malformed receipt" {
				if err == nil {
					t.Fatal("malformed receipt accepted")
				}
			} else {
				end = "commit"
				if err != nil || got.ID != receipt.ID || got.Name != receipt.Name || got.Revision != receipt.Revision || got.ArchivedAt == nil || !got.ArchivedAt.Equal(now) {
					t.Fatalf("replay returned current state instead of receipt: %+v, %v", got, err)
				}
			}
			assertExamBatch29Script(t, script, end)
		})
	}
}
