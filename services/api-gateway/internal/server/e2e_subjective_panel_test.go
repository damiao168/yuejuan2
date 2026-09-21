package server

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"edugrade-enterprise/services/api-gateway/internal/gradingevaluation"
	"edugrade-enterprise/services/api-gateway/internal/modelgovernance"
	"edugrade-enterprise/services/api-gateway/internal/subjective"
	"github.com/google/uuid"
)

// Exercises the new stores against every migration with synthetic data only.
// Model calls and production score publication are intentionally absent.
func TestPostgresSubjectivePanelAndEvaluationRoundTrip(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("EDUGRADE_E2E_DATABASE_URL"))
	if dsn == "" {
		t.Skip("EDUGRADE_E2E_DATABASE_URL is required")
	}
	db := e2eOpenPostgresTestDB(t, dsn)
	e2eApplyPostgresMigrations(t, db)
	e2eActivatePostgresDemoUsers(t, db, []string{"tenant_admin"})
	router := e2ePostgresRouter(db)
	token := e2eLoginWithTenant(t, router, "demo", "tenant_admin", "ChangeMe123!")
	suffix := time.Now().UTC().Format("20060102150405.000000000")
	fixture := e2eCreateStory056AcceptanceFixture(t, db, router, token, suffix)
	e2eSeedStory056AcceptanceAnswersCount(t, db, fixture, suffix, 1)

	ctx := context.Background()
	var segmentID, questionID string
	if err := db.QueryRowContext(ctx, `
SELECT seg.id::text,seg.question_id::text
FROM answer_segment seg
JOIN submission sub ON sub.tenant_id=seg.tenant_id AND sub.id=seg.submission_id
WHERE sub.exam_id=$1::uuid ORDER BY seg.created_at DESC LIMIT 1`, fixture.ExamID).Scan(&segmentID, &questionID); err != nil {
		t.Fatal(err)
	}
	answerVersion := "synthetic-answer-v1"
	panels := subjective.NewPostgresStore(db)
	create := subjective.CreatePanelInput{
		AnswerSegmentID: segmentID, AnswerVersion: answerVersion, QuestionID: questionID,
		RubricVersion: "rubric-v1", MaxScore: 1, DecisionConfig: subjective.PanelDecisionConfig{PolicyVersion: "panel-e2e-v1"},
	}
	panel, err := panels.GetOrCreatePanel(ctx, fixture.TenantID, fixture.AdminID, create)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := panels.GetOrCreatePanel(ctx, fixture.TenantID, fixture.AdminID, create)
	if err != nil || replay.ID != panel.ID || replay.AnswerVersion != answerVersion {
		t.Fatalf("active panel was not idempotent: %#v err=%v", replay, err)
	}
	runInput := subjective.CreateRunInput{
		AnswerSegmentID: segmentID, AnswerVersion: answerVersion, QuestionID: questionID,
		RubricVersion: create.RubricVersion, ModelVersion: "panel-model-a", PromptVersion: "panel-prompt-v1",
		MinConfidence: .8, RequestID: "panel:" + panel.ID + ":primary_a", PanelID: panel.ID,
		AgentRole: subjective.AgentRolePrimaryA,
	}
	run, err := panels.GetOrCreateRun(ctx, fixture.TenantID, fixture.AdminID, runInput)
	if err != nil {
		t.Fatal(err)
	}
	loadedRun, err := panels.GetRun(ctx, fixture.TenantID, run.ID)
	if err != nil || loadedRun.PanelID != panel.ID || loadedRun.AgentRole != subjective.AgentRolePrimaryA || loadedRun.Status != subjective.RunQueued {
		t.Fatalf("panel run binding did not round-trip: %#v err=%v", loadedRun, err)
	}
	claimedRun, owner, err := panels.ClaimPanelRun(ctx, fixture.TenantID, run.ID)
	if err != nil || !owner || claimedRun.Status != subjective.RunProcessing || claimedRun.AttemptCount != 1 {
		t.Fatalf("panel role execution was not claimed: %#v owner=%v err=%v", claimedRun, owner, err)
	}
	if _, owner, err := panels.ClaimPanelRun(ctx, fixture.TenantID, run.ID); err != nil || owner {
		t.Fatalf("panel role was claimed twice: owner=%v err=%v", owner, err)
	}
	if _, err := panels.UpdatePanel(ctx, fixture.TenantID, panel.ID, subjective.UpdatePanelInput{
		Status: subjective.PanelComparing, PrimaryBRunID: run.ID,
	}); err != subjective.ErrIdempotencyConflict {
		t.Fatalf("panel accepted a primary_a run in its primary_b slot: %v", err)
	}
	wrongSnapshotRun := runInput
	wrongSnapshotRun.AgentRole = subjective.AgentRolePrimaryB
	wrongSnapshotRun.RequestID = "invalid-panel-snapshot-store-" + suffix
	wrongSnapshotRun.AnswerVersion = "wrong-answer-version"
	if _, err := panels.GetOrCreateRun(ctx, fixture.TenantID, fixture.AdminID, wrongSnapshotRun); err != subjective.ErrIdempotencyConflict {
		t.Fatalf("store did not map frozen snapshot violation: %v", err)
	}
	duplicateRoleRun := runInput
	duplicateRoleRun.RequestID = "duplicate-panel-role-" + suffix
	if _, err := panels.GetOrCreateRun(ctx, fixture.TenantID, fixture.AdminID, duplicateRoleRun); err != subjective.ErrIdempotencyConflict {
		t.Fatalf("store allowed a second run for the same panel role: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
INSERT INTO subjective_grading_run(
  tenant_id,answer_segment_id,answer_version,question_id,rubric_version,
  model_version,prompt_version,min_confidence,request_id,panel_id,agent_role
) VALUES($1::uuid,$2::uuid,'wrong-answer-version',$3::uuid,$4,'model-b','prompt-v1',0.8,$5,$6::uuid,'primary_b')`,
		fixture.TenantID, segmentID, questionID, create.RubricVersion,
		"invalid-panel-snapshot-"+suffix, panel.ID); err == nil {
		t.Fatal("panel accepted a run from a different frozen answer version")
	}
	if _, err := db.ExecContext(ctx, `UPDATE subjective_grading_run SET panel_id=NULL WHERE tenant_id=$1::uuid AND id=$2::uuid`,
		fixture.TenantID, run.ID); err == nil {
		t.Fatal("linked panel run could detach from its frozen panel")
	}
	if _, err = panels.GetOrCreateRun(ctx, fixture.TenantID, fixture.AdminID, subjective.CreateRunInput{
		AnswerSegmentID: segmentID, AnswerVersion: answerVersion, QuestionID: questionID,
		RubricVersion: create.RubricVersion, ModelVersion: "panel-model-a", PromptVersion: "panel-prompt-v1",
		MinConfidence: .8, RequestID: "invalid-panel-role-" + suffix, PanelID: panel.ID,
		AgentRole: subjective.AgentRoleSingle,
	}); err != subjective.ErrInvalidInput {
		t.Fatalf("panel accepted single-agent role: %v", err)
	}
	bInput := runInput
	bInput.AgentRole, bInput.RequestID, bInput.ModelVersion = subjective.AgentRolePrimaryB, "panel:"+panel.ID+":primary_b", "panel-model-b"
	bRun, err := panels.GetOrCreateRun(ctx, fixture.TenantID, fixture.AdminID, bInput)
	if err != nil {
		t.Fatal(err)
	}
	if _, owner, err := panels.ClaimPanelRun(ctx, fixture.TenantID, bRun.ID); err != nil || !owner {
		t.Fatalf("primary B role execution was not claimed: owner=%v err=%v", owner, err)
	}
	if _, err := panels.UpdatePanel(ctx, fixture.TenantID, panel.ID, subjective.UpdatePanelInput{
		Status: subjective.PanelComparing, PrimaryARunID: run.ID, PrimaryBRunID: bRun.ID,
	}); err != subjective.ErrIdempotencyConflict {
		t.Fatalf("panel compared incomplete primary roles: %v", err)
	}
	completeRole := func(roleRun subjective.GradingRun) {
		grade, createErr := panels.CreateGrade(ctx, fixture.TenantID, fixture.AdminID, subjective.Grade{
			AnswerSegmentID: segmentID, QuestionID: questionID, QuestionNo: "Q1", QuestionType: "short_answer",
			AnswerVersion: answerVersion, GraderType: "llm_subjective", ModelVersion: roleRun.ModelVersion,
			PromptVersion: roleRun.PromptVersion, RubricVersion: create.RubricVersion, DeliveryMode: "shadow_only",
			SuggestedScore: 1, MaxScore: 1, Confidence: .9, NeedsHumanReview: true, Status: "succeeded",
			AdapterRequestID: roleRun.RequestID, RunID: roleRun.ID,
		})
		if createErr != nil {
			t.Fatalf("create role grade: %v", createErr)
		}
		if _, updateErr := panels.UpdateRun(ctx, fixture.TenantID, roleRun.ID, subjective.UpdateRunInput{
			Status: subjective.RunSucceeded, GradeID: grade.ID,
		}); updateErr != nil {
			t.Fatalf("complete role run: %v", updateErr)
		}
	}
	completeRole(run)
	completeRole(bRun)
	gap, score := .0, 1.0
	if _, err := panels.UpdatePanel(ctx, fixture.TenantID, panel.ID, subjective.UpdatePanelInput{
		Status: subjective.PanelComparing, PrimaryARunID: run.ID, PrimaryBRunID: bRun.ID,
	}); err != nil {
		t.Fatalf("panel could not enter comparing state: %v", err)
	}
	resolved, err := panels.UpdatePanel(ctx, fixture.TenantID, panel.ID, subjective.UpdatePanelInput{
		Status: subjective.PanelResolved, PrimaryARunID: run.ID, PrimaryBRunID: bRun.ID, ScoreA: &score, ScoreB: &score,
		ScoreGap: &gap, CriterionGap: &gap, ResolvedScore: &score,
		ResolutionSource: subjective.ResolutionPrimaryConsensus,
	})
	if err != nil || resolved.CompletedAt == nil || resolved.ResolvedScore == nil || *resolved.ResolvedScore != score {
		t.Fatalf("panel resolution did not persist: %#v err=%v", resolved, err)
	}
	if _, err := panels.UpdatePanel(ctx, fixture.TenantID, panel.ID, subjective.UpdatePanelInput{
		Status: subjective.PanelComparing,
	}); err != subjective.ErrIdempotencyConflict {
		t.Fatalf("resolved panel regressed to comparing: %v", err)
	}
	completedReplay, err := panels.GetOrCreatePanel(ctx, fixture.TenantID, fixture.AdminID, create)
	if err != nil || completedReplay.ID != panel.ID || completedReplay.Status != subjective.PanelResolved {
		t.Fatalf("completed answer version created a second panel: %#v err=%v", completedReplay, err)
	}
	changedScore := create
	changedScore.MaxScore = 2
	if _, err := panels.GetOrCreatePanel(ctx, fixture.TenantID, fixture.AdminID, changedScore); err != subjective.ErrIdempotencyConflict {
		t.Fatalf("changed max score was accepted under an existing panel identity: %v", err)
	}
	humanInput := create
	humanInput.AnswerVersion = "second-" + answerVersion
	humanPanel, err := panels.GetOrCreatePanel(ctx, fixture.TenantID, fixture.AdminID, humanInput)
	if err != nil || humanPanel.ID == panel.ID {
		t.Fatalf("new answer version reused old panel: %#v err=%v", humanPanel, err)
	}
	humanPanel, err = panels.UpdatePanel(ctx, fixture.TenantID, humanPanel.ID, subjective.UpdatePanelInput{
		Status: subjective.PanelHumanReview, ResolutionSource: subjective.ResolutionHuman,
		TriggerCodes: []string{"arbiter_unresolved"},
	})
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := panels.RoutePanelHumanReview(ctx, fixture.TenantID, fixture.AdminID, humanPanel)
	if err != nil || taskID == "" {
		t.Fatalf("human fallback did not create a review task: %v", err)
	}
	if replayTaskID, err := panels.RoutePanelHumanReview(ctx, fixture.TenantID, fixture.AdminID, humanPanel); err != nil || replayTaskID != taskID {
		t.Fatalf("panel review task was not idempotent: first=%s replay=%s err=%v", taskID, replayTaskID, err)
	}
	newerHumanInput := create
	newerHumanInput.AnswerVersion = "third-" + answerVersion
	newerHumanPanel, err := panels.GetOrCreatePanel(ctx, fixture.TenantID, fixture.AdminID, newerHumanInput)
	if err != nil {
		t.Fatal(err)
	}
	newerHumanPanel, err = panels.UpdatePanel(ctx, fixture.TenantID, newerHumanPanel.ID, subjective.UpdatePanelInput{
		Status: subjective.PanelHumanReview, ResolutionSource: subjective.ResolutionHuman,
		TriggerCodes: []string{"primary_agent_failure"},
	})
	if err != nil {
		t.Fatal(err)
	}
	newerTaskID, err := panels.RoutePanelHumanReview(ctx, fixture.TenantID, fixture.AdminID, newerHumanPanel)
	if err != nil || newerTaskID == "" || newerTaskID == taskID {
		t.Fatalf("new answer version reused an older panel review task: old=%s new=%s err=%v", taskID, newerTaskID, err)
	}

	evaluations := gradingevaluation.NewPostgresStore(db)
	evalRun, err := evaluations.CreateRun(ctx, fixture.TenantID, fixture.AdminID, gradingevaluation.CreateRunInput{
		Key: "panel-e2e-" + suffix, DisplayName: "Synthetic panel E2E", ModelReference: "panel-shadow",
		PromptVersion: "panel-prompt-v1", RubricVersion: "rubric-v1", DatasetReference: "synthetic",
		DatasetSHA256: strings.Repeat("a", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	observation, err := evaluations.AddPanelObservation(ctx, fixture.TenantID, evalRun.ID, gradingevaluation.PanelObservation{
		ResponseKey: "response-1", ResponseFingerprint: strings.Repeat("b", 64), EducationStage: "senior", Subject: "physics", Archetype: "short_constructed",
		ReferenceKind: gradingevaluation.ReferenceHumanAdjudicated, ReferenceScore: 1, MaxScore: 1,
		ScoreA: 1, ScoreB: 1, ResolvedScore: &score, ResolutionSource: subjective.ResolutionPrimaryConsensus,
		ReferenceReviewers: 2, ReferenceAdjudicated: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	items, err := evaluations.ListPanelObservations(ctx, fixture.TenantID, evalRun.ID)
	if err != nil || len(items) != 1 || items[0].ID != observation.ID {
		t.Fatalf("panel evaluation observation did not round-trip: %#v err=%v", items, err)
	}
	metrics, err := gradingevaluation.CalculateStoredPanelMetrics(ctx, evaluations, fixture.TenantID, evalRun.ID)
	if err != nil || metrics.SampleCount != 1 || metrics.ABExactAgreementRate != 1 {
		t.Fatalf("persisted panel metrics failed: %#v err=%v", metrics, err)
	}
	completedEvaluation, _, err := gradingevaluation.NewService(evaluations).CompletePanel(ctx, fixture.TenantID, evalRun.ID)
	if err != nil || completedEvaluation.Status != gradingevaluation.RunCompleted || completedEvaluation.ObservationCount != 1 {
		t.Fatalf("panel evaluation did not complete immutably: %#v err=%v", completedEvaluation, err)
	}
	policyVersion := "panel-physics-e2e-" + strings.ReplaceAll(suffix, ".", "")
	policyService := subjective.NewPanelPolicyService(panels, evaluations)
	panelPolicy, err := policyService.Create(ctx, fixture.TenantID, fixture.AdminID, e2ePanelPolicyInput(policyVersion, 2, 1))
	if err != nil || panelPolicy.Status != subjective.PanelPolicyShadow {
		t.Fatalf("shadow panel policy did not persist: %#v err=%v", panelPolicy, err)
	}
	if _, report, err := policyService.EvaluateAndApprove(ctx, fixture.TenantID, fixture.AdminID, panelPolicy.ID, evalRun.ID); !errors.Is(err, subjective.ErrPanelPolicyNotReady) || report.Ready {
		t.Fatalf("insufficient real shadow evidence did not fail closed: report=%#v err=%v", report, err)
	}

	readyRun, err := evaluations.CreateRun(ctx, fixture.TenantID, fixture.AdminID, gradingevaluation.CreateRunInput{
		Key: "panel-ready-e2e-" + suffix, DisplayName: "Synthetic ready panel E2E", ModelReference: "panel-shadow",
		PromptVersion: "panel-prompt-v1", RubricVersion: "rubric-v1", DatasetReference: "synthetic-ready",
		DatasetSHA256: strings.Repeat("c", 64),
	})
	if err != nil {
		t.Fatal(err)
	}
	for index, reference := range []float64{0, 1, 2, 3} {
		item := gradingevaluation.PanelObservation{
			ResponseKey: "ready-" + string(rune('a'+index)), ResponseFingerprint: strings.Repeat(string(rune('a'+index)), 64),
			EducationStage: "senior", Subject: "physics", Archetype: "short_constructed",
			ReferenceKind: gradingevaluation.ReferenceHumanAdjudicated, ReferenceScore: reference, MaxScore: 3,
			ReferenceReviewers: 2, ReferenceAdjudicated: true,
		}
		if index < 2 {
			resolved := reference
			item.ScoreA, item.ScoreB, item.ResolvedScore, item.ResolutionSource = reference, reference, &resolved, subjective.ResolutionPrimaryConsensus
		} else {
			arbiter, resolved := reference, reference
			item.ScoreA, item.ScoreB, item.ScoreC, item.ResolvedScore = reference-2, reference-1, &arbiter, &resolved
			item.ArbitrationTriggered, item.ResolutionSource = true, subjective.ResolutionArbiter
		}
		if _, err := evaluations.AddPanelObservation(ctx, fixture.TenantID, readyRun.ID, item); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := gradingevaluation.NewService(evaluations).CompletePanel(ctx, fixture.TenantID, readyRun.ID); err != nil {
		t.Fatal(err)
	}
	readyPolicyVersion := policyVersion + "-ready"
	readyPolicy, err := policyService.Create(ctx, fixture.TenantID, fixture.AdminID, e2ePanelPolicyInput(readyPolicyVersion, 4, 2))
	if err != nil {
		t.Fatal(err)
	}
	approvedPolicy, readyReport, err := policyService.EvaluateAndApprove(ctx, fixture.TenantID, fixture.AdminID, readyPolicy.ID, readyRun.ID)
	if err != nil || !readyReport.Ready || approvedPolicy.Status != subjective.PanelPolicyApproved {
		t.Fatalf("ready policy approval did not round-trip: policy=%#v report=%#v err=%v", approvedPolicy, readyReport, err)
	}
	if _, err := evaluations.InvalidateRun(ctx, fixture.TenantID, readyRun.ID, "must be blocked while approved", time.Now().UTC()); err == nil {
		t.Fatal("approved policy evaluation was invalidated before its policy")
	}
	if _, err := policyService.Invalidate(ctx, fixture.TenantID, fixture.AdminID, approvedPolicy.ID, "replacement calibration"); err != nil {
		t.Fatal(err)
	}
	if invalidatedRun, err := evaluations.InvalidateRun(ctx, fixture.TenantID, readyRun.ID, "replacement calibration", time.Now().UTC()); err != nil || invalidatedRun.Status != gradingevaluation.RunInvalid {
		t.Fatalf("evaluation did not invalidate after policy retirement: %#v err=%v", invalidatedRun, err)
	}
	modelConfigs := []string{uuid.NewString(), uuid.NewString(), uuid.NewString()}
	for index, id := range modelConfigs {
		if _, err := db.ExecContext(ctx, `
INSERT INTO managed_model_api_config(
 id,tenant_id,provider_key,display_name,adapter_type,base_url,model_name,model_version,
 credential_ciphertext,credential_nonce,status,last_capability_status,last_capability_probe_version,created_by
) VALUES($1::uuid,$2::uuid,$3,$4,'openai_compatible','https://example.invalid/v1',$5,$5,
 decode(repeat('00',24),'hex'),decode(repeat('00',12),'hex'),'active','success',$6,$7::uuid)`,
			id, fixture.TenantID, "panel-e2e-"+strings.ReplaceAll(suffix, ".", "")+"-"+string(rune('a'+index)),
			"Synthetic panel model", "synthetic-model-"+string(rune('a'+index)), modelgovernance.ManagedCapabilityProbeVersion,
			fixture.AdminID); err != nil {
			t.Fatalf("seed synthetic governed model %d: %v", index, err)
		}
	}
	roleStore := modelgovernance.NewPostgresStore(db, nil)
	for index, role := range []string{modelgovernance.ModelRolePrimaryA, modelgovernance.ModelRolePrimaryB, modelgovernance.ModelRoleArbiter} {
		_, err := roleStore.SaveModelRoleBinding(ctx, fixture.TenantID, fixture.AdminID, modelgovernance.SaveModelRoleBindingInput{
			EducationStage: "senior", SubjectCode: "physics", ArchetypeCode: "short_constructed",
			AgentRole: role, ManagedModelAPIConfigID: modelConfigs[index], PromptVersion: "panel-prompt-v1",
			StrengthRank: 1 + index/2,
		})
		if err != nil {
			t.Fatalf("save role %s: %v", role, err)
		}
	}
	bindings, err := modelgovernance.ResolvePanelRoleBindings(ctx, roleStore, fixture.TenantID, "senior", "physics", "short_constructed")
	if err != nil || bindings.Arbiter.ManagedModelAPIConfigID != modelConfigs[2] || bindings.Arbiter.StrengthRank != 2 {
		t.Fatalf("governed role binding did not round-trip: %#v err=%v", bindings, err)
	}
}

func e2ePanelPolicyInput(version string, minSamples, minArbitrations int) subjective.CreatePanelPolicyInput {
	return subjective.CreatePanelPolicyInput{
		PolicyVersion: version, EducationStage: "senior", SubjectCode: "physics", ArchetypeCode: "short_constructed",
		DecisionConfig: subjective.PanelDecisionConfig{
			PolicyVersion: version, ScoreGapThreshold: .15, CriterionGapThreshold: .2, ConfidenceGapThreshold: .25,
			ArbiterMinConfidence: .85, ArbiterAgreementThreshold: .8, TriggerAnyCriterionConflict: true,
			TriggerEvidenceConflict: true, HardRiskCodes: []string{"prompt_injection_suspected"},
		},
		ReadinessPolicy: gradingevaluation.PanelReadinessPolicy{
			PolicyVersion: version, MinSamplesPerSlice: minSamples, MinArbitrationsPerSlice: minArbitrations,
			MaxFalseConsensusRate: 0, MaxArbitrationErrorRate: 0, MaxResolvedMAE: 0, MinResolvedQWK: .9,
			MinArbiterMAEImprovement: .1, MinResolvedCoverageRate: 1, MaxHumanEscalationRate: 0,
			MaxArbitrationTriggerRate: .5, MaxCostPer1000AnswersMicros: 0,
		},
	}
}
