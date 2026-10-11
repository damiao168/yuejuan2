package imagequality

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/capture"
	"edugrade-enterprise/services/api-gateway/internal/submission"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

type resultOrderBatch30 struct {
	t                    *testing.T
	events               []string
	run                  Run
	pageErr, captureErr  error
	queueErr, runtimeErr error
	completion           workerruntime.CompleteInput
	failure              workerruntime.FailInput
}

func (s *resultOrderBatch30) record(name, tenant string) {
	s.t.Helper()
	if tenant != "tenant" {
		s.t.Fatalf("lost request tenant: %q", tenant)
	}
	s.events = append(s.events, name)
}

type resultStoreBatch30 struct {
	Store
	trace *resultOrderBatch30
}

func (s resultStoreBatch30) CompleteRun(_ context.Context, tenant, id string, input ResultInput) (Run, error) {
	s.trace.record("run", tenant)
	if id != "run" || input.LeaseToken != "lease" {
		s.trace.t.Fatal("lost result identity")
	}
	return s.trace.run, nil
}

type resultCoordinatorBatch30 struct {
	TransactionalStore
	trace *resultOrderBatch30
}

func (s resultCoordinatorBatch30) SubmitResultCommand(_ context.Context, tenant, actor, id string, _ ResultInput) (Run, error) {
	s.trace.record("coordinator", tenant)
	if actor != "actor" || id != "run" {
		s.trace.t.Fatal("lost coordinator identity")
	}
	return s.trace.run, nil
}

type resultSubmissionBatch30 struct {
	submission.Store
	trace *resultOrderBatch30
}

func (s resultSubmissionBatch30) ApplyPageQualityResult(_ context.Context, tenant string, input submission.ApplyPageQualityInput) (submission.SubmissionPage, error) {
	s.trace.record("page", tenant)
	if input.PageID != "page" || input.SubmissionID != "submission" || input.LatestQualityRunID != "run" {
		s.trace.t.Fatal("lost page identity")
	}
	return submission.SubmissionPage{}, s.trace.pageErr
}

type resultCaptureBatch30 struct {
	capture.Store
	trace *resultOrderBatch30
}

func (s resultCaptureBatch30) ApplyQualityOutcome(_ context.Context, tenant, page, status string) error {
	s.trace.record("capture", tenant)
	if page != "page" || status != s.trace.run.QualityStatus {
		s.trace.t.Fatal("lost capture identity")
	}
	return s.trace.captureErr
}

func (s resultCaptureBatch30) QueueSubmissionPages(_ context.Context, tenant, id, actor string) ([]capture.RegistrationRun, error) {
	s.trace.record("queue", tenant)
	if id != "submission" || actor != "actor" {
		s.trace.t.Fatal("lost queue identity")
	}
	return nil, s.trace.queueErr
}

type resultRuntimeBatch30 struct {
	workerruntime.Store
	trace *resultOrderBatch30
}

func (s resultRuntimeBatch30) GetBySource(_ context.Context, tenant, kind, id string) (workerruntime.Task, error) {
	s.trace.record("runtime", tenant)
	if kind != "image_quality_run" || id != "run" {
		s.trace.t.Fatal("lost runtime identity")
	}
	return workerruntime.Task{ID: "task"}, s.trace.runtimeErr
}

func (s resultRuntimeBatch30) Complete(_ context.Context, tenant, id string, input workerruntime.CompleteInput) (workerruntime.Task, error) {
	s.trace.record("complete", tenant)
	if id != "task" {
		s.trace.t.Fatal("wrong runtime task")
	}
	s.trace.completion = input
	return workerruntime.Task{}, nil
}

func (s resultRuntimeBatch30) Fail(_ context.Context, tenant, id string, input workerruntime.FailInput) (workerruntime.Task, error) {
	s.trace.record("fail", tenant)
	if id != "task" {
		s.trace.t.Fatal("wrong runtime task")
	}
	s.trace.failure = input
	return workerruntime.Task{}, nil
}

type resultAuditBatch30 struct {
	auth.Store
	trace *resultOrderBatch30
}

func (s resultAuditBatch30) Audit(_ context.Context, event auth.AuditEvent) error {
	s.trace.record("audit", event.TenantID)
	if event.ActorID != "actor" || event.TargetID != "run" || event.Action != "image_quality.result_submitted" {
		s.trace.t.Fatal("lost result audit identity")
	}
	return nil
}

func TestBatch30ResultKeepsLegacyProjectionAndReceiptOrder(t *testing.T) {
	fault := errors.New("projection unavailable")
	for _, tc := range []imageQualityResultOrderCase{
		{"complete without capture", ProcessingCompleted, false, false, nil, nil, nil, nil, 200, []string{"run", "page", "runtime", "complete", "audit"}},
		{"unlinked capture", ProcessingCompleted, true, false, nil, capture.ErrNotFound, nil, nil, 200, []string{"run", "page", "capture", "runtime", "complete", "audit"}},
		{"already queued", ProcessingCompleted, true, false, nil, nil, capture.ErrInvalidTransition, nil, 200, []string{"run", "page", "capture", "queue", "runtime", "complete", "audit"}},
		{"page failure", ProcessingCompleted, true, false, fault, nil, nil, nil, 500, []string{"run", "page"}},
		{"capture failure", ProcessingCompleted, true, false, nil, fault, nil, nil, 500, []string{"run", "page", "capture"}},
		{"queue failure", ProcessingCompleted, true, false, nil, nil, fault, nil, 500, []string{"run", "page", "capture", "queue"}},
		{"runtime missing", ProcessingCompleted, false, false, nil, nil, nil, workerruntime.ErrNotFound, 404, []string{"run", "page", "runtime"}},
		{"retryable error", ProcessingRetryableError, true, false, nil, nil, nil, nil, 200, []string{"run", "runtime", "fail", "audit"}},
		{"terminal error", ProcessingTerminalError, true, false, nil, nil, nil, nil, 200, []string{"run", "runtime", "fail", "audit"}},
		{"coordinator bypasses legacy writes", ProcessingCompleted, true, true, fault, fault, fault, fault, 200, []string{"coordinator", "audit"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertImageQualityResultProjectionOrder(t, tc)
		})
	}
}

type imageQualityResultOrderCase struct {
	name                  string
	processing            string
	captures, coordinator bool
	pageErr, captureErr   error
	queueErr, runtimeErr  error
	status                int
	events                []string
}

func assertImageQualityResultProjectionOrder(t *testing.T, tc imageQualityResultOrderCase) {
	t.Helper()
	// 替身只记录HTTP入口的副作用顺序并注入失败，不证明旧路径整体事务或数据库并发。
	trace := &resultOrderBatch30{t: t, pageErr: tc.pageErr, captureErr: tc.captureErr, queueErr: tc.queueErr, runtimeErr: tc.runtimeErr,
		run: Run{ID: "run", SubmissionID: "submission", SubmissionPageID: "page", ProcessingStatus: tc.processing, QualityStatus: QualityPassed, ResultVersion: "v1"}}
	h := NewHandler(resultStoreBatch30{trace: trace}, resultSubmissionBatch30{trace: trace}, nil, resultAuditBatch30{trace: trace}, resultRuntimeBatch30{trace: trace})
	if tc.captures {
		h.captures = resultCaptureBatch30{trace: trace}
	}
	if tc.coordinator {
		h.coordinator = resultCoordinatorBatch30{trace: trace}
	}
	input := ResultInput{LeaseToken: "lease", AttemptNo: 1, ResultVersion: "v1", DurationMS: 9, ProcessingStatus: tc.processing, QualityStatus: QualityReview}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/runs/run/result", strings.NewReader(string(raw)))
	r.SetPathValue("runId", "run")
	r = r.WithContext(auth.WithUser(r.Context(), auth.User{TenantID: "tenant", ID: "actor"}))
	w := httptest.NewRecorder()
	h.SubmitResult(w, r)
	if w.Code != tc.status || !reflect.DeepEqual(trace.events, tc.events) {
		t.Fatalf("status=%d events=%v, want %d %v", w.Code, trace.events, tc.status, tc.events)
	}
	assertImageQualityRuntimeReceipt(t, trace, tc, w.Code)
}

// 副作用顺序核验完成后，分别检查成功和可重试/终止失败的 worker 回执。
func assertImageQualityRuntimeReceipt(t *testing.T, trace *resultOrderBatch30, tc imageQualityResultOrderCase, status int) {
	t.Helper()
	if tc.processing == ProcessingCompleted && status == 200 && !tc.coordinator {
		if trace.completion.LeaseToken != "lease" || trace.completion.DurationMS != 9 || trace.completion.ResultSchemaVersion != "image-quality-result.v1" {
			t.Fatal("completion receipt changed")
		}
	}
	if tc.processing != ProcessingCompleted {
		if trace.failure.ErrorCode != "image_quality_processing_failed" || trace.failure.LeaseToken != "lease" || trace.failure.DurationMS != 9 || trace.failure.Retryable != (tc.processing == ProcessingRetryableError) {
			t.Fatal("failure receipt changed")
		}
	}
}
