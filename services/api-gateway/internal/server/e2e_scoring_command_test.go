package server

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/grading"
	"edugrade-enterprise/services/api-gateway/internal/idempotency"
)

func TestScoringCommandRecoveryWithPostgresTestDatabase(t *testing.T) {
	dsn := os.Getenv("EDUGRADE_E2E_DATABASE_URL")
	if dsn == "" {
		t.Skip("dedicated PostgreSQL required")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	e2eActivatePostgresDemoUsers(t, db, []string{"tenant_admin", "grader"})
	router := e2ePostgresRouter(db)
	token := e2eLoginWithTenant(t, router, "demo", "tenant_admin", "ChangeMe123!")
	suffix := time.Now().UTC().Format("20060102150405.000000000")
	fixture := e2eCreateStory056AcceptanceFixture(t, db, router, token, suffix, story056AcceptanceAnswerCount)
	e2eSeedStory056AcceptanceAnswers(t, db, fixture, suffix)
	store := grading.NewPostgresStore(db)
	ctx := context.Background()
	command := "scoring-command-" + suffix
	input := grading.StartScoringRunInput{IdempotencyKey: command}
	start := make(chan struct{})
	type outcome struct {
		run grading.ScoringRun
		err error
	}
	results := make(chan outcome, 2)
	for i := 0; i < 2; i++ {
		go func() {
			<-start
			run, err := store.StartScoringRun(ctx, fixture.TenantID, fixture.ExamID, fixture.AdminID, input)
			results <- outcome{run, err}
		}()
	}
	close(start)
	first, second := <-results, <-results
	if first.err != nil || second.err != nil || first.run.ID != second.run.ID {
		t.Fatalf("concurrent command: %+v %+v", first, second)
	}
	if _, err := store.StartScoringRun(ctx, fixture.TenantID, "00000000-0000-0000-0000-000000000001", fixture.AdminID, input); !errors.Is(err, grading.ErrCommandConflict) {
		t.Fatalf("changed target: %v", err)
	}
	otherActor := e2eLookupUserID(t, db, "demo", "grader")
	if _, err := store.StartScoringRun(ctx, fixture.TenantID, fixture.ExamID, otherActor, input); !errors.Is(err, grading.ErrCommandConflict) {
		t.Fatalf("other actor reused result: %v", err)
	}
	invisible, err := store.RecoverScoringCommand(ctx, fixture.TenantID, fixture.ExamID, otherActor, command)
	if err != nil || invisible.Status != "not_accepted" {
		t.Fatalf("actor isolation: %+v %v", invisible, err)
	}
	assertScoringCommandTransportRecovery(t, db, store, fixture, command, first.run.ID)
	if _, err = db.Exec(`UPDATE scoring_run SET deleted_at=now() WHERE id=$1::uuid`, first.run.ID); err != nil {
		t.Fatal(err)
	}
	replay, err := grading.NewPostgresStore(db).StartScoringRun(ctx, fixture.TenantID, fixture.ExamID, fixture.AdminID, input)
	if err != nil || replay.ID != first.run.ID {
		t.Fatalf("deleted replay: %+v %v", replay, err)
	}
	recovered := e2eGetJSON(t, router, "/api/v1/exams/"+fixture.ExamID+"/scoring-runs/commands/"+command, token, http.StatusOK)
	if recovered["status"] != "succeeded" || recovered["scoring_run"].(map[string]any)["id"] != first.run.ID {
		t.Fatalf("recovery: %+v", recovered)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM scoring_run WHERE tenant_id=$1::uuid AND idempotency_key=$2`, fixture.TenantID, command).Scan(&count); err != nil || count != 1 {
		t.Fatalf("run count %d: %v", count, err)
	}
}

// 故意只破坏传输回执的第一次完成写入；重试必须接回已成功的业务评分运行。
func assertScoringCommandTransportRecovery(t *testing.T, db *sql.DB, store *grading.PostgresStore, fixture story056AcceptanceFixture, command, runID string) {
	t.Helper()
	handler := grading.NewHandler(store, grading.NewEngine(), auth.NewMemoryStore())
	transport := &failCompleteOnceIdempotencyStore{Store: idempotency.NewPostgresStore(db)}
	mux := http.NewServeMux()
	mux.Handle("POST /api/v1/exams/{examId}/scoring-runs", idempotency.Middleware(transport, idempotency.Options{Enforce: true, ProcessingTimeout: time.Nanosecond})(http.HandlerFunc(handler.StartScoringRun)))
	request := func() *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/api/v1/exams/"+fixture.ExamID+"/scoring-runs", strings.NewReader(`{"idempotency_key":"`+command+`"}`))
		r.Header.Set("Idempotency-Key", command)
		return r.WithContext(auth.WithUser(r.Context(), auth.User{ID: fixture.AdminID, TenantID: fixture.TenantID}))
	}
	failed := httptest.NewRecorder()
	mux.ServeHTTP(failed, request())
	if failed.Code != 503 {
		t.Fatalf("receipt fault: %d %s", failed.Code, failed.Body)
	}
	retried := httptest.NewRecorder()
	mux.ServeHTTP(retried, request())
	if retried.Code != 201 || !strings.Contains(retried.Body.String(), runID) {
		t.Fatalf("takeover: %d %s", retried.Code, retried.Body)
	}
}
