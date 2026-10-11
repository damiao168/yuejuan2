package mathunderstanding

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
)

type batch8VerificationFixture struct {
	artifacts   *MemoryStore
	corrections *MemoryCorrectionStore
	runtime     *workerruntime.MemoryStore
	handler     *Handler
	parent      Artifact
	task        workerruntime.Task
	lease       string
}

func newBatch8VerificationFixture(t *testing.T) batch8VerificationFixture {
	t.Helper()
	f := batch8VerificationFixture{artifacts: NewMemoryStore(), runtime: workerruntime.NewMemoryStore()}
	f.corrections = NewMemoryCorrectionStore(f.artifacts)
	f.handler = NewHandler(f.artifacts, f.corrections, nil, nil, nil).WithRuntime(f.runtime)
	var err error
	f.parent, err = f.artifacts.CreateArtifact(context.Background(), "tenant-a", validInput())
	if err != nil {
		t.Fatal(err)
	}
	f.task, err = f.handler.enqueueVerificationTask(context.Background(), "worker", f.parent, 0)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := f.runtime.Claim(context.Background(), "tenant-a", workerruntime.ClaimInput{QueueName: "math-verification", WorkerService: "ocr-worker", WorkerInstanceID: "worker-1", Limit: 1, LeaseSeconds: 300})
	if err != nil || len(claimed) != 1 || claimed[0].LeaseToken == "" {
		t.Fatalf("claim verification: %#v %v", claimed, err)
	}
	f.task, f.lease = claimed[0], claimed[0].LeaseToken
	return f
}

func batch8VerificationCheck() MathVerification {
	return MathVerification{ID: "solve-f1", FormulaID: "formula-1", Kind: "constraint", Status: "verified", ReasonCode: "solution_set_computed", Domain: "real", Engine: "sympy", EngineVersion: "1.14.0", RulesetVersion: "v1", Confidence: 1}
}

func completeBatch8Verification(t *testing.T, f batch8VerificationFixture, lease string, duration int, check MathVerification) *httptest.ResponseRecorder {
	t.Helper()
	// 旧新源码都从不变的HTTP入口触发，测试不依赖新增辅助函数或私有参数结构。
	body, err := json.Marshal(completeVerificationRuntimeRequest{LeaseToken: lease, DurationMS: duration, ArtifactID: f.parent.ID, ArtifactVersion: f.parent.Version, CorrectionRevision: 0, Verifications: []MathVerification{check}})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/internal/math-verification/tasks/"+f.task.ID+"/complete", bytes.NewReader(body))
	req.SetPathValue("taskId", f.task.ID)
	req = req.WithContext(auth.WithUser(req.Context(), auth.User{ID: "worker", TenantID: "tenant-a"}))
	response := httptest.NewRecorder()
	f.handler.CompleteVerificationRuntimeTask(response, req)
	return response
}

func batch8StoredState(t *testing.T, f batch8VerificationFixture) (Artifact, Artifact, workerruntime.Task) {
	t.Helper()
	current, err := f.artifacts.GetLatestArtifact(context.Background(), "tenant-a", f.parent.AnswerSegmentID)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := f.artifacts.GetArtifact(context.Background(), "tenant-a", f.parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := f.runtime.Get(context.Background(), "tenant-a", f.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.CreatedAt.IsZero() || parent.CreatedAt.IsZero() || task.LeaseToken != f.lease {
		t.Fatalf("lost timestamp or lease binding: current=%#v parent=%#v task=%#v", current, parent, task)
	}
	return current, parent, task
}

func reportBatch8VerificationState(t *testing.T, f batch8VerificationFixture, response *httptest.ResponseRecorder) {
	t.Helper()
	current, parent, task := batch8StoredState(t, f)
	artifact := func(item Artifact) map[string]any {
		return map[string]any{"id": item.ID, "version": item.Version, "stage": item.Stage, "current": item.IsCurrent, "parent": item.ParentArtifactID, "revision": item.CorrectionRevision, "contract": item.CreateArtifactInput, "quality": item.QualitySummary}
	}
	// 时间戳和随机租约先校验绑定，再排除于跨运行状态投影；不能声称完整原生事件字节相等。
	proof := map[string]any{
		"http_status": response.Code, "current": artifact(current), "parent": artifact(parent),
		"artifact_count": len(f.artifacts.artifacts), "next_artifact_id": f.artifacts.next,
		"task_status": task.Status, "task_revision": task.Revision, "task_duration": task.DurationMS,
		"result_schema": task.ResultSchemaVersion, "result": task.Result, "result_hash": task.ResultPayloadHash,
	}
	value, err := json.Marshal(proof)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("MATH_VERIFICATION_BATCH8_PROOF %s", value)
}

func assertBatch8AcceptedVerification(t *testing.T, f batch8VerificationFixture) workerruntime.Task {
	t.Helper()
	current, parent, task := batch8StoredState(t, f)
	if current.ID != "math-artifact-2" || current.Stage != "verified" || !current.IsCurrent || current.ParentArtifactID != f.parent.ID || current.CorrectionRevision != 0 {
		t.Fatalf("wrong activated identity: %#v", current)
	}
	if parent.IsCurrent || parent.Stage != "recognition" || !reflect.DeepEqual(parent.CreateArtifactInput, f.parent.CreateArtifactInput) {
		t.Fatalf("recognition parent was rewritten: %#v", parent)
	}
	if len(current.Verifications) != 2 || !reflect.DeepEqual(current.Verifications[1], batch8VerificationCheck()) || current.QualitySummary["verified_count"] != float64(1) {
		t.Fatalf("activated verification payload changed: %#v", current)
	}
	if task.Status != workerruntime.StatusSucceeded || task.CompletedAt == nil || task.DurationMS != 18 || task.ResultSchemaVersion != "math-verification-result-v1" {
		t.Fatalf("lease completion changed: %#v", task)
	}
	want := map[string]any{"artifact_id": current.ID, "artifact_version": float64(2), "parent_artifact_id": parent.ID, "correction_revision": float64(0), "superseded": false}
	if !reflect.DeepEqual(task.Result, want) || task.ResultPayloadHash == "" || len(f.artifacts.artifacts) != 2 || f.artifacts.next != 3 {
		t.Fatalf("activation receipt changed: task=%#v artifacts=%#v next=%d", task, f.artifacts.artifacts, f.artifacts.next)
	}
	return task
}

func prepareBatch8Retry(f batch8VerificationFixture, scenario string) (MathVerification, int) {
	check, wantCode := batch8VerificationCheck(), http.StatusOK
	if scenario == "contract_conflict" {
		check.ReasonCode, wantCode = "different_result", http.StatusConflict
	}
	if scenario == "quality_conflict" {
		f.artifacts.mu.Lock()
		item := f.artifacts.artifacts["tenant-a:math-artifact-2"]
		item.QualitySummary["verified_count"] = float64(99)
		f.artifacts.mu.Unlock()
		wantCode = http.StatusConflict
	}
	return check, wantCode
}

func TestVerificationBatch8AcceptedRetryAndConflict(t *testing.T) {
	for _, scenario := range []string{"same_result_different_duration", "contract_conflict", "quality_conflict"} {
		t.Run(scenario, func(t *testing.T) {
			f := newBatch8VerificationFixture(t)
			first := completeBatch8Verification(t, f, f.lease, 18, batch8VerificationCheck())
			if first.Code != http.StatusOK {
				t.Fatalf("first completion: %d %s", first.Code, first.Body.String())
			}
			accepted := assertBatch8AcceptedVerification(t, f)
			check, wantCode := prepareBatch8Retry(f, scenario)
			response := completeBatch8Verification(t, f, f.lease, 99, check)
			if response.Code != wantCode {
				t.Fatalf("retry changed: %d %s, want %d", response.Code, response.Body.String(), wantCode)
			}
			current, _, task := batch8StoredState(t, f)
			if !reflect.DeepEqual(task, accepted) || current.Verifications[1].ReasonCode != "solution_set_computed" || len(f.artifacts.artifacts) != 2 || f.artifacts.next != 3 {
				t.Fatalf("retry altered accepted state: current=%#v task=%#v", current, task)
			}
			reportBatch8VerificationState(t, f, response)
		})
	}
}

func TestVerificationBatch8LeaseFailureRestoresActivation(t *testing.T) {
	for _, scenario := range []string{"wrong_token", "expired_token"} {
		t.Run(scenario, func(t *testing.T) {
			f := newBatch8VerificationFixture(t)
			lease := "invalid"
			if scenario == "expired_token" {
				f.runtime.ForceExpireLeaseForTest(f.task.ID)
				lease = f.lease
			}
			_, _, before := batch8StoredState(t, f)
			response := completeBatch8Verification(t, f, lease, 18, batch8VerificationCheck())
			current, parent, task := batch8StoredState(t, f)
			if response.Code != http.StatusConflict || !reflect.DeepEqual(task, before) || !reflect.DeepEqual(parent, f.parent) || !reflect.DeepEqual(current, f.parent) {
				t.Fatalf("failed lease left activation: http=%d current=%#v parent=%#v task=%#v", response.Code, current, parent, task)
			}
			if len(f.artifacts.artifacts) != 1 || f.artifacts.next != 2 {
				t.Fatalf("failed lease consumed artifact identity: %#v next=%d", f.artifacts.artifacts, f.artifacts.next)
			}
			reportBatch8VerificationState(t, f, response)
		})
	}
}

func prepareBatch8Superseded(t *testing.T, f batch8VerificationFixture, scenario string) {
	t.Helper()
	if scenario == "teacher_revision" {
		_, err := f.corrections.CreateCorrection(context.Background(), "tenant-a", f.parent.ID, "teacher", CreateCorrectionInput{ExpectedArtifactVersion: f.parent.Version, Operations: []CorrectionOperation{{Type: "move_step", TargetID: "step-1"}}, CorrectedContract: f.parent.CreateArtifactInput})
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	crop := validInput()
	crop.InputHash = "sha256:new-crop"
	if _, err := f.artifacts.CreateArtifact(context.Background(), "tenant-a", crop); err != nil {
		t.Fatal(err)
	}
}

func TestVerificationBatch8SupersededCompletionRetainsCurrentEvidence(t *testing.T) {
	for _, scenario := range []string{"teacher_revision", "new_crop"} {
		t.Run(scenario, func(t *testing.T) {
			f := newBatch8VerificationFixture(t)
			prepareBatch8Superseded(t, f, scenario)
			before, _, _ := batch8StoredState(t, f)
			beforeCount, beforeNext := len(f.artifacts.artifacts), f.artifacts.next
			response := completeBatch8Verification(t, f, f.lease, 18, batch8VerificationCheck())
			current, _, task := batch8StoredState(t, f)
			want := map[string]any{"artifact_id": f.parent.ID, "artifact_version": float64(1), "correction_revision": float64(0), "superseded": true}
			if response.Code != http.StatusOK || response.Body.String() != "{\"superseded\":true}\n" || !reflect.DeepEqual(current, before) || !reflect.DeepEqual(task.Result, want) {
				t.Fatalf("superseded task activated stale evidence: http=%d body=%s current=%#v task=%#v", response.Code, response.Body.String(), current, task)
			}
			if task.Status != workerruntime.StatusSucceeded || task.DurationMS != 18 || len(f.artifacts.artifacts) != beforeCount || f.artifacts.next != beforeNext {
				t.Fatalf("superseded completion changed artifact history: %#v", task)
			}
			reportBatch8VerificationState(t, f, response)
		})
	}
}

type batch8WrappedRuntime struct{ workerruntime.Store }
type batch8WrappedCorrections struct{ CorrectionStore }
type batch8WrappedArtifacts struct{ Store }

func TestVerificationBatch8RejectsUnboundMemoryBackends(t *testing.T) {
	for _, scenario := range []string{"runtime_type", "correction_type", "correction_store", "artifact_type"} {
		t.Run(scenario, func(t *testing.T) {
			f := newBatch8VerificationFixture(t)
			switch scenario {
			case "runtime_type":
				f.handler.runtime = batch8WrappedRuntime{Store: f.runtime}
			case "correction_type":
				f.handler.corrections = batch8WrappedCorrections{CorrectionStore: f.corrections}
			case "correction_store":
				f.handler.corrections = NewMemoryCorrectionStore(NewMemoryStore())
			case "artifact_type":
				f.handler.artifacts = batch8WrappedArtifacts{Store: f.artifacts}
			}
			response := completeBatch8Verification(t, f, f.lease, 18, batch8VerificationCheck())
			current, parent, task := batch8StoredState(t, f)
			if response.Code != http.StatusConflict || !reflect.DeepEqual(current, f.parent) || !reflect.DeepEqual(parent, f.parent) || !reflect.DeepEqual(task, f.task) {
				t.Fatalf("unbound backend mutated source or task: http=%d current=%#v parent=%#v task=%#v", response.Code, current, parent, task)
			}
			if len(f.artifacts.artifacts) != 1 || f.artifacts.next != 2 {
				t.Fatal("unbound backend created a derived artifact")
			}
			reportBatch8VerificationState(t, f, response)
		})
	}
}
