package scorerelease

import (
	"database/sql"
	"database/sql/driver"
	"testing"
)

func TestPublicationSQLFixtureRejectsQueriesOutsideOwningTransaction(t *testing.T) {
	for _, tc := range []struct {
		name   string
		active bool
		write  bool
	}{
		{name: "query before transaction"},
		{name: "write before transaction", write: true},
		{name: "query on another connection", active: true},
		{name: "write on another connection", active: true, write: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probe := &publicationTxProbe{studentBatch50Script: studentBatch50Script{steps: []studentBatch50Step{
				{fragment: "SELECT $1", args: []any{"tenant"}, columns: 1, rows: [][]driver.Value{{int64(1)}}},
				{fragment: "UPDATE score_release", args: []any{"tenant", "release"}},
			}}}
			db := sql.OpenDB(publicationTxConnector{probe: probe})
			t.Cleanup(func() { _ = db.Close() })
			var tx *sql.Tx
			var err error
			if tc.active {
				tx, err = db.BeginTx(t.Context(), nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
			}
			// 有发布事务时，db 会另取一条连接。它不能消费原事务的预期 SQL。
			if tc.write {
				_, err = db.ExecContext(t.Context(), "UPDATE score_release", "tenant", "release")
			} else {
				var rows *sql.Rows
				rows, err = db.QueryContext(t.Context(), "SELECT $1", "tenant")
				if rows != nil {
					rows.Close()
					t.Fatal("outside query exposed rows")
				}
			}
			if err == nil || probe.err != err || probe.next != 0 || probe.committed || probe.aborted {
				t.Fatalf("outside operation consumed publication SQL: error=%v probe=%+v", err, probe)
			}
			protocolErr := probe.err
			if !tc.active {
				tx, err = db.BeginTx(t.Context(), nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
			}
			var value int
			if err := tx.QueryRowContext(t.Context(), "SELECT $1", "tenant").Scan(&value); err != nil || value != 1 {
				t.Fatalf("owning transaction could not read its original step: value=%d error=%v", value, err)
			}
			if _, err := tx.ExecContext(t.Context(), "UPDATE score_release", "tenant", "release"); err != nil {
				t.Fatalf("owning transaction could not write its original step: %v", err)
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			if probe.next != len(probe.steps) || !probe.aborted || probe.committed || probe.err != protocolErr {
				t.Fatalf("owning rollback lost the earlier protocol error: %+v", probe)
			}
		})
	}
}

func TestPublicationSQLFixtureRejectsInvalidTransactionCompletion(t *testing.T) {
	for _, tc := range []struct {
		name     string
		begin    bool
		firstEnd string
		finish   string
	}{
		{name: "commit without begin", finish: "commit"},
		{name: "rollback without begin", finish: "rollback"},
		{name: "second commit", begin: true, firstEnd: "commit", finish: "commit"},
		{name: "rollback after commit", begin: true, firstEnd: "commit", finish: "rollback"},
		{name: "commit after rollback", begin: true, firstEnd: "rollback", finish: "commit"},
		{name: "second rollback", begin: true, firstEnd: "rollback", finish: "rollback"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			probe := &publicationTxProbe{}
			connection := &publicationTxConnection{probe: probe}
			if tc.begin {
				if _, err := connection.Begin(); err != nil {
					t.Fatal(err)
				}
			}
			finish := func(kind string) error {
				if kind == "commit" {
					return connection.Commit()
				}
				return connection.Rollback()
			}
			if tc.firstEnd != "" {
				if err := finish(tc.firstEnd); err != nil {
					t.Fatal(err)
				}
			}
			err := finish(tc.finish)
			if err == nil || probe.err != err || connection.activeTx || probe.next != 0 ||
				probe.committed != (tc.firstEnd == "commit") || probe.aborted != (tc.firstEnd == "rollback") {
				t.Fatalf("invalid transaction completion was accepted: error=%v probe=%+v", err, probe)
			}
		})
	}
}
