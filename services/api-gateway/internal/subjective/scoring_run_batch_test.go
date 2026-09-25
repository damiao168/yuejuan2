package subjective

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

func TestScoringRunBatchIdentityAndRetryCandidates(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	const tenant = "tenant-1"
	input := CreateBatchInput{
		IdempotencyKey: "run-1-batch", ScoringRunID: "run-1",
		SegmentIDs: []string{"failed", "succeeded", "conflict", "untouched"},
	}
	batch, err := store.CreateBatch(ctx, tenant, "actor-1", input)
	if err != nil || batch.ScoringRunID != input.ScoringRunID {
		t.Fatalf("create scoring-run batch: %+v, %v", batch, err)
	}
	replay, err := store.CreateBatch(ctx, tenant, "actor-1", input)
	if err != nil || replay.ID != batch.ID {
		t.Fatalf("same request must replay the batch: %+v, %v", replay, err)
	}
	changedRun := input
	changedRun.ScoringRunID = "run-2"
	if _, err := store.CreateBatch(ctx, tenant, "actor-1", changedRun); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("same idempotency key must not cross scoring runs: %v", err)
	}
	other, err := store.CreateBatch(ctx, tenant, "actor-1", CreateBatchInput{
		IdempotencyKey: "run-2-batch", ScoringRunID: "run-2", SegmentIDs: []string{"other"},
	})
	if err != nil {
		t.Fatal(err)
	}
	listed, err := store.ListScoringRunBatches(ctx, tenant, "run-1")
	if err != nil || len(listed) != 1 || listed[0].ID != batch.ID {
		t.Fatalf("run listing leaked another run: %+v, %v", listed, err)
	}
	listed[0].SegmentIDs[0] = "mutated"
	stored, err := store.GetBatch(ctx, tenant, batch.ID)
	if err != nil || stored.SegmentIDs[0] != "failed" {
		t.Fatalf("listing mutated stored segments: %+v, %v", stored, err)
	}
	if listed, err := store.ListScoringRunBatches(ctx, "other-tenant", "run-1"); err != nil || len(listed) != 0 {
		t.Fatalf("cross-tenant listing: %+v, %v", listed, err)
	}
	if _, err := store.FailedBatchSegments(ctx, "other-tenant", batch.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant retry candidates must be hidden: %v", err)
	}
	for _, tc := range []struct {
		segment string
		status  string
	}{
		{"failed", RunFailed}, {"succeeded", RunSucceeded}, {"conflict", RunConflict},
	} {
		run, err := store.GetOrCreateRun(ctx, tenant, "actor-1", CreateRunInput{
			BatchID: batch.ID, AnswerSegmentID: tc.segment, RequestID: "request-" + tc.segment,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.UpdateRun(ctx, tenant, run.ID, UpdateRunInput{Status: tc.status}); err != nil {
			t.Fatal(err)
		}
	}
	failed, err := store.FailedBatchSegments(ctx, tenant, batch.ID)
	if err != nil || !reflect.DeepEqual(failed, []string{"failed", "conflict"}) {
		t.Fatalf("retry must contain only failed and conflicted segments: %v, %v", failed, err)
	}
	if _, err := store.FailedBatchSegments(ctx, tenant, other.ID); err != nil {
		t.Fatalf("empty retry candidates should be valid: %v", err)
	}
	legacy, err := store.CreateBatch(ctx, tenant, "actor-1", CreateBatchInput{
		IdempotencyKey: "legacy-batch", SegmentIDs: []string{"legacy"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.FailedBatchSegments(ctx, tenant, legacy.ID); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("legacy batch has no scoring-run retry context: %v", err)
	}
	if _, err := store.FailedBatchSegments(ctx, tenant, "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown batch should be missing: %v", err)
	}
	if _, err := store.UpdateBatch(ctx, tenant, batch.ID, UpdateBatchInput{Status: "cancelled"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FailedBatchSegments(ctx, tenant, batch.ID); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("cancelled batch must not be retried: %v", err)
	}
	if _, err := store.UpdateBatch(ctx, tenant, batch.ID, UpdateBatchInput{Status: "processing"}); !errors.Is(err, ErrBatchCancelled) {
		t.Fatalf("cancelled batch must stay terminal: %v", err)
	}
	if _, err := store.RefreshBatch(ctx, tenant, batch.ID); err != nil {
		t.Fatalf("cancelled batch should remain readable: %v", err)
	}
}

func TestCreateBatchRejectsScoringRunChangeOnReplay(t *testing.T) {
	store := NewMemoryStore()
	store.AddContext("tenant-1", "segment-1", ContextForTest("short_answer", 5, "answer", nil))
	handler := NewHandler(store, nil, nil)
	create := func(runID string) *httptest.ResponseRecorder {
		body, err := json.Marshal(CreateBatchInput{
			IdempotencyKey: "batch-command-1", ScoringRunID: runID, SegmentIDs: []string{"segment-1"},
		})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, "/subjective-grading-batches", bytes.NewReader(body))
		req = req.WithContext(auth.WithUser(req.Context(), auth.User{ID: "actor-1", TenantID: "tenant-1"}))
		rec := httptest.NewRecorder()
		handler.CreateBatch(rec, req)
		return rec
	}
	first := create("run-1")
	if first.Code != http.StatusCreated {
		t.Fatalf("create run batch: %d %s", first.Code, first.Body.String())
	}
	if replay := create("run-1"); replay.Code != http.StatusCreated {
		t.Fatalf("same run must replay: %d %s", replay.Code, replay.Body.String())
	}
	if changed := create("run-2"); changed.Code != http.StatusConflict {
		t.Fatalf("changed scoring run must conflict: %d %s", changed.Code, changed.Body.String())
	}
}

func TestCancelledBatchCannotBeEnqueued(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	batch, err := store.CreateBatch(ctx, "tenant-1", "actor-1", CreateBatchInput{
		IdempotencyKey: "cancelled-1", ScoringRunID: "run-1", SegmentIDs: []string{"segment-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateBatch(ctx, "tenant-1", batch.ID, UpdateBatchInput{Status: "cancelled"}); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(store, NewMockLLMAdapter(), nil).WithWorkerRuntimeStore(workerruntime.NewMemoryStore())
	req := httptest.NewRequest(http.MethodPost, "/subjective-grading-batches/"+batch.ID+"/enqueue", nil)
	req.SetPathValue("batchId", batch.ID)
	req = req.WithContext(auth.WithUser(req.Context(), auth.User{ID: "actor-1", TenantID: "tenant-1"}))
	rec := httptest.NewRecorder()
	handler.EnqueueBatch(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("cancelled batch must reject enqueue: %d %s", rec.Code, rec.Body.String())
	}
}

func TestScoringRunBatchReadEndpoints(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	batch, err := store.CreateBatch(ctx, "tenant-1", "actor-1", CreateBatchInput{
		IdempotencyKey: "batch-1", ScoringRunID: "run-1", SegmentIDs: []string{"segment-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.GetOrCreateRun(ctx, "tenant-1", "actor-1", CreateRunInput{
		BatchID: batch.ID, AnswerSegmentID: "segment-1", RequestID: "request-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpdateRun(ctx, "tenant-1", run.ID, UpdateRunInput{Status: RunFailed}); err != nil {
		t.Fatal(err)
	}
	handler := NewHandler(store, nil, nil)
	request := func(path, pathKey, pathValue string, handle http.HandlerFunc) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.SetPathValue(pathKey, pathValue)
		req = req.WithContext(auth.WithUser(req.Context(), auth.User{ID: "actor-1", TenantID: "tenant-1"}))
		rec := httptest.NewRecorder()
		handle(rec, req)
		return rec
	}
	listed := request("/scoring-runs/run-1/batches", "runId", "run-1", handler.ListScoringRunBatches)
	var listing struct {
		Batches []GradingBatch `json:"batches"`
	}
	if listed.Code != http.StatusOK || json.Unmarshal(listed.Body.Bytes(), &listing) != nil || len(listing.Batches) != 1 || listing.Batches[0].ID != batch.ID {
		t.Fatalf("batch listing response: %d %s", listed.Code, listed.Body.String())
	}
	failed := request("/batches/"+batch.ID+"/failed-segments", "batchId", batch.ID, handler.GetFailedBatchSegments)
	var candidates struct {
		SegmentIDs []string `json:"segment_ids"`
	}
	if failed.Code != http.StatusOK || json.Unmarshal(failed.Body.Bytes(), &candidates) != nil || !reflect.DeepEqual(candidates.SegmentIDs, []string{"segment-1"}) {
		t.Fatalf("retry candidate response: %d %s", failed.Code, failed.Body.String())
	}
	missing := request("/batches/missing/failed-segments", "batchId", "missing", handler.GetFailedBatchSegments)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("missing batch should be 404: %d %s", missing.Code, missing.Body.String())
	}
}
