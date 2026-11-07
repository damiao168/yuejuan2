package server

import (
	"bytes"
	"context"
	"database/sql"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/idempotency"
	"edugrade-enterprise/services/api-gateway/internal/paper"
	"edugrade-enterprise/services/api-gateway/internal/processing"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type failCompleteOnceIdempotencyStore struct {
	idempotency.Store
	failed bool
}

func (s *failCompleteOnceIdempotencyStore) Complete(ctx context.Context, input idempotency.BeginInput, status int, headers map[string]string, body []byte) error {
	if !s.failed {
		s.failed = true
		return errors.New("injected replay receipt failure")
	}
	return s.Store.Complete(ctx, input, status, headers, body)
}

func TestReliabilityCommandRecoveryWithPostgresTestDatabase(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is not set outside the PostgreSQL CI job")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	tenantID, userID, schoolID, gradeID, classID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	academicYearID, cohortID, captureRoleID, capturePermissionID := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO tenant(id,tenant_id,name,code,status) VALUES($1::uuid,$1::uuid,'Reliability tenant',$2,'active')`, []any{tenantID, "reliability-" + uuid.NewString()}},
		{`INSERT INTO school(id,tenant_id,name,code,status) VALUES($1::uuid,$2::uuid,'Reliability school','reliability-school','active')`, []any{schoolID, tenantID}},
		// 恢复场景中的处理人必须具备真实同校授权，不能靠 tenant-wide 业务调用上下文替代数据库分派校验。
		{`INSERT INTO app_user(id,tenant_id,school_id,username,display_name,password_hash,status) VALUES($1::uuid,$2::uuid,$3::uuid,'reliability-admin','Reliability admin','not-used','active')`, []any{userID, tenantID, schoolID}},
		{`INSERT INTO role(id,tenant_id,code,name,scope_type) VALUES($1::uuid,$2::uuid,'reliability-capture-manager','Reliability capture manager','school')`, []any{captureRoleID, tenantID}},
		{`INSERT INTO permission(id,tenant_id,code,name,resource,action) VALUES($1::uuid,$2::uuid,'capture:manage','Manage capture','capture','manage')`, []any{capturePermissionID, tenantID}},
		{`INSERT INTO user_role(tenant_id,user_id,role_id,data_scope) VALUES($1::uuid,$2::uuid,$3::uuid,jsonb_build_object('scope','school','school_id',$4::text))`, []any{tenantID, userID, captureRoleID, schoolID}},
		{`INSERT INTO role_permission(tenant_id,role_id,permission_id) VALUES($1::uuid,$2::uuid,$3::uuid)`, []any{tenantID, captureRoleID, capturePermissionID}},
		{`INSERT INTO academic_year(id,tenant_id,school_id,name,start_year,end_year,starts_at,ends_at,is_current,status) VALUES($1::uuid,$2::uuid,$3::uuid,'2026-2027',2026,2027,'2026-09-01','2027-08-31',true,'active')`, []any{academicYearID, tenantID, schoolID}},
		{`INSERT INTO grade_cohort(id,tenant_id,school_id,education_stage,entry_year,expected_graduation_year,name,status) VALUES($1::uuid,$2::uuid,$3::uuid,'senior',2026,2029,'2026 cohort','active')`, []any{cohortID, tenantID, schoolID}},
		{`INSERT INTO grade(id,tenant_id,school_id,name,level_no,academic_year,status,education_stage,academic_year_id,grade_cohort_id) VALUES($1::uuid,$2::uuid,$3::uuid,'Grade 10',10,'2026-2027','active','senior',$4::uuid,$5::uuid)`, []any{gradeID, tenantID, schoolID, academicYearID, cohortID}},
		{`INSERT INTO school_class(id,tenant_id,school_id,grade_id,name,code,status,academic_year_id,grade_cohort_id,class_no) VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,'Class 1','class-1','active',$5::uuid,$6::uuid,1)`, []any{classID, tenantID, schoolID, gradeID, academicYearID, cohortID}},
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatalf("seed command fixture: %v", err)
		}
	}
	store := exam.NewPostgresStore(db)
	scope := auth.AccessScope{TenantID: tenantID, TenantWide: true}
	commandID := "reliability-" + uuid.NewString()
	input := exam.CreateSessionInput{SchoolID: schoolID, GradeID: gradeID, Name: "Reliability " + time.Now().UTC().Format(time.RFC3339Nano), ExamType: "formal_exam", GradingMode: "ai_assisted", PublishPolicy: "after_admin_approval", CommandID: commandID, Subjects: []exam.SessionSubjectInput{{Subject: "math", TotalScore: 100, DurationMinutes: 90}}}
	scenario := reliabilityCommandScenario{DB: db, TenantID: tenantID, UserID: userID, SchoolID: schoolID,
		GradeID: gradeID, ClassID: classID, Exams: store, Scope: scope, CommandID: commandID, SessionInput: input}
	scenario.First = e2eAssertInitialSessionCommandReplay(t, scenario)
	scenario.SessionInput = e2eAssertSessionCommandRecoveryStates(t, scenario)
	scenario.Separate = e2eAssertSeparateAndConcurrentSessionCommands(t, scenario)
	scenario.ExamHandler, scenario.MiddlewareBody, scenario.MiddlewareInput = e2eAssertLostSessionReceiptRecovery(t, scenario)

	t.Run("CMD-02 actual Web client recovers post-commit 503 against PostgreSQL", func(t *testing.T) {
		e2eAssertActualClientCommandRecovery(t, scenario)
	})

	scenario.FileID, scenario.Paper, scenario.ImportInput, scenario.Job = e2eCreateReliableImportCommand(t, scenario)
	scenario.Dispatch = e2eAssertImportDurableDispatchIntent(t, scenario)
	scenario.Runtime = workerruntime.NewPostgresStore(db)
	scenario.OldClaim = e2eCreateAndCancelImportRuntime(t, scenario)
	e2eAssertSuccessorRunRollback(t, scenario)
	scenario.Current = e2eAssertSuccessorImportAndStaleCallback(t, scenario)
	e2eAssertLegacyImportCallbacksRejected(t, scenario)
	scenario.FailureTask, scenario.FailureClaim = e2eCreateImportFailureRuntime(t, scenario)
	scenario.FailedJob = e2eAssertImportFailureAtomicity(t, scenario)
	scenario.Retried = e2eAssertFailedImportRetryIdentity(t, scenario)
	e2eRaceImportCancellationAndCompletion(t, scenario)
	scenario.Projection, scenario.PageID, scenario.ExceptionID = e2eCreateAssignedProjectionSource(t, scenario)
	e2eDeleteAssignedProjectionSource(t, scenario)
	e2eAssertManualProjectionResolutionPreserved(t, scenario)
	scenario.MovedPageID = e2eMoveProjectionSourceToDestination(t, scenario)
	e2eAssertDestinationFirstProjectionFacts(t, scenario)
	e2eRegroupProjectionSource(t, scenario)
	e2eDeleteProjectionSubmission(t, scenario)
	e2eAssertProjectionAcknowledgesClaimedVersion(t, scenario)

	t.Run("PROJ expired and same-owner reclaimed leases are fenced", func(t *testing.T) {
		e2eAssertProjectionLeaseGenerationFence(t, scenario)
	})

	t.Run("IMP durable preparation retries and exhaustion", func(t *testing.T) {
		e2eAssertPreparationRetryExhaustion(t, scenario)
	})
	t.Run("IMP abandoned preparation lease retries then exhausts atomically", func(t *testing.T) {
		e2eAssertAbandonedPreparationRecovery(t, scenario)
	})
	t.Run("IMP final abandoned attempt atomically fails its run", func(t *testing.T) {
		e2eAssertFinalAbandonedRuntimeRecovery(t, scenario)
	})
	t.Run("PROJ production capture delete restore follows the effective page set", func(t *testing.T) {
		e2eAssertCaptureProjectionLifecycle(t, scenario)
	})
	t.Run("IMP frozen exam rejects new commands but retains accepted command recovery", func(t *testing.T) {
		e2eAssertFrozenImportCommands(t, scenario)
	})
}

type reliabilityCommandScenario struct {
	DB                                                              *sql.DB
	TenantID, UserID, SchoolID, GradeID, ClassID, CommandID, FileID string
	Exams                                                           *exam.PostgresStore
	Scope                                                           auth.AccessScope
	SessionInput                                                    exam.CreateSessionInput
	First, Separate                                                 exam.ExamSession
	ExamHandler                                                     *exam.Handler
	MiddlewareBody                                                  []byte
	MiddlewareInput                                                 exam.CreateSessionInput
	Paper                                                           *paper.PostgresStore
	ImportInput                                                     paper.CreatePaperImportInput
	Job, Dispatch, Current, FailedJob, Retried                      paper.PaperImportJob
	Runtime                                                         *workerruntime.PostgresStore
	OldClaim, FailureClaim                                          []workerruntime.Task
	FailureTask                                                     workerruntime.Task
	Projection                                                      *processing.PostgresStore
	PageID, ExceptionID, MovedPageID                                string
}

// 真实客户端仍通过同一故障 handler 调用 PostgreSQL；资源与一分钟截止保留在该子测试。
func e2eAssertActualClientCommandRecovery(t *testing.T, scenario reliabilityCommandScenario) {
	t.Helper()
	db, tenantID, userID, scope, examHandler, middlewareBody := scenario.DB, scenario.TenantID, scenario.UserID, scenario.Scope, scenario.ExamHandler, scenario.MiddlewareBody
	mux := http.NewServeMux()
	faultStore := &failCompleteOnceIdempotencyStore{Store: idempotency.NewPostgresStore(db)}
	mux.Handle("POST /api/v1/exam-sessions", idempotency.Middleware(faultStore, idempotency.Options{Enforce: true, ProcessingTimeout: time.Nanosecond})(http.HandlerFunc(examHandler.CreateExamSession)))
	mux.HandleFunc("GET /api/v1/exam-sessions/commands/{commandId}", examHandler.RecoverExamSessionCommand)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := auth.WithUser(r.Context(), auth.User{ID: userID, TenantID: tenantID})
		mux.ServeHTTP(w, r.WithContext(auth.WithAccessScope(ctx, scope)))
	}))
	defer server.Close()
	fixture := filepath.Join(t.TempDir(), "command.json")
	if err := os.WriteFile(fixture, middlewareBody, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "node", "scripts/ci/exam-command-client.mjs", server.URL, fixture)
	command.Dir = filepath.Join("..", "..", "..", "..")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("actual client recovery failed (Node/npm ci required): %v\n%s", err, output)
	}
	var evidence struct {
		CommandIDs []string `json:"commandIds"`
		SessionIDs []string `json:"sessionIds"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(output), &evidence); err != nil {
		t.Fatalf("client evidence: %v\n%s", err, output)
	}
	if len(evidence.CommandIDs) != 2 || len(evidence.SessionIDs) != 2 {
		t.Fatalf("incomplete client evidence: %s", output)
	}
	e2eAssertActualClientCommandResources(t, scenario, evidence.CommandIDs, evidence.SessionIDs)
	t.Logf("CMD-02/CMD-04/CMD-07 real client + PostgreSQL evidence: %s", output)

}

// 逐命令核对真实客户端生成的父子资源，两个显式命令各只有一份。
func e2eAssertActualClientCommandResources(t *testing.T, scenario reliabilityCommandScenario, commandIDs, sessionIDs []string) {
	t.Helper()
	db, tenantID, userID, middlewareInput := scenario.DB, scenario.TenantID, scenario.UserID, scenario.MiddlewareInput
	for i, commandID := range commandIDs {
		var sessions, children int
		if err := db.QueryRow(`SELECT count(*),(SELECT count(*) FROM exam WHERE tenant_id=$1::uuid AND exam_session_id=$4::uuid) FROM exam_session WHERE tenant_id=$1::uuid AND created_by=$2::uuid AND command_id=$3`, tenantID, userID, commandID, sessionIDs[i]).Scan(&sessions, &children); err != nil {
			t.Fatal(err)
		}
		if sessions != 1 || children != len(middlewareInput.Subjects) {
			t.Fatalf("actual client duplicated business resources: sessions=%d children=%d", sessions, children)
		}
	}
}

// 相同 owner 重领也不能复用旧租约，过期发布和失败回调均须拒绝。
func e2eAssertProjectionLeaseGenerationFence(t *testing.T, scenario reliabilityCommandScenario) {
	t.Helper()
	db, tenantID, first, projectionStore := scenario.DB, scenario.TenantID, scenario.First, scenario.Projection
	ctx := context.Background()
	if _, err := db.Exec(`SELECT request_processing_projection_refresh($1::uuid,$2::uuid)`, tenantID, first.Exams[0].ID); err != nil {
		t.Fatal(err)
	}
	old, ok, err := projectionStore.ClaimProjection(ctx, "same-owner", time.Minute)
	if err != nil || !ok {
		t.Fatalf("claim: %v %v", ok, err)
	}
	if _, err := db.Exec(`UPDATE processing_projection_cursor SET lease_expires_at=now()-interval '1 second' WHERE tenant_id=$1::uuid AND exam_id=$2::uuid`, old.TenantID, old.ExamID); err != nil {
		t.Fatal(err)
	}
	if err := projectionStore.ApplyProjection(ctx, "same-owner", old); !errors.Is(err, processing.ErrProjectionLeaseLost) {
		t.Fatalf("expired apply: %v", err)
	}
	if err := projectionStore.FailProjection(ctx, "same-owner", old, "stale", time.Second); !errors.Is(err, processing.ErrProjectionLeaseLost) {
		t.Fatalf("expired fail: %v", err)
	}
	fresh, ok, err := projectionStore.ClaimProjection(ctx, "same-owner", time.Minute)
	if err != nil || !ok {
		t.Fatalf("reclaim: %v %v", ok, err)
	}
	if err := projectionStore.ApplyProjection(ctx, "same-owner", old); !errors.Is(err, processing.ErrProjectionLeaseLost) {
		t.Fatalf("old claim reused new lease: %v", err)
	}
	if err := projectionStore.FailProjection(ctx, "same-owner", old, "stale", time.Second); !errors.Is(err, processing.ErrProjectionLeaseLost) {
		t.Fatalf("old failure cleared new lease: %v", err)
	}
	if err := projectionStore.ApplyProjection(ctx, "same-owner", fresh); err != nil {
		t.Fatal(err)
	}

}
