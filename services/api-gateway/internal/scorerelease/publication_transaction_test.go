package scorerelease

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

type publicationTxProbe struct {
	studentBatch50Script
	committed bool
	aborted   bool
}

type publicationTxConnector struct {
	batch48ReleaseConnector
	probe *publicationTxProbe
}

func (c publicationTxConnector) Connect(context.Context) (driver.Conn, error) {
	return &publicationTxConnection{
		studentBatch50Conn: studentBatch50Conn{script: &c.probe.studentBatch50Script},
		probe:              c.probe,
	}, nil
}

type publicationTxConnection struct {
	studentBatch50Conn
	probe *publicationTxProbe
}

func (c *publicationTxConnection) Begin() (driver.Tx, error) { return c, nil }

func (c *publicationTxConnection) Commit() error {
	c.probe.committed = true
	return nil
}

func (c *publicationTxConnection) Rollback() error {
	c.probe.aborted = true
	return nil
}

func (c *publicationTxConnection) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	// 与现有查询夹具共用一个序列，检查写入出现在预期的读取和锁之后。
	rows, err := c.QueryContext(ctx, query, args)
	if err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return driver.RowsAffected(1), nil
}

func TestPostgresPublishCommitsVersionAndCurrentPointerTogether(t *testing.T) {
	const draftID = "rollback-draft"
	const frozenID = "restore-source"
	const currentID = "current-before-publish"

	now := time.Date(2026, 11, 16, 3, 4, 5, 0, time.UTC)
	gate := emptyGate(now)
	gate.Passed = true
	passedGateJSON, err := json.Marshal(gate)
	if err != nil {
		t.Fatal(err)
	}
	draftRow := []driver.Value{
		draftID, tenant, exam, int64(3), SourceRollback, "restore approved version", StatusDraft, "rollback-key",
		[]byte("{}"), []byte("{}"), []byte("{}"), frozenID, "", actor, now.Add(-time.Hour), "", nil,
	}
	prefix := []studentBatch50Step{
		{fragment: "FOR UPDATE", args: []any{tenant, draftID}, columns: len(draftRow), rows: [][]driver.Value{draftRow}},
		{fragment: "pg_advisory_xact_lock", args: []any{tenant, exam}},
		{fragment: "FROM capture_collection", args: []any{tenant, exam}, columns: 1, rows: [][]driver.Value{{exam}}},
	}
	prefix = append(prefix, sqlGateCounts(false, 0, 0)...)
	prefix = append(prefix,
		studentBatch50Step{fragment: "AND NOT EXISTS (SELECT 1 FROM score_release_question", args: []any{tenant, draftID}, columns: 1, rows: [][]driver.Value{{int64(0)}}},
		studentBatch50Step{fragment: "abs(item.total_score", args: []any{tenant, draftID}, columns: 1, rows: [][]driver.Value{{int64(0)}}},
	)
	publicationWrites := []studentBatch50Step{
		{fragment: "FROM score_release_current", args: []any{tenant, exam}, columns: 1, rows: [][]driver.Value{{currentID}}},
		{fragment: "SET status = 'published'", args: []any{tenant, draftID, actor, now, string(passedGateJSON), currentID}},
		{fragment: "INSERT INTO score_release_current", args: []any{tenant, exam, draftID, now}},
	}

	pointerFailure := errors.New("test current pointer write failure")
	for _, tc := range []struct {
		name       string
		policyErr  error
		pointerErr error
		committed  bool
	}{
		{name: "success switches current version", committed: true},
		{name: "failed pointer rolls back published status", pointerErr: pointerFailure},
		{name: "policy denial commits only blocking evidence", policyErr: ErrGateBlocked, committed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			steps := append([]studentBatch50Step(nil), prefix...)
			if tc.policyErr == nil {
				steps = append(steps, publicationWrites...)
				steps[len(steps)-1].err = tc.pointerErr
			}
			probe := &publicationTxProbe{studentBatch50Script: studentBatch50Script{steps: steps}}
			db := sql.OpenDB(publicationTxConnector{probe: probe})
			t.Cleanup(func() { _ = db.Close() })
			store := NewPostgresStore(db, nil)
			store.now = func() time.Time { return now }

			// 策略检查需要持有发布事务。拒绝时没有发布状态或指针写入，允许保存该事务的阻断证据。
			guardCalls := 0
			got, gotErr := store.PublishGuarded(t.Context(), tenant, draftID, actor, func(_ context.Context, tx *sql.Tx, draft Release, gate Gate) error {
				guardCalls++
				if tx == nil || draft.ID != draftID || !gate.Passed {
					t.Errorf("invalid transaction policy input: draft=%+v gate=%+v", draft, gate)
				}
				return tc.policyErr
			})
			wantErr := tc.pointerErr
			if tc.policyErr != nil {
				wantErr = tc.policyErr
			}
			if wantErr != nil {
				if !errors.Is(gotErr, wantErr) {
					t.Errorf("unexpected publication error: %v", gotErr)
				}
				if got.ID != "" {
					t.Errorf("failed publication returned a version: %+v", got)
				}
			} else if gotErr != nil || got.ID != draftID || got.Status != StatusPublished || got.SupersedesReleaseID != currentID ||
				got.SourceReleaseID != frozenID || got.PublishedAt == nil || !got.PublishedAt.Equal(now) {
				t.Errorf("successful publication returned wrong version: %+v error=%v", got, gotErr)
			}
			if probe.err != nil || probe.next != len(steps) || guardCalls != 1 || probe.committed != tc.committed || probe.aborted == tc.committed {
				t.Errorf("wrong transaction outcome: steps=%d/%d guard=%d committed=%t rolledBack=%t err=%v",
					probe.next, len(steps), guardCalls, probe.committed, probe.aborted, probe.err)
			}
		})
	}
}
