package subjective

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
	"edugrade-enterprise/services/api-gateway/internal/paper"
)

type materialPreflightAdapter struct {
	LLMGradingAdapter
	policy          ModelPolicy
	wrapper         string
	err             error
	capabilityCalls int
}

func (a *materialPreflightAdapter) Policy() ModelPolicy { return a.policy }
func (a *materialPreflightAdapter) ReferenceMaterialCapability(context.Context) (string, error) {
	a.capabilityCalls++
	return a.wrapper, a.err
}

func TestReferenceMaterialPreflightRequiresCapabilityAndExactConfiguredPolicy(t *testing.T) {
	for _, scenario := range []string{"missing_callback", "missing_capability", "missing_policy", "policy_mismatch", "capability_failed", "invalid_wrapper", "approval_denied", "approved"} {
		t.Run(scenario, func(t *testing.T) {
			policy := ModelPolicy{ModelVersion: "actual-model-v1", PromptVersion: "actual-prompt-v1", MinConfidence: .8}
			adapter := &materialPreflightAdapter{policy: policy, wrapper: strings.Repeat("a", 64)}
			value := Context{Subject: "mathematics", GradeLevel: "senior", Question: paper.Question{QuestionType: "short_answer"}, Rubric: paper.Rubric{Version: "rubric-frozen-v1"}}
			calls := 0
			h := NewHandler(nil, adapter, nil)
			h.mathAdapter = adapter
			h.WithReferenceMaterialPreflight(func(_ context.Context, tenant string, actual Context, requested ModelPolicy, wrapper string) error {
				calls++
				if tenant != "tenant-a" || actual.Subject != value.Subject || actual.GradeLevel != value.GradeLevel || actual.Rubric.Version != value.Rubric.Version || actual.Question.QuestionType != value.Question.QuestionType || requested != policy || wrapper != adapter.wrapper {
					t.Fatal("preflight scope drifted from the actual math adapter or frozen context")
				}
				if scenario == "approval_denied" {
					return errors.New("synthetic private authorization failure")
				}
				return nil
			})
			switch scenario {
			case "missing_callback":
				h.referenceMaterialPreflight = nil
			case "missing_capability":
				h.mathAdapter = &mathHandlerAdapter{}
			case "missing_policy":
				adapter.policy.ModelVersion = ""
			case "policy_mismatch":
				h.adapter = &materialPreflightAdapter{policy: ModelPolicy{ModelVersion: "other", PromptVersion: policy.PromptVersion}}
			case "capability_failed":
				adapter.err = errors.New("synthetic private capability failure")
			case "invalid_wrapper":
				adapter.wrapper = "invalid"
			}
			err := h.preflightReferenceMaterial(context.Background(), "tenant-a", value)
			if scenario == "approved" {
				if err != nil || calls != 1 || adapter.capabilityCalls != 1 {
					t.Fatalf("approved scope: %v calls=%d", err, calls)
				}
				return
			}
			want := "reference_material_capability_unavailable"
			if scenario == "approval_denied" {
				want = "reference_material_image_not_authorized"
			}
			assertGradingAgentV2ErrorCode(t, err, want)
			if scenario != "approval_denied" && calls != 0 {
				t.Fatal("approval callback reached without a usable capability")
			}
		})
	}
}

func TestReferenceMaterialBatchPreflightFailureDoesNotPersistPlanOrRun(t *testing.T) {
	h, store, adapter, value := newMathHandlerFixture(t, false)
	value.ReferenceMaterials = &paper.ReferenceMaterialBinding{ID: "manifest-1", Mode: "reference_images", Revision: 1, ContentHash: strings.Repeat("a", 64), ImageCount: 1}
	store.AddContext("tenant-a", value.SegmentID, value)
	batch, err := store.CreateBatch(context.Background(), "tenant-a", "teacher-1", CreateBatchInput{IdempotencyKey: "material-preflight-batch", SegmentIDs: []string{value.SegmentID}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.loadOrCreateBatchEnqueuePlan(context.Background(), "tenant-a", "teacher-1", batch)
	assertGradingAgentV2ErrorCode(t, err, "reference_material_capability_unavailable")
	if len(store.runs) != 0 || len(store.enqueuePlans) != 0 || adapter.calls != 0 {
		t.Fatal("failed preflight persisted work or called inference")
	}
}

func TestReferenceMaterialPreflightDoesNotAffectLegacyMathInput(t *testing.T) {
	h, _, _, value := newMathHandlerFixture(t, false)
	calls := 0
	h.WithReferenceMaterialPreflight(func(context.Context, string, Context, ModelPolicy, string) error {
		calls++
		return errors.New("must remain unused")
	})
	for _, binding := range []*paper.ReferenceMaterialBinding{nil, {ID: "manifest-text", Mode: "text_only"}} {
		value.ReferenceMaterials = binding
		if err := h.prepareMathEvidence(context.Background(), "tenant-a", &value); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 0 {
		t.Fatal("legacy single-image path reached material image preflight")
	}
}

func TestReferenceMaterialPreflightErrorResponsesUseOnlyAllowlistedCodes(t *testing.T) {
	for _, code := range []string{"reference_material_capability_unavailable", "reference_material_image_not_authorized", "reference_material_unavailable", "synthetic-secret-provider-error"} {
		w := httptest.NewRecorder()
		writeStoreError(w, httptest.NewRequest(http.MethodPost, "/", nil), &GradingAgentError{Code: code, cause: errors.New("synthetic private cause")})
		want := http.StatusUnprocessableEntity
		if code == "synthetic-secret-provider-error" {
			want = http.StatusInternalServerError
		}
		if w.Code != want || strings.Contains(w.Body.String(), "synthetic") {
			t.Fatalf("unsafe or incorrect error: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestHTTPReferenceMaterialCapabilityUsesAuthenticatedCurrentInstance(t *testing.T) {
	for _, scenario := range []string{"approved", "missing_receipts", "wrong_deployment", "trailing_value", "redirect"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/grading/capabilities" || r.Header.Get("Authorization") != "Bearer "+adapterTestToken {
					t.Errorf("unexpected capability request: %s", r.URL.Path)
				}
				if scenario == "redirect" {
					http.Redirect(w, r, server.URL+"/different-instance", http.StatusTemporaryRedirect)
					return
				}
				deployment := "local-qwen3-4b-q4-k-m"
				if scenario == "wrong_deployment" {
					deployment = "other"
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"features": []string{materialInputFeature}, "math_wrapper_hash": strings.Repeat("a", 64), "max_reference_images": 4, "adapter": "local_llama_cpp", "provider": "local", "deployment": deployment, "region": "on_premise"})
				if scenario == "trailing_value" {
					_, _ = w.Write([]byte("{}"))
				}
			}))
			defer server.Close()
			adapter := testHTTPAdapterV2(server.URL).WithMaterialReceipts(&MaterialReceiptStore{})
			if scenario == "missing_receipts" {
				adapter.materials = nil
			}
			wrapper, err := adapter.ReferenceMaterialCapability(context.Background())
			if scenario == "approved" {
				if err != nil || wrapper != strings.Repeat("a", 64) {
					t.Fatalf("capability=%s %v", wrapper, err)
				}
			} else {
				assertGradingAgentV2ErrorCode(t, err, "reference_material_capability_unavailable")
			}
			if scenario == "missing_receipts" && calls != 0 || scenario != "missing_receipts" && calls != 1 {
				t.Fatalf("unexpected request count: %d", calls)
			}
		})
	}
}

type trialMaterialPreflightStore struct {
	Store
	TrialPersistence
	trial        ScoringTrial
	value        Context
	prepareCalls int
	contextCalls int
}

func (s *trialMaterialPreflightStore) ListScoringTrials(context.Context, string, string, ModelPolicy) ([]ScoringTrial, error) {
	other := s.trial
	other.ID = "unrelated-trial"
	other.Samples = []TrialSample{{SegmentID: "must-not-load"}}
	return []ScoringTrial{other, s.trial}, nil
}
func (s *trialMaterialPreflightStore) LoadContext(_ context.Context, _ string, segment string) (Context, error) {
	s.contextCalls++
	if segment == "must-not-load" {
		return Context{}, ErrNotFound
	}
	value := s.value
	value.SegmentID = segment
	return value, nil
}
func (s *trialMaterialPreflightStore) PrepareScoringTrialEnqueue(context.Context, string, string, string, ModelPolicy) (ScoringTrial, error) {
	s.prepareCalls++
	// 在状态迁移边界停下，测试不需要假装 worker 已接受任务。
	return ScoringTrial{}, ErrTrialConflict
}

type trialMaterialPreflightSource struct {
	referenceMaterialSource
	calls int
	err   error
}

func (s *trialMaterialPreflightSource) Resolve(context.Context, string, string, string, *paper.ReferenceMaterialBinding) (paper.ReferenceMaterials, []paper.ResolvedReferenceImage, error) {
	s.calls++
	return paper.ReferenceMaterials{}, nil, s.err
}

func TestReferenceMaterialTrialPreflightRunsBeforeExposureAndPrepare(t *testing.T) {
	for _, scenario := range []string{"capability_failed", "approval_denied", "material_unavailable", "approved", "expanded", "legacy", "text_only", "wrong_question"} {
		t.Run(scenario, func(t *testing.T) {
			policy := ModelPolicy{ModelVersion: "actual-model-v1", PromptVersion: "actual-prompt-v1", MinConfidence: .8}
			adapter := &materialPreflightAdapter{policy: policy, wrapper: strings.Repeat("a", 64)}
			store := &trialMaterialPreflightStore{trial: ScoringTrial{ID: "trial-1", RunID: "run-1", QuestionID: "question-1", Status: "awaiting_enqueue", Samples: []TrialSample{{SegmentID: "sample-1"}}},
				value: Context{Subject: "mathematics", GradeLevel: "senior", Question: paper.Question{ID: "question-1", QuestionType: "short_answer"}, Rubric: paper.Rubric{Version: "rubric-v1"}, ReferenceMaterials: &paper.ReferenceMaterialBinding{ID: "manifest-1", Mode: "reference_images"}}}
			source := &trialMaterialPreflightSource{}
			h := NewHandler(store, adapter, nil)
			h.mathV2Enabled, h.mathAdapter, h.referenceMaterials = true, adapter, source
			approvals := 0
			h.WithReferenceMaterialPreflight(func(_ context.Context, _ string, value Context, _ ModelPolicy, _ string) error {
				approvals++
				if scenario == "expanded" && value.SegmentID != "remaining-first" {
					t.Fatal("expansion preflight used a sample instead of the expansion batch")
				}
				if scenario == "approval_denied" {
					return errors.New("synthetic private image denial")
				}
				return nil
			})
			switch scenario {
			case "capability_failed":
				adapter.err = errors.New("synthetic capability failure")
			case "material_unavailable":
				source.err = paper.ErrConflict
			case "expanded":
				store.trial.Status = "expanded"
				store.trial.RemainingSegmentIDs = make([]string, 1000)
				for i := range store.trial.RemainingSegmentIDs {
					store.trial.RemainingSegmentIDs[i] = "remaining-first"
				}
			case "legacy":
				store.value.ReferenceMaterials = nil
			case "text_only":
				store.value.ReferenceMaterials.Mode = "text_only"
			case "wrong_question":
				store.value.Question.ID = "other-question"
			}
			r := httptest.NewRequest(http.MethodPost, "/", nil)
			r.SetPathValue("runId", "run-1")
			r.SetPathValue("trialId", "trial-1")
			r = r.WithContext(auth.WithUser(r.Context(), auth.User{TenantID: "tenant-a", ID: "teacher-1"}))
			w := httptest.NewRecorder()
			h.EnqueueScoringTrial(w, r)
			if store.contextCalls != 1 {
				t.Fatalf("preflight loaded %d contexts for one frozen trial scope", store.contextCalls)
			}
			blocked := scenario == "capability_failed" || scenario == "approval_denied" || scenario == "material_unavailable" || scenario == "wrong_question"
			if blocked && store.prepareCalls != 0 {
				t.Fatal("failed material preflight changed trial state or exposure")
			}
			if !blocked && store.prepareCalls != 1 {
				t.Fatal("approved or legacy scope did not reach the original Prepare boundary")
			}
			if scenario == "capability_failed" || scenario == "approval_denied" || scenario == "material_unavailable" {
				if w.Code != http.StatusUnprocessableEntity {
					t.Fatalf("expected material 422: %d %s", w.Code, w.Body.String())
				}
			}
			if scenario == "legacy" || scenario == "text_only" {
				if source.calls != 0 || adapter.capabilityCalls != 0 || approvals != 0 {
					t.Fatal("legacy trial reached material or math preflight")
				}
			}
			if scenario == "expanded" && (source.calls != 1 || adapter.capabilityCalls != 1 || approvals != 1) {
				t.Fatal("same-question expansion repeated material preflight for every answer")
			}
		})
	}
}

func TestReferenceMaterialPreflightCacheIsLimitedToSameRequestAndFrozenScope(t *testing.T) {
	adapter := &materialPreflightAdapter{policy: ModelPolicy{ModelVersion: "model-v1", PromptVersion: "prompt-v1", MinConfidence: .8}, wrapper: strings.Repeat("a", 64)}
	source := &trialMaterialPreflightSource{}
	h := NewHandler(nil, adapter, nil)
	h.mathV2Enabled, h.mathAdapter, h.referenceMaterials = true, adapter, source
	approvals := 0
	h.WithReferenceMaterialPreflight(func(context.Context, string, Context, ModelPolicy, string) error { approvals++; return nil })
	value := Context{Subject: "mathematics", Question: paper.Question{ID: "question-1"}, ReferenceMaterials: &paper.ReferenceMaterialBinding{ID: "manifest-1", Mode: "reference_images", Revision: 1}}
	ctx := withReferencePreflightCache(context.Background())
	// trial 检查与随后建计划复用同一个 context；1000 个学生答案不会重复传输同一参考图检查。
	if err := h.prepareReferenceMaterial(ctx, "tenant-a", &value); err != nil {
		t.Fatal(err)
	}
	ctx = withReferencePreflightCache(ctx)
	for i := 0; i < 1000; i++ {
		if err := h.prepareReferenceMaterial(ctx, "tenant-a", &value); err != nil {
			t.Fatal(err)
		}
	}
	if source.calls != 1 || adapter.capabilityCalls != 1 || approvals != 1 {
		t.Fatal("same-request frozen scope repeated material I/O")
	}
	value.ReferenceMaterials.Revision++
	if err := h.prepareReferenceMaterial(ctx, "tenant-a", &value); err != nil {
		t.Fatal(err)
	}
	value.AssessmentSnapshot.ID = "different-snapshot"
	if err := h.prepareReferenceMaterial(ctx, "tenant-a", &value); err != nil {
		t.Fatal(err)
	}
	adapter.policy.PromptVersion = "different-prompt"
	if err := h.prepareReferenceMaterial(ctx, "tenant-a", &value); err != nil {
		t.Fatal(err)
	}
	if err := h.prepareReferenceMaterial(withReferencePreflightCache(context.Background()), "tenant-a", &value); err != nil {
		t.Fatal(err)
	}
	if source.calls != 5 || adapter.capabilityCalls != 5 || approvals != 5 {
		t.Fatal("preflight cache crossed a request, snapshot, material revision or actual policy boundary")
	}
}

func TestReferenceMaterialFailedPreflightIsNotCached(t *testing.T) {
	adapter := &materialPreflightAdapter{policy: ModelPolicy{ModelVersion: "model-v1", PromptVersion: "prompt-v1", MinConfidence: .8}, wrapper: strings.Repeat("a", 64), err: errors.New("synthetic capability unavailable")}
	source := &trialMaterialPreflightSource{}
	h := NewHandler(nil, adapter, nil)
	h.mathV2Enabled, h.mathAdapter, h.referenceMaterials = true, adapter, source
	h.WithReferenceMaterialPreflight(func(context.Context, string, Context, ModelPolicy, string) error { return nil })
	value := Context{Subject: "mathematics", ReferenceMaterials: &paper.ReferenceMaterialBinding{ID: "manifest-1", Mode: "reference_images"}}
	ctx := withReferencePreflightCache(context.Background())
	if err := h.prepareReferenceMaterial(ctx, "tenant-a", &value); err == nil {
		t.Fatal("missing capability was accepted")
	}
	adapter.err = nil
	if err := h.prepareReferenceMaterial(ctx, "tenant-a", &value); err != nil {
		t.Fatal(err)
	}
	if source.calls != 2 || adapter.capabilityCalls != 2 {
		t.Fatal("failed material preflight was cached")
	}
}
