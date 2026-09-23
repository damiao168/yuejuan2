package subjective

import (
	"errors"
	"net/http"

	"edugrade-enterprise/services/api-gateway/internal/grading"
	"edugrade-enterprise/services/api-gateway/internal/httpx"
)

func (h *Handler) Grade(w http.ResponseWriter, r *http.Request) {
	user := mustUser(r)
	if !h.requireAvailable(w, r) {
		return
	}
	var input GradeRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := validateIdempotencyKey(input.IdempotencyKey); err != nil {
		writeStoreError(w, r, err)
		return
	}
	policy := NormalizePolicy(input.ModelPolicy)
	if governed, ok := h.adapter.(GovernedPolicyProvider); ok {
		policy = governed.Policy()
	}
	if err := ValidatePolicy(policy); err != nil {
		writeStoreError(w, r, err)
		return
	}
	ctx, err := h.store.LoadContext(r.Context(), user.TenantID, r.PathValue("id"))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if err := h.prepareMathEvidence(r.Context(), user.TenantID, &ctx); err != nil {
		writeStoreError(w, r, err)
		return
	}
	if !IsSupportedQuestionType(ctx.Question.QuestionType) {
		writeStoreError(w, r, ErrUnsupportedQuestionType)
		return
	}
	requestID, err := stableRequestID(user.TenantID, ctx, policy, input.IdempotencyKey)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if input.IdempotencyKey != "" {
		if existing, lookupErr := h.store.GetGradeByAdapterRequestID(r.Context(), user.TenantID, requestID); lookupErr == nil {
			httpx.JSON(w, http.StatusOK, map[string]any{"grade": existing, "idempotent_replay": true})
			return
		} else if !errors.Is(lookupErr, ErrNotFound) {
			writeStoreError(w, r, lookupErr)
			return
		}
	}
	runID := ""
	if run, runErr := h.store.GetOrCreateRun(r.Context(), user.TenantID, user.ID, runInputFor(ctx, "", policy, requestID)); runErr != nil {
		writeStoreError(w, r, runErr)
		return
	} else {
		runID = run.ID
	}
	finishRun := func(status string, grade Grade, errorCode string) error {
		_, updateErr := h.store.UpdateRun(r.Context(), user.TenantID, runID, UpdateRunInput{Status: status, GradeID: grade.ID, ErrorCode: errorCode})
		return updateErr
	}
	decision, allowed, decisionErr := h.decideEligibility(r.Context(), user.TenantID, runID, ctx, policy)
	if decisionErr != nil {
		writeStoreError(w, r, decisionErr)
		return
	}
	if !allowed {
		grade, createErr := h.store.CreateGrade(r.Context(), user.TenantID, user.ID, failedGrade(ctx, policy, runID, "ai_eligibility_abstained", eligibilityAbstentionOutput(policy, decision)))
		if createErr != nil {
			writeStoreError(w, r, createErr)
			return
		}
		if updateErr := finishRun(RunFailed, grade, "ai_eligibility_abstained"); updateErr != nil {
			writeStoreError(w, r, updateErr)
			return
		}
		h.auditAction(r, "subjective.ai_eligibility_abstained", "subjective_grading_run", runID, "external AI was not admitted")
		httpx.JSON(w, http.StatusCreated, map[string]any{"grade": grade, "eligibility_decision": decision})
		return
	}
	promptGuard := InspectPromptInjection(ctx.AnswerText)
	adapterInput, activeAdapter, usedMathV2, err := h.buildAdapterInput(r.Context(), user.TenantID, requestID, ctx, policy, promptGuard, decision.OutputConstraint)
	if err != nil {
		if isMathRevisionConflict(err) {
			_, _ = h.store.UpdateRun(r.Context(), user.TenantID, runID, UpdateRunInput{Status: RunConflict, ErrorCode: "math_evidence_version_conflict"})
			writeStoreError(w, r, err)
			return
		}
		_, _ = h.store.UpdateRun(r.Context(), user.TenantID, runID, UpdateRunInput{Status: RunFailed, ErrorCode: "math_evidence_unavailable"})
		h.auditAction(r, "subjective.math_human_review_required", "subjective_grading_run", runID, "math evidence or active crop is unavailable")
		httpx.JSON(w, http.StatusUnprocessableEntity, mathReviewPayload(ctx, AdapterOutput{}))
		return
	}
	output, err := activeAdapter.Grade(r.Context(), adapterInput)
	output.RequestID = requestID
	if err != nil {
		var agentErr *GradingAgentError
		if errors.As(err, &agentErr) {
			code := "ai_service_unavailable"
			if agentErr.Code == "adapter_not_configured" {
				code = "ai_service_not_configured"
			}
			if _, updateErr := h.store.UpdateRun(r.Context(), user.TenantID, runID, UpdateRunInput{Status: RunFailed, ErrorCode: code}); updateErr != nil {
				writeStoreError(w, r, updateErr)
				return
			}
			h.auditAction(r, "subjective.ai_service_unavailable", "subjective_grading_run", runID, code)
			httpx.Error(w, r, http.StatusServiceUnavailable, code, "AI grading service is unavailable; use manual review")
			return
		}
		ApplyPromptGuard(&output, promptGuard)
		grade, createErr := h.store.CreateGrade(r.Context(), user.TenantID, user.ID, failedGrade(ctx, policy, runID, err.Error(), output))
		if createErr != nil {
			writeStoreError(w, r, createErr)
			return
		}
		if updateErr := finishRun(RunFailed, grade, "adapter_failed"); updateErr != nil {
			writeStoreError(w, r, updateErr)
			return
		}
		h.auditAction(r, "subjective.ai_grade_failed", "ai_grade", grade.ID, "subjective adapter failed")
		httpx.JSON(w, http.StatusCreated, map[string]any{"grade": grade})
		return
	}
	settlement := GradeSettlementInput{TenantID: user.TenantID, RunID: runID, Context: ctx, Policy: policy, Decision: decision, MathRun: usedMathV2}
	if settleErr := h.gradeSettlementPipeline().Settle(r.Context(), settlement, &output); settleErr != nil {
		if usedMathV2 {
			if errors.Is(settleErr, ErrMathHumanReviewRequired) {
				_, _ = h.store.UpdateRun(r.Context(), user.TenantID, runID, UpdateRunInput{Status: RunFailed, ErrorCode: "math_human_review_required"})
				h.auditAction(r, "subjective.math_human_review_required", "subjective_grading_run", runID, "math scoring remained unresolved")
				httpx.JSON(w, http.StatusUnprocessableEntity, mathReviewPayload(ctx, output))
				return
			}
			if isMathRevisionConflict(settleErr) {
				_, _ = h.store.UpdateRun(r.Context(), user.TenantID, runID, UpdateRunInput{Status: RunConflict, ErrorCode: "math_evidence_version_conflict"})
				httpx.Error(w, r, http.StatusConflict, "math_evidence_version_conflict", "math evidence changed during grading")
				return
			}
			_, _ = h.store.UpdateRun(r.Context(), user.TenantID, runID, UpdateRunInput{Status: RunFailed, ErrorCode: "invalid_model_output"})
			writeStoreError(w, r, settleErr)
			return
		}
		if errors.Is(settleErr, ErrGradeCalibration) {
			writeStoreError(w, r, settleErr)
			return
		}
		grade, createErr := h.store.CreateGrade(r.Context(), user.TenantID, user.ID, failedGrade(ctx, policy, runID, settleErr.Error(), output))
		if createErr != nil {
			writeStoreError(w, r, createErr)
			return
		}
		if updateErr := finishRun(RunFailed, grade, "invalid_model_output"); updateErr != nil {
			writeStoreError(w, r, updateErr)
			return
		}
		h.auditAction(r, "subjective.ai_grade_failed", "ai_grade", grade.ID, "subjective adapter output failed schema validation")
		httpx.JSON(w, http.StatusCreated, map[string]any{"grade": grade})
		return
	}
	grade, err := h.store.CreateGrade(r.Context(), user.TenantID, user.ID, successfulGrade(ctx, policy, runID, output))
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	if updateErr := finishRun(RunSucceeded, grade, ""); updateErr != nil {
		writeStoreError(w, r, updateErr)
		return
	}
	h.auditAction(r, "subjective.ai_grade_created", "ai_grade", grade.ID, "create subjective ai grade")
	httpx.JSON(w, http.StatusCreated, map[string]any{"grade": grade})
}

func successfulGrade(ctx Context, policy ModelPolicy, runID string, output AdapterOutput) Grade {
	graderType := LLMGraderType
	if output.Mock {
		graderType = MockGraderType
	}
	grade := Grade{
		AnswerSegmentID:        ctx.SegmentID,
		QuestionID:             ctx.Question.ID,
		QuestionNo:             ctx.Question.QuestionNo,
		QuestionType:           ctx.Question.QuestionType,
		AnswerVersion:          ctx.AnswerVersion,
		GraderType:             graderType,
		ModelVersion:           firstNonEmpty(output.ModelVersion, policy.ModelVersion),
		PromptVersion:          firstNonEmpty(output.PromptVersion, policy.PromptVersion),
		RubricVersion:          firstNonEmpty(output.RubricVersion, ctx.Rubric.Version),
		DeliveryMode:           firstNonEmpty(output.DeliveryMode, "teacher_review"),
		CapabilityProfile:      output.CapabilityProfile,
		AdapterRequestID:       output.RequestID,
		RunID:                  runID,
		AdapterName:            output.Telemetry.Adapter,
		ProviderKey:            output.Telemetry.Provider,
		DeploymentKey:          output.Telemetry.Deployment,
		DeploymentRegion:       output.Telemetry.Region,
		AdapterAttempts:        output.Telemetry.Attempts,
		AdapterLatencyMS:       output.Telemetry.ElapsedMS,
		AdapterRepairAttempted: output.Telemetry.RepairAttempted,
		InputTokens:            output.Telemetry.Usage.InputTokens,
		CachedInputTokens:      output.Telemetry.Usage.CachedInputTokens,
		OutputTokens:           output.Telemetry.Usage.OutputTokens,
		ReasoningTokens:        output.Telemetry.Usage.ReasoningTokens,
		TotalTokens:            output.Telemetry.Usage.TotalTokens,
		SuggestedScore:         output.SuggestedScore,
		MaxScore:               ctx.Question.Score,
		Confidence:             output.Confidence,
		MatchedPoints:          output.MatchedPoints,
		MissingPoints:          output.MissingPoints,
		Evidence:               output.Evidence,
		RiskFlags:              output.RiskFlags,
		NeedsHumanReview:       output.NeedsHumanReview,
		StudentFeedback:        output.StudentFeedback,
		TeacherNote:            output.TeacherNote,
		Mock:                   output.Mock,
		Status:                 "succeeded",
		RawOutput:              output.RawOutput,
	}
	if ctx.MathEvidence != nil {
		grade.MathArtifactID = ctx.MathEvidence.ArtifactID
		grade.MathArtifactVersion = ctx.MathEvidence.ArtifactVersion
		grade.MathCorrectionRevision = ctx.MathEvidence.CorrectionRevision
		grade.MathScoringVersion = ctx.MathEvidence.ScoringVersion
	}
	return grade
}

func failedGrade(ctx Context, policy ModelPolicy, runID string, reason string, output AdapterOutput) Grade {
	riskFlags := appendFlag(output.RiskFlags, "invalid_model_output")
	if output.Mock {
		riskFlags = appendFlag(riskFlags, "mock_llm_output")
	}
	graderType := LLMGraderType
	if output.Mock {
		graderType = MockGraderType
	}
	raw := output.RawOutput
	if raw == nil {
		raw = map[string]any{}
	}
	raw["failure_reason"] = reason
	grade := Grade{
		AnswerSegmentID:        ctx.SegmentID,
		QuestionID:             ctx.Question.ID,
		QuestionNo:             ctx.Question.QuestionNo,
		QuestionType:           ctx.Question.QuestionType,
		AnswerVersion:          ctx.AnswerVersion,
		GraderType:             graderType,
		ModelVersion:           firstNonEmpty(output.ModelVersion, policy.ModelVersion),
		PromptVersion:          firstNonEmpty(output.PromptVersion, policy.PromptVersion),
		RubricVersion:          firstNonEmpty(output.RubricVersion, ctx.Rubric.Version),
		DeliveryMode:           "teacher_review",
		CapabilityProfile:      output.CapabilityProfile,
		AdapterRequestID:       output.RequestID,
		RunID:                  runID,
		AdapterName:            output.Telemetry.Adapter,
		ProviderKey:            output.Telemetry.Provider,
		DeploymentKey:          output.Telemetry.Deployment,
		DeploymentRegion:       output.Telemetry.Region,
		AdapterAttempts:        output.Telemetry.Attempts,
		AdapterLatencyMS:       output.Telemetry.ElapsedMS,
		AdapterRepairAttempted: output.Telemetry.RepairAttempted,
		InputTokens:            output.Telemetry.Usage.InputTokens,
		CachedInputTokens:      output.Telemetry.Usage.CachedInputTokens,
		OutputTokens:           output.Telemetry.Usage.OutputTokens,
		ReasoningTokens:        output.Telemetry.Usage.ReasoningTokens,
		TotalTokens:            output.Telemetry.Usage.TotalTokens,
		SuggestedScore:         0,
		MaxScore:               ctx.Question.Score,
		Confidence:             0,
		MatchedPoints:          []grading.PointResult{},
		MissingPoints:          []grading.PointResult{},
		Evidence:               []grading.Evidence{},
		RiskFlags:              riskFlags,
		NeedsHumanReview:       true,
		StudentFeedback:        "",
		TeacherNote:            "Adapter output failed schema validation; no valid subjective score was produced.",
		Mock:                   output.Mock,
		Status:                 "failed",
		FailureReason:          reason,
		RawOutput:              raw,
	}
	if ctx.MathEvidence != nil {
		grade.MathArtifactID = ctx.MathEvidence.ArtifactID
		grade.MathArtifactVersion = ctx.MathEvidence.ArtifactVersion
		grade.MathCorrectionRevision = ctx.MathEvidence.CorrectionRevision
		grade.MathScoringVersion = ctx.MathEvidence.ScoringVersion
	}
	return grade
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
