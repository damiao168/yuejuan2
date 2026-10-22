package paper

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// 独立 schema 仅验证确认记录读取 SQL，事务回滚不会修改已有业务表。
func TestReadinessConfirmationFactsPostgres(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is required for confirmation facts test")
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			t.Fatalf("execute fixture SQL: %v", err)
		}
	}
	schema := "readiness_facts_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	exec(fmt.Sprintf(`CREATE SCHEMA %s; SET LOCAL search_path TO %s,pg_catalog`, schema, schema))
	exec(`CREATE TABLE exam_readiness_snapshot (id uuid,tenant_id uuid,exam_id uuid,configuration_hash text,status text,confirmed_at timestamptz,confirmed_by uuid,import_snapshot_json jsonb)`)
	tenantID, examID, snapshotID, adminID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	confirmedAt := time.Date(2026, 10, 20, 12, 52, 51, 0, time.UTC)
	insert := `INSERT INTO exam_readiness_snapshot VALUES ($1::uuid,$2::uuid,$3::uuid,'previous','passed',$4,$5::uuid,'{}')`
	exec(insert, snapshotID, tenantID, examID, confirmedAt, adminID)
	// 更晚的其他租户、其他考试和失效记录都不能冒充本考试的有效历史确认。
	exec(insert, uuid.NewString(), uuid.NewString(), examID, confirmedAt.Add(time.Hour), adminID)
	exec(insert, uuid.NewString(), tenantID, uuid.NewString(), confirmedAt.Add(time.Hour), adminID)
	exec(`INSERT INTO exam_readiness_snapshot VALUES ($1::uuid,$2::uuid,$3::uuid,'current','invalidated',$4,$5::uuid,'{}')`, uuid.NewString(), tenantID, examID, confirmedAt.Add(time.Hour), adminID)
	read := func(status, hash string) ReadinessResult {
		t.Helper()
		result, err := loadReadinessConfirmationFacts(ctx, tx, tenantID, examID, status, ReadinessResult{Ready: true, ConfigurationHash: hash})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	for _, status := range []string{"collecting", "grading", "reviewing", "finalized", "archived"} {
		result := read(status, "current")
		if result.PreparationSnapshotID != snapshotID || result.PreparationConfirmedAt == nil || !result.PreparationConfirmedAt.Equal(confirmedAt) {
			t.Fatalf("%s lost historical confirmation: %#v", status, result)
		}
		if result.Confirmed || result.SnapshotID != "" || result.ConfirmedAt != nil || result.ImportSnapshotAvailable || result.ConfigurationHash != "current" {
			t.Fatalf("%s treated history as current configuration confirmation: %#v", status, result)
		}
	}
	for _, status := range []string{"draft", "configured", "ready"} {
		result := read(status, "current")
		if result.PreparationConfirmedAt != nil || result.PreparationSnapshotID != "" || result.Confirmed {
			t.Fatalf("%s exposed a historical completion mark: %#v", status, result)
		}
	}
	current := read("collecting", "previous")
	if !current.Confirmed || current.SnapshotID != snapshotID || current.ConfirmedAt == nil || !current.ImportSnapshotAvailable {
		t.Fatalf("matching current confirmation changed: %#v", current)
	}
	exec(`UPDATE exam_readiness_snapshot SET status='invalidated' WHERE id=$1::uuid`, snapshotID)
	missing := read("collecting", "current")
	if missing.PreparationConfirmedAt != nil || missing.PreparationSnapshotID != "" || missing.Confirmed {
		t.Fatalf("invalidated history produced completion: %#v", missing)
	}
}
