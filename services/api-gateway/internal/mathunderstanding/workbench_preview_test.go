package mathunderstanding

import (
	"bytes"
	"context"
	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/workerruntime"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type previewCropStub struct {
	hash  string
	err   error
	calls int
}

type failingVerificationQueue struct{ workerruntime.Store }

func (s failingVerificationQueue) CreateTask(context.Context, string, string, workerruntime.CreateTaskInput) (workerruntime.Task, error) {
	return workerruntime.Task{}, errors.New("queue unavailable")
}

func TestCorrectionReportsSavedAndVerificationSchedulingSeparately(t *testing.T) {
	for _, scenario := range []string{"queued", "unavailable", "failed"} {
		t.Run(scenario, func(t *testing.T) {
			assertCorrectionSavedWithSchedulingStatus(t, scenario)
		})
	}
}

func (s *previewCropStub) LoadActiveMathCropHash(_ context.Context, tenantID, segmentID string) (string, error) {
	s.calls++
	if tenantID != "tenant-a" || segmentID == "" {
		return "", ErrNotFound
	}
	return s.hash, s.err
}

func TestWorkbenchRubricPreviewRejectsCropReplacementAndFailedReads(t *testing.T) {
	for _, scenario := range []string{"current", "replaced", "blank", "invalid", "read_failure", "during_calculation", "unassigned"} {
		t.Run(scenario, func(t *testing.T) {
			assertPreviewRejectsStaleCrop(t, scenario)
		})
	}
}

// 修正保存与验证调度分别报告；队列失败不能把已保存修正伪装为未接受。
func assertCorrectionSavedWithSchedulingStatus(t *testing.T, scenario string) {
	t.Helper()
	artifacts := NewMemoryStore()
	corrections := NewMemoryCorrectionStore(artifacts)
	ctx := context.Background()
	base, err := artifacts.CreateArtifact(ctx, "tenant-a", validInput())
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandler(artifacts, corrections, nil, &assignmentLookupStub{allowed: true}, nil)
	queue := workerruntime.NewMemoryStore()
	if scenario == "queued" {
		h.WithRuntime(queue)
	} else if scenario == "failed" {
		h.WithRuntime(failingVerificationQueue{queue})
	}
	revision := int64(0)
	raw, _ := json.Marshal(CreateCorrectionInput{ExpectedArtifactVersion: base.Version, ExpectedCorrectionRevision: &revision,
		Operations: []CorrectionOperation{{Type: "move_step", TargetID: "step-1"}}, CorrectedContract: base.CreateArtifactInput})
	r := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(raw))
	r.SetPathValue("artifactId", base.ID)
	r = r.WithContext(auth.WithUser(r.Context(), auth.User{ID: "teacher", TenantID: "tenant-a"}))
	w := httptest.NewRecorder()
	h.CreateCorrection(w, r)
	var response struct {
		Correction Correction `json:"correction"`
		Status     string     `json:"verification_status"`
		TaskID     string     `json:"verification_task_id"`
	}
	fields := map[string]json.RawMessage{}
	if w.Code != http.StatusCreated || json.Unmarshal(w.Body.Bytes(), &response) != nil || json.Unmarshal(w.Body.Bytes(), &fields) != nil || response.Status != scenario || response.Correction.Revision != 1 {
		t.Fatalf("save status=%d body=%s", w.Code, w.Body.String())
	}
	if scenario == "queued" {
		if _, present := fields["verification_task_id"]; !present || response.TaskID == "" {
			t.Fatal("queued scheduling did not return its task ID")
		}
		task, err := queue.Get(ctx, "tenant-a", response.TaskID)
		if err != nil || task.QueueName != "math-verification" {
			t.Fatalf("claimed queued without task: %#v %v", task, err)
		}
	} else if _, present := fields["verification_task_id"]; present {
		t.Fatal("non-queued scheduling returned a task ID field")
	}
}

// 每个场景使用独立裁剪来源；计算期间替换时必须执行二次读取，未授权人员不得读取。
func assertPreviewRejectsStaleCrop(t *testing.T, scenario string) {
	t.Helper()
	artifacts := NewMemoryStore()
	corrections := NewMemoryCorrectionStore(artifacts)
	frozen, effective := scoringFixture()
	effective.EffectiveContract.InputHash = strings.Repeat("a", 64)
	base, err := artifacts.CreateArtifact(context.Background(), "tenant-a", effective.EffectiveContract)
	if err != nil {
		t.Fatal(err)
	}
	crops := &previewCropStub{hash: base.InputHash}
	rubrics := &frozenRubricStub{frozen: frozen}
	lookup := &assignmentLookupStub{allowed: true}
	want := http.StatusConflict
	switch scenario {
	case "current":
		want = http.StatusOK
	case "replaced":
		crops.hash = strings.Repeat("b", 64)
	case "blank":
		crops.hash = ""
	case "invalid":
		crops.hash = "not-a-hash"
	case "read_failure":
		crops.err = errors.New("storage unavailable")
		want = http.StatusInternalServerError
	case "during_calculation":
		rubrics.mutate = func() { crops.hash = strings.Repeat("b", 64) }
	case "unassigned":
		lookup.allowed = false
		want = http.StatusForbidden
	}
	h := NewHandler(artifacts, corrections, nil, lookup, nil).WithFrozenRubrics(rubrics).WithActiveMathCrops(crops)
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.SetPathValue("segmentId", base.AnswerSegmentID)
	r = r.WithContext(auth.WithUser(r.Context(), auth.User{ID: "teacher", TenantID: "tenant-a"}))
	w := httptest.NewRecorder()
	h.GetRubricScore(w, r)
	if w.Code != want {
		t.Fatalf("status=%d want=%d body=%s", w.Code, want, w.Body.String())
	}
	if scenario == "unassigned" && crops.calls != 0 {
		t.Fatal("unassigned teacher read crop")
	}
	if scenario == "during_calculation" && crops.calls != 2 {
		t.Fatal("crop was not rechecked after scoring")
	}
	if want != http.StatusOK && strings.Contains(w.Body.String(), "verified_score") {
		t.Fatal("stale preview leaked a score")
	}
}
