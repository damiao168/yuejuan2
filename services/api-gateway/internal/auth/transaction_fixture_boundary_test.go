package auth

import (
	"database/sql"
	"database/sql/driver"
	"testing"
)

func TestAuthSQLFixtureRejectsQueriesAndWritesOutsideOwningTransaction(t *testing.T) {
	for _, fixture := range []string{"base", "password-change", "session-boundary"} {
		t.Run(fixture, func(t *testing.T) {
			script := &authBatch49Script{
				requireTransaction: true,
				steps: []authBatch49Step{
					{kind: "query", fragment: "SELECT", args: []any{"tenant"}, columns: 1, rows: [][]driver.Value{{"tenant"}}},
					{kind: "exec", fragment: "UPDATE auth_session", args: []any{"tenant"}},
				},
			}
			var connector driver.Connector = authBatch49Connector{script}
			switch fixture {
			case "password-change":
				connector = passwordChangeConnector{authBatch49Connector{script}, 1}
			case "session-boundary":
				connector = sessionBoundaryConnector{authBatch49Connector: authBatch49Connector{script}, affected: 1}
			}
			db := sql.OpenDB(connector)
			db.SetMaxOpenConns(2)
			t.Cleanup(func() { _ = db.Close() })

			// 未开始事务的查询不能消耗步骤，也不能靠随后一次正确查询抹去原错误。
			rows, err := db.QueryContext(t.Context(), "SELECT $1", "tenant")
			if rows != nil {
				_ = rows.Close()
			}
			if err == nil || script.err != err || script.next != 0 {
				t.Fatalf("transaction-free query consumed script or lost failure: next=%d error=%v recorded=%v", script.next, err, script.err)
			}
			firstError := script.err
			tx, err := db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = tx.Rollback() })
			owner := script.transactionOwner

			// 外层事务占着原连接时，db 操作会使用第二条连接。两种操作都必须被拒绝。
			rows, err = db.QueryContext(t.Context(), "SELECT $1", "tenant")
			if rows != nil {
				_ = rows.Close()
			}
			if err != firstError || script.next != 0 || script.transactionOwner != owner {
				t.Fatalf("other connection borrowed transaction: next=%d error=%v", script.next, err)
			}
			var value string
			if err := tx.QueryRowContext(t.Context(), "SELECT $1", "tenant").Scan(&value); err != nil || value != "tenant" {
				t.Fatalf("owning transaction could not read its expected row: value=%q error=%v", value, err)
			}
			if _, err := db.ExecContext(t.Context(), "UPDATE auth_session SET revoked_at=now() WHERE tenant_id=$1", "tenant"); err != firstError || script.next != 1 {
				t.Fatalf("other connection consumed transaction write: next=%d error=%v", script.next, err)
			}
			result, err := tx.ExecContext(t.Context(), "UPDATE auth_session SET revoked_at=now() WHERE tenant_id=$1", "tenant")
			if err != nil {
				t.Fatal(err)
			}
			affected, err := result.RowsAffected()
			if err != nil || affected != 1 {
				t.Fatalf("owning transaction write affected=%d error=%v", affected, err)
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			if script.next != len(script.steps) || script.transactionOwner != nil || script.end != "rollback" ||
				script.opened != script.closed || script.err != firstError {
				t.Fatalf("fixture did not preserve rejection and correct rollback: %+v", script)
			}
		})
	}
}
