package server

import (
	"bytes"
	"context"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/exam"
	"edugrade-enterprise/services/api-gateway/internal/idempotency"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// 创建与准确重放得到同一父子资源，恢复和未接受命令状态都核对原数据库事实。
func e2eAssertInitialSessionCommandReplay(t *testing.T, scenario reliabilityCommandScenario) exam.ExamSession {
	t.Helper()
	db, tenantID, userID, store, scope, commandID, input := scenario.DB, scenario.TenantID, scenario.UserID, scenario.Exams, scenario.Scope, scenario.CommandID, scenario.SessionInput
	var err error
	first, err := store.CreateExamSession(context.Background(), scope, userID, input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateExamSession(context.Background(), scope, userID, input)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID || len(first.Exams) != 1 || len(second.Exams) != 1 || first.Exams[0].ID != second.Exams[0].ID {
		t.Fatalf("replay duplicated business facts: %#v %#v", first, second)
	}
	var sessionCount, examCount int
	if err = db.QueryRow(`SELECT count(*),(SELECT count(*) FROM exam WHERE tenant_id=$1::uuid AND exam_session_id=$2::uuid) FROM exam_session WHERE tenant_id=$1::uuid AND created_by=$3::uuid AND command_id=$4`, tenantID, first.ID, userID, commandID).Scan(&sessionCount, &examCount); err != nil {
		t.Fatal(err)
	}
	if sessionCount != 1 || examCount != 1 {
		t.Fatalf("business fact counts session=%d exams=%d", sessionCount, examCount)
	}
	recovered, err := store.RecoverExamSessionCommand(context.Background(), scope, userID, commandID)
	if err != nil || recovered.Session == nil || recovered.Session.ID != first.ID {
		t.Fatalf("recovery=%#v err=%v", recovered, err)
	}
	notAccepted, err := store.RecoverExamSessionCommand(context.Background(), scope, userID, "not-accepted-"+uuid.NewString())
	if err != nil || notAccepted.Status != "not_accepted" {
		t.Fatalf("unaccepted command recovery=%#v err=%v", notAccepted, err)
	}
	return first
}

// 处理中与已拒绝收据返回不同恢复状态；改变原命令负载必须冲突。
func e2eAssertSessionCommandRecoveryStates(t *testing.T, scenario reliabilityCommandScenario) exam.CreateSessionInput {
	t.Helper()
	db, tenantID, userID, store, scope, input := scenario.DB, scenario.TenantID, scenario.UserID, scenario.Exams, scenario.Scope, scenario.SessionInput
	var err error
	processingCommand := "processing-" + uuid.NewString()
	if _, err = db.Exec(`INSERT INTO idempotency_record(tenant_id,actor_id,method,route,idempotency_key,request_hash,expires_at) VALUES($1::uuid,$2::uuid,'POST','/api/v1/exam-sessions',$3,'hash',now()+interval '1 day')`, tenantID, userID, processingCommand); err != nil {
		t.Fatal(err)
	}
	processingResult, err := store.RecoverExamSessionCommand(context.Background(), scope, userID, processingCommand)
	if err != nil || processingResult.Status != "processing" {
		t.Fatalf("processing command recovery=%#v err=%v", processingResult, err)
	}
	if _, err = db.Exec(`UPDATE idempotency_record SET state='completed',response_status=400,response_body=$4 WHERE tenant_id=$1::uuid AND actor_id=$2::uuid AND idempotency_key=$3`, tenantID, userID, processingCommand, []byte(`{"error":{"code":"invalid_exam_session"}}`)); err != nil {
		t.Fatal(err)
	}
	rejectedResult, err := store.RecoverExamSessionCommand(context.Background(), scope, userID, processingCommand)
	if err != nil || rejectedResult.Status != "rejected" || rejectedResult.HTTPStatus != 400 || rejectedResult.ErrorCode != "invalid_exam_session" {
		t.Fatalf("rejected command recovery=%#v err=%v", rejectedResult, err)
	}
	input.Name += " changed"
	if _, err = store.CreateExamSession(context.Background(), scope, userID, input); !errors.Is(err, exam.ErrCommandConflict) {
		t.Fatalf("changed replay error=%v", err)
	}

	return input
}

// 显式新操作不能合并旧资源，两份并发相同命令仍返回同一资源。
func e2eAssertSeparateAndConcurrentSessionCommands(t *testing.T, scenario reliabilityCommandScenario) exam.ExamSession {
	t.Helper()
	userID, store, scope, input, first := scenario.UserID, scenario.Exams, scenario.Scope, scenario.SessionInput, scenario.First
	newCommandInput := input
	newCommandInput.Name = first.Name
	newCommandInput.CommandID = "new-operation-" + uuid.NewString()
	separate, err := store.CreateExamSession(context.Background(), scope, userID, newCommandInput)
	if err != nil || separate.ID == first.ID {
		t.Fatalf("explicit new operation was incorrectly coalesced: %#v err=%v", separate, err)
	}
	concurrentInput := newCommandInput
	concurrentInput.CommandID = "concurrent-" + uuid.NewString()
	startConcurrent := make(chan struct{})
	type createResult struct {
		session exam.ExamSession
		err     error
	}
	concurrentResults := make(chan createResult, 2)
	for range 2 {
		go func() {
			<-startConcurrent
			session, createErr := store.CreateExamSession(context.Background(), scope, userID, concurrentInput)
			concurrentResults <- createResult{session: session, err: createErr}
		}()
	}
	close(startConcurrent)
	left, right := <-concurrentResults, <-concurrentResults
	if left.err != nil || right.err != nil || left.session.ID != right.session.ID {
		t.Fatalf("concurrent recovery left=%#v right=%#v", left, right)
	}

	return separate
}

// 生产事务提交后故障仅丢失传输收据，原请求重试接管并准确重放且父资源只有一份。
func e2eAssertLostSessionReceiptRecovery(t *testing.T, scenario reliabilityCommandScenario) (*exam.Handler, []byte, exam.CreateSessionInput) {
	t.Helper()
	db, tenantID, userID, schoolID, gradeID, classID, store, scope := scenario.DB, scenario.TenantID, scenario.UserID, scenario.SchoolID, scenario.GradeID, scenario.ClassID, scenario.Exams, scenario.Scope
	var err error
	// 同时测试生产 handler 与幂等中间件：业务事务提交后，传输收据持久化失败，
	// 完全相同的重试接管过时预留，且不重复创建事实。
	middlewareCommandID := "middleware-" + uuid.NewString()
	middlewareInput := exam.CreateSessionInput{
		SchoolID: schoolID, GradeID: gradeID, Name: "Middleware recovery", ExamType: "formal_exam",
		GradingMode: "ai_assisted", PublishPolicy: "after_admin_approval", CommandID: middlewareCommandID,
		ClassIDs: []string{classID}, Subjects: []exam.SessionSubjectInput{{Subject: "physics", TotalScore: 100, DurationMinutes: 90,
			Sections: []exam.BlueprintSectionInput{{Title: "Questions", QuestionType: "single_choice", QuestionCount: 20, ScorePerQuestion: 5}}}},
	}
	middlewareBody, err := json.Marshal(middlewareInput)
	if err != nil {
		t.Fatal(err)
	}
	transportStore := &failCompleteOnceIdempotencyStore{Store: idempotency.NewPostgresStore(db)}
	examHandler := exam.NewHandler(store, auth.NewMemoryStore())
	commandHandler := idempotency.Middleware(transportStore, idempotency.Options{Enforce: true, ProcessingTimeout: time.Nanosecond})(http.HandlerFunc(examHandler.CreateExamSession))
	request := func() *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/exam-sessions", bytes.NewReader(middlewareBody))
		req.Pattern = "POST /api/v1/exam-sessions"
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(idempotency.Header, middlewareCommandID)
		ctx := auth.WithUser(req.Context(), auth.User{ID: userID, TenantID: tenantID})
		ctx = auth.WithAccessScope(ctx, scope)
		return req.WithContext(ctx)
	}
	firstResponse := httptest.NewRecorder()
	commandHandler.ServeHTTP(firstResponse, request())
	if firstResponse.Code != http.StatusServiceUnavailable || !strings.Contains(firstResponse.Body.String(), "idempotency_persist_failed") {
		t.Fatalf("injected receipt failure response=%d %s", firstResponse.Code, firstResponse.Body.String())
	}
	committed, err := store.RecoverExamSessionCommand(context.Background(), scope, userID, middlewareCommandID)
	if err != nil || committed.Status != "succeeded" || committed.Session == nil {
		t.Fatalf("business fact was not committed before lost receipt: %#v err=%v", committed, err)
	}
	secondResponse := httptest.NewRecorder()
	commandHandler.ServeHTTP(secondResponse, request())
	if secondResponse.Code != http.StatusCreated || !strings.Contains(secondResponse.Body.String(), committed.Session.ID) {
		t.Fatalf("exact retry did not recover committed resource: %d %s", secondResponse.Code, secondResponse.Body.String())
	}
	thirdResponse := httptest.NewRecorder()
	commandHandler.ServeHTTP(thirdResponse, request())
	if thirdResponse.Code != http.StatusCreated || thirdResponse.Header().Get("Idempotency-Replayed") != "true" || thirdResponse.Body.String() != secondResponse.Body.String() {
		t.Fatalf("completed replay mismatch: second=%d/%s third=%d/%s replayed=%s", secondResponse.Code, secondResponse.Body.String(), thirdResponse.Code, thirdResponse.Body.String(), thirdResponse.Header().Get("Idempotency-Replayed"))
	}
	var middlewareSessionCount int
	if err = db.QueryRow(`SELECT count(*) FROM exam_session WHERE tenant_id=$1::uuid AND created_by=$2::uuid AND command_id=$3`, tenantID, userID, middlewareCommandID).Scan(&middlewareSessionCount); err != nil || middlewareSessionCount != 1 {
		t.Fatalf("middleware retry duplicated sessions: count=%d err=%v", middlewareSessionCount, err)
	}
	return examHandler, middlewareBody, middlewareInput
}
