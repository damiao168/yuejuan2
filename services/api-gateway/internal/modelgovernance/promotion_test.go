package modelgovernance

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestModelApprovalRequiresCompletedAuthorizedEvidenceAndExactVersions(t *testing.T) {
	store := NewMemoryStore()
	tenantID := "tenant-1"
	local, external := seedEvaluationDeployments(t, store, tenantID)
	runInput := validEvaluationRunInput()
	runInput.Key = "authorized-promotion"
	runInput.DatasetReference = "authorized-promotion-set"
	runInput.EvidenceClass = EvaluationEvidenceAuthorizedFrozenSet
	runInput.AuthorizationRef = "dataset-approval-001"
	run, err := store.CreateEvaluationRun(context.Background(), tenantID, "actor", runInput)
	if err != nil {
		t.Fatal(err)
	}
	for _, deployment := range []Deployment{local, external} {
		input := validEvaluationCandidateInput(deployment.ID, run)
		input.TeacherReviewedSamples = run.SampleCount
		input.TeacherAcceptedSamples = run.SampleCount - 1
		if _, err := store.AddEvaluationCandidate(
			context.Background(), tenantID, "actor", run.ID, input,
		); err != nil {
			t.Fatal(err)
		}
	}
	run, err = store.CompleteEvaluationRun(
		context.Background(), tenantID, "actor", run.ID, "freeze authorized evidence",
	)
	if err != nil {
		t.Fatal(err)
	}
	input := ModelApprovalInput{
		EvaluationRunID:   run.ID,
		DeploymentID:      external.ID,
		ManualReviewRate:  0.25,
		DecisionReference: "decision-001",
		ExpiresAt:         time.Now().UTC().Add(30 * 24 * time.Hour),
		Reason:            "approve bounded shadow suggestion scope",
	}
	approval, err := store.CreateModelApproval(context.Background(), tenantID, "actor", input)
	if err != nil {
		t.Fatal(err)
	}
	if !approval.IsActive(time.Now().UTC()) ||
		approval.AuthorizationRef != run.AuthorizationRef ||
		approval.ModelVersion != external.ModelVersion {
		t.Fatalf("approval did not snapshot authorized evidence: %#v", approval)
	}
	scope := ModelApprovalScope{
		DeploymentID:  external.ID,
		ModelVersion:  approval.ModelVersion,
		PromptVersion: approval.PromptVersion,
		RubricVersion: approval.RubricVersion,
		Subject:       run.Subject,
		Grade:         run.Grade,
		QuestionType:  run.QuestionType,
		Modality:      run.Modality,
	}
	if _, err := RequireActiveModelApproval([]ModelApproval{approval}, scope, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	scope.PromptVersion = "changed-prompt"
	if _, err := RequireActiveModelApproval([]ModelApproval{approval}, scope, time.Now().UTC()); !errors.Is(err, ErrModelNotApproved) {
		t.Fatalf("changed prompt version must fail closed: %v", err)
	}
	if _, err := store.CreateModelApproval(context.Background(), tenantID, "actor", input); !errors.Is(err, ErrConflict) {
		t.Fatalf("duplicate active approval must conflict: %v", err)
	}
	revoked, err := store.RevokeModelApproval(
		context.Background(), tenantID, "actor", approval.ID, ModelApprovalRevokeInput{
			Reason: "quality policy withdrawn", ExpectedRevision: approval.Revision,
		},
	)
	if err != nil || revoked.RevokedAt == nil {
		t.Fatalf("revoke approval: %#v %v", revoked, err)
	}
	if _, err := RequireActiveModelApproval([]ModelApproval{revoked}, scope, time.Now().UTC()); !errors.Is(err, ErrModelNotApproved) {
		t.Fatalf("revoked approval must fail closed: %v", err)
	}
	if _, err := store.RevokeModelApproval(context.Background(), tenantID, "actor", approval.ID, ModelApprovalRevokeInput{
		Reason: "stale retry", ExpectedRevision: approval.Revision,
	}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale approval revocation must conflict by revision: %v", err)
	}
}

func TestManagedApprovalUsesModelConfigAndFrozenScope(t *testing.T) {
	store := NewMemoryStore()
	tenantID := "school-1"
	for _, id := range []string{"model-a", "model-b"} {
		store.managedConfigs[id] = ManagedAPIConfig{
			ID: id, TenantID: tenantID, ProviderKey: "deepseek", ModelName: id,
			ModelVersion: "version-1", Status: "active",
			LastCapabilityStatus: "success", LastCapabilityVersion: "structured-json-v3",
		}
	}
	runInput := validEvaluationRunInput()
	runInput.EvidenceClass = EvaluationEvidenceAuthorizedFrozenSet
	runInput.AuthorizationRef = "approved-dataset"
	run, err := store.CreateEvaluationRun(context.Background(), tenantID, "actor", runInput)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"model-a", "model-b"} {
		candidateInput := validEvaluationCandidateInput("", run)
		candidateInput.ModelConfigID = id
		candidateInput.TeacherReviewedSamples = run.SampleCount
		candidateInput.TeacherAcceptedSamples = run.SampleCount - 1
		if _, err := store.AddEvaluationCandidate(context.Background(), tenantID, "actor", run.ID, candidateInput); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := store.CompleteEvaluationRun(context.Background(), tenantID, "actor", run.ID, "freeze"); err != nil {
		t.Fatal(err)
	}
	approval, err := store.CreateModelApproval(context.Background(), tenantID, "actor", ModelApprovalInput{
		EvaluationRunID: run.ID, ModelConfigID: "model-a", ManualReviewRate: 0.1,
		DecisionReference: "managed-decision", ExpiresAt: time.Now().Add(24 * time.Hour), Reason: "approve",
	})
	if err != nil {
		t.Fatal(err)
	}
	if approval.DeploymentID != "" || approval.ModelConfigID != "model-a" || approval.ModelName != "model-a" {
		t.Fatalf("approval did not freeze managed model: %#v", approval)
	}
	scope := ModelApprovalScope{
		ModelConfigID: "model-a", ModelVersion: approval.ModelVersion,
		PromptVersion: approval.PromptVersion, RubricVersion: approval.RubricVersion,
		Subject: approval.Subject, Grade: approval.Grade,
		QuestionType: approval.QuestionType, Modality: approval.Modality,
	}
	if _, err := RequireActiveModelApproval([]ModelApproval{approval}, scope, time.Now()); err != nil {
		t.Fatal(err)
	}
	scope.ModelVersion = "version-2"
	if _, err := RequireActiveModelApproval([]ModelApproval{approval}, scope, time.Now()); !errors.Is(err, ErrModelNotApproved) {
		t.Fatalf("changed model version must invalidate approval: %v", err)
	}
}

func TestModelApprovalRejectsProtocolFixtureAndExpiredDecision(t *testing.T) {
	store := NewMemoryStore()
	tenantID := "tenant-1"
	local, external := seedEvaluationDeployments(t, store, tenantID)
	runInput := validEvaluationRunInput()
	run, err := store.CreateEvaluationRun(context.Background(), tenantID, "actor", runInput)
	if err != nil {
		t.Fatal(err)
	}
	for _, deployment := range []Deployment{local, external} {
		if _, err := store.AddEvaluationCandidate(
			context.Background(), tenantID, "actor", run.ID,
			validEvaluationCandidateInput(deployment.ID, run),
		); err != nil {
			t.Fatal(err)
		}
	}
	run, err = store.CompleteEvaluationRun(context.Background(), tenantID, "actor", run.ID, "freeze fixture")
	if err != nil {
		t.Fatal(err)
	}
	input := ModelApprovalInput{
		EvaluationRunID:   run.ID,
		DeploymentID:      external.ID,
		ManualReviewRate:  1,
		DecisionReference: "fixture-decision",
		ExpiresAt:         time.Now().UTC().Add(24 * time.Hour),
		Reason:            "must reject protocol fixture",
	}
	if _, err := store.CreateModelApproval(context.Background(), tenantID, "actor", input); !errors.Is(err, ErrInvalidPromotion) {
		t.Fatalf("protocol fixture must never be promoted: %v", err)
	}
	input.ExpiresAt = time.Now().UTC().Add(-time.Minute)
	if err := ValidateModelApprovalInput(input, time.Now().UTC()); !errors.Is(err, ErrInvalidPromotion) {
		t.Fatalf("expired decision must be invalid: %v", err)
	}
}
