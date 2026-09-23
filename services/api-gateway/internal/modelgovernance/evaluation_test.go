package modelgovernance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"edugrade-enterprise/services/api-gateway/internal/auth"
)

func TestEvaluationValidationSeparatesFixtureFromQualityEvidence(t *testing.T) {
	runInput := validEvaluationRunInput()
	if err := ValidateEvaluationRunInput(runInput); err != nil {
		t.Fatal(err)
	}
	run := EvaluationRun{
		EvidenceClass: runInput.EvidenceClass,
		SampleCount:   runInput.SampleCount,
		RepeatCount:   runInput.RepeatCount,
	}
	candidate := validEvaluationCandidateInput("deployment-1", run)
	if err := ValidateEvaluationCandidateInput(candidate, run); err != nil {
		t.Fatal(err)
	}
	candidate.TeacherReviewedSamples = candidate.EvaluatedSamples
	candidate.TeacherAcceptedSamples = candidate.EvaluatedSamples
	if !errors.Is(ValidateEvaluationCandidateInput(candidate, run), ErrInvalidEvaluation) {
		t.Fatal("protocol fixtures must not claim teacher acceptance evidence")
	}

	run.EvidenceClass = EvaluationEvidenceAuthorizedFrozenSet
	runInput.EvidenceClass = EvaluationEvidenceAuthorizedFrozenSet
	if !errors.Is(ValidateEvaluationRunInput(runInput), ErrInvalidEvaluation) {
		t.Fatal("authorized frozen-set evidence must include an authorization reference")
	}
	runInput.AuthorizationRef = "dataset-approval-001"
	if err := ValidateEvaluationRunInput(runInput); err != nil {
		t.Fatal(err)
	}
	candidate = validEvaluationCandidateInput("deployment-1", run)
	if !errors.Is(ValidateEvaluationCandidateInput(candidate, run), ErrInvalidEvaluation) {
		t.Fatal("authorized frozen-set evidence must be fully teacher reviewed")
	}
	candidate.TeacherReviewedSamples = candidate.EvaluatedSamples
	candidate.TeacherAcceptedSamples = candidate.EvaluatedSamples - 1
	if err := ValidateEvaluationCandidateInput(candidate, run); err != nil {
		t.Fatal(err)
	}

	metrics := PopulateEvaluationMetrics(EvaluationCandidate{
		EvaluatedSamples:       10,
		TeacherReviewedSamples: 10,
		TeacherAcceptedSamples: 8,
		SeriousErrorSamples:    1,
		EvidenceValidSamples:   9,
		RepeatComparisons:      10,
		StableRepeatSamples:    7,
		TotalCostMicros:        250,
	}).Metrics
	if metrics.TeacherAcceptanceRate != 0.8 ||
		metrics.SeriousErrorRate != 0.1 ||
		metrics.EvidenceValidityRate != 0.9 ||
		metrics.StabilityRate != 0.7 ||
		metrics.AverageCostMicros != 25 {
		t.Fatalf("unexpected derived evaluation metrics: %#v", metrics)
	}
}

func TestMemoryEvaluationLifecycleIsTenantBoundImmutableAndRequiresLocalBaseline(t *testing.T) {
	store := NewMemoryStore()
	tenantID := "tenant-1"
	local, external := seedEvaluationDeployments(t, store, tenantID)
	input := validEvaluationRunInput()
	run, err := store.CreateEvaluationRun(context.Background(), tenantID, "actor", input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateEvaluationRun(context.Background(), tenantID, "actor", input); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate run key must conflict: %v", err)
	}
	if _, err := store.AddEvaluationCandidate(
		context.Background(), "tenant-2", "actor", run.ID,
		validEvaluationCandidateInput(local.ID, run),
	); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant evaluation candidate must be hidden: %v", err)
	}
	localCandidate, err := store.AddEvaluationCandidate(
		context.Background(), tenantID, "actor", run.ID,
		validEvaluationCandidateInput(local.ID, run),
	)
	if err != nil {
		t.Fatal(err)
	}
	if localCandidate.ModelVersion != local.ModelVersion {
		t.Fatalf("candidate did not snapshot governed model version: %#v", localCandidate)
	}
	if _, err := store.CompleteEvaluationRun(
		context.Background(), tenantID, "actor", run.ID, "complete too early",
	); !errors.Is(err, ErrInvalidEvaluation) {
		t.Fatalf("single-candidate evaluation must not complete: %v", err)
	}
	if _, err := store.AddEvaluationCandidate(
		context.Background(), tenantID, "actor", run.ID,
		validEvaluationCandidateInput(local.ID, run),
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate deployment candidate must conflict: %v", err)
	}
	if _, err := store.AddEvaluationCandidate(
		context.Background(), tenantID, "actor", run.ID,
		validEvaluationCandidateInput(external.ID, run),
	); err != nil {
		t.Fatal(err)
	}
	completed, err := store.CompleteEvaluationRun(
		context.Background(), tenantID, "actor", run.ID, "freeze comparison report",
	)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != EvaluationStatusCompleted ||
		completed.CompletedAt == nil ||
		len(completed.Candidates) != 2 {
		t.Fatalf("evaluation did not complete with immutable comparison: %#v", completed)
	}
	if _, err := store.AddEvaluationCandidate(
		context.Background(), tenantID, "actor", run.ID,
		validEvaluationCandidateInput("another", run),
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("completed evaluation accepted another candidate: %v", err)
	}
	invalidated, err := store.InvalidateEvaluationRun(
		context.Background(), tenantID, "actor", run.ID, "dataset authorization withdrawn",
	)
	if err != nil || invalidated.Status != EvaluationStatusInvalidated || invalidated.InvalidatedAt == nil {
		t.Fatalf("evaluation was not invalidated: %#v %v", invalidated, err)
	}
	if _, err := store.InvalidateEvaluationRun(
		context.Background(), tenantID, "actor", run.ID, "repeat",
	); !errors.Is(err, ErrConflict) {
		t.Fatalf("repeat invalidation must conflict: %v", err)
	}
}

func TestManagedEvaluationCandidatesUseTenantConfigAndFrozenIdentity(t *testing.T) {
	store := NewMemoryStore()
	tenantID := "school-1"
	for _, id := range []string{"model-a", "model-b"} {
		store.managedConfigs[id] = ManagedAPIConfig{
			ID: id, TenantID: tenantID, ProviderKey: "deepseek", ModelName: id,
			ModelVersion: "version-1", Status: "active",
			LastCapabilityStatus: "success", LastCapabilityVersion: "structured-json-v3",
		}
	}
	run, err := store.CreateEvaluationRun(context.Background(), tenantID, "actor", validEvaluationRunInput())
	if err != nil {
		t.Fatal(err)
	}
	input := validEvaluationCandidateInput("", run)
	input.ModelConfigID = "model-a"
	if _, err := store.AddEvaluationCandidate(context.Background(), "school-2", "actor", run.ID, input); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-school candidate must be rejected: %v", err)
	}
	first, err := store.AddEvaluationCandidate(context.Background(), tenantID, "actor", run.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if first.DeploymentID != "" || first.ModelConfigID != "model-a" || first.ModelName != "model-a" || first.ModelVersion != "version-1" {
		t.Fatalf("candidate did not freeze managed model identity: %#v", first)
	}
	if _, err := store.AddEvaluationCandidate(context.Background(), tenantID, "actor", run.ID, input); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate managed candidate must conflict: %v", err)
	}
	input.ModelConfigID = "model-b"
	if _, err := store.AddEvaluationCandidate(context.Background(), tenantID, "actor", run.ID, input); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompleteEvaluationRun(context.Background(), tenantID, "actor", run.ID, "complete"); err != nil {
		t.Fatalf("two managed candidates should complete without a legacy deployment: %v", err)
	}
}

func TestEvaluationHandlersUseStrictJSONAndAuditLifecycle(t *testing.T) {
	store := NewMemoryStore()
	audits := auth.NewMemoryStore()
	tenantID := "tenant-1"
	local, external := seedEvaluationDeployments(t, store, tenantID)
	handler := NewHandler(store, audits, NewEnvironmentSecretResolver(t.TempDir()), testBaseline())
	user := auth.User{
		ID:          "actor-1",
		TenantID:    tenantID,
		Permissions: []string{"model:read", "model:evaluation:manage"},
	}

	runInput := validEvaluationRunInput()
	runBody, _ := json.Marshal(runInput)
	create := performHandlerRequest(
		t, user, http.MethodPost, "/api/v1/model-evaluation-runs",
		string(runBody), handler.CreateEvaluationRun,
	)
	if create.Code != http.StatusCreated {
		t.Fatalf("create evaluation returned %d: %s", create.Code, create.Body.String())
	}
	var createPayload struct {
		Run EvaluationRun `json:"evaluation_run"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &createPayload); err != nil {
		t.Fatal(err)
	}
	run := createPayload.Run

	for _, deployment := range []Deployment{local, external} {
		candidateInput := validEvaluationCandidateInput(deployment.ID, run)
		body, _ := json.Marshal(candidateInput)
		request := httptest.NewRequest(
			http.MethodPost,
			"/api/v1/model-evaluation-runs/"+run.ID+"/candidates",
			bytes.NewReader(body),
		)
		request.SetPathValue("id", run.ID)
		request = request.WithContext(auth.WithUser(request.Context(), user))
		response := httptest.NewRecorder()
		handler.AddEvaluationCandidate(response, request)
		if response.Code != http.StatusCreated ||
			!strings.Contains(response.Body.String(), `"metrics"`) {
			t.Fatalf("add evaluation candidate returned %d: %s", response.Code, response.Body.String())
		}
	}

	completeRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/model-evaluation-runs/"+run.ID+"/complete",
		strings.NewReader(`{"reason":"freeze comparison evidence"}`),
	)
	completeRequest.SetPathValue("id", run.ID)
	completeRequest = completeRequest.WithContext(auth.WithUser(completeRequest.Context(), user))
	completeResponse := httptest.NewRecorder()
	handler.CompleteEvaluationRun(completeResponse, completeRequest)
	if completeResponse.Code != http.StatusOK ||
		!strings.Contains(completeResponse.Body.String(), `"status":"completed"`) {
		t.Fatalf("complete evaluation returned %d: %s", completeResponse.Code, completeResponse.Body.String())
	}

	list := performHandlerRequest(
		t, user, http.MethodGet, "/api/v1/model-evaluation-runs",
		"", handler.ListEvaluationRuns,
	)
	if list.Code != http.StatusOK ||
		!strings.Contains(list.Body.String(), run.ID) ||
		!strings.Contains(list.Body.String(), EvaluationEvidenceProtocolFixture) {
		t.Fatalf("list evaluations returned %d: %s", list.Code, list.Body.String())
	}

	invalidateRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/model-evaluation-runs/"+run.ID+"/invalidate",
		strings.NewReader(`{"reason":"fixture evidence superseded"}`),
	)
	invalidateRequest.SetPathValue("id", run.ID)
	invalidateRequest = invalidateRequest.WithContext(auth.WithUser(invalidateRequest.Context(), user))
	invalidateResponse := httptest.NewRecorder()
	handler.InvalidateEvaluationRun(invalidateResponse, invalidateRequest)
	if invalidateResponse.Code != http.StatusOK ||
		!strings.Contains(invalidateResponse.Body.String(), `"status":"invalidated"`) {
		t.Fatalf("invalidate evaluation returned %d: %s", invalidateResponse.Code, invalidateResponse.Body.String())
	}

	records, err := audits.ListAudits(context.Background(), tenantID, auth.AuditFilter{Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(records)
	for _, action := range []string{
		"model.evaluation_created",
		"model.evaluation_candidate_added",
		"model.evaluation_completed",
		"model.evaluation_invalidated",
	} {
		if !bytes.Contains(raw, []byte(action)) {
			t.Fatalf("missing evaluation audit action %s: %s", action, raw)
		}
	}
}

func TestEvaluationHandlerRejectsUnknownFields(t *testing.T) {
	handler := NewHandler(NewMemoryStore(), nil, NewEnvironmentSecretResolver(t.TempDir()), testBaseline())
	user := auth.User{
		ID:          "actor",
		TenantID:    "tenant-1",
		Permissions: []string{"model:evaluation:manage"},
	}
	response := performHandlerRequest(
		t, user, http.MethodPost, "/api/v1/model-evaluation-runs",
		`{"run_key":"fixture","unknown":true}`, handler.CreateEvaluationRun,
	)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unknown field returned %d: %s", response.Code, response.Body.String())
	}
}

func seedEvaluationDeployments(t *testing.T, store *MemoryStore, tenantID string) (Deployment, Deployment) {
	t.Helper()
	if err := store.EnsureLocalBaseline(context.Background(), tenantID, testBaseline()); err != nil {
		t.Fatal(err)
	}
	deployments, err := store.ListDeployments(context.Background(), tenantID)
	if err != nil || len(deployments) != 1 {
		t.Fatalf("seed local deployment: %#v %v", deployments, err)
	}
	provider, err := store.CreateProvider(context.Background(), tenantID, "", ProviderInput{
		Key:           "offline-fixture-provider",
		DisplayName:   "Offline fixture provider",
		Kind:          ProviderExternal,
		AdapterType:   "fixture_native",
		CredentialRef: "vault://edugrade/offline-fixture",
		Region:        "fixture-only",
		DataPolicy:    DataPolicy{RetentionMode: "no_store"},
		Status:        "unverified",
	})
	if err != nil {
		t.Fatal(err)
	}
	external, err := store.CreateDeployment(context.Background(), tenantID, "", DeploymentInput{
		ProviderID:        provider.ID,
		Key:               "offline-fixture-model",
		ModelName:         "Offline fixture model",
		ModelVersion:      "fixture-v1",
		Region:            provider.Region,
		CapabilityProfile: "fixture-comparison-v1",
		Modalities:        []string{"text"},
		PricingPolicy:     map[string]any{"meter": "fixture"},
		Status:            "unverified",
		HealthState:       "unverified",
	})
	if err != nil {
		t.Fatal(err)
	}
	return deployments[0], external
}

func validEvaluationRunInput() EvaluationRunInput {
	return EvaluationRunInput{
		Key:              "fixture-comparison-001",
		DisplayName:      "Protocol fixture comparison",
		DatasetReference: "fixture-set-001",
		DatasetSHA256:    strings.Repeat("a", 64),
		EvidenceClass:    EvaluationEvidenceProtocolFixture,
		Subject:          "数学",
		Grade:            "九年级",
		QuestionType:     "short_answer",
		Modality:         "text",
		SampleCount:      10,
		RepeatCount:      2,
		Reason:           "create offline protocol fixture comparison",
	}
}

func validEvaluationCandidateInput(deploymentID string, run EvaluationRun) EvaluationCandidateInput {
	return EvaluationCandidateInput{
		DeploymentID:         deploymentID,
		PromptVersion:        "prompt-v1",
		RubricVersion:        "rubric-v1",
		EvaluatedSamples:     run.SampleCount,
		SeriousErrorSamples:  1,
		EvidenceValidSamples: run.SampleCount - 1,
		RepeatComparisons:    run.SampleCount,
		StableRepeatSamples:  run.SampleCount - 2,
		P95LatencyMS:         120,
		TotalCostMicros:      1000,
		Reason:               "record immutable offline candidate measurements",
	}
}
